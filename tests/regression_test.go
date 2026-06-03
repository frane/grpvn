package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression: a fresh identity must NOT be minted on every invocation.
//
// Reported by a Claude Desktop session: "I was fancy-owl-77d4, then
// wild-fawn-3549 on my first message, and now holy-bug-216e. Each message
// seems to mint a fresh handle." The root cause was bootstrap writing
// state.json to a cwd that wasn't writable for the MCP host; the save
// failed silently and the next call regenerated.
//
// The fix is two-part: (a) default the state path to $HOME/.grpvn/state.json
// (always writable for a normal HOME), and (b) surface save failures
// instead of swallowing them.
func TestIdentityIsStableAcrossInvocations(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")

	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(binPath, args...)
		// Deliberately point cwd at a tempdir with no .grpvn — verifies that
		// the state file lives under HOME, not cwd.
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"USERPROFILE="+home,
			"GRPVN_DB="+dbPath,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("exec %v: %v", args, err)
			}
		}
		return stdout.String(), stderr.String(), code
	}

	// Three invocations from three different cwds. The identity returned
	// by `id` must be the same all three times.
	identities := []string{}
	for i := 0; i < 3; i++ {
		out, stderr, code := run("id")
		if code != 0 {
			t.Fatalf("id call %d failed: %s", i, stderr)
		}
		ident := strings.SplitN(strings.TrimSpace(out), "@", 2)[0]
		identities = append(identities, ident)
	}
	if identities[0] == "" {
		t.Fatal("id should print a non-empty identity")
	}
	if identities[1] != identities[0] || identities[2] != identities[0] {
		t.Fatalf("identity mint loop: %v", identities)
	}
	// And the state file must actually exist at $HOME/.grpvn/state.json.
	statePath := filepath.Join(home, ".grpvn", "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state file should exist at %s: %v", statePath, err)
	}
	if !strings.Contains(string(data), identities[0]) {
		t.Fatalf("state file should contain identity %q: %s", identities[0], data)
	}
}

// Regression: $GRPVN_STATE override gives each agent runtime its own
// identity even though they share $HOME. This is the contract the skill
// installer relies on when it writes env:{GRPVN_STATE} per agent.
func TestPerAgentStateGivesDistinctIdentities(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")

	runAs := func(agentStatePath string) string {
		cmd := exec.Command(binPath, "id")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(),
			"HOME="+home,
			"USERPROFILE="+home,
			"GRPVN_DB="+dbPath,
			"GRPVN_STATE="+agentStatePath,
		)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("exec id with state %s: %v", agentStatePath, err)
		}
		return strings.SplitN(strings.TrimSpace(string(out)), "@", 2)[0]
	}

	a := runAs(filepath.Join(home, ".grpvn", "state-claude.json"))
	b := runAs(filepath.Join(home, ".grpvn", "state-codex.json"))
	if a == "" || b == "" {
		t.Fatalf("expected identities; got %q %q", a, b)
	}
	if a == b {
		t.Fatalf("two GRPVN_STATE paths should mint distinct identities: both %q", a)
	}
	// Repeating each call keeps the same identity.
	if runAs(filepath.Join(home, ".grpvn", "state-claude.json")) != a {
		t.Fatal("claude state path should keep its identity across calls")
	}
	if runAs(filepath.Join(home, ".grpvn", "state-codex.json")) != b {
		t.Fatal("codex state path should keep its identity across calls")
	}
}

// Regression: cursor advancing past unread messages in a scoped channel.
//
// Reported: a reply landed in a channel, an unrelated DM arrived later, a
// read advanced the (then-global) cursor past both, the user followed the
// channel and the reply never showed up under `r` even though `l` saw it.
//
// With per-target cursors the channel's cursor is independent of the DM's.
// Following the channel after the fact must surface every message in it.
func TestPerTargetCursorReadsHistoricalChannelMessages(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	sender := newRunner(t, "sender").withSharedDB(dbPath)
	reader := newRunner(t, "reader").withSharedDB(dbPath)

	// Reply lands in a channel the reader doesn't follow yet.
	sender.mustRun("s", "#ops", "this is the reply you should not lose")
	// A later DM with a higher ULID arrives and the reader consumes it.
	sender.mustRun("s", "@reader", "unrelated dm")
	if _, _, code := reader.run("r"); code != 0 {
		t.Fatalf("reader should see the DM, got exit %d", code)
	}
	// Now the reader follows #ops — the historical reply should appear.
	reader.mustRun("follow", "#ops")
	out, _, code := reader.run("c")
	if code != 0 {
		t.Fatalf("after follow, c should report unread; got exit %d, out=%q", code, out)
	}
	if !strings.Contains(out, "1 #ops") {
		t.Fatalf("expected '1 #ops' unread; got %q", out)
	}
	out = reader.mustRun("r")
	if !strings.Contains(out, "this is the reply you should not lose") {
		t.Fatalf("r should print the historical reply; got %q", out)
	}
}

