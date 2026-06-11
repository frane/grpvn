package internal

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The reason cursors are seq-based: a ULID is minted before its insert
// commits, so a message can land "in the past" relative to a cursor that
// already advanced (clock skew, or a racing reader catching a later send
// first). Under v1's `id > cursor` that message was lost forever. Under
// seq cursors it MUST still surface as unread.
func TestOutOfOrderULIDIsNotLost(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}

	// A message with a high ULID commits and gets read.
	high := NewMessage("bob", "#dev", []byte("later clock"))
	high.ID = "01ZZZZZZZZZZZZZZZZZZZZZZZZ"
	high.ChainRoot = high.ID
	if err := high.Save(db); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if code, err := Read(&buf, db, st, 0, true, false, false, false, "never"); err != nil || code != 0 {
		t.Fatalf("first read: code=%d err=%v", code, err)
	}

	// Now a message whose ULID sorts BEFORE the consumed one commits — the
	// v1 lost-message scenario.
	low := NewMessage("carol", "#dev", []byte("earlier clock"))
	low.ID = "01AAAAAAAAAAAAAAAAAAAAAAAA"
	low.ChainRoot = low.ID
	if err := low.Save(db); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	code, err := Check(&buf, db, st)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(buf.String(), "1 #dev") {
		t.Fatalf("late-committing low ULID must be unread; code=%d out=%q", code, buf.String())
	}
	buf.Reset()
	if code, err := Read(&buf, db, st, 0, true, false, false, false, "never"); err != nil || code != 0 {
		t.Fatalf("read of late message: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "earlier clock") {
		t.Fatalf("expected the late message body, got %q", buf.String())
	}
}

func TestAdvanceCursorIsMonotonic(t *testing.T) {
	db := newTestDB(t)
	if err := advanceCursor(db, "a", "#c", 10); err != nil {
		t.Fatal(err)
	}
	// A stale advance must lose.
	if err := advanceCursor(db, "a", "#c", 5); err != nil {
		t.Fatal(err)
	}
	if pos := cursorPos(t, db, "a", "#c"); pos != 10 {
		t.Fatalf("stale advance must not regress the cursor; got %d", pos)
	}
	if err := advanceCursor(db, "a", "#c", 12); err != nil {
		t.Fatal(err)
	}
	if pos := cursorPos(t, db, "a", "#c"); pos != 12 {
		t.Fatalf("fresh advance should win; got %d", pos)
	}
}

func TestMigrateLegacyCursors(t *testing.T) {
	db := newTestDB(t)
	statePath := filepath.Join(t.TempDir(), "state.json")

	m1 := NewMessage("bob", "#dev", []byte("read already"))
	m1.Save(db)
	m2 := NewMessage("bob", "#dev", []byte("read already too"))
	m2.Save(db)
	m3 := NewMessage("bob", "#dev", []byte("still unread"))
	m3.Save(db)

	st := &State{
		Name:    "alice",
		Follow:  []string{"#dev"},
		Cursors: map[string]string{"#dev": m2.ID},
	}
	if err := MigrateLegacyCursors(db, st, statePath); err != nil {
		t.Fatal(err)
	}
	if st.Cursor != "" || st.Cursors != nil {
		t.Fatalf("legacy fields must be cleared in memory: %+v", st)
	}
	loaded, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Cursor != "" || len(loaded.Cursors) != 0 {
		t.Fatalf("legacy fields must be cleared on disk: %+v", loaded)
	}
	if pos := cursorPos(t, db, "alice", "#dev"); pos != m2.Seq {
		t.Fatalf("legacy ULID cursor should translate to seq %d, got %d", m2.Seq, pos)
	}
	var buf bytes.Buffer
	code, err := Check(&buf, db, st)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(buf.String(), "1 #dev") {
		t.Fatalf("exactly m3 should be unread after migration; code=%d out=%q", code, buf.String())
	}

	// Re-running with no legacy fields is a no-op.
	if err := MigrateLegacyCursors(db, st, statePath); err != nil {
		t.Fatal(err)
	}

	// A legacy cursor must never regress a DB cursor that is further ahead.
	if err := advanceCursor(db, "alice", "#dev", m3.Seq); err != nil {
		t.Fatal(err)
	}
	st2 := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{"#dev": m1.ID}}
	if err := MigrateLegacyCursors(db, st2, statePath); err != nil {
		t.Fatal(err)
	}
	if pos := cursorPos(t, db, "alice", "#dev"); pos != m3.Seq {
		t.Fatalf("migration must not move cursors backwards; got %d want %d", pos, m3.Seq)
	}
}

