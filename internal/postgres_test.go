package internal

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func newPostgresTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "grpvn_test_" + strings.ToLower(NewMessage("", "", nil).ID)
	if _, err = admin.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Setenv("GRPVN_DATABASE_URL", u.String())
	db, err := OpenDB()
	if err != nil {
		admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		admin.Close()
	})
	return db
}

func TestPostgresCommitOrderCursors(t *testing.T) {
	db := newTestDB(t)
	if !isPostgres(db) {
		t.Skip("requires GRPVN_TEST_POSTGRES")
	}
	first, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	a := NewMessage("a", "#concurrent", []byte("first"))
	if err = a.save(first); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	b := NewMessage("b", "#concurrent", []byte("second"))
	go func() { done <- b.Save(db) }()
	select {
	case err := <-done:
		t.Fatalf("later message committed past an uncommitted earlier cursor: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if b.Seq <= a.Seq {
		t.Fatalf("sequence order %d -> %d", a.Seq, b.Seq)
	}
}

func TestPostgresSQLBinding(t *testing.T) {
	q := postgresSQL("SELECT '?' AS text, \"?\", name FROM principals WHERE id=? AND (? AND active=1) -- ? ignored\n AND name=?")
	if !strings.Contains(q, "id=$1 AND ($2=1 AND") || !strings.Contains(q, "name=$3") || !strings.Contains(q, "'?' AS text") {
		t.Fatal(q)
	}
	if q = postgresSQL("INSERT OR IGNORE INTO example(id) VALUES(?)"); q != "INSERT INTO example(id) VALUES($1) ON CONFLICT DO NOTHING" {
		t.Fatal(q)
	}
}
