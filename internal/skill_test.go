package internal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
	t.Setenv("HOME", home)
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