// A v1 database (no seq column, cursors in state.json, schema_version=1)
// must rebuild transparently on open: same messages, same order, marks
// intact, version recorded as 2.
func TestMigrateV1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	t.Setenv("GRPVN_DB", path)

	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	const v1Schema = `
CREATE TABLE messages (
    id           TEXT PRIMARY KEY,
    sender       TEXT NOT NULL,
    target       TEXT NOT NULL,
    body         BLOB NOT NULL,
    chain_root   TEXT NOT NULL,
    chain_depth  INTEGER NOT NULL DEFAULT 0,
    parent_id    TEXT,
    correlation  TEXT,
    created_at   INTEGER NOT NULL
);
CREATE INDEX idx_messages_target_id ON messages(target, id);
CREATE TABLE marks (
    agent_name   TEXT NOT NULL,
    message_id   TEXT NOT NULL,
    marked_at    INTEGER NOT NULL,
    PRIMARY KEY (agent_name, message_id),
    FOREIGN KEY (message_id) REFERENCES messages(id)
);
CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
INSERT INTO schema_version VALUES (1, 0);`
	if _, err := raw.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"01AAAAAAAAAAAAAAAAAAAAAAAA",
		"01BBBBBBBBBBBBBBBBBBBBBBBB",
		"01CCCCCCCCCCCCCCCCCCCCCCCC",
	}
	for i, id := range ids {
		if _, err := raw.Exec(
			"INSERT INTO messages (id, sender, target, body, chain_root, chain_depth, created_at) VALUES (?, 'bob', '#dev', ?, ?, 0, ?)",
			id, "msg"+id[:4], id, int64(i),
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec("INSERT INTO marks VALUES ('alice', ?, 0)", ids[1]); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := OpenDB()
	if err != nil {
		t.Fatalf("open v1 db: %v", err)
	}
	defer db.Close()

	v, err := SchemaVersion(db)
	if err != nil || v != 2 {
		t.Fatalf("schema version should be 2, got %d (%v)", v, err)
	}
	rows, err := db.Query("SELECT id FROM messages ORDER BY seq ASC")
	if err != nil {
		t.Fatalf("seq column missing after migration: %v", err)
	}
	var got []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		got = append(got, id)
	}
	rows.Close()
	if len(got) != 3 || got[0] != ids[0] || got[1] != ids[1] || got[2] != ids[2] {
		t.Fatalf("seq order must preserve v1 ULID order: %v", got)
	}
	var marks int
	db.QueryRow("SELECT COUNT(*) FROM marks").Scan(&marks)
	if marks != 1 {
		t.Fatalf("marks must survive migration, got %d", marks)
	}
	var fk int
	db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign keys must be re-enabled after migration")
	}
	// Re-opening (migration already done) must be clean.
	db2, err := OpenDB()
	if err != nil {
		t.Fatalf("re-open migrated db: %v", err)
	}
	db2.Close()
}

func TestGcPrunesOldMessages(t *testing.T) {
	db := newTestDB(t)
	old := NewMessage("bob", "#dev", []byte("ancient"))
	old.CreatedAt = time.Now().Add(-48 * time.Hour).UnixMilli()
	old.Save(db)
	if _, err := db.Exec("INSERT INTO marks VALUES ('alice', ?, 0)", old.ID); err != nil {
		t.Fatal(err)
	}
	fresh := NewMessage("bob", "#dev", []byte("recent"))
	fresh.Save(db)

	var buf bytes.Buffer
	if err := Gc(&buf, db, 24*time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "pruned 1 messages, 1 marks") {
		t.Fatalf("unexpected gc report: %q", buf.String())
	}
	var n int
	db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 1 {
		t.Fatalf("expected only the fresh message to survive, got %d", n)
	}
	db.QueryRow("SELECT COUNT(*) FROM marks").Scan(&n)
	if n != 0 {
		t.Fatalf("marks on pruned messages must go too, got %d", n)
	}
	if err := Gc(&buf, db, 24*time.Hour, true); err != nil {
		t.Fatalf("gc --vacuum: %v", err)
	}
	// seq must not be reused after pruning + vacuum.
	next := NewMessage("bob", "#dev", []byte("after gc"))
	if err := next.Save(db); err != nil {
		t.Fatal(err)
	}
	if next.Seq <= fresh.Seq {
		t.Fatalf("seq must keep increasing after gc (got %d after %d)", next.Seq, fresh.Seq)
	}
	if err := Gc(&buf, db, 0, false); err == nil {
		t.Fatal("gc with non-positive cutoff must error")
	}
}

func TestSendRejectsOversizedBody(t *testing.T) {
	db := newTestDB(t)
	big := strings.Repeat("x", MaxBodyBytes+1)
	if _, err := Send(db, "a", "#dev", big, "", false); err == nil || !strings.Contains(err.Error(), "body too large") {
		t.Fatalf("expected body too large error, got %v", err)
	}
	ok := strings.Repeat("x", MaxBodyBytes)
	if _, err := Send(db, "a", "#dev", ok, "", false); err != nil {
		t.Fatalf("exactly MaxBodyBytes should be accepted: %v", err)
	}
}
