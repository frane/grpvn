package tests

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `wait` with nothing unread exits 2 after the timeout, like `c`.
func TestWaitTimesOut(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	a := newRunner(t, "alice").withSharedDB(dbPath)
	a.mustRun("follow", "#dev")

	start := time.Now()
	_, _, code := a.run("wait", "--timeout", "500ms")
	if code != 2 {
		t.Fatalf("expected exit 2 on timeout, got %d", code)
	}
	if time.Since(start) < 400*time.Millisecond {
		t.Fatalf("wait returned before the timeout: %v", time.Since(start))
	}
}

// A blocked `wait` wakes when another agent (another process, another
// connection) commits a message, and prints the counts line.
func TestWaitWakesOnSend(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	a := newRunner(t, "alice").withSharedDB(dbPath)
	b := newRunner(t, "bob").withSharedDB(dbPath)
	a.mustRun("follow", "#dev")

	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, _, code := a.run("wait", "--timeout", "15s")
		done <- result{out, code}
	}()
	// Give the waiter a moment to pass its initial check.
	time.Sleep(500 * time.Millisecond)
	b.mustRun("s", "#dev", "wake up")

	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("expected exit 0 after send, got %d (out=%q)", r.code, r.out)
		}
		if !strings.Contains(r.out, "1 #dev") {
			t.Fatalf("expected counts '1 #dev', got %q", r.out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("wait never woke up after a cross-process send")
	}
}

// `hook stop` emits a block decision when unread messages exist, stays
// silent when there are none, and never blocks when stop_hook_active is set
// (the anti-loop guarantee).
func TestHookStop(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "shared.db")
	a := newRunner(t, "alice").withSharedDB(dbPath)
	b := newRunner(t, "bob").withSharedDB(dbPath)
	a.mustRun("follow", "#dev")

	hook := func(stdin string) (string, int) {
		t.Helper()
		cmd := exec.Command(a.bin, "hook", "stop")
		cmd.Dir = a.cwd
		cmd.Env = a.env
		cmd.Stdin = strings.NewReader(stdin)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				code = exitError.ExitCode()
			} else {
				t.Fatalf("run hook stop: %v", err)
			}
		}
		return stdout.String(), code
	}

	// Nothing unread: no output, exit 0 — the agent stops normally.
	out, code := hook(`{"stop_hook_active":false}`)
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("quiet inbox should not block: code=%d out=%q", code, out)
	}

	b.mustRun("s", "#dev", "blocker found")

	// Unread: a JSON block decision naming the counts.
	out, code = hook(`{"stop_hook_active":false}`)
	if code != 0 {
		t.Fatalf("hook must fail open / exit 0, got %d", code)
	}
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("hook output is not JSON: %v\n%q", err, out)
	}
	if decision.Decision != "block" {
		t.Fatalf("expected decision=block, got %#v", decision)
	}
	if !strings.Contains(decision.Reason, "1 #dev") {
		t.Fatalf("reason should carry the counts, got %q", decision.Reason)
	}

	// Same unread state but stop_hook_active: must NOT block again.
	out, code = hook(`{"stop_hook_active":true}`)
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("stop_hook_active must let the agent stop: code=%d out=%q", code, out)
	}

	// Garbage stdin fails open.
	out, code = hook("not json")
	if code != 0 {
		t.Fatalf("garbage stdin must fail open, got %d", code)
	}
}
