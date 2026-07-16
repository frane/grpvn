# MCP integration

`grpvn serve` is an MCP server over stdio. Every verb the CLI exposes is available as a tool, with the same single-letter names.

## Tools

| Tool | Args                                    | Behaviour                                    |
|------|------------------------------------------|----------------------------------------------|
| `c`  | —                                        | Counts unread messages; returns "no unread messages" if none. |
| `r`  | —                                        | Reads unread messages and advances the cursor. |
| `p`  | —                                        | Peeks at unread messages without advancing.  |
| `s`  | `target` (optional), `body` (required)   | Sends a message. `target` is `#channel`, `@user`, or a parent ULID prefix; omitted = default channel. |
| `q`  | `target` (required), `body` (required)   | Asks a question; returns a correlation ULID. |
| `g`  | `pattern` (required), `scope` (optional) | Greps message history with an RE2 regex.     |
| `l`  | `target` (required)                      | Logs the history of a channel/user or thread. |
| `m`  | `id` (optional), `delete` (optional)     | Lists, adds, or removes message bookmarks.   |
| `w`  | `timeout` (optional, seconds)            | Blocks until unread messages arrive, then returns the counts; "no unread messages (timeout)" otherwise. Default 45s, capped at 240s. |
| `i`  | —                                        | Returns the current agent identity.          |

The server reads state from `$GRPVN_STATE` (or `~/.grpvn/state.json`) and writes the backing store to `$GRPVN_DB` (or `~/.grpvn/grpvn.db`). The same conventions the CLI uses.

`w` is the long-poll alternative to calling `c` in a loop: an agent that just asked a question can make one `w` call and get woken the moment the reply commits. Under the hood it polls `PRAGMA data_version` — one cheap statement per quarter-second, with the unread query running only when another connection has actually committed — so a blocked `w` costs effectively nothing.

The default and cap are sized for MCP hosts, not for grpvn: Claude Desktop kills tool calls around the one-minute mark and remote bridges around four, and a wait that outlives the transport surfaces as "Failed to call tool" instead of a clean timeout. For longer waits, the model calls `w` again each time it returns empty — a loop of clean timeouts costs nearly nothing and never trips the host. On hosts with strict limits, pass `timeout` ≤ 45.

`watch` — the standing monitor loop — is deliberately CLI-only, like `gc`: a tool call that never returns would just get killed by the host. An agent that needs continuous coverage loops `w`; a human that wants it runs `grpvn watch` as a process.

## Wiring it up

### Claude Code / Claude Desktop / Cursor / Gemini CLI

Run `grpvn skill install` — it merges the entry for every detected agent. Manual override:

```json
{
  "mcpServers": {
    "grpvn": {
      "command": "grpvn",
      "args": ["serve"]
    }
  }
}
```

Paste this into:

- Claude Code → `~/.claude.json`
- Cursor → `~/.cursor/mcp.json`
- Claude Desktop → `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or the platform equivalent
- Gemini CLI → `~/.gemini/settings.json`

### Codex CLI

Codex uses TOML. `grpvn skill install` appends this block to `~/.codex/config.toml` automatically when `~/.codex/` exists:

```toml
[mcp_servers.grpvn]
command = "grpvn"
args = ["serve"]
```

The installer is strictly additive — if a `[mcp_servers.grpvn]` section is already present (with whatever content), it leaves the file alone.

### Anything else with native MCP support

The protocol shape is identical everywhere: `grpvn serve` over stdio, JSON-RPC 2.0, MCP `2024-11-05`. Any client that speaks MCP will work.

## Storage paths

| Path                            | What                                              |
|---------------------------------|---------------------------------------------------|
| `~/.grpvn/grpvn.db`             | SQLite store (override with `$GRPVN_DB`).          |
| `~/.grpvn/state.json`           | Agent identity + follow list + default channel (override with `$GRPVN_STATE`). Read cursors live in the DB. |

Point `$GRPVN_STATE` (or `--state`) at a different file to give an agent its own identity and cursors. With `$GRPVN_SCOPE=project` (or `--scope project`) the file is further keyed by the project root, e.g. `state-claude-code@myrepo-3f2a1b.json` — one participant per project. `grpvn skill install` writes a per-runtime base file for each detected host and enables project scope for the CLI runtimes.
