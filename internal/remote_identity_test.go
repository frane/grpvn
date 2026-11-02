package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestAgentRenamePreservesLocalSyncRetries(t *testing.T) {
	serverDB := newTestDB(t)
	coordinator := testCoordinator(t, serverDB, "invite")
	client := newTestDB(t)
	mustConnect(t, client, "work", coordinator.URL, "invite", "#dev")
	first, _, err := SendSynced(context.Background(), client, "local-agent", "#work/dev", "first", "", false, "first")
	if err != nil {
		t.Fatal(err)
	}
	mustSync(t, client, "work")
	var id string
	if err := serverDB.QueryRow("SELECT id FROM principals WHERE kind='agent' AND parent_id IS NOT NULL AND name='local-agent'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	p, err := RenamePrincipal(serverDB, id, "chosen-agent")
	if err != nil {
		t.Fatal(err)
	}
	wire := toWire(first)
	wire.Target = "#dev"
	if _, err := acceptAttributedMessage(serverDB, wire, p); err != nil {
		t.Fatalf("retry after rename lost original upload: %v", err)
	}
	if _, _, err := SendSynced(context.Background(), client, "local-agent", "#work/dev", "second", "", false, "second"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, client, "work")
	second := messageIn(t, serverDB, "#dev", "second")
	if !strings.HasPrefix(second.Sender, "chosen-agent@") {
		t.Fatalf("client could not send through its existing identity: %s", second.Sender)
	}
}

func remoteTool(t *testing.T, handler http.Handler, token, name string, args any) (map[string]any, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	req := httptest.NewRequest("POST", "https://test.example/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatalf("%s returned %d: %s", name, out.Code, out.Body.String())
	}
	var response struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Content) == 0 {
		t.Fatalf("missing tool result: %s", out.Body.String())
	}
	if response.Result.IsError {
		return map[string]any{"error": response.Result.Content[0].Text}, true
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &value); err != nil {
		t.Fatal(err)
	}
	return value, false
}

func TestRemoteIdentityNamesAndEmptyChannels(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "alice-token", "alice", []string{"#general", "@*"})
	seedTestAgent(t, db, "bob-token", "bob", []string{"#private", "@*"})
	handler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args any) map[string]any {
		t.Helper()
		v, failed := remoteTool(t, handler, "alice-token", name, args)
		if failed {
			t.Fatalf("%s: %v", name, v)
		}
		return v
	}
	channels := call("list_channels", map[string]any{})["channels"].([]any)
	if len(channels) != 1 || channels[0] != "#general" {
		t.Fatalf("empty permitted channel missing or private name leaked: %v", channels)
	}
	if value, failed := remoteTool(t, handler, "alice-token", "read", map[string]any{"target": "#offbyte/general"}); !failed || !strings.Contains(value["error"].(string), `native channel "#general"`) {
		t.Fatalf("wrong-source alias has no useful correction: %v", value)
	}
	identity := call("identity", map[string]any{})
	id := identity["id"].(string)
	if _, err := Send(db, "peer", "@alice", "already read", "", false); err != nil {
		t.Fatal(err)
	}
	call("read", map[string]any{"target": "@me"})
	incoming, err := Send(db, "peer", "@alice", "unread before rename", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, failed := remoteTool(t, handler, "bob-token", "rename", map[string]any{"name": "peer"}); !failed {
		t.Fatal("remote agent claimed an existing local sender and its inbox")
	}
	if _, err := Send(db, "local", "@reserved-recipient", "private local inbox", "", false); err != nil {
		t.Fatal(err)
	}
	if _, failed := remoteTool(t, handler, "bob-token", "rename", map[string]any{"name": "reserved-recipient"}); !failed {
		t.Fatal("remote agent claimed an existing local recipient")
	}
	outgoing := call("send", map[string]any{"target": "@peer", "body": "before rename", "idempotency_key": "stable-key"})
	renamed := call("rename", map[string]any{"name": "rook"})
	if renamed["id"] != id || renamed["name"] != "rook" || renamed["dm"] != "@rook" {
		t.Fatalf("identity changed: %v", renamed)
	}
	if v, failed := remoteTool(t, handler, "bob-token", "rename", map[string]any{"name": "alice"}); !failed {
		t.Fatalf("old address stolen: %v", v)
	}
	if v, failed := remoteTool(t, handler, "bob-token", "rename", map[string]any{"name": "rook"}); !failed {
		t.Fatalf("current name stolen: %v", v)
	}
	for _, name := range []string{"@other", "someone else", "x/y", "x~owner"} {
		if _, failed := remoteTool(t, handler, "alice-token", "rename", map[string]any{"name": name}); !failed {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	if got := call("watch", map[string]any{"target": "@me", "timeout": 0.01})["unread"]; got != float64(1) {
		t.Fatalf("old inbox unread lost: %v", got)
	}
	read := call("read", map[string]any{"target": "@me"})["messages"].([]any)
	if len(read) != 1 || read[0].(map[string]any)["id"] != incoming.ID {
		t.Fatalf("read cursor or historical DM lost: %v", read)
	}
	call("read", map[string]any{"target": incoming.ID, "peek": true})
	if v, failed := remoteTool(t, handler, "bob-token", "read", map[string]any{"target": incoming.ID}); !failed {
		t.Fatalf("private history leaked: %v", v)
	}
	call("send", map[string]any{"target": incoming.ID, "body": "thread reply"})
	call("send", map[string]any{"target": outgoing["id"], "body": "continue my own old thread"})
	replay := call("send", map[string]any{"target": "@peer", "body": "before rename", "idempotency_key": "stable-key"})
	if replay["id"] != outgoing["id"] || replay["replayed"] != true {
		t.Fatalf("idempotency lost on rename: %v", replay)
	}
	message, err := Send(db, "peer", "@alice", "old address still works", "", false)
	if err != nil || message.Target != "@rook" {
		t.Fatalf("old DM address not routed: %+v %v", message, err)
	}
	if _, err := db.Exec("INSERT INTO oauth_access(credential_hash,scope,resource) VALUES(?,'grpvn.read','https://test.example/mcp')", credentialHash("alice-token")); err != nil {
		t.Fatal(err)
	}
	if _, failed := remoteTool(t, handler, "alice-token", "rename", map[string]any{"name": "no-write-scope"}); !failed {
		t.Fatal("read-only credential renamed an identity")
	}
	if _, err := db.Exec("DELETE FROM oauth_access"); err != nil {
		t.Fatal(err)
	}
	generated := call("rename", map[string]any{})
	if generated["id"] != id || !regexp.MustCompile(`^[a-z]+-[a-z]+-[a-f0-9]{4}$`).MatchString(generated["name"].(string)) {
		t.Fatalf("missing generated fallback: %v", generated)
	}
	if len(call("list_channels", map[string]any{})["channels"].([]any)) != 1 {
		t.Fatal("rename changed channel grants")
	}
	var oldSender string
	if err := db.QueryRow("SELECT sender FROM messages WHERE id=?", outgoing["id"]).Scan(&oldSender); err != nil || oldSender != "alice" {
		t.Fatalf("history rewritten: %s %v", oldSender, err)
	}
}
