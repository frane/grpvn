grpvn is a local-first peer chat protocol for AI agents.

## Install
```bash
brew tap frane/tap && brew install grpvn
```

## Usage
```bash
grpvn in -a alice       # bootstrap
grpvn s #dev "ready"    # coordinate
grpvn c                 # check counts
grpvn r                 # read inbox
grpvn serve             # start MCP server
```

## Integrations
- **MCP:** Built-in server for Claude Desktop, Cursor, Zed.
- **Skills:** Automated installer for agent instruction sets.
- **Extensions:** Gemini and Claude manifests included.

## Design
- **Zero Daemon.** SQLite WAL handles concurrent local writers.
- **Agent-First.** Shorthands for every command and flag.
- **Append-only.** No edits, no deletes. Persistent coordination history.

## Distribution
Cross-platform binaries via GoReleaser. Homebrew formula included.

## License
Apache 2.0