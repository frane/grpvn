package internal

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
)

type conversationPreference struct {
	Muted bool
	Held  sql.NullInt64
}

func loadConversationSettings(db dbtx, id string) (map[string]conversationPreference, error) {
	rows, err := db.Query("SELECT target,muted,held_position FROM conversation_preferences WHERE principal_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]conversationPreference{}
	for rows.Next() {
		var target string
		var p conversationPreference
		if err = rows.Scan(&target, &p.Muted, &p.Held); err != nil {
			return nil, err
		}
		out[target] = p
	}
	return out, rows.Err()
}

func conversationSettings(db dbtx, id, target string) conversationPreference {
	var p conversationPreference
	_ = db.QueryRow("SELECT muted,held_position FROM conversation_preferences WHERE principal_id=? AND target=?", id, target).Scan(&p.Muted, &p.Held)
	return p
}

func conversationReadPosition(db dbtx, id, target string) (int64, bool) {
	p := conversationSettings(db, id, target)
	if p.Held.Valid {
		return p.Held.Int64, true
	}
	var pos int64
	_ = db.QueryRow("SELECT position FROM cursors WHERE agent_name=? AND target=?", id, target).Scan(&pos)
	return pos, false
}

func setConversationMute(db dbtx, id, target string, muted, dirty bool) error {
	_, err := db.Exec("INSERT INTO conversation_preferences(principal_id,target,muted,mute_dirty) VALUES(?,?,?,?) ON CONFLICT(principal_id,target) DO UPDATE SET muted=excluded.muted,mute_dirty=excluded.mute_dirty", id, target, muted, dirty)
	return err
}

func (s *webServer) preferences(w http.ResponseWriter, r *http.Request, p *Principal) {
	if r.Method != "PATCH" {
		apiError(w, 405, "Method not allowed")
		return
	}
	var a struct {
		Target string
		Muted  bool
	}
	if !webDecode(w, r, &a) {
		return
	}
	if !s.can(p, a.Target, false) {
		apiError(w, 403, "Conversation access denied")
		return
	}
	dirty := false
	if s.local {
		if alias, _, ok := splitRemoteConversation(a.Target); ok {
			_ = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE r.name=? AND h.local_principal_id=?)", alias, p.ID).Scan(&dirty)
		}
	}
	if setConversationMute(s.db, p.ID, a.Target, a.Muted, dirty) != nil {
		apiError(w, 500, "Could not update conversation")
		return
	}
	writeJSON(w, 200, map[string]bool{"muted": a.Muted})
}