// Regression: cursor for one target must not advance just because another
// target's cursor moved. Specifically, reading a DM with a high ULID does
// not consume an unread channel message with a lower ULID.
func TestPerTargetCursorIsolatesScopes(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	sender := newRunner(t, "sender").withSharedDB(dbPath)
	reader := newRunner(t, "reader").withSharedDB(dbPath)
	reader.mustRun("follow", "#ops")

	// Channel message first, DM second. Both are unread.
	sender.mustRun("s", "#ops", "channel-1")
	sender.mustRun("s", "@reader", "dm-1")
	// Read consumes BOTH (channel was followed before send), cursors advance.
	out := reader.mustRun("r")
	if !strings.Contains(out, "channel-1") || !strings.Contains(out, "dm-1") {
		t.Fatalf("first read should see both; got %q", out)
	}
	// Now sender posts a NEW channel message; DM cursor must not skip it.
	sender.mustRun("s", "#ops", "channel-2")
	out, _, code := reader.run("c")
	if code != 0 || !strings.Contains(out, "1 #ops") {
		t.Fatalf("expected '1 #ops' unread after new channel post; got exit %d, out=%q", code, out)
	}
}

// Regression: when the legacy single-cursor schema is loaded, the new code
// treats it as the high-watermark for every target. Existing state files
// from v0.1.x must not regress into "everything looks unread" or
// "everything looks read".
func TestLegacyCursorAppliesToAllTargets(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")

	sender := newRunner(t, "sender").withSharedDB(dbPath)
	// Two messages: one in #ops, one DM. Capture the second ULID so the
	// legacy cursor sits between "everything before this is consumed" and
	// "anything new is fresh".
	sender.mustRun("follow", "#ops")
	sender.mustRun("s", "#ops", "old-channel")
	dmULID := strings.TrimSpace(sender.mustRun("q", "@reader", "old-dm"))

	statePath := filepath.Join(home, ".grpvn", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]interface{}{
		"name":            "reader",
		"cursor":          dmULID,
		"follow":          []string{"#ops"},
		"default_channel": "",
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	readerEnv := append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home,
		"GRPVN_DB="+dbPath,
	)
	runReader := func(args ...string) (string, int) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = readerEnv
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("exec %v: %v\nstderr=%s", args, err, stderr.String())
			}
		}
		return stdout.String(), code
	}

	// Pre-cursor messages should be invisible to `c`.
	_, code := runReader("c")
	if code != 2 {
		t.Fatalf("expected exit 2 with legacy cursor at high-watermark; got %d", code)
	}
	// Sender posts new ones; both must show up despite the legacy schema.
	sender.mustRun("s", "#ops", "new-channel")
	sender.mustRun("s", "@reader", "new-dm")
	out, code := runReader("c")
	if code != 0 {
		t.Fatalf("expected 0 with fresh messages; got %d", code)
	}
	if !strings.Contains(out, "1 #ops") || !strings.Contains(out, "1 @me") {
		t.Fatalf("expected '1 #ops' and '1 @me'; got %q", out)
	}

	// After a read, the file must have migrated to the cursors map.
	if _, code = runReader("r"); code != 0 {
		t.Fatalf("r should succeed; got %d", code)
	}
	migrated, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cursor  string            `json:"cursor"`
		Cursors map[string]string `json:"cursors"`
	}
	if err := json.Unmarshal(migrated, &doc); err != nil {
		t.Fatalf("migrated state must be valid JSON: %v\n%s", err, migrated)
	}
	if doc.Cursor != "" {
		t.Fatalf("after read+save, legacy scalar cursor should be cleared; got %q", doc.Cursor)
	}
	if len(doc.Cursors) == 0 {
		t.Fatalf("after read+save, cursors map should be populated; got %#v", doc.Cursors)
	}
}

