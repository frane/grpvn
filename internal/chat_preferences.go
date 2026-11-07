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
		if alias, _, ok := splitRemoteTarget(a.Target); ok {
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
		Target         string
		Position       *int64
		Unread, Resume bool
	}
	if !webDecode(w, r, &a) {
		return
	}
	if !s.can(p, a.Target, false) {
		apiError(w, 403, "Conversation access denied")
		return
	}
	var max int64
	if strings.HasPrefix(a.Target, "@") {
		_ = s.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages WHERE (sender=? AND target=?) OR (sender=? AND target=?)", principalAddress(p), a.Target, strings.TrimPrefix(a.Target, "@"), "@"+principalAddress(p)).Scan(&max)
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
	if a.Unread {
		_, err := s.db.Exec("INSERT INTO conversation_preferences(principal_id,target,held_position) VALUES(?,?,?) ON CONFLICT(principal_id,target) DO UPDATE SET held_position=excluded.held_position", p.ID, a.Target, pos)
		if err != nil {
			apiError(w, 500, "Could not mark unread")
			return
		}
		_ = TouchChatMetadata(s.db)
	} else {
		if a.Resume {
			if _, err := s.db.Exec("UPDATE conversation_preferences SET held_position=NULL WHERE principal_id=? AND target=?", p.ID, a.Target); err != nil {
				apiError(w, 500, "Could not mark read")
				return
			}
		}
		if advanceCursor(s.db, p.ID, a.Target, pos) != nil {
			apiError(w, 500, "Could not mark read")
			return
		}
		if a.Resume {
			_ = TouchChatMetadata(s.db)
		}
	}
	effective, held := conversationReadPosition(s.db, p.ID, a.Target)
	writeJSON(w, 200, map[string]any{"ok": true, "position": effective, "held": held})
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
	rows, err := db.Query("SELECT target,muted FROM conversation_preferences WHERE principal_id=? AND mute_dirty=1 AND target LIKE ? ESCAPE '!' ORDER BY target LIMIT 10", owner, strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(prefix)+"%")
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
		if err = remoteRequest(ctx, remote, "PATCH", "/v1/chat/preferences", map[string]any{"Target": "#" + strings.TrimPrefix(u.target, prefix), "Muted": u.muted}, &result); err != nil {
			return err
		}
		if _, err = db.Exec("UPDATE conversation_preferences SET mute_dirty=0 WHERE principal_id=? AND target=? AND muted=?", owner, u.target, u.muted); err != nil {
			return err
		}
	}
	return nil
}
