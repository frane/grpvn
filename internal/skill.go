package internal

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

//go:embed embedded/SKILL.md
var SkillContent []byte

// Target describes one supported agent surface. The skill installer writes
// SKILL.md into Skill (always) and merges an mcpServers.grpvn entry into MCP
// (when MCP is non-empty and the host directory exists). A target is "present"
// when DetectDir exists under HOME — this keeps the installer from littering
// directories for agents the user hasn't installed.
type Target struct {
	Name      string // human label printed on install
	DetectDir string // path under HOME that, when present, signals the agent is installed
	Skill     string // path under HOME where SKILL.md is written
	MCP       string // path under HOME of the JSON config that holds mcpServers (empty = skip)
}

// HomeTargets enumerates the supported integrations. Paths are relative to the
// user's HOME so tests can substitute a temp dir.
func HomeTargets() []Target {
	base := []Target{
		{
			Name:      "Claude Code",
			DetectDir: ".claude",
			Skill:     ".claude/skills/grpvn/SKILL.md",
			MCP:       ".claude.json",
		},
		{
			Name:      "Cursor",
			DetectDir: ".cursor",
			Skill:     ".cursor/skills/grpvn/SKILL.md",
			MCP:       ".cursor/mcp.json",
		},
		{
			Name:      "Codex CLI",
			DetectDir: ".codex",
			Skill:     ".codex/skills/grpvn/SKILL.md",
			MCP:       "", // Codex uses TOML; install --codex handles that separately
		},
		{
			Name:      "Gemini CLI",
			DetectDir: ".gemini",
			Skill:     ".gemini/skills/grpvn/SKILL.md",
			MCP:       ".gemini/settings.json",
		},
		{
			Name:      "agents (generic)",
			DetectDir: ".agents",
			Skill:     ".agents/skills/grpvn/SKILL.md",
			MCP:       "",
		},
	}
	if runtime.GOOS == "darwin" {
		base = append(base, Target{
			Name:      "Claude Desktop",
			DetectDir: "Library/Application Support/Claude",
			Skill:     "Library/Application Support/Claude/skills/grpvn/SKILL.md",
			MCP:       "Library/Application Support/Claude/claude_desktop_config.json",
		})
	}
	return base
}

// InstallSkill writes SKILL.md and (where applicable) registers the grpvn MCP
// server into each detected agent's config. Targets whose DetectDir is absent
// are skipped silently. The caller may pass forceAll to install into every
// known target regardless of detection (useful for first-time setup before any
// agent has run).
func InstallSkill(w io.Writer) error {
	return installSkillFromHome(w, "", false)
}

// InstallSkillAll forces installation into every known target even when the
// agent's directory doesn't exist yet. Used by `grpvn skill install --all`.
func InstallSkillAll(w io.Writer) error {
	return installSkillFromHome(w, "", true)
}

func installSkillFromHome(w io.Writer, homeOverride string, forceAll bool) error {
	home := homeOverride
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home: %w", err)
		}
		home = h
	}
	var (
		lastErr  error
		written  []string
		skipped  []string
		mcpAdded []string
	)
	for _, t := range HomeTargets() {
		detect := filepath.Join(home, t.DetectDir)
		if !forceAll {
			if _, err := os.Stat(detect); err != nil {
				skipped = append(skipped, t.Name)
				continue
			}
		}
		skillPath := filepath.Join(home, t.Skill)
		if err := os.MkdirAll(filepath.Dir(skillPath), 0755); err != nil {
			lastErr = err
			continue
		}
		if err := os.WriteFile(skillPath, SkillContent, 0644); err != nil {
			lastErr = err
			continue
		}
		written = append(written, fmt.Sprintf("%s: %s", t.Name, skillPath))
		if t.MCP == "" {
			continue
		}
		mcpPath := filepath.Join(home, t.MCP)
		if err := mergeMCP(mcpPath, "grpvn", "grpvn", []string{"serve"}); err != nil {
			lastErr = err
			continue
		}
		mcpAdded = append(mcpAdded, fmt.Sprintf("%s: %s", t.Name, mcpPath))
	}
	sort.Strings(written)
	sort.Strings(mcpAdded)
	for _, line := range written {
		fmt.Fprintf(w, "skill   %s\n", line)
	}
	for _, line := range mcpAdded {
		fmt.Fprintf(w, "mcp     %s\n", line)
	}
	if !forceAll {
		for _, s := range skipped {
			fmt.Fprintf(w, "skip    %s (not detected)\n", s)
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return nil
}

// mergeMCP reads a JSON file holding an "mcpServers" object, sets
// mcpServers[name] = {command, args}, and writes the result atomically. If the
// file doesn't exist it's created. Any existing keys other than the named
// server entry are preserved. Existing non-JSON content is preserved by
// erroring out rather than overwriting.
func mergeMCP(path, name, command string, args []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	var doc map[string]interface{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("read %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	servers, _ := doc["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	servers[name] = map[string]interface{}{
		"command": command,
		"args":    args,
	}
	doc["mcpServers"] = servers
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, encoded, 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
