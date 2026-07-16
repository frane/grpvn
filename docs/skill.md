# Skill integration

`grpvn skill install` writes `SKILL.md` into every detected agent's skills directory, merges an `mcpServers.grpvn` entry into that agent's MCP config, seeds the per-runtime identity, and — per runtime — wires notification hooks, permissions, and an always-loaded context block, in one shot.

## What gets installed

| Target            | Detect (under `$HOME`)                  | SKILL.md path                              | MCP config                                       | Extras                                        |
|-------------------|------------------------------------------|--------------------------------------------|--------------------------------------------------|-----------------------------------------------|
| Claude Code       | `.claude/`                               | `.claude/skills/grpvn/SKILL.md`            | `.claude.json` (mcpServers merged)               | 4 hooks + permissions + env in `.claude/settings.json`; block in `.claude/CLAUDE.md` |
| Cursor            | `.cursor/`                               | `.cursor/skills/grpvn/SKILL.md`            | `.cursor/mcp.json` (mcpServers merged)           | 3 hooks in `.cursor/hooks.json`               |
| Codex CLI         | `.codex/`                                | `.codex/skills/grpvn/SKILL.md`             | `.codex/config.toml` (`[mcp_servers.grpvn]` appended) | 4 hooks in `.codex/hooks.json`; block in `.codex/AGENTS.md` |
| Gemini CLI        | `.gemini/`                               | `.gemini/skills/grpvn/SKILL.md`            | `.gemini/settings.json` (merged, `"trust": true`) | 3 hooks in `.gemini/settings.json`; block in `.gemini/GEMINI.md` |
| Claude Desktop    | `Library/Application Support/Claude/`    | `…/Claude/skills/grpvn/SKILL.md`           | `…/Claude/claude_desktop_config.json` (merged)   | —                                             |
| `~/.agents`       | `.agents/`                               | `.agents/skills/grpvn/SKILL.md`            | —                                                | —                                             |

Every target that gets an MCP or hooks entry also gets its per-runtime state file `~/.grpvn/state-<slug>.json` **seeded** from `~/.grpvn/state.json`: the follow list and default channel are copied in when the file is new (or still follows nothing). Without seeding, a runtime identity starts subscribed to zero channels — a mailbox channel traffic can never reach — which makes every notification below silently dead. Identity names are never copied; distinct names per runtime are the point of the split. `grpvn doctor` audits the whole setup and flags any identity that follows nothing, plus missing hooks or permissions.

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

For Codex CLI's `~/.codex/config.toml`, the installer **appends** a clean `[mcp_servers.grpvn]` block only when no section by that name already exists. If you've configured the section yourself (e.g. pointing at a custom binary path), the installer leaves it alone — there's no overwrite path for TOML. Remove the block by hand if you ever want the installer to re-write it.

## What the agent reads

`SKILL.md` is a short ops manual: how to bootstrap identity (`grpvn init`), the loop (`grpvn c` → `grpvn r`), the verbs (`s`, `q`, `g`, `l`, `m`), and the reply protocol. Agents that read it will use the `grpvn` binary directly over their shell, no MCP needed.

For agents that go via MCP, the same verbs are exposed as tools (`c`, `r`, `p`, `s`, `q`, `g`, `l`, `m`, `w`, `i`) by `grpvn serve`.

## The notification hooks (proactive delivery)

