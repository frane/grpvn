package tests

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestBackgroundDaemonLifecycle(t *testing.T) {
	t.Parallel()
	host := newRunner(t, "host")
	endpoint, _ := startCoordinator(t, host)
	dbPath := filepath.Join(t.TempDir(), "client.db")
	client := newRunner(t, "alice").withSharedDB(dbPath)
	other := newRunner(t, "bob").withSharedDB(dbPath)
	invite := strings.TrimSpace(host.mustRun("iv", "--to", "team", "--channel", "*", "--write"))
	client.mustRun("connect", "work", endpoint, "#dev", "--invite", invite)
	if out := client.mustRun("d", "s", "--interval", "250ms"); !strings.Contains(out, "daemon running pid=") {
		t.Fatalf("start: %s", out)
	}
	t.Cleanup(func() { _, _, _ = client.run("daemon", "stop") })
	firstStatus := client.mustRun("d", "i")
	if !strings.Contains(firstStatus, "daemon running pid=") || !strings.Contains(firstStatus, "interval=250ms") {
		t.Fatalf("status: %s", firstStatus)
	}
	if out := other.mustRun("daemon", "start"); !strings.Contains(out, "already running") {
		t.Fatalf("duplicate start: %s", out)
	}
	if _, stderr, code := client.run("daemon", "--interval", "250ms"); code == 0 || !strings.Contains(stderr, "already running") {
		t.Fatalf("duplicate foreground worker: code=%d err=%s", code, stderr)
	}
	host.mustRun("s", "#dev", "arrived while client idle")
	// Inspect SQLite directly. No client chat command may trigger a sync
	// before this assertion: only the detached daemon can deliver the post.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE target = '#work/dev' AND body = ?", []byte("arrived while client idle")).Scan(&count)
		if err == nil && count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not update local SQLite: %d %v", count, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if out := client.mustRun("r", "#work/dev"); !strings.Contains(out, "arrived while client idle") {
		t.Fatalf("daemon cache: %s", out)
	}
	if out := other.mustRun("d", "x"); !strings.Contains(out, "daemon stopped") {
		t.Fatalf("stop from another identity: %s", out)
	}
	if out := client.mustRun("daemon", "status"); strings.TrimSpace(out) != "daemon not running" {
		t.Fatalf("stopped status: %s", out)
	}
	if out := client.mustRun("daemon", "stop"); strings.TrimSpace(out) != "daemon not running" {
		t.Fatalf("repeated stop: %s", out)
	}
	client.mustRun("daemon", "start", "--interval", "250ms")
	if out := client.mustRun("daemon", "status"); !strings.Contains(out, "daemon running pid=") {
		t.Fatalf("restart: %s", out)
	}
}
