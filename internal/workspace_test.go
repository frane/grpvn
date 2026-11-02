package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkspaceAgentTriggerAndMCPReply(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "chatgpt-member", "chatgpt", []string{"#team", "@*"})
	var creates, polls atomic.Int64
	var conversation, key string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer workspace-token" {
			t.Error("Workspace credential missing")
		}
		if r.Method == "POST" {
			if r.URL.Path != "/v1/workspace_agents/agtch_test/trigger" || r.Header.Get("OpenAI-Beta") != "workspace_agent_runs=v1" {
				t.Error("wrong Workspace trigger")
			}
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			conversation = body["conversation_key"]
			key = r.Header.Get("Idempotency-Key")
			if !strings.Contains(body["input"], "grpvn read/send") || key == "" {
				t.Error("trigger lacks bidirectional reply instructions or durable key")
			}
			creates.Add(1)
			writeJSON(w, 202, map[string]string{"conversation_url": "https://chatgpt.com/c/test", "agent_trigger_run_id": "apirun_test"})
		} else {
			polls.Add(1)
			writeJSON(w, 200, map[string]string{"status": "completed", "conversation_url": "https://chatgpt.com/c/test"})
		}
	}))
	t.Cleanup(provider.Close)
	t.Setenv("GRPVN_WORKSPACE_AGENT_TOKEN", "workspace-token")
	id, err := RegisterSession(db, AgentSession{Agent: "chatgpt", Runtime: "workspace-agent", RuntimeSession: "agtch_test", Endpoint: provider.URL, Mode: "activate", Supervise: true}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := Send(db, "alice", "@chatgpt", "Please review", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	sessions, _ := Sessions(db, true)
	a, err := ClaimActivation(db, sessions[0], 0)
	if err != nil || a == nil {
		t.Fatalf("claim: %+v %v", a, err)
	}
	if err := DispatchActivation(context.Background(), db, *a); err != nil {
		t.Fatal(err)
	}
	if conversation != "grpvn:"+id+":dm:alice" || key != a.ID {
		t.Fatalf("unstable continuity/delivery keys: %q %q", conversation, key)
	}
	var consumed int
	_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE consumed_at IS NOT NULL").Scan(&consumed)
	if consumed != 0 {
		t.Fatal("API acceptance marked DM read")
	}
	handler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rpc := func(method string, params any) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, _ := http.NewRequest("POST", server.URL+"/mcp", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer chatgpt-member")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		out, _ := io.ReadAll(response.Body)
		if response.StatusCode != 200 || bytes.Contains(out, []byte(`"isError":true`)) {
			t.Fatalf("MCP callback failed: %d %s", response.StatusCode, out)
		}
		return string(out)
	}
	rpc("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "Workspace Agent", "version": "1"}})
	call := func(name string, args any) string {
		return rpc("tools/call", map[string]any{"name": name, "arguments": args})
	}
	if out := call("read", map[string]string{"target": "@me"}); !strings.Contains(out, "Please review") {
		t.Fatal("Workspace Agent could not read its DM")
	}
	if out := call("read", map[string]string{"target": original.ID}); !strings.Contains(out, "Please review") {
		t.Fatal("message-ID fetch could not recover consumed history")
	}
	replyArgs := map[string]string{"target": original.ID, "body": "Reply from Workspace Agent", "idempotency_key": "grpvn:" + a.ID}
	call("send", replyArgs)
	call("send", replyArgs)
	reply := messageIn(t, db, "@alice", "Reply from Workspace Agent")
	if reply.ParentID == nil || *reply.ParentID != original.ID {
		t.Fatal("callback lost the DM thread")
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM messages WHERE body=?", []byte("Reply from Workspace Agent")).Scan(&count)
	if count != 1 {
		t.Fatal("callback retry duplicated reply")
	}
	var automated bool
	_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM automated_messages WHERE message_id=?)", reply.ID).Scan(&automated)
	if !automated {
		t.Fatal("native ChatGPT reply lacks loop provenance")
	}
	if _, err := db.Exec("UPDATE activation_runs SET updated_at=0 WHERE id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := PollAgentRuns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var status, link string
	_ = db.QueryRow("SELECT status,conversation_url FROM activation_runs WHERE id=?", a.ID).Scan(&status, &link)
	if status != "completed" || link != "https://chatgpt.com/c/test" || creates.Load() != 1 || polls.Load() != 1 {
		t.Fatalf("provider completion not durable: %s %s %d %d", status, link, creates.Load(), polls.Load())
	}
	_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE status='consumed'").Scan(&consumed)
	if consumed != 1 {
		t.Fatal("provider status overwrote the real read receipt")
	}
}

func TestOpenCodeDirectAdapterUsesNativeAsyncEndpoint(t *testing.T) {
	db := newTestDB(t)
	t.Setenv("OPENCODE_TEST_PASSWORD", "secret")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "opencode" || password != "secret" {
			t.Error("wrong runtime authentication")
		}
		if r.URL.EscapedPath() != "/session/session%20one/prompt_async" {
			t.Errorf("wrong native session path: %s", r.URL.EscapedPath())
		}
		var body struct {
			MessageID string `json:"messageID"`
			NoReply   bool   `json:"noReply"`
			Parts     []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.NoReply || !strings.HasPrefix(body.MessageID, "msg_") || len(body.Parts) != 1 {
			t.Error("activation did not request a native turn")
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(provider.Close)
	_, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "opencode", RuntimeSession: "session one", Endpoint: provider.URL, CredentialEnv: "OPENCODE_TEST_PASSWORD", Mode: "activate"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@bot", "please respond", "", false); err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	sessions, _ := Sessions(db, true)
	a, err := ClaimActivation(db, sessions[0], 0)
	if err != nil || a == nil {
		t.Fatalf("claim %+v %v", a, err)
	}
	if err := DispatchActivation(context.Background(), db, *a); err != nil {
		t.Fatal(err)
	}
	var consumed int
	_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE consumed_at IS NOT NULL").Scan(&consumed)
	if consumed != 0 {
		t.Fatal("runtime acceptance marked chat read")
	}
}
