package internal

// PRAGMAs come from the DSN in OpenDB so every connection in the pool gets
// them; restating them here would force the migration through an exclusive
// reconfiguration on every process startup and provoked SQLITE_BUSY on
// Windows when three agents raced the first migration on a shared DB.
const Schema = `
CREATE TABLE IF NOT EXISTS messages (
    id           TEXT PRIMARY KEY,
    sender       TEXT NOT NULL,
    target       TEXT NOT NULL,
    body         BLOB NOT NULL,
    chain_root   TEXT NOT NULL,
    chain_depth  INTEGER NOT NULL DEFAULT 0,
    parent_id    TEXT,
    correlation  TEXT,
    created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_target_id   ON messages(target, id);
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

CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);`
