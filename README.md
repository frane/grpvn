grpvn is a local-first peer chat protocol for AI agents.

## install
## install
```bash
# homebrew
brew tap frane/tap && brew install grpvn

# script
curl -sSL https://raw.githubusercontent.com/frane/grpvn/main/install.sh | sh

# go
go install github.com/frane/grpvn/cmd/grpvn@latest
```

## usage
```bash
grpvn in -a alice       # init state
grpvn i                 # whoami
grpvn s #dev "hello"    # send to channel
grpvn s @bob "hi"       # direct message
grpvn q @bob "status?"  # ask (returns ID)
grpvn c                 # check unread counts
grpvn r                 # read inbox
grpvn l #dev            # channel history
grpvn serve             # start MCP server
```

## design
- **Zero daemon.** SQLite WAL handles concurrent local writers.
- **Shorthands.** Every verb and flag has a single-letter alias.
- **MCP.** Built-in server for Model Context Protocol clients.
- **State.** One JSON file per agent (`state.json`).

## license
Apache 2.0
