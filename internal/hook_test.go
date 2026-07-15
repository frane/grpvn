package internal

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnreadLine(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	line, err := UnreadLine(db, st)
	if err != nil {
		t.Fatal(err)
	}
	if line != "" {
		t.Fatalf("expected empty line on quiet DB, got %q", line)
	}
	NewMessage("bob", "#dev", []byte("hi")).Save(db)
	line, err = UnreadLine(db, st)
	if err != nil {
		t.Fatal(err)
	}
	if line != "1 #dev" {
		t.Fatalf("expected \"1 #dev\", got %q", line)
	}
}

func TestHookSessionStartInjectsIdentityAndUnread(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}, DefaultChannel: "#dev"}
	NewMessage("bob", "#dev", []byte("hi")).Save(db)
	var buf bytes.Buffer
	if err := HookSessionStart(&buf, db, st, DialectClaude); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"alice", "#dev", "Unread now: 1 #dev"} {
		if !strings.Contains(out, want) {
			t.Fatalf("session-start output missing %q: %q", want, out)
		}
	}
}

func TestHookSessionStartWarnsOnEmptyFollow(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice"}
	var buf bytes.Buffer
	if err := HookSessionStart(&buf, db, st, DialectClaude); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no channels") {
		t.Fatalf("expected empty-follow warning, got %q", buf.String())
	}
}

func TestHookPromptSilentWhenNothingUnread(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	var buf bytes.Buffer
	if err := HookPrompt(&buf, db, st, DialectClaude); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("prompt hook must stay silent with no unread, got %q", buf.String())
	}
	NewMessage("bob", "#dev", []byte("hi")).Save(db)
	if err := HookPrompt(&buf, db, st, DialectClaude); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "1 #dev") {
		t.Fatalf("expected unread counts, got %q", buf.String())
	}
}

func TestHookPostToolThrottlesAndEmitsJSON(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}
	marker := filepath.Join(t.TempDir(), ".posttool-alice")

	// Silent when nothing is unread, and no marker gets written.
	var buf bytes.Buffer
	if err := HookPostTool(&buf, db, st, marker, time.Minute, DialectClaude); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("posttool must stay silent with no unread, got %q", buf.String())
	}

	NewMessage("bob", "#dev", []byte("hi")).Save(db)
	if err := HookPostTool(&buf, db, st, marker, time.Minute, DialectClaude); err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("posttool output not valid JSON: %v\n%s", err, buf.String())
	}
	if out.HookSpecificOutput.HookEventName != "PostToolUse" {
		t.Fatalf("wrong hookEventName: %q", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "1 #dev") {
		t.Fatalf("additionalContext missing counts: %q", out.HookSpecificOutput.AdditionalContext)
	}

	// Within the throttle window the hook stays silent even with unread.
	buf.Reset()
	if err := HookPostTool(&buf, db, st, marker, time.Minute, DialectClaude); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("second nudge inside throttle window: %q", buf.String())
	}

	// Once the window has passed the nudge fires again.
	buf.Reset()
	if err := HookPostTool(&buf, db, st, marker, time.Nanosecond, DialectClaude); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected a nudge after the throttle window elapsed")
	}
}

func TestDoctorFlagsDeadSetups(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	newTestDB(t)
	empty := &State{Name: "holy-fox"}
	emptyPath := filepath.Join(home, ".grpvn", "state-claude-code.json")
	if err := empty.Save(emptyPath); err != nil {
		t.Fatal(err)
	}
	full := &State{Name: "green-lynx", Follow: []string{"#dev"}}
	fullPath := filepath.Join(home, ".grpvn", "state.json")
	if err := full.Save(fullPath); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Doctor(&buf, home, fullPath); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "holy-fox follows no channels") {
		t.Fatalf("doctor should flag the empty-follow identity: %q", out)
	}
	if !strings.Contains(out, "2 identities") {
		t.Fatalf("doctor should note multiple identities: %q", out)
	}
	if !strings.Contains(out, "no unread for green-lynx") {
		t.Fatalf("doctor should report unread for the active identity: %q", out)
	}
}

