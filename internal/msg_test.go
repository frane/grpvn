package internal

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grpvn.db")
	t.Setenv("GRPVN_DB", path)
	db, err := OpenDB()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewMessageRootIsSelf(t *testing.T) {
	m := NewMessage("alice", "#dev", []byte("hi"))
	if m.ID == "" {
		t.Fatal("ID must be set")
	}
	if m.ChainRoot != m.ID {
		t.Fatalf("root message chain_root should equal ID; %s vs %s", m.ChainRoot, m.ID)
	}
	if m.ChainDepth != 0 {
		t.Fatalf("root depth should be 0, got %d", m.ChainDepth)
	}
	if m.CreatedAt == 0 {
		t.Fatal("CreatedAt must be set")
	}
	if len(m.ID) != 26 {
		t.Fatalf("ULID should be 26 chars, got %d", len(m.ID))
	}
}

func TestResolveTargetChannelAndUser(t *testing.T) {
	db := newTestDB(t)
	target, parent, err := ResolveTarget(db, "#dev", "#fallback")
	if err != nil {
		t.Fatal(err)
	}
	if target != "#dev" || parent != nil {
		t.Fatalf("unexpected: %s %+v", target, parent)
	}
	target, _, err = ResolveTarget(db, "@bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if target != "@bob" {
		t.Fatalf("expected @bob, got %s", target)
	}
}

func TestResolveTargetDefaultsToChannel(t *testing.T) {
	db := newTestDB(t)
	target, _, err := ResolveTarget(db, "", "#main")
	if err != nil {
		t.Fatal(err)
	}
	if target != "#main" {
		t.Fatalf("expected #main, got %s", target)
	}
	if _, _, err := ResolveTarget(db, "", ""); err == nil {
		t.Fatal("empty target with no default should error")
	}
}

func TestResolveTargetByULIDPrefix(t *testing.T) {
	db := newTestDB(t)
	m := NewMessage("alice", "#x", []byte("root"))
	if err := m.Save(db); err != nil {
		t.Fatal(err)
	}
	prefix := m.ID[:8]
	target, parent, err := ResolveTarget(db, prefix, "")
	if err != nil {
		t.Fatalf("resolve by prefix: %v", err)
	}
	if target != "#x" {
		t.Fatalf("target should match parent's target, got %s", target)
	}
	if parent == nil || parent.ID != m.ID {
		t.Fatalf("parent should match: %+v", parent)
	}
}

func TestMessageSavePersists(t *testing.T) {
	db := newTestDB(t)
	m := NewMessage("alice", "#x", []byte("hi"))
	if err := m.Save(db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM messages WHERE id = ?", m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestMigrateRecordsVersion(t *testing.T) {
	db := newTestDB(t)
	v, err := SchemaVersion(db)
	if err != nil {
		t.Fatal(err)
	}
	if v != CurrentSchemaVersion {
		t.Fatalf("schema version should be %d, got %d", CurrentSchemaVersion, v)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := newTestDB(t)
	for i := 0; i < 5; i++ {
		if err := Migrate(db); err != nil {
			t.Fatalf("migrate %d: %v", i, err)
		}
	}
	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("schema_version should have exactly 1 row, got %d", rows)
	}
}
