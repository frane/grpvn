package internal

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// A daemon owns one stream per coordinator, shared across its aliases. Streams
// only wake the existing durable sync engine: leases, cursors, deduplication,
// pacing and Retry-After still apply. Older servers keep paced polling.
func runRemoteEvents(ctx context.Context, db *sql.DB, wake chan<- struct{}) {
	type running struct {
		signature string
		cancel    context.CancelFunc
		done      <-chan struct{}
	}
	active := map[string]running{}
	var workers sync.WaitGroup
	defer func() {
		for _, watch := range active {
			watch.cancel()
		}
		workers.Wait()
	}()
	refresh := func() {
		remotes, err := ListRemotes(db)
		if err != nil {
			return
		}
		type eventSource struct {
			remote            Remote
			aliases, channels []string
		}
		sources := map[string]*eventSource{}
		for _, remote := range remotes {
			channels, err := RemoteChannels(db, remote.Name)
			if err != nil {
				continue
			}
			credential, err := localCredential(db, remote.ServerID, "")
			if err != nil || credential.Secret == "" {
				continue
			}
			source := sources[remote.ServerID]
			if source == nil {
				remote.DB = db
				remote.Token = credential.Secret
				source = &eventSource{remote: remote}
				sources[remote.ServerID] = source
			}
			source.aliases = append(source.aliases, remote.Name)
			var humanOwner string
			_ = db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&humanOwner)
			for _, channel := range channels {
				if humanOwner != "" && !channelJoined(db, humanOwner, channel.Target) {
					continue
				}
				source.channels = append(source.channels, channel.Channel)
			}
		}
		for id, source := range sources {
			sort.Strings(source.aliases)
			sort.Strings(source.channels)
			source.channels = slices.Compact(source.channels)
			signature := source.remote.URL + "\x00" + source.remote.Token + "\x00" + strings.Join(source.aliases, ",") + "\x00" + strings.Join(source.channels, ",")
			if watch, ok := active[id]; ok && watch.signature == signature {
				continue
			}
			if watch, ok := active[id]; ok {
				watch.cancel()
				<-watch.done
			}
			run, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			active[id] = running{signature, cancel, done}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer close(done)
				watchRemoteEvents(run, db, source.remote, source.aliases, source.channels, wake)
			}()
		}
		for id, watch := range active {
			if sources[id] == nil {
				watch.cancel()
				delete(active, id)
			}
		}
	}
	refresh()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

var eventHTTPClient = &http.Client{
	Transport: func() *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = 10 * time.Second
		return transport
	}(),
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("coordinator redirects are not supported")
	},
}

func watchRemoteEvents(ctx context.Context, db *sql.DB, remote Remote, aliases, channels []string, wake chan<- struct{}) {
	delay := time.Second
	notify := func(metadata bool) {
		if metadata {
			_, _ = db.ExecContext(ctx, "UPDATE chat_metadata SET value=0 WHERE key=?", "remote:"+remote.ServerID)
			_ = TouchChatMetadata(db)
		}
		for _, alias := range aliases {
			_ = wakeRemote(db, alias)
		}
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	for ctx.Err() == nil {
		// A push reconnect must not bypass a coordinator's persisted cooldown.
		var retry int64
		_ = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(s.retry_at),0) FROM sync_schedule s JOIN remotes r ON r.name=s.remote_name WHERE r.server_id=?", remote.ServerID).Scan(&retry)
		if remaining := time.Until(time.UnixMilli(retry)); remaining > 0 {
			if !waitForPush(ctx, remaining) {
				return
			}
		}
		err := remoteRequestSlot(ctx, remote)
		var retryAt time.Time
		status := 0
		if err == nil {
			query := url.Values{}
			if len(channels) == 0 {
				query.Set("metadata_only", "1")
			}
			for _, channel := range channels {
				query.Add("channel", channel)
			}
			endpoint := "/v1/events"
			var human bool
			_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM remote_human_sessions WHERE server_id=?)", remote.ServerID).Scan(&human)
			if human {
				endpoint = "/v1/chat/events"
			}
			request, _ := http.NewRequestWithContext(ctx, "GET", remote.URL+endpoint+"?"+query.Encode(), nil)
			request.Header.Set("Authorization", "Bearer "+remote.Token)
			request.Header.Set("X-Grpvn-Server", remote.ServerID)
			request.Header.Set("Accept", "text/event-stream")
			response, requestErr := eventHTTPClient.Do(request)
			err = requestErr
			if err == nil {
				status = response.StatusCode
				retryAt = retryAfter(response.Header.Get("Retry-After"), time.Now())
				if status == 200 && strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
					connectedAt := time.Now()
					scanner := bufio.NewScanner(response.Body)
					scanner.Buffer(make([]byte, 4096), 64*1024)
					event := ""
					data := ""
					for scanner.Scan() {
						line := scanner.Text()
						if strings.HasPrefix(line, "event:") {
							event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
						}
						if strings.HasPrefix(line, "data:") {
							data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
						}
						if line == "" {
							if event == "connected" {
								notify(false)
							}
							if event == "changed" {
								var change struct {
									Metadata bool `json:"metadata"`
								}
								_ = json.Unmarshal([]byte(data), &change)
								notify(change.Metadata)
							}
							if event == "signed-out" {
								status = 401
								break
							}
							event = ""
							data = ""
						}
					}
					err = scanner.Err()
					if time.Since(connectedAt) > 30*time.Second {
						delay = time.Second
					}
				}
				response.Body.Close()
			}
		}
		if ctx.Err() != nil {
			return
		}
		if retryAt.After(time.Now()) {
			_, _ = db.ExecContext(ctx, "UPDATE sync_peers SET next_request_at=MAX(next_request_at,?) WHERE server_id=?", retryAt.UnixMilli(), remote.ServerID)
			_, _ = db.ExecContext(ctx, "UPDATE sync_schedule SET retry_at=MAX(retry_at,?),next_at=MAX(next_at,?) WHERE remote_name IN(SELECT name FROM remotes WHERE server_id=?)", retryAt.UnixMilli(), retryAt.UnixMilli(), remote.ServerID)
		}
		if status == 404 || status == 401 || status == 403 || status == 409 {
			delay = maxFailureInterval
		}
		if remaining := time.Until(retryAt); remaining > delay {
			delay = remaining
		}
		if !waitForPush(ctx, delay) {
			return
		}
		delay = backoff(delay, 1, maxFailureInterval)
	}
}
func waitForPush(ctx context.Context, delay time.Duration) bool {
	// Positive jitter avoids reconnecting all clients simultaneously.
	delay += time.Duration(time.Now().UnixNano() % int64(delay/5+1))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
