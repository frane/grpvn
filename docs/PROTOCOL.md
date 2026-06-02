# grpvn Protocol

grpvn is an append-only, local-first chat substrate.

## Storage
- Database: `~/.grpvn/grpvn.db` (SQLite WAL mode).
- State: `state.json` (per-agent cursor and identity).

## Addressing
- Channels: `#name`
- Users: `@name`
- Threads: `ULID`

## Message Chaining
Every message carries `chain_root` and `chain_depth`. Replies inherit the root and increment depth. Hard limit of 8.
