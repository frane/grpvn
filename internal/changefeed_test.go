package internal

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type eventFrame struct {
	Name string
	Data string
	At   time.Time
}

func readEventFrames(ctx context.Context, response *http.Response) <-chan eventFrame {
	frames := make(chan eventFrame, 32)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(response.Body)
		var frame eventFrame
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event:") {
				frame.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				frame.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
			if line == "" && frame.Name != "" {
				frame.At = time.Now()
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				}
				frame = eventFrame{}
			}
		}
	}()
	return frames
}
func nextEvent(t *testing.T, ctx context.Context, frames <-chan eventFrame, name string) eventFrame {
	t.Helper()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("live stream closed")
			}
			if frame.Name == name {
				return frame
			}
		case <-ctx.Done():
			t.Fatal("live stream timed out", ctx.Err())
		}
	}
}

func TestWebMessageDeltaKeepsOrderAndAuthorization(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "reader", "reef", []string{"#dev", "@*"})
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	old, err := Send(db, "other", "#dev", "old history", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		if _, err = Send(db, "other", "#dev", fmt.Sprint(i), "", false); err != nil {
			t.Fatal(err)
		}
	}
	request := func(query string) (int, struct {
		Messages []struct {
			Seq  int64  `json:"seq"`
			Body string `json:"body"`
		}
		More bool
	}) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/chat/messages?"+query, nil)
		r.Header.Set("Authorization", "Bearer reader")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result struct {
			Messages []struct {
				Seq  int64  `json:"seq"`
				Body string `json:"body"`
			}
			More bool
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return w.Code, result
	}
	code, page := request(fmt.Sprintf("target=%%23dev&after=%d", old.Seq))
	if code != 200 || len(page.Messages) != 100 || !page.More || page.Messages[0].Body != "0" || page.Messages[99].Body != "99" {
		t.Fatalf("invalid first delta: %d %+v", code, page)
	}
	code, last := request(fmt.Sprintf("target=%%23dev&after=%d", page.Messages[99].Seq))
	if code != 200 || len(last.Messages) != 5 || last.More || last.Messages[0].Body != "100" {
		t.Fatalf("invalid last delta: %d %+v", code, last)
	}
	if code, _ := request("target=%23secret&after=0"); code != 403 {
		t.Fatal("delta bypassed channel permission", code)
	}
	for _, query := range []string{"target=%23dev&after=-1", "target=%23dev&after=bad", "target=%23dev&after=1&before=2"} {
		if code, _ := request(query); code != 400 {
			t.Fatal("accepted invalid cursor", query, code)
		}
	}
}

func TestCoordinatorPushIsPromptFilteredAndRevocable(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "reader", "reef", []string{"#dev", "@*"})
	h, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/events?channel=%23dev", nil)
	r.Header.Set("Authorization", "Bearer reader")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	frames := readEventFrames(ctx, response)
	nextEvent(t, ctx, frames, "connected")
	// Cross-user DMs and private channels must never appear in event payloads.
	_, _ = Send(db, "alice", "@bob", "private", "", false)
	_, _ = Send(db, "alice", "#secret", "private", "", false)
	var samples []time.Duration
	for i := 0; i < 12; i++ {
		started := time.Now()
		if _, err := Send(db, "peer", "#dev", fmt.Sprint(i), "", false); err != nil {
			t.Fatal(err)
		}
		frame := nextEvent(t, ctx, frames, "changed")
		samples = append(samples, frame.At.Sub(started))
		var change struct {
			Targets  []string `json:"targets"`
			Metadata bool     `json:"metadata"`
		}
		if json.Unmarshal([]byte(frame.Data), &change) != nil || len(change.Targets) != 1 || change.Targets[0] != "#dev" || change.Metadata {
			t.Fatal("event leaked an unrelated conversation or forced metadata reload", frame.Data)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("commit-to-SSE: median=%s p95=%s", samples[len(samples)/2], samples[len(samples)-1])
	if samples[len(samples)-1] > 750*time.Millisecond {
		t.Fatal("push delivery is no longer interactive", samples)
	}
	if _, err := db.Exec("UPDATE principals SET status='revoked' WHERE name='reef'"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, ctx, frames, "signed-out")
}

func TestSQLiteFeedObservesOtherConnectionsAndIgnoresRollback(t *testing.T) {
	db := newTestDB(t)
	if isPostgres(db) {
		t.Skip("SQLite cross-process path")
	}
	var sequence int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ch, closeFeed := subscribeWeb(db)
	defer closeFeed()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("feed did not initialize")
	}
	started := time.Now()
	if _, err = Send(other, "peer", "#dev", "another process connection", "", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
		t.Log("cross-connection commit-to-wake", time.Since(started))
	case <-time.After(750 * time.Millisecond):
		t.Fatal("cross-connection change waited for safety polling")
	}
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := TouchChatMetadata(tx); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	select {
	case <-ch:
		t.Fatal("rolled-back metadata change was published")
	case <-time.After(60 * time.Millisecond):
	}
}

