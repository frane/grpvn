package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSupervisorResumesWithoutShellInterpolation(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix executable fixtures")
	}
	for _, runtime := range []string{"grok", "claude-channel", "pi", "opencode"} {
		t.Run(runtime, func(t *testing.T) {
			db := newTestDB(t)
			dir := t.TempDir()
			capture := filepath.Join(dir, "capture")
			binary := runtime
			if runtime == "claude-channel" {
				binary = "claude"
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" \"$GRPVN_AS\" \"$GRPVN_STATE\" > \"$GRPVN_NATIVE_CAPTURE\"\n"
			if err := os.WriteFile(filepath.Join(dir, binary), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GRPVN_NATIVE_CAPTURE", capture)
			state := filepath.Join(dir, "native.json")
			_, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: runtime, RuntimeSession: "native-session", StatePath: state, Project: dir, Mode: "activate", Supervise: true}, true, false)
			if err != nil {
				t.Fatal(err)
			}
			original, err := Send(db, "alice", "@bot", "$(never execute peer body)", "", false)
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
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			out := string(data)
			if !strings.Contains(out, "native-session") || !strings.Contains(out, original.ID) || !strings.Contains(out, "\nbot\n"+state) || strings.Contains(out, "never execute peer body") {
				t.Fatalf("resume lost identity/thread boundary: %s", out)
			}
			for _, forbidden := range []string{"--always-approve", "--yolo", "--dangerously-skip-permissions", "--auto\n"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("supervisor expanded native permissions: %s", out)
				}
			}
			reply, err := Send(db, "bot", original.ID, "native reply", "", false)
			if err != nil {
				t.Fatal(err)
			}
			var automated bool
			_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM automated_messages WHERE message_id=?)", reply.ID).Scan(&automated)
			if !automated {
				t.Fatal("native reply can create an acknowledgement loop")
			}
		})
	}
}

func TestSupervisorRecoversQueuedDeliveryAfterRestart(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix executable fixture")
	}
	db := newTestDB(t)
	dir := t.TempDir()
	capture := filepath.Join(dir, "calls")
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nprintf x >> \"$GRPVN_NATIVE_CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GRPVN_NATIVE_CAPTURE", capture)
	_, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "codex", RuntimeSession: "native", Mode: "activate"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@bot", "wake after restart", "", false); err != nil {
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
	if err := FinishActivation(db, a.ID, "queued", "worker not started", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); RunActivationWatcher(ctx, db, nil) }()
	daemonEventually(t, func() bool {
		var status string
		_ = db.QueryRow("SELECT status FROM activation_runs WHERE id=?", a.ID).Scan(&status)
		return status == "accepted"
	})
	cancel()
	<-done
	if err := ProcessActivations(context.Background(), db, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil || string(data) != "x" {
		t.Fatalf("queued run duplicated or lost: %s %v", data, err)
	}
	var consumed int
	_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE consumed_at IS NOT NULL").Scan(&consumed)
	if consumed != 0 {
		t.Fatal("supervisor acceptance marked unread consumed")
	}
}

func TestAcceptedProviderIDSurvivesLocalCompletionFailure(t *testing.T) {
	db := newTestDB(t)
	_, err := RegisterSession(db, AgentSession{Agent: "bot", Runtime: "openai", RuntimeSession: "worker", Mode: "activate", Supervise: true, Model: "test-model"}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "alice", "@bot", "review", "", false); err != nil {
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
	if err := FinishActivation(db, a.ID, "running", "provider accepted", "resp_durable", ""); err != nil {
		t.Fatal(err)
	}
	if err := recordDispatchFailure(db, a.ID, fmt.Errorf("local completion interrupted")); err != nil {
		t.Fatal(err)
	}
	var status, provider string
	if err := db.QueryRow("SELECT status,provider_id FROM activation_runs WHERE id=?", a.ID).Scan(&status, &provider); err != nil {
		t.Fatal(err)
	}
	if status != "running" || provider != "resp_durable" {
		t.Fatalf("lost accepted result needed for recovery: %s %s", status, provider)
	}
}
