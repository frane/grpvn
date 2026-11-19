package internal

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func readStateCall(t *testing.T, h http.Handler, token, method, path string, body any) map[string]any {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code < 200 || w.Code >= 300 {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHumanReadStateFollowsAccountAcrossCachesAndBrowsers(t *testing.T) {
	serverDB := newTestDB(t)
	for _, name := range []string{"person", "other", "agent"} {
		seedTestAgent(t, serverDB, name+"-token", name, []string{"#general", "@*"})
	}
	_, _ = serverDB.Exec("UPDATE principals SET kind='human' WHERE name IN('person','other')")
	serverHandler, err := CoordinatorHandler(serverDB, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "offline", 503)
			return
		}
		serverHandler.ServeHTTP(w, r)
	}))
	defer server.Close()
	person, _ := participantByAddress(serverDB, "person")
	var serverID string
	_ = serverDB.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&serverID)
	type device struct {
		db      *sql.DB
		handler http.Handler
		owner   *Principal
		alias   string
		token   string
		remote  Remote
	}
	newDevice := func(alias, localName string) device {
		db := newTestDB(t)
		token := "local-token-" + alias
		seedTestAgent(t, db, token, localName, []string{"*", "@*"})
		_, _ = db.Exec("UPDATE principals SET kind='human' WHERE name=?", localName)
		owner, e := participantByAddress(db, localName)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO remotes(name,url,server_id) VALUES(?,?,?)", alias, server.URL, serverID); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO local_credentials(server_id,agent_name,credential,principal_id,status) VALUES(?,'','person-token',?,'active')", serverID, person.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO remote_human_sessions(server_id,local_principal_id,principal_id,name,client_id,refresh_token,expires_at) VALUES(?,?,?,'person','','',?)", serverID, owner.ID, person.ID, time.Now().Add(time.Hour).UnixMilli()); e != nil {
			t.Fatal(e)
		}
		handler, e := CoordinatorHandler(db, CoordinatorOptions{LocalUI: true})
		if e != nil {
			t.Fatal(e)
		}
		return device{db: db, handler: handler, owner: owner, alias: alias, token: token, remote: Remote{DB: db, Name: alias, URL: server.URL, ServerID: serverID, Token: "person-token"}}
	}
	a, b := newDevice("work", "laptop-user"), newDevice("desk", "phone-cache")
	if a.owner.ID == b.owner.ID {
		t.Fatal("test devices must have different local profiles")
	}
	ids := []string{}
	for i := 0; i < 5; i++ {
		sent, failed := remoteTool(t, serverHandler, "agent-token", "send", map[string]any{"target": "#general", "body": "An unread update"})
		if failed {
			t.Fatal(sent)
		}
		ids = append(ids, sent["id"].(string))
	}
	mustSync(t, a.db, a.alias)
	mustSync(t, b.db, b.alias)
	localMessage := func(d device, native string) *Message {
		t.Helper()
		var id string
		if err := d.db.QueryRow("SELECT local_id FROM remote_messages WHERE remote_name=? AND remote_id=?", d.alias, native).Scan(&id); err != nil {
			t.Fatal(err)
		}
		m, e := findMessageByID(d.db, id)
		if e != nil {
			t.Fatal(e)
		}
		if m.Seq <= 0 {
			t.Fatal("loaded message lost commit position", m.ID)
		}
		return m
	}
	firstA := localMessage(a, ids[1])
	readStateCall(t, a.handler, a.token, "POST", "/v1/chat/read-markers", map[string]any{"Target": "#work/general", "Position": firstA.Seq})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if err = syncHumanReads(context.Background(), b.db, b.remote, false); err != nil {
		t.Fatal(err)
	}
	nativeSecond, _ := findMessageByID(serverDB, ids[1])
	if position, held := conversationReadPosition(serverDB, person.ID, "#general"); position != nativeSecond.Seq || held {
		t.Fatal("desktop read did not reach account", position, held)
	}
	if position, _ := conversationReadPosition(b.db, b.owner.ID, "#desk/general"); position != localMessage(b, ids[1]).Seq {
		t.Fatal("another device retained separate unread", position)
	}
	// The browser uses the same account, with a different transport/session.
	nativeFourth, _ := findMessageByID(serverDB, ids[3])
	readStateCall(t, serverHandler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Position": nativeFourth.Seq})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if position, _ := conversationReadPosition(a.db, a.owner.ID, "#work/general"); position != localMessage(a, ids[3]).Seq {
		t.Fatal("browser read did not reach desktop", position)
	}
	var agentCursors int
	_ = serverDB.QueryRow("SELECT COUNT(*) FROM cursors WHERE agent_name IN('agent',(SELECT id FROM principals WHERE name='agent'))").Scan(&agentCursors)
	if agentCursors != 0 {
		t.Fatal("human reading advanced agent cursors")
	}
	// A new unread mark on another device survives stale automatic reads.
	nativeFirst, _ := findMessageByID(serverDB, ids[0])
	marked := readStateCall(t, serverHandler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Position": nativeFirst.Seq, "Unread": true})
	version := marked["read_version"].(string)
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	readStateCall(t, a.handler, a.token, "POST", "/v1/chat/read-markers", map[string]any{"Target": "#work/general", "Position": localMessage(a, ids[4]).Seq})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if position, held := conversationReadPosition(serverDB, person.ID, "#general"); !held || position != nativeFirst.Seq {
		t.Fatal("in-flight read erased explicit unread", position, held)
	}
	readStateCall(t, serverHandler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Position": nativeSecond.Seq, "Unread": true})
	readStateCall(t, serverHandler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Position": nativeFourth.Seq, "Resume": true, "ExpectedVersion": version})
	if position, held := conversationReadPosition(serverDB, person.ID, "#general"); !held || position != nativeSecond.Seq {
		t.Fatal("stale resume erased newer unread", position, held)
	}
	readStateCall(t, serverHandler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Resume": true})
	// Offline reads stay queued and merge on reconnection without new profiles.
	mustSync(t, a.db, a.alias)
	offline.Store(true)
	readStateCall(t, a.handler, a.token, "POST", "/v1/chat/read-markers", map[string]any{"Target": "#work/general", "Position": firstA.Seq, "Unread": true})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err == nil {
		t.Fatal("offline read-state push pretended to succeed")
	}
	var pending int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM human_read_outbox").Scan(&pending)
	if pending == 0 {
		t.Fatal("offline unread mark was lost")
	}
	offline.Store(false)
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if err = syncHumanReads(context.Background(), b.db, b.remote, false); err != nil {
		t.Fatal(err)
	}
	if position, held := conversationReadPosition(b.db, b.owner.ID, "#desk/general"); !held || position != localMessage(b, ids[1]).Seq {
		t.Fatal("offline unread mark did not propagate", position, held)
	}
	dm, failed := remoteTool(t, serverHandler, "agent-token", "send", map[string]any{"target": "@person", "body": "A shared DM unread marker"})
	if failed {
		t.Fatal(dm)
	}
	mustSync(t, a.db, a.alias)
	mustSync(t, b.db, b.alias)
	dmA := localMessage(a, dm["id"].(string))
	dmB := localMessage(b, dm["id"].(string))
	badges := readStateCall(t, serverHandler, "person-token", "GET", "/v1/chat/direct-conversations", nil)["read_states"].(map[string]any)
	if badges["@agent"].(map[string]any)["unread"] != float64(1) {
		t.Fatal("lightweight DM state lost the unread badge", badges)
	}
	readStateCall(t, a.handler, a.token, "POST", "/v1/chat/read-markers", map[string]any{"Target": dmA.Target, "Position": dmA.Seq})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if err = syncHumanReads(context.Background(), b.db, b.remote, false); err != nil {
		t.Fatal(err)
	}
	if position, _ := conversationReadPosition(b.db, b.owner.ID, dmB.Target); position < dmB.Seq {
		t.Fatal("DM read marker stayed on one device", position, dmB.Seq)
	}
	badges = readStateCall(t, serverHandler, "person-token", "GET", "/v1/chat/direct-conversations", nil)["read_states"].(map[string]any)
	if state, exists := badges["@agent"]; exists && state.(map[string]any)["unread"] != float64(0) {
		nativeDM, _ := findMessageByID(serverDB, dm["id"].(string))
		position, held := conversationReadPosition(serverDB, person.ID, "@agent")
		var pendingPayload string
		_ = a.db.QueryRow("SELECT payload FROM human_read_outbox WHERE target=?", dmA.Target).Scan(&pendingPayload)
		t.Fatal("lightweight DM badge did not follow the account read", badges, "cursor", position, "held", held, "DM", nativeDM.Seq, nativeDM.Sender, nativeDM.Target, "pending", pendingPayload)
	}
	reply, failed := remoteTool(t, serverHandler, "agent-token", "send", map[string]any{"target": ids[0], "body": "A thread read on another device"})
	if failed {
		t.Fatal(reply)
	}
	mustSync(t, a.db, a.alias)
	mustSync(t, b.db, b.alias)
	rootA := localMessage(a, ids[0])
	rootB := localMessage(b, ids[0])
	replyA := localMessage(a, reply["id"].(string))
	replyB := localMessage(b, reply["id"].(string))
	readStateCall(t, a.handler, a.token, "PATCH", "/v1/chat/threads", map[string]any{"RootID": rootA.ID, "Following": true, "Position": replyA.Seq})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if err = syncHumanReads(context.Background(), b.db, b.remote, false); err != nil {
		t.Fatal(err)
	}
	var threadPosition int64
	var following bool
	if err = b.db.QueryRow("SELECT position,following FROM human_threads WHERE principal_id=? AND root_id=?", b.owner.ID, rootB.ID).Scan(&threadPosition, &following); err != nil || !following || threadPosition < replyB.Seq {
		t.Fatal("thread reads/following stayed on one device", threadPosition, following, err)
	}
	mention, failed := remoteTool(t, serverHandler, "agent-token", "send", map[string]any{"target": "#general", "body": "@person, please check this separately"})
	if failed {
		t.Fatal(mention)
	}
	mustSync(t, a.db, a.alias)
	mustSync(t, b.db, b.alias)
	mentionA := localMessage(a, mention["id"].(string))
	mentionB := localMessage(b, mention["id"].(string))
	readStateCall(t, a.handler, a.token, "GET", "/v1/chat/activity", nil)
	readStateCall(t, a.handler, a.token, "PATCH", "/v1/chat/activity", map[string]any{"IDs": []string{mentionA.ID}})
	if err = syncHumanReads(context.Background(), a.db, a.remote, false); err != nil {
		t.Fatal(err)
	}
	if err = syncHumanReads(context.Background(), b.db, b.remote, false); err != nil {
		t.Fatal(err)
	}
	readStateCall(t, b.handler, b.token, "GET", "/v1/chat/activity", nil)
	var readAt int64
	if err = b.db.QueryRow("SELECT read_at FROM human_inbox WHERE principal_id=? AND message_id=?", b.owner.ID, mentionB.ID).Scan(&readAt); err != nil || readAt == 0 {
		t.Fatal("an inbox read was lost before the other device materialized it", readAt, err)
	}
	// Private events are scoped to the authenticated human, including admins.
	otherPage := readStateCall(t, serverHandler, "other-token", "GET", "/v1/chat/read-markers?after=0", nil)
	for _, raw := range otherPage["changes"].([]any) {
		if raw.(map[string]any)["kind"] != "checkpoint" {
			t.Fatal("another human received private read state", otherPage)
		}
	}
	// Rebuilding the local HTTP session retains the shared cache marker.
	restored, e := CoordinatorHandler(a.db, CoordinatorOptions{LocalUI: true})
	if e != nil {
		t.Fatal(e)
	}
	page := readStateCall(t, restored, a.token, "GET", "/v1/chat/messages?target="+url.QueryEscape("#work/general"), nil)
	if page["read_held"] != true {
		t.Fatal("reconnect lost unread state", page)
	}
}
