package internal

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type channelTestSession struct {
	ready         atomic.Bool
	notifications chan mcp.JSONRPCNotification
}

func (s *channelTestSession) Initialize()       { s.ready.Store(true) }
func (s *channelTestSession) Initialized() bool { return s.ready.Load() }
func (s *channelTestSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return s.notifications
}
func (s *channelTestSession) SessionID() string { return "native-client" }

func TestClaudeChannelPushAndConsumption(t *testing.T) {
	db := newTestDB(t)
	hooks := &server.Hooks{}
	service := server.NewMCPServer("grpvn", "test", server.WithHooks(hooks), server.WithExperimental(map[string]any{"claude/channel": map[string]any{}}))
	st := &State{Name: "claude-bot", Follow: []string{"#team"}}
	cleanup := attachClaudeChannel(service, hooks, func() (string, *State, error) { return st.Name, st, nil }, MCPOptions{ClaudeChannel: true, RuntimeSession: "claude-fixture", ActivationMode: "activate"})
	defer cleanup()
	client := &channelTestSession{notifications: make(chan mcp.JSONRPCNotification, 8)}
	if err := service.RegisterSession(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	client.Initialize()
	daemonEventually(t, func() bool {
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM agent_sessions WHERE runtime_session='claude-fixture'").Scan(&count)
		return count == 1
	})
	original, err := Send(db, "alice", "@claude-bot", "do not inject raw peer instructions", "", false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case notification := <-client.notifications:
		if notification.Method != "notifications/claude/channel" {
			t.Fatalf("wrong native method: %s", notification.Method)
		}
		content, _ := notification.Params.AdditionalFields["content"].(string)
		if !strings.Contains(content, original.ID) || strings.Contains(content, "do not inject raw") {
			t.Fatalf("notification should contain bounded routing, not peer body: %s", content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle Claude session did not receive unsolicited channel event")
	}
	daemonEventually(t, func() bool {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE accepted_at IS NOT NULL AND consumed_at IS NULL").Scan(&n)
		return n == 1
	})
	var out bytes.Buffer
	if _, err := Read(&out, db, st, 0, true, false, true, false, "never", "@me"); err != nil {
		t.Fatal(err)
	}
	var consumed int
	_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE status='consumed'").Scan(&consumed)
	if consumed != 1 {
		t.Fatal("real read did not confirm consumption")
	}
	service.UnregisterSession(context.Background(), client.SessionID())
	cleanup()
	var live int
	_ = db.QueryRow("SELECT COUNT(*) FROM bridge_leases").Scan(&live)
	if live != 0 {
		t.Fatal("channel shutdown left a live bridge")
	}
}

func TestNativeNotifyDoesNotActivateAndKeepsHumanPolicy(t *testing.T) {
	db := newTestDB(t)
	id, err := RegisterSession(db, AgentSession{Agent: "pi-bot", Runtime: "pi", RuntimeSession: "pi-one", Mode: "notify"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@pi-bot", "one", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@pi-bot", "two", "", false); err != nil {
		t.Fatal(err)
	}
	if err := ScanActivationEvents(db); err != nil {
		t.Fatal(err)
	}
	sessions, _ := Sessions(db, true)
	if a, err := ClaimActivation(db, sessions[0], 0); err != nil || a != nil {
		t.Fatalf("notify policy started inference: %+v %v", a, err)
	}
	a, err := ClaimDelivery(db, sessions[0], true, 0)
	if err != nil || a == nil || len(a.Events) != 2 || a.activates() {
		t.Fatalf("notification coalescing: %+v %v", a, err)
	}
	if err := FinishActivation(db, a.ID, "accepted", "native append accepted", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE agent_sessions SET mode='activate',supervise=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterSession(db, AgentSession{Agent: "pi-bot", Runtime: "pi", RuntimeSession: "pi-one", Mode: "notify", KeepPolicy: true}, false, false); err != nil {
		t.Fatal(err)
	}
	sessions, _ = Sessions(db, true)
	if sessions[0].Mode != "activate" || !sessions[0].Supervise {
		t.Fatal("native re-registration reset human activation policy")
	}
}

func TestOnlyOneNativeBridgePerSession(t *testing.T) {
	db := newTestDB(t)
	id, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "pi", RuntimeSession: "pi-one", Mode: "notify"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunSessionBridge(ctx, db, id, true, func(Activation) error { return nil }) }()
	daemonEventually(t, func() bool {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM bridge_leases WHERE session_id=?", id).Scan(&n)
		return n == 1
	})
	if err := RunSessionBridge(context.Background(), db, id, true, func(Activation) error { return nil }); err == nil || !strings.Contains(err.Error(), "already connected") {
		t.Fatalf("duplicate bridge accepted: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native bridge would not stop")
	}
}
