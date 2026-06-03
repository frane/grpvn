package internal

import (
	"bytes"
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
	RenderAI(&buf, m, "alice", "#dev", false, false)
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
	RenderAI(&buf, m, "alice", "#dev", false, true)
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
	RenderAI(&buf, m, "alice", "", false, false)
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
	RenderAI(&buf, m, "alice", "#dev", false, false)
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
	RenderAI(&buf, m, "alice", "#dev", false, false)
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
