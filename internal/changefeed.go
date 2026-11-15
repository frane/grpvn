package internal

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/lib/pq"
)

type chatRevision struct{ Sequence, Metadata, Personal int64 }

func readChatRevision(ctx context.Context, db *sql.DB) (chatRevision, error) {
	var revision chatRevision
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0),COALESCE((SELECT value FROM chat_metadata WHERE key='revision'),0),COALESCE((SELECT SUM(value) FROM chat_metadata WHERE key LIKE 'personal:%'),0) FROM messages").Scan(&revision.Sequence, &revision.Metadata, &revision.Personal)
	return revision, err
}

type webFeed struct {
	mu        sync.Mutex
	listeners map[chan struct{}]bool
	cancel    context.CancelFunc
}

var webFeeds sync.Map
var webFeedRegistry sync.Mutex

// One watcher is shared by every UI and sync stream on this database. SQLite
// uses OS notifications on its directory (including WAL creation/replacement),
// so commits from other processes wake the feed too. PostgreSQL uses LISTEN.
// Notifications are hints: only committed sequence/revision changes publish.
func databaseSignals(ctx context.Context, db *sql.DB) <-chan struct{} {
	signals := make(chan struct{}, 1)
	signal := func() {
		select {
		case signals <- struct{}{}:
		default:
		}
	}
	go func() {
		if isPostgres(db) {
			conn, err := db.Conn(ctx)
			if err != nil {
				return
			}
			var dsn string
			err = conn.Raw(func(raw any) error { dsn = raw.(*postgresConn).dsn; return nil })
			conn.Close()
			if err != nil {
				return
			}
			listener := pq.NewListener(dsn, time.Second, 30*time.Second, nil)
			defer listener.Close()
			closed := make(chan struct{})
			defer close(closed)
			go func() {
				select {
				case <-ctx.Done():
					_ = listener.Close()
				case <-closed:
				}
			}()
			if listener.Listen("grpvn_messages") != nil {
				return
			}
			signal()
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-listener.Notify:
					if !ok {
						return
					}
					signal()
				}
			}
		}
		var seq int
		var name, path string
		if db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path) != nil || path == "" {
			return
		}
		watchSQLiteFiles(ctx, path, signal)
	}()
	return signals
}

func subscribeWeb(db *sql.DB) (chan struct{}, func()) {
	webFeedRegistry.Lock()
	value, _ := webFeeds.LoadOrStore(db, &webFeed{listeners: map[chan struct{}]bool{}})
	feed := value.(*webFeed)
	ch := make(chan struct{}, 1)
	feed.mu.Lock()
	feed.listeners[ch] = true
	if feed.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		feed.cancel = cancel
		go func() {
			signals := databaseSignals(ctx, db)
			safety := time.NewTicker(2 * time.Second)
			defer safety.Stop()
			last := chatRevision{Sequence: -1, Metadata: -1}
			check := func() {
				revision, err := readChatRevision(ctx, db)
				if err != nil || revision == last {
					return
				}
				last = revision
				feed.mu.Lock()
				defer feed.mu.Unlock()
				for c := range feed.listeners {
					select {
					case c <- struct{}{}:
					default:
					}
				}
			}
			check()
			for {
				select {
				case <-ctx.Done():
					return
				case <-signals:
					// Briefly coalesce WAL writes; read only after the transaction commits.
					timer := time.NewTimer(8 * time.Millisecond)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
					check()
				case <-safety.C:
					check()
				}
			}
		}()
	}
	feed.mu.Unlock()
	webFeedRegistry.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			webFeedRegistry.Lock()
			defer webFeedRegistry.Unlock()
			feed.mu.Lock()
			defer feed.mu.Unlock()
			delete(feed.listeners, ch)
			if len(feed.listeners) == 0 && feed.cancel != nil {
				feed.cancel()
				feed.cancel = nil
				webFeeds.Delete(db)
			}
		})
	}
}
