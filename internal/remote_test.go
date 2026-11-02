package internal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testCoordinator(t *testing.T, db *sql.DB, token string, channels ...string) *httptest.Server {
	t.Helper()
	if token != "" {
		seedTestAgent(t, db, token, "alice", []string{"*"})
		_, err := db.Exec(`INSERT OR IGNORE INTO invites (id,secret_hash,person,channels,can_write,expires_at,uses_left,created_at)
		VALUES (?,?,?,'["*"]',1,?,1000,?)`, NewMessage("", "", nil).ID, credentialHash(token), "fixture", time.Now().Add(time.Hour).UnixMilli(), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{Channels: channels, Open: token == ""})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func seedTestAgent(t *testing.T, db *sql.DB, credential, name string, channels []string) {
	t.Helper()
	hash := credentialHash(credential)
	if _, err := db.Exec("INSERT OR IGNORE INTO principals (id,credential_hash,name,kind,status,approval_code,created_at) VALUES (?,?,?,'agent','active',?,?)", hash[:32], hash, name, hash[:8], time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, ch := range channels {
		if _, err := db.Exec("INSERT OR IGNORE INTO permissions (principal_id,channel,can_read,can_write) VALUES (?,?,1,1)", hash[:32], ch); err != nil {
			t.Fatal(err)
		}
	}
}

func wireServerID(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var id string
	if err := db.QueryRow("SELECT server_id FROM remotes WHERE name=?", name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustConnect(t *testing.T, db *sql.DB, name, endpoint, token string, channels ...string) {
	t.Helper()
	if err := ConnectRemote(context.Background(), db, name, endpoint, token, channels); err != nil {
		t.Fatal(err)
	}
}

func mustSync(t *testing.T, db *sql.DB, name string) SyncStats {
	t.Helper()
	stats, err := SyncRemotes(context.Background(), db, name)
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func messageIn(t *testing.T, db *sql.DB, target, body string) *Message {
	t.Helper()
	var id string
	if err := db.QueryRow("SELECT id FROM messages WHERE target = ? AND body = ?", target, []byte(body)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	m, err := findMessageByID(db, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRemoteSourcesAndThreads(t *testing.T) {
	ctx := context.Background()
	serverDB := newTestDB(t)
	root, err := Send(serverDB, "alice", "#dev", "review?", "", true)
	if err != nil {
		t.Fatal(err)
	}
	server := testCoordinator(t, serverDB, "secret")
	otherDB := newTestDB(t)
	// Independent coordinators can contain the very same public ID.
	if err := root.Save(otherDB); err != nil {
		t.Fatal(err)
	}
	other := testCoordinator(t, otherDB, "")
	a, b := newTestDB(t), newTestDB(t)
	if _, err := Send(a, "local", "#dev", "local only", "", false); err != nil {
		t.Fatal(err)
	}
	mustConnect(t, a, "work", server.URL, "secret", "#dev", "#ops")
	mustConnect(t, a, "other", other.URL, "", "#dev")
	mustConnect(t, b, "work", server.URL, "secret", "#dev")
	if stats := mustSync(t, a, ""); stats.Pulled != 2 {
		t.Fatalf("stats: %+v", stats)
	}
	mustSync(t, b, "")
	aroot := messageIn(t, a, "#work/dev", "review?")
	otherRoot := messageIn(t, a, "#other/dev", "review?")
	if aroot.ID == otherRoot.ID || aroot.ID == root.ID {
		t.Fatal("remote IDs must be isolated")
	}
	if aroot.Correlation == nil || *aroot.Correlation != aroot.ID {
		t.Fatal("question ID was not translated")
	}
	broot := messageIn(t, b, "#work/dev", "review?")
	reply, _, err := SendSynced(ctx, b, "bob", broot.ID, "looks good", "", false, "reply-1")
	if err != nil || reply.Pending {
		t.Fatalf("send: %+v %v", reply, err)
	}
	mustSync(t, a, "work")
	ar := messageIn(t, a, "#work/dev", "looks good")
	if ar.ParentID == nil || *ar.ParentID != aroot.ID || ar.ChainRoot != aroot.ID || ar.ChainDepth != 1 {
		t.Fatalf("broken thread: %+v", ar)
	}
	var leaked int
	if err := serverDB.QueryRow("SELECT COUNT(*) FROM messages WHERE body = ?", []byte("local only")).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("local channel leaked: %d %v", leaked, err)
	}
	if stats := mustSync(t, a, ""); stats.Pulled != 0 {
		t.Fatalf("repeated sync duplicated messages: %+v", stats)
	}
	var unread bytes.Buffer
	st := &State{Name: "reader", Follow: []string{"#work/dev", "#other/dev"}}
	if _, err := Read(&unread, a, st, 0, true, false, true, false, "never", "#work/dev"); err != nil {
		t.Fatal(err)
	}
	unread.Reset()
	if _, err := Check(&unread, a, st); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(unread.String(), "#work/dev") || !strings.Contains(unread.String(), "1 #other/dev") {
		t.Fatalf("read positions mixed sources: %s", &unread)
	}
}

func TestRemoteOutboxRetriesAndRetention(t *testing.T) {
	ctx := context.Background()
	serverDB := newTestDB(t)
	server := testCoordinator(t, serverDB, "secret")
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, "secret", "#dev")
	server.Close()
	msg, _, err := SendSynced(ctx, client, "alice", "#work/dev", "offline", "", true, "retry-me")
	if err != nil || !msg.Pending || !strings.HasSuffix(FormatSendAck(msg, false), " queued") {
		t.Fatalf("offline send: %+v %v", msg, err)
	}
	if _, err := client.Exec("UPDATE messages SET created_at = ? WHERE id = ?", time.Now().Add(-72*time.Hour).UnixMilli(), msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := Gc(io.Discard, client, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if _, err := findMessageByID(client, msg.ID); err != nil {
		t.Fatal("gc discarded pending send:", err)
	}
	// Restart the coordinator on another address, backed by the same DB.
	restarted := testCoordinator(t, serverDB, "secret")
	mustConnect(t, client, "work", restarted.URL, "", "#dev")
	wire, _, err := nextOutgoing(client, "work")
	if err != nil || wire == nil {
		t.Fatalf("lost outbox: %v", err)
	}
	// Simulate a committed upload whose HTTP response was lost. The local
	// acknowledgement stays absent; a retry must reuse the original row.
	rootCredential, err := localCredential(client, wireServerID(t, client, "work"), "")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := getRemote(client, "work")
	if err != nil {
		t.Fatal(err)
	}
	remote.Token = rootCredential.Secret
	agent, err := agentRemote(ctx, client, *remote, wire.Sender, wire.Target)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := findPrincipal(serverDB, "credential_hash=?", credentialHash(agent.Token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acceptAttributedMessage(serverDB, *wire, principal); err != nil {
		t.Fatal(err)
	}
	if stats := mustSync(t, client, "work"); stats.Pushed != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	replayed, replay, err := SendSynced(ctx, client, "alice", "#work/dev", "offline", "", true, "retry-me")
	if err != nil || !replay || replayed.ID != msg.ID || replayed.Pending {
		t.Fatalf("retry: %+v %v %v", replayed, replay, err)
	}
	var count int
	if err := serverDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate upload: %d %v", count, err)
	}
	if err := Gc(io.Discard, client, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if _, err := findMessageByID(client, msg.ID); err != sql.ErrNoRows {
		t.Fatalf("delivered message not pruned: %v", err)
	}
}

func TestRemotePaginationAndAtomicCursor(t *testing.T) {
	serverDB := newTestDB(t)
	for i := 0; i < syncPageSize+7; i++ {
		if _, err := Send(serverDB, "alice", "#dev", fmt.Sprint(i), "", false); err != nil {
			t.Fatal(err)
		}
	}
	server := testCoordinator(t, serverDB, "")
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, "", "#dev", "#empty")
	if stats := mustSync(t, client, ""); stats.Pulled != syncPageSize+7 {
		t.Fatalf("pagination: %+v", stats)
	}
	channels, err := RemoteChannels(client, "work")
	if err != nil {
		t.Fatal(err)
	}
	ch := channels[0]
	invalid := toWire(NewMessage("bob", "#dev", []byte("bad")))
	invalid.Seq = ch.Position + 1
	page := messagePage{Messages: []wireMessage{invalid}, Cursor: invalid.Seq + 1}
	if _, err := importPage(client, "work", ch, page); err == nil {
		t.Fatal("accepted a cursor beyond the stored page")
	}
	after, err := RemoteChannels(client, "work")
	if err != nil || after[0].Position != ch.Position {
		t.Fatalf("failed page advanced cursor: %+v %v", after, err)
	}
	// A restored connection keeps the durable positions instead of fetching
	// history again, including an independent zero position for an empty channel.
	mustConnect(t, client, "work", server.URL, "", "#dev", "#empty")
	if stats := mustSync(t, client, ""); stats.Pulled != 0 {
		t.Fatalf("reconnect replayed history: %+v", stats)
	}
}

func TestCoordinatorAuthAndIsolation(t *testing.T) {
	db := newTestDB(t)
	for _, target := range []string{"#dev", "#private", "@alice"} {
		if _, err := Send(db, "bob", target, target, "", false); err != nil {
			t.Fatal(err)
		}
	}
	server := testCoordinator(t, db, "secret", "#dev")
	for _, path := range []string{"/v1/channels", "/v1/messages?channel=%23dev"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", path, resp.StatusCode)
		}
	}
	r := Remote{URL: server.URL, Token: "secret"}
	var page messagePage
	if err := remoteRequest(context.Background(), r, "GET", "/v1/messages?channel=%40alice", nil, &page); err == nil {
		t.Fatal("API exposed DMs")
	}
	if err := remoteRequest(context.Background(), r, "GET", "/v1/messages?channel=%23private", nil, &page); err == nil {
		t.Fatal("API exposed excluded channel")
	}
	var channels struct {
		Channels []string `json:"channels"`
	}
	if err := remoteRequest(context.Background(), r, "GET", "/v1/channels", nil, &channels); err != nil {
		t.Fatal(err)
	}
	if len(channels.Channels) != 1 || channels.Channels[0] != "#dev" {
		t.Fatalf("channels: %+v", channels)
	}
	wire := toWire(NewMessage("alice", "#dev", []byte("hello")))
	var ack wireMessage
	if err := remoteRequest(context.Background(), r, "POST", "/v1/messages", wire, &ack); err != nil {
		t.Fatal(err)
	}
	wire.Body = "different"
	if err := remoteRequest(context.Background(), r, "POST", "/v1/messages", wire, &ack); err == nil {
		t.Fatal("accepted changed payload for same ID")
	}
	client := newTestDB(t)
	if err := ConnectRemote(context.Background(), client, "work", server.URL, "wrong", []string{"#dev"}); err == nil {
		t.Fatal("connected with wrong token")
	}
	remotes, err := ListRemotes(client)
	if err != nil || len(remotes) != 0 {
		t.Fatal("failed connection persisted")
	}
	mustConnect(t, client, "work", server.URL, "secret", "#dev")
	mustSync(t, client, "work")
	// A client that is also a coordinator must not re-export its remote cache.
	clientServer := testCoordinator(t, client, "")
	if err := remoteRequest(context.Background(), Remote{URL: clientServer.URL}, "GET", "/v1/channels", nil, &channels); err != nil || len(channels.Channels) != 0 {
		t.Fatalf("remote cache re-exported: %+v %v", channels, err)
	}
	other := testCoordinator(t, newTestDB(t), "")
	if err := ConnectRemote(context.Background(), client, "work", other.URL, "", []string{"#dev"}); err == nil {
		t.Fatal("alias accepted a different coordinator")
	}
	var listing bytes.Buffer
	if err := PrintRemotes(&listing, client); err != nil || strings.Contains(listing.String(), "secret") {
		t.Fatalf("listing leaks credentials: %s %v", &listing, err)
	}
}

func TestConcurrentRemoteSync(t *testing.T) {
	serverDB := newTestDB(t)
	server := testCoordinator(t, serverDB, "")
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, "", "#dev")
	for i := 0; i < 12; i++ {
		if _, err := Send(client, "alice", "#work/dev", fmt.Sprint(i), "", false); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Go(func() {
			if _, err := SyncRemotes(context.Background(), client, ""); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var count int
	if err := serverDB.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 12 {
		t.Fatalf("concurrent duplicates: %d %v", count, err)
	}
	if pending, err := PendingRemoteCount(client, "work"); err != nil || pending != 0 {
		t.Fatalf("pending: %d %v", pending, err)
	}
}

func TestRemoteWaitPullsNewMessages(t *testing.T) {
	serverDB := newTestDB(t)
	server := testCoordinator(t, serverDB, "")
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, "", "#dev")
	st := &State{Name: "bob", Follow: []string{"#work/dev"}}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = Send(serverDB, "alice", "#dev", "new remote message", "", false)
	}()
	var out bytes.Buffer
	code, err := Wait(context.Background(), &out, client, func() (*State, error) { return st, nil }, 4*time.Second, 10*time.Millisecond)
	if err != nil || code != 0 || !strings.Contains(out.String(), "1 #work/dev") {
		t.Fatalf("wait: code=%d err=%v out=%q", code, err, &out)
	}
}

func TestCoordinatorRejectsMalformedThreads(t *testing.T) {
	db := newTestDB(t)
	root, _ := Send(db, "alice", "#dev", "root", "", false)
	server := testCoordinator(t, db, "")
	bad := toWire(NewMessage("bob", "#other", []byte("cross-channel")))
	bad.ParentID, bad.ChainRoot, bad.ChainDepth = &root.ID, root.ID, 1
	var ack wireMessage
	if err := remoteRequest(context.Background(), Remote{URL: server.URL}, "POST", "/v1/messages", bad, &ack); err == nil {
		t.Fatal("accepted cross-channel reply")
	}
	good := toWire(NewMessage("bob", "#dev", []byte("ok")))
	data, _ := json.Marshal(good)
	resp, err := http.Post(server.URL+"/v1/messages", "application/json", strings.NewReader(string(data)+" {}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("accepted trailing JSON: %d", resp.StatusCode)
	}
}

func TestRemoteFailureIsolation(t *testing.T) {
	firstDB, secondDB := newTestDB(t), newTestDB(t)
	for _, db := range []*sql.DB{firstDB, secondDB} {
		if _, err := Send(db, "alice", "#dev", "available", "", false); err != nil {
			t.Fatal(err)
		}
	}
	first := testCoordinator(t, firstDB, "", "#dev")
	second := testCoordinator(t, secondDB, "")
	client := newTestDB(t)
	mustConnect(t, client, "first", first.URL, "", "#aaa-denied", "#dev")
	mustConnect(t, client, "second", second.URL, "", "#dev")
	offline := testCoordinator(t, newTestDB(t), "")
	mustConnect(t, client, "offline", offline.URL, "", "#dev")
	// Give one alias an unreachable endpoint without changing the healthy
	// coordinator used by another alias.
	if _, err := client.Exec("UPDATE remotes SET url = 'http://127.0.0.1:1' WHERE name = 'offline'"); err != nil {
		t.Fatal(err)
	}
	stats, err := SyncRemotes(context.Background(), client, "")
	if err == nil || stats.Pulled != 2 {
		t.Fatalf("healthy channels did not sync independently: %+v %v", stats, err)
	}
	messageIn(t, client, "#first/dev", "available")
	messageIn(t, client, "#second/dev", "available")
	if _, err := Send(client, "invalid sender", "#first/dev", "bad", "", false); err == nil {
		t.Fatal("invalid remote payload entered the outbox")
	}
	var count int
	if err := client.QueryRow("SELECT COUNT(*) FROM messages WHERE body = ?", []byte("bad")).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed remote send was partially committed: %d %v", count, err)
	}
}

func TestImportPageRollbackAndDurablePosition(t *testing.T) {
	server := testCoordinator(t, newTestDB(t), "")
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, "", "#dev")
	pending, err := Send(client, "bob", "#work/dev", "original", "", false)
	if err != nil {
		t.Fatal(err)
	}
	first := toWire(NewMessage("alice", "#dev", []byte("new")))
	first.Seq = 1
	conflict := toWire(pending)
	conflict.Target, conflict.Body, conflict.Seq = "#dev", "changed by coordinator", 2
	channels, err := RemoteChannels(client, "work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importPage(client, "work", channels[0], messagePage{Messages: []wireMessage{first, conflict}, Cursor: 2}); err == nil {
		t.Fatal("accepted conflicting pending payload")
	}
	var count int
	if err := client.QueryRow("SELECT COUNT(*) FROM remote_messages WHERE remote_id = ?", first.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial page survived rollback: %d %v", count, err)
	}
	if _, err := importPage(client, "work", channels[0], messagePage{Messages: []wireMessage{first}, Cursor: 1}); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	positions, err := RemoteChannels(reopened, "work")
	if err != nil || positions[0].Position != 1 {
		t.Fatalf("sync position not durable: %+v %v", positions, err)
	}
	messageIn(t, reopened, "#work/dev", "new")
}

func TestMigrateV2PreservesLocalLog(t *testing.T) {
	db := newTestDB(t)
	msg, err := Send(db, "alice", "#local", "existing", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := advanceCursor(db, "bob", "#local", msg.Seq); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"remote_messages", "remote_channels", "remotes", "server_meta", "daemon_state"} {
		if _, err := db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("DELETE FROM schema_version"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schema_version (version, applied_at) VALUES (2, 1)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := db.QueryRow("SELECT seq FROM messages WHERE id = ?", msg.ID).Scan(&seq); err != nil || seq != msg.Seq {
		t.Fatalf("local sequence changed: %d %v", seq, err)
	}
	positions, err := loadCursors(db, "bob")
	if err != nil || positions["#local"] != msg.Seq {
		t.Fatalf("local read position changed: %+v %v", positions, err)
	}
	if version, err := SchemaVersion(db); err != nil || version != CurrentSchemaVersion {
		t.Fatalf("version=%d %v", version, err)
	}
}
