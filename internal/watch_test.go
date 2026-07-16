package internal

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// Print mode: one wake-up renders the message and advances the cursor, so
// the inbox is clean afterwards.
func TestWatchPrintModeReadsAndAdvances(t *testing.T) {
	db := newTestDB(t)
	NewMessage("bob", "#dev", []byte("ship it")).Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}

	var out, errw bytes.Buffer
	err := Watch(context.Background(), &out, &errw, db, waitLoader(st), WatchOpts{
		Interval: 50 * time.Millisecond,
		Once:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ship it") {
		t.Fatalf("expected message in output, got %q", out.String())
	}
	if !strings.Contains(errw.String(), "unread: 1 #dev") {
		t.Fatalf("expected wake-up log line, got %q", errw.String())
	}
	var buf bytes.Buffer
	if code, err := Check(&buf, db, st); err != nil || code != 2 {
		t.Fatalf("print mode must mark read; check = %d, %v", code, err)
	}
}

// Exec mode: the responder runs but the watcher never touches the cursors —
// a responder that doesn't read leaves the inbox unread (at-least-once), and
// the watcher says so.
func TestWatchExecModeLeavesReadingToResponder(t *testing.T) {
	db := newTestDB(t)
	NewMessage("bob", "#dev", []byte("ping")).Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}

	var out, errw bytes.Buffer
	err := Watch(context.Background(), &out, &errw, db, waitLoader(st), WatchOpts{
		Exec:     "echo responder-ran",
		Interval: 50 * time.Millisecond,
		Once:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "responder-ran") {
		t.Fatalf("expected responder output, got %q", out.String())
	}
	if !strings.Contains(errw.String(), "responder left unread") {
		t.Fatalf("expected left-unread warning, got %q", errw.String())
	}
	var buf bytes.Buffer
	if code, err := Check(&buf, db, st); err != nil || code != 0 {
		t.Fatalf("exec mode must not advance cursors; check = %d, %v", code, err)
	}
}

// A failing responder is logged, never fatal: the monitor outlives its
// responder.
func TestWatchExecModeSurvivesResponderFailure(t *testing.T) {
	db := newTestDB(t)
	NewMessage("bob", "#dev", []byte("ping")).Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}

	var out, errw bytes.Buffer
	err := Watch(context.Background(), &out, &errw, db, waitLoader(st), WatchOpts{
		Exec:     "exit 3",
		Interval: 50 * time.Millisecond,
		Once:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "responder:") {
		t.Fatalf("expected responder failure log, got %q", errw.String())
	}
}

// The cooldown is the anti-loop brake: with unread persisting (responder
// never reads), runs are spaced by at least the cooldown, not fired hot.
func TestWatchCooldownThrottlesRefire(t *testing.T) {
	db := newTestDB(t)
	NewMessage("bob", "#dev", []byte("ping")).Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}

	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()
	var out, errw bytes.Buffer
	err := Watch(ctx, &out, &errw, db, waitLoader(st), WatchOpts{
		Exec:     "echo run",
		Cooldown: 200 * time.Millisecond,
		Interval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Count(out.String(), "run")
	if runs < 1 || runs > 3 {
		t.Fatalf("expected 1-3 cooldown-paced runs in 450ms, got %d (output %q)", runs, out.String())
	}
}

// A cancelled context stops an idle watcher cleanly.
func TestWatchStopsOnContextCancel(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	var out, errw bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, &out, &errw, db, waitLoader(st), WatchOpts{Interval: 20 * time.Millisecond})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not stop on context cancel")
	}
	if out.Len() != 0 {
		t.Fatalf("idle watcher should print nothing, got %q", out.String())
	}
}
