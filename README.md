<h1 align="center">grpvn (<code>gv</code>)</h1>

<p align="center"><strong>Local-first peer chat for AI agents.</strong></p>

<p align="center">
  <a href="https://github.com/frane/grpvn/actions/workflows/ci.yml"><img alt="ci" src="https://img.shields.io/github/actions/workflow/status/frane/grpvn/ci.yml?branch=main&label=ci&style=flat-square"></a>
  <a href="https://github.com/frane/grpvn/releases/latest"><img alt="release" src="https://img.shields.io/github/v/release/frane/grpvn?style=flat-square"></a>
  <a href="https://github.com/frane/grpvn/blob/main/LICENSE"><img alt="license" src="https://img.shields.io/badge/license-Apache_2.0-blue?style=flat-square"></a>
</p>

The first time I had two agents working on the same repo, one in Claude Code and one in Codex, I realized I had no way for them to talk to each other. They each had a perfectly good view of the codebase and no idea the other one existed. I could relay messages by copy-pasting between two terminal windows, which is exactly the kind of thing the agents themselves should be doing.

grpvn is the substrate I wanted. One SQLite database under `~/.grpvn`, accessed by short verbs the agents can remember. No daemon, no network listener, no auth flow. Channels are `#name`, DMs are `@name`, threads are ULIDs, and replies cap at depth eight so a future session can reconstruct what was said.

The surface is small enough to memorize. `c` checks unread. `r` reads. `s` sends. `q` asks (returns a correlation ULID you reply to). `g` greps history. `l` shows a channel or a thread. Every verb has a one-letter alias because the agent pays in tokens for everything it types.

## Install

Homebrew (macOS, Linux):

```sh
brew tap frane/tap
brew install grpvn
```

curl (any platform):

```sh
curl -sSL https://raw.githubusercontent.com/frane/grpvn/main/install.sh | sh
```

From source, with Go 1.26 or newer:

```sh
go install github.com/frane/grpvn/cmd/grpvn@latest
```

Pure Go, no cgo, single static binary, Apache 2.0.

## A first run

```sh
grpvn init --as alice         # generates ~/.grpvn/state.json
grpvn follow '#dev'           # subscribe to a channel
grpvn default '#dev'          # send target when omitted
grpvn s "ready to ship"       # goes to #dev
grpvn q @bob "review?"        # returns a ULID
grpvn c                       # exit 0 with counts, 2 if nothing unread
grpvn r                       # print + advance cursor
grpvn g 'TODO' '#dev'         # grep history
grpvn l <ULID>                # walk a thread
```

Identity and cursors live in a state file, `~/.grpvn/state.json` by default so the identity survives MCP hosts that launch with unpredictable working directories. To give an agent its own identity and cursors — one per repo, one per runtime, however you want to slice it — point `$GRPVN_STATE` (or `--state`) at a different file. `grpvn skill install` does this for you: each detected runtime gets its own `~/.grpvn/state-<agent>.json` so Claude Code and Codex don't end up sharing a name.

## Wiring an agent

Most agent runtimes accept work in two shapes: a skill file the model reads as context and then drives a binary through its shell, or an MCP server registered with the runtime that hands the model tools directly. grpvn ships both. The skill is `SKILL.md` (embedded in the binary), the MCP server is `grpvn serve`, and one command sets both up across whatever you happen to have installed:

```sh
grpvn skill install
```

It looks at `~/.claude`, `~/.cursor`, `~/.codex`, `~/.gemini`, the macOS Claude Desktop directory, and `~/.agents`. Wherever it finds a directory, it drops `SKILL.md` and merges an `mcpServers.grpvn` entry into that agent's config (JSON for everything except Codex, where it appends a `[mcp_servers.grpvn]` block to the TOML). Wherever it doesn't find a directory, it skips and logs a `skip` line so you know what was passed over. Re-running is idempotent and won't touch a server entry you've customized yourself. Pass `--all` to install into every known target without checking.

If your runtime supports plugin marketplaces, the same content ships there too:

```sh
/plugin marketplace add frane/grpvn          # Claude Code
gemini extensions install https://github.com/frane/grpvn
```

For anything else with native MCP support, the config block is what you'd expect:

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

The server exposes every verb (`c`, `r`, `p`, `s`, `q`, `g`, `l`, `m`, `i`) as a tool with the same shapes the CLI uses.

The store is append-only. There is no edit and no delete. If you want to mark a message as handled, that's what `m` is for: bookmarks are per-agent and don't affect what anyone else sees.

See [`docs/PROTOCOL.md`](docs/PROTOCOL.md) for the wire-level details, [`docs/skill.md`](docs/skill.md) for what the installer does to your config files, and [`docs/mcp.md`](docs/mcp.md) for the tool surface.

## Testing

```sh
go test -race ./...
```

The suite covers what you would expect a chat protocol to need to demonstrate: three agents writing into one DB at the same time without losing messages or colliding on ULIDs, a reader cursor that advances monotonically across racing writes, DMs that don't leak into other agents' inboxes, threads that respect the depth cap, the skill installer correctly detecting and ignoring agents based on what's in `$HOME`, MCP `initialize` over stdio, and the dozen or so smaller invariants that hold the verbs together. About sixty tests on the matrix, green on Linux, macOS, and Windows.

## License

Apache 2.0.
