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
	Hooks     string // path under HOME of the settings JSON that takes Claude-Code-style hooks, permissions, and env (empty = skip)
	HookFile  string // path under HOME of a JSON doc that takes a "hooks" object in HookDialect's shape (empty = skip)
	HookDial  string // hook output dialect for HookFile entries: DialectCodex, DialectGemini, or DialectCursor
	Context   string // path under HOME of an always-loaded context file (CLAUDE.md/AGENTS.md/GEMINI.md) to append the coordination block to (empty = skip)
	Trust     bool   // set "trust": true on the mcpServers entry (Gemini CLI: skips per-call confirmation)
	Scope     bool   // project-scoped identities: GRPVN_SCOPE=project in env, --scope project in hook commands. Only for runtimes whose cwd is the project (not Claude Desktop)
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
			Context:   ".claude/CLAUDE.md",
			Scope:     true,
		},
		{
			Name:      "Cursor",
			Slug:      "cursor",
			DetectDir: ".cursor",
			Skill:     ".cursor/skills/grpvn/SKILL.md",
			MCP:       ".cursor/mcp.json",
			HookFile:  ".cursor/hooks.json",
			HookDial:  DialectCursor,
			Scope:     true,
		},
		{
			Name:      "Codex CLI",
			Slug:      "codex",
			DetectDir: ".codex",
			Skill:     ".codex/skills/grpvn/SKILL.md",
			MCPTOML:   ".codex/config.toml",
			HookFile:  ".codex/hooks.json",
			HookDial:  DialectCodex,
			Context:   ".codex/AGENTS.md",
			Scope:     true,
		},
		{
			Name:      "Gemini CLI",
			Slug:      "gemini",
			DetectDir: ".gemini",
			Skill:     ".gemini/skills/grpvn/SKILL.md",
			MCP:       ".gemini/settings.json",
			HookFile:  ".gemini/settings.json",
			HookDial:  DialectGemini,
			Context:   ".gemini/GEMINI.md",
			Trust:     true,
			Scope:     true,
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
		lastErr       error
		written       []string
		skipped       []string
		mcpAdded      []string
		settingsAdded []string
		ctxAdded      []string
		seeded        []string
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
		if t.Scope {
			env["GRPVN_SCOPE"] = "project"
		}
		// Seed the per-runtime state before anything points at it. A
		// runtime identity that follows no channels is a mailbox channel
		// traffic can never reach — the notification hooks would guard an
		// empty inbox forever.
		if t.MCP != "" || t.MCPTOML != "" || t.Hooks != "" || t.HookFile != "" {
			ok, err := seedRuntimeState(home, statePath)
			if err != nil {
				lastErr = err
				continue
			}
			if ok {
				seeded = append(seeded, fmt.Sprintf("%s: %s", t.Name, statePath))
			}
		}
		if t.MCP != "" {
			mcpPath := filepath.Join(home, t.MCP)
			var extra map[string]interface{}
			if t.Trust {
				extra = map[string]interface{}{"trust": true}
			}
			if err := mergeMCP(mcpPath, "grpvn", "grpvn", []string{"serve"}, env, extra); err != nil {
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
			added, err := mergeClaudeSettings(hooksPath, statePath, t.Scope)
			if err != nil {
				lastErr = err
				continue
			}
			if added {
				settingsAdded = append(settingsAdded, fmt.Sprintf("%s: %s", t.Name, hooksPath))
			}
		}
		if t.HookFile != "" {
			hookPath := filepath.Join(home, t.HookFile)
			added, err := mergeHooksFile(hookPath, statePath, t.HookDial, t.Scope)
			if err != nil {
				lastErr = err
				continue
			}
			if added {
				settingsAdded = append(settingsAdded, fmt.Sprintf("%s: %s", t.Name, hookPath))
			}
		}
		if t.Context != "" {
			ctxPath := filepath.Join(home, t.Context)
			added, err := mergeContext(ctxPath)
			if err != nil {
				lastErr = err
				continue
			}
			if added {
				ctxAdded = append(ctxAdded, fmt.Sprintf("%s: %s", t.Name, ctxPath))
			}
		}
	}
	sort.Strings(written)
	sort.Strings(mcpAdded)
	sort.Strings(settingsAdded)
	sort.Strings(ctxAdded)
	sort.Strings(seeded)
	for _, line := range written {
		fmt.Fprintf(w, "skill   %s\n", line)
	}
	for _, line := range mcpAdded {
		fmt.Fprintf(w, "mcp     %s\n", line)
	}
	for _, line := range settingsAdded {
		fmt.Fprintf(w, "settings %s\n", line)
	}
	for _, line := range ctxAdded {
		fmt.Fprintf(w, "context %s\n", line)
	}
	for _, line := range seeded {
		fmt.Fprintf(w, "seed    %s\n", line)
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
func mergeMCP(path, name, command string, args []string, env map[string]string, extra map[string]interface{}) error {
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
	for k, v := range extra {
		entry[k] = v
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
// hookSpec is one Claude Code hook event the installer wires. Sub is the
// `grpvn hook <sub>` subcommand and doubles as the idempotency needle: any
// existing command containing both "grpvn" and Sub — including
// user-customized variants — leaves that event untouched.
type hookSpec struct {
	Event   string
	Sub     string
	Matcher string // tool-name matcher for tool events ("*" = all); empty = no matcher key
}

// grpvnHooks is the full notification surface: identity + unread at session
// start, an unread line on every user prompt, a throttled mid-turn notice
// after tool calls, and the end-of-turn block. Together they make delivery
// structural instead of relying on the model remembering to poll.
var grpvnHooks = []hookSpec{
	{Event: "SessionStart", Sub: "hook session-start"},
	{Event: "UserPromptSubmit", Sub: "hook prompt"},
	{Event: "PostToolUse", Sub: "hook posttool", Matcher: "*"},
	{Event: "Stop", Sub: "hook stop"},
}

// grpvnPermissions is what agents need allowlisted to act on a notification
// without a permission prompt: every tool on the grpvn MCP server, and the
// grpvn binary over Bash. Without these the hooks nudge the agent into a
// prompt the user has to click through — which is no proactivity at all.
var grpvnPermissions = []string{"mcp__grpvn", "Bash(grpvn:*)"}

// hookCommand renders the installer's canonical hook command. Anything
// matching its `grpvn --state "` prefix is treated as installer-written and
// safe to upgrade in place; anything else (custom binary path, wrapper
// script) is the user's and stays untouched.
func hookCommand(statePath, sub string, scoped bool, dialect string) string {
	c := fmt.Sprintf(`grpvn --state "%s"`, statePath)
	if scoped {
		c += " --scope project"
	}
	c += " " + sub
	if dialect != "" && dialect != DialectClaude {
		c += " --format " + dialect
	}
	return c
}

// upgradeableHook reports whether an existing hook command is one the
// installer wrote (and may therefore rewrite when the canonical shape
// changes, e.g. gaining --scope).
func upgradeableHook(c string) bool {
	return strings.HasPrefix(c, `grpvn --state "`)
}

// mergeClaudeSettings wires grpvn into a Claude-Code-style settings JSON:
// the four notification hooks (each carrying the per-runtime state path,
// since hooks run through the shell with the session env, not the MCP
// server's), the permission allowlist, and session-wide GRPVN_STATE (and,
// when scoped, GRPVN_SCOPE) env vars so CLI calls inside the session
// resolve to the same identity as the MCP server. Idempotent per item;
// installer-written hook commands are upgraded in place when the canonical
// shape changes, user-customized ones are left alone; every other key in
// the document is preserved and the write is atomic. Returns whether
// anything changed.
func mergeClaudeSettings(path, statePath string, scoped bool) (bool, error) {
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
	changed := false

	hooks, _ := doc["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	for _, spec := range grpvnHooks {
		want := hookCommand(statePath, spec.Sub, scoped, DialectClaude)
		groups, _ := hooks[spec.Event].([]interface{})
		present := false
		for _, group := range groups {
			g, _ := group.(map[string]interface{})
			entries, _ := g["hooks"].([]interface{})
			for _, entry := range entries {
				e, _ := entry.(map[string]interface{})
				c, _ := e["command"].(string)
				if !strings.Contains(c, "grpvn") || !strings.Contains(c, spec.Sub) {
					continue
				}
				present = true
				if c != want && upgradeableHook(c) {
					e["command"] = want
					changed = true
				}
			}
		}
		if present {
			continue
		}
		group := map[string]interface{}{
			"hooks": []interface{}{
				map[string]interface{}{
					"type":    "command",
					"command": want,
				},
			},
		}
		if spec.Matcher != "" {
			group["matcher"] = spec.Matcher
		}
		hooks[spec.Event] = append(groups, group)
		changed = true
	}
	doc["hooks"] = hooks

	perms, _ := doc["permissions"].(map[string]interface{})
	if perms == nil {
		perms = map[string]interface{}{}
	}
	allow, _ := perms["allow"].([]interface{})
	for _, want := range grpvnPermissions {
		present := false
		for _, have := range allow {
			if s, _ := have.(string); s == want {
				present = true
			}
		}
		if !present {
			allow = append(allow, want)
			changed = true
		}
	}
	perms["allow"] = allow
	doc["permissions"] = perms

	envDoc, _ := doc["env"].(map[string]interface{})
	if envDoc == nil {
		envDoc = map[string]interface{}{}
	}
	if _, ok := envDoc["GRPVN_STATE"]; !ok {
		envDoc["GRPVN_STATE"] = statePath
		changed = true
	}
	if scoped {
		if _, ok := envDoc["GRPVN_SCOPE"]; !ok {
			envDoc["GRPVN_SCOPE"] = "project"
			changed = true
		}
	}
	doc["env"] = envDoc

	if !changed {
		return false, nil
	}
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

// dialectHooks maps each non-Claude runtime to the lifecycle events that
// carry grpvn's notification moments. Gemini gets no stop entry — its
// nearest event (AfterAgent with deny) retries the response instead of
// nudging once, a loop with no brake — and Cursor gets no prompt entry
// because beforeSubmitPrompt can only block, not inject context.
var dialectHooks = map[string][]hookSpec{
	DialectCodex: {
		{Event: "SessionStart", Sub: "hook session-start"},
		{Event: "UserPromptSubmit", Sub: "hook prompt"},
		{Event: "PostToolUse", Sub: "hook posttool"},
		{Event: "Stop", Sub: "hook stop"},
	},
	DialectGemini: {
		{Event: "SessionStart", Sub: "hook session-start"},
		{Event: "BeforeAgent", Sub: "hook prompt"},
		{Event: "AfterTool", Sub: "hook posttool"},
	},
	DialectCursor: {
		{Event: "sessionStart", Sub: "hook session-start"},
		{Event: "postToolUse", Sub: "hook posttool"},
		{Event: "stop", Sub: "hook stop"},
	},
}

// mergeHooksFile wires grpvn's notification hooks into a runtime's hooks
// document: ~/.codex/hooks.json and Gemini's settings.json share Claude
// Code's matcher-group shape, while Cursor's ~/.cursor/hooks.json takes
// flat {"command": …} entries under a "version": 1 doc. Idempotency
// matches mergeClaudeSettings: an existing command containing "grpvn" and
// the subcommand leaves that event untouched, everything else in the
// document is preserved, and the write is atomic.
func mergeHooksFile(path, statePath, dialect string, scoped bool) (bool, error) {
	specs, ok := dialectHooks[dialect]
	if !ok {
		return false, fmt.Errorf("unknown hook dialect %q", dialect)
	}
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
	changed := false
	if dialect == DialectCursor {
		if _, ok := doc["version"]; !ok {
			doc["version"] = 1
			changed = true
		}
	}
	hooks, _ := doc["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = map[string]interface{}{}
	}
	for _, spec := range specs {
		want := hookCommand(statePath, spec.Sub, scoped, dialect)
		entries, _ := hooks[spec.Event].([]interface{})
		present := false
		match := func(e map[string]interface{}) {
			c, _ := e["command"].(string)
			if !strings.Contains(c, "grpvn") || !strings.Contains(c, spec.Sub) {
				return
			}
			present = true
			if c != want && upgradeableHook(c) {
				e["command"] = want
				changed = true
			}
		}
		for _, entry := range entries {
			e, _ := entry.(map[string]interface{})
			// Cursor entries carry the command directly; Codex/Gemini nest
			// it in a matcher group's hooks array.
			match(e)
			inner, _ := e["hooks"].([]interface{})
			for _, ih := range inner {
				h, _ := ih.(map[string]interface{})
				match(h)
			}
		}
		if present {
			continue
		}
		var entry map[string]interface{}
		if dialect == DialectCursor {
			entry = map[string]interface{}{"command": want}
		} else {
			entry = map[string]interface{}{
				"hooks": []interface{}{
					map[string]interface{}{"type": "command", "command": want},
				},
			}
		}
		hooks[spec.Event] = append(entries, entry)
		changed = true
	}
	doc["hooks"] = hooks
	if !changed {
		return false, nil
	}
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

// seedRuntimeState makes a per-runtime state file inherit the followed
// channels and default channel from the base ~/.grpvn/state.json. Without
// this, a runtime identity starts subscribed to nothing — a mailbox channel
// traffic can never reach. Runs on the upgrade path too: an existing
// per-runtime state that follows nothing and has no default channel is
// treated as unseeded. The identity name is never copied — distinct names
// per runtime are the point of the split.
func seedRuntimeState(home, statePath string) (bool, error) {
	st, err := LoadState(statePath)
	if err != nil {
		return false, err
	}
	if len(st.Follow) > 0 || st.DefaultChannel != "" {
		return false, nil
	}
	base, err := LoadState(filepath.Join(home, ".grpvn", "state.json"))
	if err != nil {
		return false, err
	}
	if len(base.Follow) == 0 && base.DefaultChannel == "" {
		return false, nil
	}
	st.Follow = append([]string{}, base.Follow...)
	st.DefaultChannel = base.DefaultChannel
	if err := st.Save(statePath); err != nil {
		return false, err
	}
	return true, nil
}

// contextMarker and contextMarkerEnd delimit the installer-owned region of
// a context file. Both present: the region is replaced on upgrade. Only the
// start marker (a legacy install, or a user-edited descendant): replaced
// only when the block matches a shipped version verbatim, otherwise hands
// off.
const (
	contextMarker    = "<!-- grpvn:coordination -->"
	contextMarkerEnd = "<!-- /grpvn:coordination -->"
)

var contextBlock = contextMarker + `
## grpvn — peer chat with the other agents on this host

Check unread grpvn messages with the c tool (or ` + "`grpvn c`" + `) at the start of
every turn and every few tool calls during long-running work. When there is
unread, read it with r and answer any questions before continuing. Announce
substantive work in the relevant channel. If your runtime supports
background shell tasks, keep one ` + "`grpvn w --timeout 0`" + ` armed as a
background task from the start of the session: it exits the moment a
message lands, waking you with the counts — read with r, reply, re-arm.
One armed waiter per session; never poll in a loop.
` + contextMarkerEnd + `
`

// legacyContextBlocks are prior shipped block texts (no end marker), safe
// to replace verbatim on upgrade because we wrote them.
var legacyContextBlocks = []string{contextMarker + `
## grpvn — peer chat with the other agents on this host

Check unread grpvn messages with the c tool (or ` + "`grpvn c`" + `) at the start of
every turn and every few tool calls during long-running work. When there is
unread, read it with r and answer any questions before continuing. Announce
substantive work in the relevant channel. When a reply is the only thing
blocking you, run ` + "`grpvn w --timeout 0`" + ` as a background task — it exits the
moment a message lands, instead of burning turns polling.
`}

// mergeContext appends the coordination block to an always-loaded context
// file (CLAUDE.md, AGENTS.md, GEMINI.md). Unlike SKILL.md — whose body is
// lazy-loaded and in practice never opened — these files sit in every
// session's context, so the poll instruction actually reaches the model on
// runtimes without a hook surface.
func mergeContext(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	var out bytes.Buffer
	switch {
	case bytes.Contains(data, []byte(contextMarkerEnd)):
		// Marked region: replace it wholesale on upgrade.
		start := bytes.Index(data, []byte(contextMarker))
		end := bytes.Index(data, []byte(contextMarkerEnd)) + len(contextMarkerEnd)
		if start < 0 || start > end {
			return false, nil
		}
		replaced := append(append([]byte{}, data[:start]...), []byte(strings.TrimSuffix(contextBlock, "\n"))...)
		replaced = append(replaced, data[end:]...)
		if bytes.Equal(replaced, data) {
			return false, nil
		}
		out.Write(replaced)
	case bytes.Contains(data, []byte(contextMarker)):
		// Legacy block without an end marker: upgrade only what we
		// verifiably wrote; a user-edited descendant stays untouched.
		upgraded := false
		for _, legacy := range legacyContextBlocks {
			if bytes.Contains(data, []byte(legacy)) {
				out.Write(bytes.Replace(data, []byte(legacy), []byte(contextBlock), 1))
				upgraded = true
				break
			}
		}
		if !upgraded {
			return false, nil
		}
	default:
		out.Write(data)
		if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
			out.WriteString("\n")
		}
		if len(data) > 0 {
			out.WriteString("\n")
		}
		out.WriteString(contextBlock)
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, out.Bytes(), 0644); err != nil {
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
	// The env upgrade only applies to sections the installer wrote itself,
	// recognized by the canonical GRPVN_STATE value. A user-customized env
	// table (different state path, different binary) is theirs — additive
	// TOML handling means we never modify what we can't prove we created.
	installerEnv := hasEnv && env["GRPVN_STATE"] != "" &&
		bytes.Contains(existing, []byte(fmt.Sprintf("%q", env["GRPVN_STATE"])))
	if hasSection && installerEnv && len(env) > 0 {
		// Upgrade path: the env table exists but may predate a newer key
		// (GRPVN_SCOPE arrived after GRPVN_STATE). Insert missing keys
		// directly under the env table header — the one place they're
		// guaranteed to land inside the right TOML table without parsing
		// the whole document.
		var missing []string
		for k := range env {
			if !bytes.Contains(existing, []byte(k+" = ")) {
				missing = append(missing, k)
			}
		}
		if len(missing) == 0 {
			return false, nil
		}
		sort.Strings(missing)
		idx := bytes.Index(existing, []byte(envHeader))
		lineEnd := idx + len(envHeader)
		if nl := bytes.IndexByte(existing[lineEnd:], '\n'); nl >= 0 {
			lineEnd += nl + 1
		} else {
			existing = append(existing, '\n')
			lineEnd = len(existing)
		}
		var merged bytes.Buffer
		merged.Write(existing[:lineEnd])
		for _, k := range missing {
			fmt.Fprintf(&merged, "%s = %q\n", k, env[k])
		}
		merged.Write(existing[lineEnd:])
		tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
		if err := os.WriteFile(tmp, merged.Bytes(), 0644); err != nil {
			return false, fmt.Errorf("write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return false, fmt.Errorf("rename %s: %w", path, err)
		}
		return true, nil
	}
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
