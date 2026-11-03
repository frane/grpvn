package internal

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeCacheCanPlanThenCreatePrivateChannel(t *testing.T) {
	serverDB := newTestDB(t)
	handler, err := CoordinatorHandler(serverDB, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	connect := func() *sql.DB {
		t.Helper()
		_, invite, err := CreateInvite(serverDB, "Native QA", []string{"#general"}, true, time.Hour, 1)
		if err != nil {
			t.Fatal(err)
		}
		client := newTestDB(t)
		mustConnect(t, client, "work", server.URL, invite, "#new-project")
		mustSync(t, client, "work")
		return client
	}
	first, second := connect(), connect()
	root, _, err := SendSynced(context.Background(), first, "builder", "#work/new-project", "project opener", "", false, "native-opener")
	if err != nil || root.Pending {
		t.Fatalf("first native send did not create channel: %+v %v", root, err)
	}
	mustSync(t, first, "work")
	messageIn(t, serverDB, "#new-project", "project opener")
	if _, err := SyncRemotes(context.Background(), second, "work"); err == nil {
		t.Fatal("provisional subscriber gained access after another member created a private channel")
	}
	var leaked int
	if err := second.QueryRow("SELECT COUNT(*) FROM messages WHERE target='#work/new-project'").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("private messages leaked to provisional subscriber: %d %v", leaked, err)
	}
	blocked, _, err := SendSynced(context.Background(), second, "outsider", "#work/new-project", "not invited", "", false, "native-blocked")
	if err != nil || !blocked.Pending {
		t.Fatalf("existing private channel was claimed by another native agent: %+v %v", blocked, err)
	}
}

func TestWorkspaceMembersCreateInviteAndCommunicate(t *testing.T) {
	db := newTestDB(t)
	for _, name := range []string{"alice", "bob", "carol", "guest"} {
		channels := []string{"#general", "@*"}
		if name == "carol" {
			channels = []string{"#private", "@*"}
		}
		seedTestAgent(t, db, name+"-token", name, channels)
		if name != "guest" {
			if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name=?", name); err != nil {
				t.Fatal(err)
			}
		}
	}
	handler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(who, name string, args any) map[string]any {
		t.Helper()
		value, failed := remoteTool(t, handler, who+"-token", name, args)
		if failed {
			t.Fatalf("%s %s: %v", who, name, value)
		}
		return value
	}
	denied := func(who, name string, args any) {
		t.Helper()
		if value, failed := remoteTool(t, handler, who+"-token", name, args); !failed {
			t.Fatalf("%s unexpectedly allowed %s: %v", who, name, value)
		}
	}
	denied("guest", "send", map[string]any{"target": "#unapproved-project", "body": "no admission"})
	root := call("alice", "send", map[string]any{"target": "#ftkit", "body": "project opener", "idempotency_key": "opener"})
	denied("bob", "read", map[string]any{"target": "#ftkit"})
	denied("bob", "send", map[string]any{"target": "#ftkit", "body": "not a member"})
	denied("bob", "create_channel", map[string]any{"channel": "#ftkit"})
	denied("bob", "invite", map[string]any{"channel": "#ftkit", "agent": "@carol"})
	call("alice", "invite", map[string]any{"channel": "#ftkit", "agent": "@bob"})
	messages := call("bob", "read", map[string]any{"target": "#ftkit"})["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["id"] != root["id"] {
		t.Fatalf("invitation did not take effect immediately: %v", messages)
	}
	reply := call("bob", "send", map[string]any{"target": root["id"], "body": "ready to help", "idempotency_key": "reply"})
	thread := messageIn(t, db, "#ftkit", "ready to help")
	if thread.ParentID == nil || *thread.ParentID != root["id"] {
		t.Fatalf("agent reply did not preserve its thread: %v", reply)
	}
	// Ordinary channel writers can invite peers; they need no creator/admin role.
	call("bob", "invite", map[string]any{"channel": "#ftkit", "agent": "@carol"})
	call("carol", "read", map[string]any{"target": "#ftkit"})
	denied("alice", "invite", map[string]any{"channel": "#private", "agent": "@bob"})
	denied("alice", "invite", map[string]any{"channel": "@*", "agent": "@bob"})
	call("alice", "invite", map[string]any{"channel": "#general", "agent": "@guest"})
	call("guest", "send", map[string]any{"target": "#guest-project", "body": "admitted participant"})
	// Broadly approved membership never exposes somebody else's private channels.
	denied("guest", "read", map[string]any{"target": "#private"})
	directory := call("alice", "list_agents", map[string]any{})
	encoded, _ := json.Marshal(directory)
	if strings.Contains(string(encoded), "token") || strings.Contains(string(encoded), "#private") {
		t.Fatal("directory leaked credentials or private memberships")
	}
	if _, err := db.Exec("INSERT INTO oauth_access(credential_hash,scope,resource) VALUES(?,'grpvn.read','test')", credentialHash("bob-token")); err != nil {
		t.Fatal(err)
	}
	denied("bob", "create_channel", map[string]any{"channel": "#read-only"})
	denied("bob", "invite", map[string]any{"channel": "#ftkit", "agent": "@guest"})
	call("alice", "create_channel", map[string]any{"channel": "#public", "open": true})
	req := httptest.NewRequest("GET", "https://test/v1/channels", nil)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "#public") || strings.Contains(out.Body.String(), "#ftkit") {
		t.Fatalf("anonymous discovery violated channel visibility: %d %s", out.Code, out.Body.String())
	}
	call("alice", "create_channel", map[string]any{"channel": "#public", "open": false})
	var open bool
	if err := db.QueryRow("SELECT is_open FROM workspace_channels WHERE channel='#public'").Scan(&open); err != nil || !open {
		t.Fatal("idempotent creation changed existing visibility")
	}
}

func TestWorkspaceInviteAdmitsRegisteredPeerWithoutAdminCode(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "owner-token", "owner", []string{"#general"})
	seedTestAgent(t, db, "peer-token", "new-peer", nil)
	if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='owner'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE principals SET status='pending' WHERE name='new-peer'"); err != nil {
		t.Fatal(err)
	}
	handler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, failed := remoteTool(t, handler, "owner-token", "send", map[string]any{"target": "#project", "body": "welcome"}); failed {
		t.Fatal("owner could not create project")
	}
	directory, failed := remoteTool(t, handler, "owner-token", "list_agents", map[string]any{"pending": true})
	if failed {
		t.Fatal(directory)
	}
	encoded, _ := json.Marshal(directory)
	if !strings.Contains(string(encoded), "new-peer") {
		t.Fatal("pending collaborator cannot be found")
	}
	if value, failed := remoteTool(t, handler, "owner-token", "invite", map[string]any{"channel": "#project", "agent": "@new-peer"}); failed {
		t.Fatal(value)
	}
	if value, failed := remoteTool(t, handler, "peer-token", "send", map[string]any{"target": "#project", "body": "joined without code relay"}); failed {
		t.Fatal(value)
	}
	if value, failed := remoteTool(t, handler, "peer-token", "send", map[string]any{"target": "#general", "body": "not invited here"}); !failed {
		t.Fatalf("invitation exceeded granted channel: %v", value)
	}
	if value, failed := remoteTool(t, handler, "peer-token", "send", map[string]any{"target": "#my-project", "body": "can collaborate autonomously"}); failed {
		t.Fatal(value)
	}
}
