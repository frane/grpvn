package internal

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderAIShortenID(t *testing.T) {
	m := &Message{
		ID:        "01HQ7P0000000000000000000A",
		Sender:    "bob",
		Target:    "#dev",
		Body:      []byte("hello"),
		ChainRoot: "01HQ7P0000000000000000000A",
		CreatedAt: time.Now().UnixMilli(),
	}
	var buf bytes.Buffer
	RenderAI(&buf, m, "alice", "#dev", false, false, 6)
	out := buf.String()
	if !strings.HasPrefix(out, "01HQ7P ") {
		t.Fatalf("expected 6-char ID prefix, got %q", out)
	}
	if strings.Contains(out, "#dev") {
		t.Fatalf("default channel should be omitted: %q", out)
	}
	if !strings.Contains(out, "bob: hello") {
		t.Fatalf("expected sender and body: %q", out)
	}
}

func TestRenderAIFullID(t *testing.T) {
	m := &Message{
		ID:        "01HQ7P0000000000000000000A",
		Sender:    "bob",
		Target:    "#other",
		Body:      []byte("hi"),
		ChainRoot: "01HQ7P0000000000000000000A",
	}
	var buf bytes.Buffer
	RenderAI(&buf, m, "alice", "#dev", false, true, 6)
	out := buf.String()
	if !strings.HasPrefix(out, m.ID+" ") {
		t.Fatalf("expected full ULID, got %q", out)
	}
	if !strings.Contains(out, "[#other]") {
		t.Fatalf("non-default channel should appear in brackets: %q", out)
	}
}

func TestRenderAISelfBecomesMe(t *testing.T) {
	m := &Message{
		ID:        "01HQ7P0000000000000000000A",
		Sender:    "bob",
		Target:    "@alice",
		Body:      []byte("hi"),
		ChainRoot: "01HQ7P0000000000000000000A",
	}
	var buf bytes.Buffer
	RenderAI(&buf, m, "alice", "", false, false, 6)
	if !strings.Contains(buf.String(), "[@me]") {
		t.Fatalf("@alice should render as @me when self=alice: %q", buf.String())
	}
}

func TestRenderAIReplyTrailer(t *testing.T) {
	parent := "01HQ7PAAAAAAAAAAAAAAAAAAAB"
	m := &Message{
		ID:        "01HQ7P0000000000000000000A",
		Sender:    "bob",
		Target:    "#dev",
		Body:      []byte("ack"),
		ChainRoot: parent,
		ParentID:  &parent,
	}
	var buf bytes.Buffer
	RenderAI(&buf, m, "alice", "#dev", false, false, 6)
	if !strings.Contains(buf.String(), "reply:01HQ7P") {
		t.Fatalf("expected reply trailer, got %q", buf.String())
	}
}

func TestRenderAIMultiline(t *testing.T) {
	m := &Message{
		ID:        "01HQ7P0000000000000000000A",
		Sender:    "bob",
		Target:    "#dev",
		Body:      []byte("line1\nline2\nline3"),
		ChainRoot: "01HQ7P0000000000000000000A",
	}
	var buf bytes.Buffer
	RenderAI(&buf, m, "alice", "#dev", false, false, 6)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[1], "  line2") || !strings.HasPrefix(lines[2], "  line3") {
		t.Fatalf("continuation lines should be indented: %q", buf.String())
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Now().UnixMilli()
	cases := []struct {
		label string
		ago   time.Duration
		want  string
	}{
		{"seconds", 5 * time.Second, "5s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 3 * time.Hour, "3h"},
		{"days", 48 * time.Hour, "2d"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			out := RelativeTime(now - c.ago.Milliseconds())
			if out != c.want {
				t.Fatalf("expected %s, got %s", c.want, out)
			}
		})
	}
}

func TestShouldColorExplicit(t *testing.T) {
	if !ShouldColor("always") {
		t.Fatal("always should enable color")
	}
	if ShouldColor("never") {
		t.Fatal("never should disable color")
	}
}

func TestColorWrapping(t *testing.T) {
	out := C("hi", ColorBold, true)
	if !strings.HasPrefix(out, ColorBold) || !strings.HasSuffix(out, ColorReset) {
		t.Fatalf("expected escape sequence wrap, got %q", out)
	}
	out = C("hi", ColorBold, false)
	if out != "hi" {
		t.Fatalf("disabled color should be pass-through, got %q", out)
	}
}

// Messages minted in the same minute share well past six ULID chars; the
// batch must stretch prefixes until reply targets are unambiguous.
func TestRenderBatchUniquePrefixes(t *testing.T) {
	db := newTestDB(t)
	ids := []string{
		"01KXN0AAAA0000000000000001",
		"01KXN0AAAA0000000000000002",
		"01KXN0AAAB0000000000000003",
	}
	for _, id := range ids {
		m := &Message{ID: id, Sender: "bob", Target: "#dev", Body: []byte("x"), ChainRoot: id, CreatedAt: 1}
		if err := m.Save(db); err != nil {
			t.Fatal(err)
		}
	}
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	var buf bytes.Buffer
	if code, err := Read(&buf, db, st, 0, false, false, false, false, "never"); err != nil || code != 0 {
		t.Fatalf("read: code=%d err=%v", code, err)
	}
	out := buf.String()
	// The first two IDs differ at position 26; all three must print with
	// enough chars to distinguish (25-char common prefix → 26 chars).
	for _, want := range []string{"01KXN0AAAA0000000000000001", "01KXN0AAAA0000000000000002"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected unambiguous prefix %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "01KXN0 ") {
		t.Fatalf("6-char ambiguous prefix leaked:\n%s", out)
	}
}

// A lone message keeps the compact 6-char prefix.
func TestRenderBatchShortPrefixWhenUnambiguous(t *testing.T) {
	db := newTestDB(t)
	NewMessage("bob", "#dev", []byte("hi")).Save(db)
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	var buf bytes.Buffer
	if code, err := Read(&buf, db, st, 0, false, false, false, false, "never"); err != nil || code != 0 {
		t.Fatalf("read: code=%d err=%v", code, err)
	}
	id := strings.Fields(buf.String())[0]
	if len(id) != 6 {
		t.Fatalf("single message should render a 6-char prefix, got %q", id)
	}
}

// Posting into a channel subscribes the sender; DMs and known channels
// don't.
func TestAutoFollow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	added, err := AutoFollow(st, path, "#fsd")
	if err != nil || !added {
		t.Fatalf("expected follow added, got added=%v err=%v", added, err)
	}
	saved, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Follow) != 2 || saved.Follow[1] != "#fsd" {
		t.Fatalf("follow not persisted: %#v", saved.Follow)
	}
	if added, _ := AutoFollow(st, path, "#fsd"); added {
		t.Fatal("re-following must be a no-op")
	}
	if added, _ := AutoFollow(st, path, "@bob"); added {
		t.Fatal("DMs must not be followed")
	}
}
