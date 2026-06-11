package internal

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func waitLoader(st *State) func() (*State, error) {
	return func() (*State, error) { return st, nil }
}

func TestWaitReturnsImmediatelyWhenUnread(t *testing.T) {
	db := newTestDB(t)
	NewMessage("bob", "#dev", []byte("hi")).Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}

	var buf bytes.Buffer
	start := time.Now()
	code, err := Wait(context.Background(), &buf, db, waitLoader(st), 5*time.Second, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !strings.Contains(buf.String(), "1 #dev") {
		t.Fatalf("expected counts in output, got %q", buf.String())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("pre-existing unread should return without polling; took %v", time.Since(start))
	}
}

func TestWaitTimesOutAtTwo(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	var buf bytes.Buffer
	code, err := Wait(context.Background(), &buf, db, waitLoader(st), 300*time.Millisecond, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected 2 on timeout, got %d", code)
	}
	if buf.Len() != 0 {
		t.Fatalf("timeout should produce no output, got %q", buf.String())
	}
}

// The wake-up path: a write committed on a DIFFERENT connection must bump
// PRAGMA data_version on the waiting connection and surface the message.
func TestWaitWakesOnCrossConnectionWrite(t *testing.T) {
	db := newTestDB(t) // sets GRPVN_DB; the waiter polls on this handle
	writer, err := OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	go func() {
		time.Sleep(300 * time.Millisecond)
		NewMessage("bob", "#dev", []byte("wake up")).Save(writer)
	}()

	var buf bytes.Buffer
	start := time.Now()
	code, err := Wait(context.Background(), &buf, db, waitLoader(st), 10*time.Second, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected 0 after cross-connection write, got %d", code)
	}
	if !strings.Contains(buf.String(), "1 #dev") {
		t.Fatalf("expected '1 #dev', got %q", buf.String())
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("wake-up took %v; data_version polling appears broken", time.Since(start))
	}
}

// Own messages never count as unread, so they must not wake a waiter.
func TestWaitIgnoresOwnMessages(t *testing.T) {
	db := newTestDB(t)
	writer, err := OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	go func() {
		time.Sleep(100 * time.Millisecond)
		NewMessage("alice", "#dev", []byte("talking to myself")).Save(writer)
	}()

	var buf bytes.Buffer
	code, err := Wait(context.Background(), &buf, db, waitLoader(st), 600*time.Millisecond, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("own message must not wake the waiter; got code %d output %q", code, buf.String())
	}
}

// Context cancellation behaves like a timeout (exit 2), so an MCP host
// cancelling a w call gets a clean "nothing" instead of an error.
func TestWaitHonoursContextCancel(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	var buf bytes.Buffer
	code, err := Wait(ctx, &buf, db, waitLoader(st), 0, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected 2 on cancel, got %d", code)
	}
}
