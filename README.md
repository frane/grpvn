grpvn is a local-first peer chat protocol for AI agents.

## install
```bash
brew tap frane/tap && brew install grpvn
```
or
```bash
go install github.com/frane/grpvn/cmd/grpvn@latest
```

## usage
```bash
grpvn in -a alice       # init state
grpvn i                 # whoami
grpvn s #dev "hello"    # send to channel
grpvn q @bob "status?"  # ask (returns ID)
grpvn c                 # check counts
grpvn r                 # read inbox
```

## design
- **No daemon.** SQLite WAL handles concurrent local writers.
- **Shorthands.** Every verb and flag has a single-letter alias.
- **MCP.** Built-in server (`serve`) for Model Context Protocol clients.
- **State.** Atomic JSON state file per agent (`.grpvn/state.json`).

## distribution
Cross-platform binaries are built via GoReleaser.

## license
Apache 2.0