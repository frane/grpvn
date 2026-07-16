package internal

import (
	"database/sql"
	"fmt"
	"time"
)

// Cursors are (agent, target) -> seq positions in the cursors table. seq is
// assigned at commit under SQLite's single-writer lock, so `seq > position`
// can never skip a message the way `id > ulid` could: a ULID is minted
// before its insert commits, so a racing reader could advance past it while
// it was still in flight, losing it forever. Advancing is guarded to be
// monotonic, which makes delivery at-least-once: two racing reads by the
// same agent may both print a message, but neither can bury one.

// loadCursors returns the agent's positions; absent targets read as 0,
// which means "everything in this target is unread".
func loadCursors(db *sql.DB, agent string) (map[string]int64, error) {
	rows, err := db.Query("SELECT target, position FROM cursors WHERE agent_name = ?", agent)
	if err != nil {
		return nil, fmt.Errorf("load cursors: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var target string
		var pos int64
		if err := rows.Scan(&target, &pos); err != nil {
			return nil, err
		}
		out[target] = pos
	}
	return out, rows.Err()
}

// advanceCursor moves the agent's cursor for a target forward. The
// conflict guard keeps it monotonic under concurrent reads — a stale
// advance silently loses to a fresher one.
func advanceCursor(db *sql.DB, agent, target string, pos int64) error {
	_, err := db.Exec(`INSERT INTO cursors (agent_name, target, position, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (agent_name, target) DO UPDATE
		SET position = excluded.position, updated_at = excluded.updated_at
		WHERE excluded.position > cursors.position`,
		agent, target, pos, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("advance cursor %s/%s: %w", agent, target, err)
	}
	return nil
}

// MigrateLegacyCursors moves pre-v2 cursors out of state.json into the
// cursors table, then clears them from the state file. v1 cursors were
// ULIDs with `id > cursor` semantics; the equivalent seq position is the
// highest seq among that target's messages at or before the ULID. The
// insert is monotonic-guarded, so re-running (or racing another process on
// the same state file) can only ever move cursors forward. Lossy edge
// cases resolve toward "unread", never toward "lost".
func MigrateLegacyCursors(db *sql.DB, st *State, statePath string) error {
	if st.Cursor == "" && len(st.Cursors) == 0 {
		return nil
	}
	legacy := map[string]string{}
	if st.Cursor != "" {
		// The v0.1.x scalar applied to every target the agent read.
		for _, t := range targetsFor(st.Name, st.Follow) {
			legacy[t] = st.Cursor
		}
	}
	for target, ulid := range st.Cursors {
		legacy[target] = ulid
	}
	for target, ulid := range legacy {
		var pos int64
		err := db.QueryRow(
			"SELECT COALESCE(MAX(seq), 0) FROM messages WHERE target = ? AND id <= ?",
			target, ulid,
		).Scan(&pos)
		if err != nil {
			return fmt.Errorf("translate legacy cursor for %s: %w", target, err)
		}
		if pos == 0 {
			continue
		}
		if err := advanceCursor(db, st.Name, target, pos); err != nil {
			return err
		}
	}
	st.Cursor = ""
	st.Cursors = nil
	if err := st.Save(statePath); err != nil {
		return fmt.Errorf("clear legacy cursors: %w", err)
	}
	return nil
}

// FastForwardCursors positions the agent's cursor for every followed
// channel and its DM inbox at the store's current tail. Called once when
// an identity is minted: a brand-new participant starts reading from now
// instead of replaying the host's entire history as unread — which in the
// field meant a first Stop hook blocking on 200 ancient messages and the
// agent paying tokens to read them all. History stays one `l` away.
func FastForwardCursors(db *sql.DB, st *State) error {
	for _, target := range targetsFor(st.Name, st.Follow) {
		if err := fastForwardTarget(db, st.Name, target); err != nil {
			return err
		}
	}
	return nil
}

func fastForwardTarget(db *sql.DB, agent, target string) error {
	var max sql.NullInt64
	if err := db.QueryRow("SELECT MAX(seq) FROM messages WHERE target = ?", target).Scan(&max); err != nil {
		return err
	}
	if !max.Valid {
		return nil
	}
	return advanceCursor(db, agent, target, max.Int64)
}
