package internal

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cursorPos reads an agent's cursor position straight from the cursors
// table; 0 = no cursor row, everything unread.
func cursorPos(t *testing.T, db *sql.DB, agent, target string) int64 {
	t.Helper()
	var pos int64
	err := db.QueryRow("SELECT COALESCE(MAX(position), 0) FROM cursors WHERE agent_name = ? AND target = ?", agent, target).Scan(&pos)
	if err != nil {
		t.Fatal(err)
	}
	return pos
}

func TestCheckEmptyExits2(t *testing.T) {
	db := newTestDB(t)
	var buf bytes.Buffer
	st := &State{Name: "alice", Follow: []string{"#dev"}}
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

	st := &State{Name: "alice", Follow: []string{"#dev"}}
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

	st := &State{Name: "alice", Follow: []string{"#dev"}}
	var buf bytes.Buffer
	code, err := Read(&buf, db, st, 0, true, false, false, false, "never")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if pos := cursorPos(t, db, "alice", "#dev"); pos != m2.Seq {
		t.Fatalf("cursor for #dev should advance to seq %d, got %d", m2.Seq, pos)
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
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	var buf bytes.Buffer
	code, err := Read(&buf, db, st, 0, false, false, false, false, "never")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if pos := cursorPos(t, db, "alice", "#dev"); pos != 0 {
		t.Fatalf("non-advancing read should not modify cursor; got %d", pos)
	}
}

func TestReadOneTargetLeavesOthersUnread(t *testing.T) {
	db := newTestDB(t)
	dev := NewMessage("bob", "#dev", []byte("parser"))
	dev.Save(db)
	ops := NewMessage("bob", "#ops", []byte("deploy"))
	ops.Save(db)
	dm := NewMessage("bob", "@alice", []byte("hey"))
	dm.Save(db)

	st := &State{Name: "alice", Follow: []string{"#dev", "#ops"}}
	var buf bytes.Buffer
	code, err := Read(&buf, db, st, 0, true, false, false, false, "never", "#dev")
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("expected 0, got %d", code)
	}
	if !strings.Contains(buf.String(), "parser") {
		t.Fatalf("expected #dev body, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "deploy") || strings.Contains(buf.String(), "hey") {
		t.Fatalf("r '#dev' must not print other targets: %q", buf.String())
	}
	if pos := cursorPos(t, db, "alice", "#dev"); pos != dev.Seq {
		t.Fatalf("#dev cursor should advance to %d, got %d", dev.Seq, pos)
	}
	if pos := cursorPos(t, db, "alice", "#ops"); pos != 0 {
		t.Fatalf("#ops cursor should stay 0, got %d", pos)
	}
	if pos := cursorPos(t, db, "alice", "@alice"); pos != 0 {
		t.Fatalf("DM cursor should stay 0, got %d", pos)
	}

	buf.Reset()
	code, err = Read(&buf, db, st, 0, true, false, false, false, "never", "@me")
	if err != nil || code != 0 {
		t.Fatalf("r @me: code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), "hey") {
		t.Fatalf("expected DM body, got %q", buf.String())
	}
	if pos := cursorPos(t, db, "alice", "@alice"); pos != dm.Seq {
		t.Fatalf("DM cursor should advance to %d, got %d", dm.Seq, pos)
	}
	if pos := cursorPos(t, db, "alice", "#ops"); pos != 0 {
		t.Fatalf("#ops must still be unread, got cursor %d", pos)
	}
}

func TestReadRejectsUnfollowedTarget(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	var buf bytes.Buffer
	_, err := Read(&buf, db, st, 0, true, false, false, false, "never", "#ops")
	if err == nil || !strings.Contains(err.Error(), "not following") {
		t.Fatalf("expected not-following error, got %v", err)
	}
}

func TestCheckActionableOmitsChannelChatter(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev", "#ops"}}
	NewMessage("bob", "#dev", []byte("noise")).Save(db)
	NewMessage("bob", "#ops", []byte("alice please look")).Save(db)
	NewMessage("bob", "@alice", []byte("dm")).Save(db)

	var buf bytes.Buffer
	code, err := Check(&buf, db, st)
	if err != nil || code != 0 {
		t.Fatalf("check: code=%d err=%v", code, err)
	}
	all := buf.String()
	if !strings.Contains(all, "1 #dev") || !strings.Contains(all, "1 #ops") || !strings.Contains(all, "1 @me") {
		t.Fatalf("full check should list every target, got %q", all)
	}

	buf.Reset()
	code, err = CheckActionable(&buf, db, st)
	if err != nil || code != 0 {
		t.Fatalf("actionable: code=%d err=%v", code, err)
	}
	got := buf.String()
	if !strings.Contains(got, "1 @me") {
		t.Fatalf("DM must be actionable: %q", got)
	}
	if !strings.Contains(got, "1 #ops") {
		t.Fatalf("mention must be actionable: %q", got)
	}
	if strings.Contains(got, "#dev") {
		t.Fatalf("channel chatter must not be actionable: %q", got)
	}
}

func TestCheckActionableIncludesReplyToMe(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	mine, err := Send(db, "alice", "#dev", "starting parser", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Send(db, "bob", mine.ID, "looks good", "", false); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code, err := CheckActionable(&buf, db, st)
	if err != nil || code != 0 {
		t.Fatalf("actionable: code=%d err=%v out=%q", code, err, buf.String())
	}
	if !strings.Contains(buf.String(), "1 #dev") {
		t.Fatalf("reply to alice should be actionable: %q", buf.String())
	}
}

func TestSendIdempotentRetriesReturnOriginal(t *testing.T) {
	db := newTestDB(t)
	first, replayed, err := SendIdempotent(db, "alice", "#dev", "hello", "", false, "recon-1")
	if err != nil || replayed {
		t.Fatalf("first send: replayed=%v err=%v", replayed, err)
	}
	second, replayed, err := SendIdempotent(db, "alice", "#dev", "hello again", "", false, "recon-1")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed {
		t.Fatal("second send with the same key must be a replay")
	}
	if second.ID != first.ID {
		t.Fatalf("replayed id %s != original %s", second.ID, first.ID)
	}
	if string(second.Body) != "hello" {
		t.Fatalf("replay must return the original body, got %q", second.Body)
	}
	var n int
	db.QueryRow("SELECT COUNT(*) FROM messages WHERE target = '#dev'").Scan(&n)
	if n != 1 {
		t.Fatalf("store should still have 1 row, got %d", n)
	}
	other, replayed, err := SendIdempotent(db, "alice", "#dev", "other", "", false, "recon-2")
	if err != nil || replayed {
		t.Fatalf("different key should insert: replayed=%v err=%v", replayed, err)
	}
	if other.ID == first.ID {
		t.Fatal("different key must be a new message")
	}
}

func TestSendIdempotentIsPerSender(t *testing.T) {
	db := newTestDB(t)
	a, _, err := SendIdempotent(db, "alice", "#dev", "from alice", "", false, "k")
	if err != nil {
		t.Fatal(err)
	}
	b, replayed, err := SendIdempotent(db, "bob", "#dev", "from bob", "", false, "k")
	if err != nil || replayed {
		t.Fatalf("bob with alice's key should insert: replayed=%v err=%v", replayed, err)
	}
	if a.ID == b.ID {
		t.Fatal("idempotency keys are per sender")
	}
}

func TestLogLimitReturnsTail(t *testing.T) {
	db := newTestDB(t)
	for _, body := range []string{"one", "two", "three"} {
		if _, err := Send(db, "a", "#dev", body, "", false); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := Log(&buf, db, "alice", "#dev", 2, "", false, false, false, "never"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "one") {
		t.Fatalf("limit 2 should drop the oldest, got %q", out)
	}
	if !strings.Contains(out, "two") || !strings.Contains(out, "three") {
		t.Fatalf("limit 2 should keep the tail, got %q", out)
	}
}

func TestSendToChannel(t *testing.T) {
	db := newTestDB(t)
	if _, err := Send(db, "alice", "#dev", "hello", "", false); err != nil {
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
	if _, err := Send(db, "bob", parent.ID, "answer", "", false); err != nil {
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
	_, err := Send(db, "a", current.ID, "ninth", "", false)
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

// A limited grep used to break out of the row loop with the rows still open,
// which held the pool's single connection while RenderBatch asked for a
// second one: the process hung instead of printing.
func TestGrepWithLimitDoesNotDeadlock(t *testing.T) {
	db := newTestDB(t)
	Send(db, "a", "#dev", "apple pie", "", false)
	Send(db, "a", "#dev", "apple tart", "", false)
	Send(db, "a", "#dev", "apple cake", "", false)

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Grep(&buf, db, "a", []string{"#dev"}, "^apple", "", 1, "", false, false, false, "never")
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("grep with a limit did not return: the row cursor still holds the only connection")
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Fatalf("expected 1 message for -n 1, got %d in %q", n, buf.String())
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
	// Full ID: the replies were minted in the same millisecond, so a short
	// prefix is genuinely ambiguous and is now rejected rather than guessed.
	if err := Log(&buf, db, "a", root.ID, 0, "", false, false, false, "never"); err != nil {
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