func TestPushWakesIdleDaemonWithoutDuplicateStreams(t *testing.T) {
	serverDB := newTestDB(t)
	seedTestAgent(t, serverDB, "push-token", "alice", []string{"*"})
	// ConnectRemote's invitation flow uses the existing test coordinator fixture.
	fixture := testCoordinator(t, serverDB, "push-token")
	var streams atomic.Int32
	wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" {
			streams.Add(1)
		}
		fixture.Config.Handler.ServeHTTP(w, r)
	}))
	defer wrapped.Close()
	clientDB := newTestDB(t)
	mustConnect(t, clientDB, "work", wrapped.URL, "push-token", "#dev")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunDaemon(ctx, clientDB, time.Second, nil) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for streams.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if streams.Load() != 1 {
		t.Fatal("daemon did not establish one shared stream", streams.Load())
	}
	// Let the connected catch-up finish, then impose a long idle delay.
	time.Sleep(400 * time.Millisecond)
	_, _ = clientDB.Exec("UPDATE sync_schedule SET next_at=?", time.Now().Add(time.Hour).UnixMilli())
	started := time.Now()
	if _, err := Send(serverDB, "peer", "#dev", "after a long idle", "", false); err != nil {
		t.Fatal(err)
	}
	var count int
	for time.Since(started) < 2*time.Second {
		_ = clientDB.QueryRow("SELECT COUNT(*) FROM messages WHERE target='#work/dev'").Scan(&count)
		if count > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if count != 1 {
		t.Fatal("push failed to wake the idle durable sync worker")
	}
	t.Log("remote commit-to-cached delivery", time.Since(started))
	if streams.Load() != 1 {
		t.Fatal("extra streams were opened", streams.Load())
	}
}

func TestPushCooldownSurvivesConcurrentSyncCompletion(t *testing.T) {
	db := newTestDB(t)
	remote := Remote{Name: "work", URL: "https://example.test", ServerID: "server", DB: db}
	if _, err := db.Exec("INSERT INTO remotes(name,url,server_id) VALUES(?,?,?)", remote.Name, remote.URL, remote.ServerID); err != nil {
		t.Fatal(err)
	}
	owner, claimed, err := claimSync(db, remote, true, time.Now())
	if err != nil || !claimed {
		t.Fatal(err, claimed)
	}
	retry := time.Now().Add(time.Minute).UnixMilli()
	if _, err := db.Exec("UPDATE sync_schedule SET retry_at=? WHERE remote_name=?", retry, remote.Name); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec("UPDATE sync_peers SET next_request_at=? WHERE server_id=?", retry, remote.ServerID)
	if err := finishSync(db, remote, owner, time.Second, SyncStats{Pulled: 1}, nil); err != nil {
		t.Fatal(err)
	}
	var persisted int64
	if err := db.QueryRow("SELECT retry_at FROM sync_schedule WHERE remote_name=?", remote.Name).Scan(&persisted); err != nil || persisted != retry {
		t.Fatal("successful sync erased Retry-After", persisted, retry, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := remoteRequestSlot(ctx, remote); err == nil {
		t.Fatal("another request bypassed the shared coordinator cooldown")
	}
}
