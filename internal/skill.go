package internal

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

//go:embed embedded/SKILL.md
var SkillContent []byte

// Target describes one supported agent surface. The skill installer writes
// SKILL.md into Skill (always) and registers the grpvn MCP server in either
// MCP (a JSON config) or MCPTOML (a TOML config that takes a
// [mcp_servers.<name>] section). A target is "present" when DetectDir exists
// under HOME — this keeps the installer from littering directories for agents
// the user hasn't installed.
type Target struct {
	Name      string // human label printed on install
	Slug      string // short, filesystem-safe id used for the per-agent state file (e.g. "claude-code")
	DetectDir string // path under HOME that, when present, signals the agent is installed
	Skill     string // path under HOME where SKILL.md is written
	MCP       string // path under HOME of the JSON config that holds mcpServers (empty = skip)
	MCPTOML   string // path under HOME of the TOML config that holds [mcp_servers.<name>] (empty = skip)
	Hooks     string // path under HOME of the settings JSON that takes Claude-Code-style hooks (empty = skip)
}

// HomeTargets enumerates the supported integrations. Paths are relative to the
// user's HOME so tests can substitute a temp dir.
func HomeTargets() []Target {
	base := []Target{
		{
			Name:      "Claude Code",
			Slug:      "claude-code",
			DetectDir: ".claude",
			Skill:     ".claude/skills/grpvn/SKILL.md",
			MCP:       ".claude.json",
			Hooks:     ".claude/settings.json",
		},
		{
			Name:      "Cursor",
			Slug:      "cursor",
			DetectDir: ".cursor",
			Skill:     ".cursor/skills/grpvn/SKILL.md",
			MCP:       ".cursor/mcp.json",
		},
		{
			Name:      "Codex CLI",
			Slug:      "codex",
			DetectDir: ".codex",
			Skill:     ".codex/skills/grpvn/SKILL.md",
			MCPTOML:   ".codex/config.toml",
		},
		{
			Name:      "Gemini CLI",
			Slug:      "gemini",
			DetectDir: ".gemini",
			Skill:     ".gemini/skills/grpvn/SKILL.md",
			MCP:       ".gemini/settings.json",
		},
		{
			Name:      "agents (generic)",
			Slug:      "agents",
			DetectDir: ".agents",
			Skill:     ".agents/skills/grpvn/SKILL.md",
			MCP:       "",
		},
	}
	if runtime.GOOS == "darwin" {
		base = append(base, Target{
			Name:      "Claude Desktop",
			Slug:      "claude-desktop",
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
		lastErr    error
		written    []string
		skipped    []string
		mcpAdded   []string
		hooksAdded []string
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
		// Per-agent state file so the identity stays stable AND distinct
		// across runtimes. Without this every MCP host shares
		// $HOME/.grpvn/state.json and inherits the same identity.
		statePath := filepath.Join(home, ".grpvn", "state-"+t.Slug+".json")
		env := map[string]string{"GRPVN_STATE": statePath}
		if t.MCP != "" {
			mcpPath := filepath.Join(home, t.MCP)
			if err := mergeMCP(mcpPath, "grpvn", "grpvn", []string{"serve"}, env); err != nil {
				lastErr = err
				continue
			}
			mcpAdded = append(mcpAdded, fmt.Sprintf("%s: %s", t.Name, mcpPath))
		}
		if t.MCPTOML != "" {
			tomlPath := filepath.Join(home, t.MCPTOML)
			added, err := mergeMCPTOML(tomlPath, "grpvn", "grpvn", []string{"serve"}, env)
			if err != nil {
				lastErr = err
				continue
			}
			if added {
				mcpAdded = append(mcpAdded, fmt.Sprintf("%s: %s", t.Name, tomlPath))
			}
		}
		if t.Hooks != "" {
			hooksPath := filepath.Join(home, t.Hooks)
			// The hook command carries the same per-agent state file the MCP
			// entry gets via env, so the unread check sees the runtime's own
			// cursors. Hooks run through the shell with the session env, not
			// the MCP server's, so the path is baked in.
			command := fmt.Sprintf(`grpvn --state "%s" hook stop`, statePath)
			added, err := mergeStopHook(hooksPath, command)
			if err != nil {
				lastErr = err
				continue
			}
			if added {
				hooksAdded = append(hooksAdded, fmt.Sprintf("%s: %s", t.Name, hooksPath))
			}
		}
	}
	sort.Strings(written)
	sort.Strings(mcpAdded)
	sort.Strings(hooksAdded)
	for _, line := range written {
		fmt.Fprintf(w, "skill   %s\n", line)
	}
	for _, line := range mcpAdded {
		fmt.Fprintf(w, "mcp     %s\n", line)
	}
	for _, line := range hooksAdded {
		fmt.Fprintf(w, "hook    %s\n", line)
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
func mergeMCP(path, name, command string, args []string, env map[string]string) error {
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
	entry := map[string]interface{}{
		"command": command,
		"args":    args,
	}
	if len(env) > 0 {
		envMap := map[string]interface{}{}
		for k, v := range env {
			envMap[k] = v
		}
		entry["env"] = envMap
	}
	servers[name] = entry
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

// mergeMCPTOML appends a [mcp_servers.<name>] block to a TOML config if a
// section with that exact header isn't already present. It deliberately does
// not parse the whole TOML document — that would mean shipping a TOML parser
// for one config write. Instead it's strictly additive: if the section exists
// (in any form) the file is left untouched and (false, nil) is returned. If
// the file doesn't exist or doesn't contain the header, the block is appended
// with a leading blank line and (true, nil) is returned.
//
// Codex's config.toml uses `[mcp_servers.<name>]` for MCP servers, and there's
// only one canonical shape for an MCP server entry, so blind append-if-absent
// is safe enough and dodges every "preserve comments / preserve ordering"
// TOML-parser headache.
// mergeStopHook registers a Stop hook in a Claude-Code-style settings JSON
// (hooks.Stop is a list of matcher groups, each holding a list of command
// hooks). It is idempotent: if any existing Stop hook command already
// invokes `grpvn` with `hook stop`, the file is left untouched and
// (false, nil) is returned — including when the user has customized the
// command (different --state path, wrapper script). Every other key in the
// settings document is preserved, and the write is atomic.
func mergeStopHook(path, command string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	var doc map[string]interface{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil && len(data) > 0:
		if err := json.Unmarshal(data, &doc); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	case err != nil && !os.IsNotExist(err):
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	hooks, _ := doc["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	stops, _ := hooks["Stop"].([]interface{})
	for _, group := range stops {
		g, _ := group.(map[string]interface{})
		entries, _ := g["hooks"].([]interface{})
		for _, entry := range entries {
			e, _ := entry.(map[string]interface{})
			c, _ := e["command"].(string)
			if strings.Contains(c, "grpvn") && strings.Contains(c, "hook stop") {
				return false, nil
			}
		}
	}
	stops = append(stops, map[string]interface{}{
		"hooks": []interface{}{
			map[string]interface{}{"type": "command", "command": command},
		},
	})
	hooks["Stop"] = stops
	doc["hooks"] = hooks
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode %s: %w", path, err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, encoded, 0644); err != nil {
		return false, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("rename %s: %w", path, err)
	}
	return true, nil
}

func mergeMCPTOML(path, name, command string, args []string, env map[string]string) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	header := fmt.Sprintf("[mcp_servers.%s]", name)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	envHeader := fmt.Sprintf("[mcp_servers.%s.env]", name)
	hasSection := bytes.Contains(existing, []byte(header))
	hasEnv := bytes.Contains(existing, []byte(envHeader))
	if hasSection && (hasEnv || len(env) == 0) {
		return false, nil
	}
	if hasSection && !hasEnv && len(env) > 0 {
		// Section exists but is missing the env block — append just the env
		// sub-table. This is the upgrade path from a previous install that
		// didn't write an env block.
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var addendum strings.Builder
		if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
			addendum.WriteString("\n")
		}
		addendum.WriteString("\n")
		addendum.WriteString(envHeader)
		addendum.WriteString("\n")
		for _, k := range keys {
			fmt.Fprintf(&addendum, "%s = %q\n", k, env[k])
		}
		merged := append(existing, []byte(addendum.String())...)
		tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
		if err := os.WriteFile(tmp, merged, 0644); err != nil {
			return false, fmt.Errorf("write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return false, fmt.Errorf("rename %s: %w", path, err)
		}
		return true, nil
	}
	quotedArgs := make([]string, len(args))
	for i, a := range args {
		quotedArgs[i] = fmt.Sprintf("%q", a)
	}
	var block strings.Builder
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		block.WriteString("\n")
	}
	if len(existing) > 0 {
		block.WriteString("\n")
	}
	block.WriteString(header)
	block.WriteString("\n")
	fmt.Fprintf(&block, "command = %q\n", command)
	fmt.Fprintf(&block, "args = [%s]\n", strings.Join(quotedArgs, ", "))
	if len(env) > 0 {
		// Stable order so the file diff is deterministic across runs.
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		block.WriteString("\n[mcp_servers.")
		block.WriteString(name)
		block.WriteString(".env]\n")
		for _, k := range keys {
			fmt.Fprintf(&block, "%s = %q\n", k, env[k])
		}
	}
	merged := append(existing, []byte(block.String())...)
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, merged, 0644); err != nil {
		return false, fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("rename %s: %w", path, err)
	}
	return true, nil
}
