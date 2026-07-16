package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestSkillContentEmbedded(t *testing.T) {
	if len(SkillContent) == 0 {
		t.Fatal("SKILL.md should be embedded at build time")
	}
	if !bytes.Contains(SkillContent, []byte("grpvn")) {
		t.Fatal("SKILL.md should mention grpvn")
	}
}

// InstallSkill must auto-detect: it should write SKILL.md only into targets
// whose DetectDir exists, and silently skip the rest.
func TestInstallSkillDetectsAgents(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	// Pretend the user has Claude Code and Cursor installed; nothing else.
	for _, d := range []string{".claude", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatalf("install: %v", err)
	}
	out := buf.String()
	// SKILL.md written into the detected directories.
	for _, p := range []string{".claude/skills/grpvn/SKILL.md", ".cursor/skills/grpvn/SKILL.md"} {
		got, err := os.ReadFile(filepath.Join(home, p))
		if err != nil {
			t.Fatalf("expected SKILL.md at %s: %v", p, err)
		}
		if !bytes.Equal(got, SkillContent) {
			t.Fatalf("content mismatch at %s", p)
		}
	}
	// Undetected agents have no skill file written.
	if _, err := os.Stat(filepath.Join(home, ".codex/skills/grpvn/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal(".codex skill should not be installed when .codex absent")
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini/skills/grpvn/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal(".gemini skill should not be installed when .gemini absent")
	}
	// Output reports both written and skipped lines.
	if !strings.Contains(out, "skill   Claude Code") {
		t.Fatalf("expected Claude Code skill line: %q", out)
	}
	if !strings.Contains(out, "skip    Codex CLI") {
		t.Fatalf("expected Codex skip line: %q", out)
	}
}

// InstallSkill must merge into existing MCP config files without clobbering
// other entries.
func TestInstallSkillMergesMCPConfig(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing Cursor MCP config with another server.
	existing := []byte(`{"mcpServers":{"other":{"command":"other","args":["foo"]}},"unrelated":42}`)
	if err := os.WriteFile(filepath.Join(home, ".cursor/mcp.json"), existing, 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".cursor/mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("merged config not valid JSON: %v", err)
	}
	if doc["unrelated"].(float64) != 42 {
		t.Fatal("merge clobbered unrelated top-level field")
	}
	servers, ok := doc["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers missing")
	}
	if _, ok := servers["other"]; !ok {
		t.Fatal("merge dropped existing 'other' MCP server")
	}
	grpvn, ok := servers["grpvn"].(map[string]interface{})
	if !ok {
		t.Fatal("grpvn server missing from mcpServers")
	}
	if grpvn["command"] != "grpvn" {
		t.Fatalf("expected command grpvn, got %v", grpvn["command"])
	}
	args, ok := grpvn["args"].([]interface{})
	if !ok || len(args) != 1 || args[0] != "serve" {
		t.Fatalf("expected args [\"serve\"], got %v", grpvn["args"])
	}
}

// InstallSkill must error cleanly when the existing MCP config is malformed,
// not silently overwrite it.
func TestInstallSkillRefusesCorruptMCPConfig(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".cursor/mcp.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := InstallSkill(&buf)
	if err == nil {
		t.Fatal("expected error on corrupt MCP config")
	}
	got, _ := os.ReadFile(filepath.Join(home, ".cursor/mcp.json"))
	if string(got) != "not json" {
		t.Fatalf("config was overwritten despite parse error: %q", got)
	}
}

// InstallSkillAll bypasses detection and installs into every known target.
func TestInstallSkillAllForce(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	var buf bytes.Buffer
	if err := InstallSkillAll(&buf); err != nil {
		t.Fatalf("install --all: %v", err)
	}
	for _, tgt := range HomeTargets() {
		p := filepath.Join(home, tgt.Skill)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist for target %q: %v", p, tgt.Name, err)
		}
	}
}

// Re-running install must be idempotent: same SKILL.md content, same MCP entry
// (no duplicate server keys).
func TestInstallSkillIdempotent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(home, ".cursor/mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(home, ".cursor/mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("install should be idempotent\nfirst:  %s\nsecond: %s", first, second)
	}
}

