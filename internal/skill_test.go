package internal

import (
	"bytes"
	"encoding/json"
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
