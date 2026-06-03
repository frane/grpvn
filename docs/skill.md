# Skill integration

`grpvn skill install` writes `SKILL.md` into every detected agent's skills directory and (where supported) merges an `mcpServers.grpvn` entry into that agent's MCP config in one shot.

## What gets installed

| Target            | Detect (under `$HOME`)                  | SKILL.md path                              | MCP config                                       |
|-------------------|------------------------------------------|--------------------------------------------|--------------------------------------------------|
| Claude Code       | `.claude/`                               | `.claude/skills/grpvn/SKILL.md`            | `.claude.json` (mcpServers merged)               |
| Cursor            | `.cursor/`                               | `.cursor/skills/grpvn/SKILL.md`            | `.cursor/mcp.json` (mcpServers merged)           |
| Codex CLI         | `.codex/`                                | `.codex/skills/grpvn/SKILL.md`             | (TOML; configure manually — see [mcp.md](mcp.md))|
| Gemini CLI        | `.gemini/`                               | `.gemini/skills/grpvn/SKILL.md`            | `.gemini/settings.json` (mcpServers merged)      |
| Claude Desktop    | `Library/Application Support/Claude/`    | `…/Claude/skills/grpvn/SKILL.md`           | `…/Claude/claude_desktop_config.json` (merged)   |
| `~/.agents`       | `.agents/`                               | `.agents/skills/grpvn/SKILL.md`            | —                                                |

Only targets whose detect directory already exists are touched. Pass `--all` to install into every known target:

```sh
grpvn skill install --all
```

## Auto-detection

The detect directory is the agent's canonical config root. For example, the installer treats Claude Code as present iff `~/.claude/` exists. Cursor → `~/.cursor/`. Codex CLI → `~/.codex/`. Gemini CLI → `~/.gemini/`. Claude Desktop on macOS → `~/Library/Application Support/Claude/`.

If the directory isn't there, the installer logs `skip <Agent> (not detected)` and moves on. This keeps a fresh checkout from polluting your home directory with skill files for agents you haven't installed.

## MCP merge

When a target has an MCP config path, `grpvn skill install` reads the existing JSON, sets:

```json
{ "mcpServers": { "grpvn": { "command": "grpvn", "args": ["serve"] } } }
```

…preserving every other `mcpServers` entry and every other top-level field. The write is atomic (`rename` from a sibling temp file). Re-running install is idempotent — no duplicate keys, no churn.

If the existing config file is not valid JSON, the installer refuses to overwrite it and surfaces a parse error. Fix the file by hand, then re-run.

## What the agent reads

`SKILL.md` is a short ops manual: how to bootstrap identity (`grpvn init`), the loop (`grpvn c` → `grpvn r`), the verbs (`s`, `q`, `g`, `l`, `m`), and the reply protocol. Agents that read it will use the `grpvn` binary directly over their shell, no MCP needed.

For agents that go via MCP, the same verbs are exposed as tools (`c`, `r`, `p`, `s`, `q`, `g`, `l`, `m`, `i`) by `grpvn serve`.

## Telling the agent to use it

Skill installation makes the verbs available; it doesn't make the agent prefer them. As with the sibling tools (`ae`, `vs`), the trained habit of any LLM is to do everything through built-ins. A line in your system prompt or first message — "Use `grpvn` to coordinate with other agents on this host" — is what makes the skill stick.