// Regression: bootstrap surfaces save errors. A read-only state path means
// the next call would otherwise regenerate the identity silently.
func TestBootstrapSurfacesSaveErrors(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		// Windows ACLs ignore unix file modes; os.Chmod(dir, 0500) leaves
		// the directory writable on NTFS, so the save we want to force-fail
		// succeeds. The bootstrap code path itself is OS-independent and is
		// exercised by linux/macos runs.
		t.Skip("cannot force a write failure via os.Chmod on Windows")
	}
	// Point GRPVN_STATE at a path inside a read-only directory.
	roDir := t.TempDir()
	if err := os.Chmod(roDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0700) })

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")

	cmd := exec.Command(binPath, "id")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"HOME="+t.TempDir(),
		"USERPROFILE="+t.TempDir(),
		"GRPVN_DB="+dbPath,
		"GRPVN_STATE="+filepath.Join(roDir, "subdir-that-cannot-be-created", "state.json"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit when state cannot be saved; stdout=%q", stdout.String())
	}
	combined := stderr.String() + stdout.String()
	if !strings.Contains(combined, "save state") {
		t.Fatalf("expected an error mentioning the save failure; got stderr=%q stdout=%q", stderr.String(), stdout.String())
	}
}

// Sanity test for the skill installer's per-agent state path: every MCP
// entry it writes must include env.GRPVN_STATE pointing at a path that
// includes the agent's slug. Without this, identity-mint defeats the rest.
func TestSkillInstallSetsPerAgentStateEnv(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// Pretend Claude Code and Cursor are present.
	for _, d := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(binPath, "skill", "install")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("skill install: %v\n%s", err, out)
	}

	check := func(path, slug string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, path))
		if err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
		var doc struct {
			McpServers map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse %s: %v\n%s", path, err, data)
		}
		srv, ok := doc.McpServers["grpvn"]
		if !ok {
			t.Fatalf("grpvn missing in %s: %s", path, data)
		}
		state := srv.Env["GRPVN_STATE"]
		if state == "" {
			t.Fatalf("%s: env.GRPVN_STATE missing", path)
		}
		want := "state-" + slug + ".json"
		if !strings.HasSuffix(state, want) {
			t.Fatalf("%s: GRPVN_STATE should end with %s, got %s", path, want, state)
		}
		// And it must live somewhere under HOME, not the test cwd.
		if !strings.HasPrefix(state, home) {
			t.Fatalf("%s: GRPVN_STATE should be under HOME (%s), got %s", path, home, state)
		}
	}
	check(".claude.json", "claude-code")
	check(".cursor/mcp.json", "cursor")
}

// Avoid unused-import warnings when reorganising the suite.
var _ = fmt.Sprintf

// Regression: an agent's own outbound messages must not show up in its own
// unread feed. Reported: "01KT6Q green-lynx-7cf5: ... my own message
// echoing back." Without a sender filter, posting to a channel you also
// follow makes your own message unread to yourself.
func TestOwnMessagesAreNotUnreadToSelf(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	a := newRunner(t, "alice").withSharedDB(dbPath)
	a.mustRun("follow", "#mine")

	a.mustRun("s", "#mine", "to myself")

	_, _, code := a.run("c")
	if code != 2 {
		t.Fatalf("agent's own send must not count as unread to itself; got exit %d", code)
	}
	out, _, code := a.run("r")
	if code != 2 {
		t.Fatalf("agent's own send must not appear in r; got exit %d, out=%q", code, out)
	}

	// A different agent in the same channel still sees the message.
	b := newRunner(t, "bob").withSharedDB(dbPath)
	b.mustRun("follow", "#mine")
	out = b.mustRun("r")
	if !strings.Contains(out, "to myself") {
		t.Fatalf("other agent should still see the message; got %q", out)
	}

	// And `l` continues to show your own messages — history is the truth.
	out = a.mustRun("l", "#mine")
	if !strings.Contains(out, "to myself") {
		t.Fatalf("l should include the agent's own messages; got %q", out)
	}
}
