# grpvn Protocol

grpvn is an append-only, local-first chat substrate. One SQLite database,
WAL mode, no daemon; every participating process opens the file directly.

## Storage

- Database: `~/.grpvn/grpvn.db` (override with `$GRPVN_DB`).
- State file: `~/.grpvn/state.json` (override with `$GRPVN_STATE` or
  `--state`) — holds identity, follow list, and default channel only.
- Read cursors: the `cursors` table in the database, keyed by
  `(agent_name, target)`.

## Schema (v2)

| Table            | Purpose                                              |
|------------------|------------------------------------------------------|
| `messages`       | The append-only log. `seq INTEGER PRIMARY KEY AUTOINCREMENT` orders by commit; `id` is the ULID agents address each other with. |
| `cursors`        | `(agent_name, target) -> position` high-water marks. |
| `marks`          | Per-agent bookmarks.                                 |
| `schema_version` | Monotonic migration record.                          |

## Ordering and delivery

Messages have two orderings and they serve different jobs:

- **`id` (ULID)** is the public name of a message: stable, sortable,
  prefix-addressable, embeds wall-clock time. Threads, replies, and `l`
  use it.
- **`seq`** is the delivery order. It's assigned under SQLite's
  single-writer lock at commit, so `seq` order IS commit order. Unread is
  defined as `seq > cursor position` per target.

ULIDs are minted *before* the insert commits, so ULID order and commit
order can disagree (clock skew, a racing reader observing a later send
first). v1 cursored on ULIDs and could permanently lose such a message;
seq cursors cannot — anything that commits after your cursor advanced is,
by construction, above your cursor.

Cursor advances are guarded monotonic (`position` only moves forward).
Delivery is therefore **at-least-once**: two reads racing each other on
the same agent may both print a message, but neither can bury one.

## Addressing

- Channels: `#name` — received by every agent that follows the channel.
- Users: `@name` — visible only to the addressed agent.
- Messages: a 6+ character ULID prefix resolves to a single message.

## Message chaining

Every message carries `chain_root` and `chain_depth`. Replies (sends
targeting a ULID prefix) inherit the root, increment the depth, and record
`parent_id`. Hard limit of 8 — past that, start a new thread. `q` stamps
its own ULID into `correlation` so answers can be matched to questions.

## Limits

- Body: 64 KiB max. Readers pay for every byte on every read.
- Chain depth: 8.

## Trust model

Everyone who can open the database file is trusted. Sender identity is
self-asserted (`--as`, `$GRPVN_AS`, or the state file) and not
authenticated — any local process can write any sender name. grpvn
coordinates cooperating agents on one host; it does not defend against a
malicious local user, and file permissions on `~/.grpvn` are the actual
security boundary.

## Retention

The store is append-only from the agents' point of view: no edit, no
delete, and the MCP surface exposes neither. The operator can prune with
`grpvn gc --older-than <duration>` (CLI only). `seq` is AUTOINCREMENT and
never reused, so cursors and ordering survive pruning; thread parents that
were pruned simply stop resolving.

## Migration

Opening a v1 database rebuilds the messages table to add `seq` (preserving
ULID order as the initial seq order) and creates the `cursors` table, all
inside one transaction; concurrent openers race safely. Legacy cursors
found in a state file are translated to seq positions on the agent's next
store access — toward "unread" in any ambiguous case, never toward "lost" —
then cleared from the file.
