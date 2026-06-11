package internal

// PRAGMAs here are also set in OpenDB's DSN — they're restated on the
// migration connection because modernc.org/sqlite doesn't always have the
// DSN pragmas in effect before the first non-PRAGMA statement of the
// connection runs, and without them the very first migration on a shared DB
// can race a sibling process to SQLITE_BUSY.
//
// Schema v2: messages carry an explicit `seq INTEGER PRIMARY KEY
// AUTOINCREMENT`. seq is assigned under SQLite's single-writer lock at
// commit time, so commit order IS seq order — unlike ULIDs, which are
// minted before the insert commits and can therefore appear "in the past"
// relative to a cursor that already advanced. Cursors live in the database
// (per agent, per target) instead of state.json, so reading is one store's
// problem rather than a load-modify-save race across files. AUTOINCREMENT
// (plus the explicit INTEGER PRIMARY KEY) also pins seq across VACUUM and
// guarantees it never regresses, which `gc` relies on.
const Schema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS messages (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    sender       TEXT NOT NULL,
    target       TEXT NOT NULL,
    body         BLOB NOT NULL,
    chain_root   TEXT NOT NULL,
    chain_depth  INTEGER NOT NULL DEFAULT 0,
    parent_id    TEXT,
    correlation  TEXT,
    created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_target      ON messages(target);
CREATE INDEX IF NOT EXISTS idx_messages_chain_root  ON messages(chain_root);
CREATE INDEX IF NOT EXISTS idx_messages_correlation ON messages(correlation);
CREATE INDEX IF NOT EXISTS idx_messages_parent      ON messages(parent_id);

CREATE TABLE IF NOT EXISTS marks (
    agent_name   TEXT NOT NULL,
    message_id   TEXT NOT NULL,
    marked_at    INTEGER NOT NULL,
    PRIMARY KEY (agent_name, message_id),
    FOREIGN KEY (message_id) REFERENCES messages(id)
);

CREATE TABLE IF NOT EXISTS cursors (
    agent_name   TEXT NOT NULL,
    target       TEXT NOT NULL,
    position     INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (agent_name, target)
);

CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);`

// migrateV1toV2 rebuilds the messages table to add the seq column and is
// executed inside a transaction by Migrate. ORDER BY id preserves the v1
// notion of message order (ULIDs) as the initial seq assignment, so
// migrated history reads back in the same sequence it always did.
const migrateV1toV2 = `
CREATE TABLE messages_v2 (
    seq          INTEGER PRIMARY KEY AUTOINCREMENT,
    id           TEXT NOT NULL UNIQUE,
    sender       TEXT NOT NULL,
    target       TEXT NOT NULL,
    body         BLOB NOT NULL,
    chain_root   TEXT NOT NULL,
    chain_depth  INTEGER NOT NULL DEFAULT 0,
    parent_id    TEXT,
    correlation  TEXT,
    created_at   INTEGER NOT NULL
);
INSERT INTO messages_v2 (id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at)
    SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at
    FROM messages ORDER BY id ASC;
DROP TABLE messages;
ALTER TABLE messages_v2 RENAME TO messages;
CREATE INDEX IF NOT EXISTS idx_messages_target      ON messages(target);
CREATE INDEX IF NOT EXISTS idx_messages_chain_root  ON messages(chain_root);
CREATE INDEX IF NOT EXISTS idx_messages_correlation ON messages(correlation);
CREATE INDEX IF NOT EXISTS idx_messages_parent      ON messages(parent_id);
DROP INDEX IF EXISTS idx_messages_target_id;
CREATE TABLE IF NOT EXISTS cursors (
    agent_name   TEXT NOT NULL,
    target       TEXT NOT NULL,
    position     INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (agent_name, target)
);`
