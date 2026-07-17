package internal

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Hook output dialects. Codex copied Claude Code's hook schema wholesale
// (hookSpecificOutput with hookEventName), Gemini uses the same envelope
// minus the event name, and Cursor speaks snake_case top-level fields.
const (
	DialectClaude = "claude"
	DialectCodex  = "codex"
	DialectGemini = "gemini"
	DialectCursor = "cursor"
)

// UnreadLine runs Check and returns its counts line ("1 #dev 2 @me"), or ""
// when nothing is unread. It never advances cursors.
func UnreadLine(db *sql.DB, st *State) (string, error) {
	var buf bytes.Buffer
	code, err := Check(&buf, db, st)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", nil
	}
	return strings.TrimSpace(buf.String()), nil
}

// contextPayload wraps injected context in the dialect's JSON envelope.
// event is the runtime's own event name, required by the Claude/Codex
// schema and ignored by the others.
func contextPayload(dialect, event, text string) (string, error) {
	var doc map[string]interface{}
	switch dialect {
	case DialectClaude, DialectCodex:
		doc = map[string]interface{}{
			"hookSpecificOutput": map[string]interface{}{
				"hookEventName":     event,
				"additionalContext": text,
			},
		}
	case DialectGemini:
		doc = map[string]interface{}{
			"hookSpecificOutput": map[string]interface{}{
				"additionalContext": text,
			},
		}
	case DialectCursor:
		doc = map[string]interface{}{"additional_context": text}
	default:
		return "", fmt.Errorf("unknown hook format %q", dialect)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// HookSessionStart writes the session-start context block: who this agent
// is on grpvn, what it follows, and what is already waiting. Claude Code
// injects a SessionStart hook's plain stdout; the other runtimes take the
// same text through their JSON envelope. This is the one moment identity
// gets in front of the model without the model asking for it.
func HookSessionStart(w io.Writer, db *sql.DB, st *State, dialect string) error {
	follows := "no channels (run `grpvn follow '#channel'` to subscribe)"
	if len(st.Follow) > 0 {
		follows = strings.Join(st.Follow, " ")
	}
	var text strings.Builder
	fmt.Fprintf(&text, "[grpvn] You are %s on the local agent chat, following: %s.", st.Name, follows)
	if st.DefaultChannel != "" {
		fmt.Fprintf(&text, " Default channel: %s.", st.DefaultChannel)
	}
	line, err := UnreadLine(db, st)
	if err != nil {
		return err
	}
	if line != "" {
		fmt.Fprintf(&text, " Unread now: %s — read with the grpvn r tool.", line)
	}
	text.WriteString(" Coordinate substantive work with the other agents via grpvn (s to send, q to ask, r to read).")
	text.WriteString(" If your runtime supports background shell tasks, arm the doorbell now: start `grpvn w --timeout 0` as a background task — it exits the moment a message arrives, waking you; read with r, reply, then re-arm it. One armed waiter per session, never a polling loop.")
	if dialect == DialectClaude {
		fmt.Fprintln(w, text.String())
		return nil
	}
	out, err := contextPayload(dialect, "SessionStart", text.String())
	if err != nil {
		return err
	}
	fmt.Fprintln(w, out)
	return nil
}

// HookPrompt writes one unread notice at turn start (Claude Code
// UserPromptSubmit, Codex UserPromptSubmit, Gemini BeforeAgent) and nothing
// when the inbox is clean. Cursor has no context-injecting prompt event, so
// that dialect is rejected rather than silently wired to nothing.
func HookPrompt(w io.Writer, db *sql.DB, st *State, dialect string) error {
	if dialect == DialectCursor {
		return fmt.Errorf("cursor has no prompt-injection hook event")
	}
	line, err := UnreadLine(db, st)
	if err != nil {
		return err
	}
	if line == "" {
		return nil
	}
	text := fmt.Sprintf("[grpvn] Unread messages: %s — read them with the grpvn r tool and reply to any questions before proceeding.", line)
	if dialect == DialectClaude {
		fmt.Fprintln(w, text)
		return nil
	}
	out, err := contextPayload(dialect, "UserPromptSubmit", text)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, out)
	return nil
}

// HookPostTool emits a mid-turn unread notice (Claude/Codex PostToolUse,
// Gemini AfterTool, Cursor postToolUse), throttled through the marker
// file's mtime so a burst of tool calls yields one nudge per `every`
// window. It runs after every tool call, so the quiet path — stat the
// marker, one COUNT query — must stay sub-millisecond, and silence is the
// default: no unread, no output.
func HookPostTool(w io.Writer, db *sql.DB, st *State, marker string, every time.Duration, dialect string) error {
	if fi, err := os.Stat(marker); err == nil && time.Since(fi.ModTime()) < every {
		return nil
	}
	line, err := UnreadLine(db, st)
	if err != nil {
		return err
	}
	if line == "" {
		return nil
	}
	text := fmt.Sprintf("[grpvn] Unread messages: %s — read them with the grpvn r tool at the next good stopping point.", line)
	out, err := contextPayload(dialect, "PostToolUse", text)
	if err != nil {
		return err
	}
	// Touch the marker before emitting: if two hook invocations race, the
	// worst case is one duplicate nudge, and a marker write failure must
	// not suppress the notice.
	_ = os.WriteFile(marker, []byte{}, 0644)
	fmt.Fprintln(w, out)
	return nil
}

// HookStop handles the end-of-turn nudge. Claude Code and Codex take
// {"decision": "block"}; Cursor takes a followup_message it auto-submits
// (bounded by its own loop_limit). Gemini's nearest event retries the whole
// response on deny — a loop with no brake — so that dialect is rejected and
// the installer never wires it.
//
// Loop safety differs per runtime. Claude Code sets stopHookActive when the
// agent is already continuing because a stop hook blocked it; honoring it
// caps the nudge at once per natural stop. Codex documents no such flag, so
// the marker file throttles blocks to one per `window` as the brake — pass
// an empty marker (Claude, Cursor) to skip that guard.
func HookStop(w io.Writer, db *sql.DB, st *State, dialect string, stopHookActive bool, marker string, window time.Duration) error {
	if dialect == DialectGemini {
		return fmt.Errorf("gemini has no safe stop hook event (AfterTool/BeforeAgent carry the nudges instead)")
	}
	if stopHookActive {
		return nil
	}
	if marker != "" {
		if fi, err := os.Stat(marker); err == nil && time.Since(fi.ModTime()) < window {
			return nil
		}
	}
	line, err := UnreadLine(db, st)
	if err != nil {
		return err
	}
	if line == "" {
		return nil
	}
	reason := fmt.Sprintf("Unread grpvn messages: %s. Read them with the grpvn r tool (or `grpvn r`) and reply to any questions before stopping.", line)
	var doc map[string]interface{}
	switch dialect {
	case DialectClaude, DialectCodex:
		doc = map[string]interface{}{"decision": "block", "reason": reason}
	case DialectCursor:
		doc = map[string]interface{}{"followup_message": reason}
	default:
		return fmt.Errorf("unknown hook format %q", dialect)
	}
	if marker != "" {
		_ = os.WriteFile(marker, []byte{}, 0644)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(out))
	return nil
}
