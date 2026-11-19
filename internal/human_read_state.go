package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type humanReadChange struct {
	Seq             int64    `json:"seq,omitempty"`
	Kind            string   `json:"kind"`
	Target          string   `json:"target,omitempty"`
	Position        int64    `json:"position,omitempty"`
	Held            *int64   `json:"held,omitempty"`
	Mutation        string   `json:"mutation,omitempty"`
	Version         string   `json:"version,omitempty"`
	ExpectedVersion *string  `json:"expected_version,omitempty"`
	RootID          string   `json:"root_id,omitempty"`
	Following       bool     `json:"following,omitempty"`
	IDs             []string `json:"ids,omitempty"`
	All             bool     `json:"all,omitempty"`
	Through         int64    `json:"through,omitempty"`
}
type humanReadPage struct {
	Changes []humanReadChange `json:"changes"`
	Cursor  int64             `json:"cursor"`
	More    bool              `json:"more"`
}

func lockHumanRead(db *sql.DB, tx dbtx, id string) error {
	if !isPostgres(db) {
		return nil
	}
	var locked string
	return tx.QueryRow("SELECT id FROM principals WHERE id=? FOR UPDATE", id).Scan(&locked)
}
func appendNativeHumanRead(tx dbtx, id string, change humanReadChange) (int64, error) {
	change.Seq = 0
	payload, err := json.Marshal(change)
	if err != nil {
		return 0, err
	}
	var seq int64
	err = tx.QueryRow("INSERT INTO human_read_events(principal_id,payload) VALUES(?,?) RETURNING seq", id, string(payload)).Scan(&seq)
	return seq, err
}
func conversationReadSnapshot(tx dbtx, id, target, mutation string) humanReadChange {
	change := humanReadChange{Kind: "conversation", Target: target, Mutation: mutation}
	_ = tx.QueryRow("SELECT position FROM cursors WHERE agent_name=? AND target=?", id, target).Scan(&change.Position)
	_ = tx.QueryRow("SELECT version FROM human_read_versions WHERE principal_id=? AND target=?", id, target).Scan(&change.Version)
	prefs := conversationSettings(tx, id, target)
	if prefs.Held.Valid {
		held := prefs.Held.Int64
		change.Held = &held
	}
	return change
}
func cachedRemotePosition(tx dbtx, alias, target, owner string, position int64) int64 {
	var native int64
	// Own queued messages may have a later server sequence than incoming
	// messages not downloaded yet. They cannot prove those messages were seen.
	_ = tx.QueryRow(`SELECT COALESCE(MAX(rm.remote_seq),0) FROM remote_messages rm JOIN messages m ON m.id=rm.local_id
 WHERE rm.remote_name=? AND m.target=? AND m.seq<=? AND NOT EXISTS(SELECT 1 FROM message_principals mp WHERE mp.message_id=m.id AND mp.principal_id=?)
 AND NOT EXISTS(SELECT 1 FROM remote_attributions a JOIN remotes r ON r.name=rm.remote_name JOIN remote_human_sessions h ON h.server_id=r.server_id WHERE a.local_id=m.id AND a.principal_id=h.principal_id)`, alias, target, position, owner).Scan(&native)
	return native
}
func cachedLocalPosition(tx dbtx, alias, target string, position int64) int64 {
	var local int64
	_ = tx.QueryRow("SELECT COALESCE(MAX(m.seq),0) FROM messages m JOIN remote_messages rm ON rm.local_id=m.id WHERE rm.remote_name=? AND m.target=? AND rm.remote_seq<=?", alias, target, position).Scan(&local)
	return local
}
func queueHumanRead(tx dbtx, owner, alias, key, root string, change humanReadChange) error {
	if change.Kind == "conversation" && change.Mutation == "read" {
		var previous string
		if tx.QueryRow("SELECT payload FROM human_read_outbox WHERE remote_name=? AND principal_id=? AND kind='conversation' AND target=? AND root_id=?", alias, owner, key, root).Scan(&previous) == nil {
			var pending humanReadChange
			if json.Unmarshal([]byte(previous), &pending) == nil && (pending.Mutation == "unread" || pending.Mutation == "resume") {
				change.Mutation = pending.Mutation
				change.ExpectedVersion = pending.ExpectedVersion
				if pending.Mutation == "unread" {
					change.Held = pending.Held
				}
			}
		}
	}
	change.Seq = 0
	payload, err := json.Marshal(change)
	if err != nil {
		return err
	}
	id := NewMessage("", "", nil).ID
	_, err = tx.Exec(`INSERT INTO human_read_outbox(id,remote_name,principal_id,kind,target,root_id,payload) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(remote_name,principal_id,kind,target,root_id) DO UPDATE SET id=excluded.id,payload=excluded.payload`, id, alias, owner, change.Kind, key, root, string(payload))
	if err != nil {
		return err
	}
	return wakeRemote(tx, alias)
}