// The MCP server name in the merged config must always be "grpvn", and the
// command must be the bare binary so PATH resolution kicks in.
func TestInstallSkillMCPEntryShape(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".cursor/mcp.json"))
	var doc struct {
		McpServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	srv, ok := doc.McpServers["grpvn"]
	if !ok {
		t.Fatalf("grpvn missing: %s", data)
	}
	if srv.Command != "grpvn" {
		t.Fatalf("command should be grpvn, got %q", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "serve" {
		t.Fatalf("args should be [\"serve\"], got %v", srv.Args)
	}
}

// The set of targets must be stable and named for human output.
func TestHomeTargetsContainsAllKnownAgents(t *testing.T) {
	names := map[string]bool{}
	for _, t := range HomeTargets() {
		names[t.Name] = true
	}
	for _, want := range []string{"Claude Code", "Cursor", "Codex CLI", "Gemini CLI", "agents (generic)"} {
		if !names[want] {
			t.Fatalf("HomeTargets missing %q; got %v", want, names)
		}
	}
}

// Codex CLI installs SKILL.md and appends [mcp_servers.grpvn] to
// ~/.codex/config.toml when the agent is detected.
func TestInstallSkillWiresCodexTOML(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	existing := "# user config\n[history]\nsize = 1000\n"
	if err := os.WriteFile(filepath.Join(home, ".codex/config.toml"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, "# user config") {
		t.Fatalf("existing TOML content lost: %q", out)
	}
	if !strings.Contains(out, "[history]") || !strings.Contains(out, "size = 1000") {
		t.Fatalf("existing TOML section lost: %q", out)
	}
	if !strings.Contains(out, "[mcp_servers.grpvn]") {
		t.Fatalf("expected [mcp_servers.grpvn] section: %q", out)
	}
	if !strings.Contains(out, `command = "grpvn"`) {
		t.Fatalf("expected command = \"grpvn\": %q", out)
	}
	if !strings.Contains(out, `args = ["serve"]`) {
		t.Fatalf("expected args = [\"serve\"]: %q", out)
	}
	if !strings.Contains(buf.String(), filepath.Join(".codex", "config.toml")) {
		t.Fatalf("install output should mention codex TOML write: %q", buf.String())
	}
}

// Re-running install must not duplicate the [mcp_servers.grpvn] block in TOML.
func TestInstallSkillTOMLIdempotent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("TOML install should be idempotent\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	count := strings.Count(string(second), "[mcp_servers.grpvn]")
	if count != 1 {
		t.Fatalf("expected exactly 1 [mcp_servers.grpvn] section, got %d", count)
	}
}

// If a [mcp_servers.grpvn] section already exists with its own env block,
// the installer leaves the whole thing alone — user customisation wins.
func TestInstallSkillTOMLLeavesExistingSection(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	pre := "[mcp_servers.grpvn]\ncommand = \"/usr/local/bin/grpvn\"\nargs = [\"serve\", \"--debug\"]\n\n[mcp_servers.grpvn.env]\nGRPVN_STATE = \"/tmp/custom-state.json\"\n"
	if err := os.WriteFile(filepath.Join(home, ".codex/config.toml"), []byte(pre), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if string(got) != pre {
		t.Fatalf("installer overwrote a user-customised section with env\nwant: %q\ngot:  %q", pre, got)
	}
}

// If the [mcp_servers.grpvn] section exists but predates the env support
// (left over from a v0.1.1 install), the installer must append a
// [mcp_servers.grpvn.env] sub-section without touching the original.
func TestInstallSkillTOMLUpgradesMissingEnv(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	legacy := "[mcp_servers.grpvn]\ncommand = \"grpvn\"\nargs = [\"serve\"]\n"
	if err := os.WriteFile(filepath.Join(home, ".codex/config.toml"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".codex/config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), legacy) {
		t.Fatalf("installer must preserve the legacy section verbatim; got %q", got)
	}
	if !strings.Contains(string(got), "[mcp_servers.grpvn.env]") {
		t.Fatalf("expected env subsection to be appended; got %q", got)
	}
	if !strings.Contains(string(got), "GRPVN_STATE = ") {
		t.Fatalf("expected GRPVN_STATE in env block; got %q", got)
	}
}

// TOML appends should leave the file with exactly one trailing newline at the
// end of the new section regardless of whether the original ended in a newline.
func TestInstallSkillTOMLAppendsCleanly(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		"",                             // empty file
		"key = 1\n",                    // ends with newline
		"key = 1",                      // missing trailing newline
		"# header\n\n[other]\nx = 1\n", // multi-section
	}
	for _, pre := range cases {
		path := filepath.Join(home, ".codex/config.toml")
		if err := os.WriteFile(path, []byte(pre), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := InstallSkill(&buf); err != nil {
			t.Fatalf("with prefix %q: %v", pre, err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Contains(got, []byte("[mcp_servers.grpvn]")) {
			t.Fatalf("with prefix %q: section not appended: %q", pre, got)
		}
		if !bytes.HasSuffix(got, []byte("\n")) {
			t.Fatalf("with prefix %q: result should end with newline: %q", pre, got)
		}
	}
}

// The Claude Code target gets a Stop hook that blocks stopping while unread
// messages exist. The merge must preserve existing settings, bake the
// per-agent state path into the command, and be idempotent — including when
// the user has customized the command.
func TestInstallSkillWiresClaudeSettings(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"model":"opus","permissions":{"allow":["Bash(ls:*)"]}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(buf.String(), "settings Claude Code") {
		t.Fatalf("expected settings line in output: %q", buf.String())
	}

	type settings struct {
		Model string `json:"model"`
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
		Env map[string]string `json:"env"`
	}
	read := func() settings {
		t.Helper()
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		var s settings
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("settings.json no longer parses: %v\n%s", err, data)
		}
		return s
	}

	s := read()
	if s.Model != "opus" {
		t.Fatalf("existing settings key clobbered: %#v", s)
	}
	// All four notification hooks land, each exactly once, each carrying the
	// per-agent state file.
	for event, sub := range map[string]string{
		"SessionStart":     "hook session-start",
		"UserPromptSubmit": "hook prompt",
		"PostToolUse":      "hook posttool",
		"Stop":             "hook stop",
	} {
		groups := s.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("expected exactly one %s hook entry: %#v", event, s.Hooks)
		}
		h := groups[0].Hooks[0]
		if h.Type != "command" {
			t.Fatalf("%s hook type should be command, got %q", event, h.Type)
		}
		if !strings.Contains(h.Command, sub) || !strings.Contains(h.Command, "state-claude-code.json") {
			t.Fatalf("%s hook command should run grpvn %s with the per-agent state file, got %q", event, sub, h.Command)
		}
	}
	// PostToolUse needs a matcher so it runs after every tool.
	if s.Hooks["PostToolUse"][0].Matcher != "*" {
		t.Fatalf("PostToolUse matcher should be *, got %q", s.Hooks["PostToolUse"][0].Matcher)
	}
	// Permissions are appended, preserving the user's own entries.
	wantPerms := append([]string{"Bash(ls:*)"}, grpvnPermissions...)
	if len(s.Permissions.Allow) != len(wantPerms) {
		t.Fatalf("permissions.allow = %v, want %v", s.Permissions.Allow, wantPerms)
	}
	for i, w := range wantPerms {
		if s.Permissions.Allow[i] != w {
			t.Fatalf("permissions.allow = %v, want %v", s.Permissions.Allow, wantPerms)
		}
	}
	// Session env pins the per-runtime identity for Bash calls too.
	if !strings.Contains(s.Env["GRPVN_STATE"], "state-claude-code.json") {
		t.Fatalf("env.GRPVN_STATE should carry the per-agent state file, got %q", s.Env["GRPVN_STATE"])
	}

	// Re-running install must not duplicate anything.
	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	if s := read(); len(s.Hooks["Stop"]) != 1 || len(s.Permissions.Allow) != len(wantPerms) {
		t.Fatalf("re-install duplicated hooks or permissions: %#v", s)
	}

	// A user-customized grpvn hook command is respected as-is; the other
	// events are still added around it.
	custom := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/opt/grpvn --state /elsewhere.json hook stop"}]}]}}`)
	if err := os.WriteFile(settingsPath, custom, 0644); err != nil {
		t.Fatal(err)
	}
	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("install over custom hook: %v", err)
	}
	if s := read(); len(s.Hooks["Stop"]) != 1 || !strings.Contains(s.Hooks["Stop"][0].Hooks[0].Command, "/opt/grpvn") {
		t.Fatalf("customized hook should be left untouched: %#v", s.Hooks)
	}
	if s := read(); len(s.Hooks["SessionStart"]) != 1 {
		t.Fatalf("other hook events should still be installed: %#v", s.Hooks)
	}
}

// The installer must seed a fresh per-runtime state file with the follows
// and default channel of the base state.json — an identity subscribed to
// nothing can never see channel traffic, which kills every notification
// downstream.
func TestInstallSkillSeedsRuntimeState(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	base := &State{Name: "base-name", DefaultChannel: "#dev", Follow: []string{"#dev", "#ops"}}
	if err := base.Save(filepath.Join(home, ".grpvn", "state.json")); err != nil {
		t.Fatal(err)
	}

	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	seededPath := filepath.Join(home, ".grpvn", "state-claude-code.json")
	st, err := LoadState(seededPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "" {
		t.Fatalf("identity name must not be copied, got %q", st.Name)
	}
	if st.DefaultChannel != "#dev" || len(st.Follow) != 2 {
		t.Fatalf("seeded state should inherit follows and default: %#v", st)
	}

	// A state that already follows something (or was deliberately emptied of
	// its default) is left alone on re-install.
	st.Follow = []string{"#only"}
	st.DefaultChannel = ""
	if err := st.Save(seededPath); err != nil {
		t.Fatal(err)
	}
	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	st, err = LoadState(seededPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Follow) != 1 || st.Follow[0] != "#only" {
		t.Fatalf("re-install must not overwrite a configured runtime state: %#v", st)
	}
}

// The coordination block goes into the always-loaded context files, once,
// preserving existing content.
func TestInstallSkillAppendsContextBlock(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	ctxPath := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.WriteFile(ctxPath, []byte("# my rules\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := InstallSkill(&buf); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(buf.String(), "context Claude Code") {
		t.Fatalf("expected context line in output: %q", buf.String())
	}
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# my rules") {
		t.Fatal("existing context content clobbered")
	}
	if !strings.Contains(string(data), contextMarker) {
		t.Fatal("coordination block missing")
	}

	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	again, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(again), contextMarker) != 1 {
		t.Fatal("coordination block duplicated on re-install")
	}
}

// Gemini's server entry is marked trusted so tool calls skip per-call
// confirmation — the notification path must not dead-end in a prompt.
func TestInstallSkillMarksGeminiTrusted(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]struct {
			Trust bool `json:"trust"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if !doc.Servers["grpvn"].Trust {
		t.Fatalf("gemini grpvn entry should carry trust: true, got %s", data)
	}
}

// Codex, Gemini, and Cursor each get grpvn's notification hooks in their
// own dialect: Codex a dedicated hooks.json in Claude's shape (with a Stop
// entry), Gemini hooks inside settings.json next to mcpServers (no stop —
// its deny semantics retry-loop), Cursor a version-1 hooks.json with flat
// command entries (no prompt — it can't inject there).
func TestInstallSkillWiresRuntimeHookFiles(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	for _, d := range []string{".codex", ".gemini", ".cursor"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}

	type group struct {
		Command string `json:"command"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	load := func(rel string) map[string][]group {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Version int                `json:"version"`
			Hooks   map[string][]group `json:"hooks"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse %s: %v\n%s", rel, err, data)
		}
		if rel == ".cursor/hooks.json" && doc.Version != 1 {
			t.Fatalf("cursor hooks.json needs version 1, got %d", doc.Version)
		}
		return doc.Hooks
	}

	codex := load(".codex/hooks.json")
	for event, sub := range map[string]string{
		"SessionStart":     "hook session-start",
		"UserPromptSubmit": "hook prompt",
		"PostToolUse":      "hook posttool",
		"Stop":             "hook stop",
	} {
		gs := codex[event]
		if len(gs) != 1 || len(gs[0].Hooks) != 1 {
			t.Fatalf("codex %s: expected one nested entry: %#v", event, codex)
		}
		c := gs[0].Hooks[0].Command
		if !strings.Contains(c, sub) || !strings.Contains(c, "--format codex") || !strings.Contains(c, "state-codex.json") {
			t.Fatalf("codex %s command wrong: %q", event, c)
		}
	}

	gemini := load(".gemini/settings.json")
	if len(gemini["SessionStart"]) != 1 || len(gemini["BeforeAgent"]) != 1 || len(gemini["AfterTool"]) != 1 {
		t.Fatalf("gemini events missing: %#v", gemini)
	}
	if len(gemini["Stop"]) != 0 || len(gemini["AfterAgent"]) != 0 {
		t.Fatalf("gemini must not get a stop-style hook: %#v", gemini)
	}
	if c := gemini["AfterTool"][0].Hooks[0].Command; !strings.Contains(c, "--format gemini") {
		t.Fatalf("gemini command wrong: %q", c)
	}
	// mcpServers must survive in the same file.
	raw, _ := os.ReadFile(filepath.Join(home, ".gemini/settings.json"))
	if !strings.Contains(string(raw), "mcpServers") {
		t.Fatalf("gemini settings lost mcpServers: %s", raw)
	}

	cursor := load(".cursor/hooks.json")
	for event, sub := range map[string]string{
		"sessionStart": "hook session-start",
		"postToolUse":  "hook posttool",
		"stop":         "hook stop",
	} {
		gs := cursor[event]
		if len(gs) != 1 || gs[0].Command == "" {
			t.Fatalf("cursor %s: expected one flat entry: %#v", event, cursor)
		}
		if !strings.Contains(gs[0].Command, sub) || !strings.Contains(gs[0].Command, "--format cursor") {
			t.Fatalf("cursor %s command wrong: %q", event, gs[0].Command)
		}
	}
	if len(cursor["beforeSubmitPrompt"]) != 0 {
		t.Fatalf("cursor must not get a prompt hook: %#v", cursor)
	}

	// Idempotent across re-installs.
	before, _ := os.ReadFile(filepath.Join(home, ".codex/hooks.json"))
	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatalf("re-install: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".codex/hooks.json"))
	if !bytes.Equal(before, after) {
		t.Fatalf("re-install changed codex hooks.json\nbefore: %s\nafter: %s", before, after)
	}
}

// Project scope reaches every wiring surface: GRPVN_SCOPE in the MCP env
// and settings env, --scope project in hook commands — and an existing
// installer-written install upgrades in place. Claude Desktop stays
// runtime-scoped.
func TestInstallSkillWiresProjectScope(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	for _, d := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a pre-scope install: hooks and env without --scope/GRPVN_SCOPE.
	statePath := filepath.Join(home, ".grpvn", "state-claude-code.json")
	old := fmt.Sprintf(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"grpvn --state \"%s\" hook stop"}]}]},"env":{"GRPVN_STATE":%q}}`, statePath, statePath)
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	codexState := filepath.Join(home, ".grpvn", "state-codex.json")
	toml := fmt.Sprintf("[mcp_servers.grpvn]\ncommand = \"grpvn\"\nargs = [\"serve\"]\n\n[mcp_servers.grpvn.env]\nGRPVN_STATE = %q\n", codexState)
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	if err := InstallSkill(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	settings, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	for _, want := range []string{`--scope project hook stop`, `--scope project hook prompt`, `"GRPVN_SCOPE": "project"`} {
		if !strings.Contains(string(settings), want) {
			t.Fatalf("settings missing %q:\n%s", want, settings)
		}
	}
	if strings.Count(string(settings), "hook stop") != 1 {
		t.Fatalf("upgrade must rewrite, not duplicate, the Stop hook:\n%s", settings)
	}

	mcp, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(mcp), `"GRPVN_SCOPE": "project"`) {
		t.Fatalf(".claude.json MCP env missing GRPVN_SCOPE:\n%s", mcp)
	}

	cfg, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(cfg), `GRPVN_SCOPE = "project"`) {
		t.Fatalf("codex config missing GRPVN_SCOPE upgrade:\n%s", cfg)
	}
	codexHooks, _ := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
	if !strings.Contains(string(codexHooks), "--scope project") {
		t.Fatalf("codex hooks missing --scope:\n%s", codexHooks)
	}
}
