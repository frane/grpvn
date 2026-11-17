package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopRemoteDMsCacheReplyThreadsAndIsolateAccounts(t *testing.T) {
	serverDB := newTestDB(t)
	for _, name := range []string{"remote-human", "remote-agent", "other-human"} {
		seedTestAgent(t, serverDB, name+"-token", name, []string{"#general", "@*"})
	}
	if _, err := serverDB.Exec("UPDATE principals SET kind='human' WHERE name IN('remote-human','other-human')"); err != nil {
		t.Fatal(err)
	}
	handler, err := CoordinatorHandler(serverDB, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "temporarily offline", 503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	var serverID string
	if err = serverDB.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&serverID); err != nil {
		t.Fatal(err)
	}
	remoteHuman, err := participantByAddress(serverDB, "remote-human")
	if err != nil {
		t.Fatal(err)
	}
	db := newTestDB(t)
	seedTestAgent(t, db, "local-token", "local-human", []string{"*", "@*"})
	seedTestAgent(t, db, "another-token", "another-human", []string{"*", "@*"})
	if _, err = db.Exec("UPDATE principals SET kind='human'"); err != nil {
		t.Fatal(err)
	}
	localHuman, err := participantByAddress(db, "local-human")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO remotes(name,url,server_id) VALUES('work',?,?)", server.URL, serverID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO local_credentials(server_id,agent_name,credential,principal_id,status) VALUES(?,'','remote-human-token',?,'active')", serverID, remoteHuman.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO remote_human_sessions(server_id,local_principal_id,principal_id,name,client_id,refresh_token,expires_at) VALUES(?,?,?,'remote-human','','',?)", serverID, localHuman.ID, remoteHuman.ID, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	localHandler, err := CoordinatorHandler(db, CoordinatorOptions{LocalUI: true})
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		localHandler.ServeHTTP(response, request)
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(response.Code, response.Body.String())
		}
		return response.Code, result
	}
	fileCode, file := uploadTestFile(t, http.DefaultClient, server.URL, "@remote-human", "remote-agent-token", "private.txt", []byte("The original private attachment"))
	if fileCode != 201 {
		t.Fatal(fileCode, file)
	}
	incomingBody := "An incoming remote DM [private.txt](grpvn-file:" + file["id"].(string) + ")"
	posted, failed := remoteTool(t, handler, "remote-agent-token", "send", map[string]any{"target": "@remote-human", "body": incomingBody})
	if failed {
		t.Fatal(posted)
	}
	if _, failed := remoteTool(t, handler, "remote-agent-token", "send", map[string]any{"target": "@other-human", "body": "A private DM for someone else"}); failed {
		t.Fatal("fixture send failed")
	}
	if _, err = SyncRemotes(context.Background(), db, "work"); err != nil {
		t.Fatal(err)
	}
	code, list := call("local-token", "GET", "/v1/chat/direct-conversations", nil)
	if code != 200 {
		t.Fatal(code, list)
	}
	conversations := list["conversations"].([]any)
	if len(conversations) != 1 {
		t.Fatal("desktop did not discover only its own DM", list)
	}
	conversation := conversations[0].(map[string]any)
	target := conversation["name"].(string)
	if conversation["label"] != "remote-agent" || conversation["unread"] != float64(1) {
		t.Fatal(conversation)
	}
	code, history := call("local-token", "GET", "/v1/chat/messages?target="+url.QueryEscape(target), nil)
	if code != 200 {
		t.Fatal(code, history)
	}
	messages := history["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["body"] != incomingBody {
		t.Fatal(history)
	}
	attachment, err := loadChatFile(db, file["id"].(string), target)
	if err != nil || string(attachment.Data) != "The original private attachment" {
		t.Fatal("incoming DM attachment was not cached", err)
	}
	root := messages[0].(map[string]any)["id"].(string)
	code, inbox := call("local-token", "GET", "/v1/chat/activity", nil)
	if code != 200 || inbox["unread"] != float64(1) {
		t.Fatal("desktop DM did not create attention", code, inbox)
	}
	if code, result := call("another-token", "GET", "/v1/chat/messages?target="+url.QueryEscape(target), nil); code != 403 {
		t.Fatal("another local account could read the remote DM", code, result)
	}
	code, sent := call("local-token", "POST", "/v1/chat/messages", map[string]any{"Target": root, "Body": "A threaded reply from desktop", "IdempotencyKey": "desktop-dm-reply"})
	if code != 201 || sent["pending"] != true {
		t.Fatal(code, sent)
	}
	code, retry := call("local-token", "POST", "/v1/chat/messages", map[string]any{"Target": root, "Body": "A threaded reply from desktop", "IdempotencyKey": "desktop-dm-reply"})
	if code != 201 || retry["id"] != sent["id"] || retry["replayed"] != true {
		t.Fatal("desktop retry was duplicated", code, retry)
	}
	if _, err = SyncRemotes(context.Background(), db, "work"); err != nil {
		t.Fatal(err)
	}
	reply := messageIn(t, serverDB, "@remote-agent", "A threaded reply from desktop")
	if reply.ParentID == nil || *reply.ParentID != posted["id"] {
		t.Fatal("DM reply lost its original thread", reply)
	}
	code, thread := call("local-token", "GET", "/v1/chat/messages?target="+url.QueryEscape(target)+"&thread="+root, nil)
	if code != 200 || len(thread["messages"].([]any)) != 2 {
		t.Fatal("desktop cannot open its DM thread", code, thread)
	}
	if code, result := call("local-token", "PATCH", "/v1/chat/threads", map[string]any{"RootID": root, "Following": true}); code != 200 {
		t.Fatal(code, result)
	}
	if code, result := call("local-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": target}); code != 200 {
		t.Fatal(code, result)
	}
	code, list = call("local-token", "GET", "/v1/chat/direct-conversations", nil)
	if code != 200 || list["conversations"].([]any)[0].(map[string]any)["unread"] != float64(0) {
		t.Fatal("reading did not clear the DM badge", code, list)
	}
	offline.Store(true)
	code, queued := call("local-token", "POST", "/v1/chat/messages", map[string]any{"Target": target, "Body": "Durably queued while offline", "IdempotencyKey": "offline-dm"})
	if code != 201 || queued["pending"] != true {
		t.Fatal(code, queued)
	}
	if _, err = SyncRemotes(context.Background(), db, "work"); err == nil {
		t.Fatal("outage was not reported")
	}
	code, history = call("local-token", "GET", "/v1/chat/messages?target="+url.QueryEscape(target), nil)
	if code != 200 || len(history["messages"].([]any)) != 3 {
		t.Fatal("offline DM cache was lost", code, history)
	}
	offline.Store(false)
	// Explicit synchronization still respects a persisted outage cooldown.
	if _, err = db.Exec("UPDATE sync_schedule SET retry_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err = SyncRemotes(context.Background(), db, "work"); err != nil {
		t.Fatal(err)
	}
	messageIn(t, serverDB, "@remote-agent", "Durably queued while offline")
}