func (s *webServer) recordHumanRead(tx dbtx, p *Principal, change humanReadChange) error {
	if p.Kind != "human" {
		return nil
	}
	if !s.local {
		_, err := appendNativeHumanRead(tx, p.ID, change)
		return err
	}
	if change.Kind == "inbox" {
		groups := map[string][]string{}
		for _, id := range change.IDs {
			var alias, native, owner string
			if tx.QueryRow("SELECT rm.remote_name,rm.remote_id,h.local_principal_id FROM remote_messages rm JOIN remotes r ON r.name=rm.remote_name JOIN remote_human_sessions h ON h.server_id=r.server_id WHERE rm.local_id=?", id).Scan(&alias, &native, &owner) == nil && owner == p.ID {
				groups[alias] = append(groups[alias], native)
			}
		}
		for alias, ids := range groups {
			for start := 0; start < len(ids); start += 100 {
				page := humanReadChange{Kind: "inbox", IDs: ids[start:min(start+100, len(ids))]}
				if err := queueHumanRead(tx, p.ID, alias, "inbox", NewMessage("", "", nil).ID, page); err != nil {
					return err
				}
			}
		}
		return nil
	}
	alias, native, mapped := splitRemoteConversation(change.Target)
	if !mapped {
		return nil
	}
	var owner string
	if tx.QueryRow("SELECT h.local_principal_id FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE r.name=?", alias).Scan(&owner) != nil || owner != p.ID {
		return nil
	}
	localTarget := change.Target
	change.Target = native
	change.Position = cachedRemotePosition(tx, alias, localTarget, p.ID, change.Position)
	if change.Held != nil {
		held := cachedRemotePosition(tx, alias, localTarget, p.ID, *change.Held)
		change.Held = &held
	}
	root := ""
	if change.RootID != "" {
		root = change.RootID
		if err := tx.QueryRow("SELECT remote_id FROM remote_messages WHERE remote_name=? AND local_id=?", alias, change.RootID).Scan(&change.RootID); err != nil {
			return err
		}
	}
	return queueHumanRead(tx, p.ID, alias, localTarget, root, change)
}