// Every dialect wraps the same text in its runtime's envelope: Codex keeps
// Claude's schema including hookEventName, Gemini drops the event name,
// Cursor speaks snake_case at top level.
func TestHookDialectEnvelopes(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}, DefaultChannel: "#dev"}
	NewMessage("bob", "#dev", []byte("hi")).Save(db)

	decode := func(buf *bytes.Buffer) map[string]interface{} {
		t.Helper()
		var doc map[string]interface{}
		if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
			t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
		}
		return doc
	}

	var buf bytes.Buffer
	if err := HookSessionStart(&buf, db, st, DialectCodex); err != nil {
		t.Fatal(err)
	}
	hso := decode(&buf)["hookSpecificOutput"].(map[string]interface{})
	if hso["hookEventName"] != "SessionStart" || !strings.Contains(hso["additionalContext"].(string), "alice") {
		t.Fatalf("codex session-start envelope wrong: %v", hso)
	}

	buf.Reset()
	if err := HookPrompt(&buf, db, st, DialectGemini); err != nil {
		t.Fatal(err)
	}
	hso = decode(&buf)["hookSpecificOutput"].(map[string]interface{})
	if _, hasEvent := hso["hookEventName"]; hasEvent {
		t.Fatalf("gemini envelope must not carry hookEventName: %v", hso)
	}
	if !strings.Contains(hso["additionalContext"].(string), "1 #dev") {
		t.Fatalf("gemini prompt envelope missing counts: %v", hso)
	}

	buf.Reset()
	marker := filepath.Join(t.TempDir(), ".posttool-alice")
	if err := HookPostTool(&buf, db, st, marker, time.Minute, DialectCursor); err != nil {
		t.Fatal(err)
	}
	doc := decode(&buf)
	if !strings.Contains(doc["additional_context"].(string), "1 #dev") {
		t.Fatalf("cursor posttool envelope wrong: %v", doc)
	}

	// Cursor has no context-injecting prompt event; wiring it is a mistake
	// the hook should refuse rather than silently no-op.
	if err := HookPrompt(&bytes.Buffer{}, db, st, DialectCursor); err == nil {
		t.Fatal("cursor prompt dialect should be rejected")
	}
}

func TestHookStopDialects(t *testing.T) {
	db := newTestDB(t)
	st := &State{Name: "alice", Follow: []string{"#dev"}}

	// Silent when nothing is unread.
	var buf bytes.Buffer
	if err := HookStop(&buf, db, st, DialectClaude, false, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stop must stay silent with no unread, got %q", buf.String())
	}

	NewMessage("bob", "#dev", []byte("hi")).Save(db)

	// stop_hook_active suppresses the nudge so the agent is never trapped.
	if err := HookStop(&buf, db, st, DialectClaude, true, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stop_hook_active must suppress the block, got %q", buf.String())
	}

	if err := HookStop(&buf, db, st, DialectClaude, false, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	var claude struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(buf.Bytes(), &claude); err != nil {
		t.Fatalf("claude stop output not valid JSON: %v\n%s", err, buf.String())
	}
	if claude.Decision != "block" || !strings.Contains(claude.Reason, "1 #dev") {
		t.Fatalf("claude stop envelope wrong: %+v", claude)
	}

	// Cursor gets a followup message instead of a block decision.
	buf.Reset()
	if err := HookStop(&buf, db, st, DialectCursor, false, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	var cursor struct {
		Followup string `json:"followup_message"`
	}
	if err := json.Unmarshal(buf.Bytes(), &cursor); err != nil {
		t.Fatalf("cursor stop output not valid JSON: %v\n%s", err, buf.String())
	}
	if !strings.Contains(cursor.Followup, "1 #dev") {
		t.Fatalf("cursor followup missing counts: %q", cursor.Followup)
	}

	// Codex has no stop_hook_active equivalent, so the marker throttles
	// consecutive blocks — the anti-loop brake.
	buf.Reset()
	marker := filepath.Join(t.TempDir(), ".stop-alice")
	if err := HookStop(&buf, db, st, DialectCodex, false, marker, time.Minute); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("first codex stop should block")
	}
	buf.Reset()
	if err := HookStop(&buf, db, st, DialectCodex, false, marker, time.Minute); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("second codex stop inside the window must be throttled, got %q", buf.String())
	}

	// Gemini has no safe stop event; wiring it is refused.
	if err := HookStop(&bytes.Buffer{}, db, st, DialectGemini, false, "", time.Minute); err == nil {
		t.Fatal("gemini stop dialect should be rejected")
	}
}