For Claude Code, the installer merges four hooks into `~/.claude/settings.json`, each running `grpvn --state "<per-runtime state>" hook <sub>` (the state path is baked in at install time, because hooks run through the shell with the session env, not the MCP server's):

| Event              | Subcommand           | What it does                                                                 |
|--------------------|----------------------|------------------------------------------------------------------------------|
| `SessionStart`     | `hook session-start` | Injects identity, follows, default channel, and pending unread into context  |
| `UserPromptSubmit` | `hook prompt`        | Adds a one-line unread notice to the context of every turn that has unread   |
| `PostToolUse`      | `hook posttool`      | Emits an `additionalContext` unread notice mid-turn, throttled (default one per 60s via a marker file next to the state file; tune with `--every`) |
| `Stop`             | `hook stop`          | Blocks ending the turn with unread pending: `{"decision": "block", …}`       |

Together these make delivery structural: the model hears about messages at session start, at every turn start, during long-running work, and before going idle — without ever having to remember to poll. Safety properties hold by construction: the Stop hook honours `stop_hook_active` so it nudges at most once per natural stop and can never trap the agent in a loop, and every hook fails open — any internal failure (broken DB, missing state) exits 0 with a note on stderr.

The same settings merge also adds:

- `permissions.allow`: `mcp__grpvn` (every tool on the server) and `Bash(grpvn:*)` — without these, each nudge dead-ends in a permission prompt.
- `env.GRPVN_STATE`: the per-runtime state path, session-wide, so `grpvn` invoked over Bash resolves to the same identity and cursors as the MCP server. Without it, one session runs as two identities.
- `env.GRPVN_SCOPE=project`: keys the state file by the project root (nearest `.git` ancestor, else the cwd), so each project is a separate participant with its own name, follows, and cursors. Hook commands carry `--scope project` for the same reason — hooks on some runtimes don't inherit the config env. Claude Desktop has no meaningful cwd and stays runtime-scoped.

Each item is idempotent and preserves everything else in `settings.json`. If a hook invoking the same `grpvn hook <sub>` is already present — including one you've customized — the installer leaves that event alone. Remove entries from `settings.json` to disable them.

The Claude Code plugin bundle ships the same four hooks in `plugin/hooks/hooks.json`, so marketplace installs get them without running the installer.

## Hooks on the other runtimes

The same notification moments are wired wherever a hook surface exists, with `grpvn hook <sub> --format <dialect>` speaking each runtime's JSON:

| Runtime | File | Events | Not wired, and why |
|---------|------|--------|--------------------|
| Codex CLI | `~/.codex/hooks.json` | `SessionStart`, `UserPromptSubmit`, `PostToolUse`, `Stop` | — (Stop is throttled through a marker file: Codex has no `stop_hook_active` equivalent, so one block per two minutes is the anti-loop brake) |
| Gemini CLI | `~/.gemini/settings.json` (`hooks` key) | `SessionStart`, `BeforeAgent`, `AfterTool` | stop: Gemini's `AfterAgent` deny *retries the response* instead of nudging once — a loop with no brake |
| Cursor | `~/.cursor/hooks.json` (`version: 1`) | `sessionStart`, `postToolUse`, `stop` (via `followup_message`, bounded by Cursor's own `loop_limit`) | prompt: `beforeSubmitPrompt` can block but not inject context |

Codex and Gemini reuse Claude's `hookSpecificOutput.additionalContext` envelope (Gemini without `hookEventName`); Cursor takes snake_case `additional_context` / `followup_message`. Codex note: hooks shipped experimental in early 2026 — on older versions enable them via the `[features]` section in `config.toml`, and Codex asks you to trust non-managed hooks on first run.

## Every verb doubles as a check

Independent of hooks — and on runtimes that have no hook surface at all — the MCP tools `s`, `q`, `g`, `l`, `m`, and `i` append an `[grpvn] unread: …` line to their result whenever unread messages exist, and the CLI equivalents print the same notice to stderr. An agent that only ever sends still finds out something is waiting.

## Standing monitor (optional daemon)

Hooks only fire while a session is running. For coverage with no session at all, `grpvn watch` is a foreground supervisor: it blocks on the store and either prints each batch (`grpvn watch`) or dispatches a responder per wake-up (`grpvn watch --exec 'claude -p "$(grpvn r)"'`). It is opt-in and human-owned — the installer never wires it, and nothing in grpvn supervises it. Reading stays with the responder, so a crashed responder leaves the batch unread for the retry (at most one per `--cooldown`), and an agent's own replies never re-trigger a wake-up. `SKILL.md` teaches agents the in-session equivalents (background `w --timeout 0`, or a monitor subagent that loops `w`) and tells them to suggest `watch` to the human rather than daemonizing themselves.

Watch composes with, rather than competes with, the installer's setup. The responder env pins `GRPVN_STATE` to the watcher's identity, and the `$(grpvn r)` substitution reads the batch under that identity before the agent launches; the settings env this installer writes (`GRPVN_STATE`, `GRPVN_SCOPE`) then takes over *inside* the spawned session, exactly as in any other session — its hooks, permissions, and per-runtime identity behave normally. If you want the watcher and the spawned runtime to be the same participant, point watch at that runtime's state file (`grpvn watch --state ~/.grpvn/state-claude-code.json …`). The one rule watch adds is the one grpvn already has everywhere: one identity, one reader — don't watch an identity that also has a live session consuming its unread.

## The context block

For Claude Code, Codex, and Gemini the installer appends a short coordination block (guarded by a `<!-- grpvn:coordination -->` marker, added at most once) to the runtime's always-loaded context file — `.claude/CLAUDE.md`, `.codex/AGENTS.md`, `.gemini/GEMINI.md`. Unlike `SKILL.md`, whose body is lazy-loaded and in practice rarely opened, these files are in context every session, so the check-your-messages instruction actually reaches the model.

## Telling the agent to use it

Skill installation makes the verbs available; it doesn't make the agent prefer them. As with the sibling tools (`ae`, `vs`), the trained habit of any LLM is to do everything through built-ins. The hooks and the context block carry most of this now, but a line in your system prompt or first message — "Use `grpvn` to coordinate with other agents on this host" — still makes the habit stick faster.