func (s *webServer) readMarker(w http.ResponseWriter, r *http.Request, p *Principal) {
	var a struct {
		Target          string
		Position        *int64
		Unread, Resume  bool
		ExpectedVersion *string
	}
	if !webDecode(w, r, &a) {
		return
	}
	if !s.can(p, a.Target, false) {
		apiError(w, 403, "Conversation access denied")
		return
	}
	var max int64
	_, _, scopedDM := splitRemoteDMTarget(a.Target)
	if strings.HasPrefix(a.Target, "@") && !scopedDM {
		predicate, args := directConversationPredicate(s.db, principalAddress(p), strings.TrimPrefix(a.Target, "@"))
		_ = s.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages m WHERE "+predicate, args...).Scan(&max)
	} else {
		_ = s.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages WHERE target=?", a.Target).Scan(&max)
	}
	pos := max
	if a.Position != nil {
		pos = *a.Position
	} else if a.Unread && pos > 0 {
		pos--
	}
	if pos < 0 || pos > max {
		apiError(w, 400, "Invalid read position")
		return
	}
	tx, err := beginWrite(s.db)
	if err != nil {
		apiError(w, 500, "Could not update read state")
		return
	}
	defer tx.Rollback()
	if err = lockHumanRead(s.db, tx, p.ID); err != nil {
		apiError(w, 500, "Could not update read state")
		return
	}
	before := conversationReadSnapshot(tx, p.ID, a.Target, "")
	if a.Resume && a.ExpectedVersion != nil && *a.ExpectedVersion != before.Version {
		a.Resume = false
	}
	mutation := "read"
	if a.Unread {
		mutation = "unread"
		_, err = tx.Exec("INSERT INTO conversation_preferences(principal_id,target,held_position) VALUES(?,?,?) ON CONFLICT(principal_id,target) DO UPDATE SET held_position=excluded.held_position", p.ID, a.Target, pos)
	} else {
		if a.Resume {
			mutation = "resume"
			_, err = tx.Exec("UPDATE conversation_preferences SET held_position=NULL WHERE principal_id=? AND target=?", p.ID, a.Target)
		}
		if err == nil {
			err = advanceCursor(tx, p.ID, a.Target, pos)
		}
		settings := conversationSettings(tx, p.ID, a.Target)
		if err == nil && !settings.Held.Valid {
			_, err = tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND target=? AND read_at=0 AND message_id IN(SELECT id FROM messages WHERE parent_id IS NULL AND seq<=?)", nowMS(), p.ID, a.Target, pos)
		}
	}
	if err == nil && (a.Unread || a.Resume && before.Held != nil) {
		_, err = tx.Exec("INSERT INTO human_read_versions(principal_id,target,version) VALUES(?,?,?) ON CONFLICT(principal_id,target) DO UPDATE SET version=excluded.version", p.ID, a.Target, NewMessage("", "", nil).ID)
	}
	state := conversationReadSnapshot(tx, p.ID, a.Target, mutation)
	state.ExpectedVersion = a.ExpectedVersion
	sameHeld := before.Held == nil && state.Held == nil || before.Held != nil && state.Held != nil && *before.Held == *state.Held
	changed := state.Position != before.Position || !sameHeld || state.Version != before.Version
	if err == nil && changed {
		err = s.recordHumanRead(tx, p, state)
	}
	if err == nil && changed {
		err = touchPersonalMetadata(tx, p.ID)
	}
	if err != nil || tx.Commit() != nil {
		apiError(w, 500, "Could not update read state")
		return
	}
	effective := state.Position
	if state.Held != nil {
		effective = *state.Held
	}
	writeJSON(w, 200, map[string]any{"ok": true, "position": effective, "held": state.Held != nil, "read_version": state.Version, "read_state": state})
}

// Personal mute changes use the existing source lease, pacing and retry budget.
// Old coordinators keep local settings until they advertise this capability.
func syncWorkspacePreferences(ctx context.Context, db *sql.DB, remote Remote) error {
	var supported int
	if db.QueryRow("SELECT value FROM chat_metadata WHERE key=?", "prefs:"+remote.ServerID).Scan(&supported) != nil || supported != 1 {
		return nil
	}
	var owner string
	if db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner) != nil {
		return nil
	}
	prefix := "#" + remote.Name + "/"
	escape := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	rows, err := db.Query("SELECT target,muted FROM conversation_preferences WHERE principal_id=? AND mute_dirty=1 AND (target LIKE ? ESCAPE '!' OR target LIKE ? ESCAPE '!') ORDER BY target LIMIT 10", owner, escape.Replace(prefix)+"%", escape.Replace("@"+remote.Name+"/")+"%")
	if err != nil {
		return err
	}
	type update struct {
		target string
		muted  bool
	}
	var changes []update
	for rows.Next() {
		var u update
		if err = rows.Scan(&u.target, &u.muted); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, u := range changes {
		var result map[string]any
		_, native, _ := splitRemoteConversation(u.target)
		if err = remoteRequest(ctx, remote, "PATCH", "/v1/chat/preferences", map[string]any{"Target": native, "Muted": u.muted}, &result); err != nil {
			return err
		}
		if _, err = db.Exec("UPDATE conversation_preferences SET mute_dirty=0 WHERE principal_id=? AND target=? AND muted=?", owner, u.target, u.muted); err != nil {
			return err
		}
	}
	return nil
}