// A new device receives a compact current snapshot. Existing devices can
// replay the same snapshot after compaction without losing their cursor.
func (s *webServer) snapshotHumanReads(p *Principal) error {
	tx, err := beginWrite(s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockHumanRead(s.db, tx, p.ID); err != nil {
		return err
	}
	first, err := appendNativeHumanRead(tx, p.ID, humanReadChange{Kind: "checkpoint"})
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT target FROM cursors WHERE agent_name=? UNION SELECT target FROM conversation_preferences WHERE principal_id=? AND held_position IS NOT NULL`, p.ID, p.ID)
	if err != nil {
		return err
	}
	targets := []string{}
	for rows.Next() {
		var target string
		if err = rows.Scan(&target); err != nil {
			rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, target := range targets {
		change := conversationReadSnapshot(tx, p.ID, target, "")
		if _, err = appendNativeHumanRead(tx, p.ID, change); err != nil {
			return err
		}
	}
	rows, err = tx.Query("SELECT root_id,target,position,following FROM human_threads WHERE principal_id=?", p.ID)
	if err != nil {
		return err
	}
	threads := []humanReadChange{}
	for rows.Next() {
		change := humanReadChange{Kind: "thread"}
		if err = rows.Scan(&change.RootID, &change.Target, &change.Position, &change.Following); err != nil {
			rows.Close()
			return err
		}
		threads = append(threads, change)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, change := range threads {
		if _, err = appendNativeHumanRead(tx, p.ID, change); err != nil {
			return err
		}
	}
	rows, err = tx.Query("SELECT message_id FROM human_inbox WHERE principal_id=? AND read_at>0", p.ID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for start := 0; start < len(ids); start += 100 {
		if _, err = appendNativeHumanRead(tx, p.ID, humanReadChange{Kind: "inbox", IDs: ids[start:min(start+100, len(ids))]}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM human_read_events WHERE principal_id=? AND seq<?", p.ID, first); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *webServer) readMarkerChanges(w http.ResponseWriter, r *http.Request, p *Principal) {
	if p.Kind != "human" || s.local || (p.scopes != nil && !p.scopes["grpvn.read"]) {
		apiError(w, 403, "A human account read credential is required")
		return
	}
	after := int64(0)
	var err error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || after < 0 {
		apiError(w, 400, "Invalid read-state cursor")
		return
	}
	if after == 0 {
		if err = s.snapshotHumanReads(p); err != nil {
			apiError(w, 500, "Could not read account state")
			return
		}
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT seq,payload FROM human_read_events WHERE principal_id=? AND seq>? ORDER BY seq LIMIT 101", p.ID, after)
	if err != nil {
		apiError(w, 500, "Could not read account state")
		return
	}
	defer rows.Close()
	page := humanReadPage{Changes: []humanReadChange{}, Cursor: after}
	for rows.Next() {
		var seq int64
		var payload string
		if err = rows.Scan(&seq, &payload); err != nil {
			apiError(w, 500, "Could not read account state")
			return
		}
		if len(page.Changes) == 100 {
			page.More = true
			break
		}
		var change humanReadChange
		if err = json.Unmarshal([]byte(payload), &change); err != nil {
			apiError(w, 500, "Invalid stored read state")
			return
		}
		change.Seq = seq
		page.Changes = append(page.Changes, change)
		page.Cursor = seq
	}
	if rows.Err() != nil {
		apiError(w, 500, "Could not read account state")
		return
	}
	writeJSON(w, 200, page)
}

func localReadTarget(tx dbtx, alias, owner, native string) (string, error) {
	if strings.HasPrefix(native, "#") {
		var target string
		err := tx.QueryRow("SELECT target FROM remote_channels WHERE remote_name=? AND channel=?", alias, native).Scan(&target)
		return target, err
	}
	if strings.HasPrefix(native, "@") {
		var target string
		err := tx.QueryRow("SELECT target FROM remote_direct_threads WHERE remote_name=? AND local_principal_id=? AND peer_name=?", alias, owner, strings.TrimPrefix(native, "@")).Scan(&target)
		return target, err
	}
	return "", fmt.Errorf("invalid read-state target")
}

func applyRemoteHumanRead(tx dbtx, alias, owner string, change humanReadChange) error {
	if change.Kind == "checkpoint" {
		return nil
	}
	if change.Kind == "conversation" || change.Kind == "thread" {
		key := change.Target
		if change.Kind == "thread" {
			key = change.RootID
		}
		payload, _ := json.Marshal(change)
		if _, err := tx.Exec("INSERT INTO remote_read_states(remote_name,kind,state_key,payload) VALUES(?,?,?,?) ON CONFLICT(remote_name,kind,state_key) DO UPDATE SET payload=excluded.payload", alias, change.Kind, key, string(payload)); err != nil {
			return err
		}
	}
	if change.Kind == "inbox" {
		if change.All {
			_, err := tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND message_id IN(SELECT rm.local_id FROM remote_messages rm WHERE rm.remote_name=? AND rm.remote_seq<=?) AND read_at=0", nowMS(), owner, alias, change.Through)
			return err
		}
		for _, id := range change.IDs {
			local, err := localRemoteID(tx, alias, id)
			if err != nil {
				return err
			}
			if _, err = tx.Exec("INSERT INTO human_message_reads(principal_id,message_id,read_at) VALUES(?,?,?) ON CONFLICT(principal_id,message_id) DO UPDATE SET read_at=excluded.read_at", owner, local, nowMS()); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND message_id IN(SELECT local_id FROM remote_messages WHERE remote_name=? AND remote_id=?) AND read_at=0", nowMS(), owner, alias, id); err != nil {
				return err
			}
		}
		return nil
	}
	target, err := localReadTarget(tx, alias, owner, change.Target)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var dirty bool
	if tx.QueryRow("SELECT EXISTS(SELECT 1 FROM human_read_outbox WHERE remote_name=? AND principal_id=? AND kind=? AND target=?)", alias, owner, change.Kind, target).Scan(&dirty) != nil || dirty {
		return nil
	}
	position := cachedLocalPosition(tx, alias, target, change.Position)
	if change.Kind == "conversation" {
		if _, err = tx.Exec("INSERT INTO human_read_versions(principal_id,target,version) VALUES(?,?,?) ON CONFLICT(principal_id,target) DO UPDATE SET version=excluded.version", owner, target, change.Version); err != nil {
			return err
		}
		if err = advanceCursor(tx, owner, target, position); err != nil {
			return err
		}
		var held any
		if change.Held != nil {
			held = cachedLocalPosition(tx, alias, target, *change.Held)
		}
		if _, err = tx.Exec("INSERT INTO conversation_preferences(principal_id,target,held_position) VALUES(?,?,?) ON CONFLICT(principal_id,target) DO UPDATE SET held_position=excluded.held_position", owner, target, held); err != nil {
			return err
		}
		if change.Held == nil {
			_, err = tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND target=? AND read_at=0 AND message_id IN(SELECT id FROM messages WHERE parent_id IS NULL AND seq<=?)", nowMS(), owner, target, position)
		}
		return err
	}
	if change.Kind == "thread" {
		var root string
		if tx.QueryRow("SELECT local_id FROM remote_messages WHERE remote_name=? AND remote_id=?", alias, change.RootID).Scan(&root) != nil {
			return nil
		}
		var exists bool
		_ = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE id=?)", root).Scan(&exists)
		if !exists {
			return nil
		}
		if _, err = tx.Exec("INSERT INTO human_threads(principal_id,root_id,target,following,position) VALUES(?,?,?,?,?) ON CONFLICT(principal_id,root_id) DO UPDATE SET following=excluded.following,position=MAX(human_threads.position,excluded.position)", owner, root, target, change.Following, position); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND root_id=? AND read_at=0 AND message_id IN(SELECT id FROM messages WHERE seq<=?)", nowMS(), owner, root, position)
		return err
	}
	return fmt.Errorf("unknown account read-state event")
}
