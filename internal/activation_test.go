package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActivationReceiptsCoalesceAndKeepUnread(t *testing.T) {
	db := newTestDB(t)
	id, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "codex", RuntimeSession: "native-session", Mode: "activate", Channels: []string{"#dev"}}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Send(db, "alice", "@bot", "please review", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@bot", "one more detail", "", false); err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	sessions, err := Sessions(db, true)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ClaimActivation(db, sessions[0], 0)
	if err != nil || a == nil || len(a.Events) != 2 {
		t.Fatalf("coalesce: %+v %v", a, err)
	}
	if sessions[0].ID != id {
		t.Fatal("wrong owner")
	}
	if err := FinishActivation(db, a.ID, "accepted", "queued", "", ""); err != nil {
		t.Fatal(err)
	}
	var unread bytes.Buffer
	st := &State{Name: "bot", Follow: []string{"#dev"}}
	if code, err := Check(&unread, db, st); err != nil || code != 0 {
		t.Fatalf("wake marked messages read: %s %v", &unread, err)
	}
	var consumed int
	if err := db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE consumed_at IS NOT NULL").Scan(&consumed); err != nil || consumed != 0 {
		t.Fatalf("accepted is not consumed: %d %v", consumed, err)
	}
	unread.Reset()
	if _, err := Read(&unread, db, st, 0, true, false, true, false, "never", "@me"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE consumed_at IS NOT NULL").Scan(&consumed); err != nil || consumed != 2 {
		t.Fatalf("read receipt missing: %d %v", consumed, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM activation_events").Scan(&count); err != nil || count != 2 {
		t.Fatalf("duplicate event: %d %v", count, err)
	}
}

func TestNativeCodexQueueUsesArgumentVector(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell fixture is Unix-only")
	}
	db := newTestDB(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GRPVN_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GRPVN_CAPTURE", capture)
	_, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "codex", RuntimeSession: "session with spaces", Mode: "activate", Supervise: true}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "peer", "@bot", "$(do not execute this)", "", false); err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	sessions, _ := Sessions(db, true)
	a, err := ClaimActivation(db, sessions[0], 0)
	if err != nil || a == nil {
		t.Fatal(err)
	}
	if err := DispatchActivation(context.Background(), db, *a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "queue\n--thread\nsession with spaces\n--message\n") || !strings.Contains(out, "--resume") || strings.Contains(out, "$(do not execute") {
		t.Fatalf("unsafe or wrong queue args: %s", out)
	}
}

func TestOpenAIBackgroundReplyIsDurableAndNotChatGPTUI(t *testing.T) {
	db := newTestDB(t)
	var creates atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Error("missing API credential")
		}
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["background"] != true || input["model"] != "test-model" {
			t.Errorf("bad request: %+v", input)
		}
		creates.Add(1)
		writeJSON(w, 200, map[string]any{"id": "resp_test", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "Here is the review."}}}}})
	}))
	t.Cleanup(provider.Close)
	t.Setenv("OPENAI_API_KEY", "test-api-key")
	_, err := RegisterSession(db, AgentSession{Agent: "api-bot", Runtime: "openai", RuntimeSession: "api-worker", Endpoint: provider.URL, Model: "test-model", Mode: "activate", Supervise: true}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := Send(db, "alice", "@api-bot", "Review this", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	sessions, _ := Sessions(db, true)
	a, err := ClaimActivation(db, sessions[0], 0)
	if err != nil || a == nil {
		t.Fatal(err)
	}
	if err := DispatchActivation(context.Background(), db, *a); err != nil {
		t.Fatal(err)
	}
	reply := messageIn(t, db, "@alice", "Here is the review.")
	if reply.ParentID == nil || *reply.ParentID != original.ID || reply.ChainRoot != original.ChainRoot {
		t.Fatalf("reply lost thread: %+v", reply)
	}
	var replay apiResponse
	if err := json.Unmarshal([]byte(`{"id":"resp_test","status":"completed","output":[{"content":[{"type":"output_text","text":"Here is the review."}]}]}`), &replay); err != nil {
		t.Fatal(err)
	}
	if err := completeAPIReply(context.Background(), db, *a, replay); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE body=?", []byte("Here is the review.")).Scan(&count); err != nil || count != 1 || creates.Load() != 1 {
		t.Fatalf("duplicate generation/reply: %d %d %v", count, creates.Load(), err)
	}
	var automated bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM automated_messages WHERE message_id=?)", reply.ID).Scan(&automated); err != nil || !automated {
		t.Fatal("reply lacks loop-prevention provenance")
	}
}

func TestProviderPollingRespectsCooldownAcrossRestart(t *testing.T) {
	db := newTestDB(t)
	var creates, polls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			creates.Add(1)
			writeJSON(w, 200, map[string]string{"id": "resp_restart", "status": "queued"})
			return
		}
		if polls.Add(1) == 1 {
			w.Header().Set("Retry-After", "60")
			writeJSON(w, 429, map[string]string{"error": "slow down"})
			return
		}
		writeJSON(w, 200, map[string]any{"id": "resp_restart", "status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": "Finished after restart."}}}}})
	}))
	t.Cleanup(provider.Close)
	t.Setenv("OPENAI_API_KEY", "test-key")
	_, err := RegisterSession(db, AgentSession{Agent: "worker", Runtime: "openai", RuntimeSession: "durable", Endpoint: provider.URL, Model: "test-model", Mode: "activate", Supervise: true}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@worker", "Please review", "", false); err != nil {
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
	if _, err := db.Exec("UPDATE activation_runs SET updated_at=0 WHERE id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := PollAgentRuns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var next int64
	if err := db.QueryRow("SELECT next_at FROM provider_poll WHERE run_id=?", a.ID).Scan(&next); err != nil || next < before.Add(time.Minute).UnixMilli() {
		t.Fatalf("Retry-After lost: %d %v", next, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := PollAgentRuns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 || polls.Load() != 1 {
		t.Fatalf("restart repeated work during cooldown: creates=%d polls=%d", creates.Load(), polls.Load())
	}
	// Advance the persisted deadline rather than sleeping a minute.
	if _, err := db.Exec("UPDATE provider_poll SET next_at=0 WHERE run_id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := PollAgentRuns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	messageIn(t, db, "@alice", "Finished after restart.")
	if err := PollAgentRuns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 || polls.Load() != 2 {
		t.Fatalf("completed provider run repeated: creates=%d polls=%d", creates.Load(), polls.Load())
	}
}

func TestActivationOwnerAndAutomatedLoopGuard(t *testing.T) {
	db := newTestDB(t)
	first, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "codex", RuntimeSession: "one", Mode: "activate"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterSession(db, AgentSession{Agent: "bot", Runtime: "codex", RuntimeSession: "two", Mode: "activate"}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err = RegisterSession(db, AgentSession{Agent: "other", Runtime: "codex", RuntimeSession: "one", Mode: "activate"}, false, false); err == nil {
		t.Fatal("a native session was rebound to another identity")
	}
	owners, err := Sessions(db, true)
	if err != nil || len(owners) != 1 || owners[0].ID != first {
		t.Fatalf("ambiguous owner: %+v %v", owners, err)
	}
	if _, _, err := SendIdempotent(db, "peer", "@bot", "automatic acknowledgement", "", false, "activation:test-loop"); err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM activation_events").Scan(&count); err != nil || count != 0 {
		t.Fatalf("automated reply activated another turn: %d %v", count, err)
	}
}
