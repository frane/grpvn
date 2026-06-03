package internal

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckEmptyExits2(t *testing.T) {
	db := newTestDB(t)
	var buf bytes.Buffer
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	code, err := Check(&buf, db, st)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if buf.Len() != 0 {
		t.Fatalf("empty check should produce no output, got %q", buf.String())
	}
}

func TestCheckCountsByTarget(t *testing.T) {
	db := newTestDB(t)
	m1 := NewMessage("bob", "#dev", []byte("a"))
	m1.Save(db)
	m2 := NewMessage("bob", "#dev", []byte("b"))
	m2.Save(db)
	m3 := NewMessage("bob", "@alice", []byte("c"))
	m3.Save(db)
	m4 := NewMessage("bob", "#ops", []byte("d")) // not followed, excluded
	m4.Save(db)

	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	var buf bytes.Buffer
	code, err := Check(&buf, db, st)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "2 #dev") {
		t.Fatalf("expected '2 #dev' in %q", out)
	}
	if !strings.Contains(out, "1 @me") {
		t.Fatalf("expected '1 @me' in %q", out)
	}
}

func TestReadAdvancesCursor(t *testing.T) {
	db := newTestDB(t)
	m1 := NewMessage("bob", "#dev", []byte("a"))
	m1.Save(db)
	m2 := NewMessage("bob", "#dev", []byte("b"))
	m2.Save(db)

	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	var buf bytes.Buffer
	code, err := Read(&buf, db, st, 0, true, false, false, false, "never")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if st.CursorFor("#dev") != m2.ID {
		t.Fatalf("cursor for #dev should advance to %s, got %s", m2.ID, st.CursorFor("#dev"))
	}

	// Read again — should be empty.
	buf.Reset()
	code, err = Read(&buf, db, st, 0, true, false, false, false, "never")
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 {
		t.Fatalf("expected exit 2 on empty re-read, got %d", code)
	}
}

func TestReadWithoutAdvanceKeepsCursor(t *testing.T) {
	db := newTestDB(t)
	m := NewMessage("bob", "#dev", []byte("a"))
	m.Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}, Cursors: map[string]string{}}
	var buf bytes.Buffer
	code, err := Read(&buf, db, st, 0, false, false, false, false, "never")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if st.CursorFor("#dev") != "" {
		t.Fatalf("non-advancing read should not modify cursor; got %q", st.CursorFor("#dev"))
	}
}

func TestSendToChannel(t *testing.T) {
	db := newTestDB(t)
	if err := Send(db, "alice", "#dev", "hello", "", false); err != nil {
		t.Fatal(err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM messages WHERE target = '#dev'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 row in #dev, got %d", count)
	}
}

func TestSendReplyChainsToParent(t *testing.T) {
	db := newTestDB(t)
	parent := NewMessage("alice", "#dev", []byte("question"))
	parent.Save(db)
	if err := Send(db, "bob", parent.ID, "answer", "", false); err != nil {
		t.Fatal(err)
	}
	var depth int
	var root, parentID string
	err := db.QueryRow("SELECT chain_depth, chain_root, parent_id FROM messages WHERE sender = 'bob'").Scan(&depth, &root, &parentID)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 1 {
		t.Fatalf("reply depth should be 1, got %d", depth)
	}
	if root != parent.ID {
		t.Fatalf("reply root should be parent ID, got %s", root)
	}
	if parentID != parent.ID {
		t.Fatalf("parent_id should be %s, got %s", parent.ID, parentID)
	}
}

func TestSendRejectsDepthOverflow(t *testing.T) {
	db := newTestDB(t)
	root := NewMessage("a", "#deep", []byte("0"))
	root.Save(db)
	current := root
	// Build a 9-deep chain manually.
	for depth := 1; depth <= 8; depth++ {
		m := NewMessage("a", "#deep", []byte("x"))
		m.ChainRoot = root.ID
		m.ChainDepth = depth
		m.ParentID = &current.ID
		if err := m.Save(db); err != nil {
			t.Fatal(err)
		}
		current = m
	}
	// 9th level should be rejected via Send.
	err := Send(db, "a", current.ID, "ninth", "", false)
	if err == nil {
		t.Fatal("expected chain depth error")
	}
	if !strings.Contains(err.Error(), "chain depth") {
		t.Fatalf("expected chain depth error, got %v", err)
	}
}

func TestGrepFiltersByPattern(t *testing.T) {
	db := newTestDB(t)
	Send(db, "a", "#dev", "apple pie", "", false)
	Send(db, "a", "#dev", "banana bread", "", false)
	Send(db, "a", "#dev", "apricot tart", "", false)

	var buf bytes.Buffer
	if err := Grep(&buf, db, "a", []string{"#dev"}, "^ap", "", 0, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "apple") || !strings.Contains(out, "apricot") {
		t.Fatalf("expected apple+apricot: %q", out)
	}
	if strings.Contains(out, "banana") {
		t.Fatalf("banana should not match ^ap: %q", out)
	}
}

func TestLogByChannel(t *testing.T) {
	db := newTestDB(t)
	Send(db, "a", "#c1", "one", "", false)
	Send(db, "a", "#c1", "two", "", false)
	Send(db, "a", "#c2", "three", "", false)

	var buf bytes.Buffer
	if err := Log(&buf, db, "a", "#c1", 0, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Count(out, "\n")
	if lines != 2 {
		t.Fatalf("expected 2 messages in #c1, got %d in %q", lines, out)
	}
	if strings.Contains(out, "three") {
		t.Fatalf("should not include #c2 message: %q", out)
	}
}

func TestLogByThread(t *testing.T) {
	db := newTestDB(t)
	root := NewMessage("a", "#t", []byte("root"))
	root.Save(db)
	for i := 0; i < 3; i++ {
		Send(db, "a", root.ID, "reply", "", false)
	}

	var buf bytes.Buffer
	if err := Log(&buf, db, "a", root.ID[:8], 0, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Count(out, "\n") != 4 {
		t.Fatalf("expected 4 messages in thread, got %q", out)
	}
}

func TestMarkAddListDelete(t *testing.T) {
	db := newTestDB(t)
	m := NewMessage("a", "#c", []byte("hi"))
	m.Save(db)
	var buf bytes.Buffer
	if err := Mark(&buf, db, "a", m.ID, false, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := Mark(&buf, db, "a", "", false, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), m.ID[:6]) {
		t.Fatalf("mark list should include %s, got %q", m.ID, buf.String())
	}
	if err := Mark(&buf, db, "a", m.ID, true, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := Mark(&buf, db, "a", "", false, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), m.ID[:6]) {
		t.Fatalf("mark list should not include %s after delete, got %q", m.ID, buf.String())
	}
}

func TestInitGeneratesIdentityWhenAsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	name, err := Init(p, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "-") {
		t.Fatalf("generated identity should contain dashes: %q", name)
	}
	s, err := LoadState(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != name {
		t.Fatalf("state name should match returned name; %s vs %s", s.Name, name)
	}
}

func TestInitRespectsForce(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if _, err := Init(p, "first", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(p, "second", false); err == nil {
		t.Fatal("without --force, second init should fail")
	}
	name, err := Init(p, "second", true)
	if err != nil {
		t.Fatal(err)
	}
	if name != "second" {
		t.Fatalf("force should overwrite: %s", name)
	}
}
