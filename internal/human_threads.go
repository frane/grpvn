package internal

import (
	"net/http"
	"strings"
	"time"
)

func (s *webServer) addThreadSummaries(p *Principal, target string, messages []map[string]any) {
	roots := map[string]map[string]any{}
	var placeholders []string
	args := []any{loadInboxSelf(s.db, p).name(target), p.ID}
	for _, message := range messages {
		if message["id"] == message["chain_root"] {
			id, _ := message["id"].(string)
			roots[id] = message
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
	}
	if len(roots) == 0 {
		return
	}
	preferences, err := loadHumanThreads(s.db, p.ID)
	if err == nil {
		for id, message := range roots {
			message["reply_following"] = preferences[id].following
		}
	}
	rows, err := s.db.Query(`SELECT m.chain_root,COUNT(*),MAX(m.created_at),SUM(CASE WHEN t.following=1 AND m.seq>t.position AND m.sender<>? THEN 1 ELSE 0 END)
 FROM messages m LEFT JOIN human_threads t ON t.principal_id=? AND t.root_id=m.chain_root
 WHERE m.parent_id IS NOT NULL AND m.chain_root IN (`+strings.Join(placeholders, ",")+`) GROUP BY m.chain_root`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var root string
		var count, at, unread int64
		if rows.Scan(&root, &count, &at, &unread) == nil {
			if message := roots[root]; message != nil {
				message["reply_count"] = count
				message["last_reply_at"] = at
				message["reply_unread"] = unread
			}
		}
	}
}
func (s *webServer) threads(w http.ResponseWriter, r *http.Request, p *Principal) {
	if p.Kind != "human" {
		apiError(w, 403, "A human profile is required")
		return
	}
	if r.Method == "PATCH" {
		var a struct {
			RootID    string
			Target    string
			Following *bool
			Position  *int64
		}
		if !webDecode(w, r, &a) {
			return
		}
		root, err := findMessageByID(s.db, a.RootID)
		if err != nil || !s.can(p, root.Target, false) {
			apiError(w, 403, "Thread access denied")
			return
		}
		self := loadInboxSelf(s.db, p)
		if strings.HasPrefix(root.Target, "@") && !self.owns(root.Sender, root.Target) && !self.owns(strings.TrimPrefix(root.Target, "@"), root.Target) {
			apiError(w, 403, "Thread access denied")
			return
		}
		target := root.Target
		if strings.HasPrefix(target, "@") && self.owns(strings.TrimPrefix(target, "@"), target) {
			target = "@" + root.Sender
		}
		var latest int64
		_ = s.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages WHERE chain_root=?", root.ChainRoot).Scan(&latest)
		if a.Position != nil && (*a.Position < 0 || *a.Position > latest) {
			apiError(w, 400, "Invalid thread position")
			return
		}
		tx, err := beginWrite(s.db)
		if err != nil {
			apiError(w, 500, "Could not update thread")
			return
		}
		defer tx.Rollback()
		following := false
		position := int64(0)
		_ = tx.QueryRow("SELECT following,position FROM human_threads WHERE principal_id=? AND root_id=?", p.ID, root.ChainRoot).Scan(&following, &position)
		oldFollowing, oldPosition := following, position
		if a.Following != nil {
			following = *a.Following
			if position == 0 {
				position = latest
			}
		}
		if a.Position != nil && *a.Position > position {
			position = *a.Position
		}
		if following == oldFollowing && position == oldPosition {
			writeJSON(w, 200, map[string]any{"following": following, "position": position, "latest": latest})
			return
		}
		_, err = tx.Exec("INSERT INTO human_threads(principal_id,root_id,target,following,position) VALUES(?,?,?,?,?) ON CONFLICT(principal_id,root_id) DO UPDATE SET following=excluded.following,position=MAX(human_threads.position,excluded.position)", p.ID, root.ChainRoot, target, following, position)
		if err == nil && a.Position != nil {
			_, err = tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND root_id=? AND message_id IN(SELECT id FROM messages WHERE seq<=?) AND read_at=0", nowMS(), p.ID, root.ChainRoot, position)
		}
		if err == nil {
			err = touchPersonalMetadata(tx, p.ID)
		}
		if err != nil || tx.Commit() != nil {
			apiError(w, 500, "Could not update thread")
			return
		}
		writeJSON(w, 200, map[string]any{"following": following, "position": position, "latest": latest})
		return
	}
	if r.Method != "GET" {
		apiError(w, 405, "Method not allowed")
		return
	}
	if err := s.materializeInbox(p); err != nil {
		apiError(w, 500, "Could not update threads")
		return
	}
	rows, err := s.db.Query(`SELECT root.id,root.target,root.sender,root.body,root.created_at,t.position,
 (SELECT COUNT(*) FROM messages m WHERE m.chain_root=root.id AND m.parent_id IS NOT NULL),
 (SELECT COALESCE(MAX(seq),0) FROM messages m WHERE m.chain_root=root.id),
 (SELECT COALESCE(MAX(created_at),0) FROM messages m WHERE m.chain_root=root.id),
 (SELECT COUNT(*) FROM messages m WHERE m.chain_root=root.id AND m.parent_id IS NOT NULL AND m.seq>t.position AND m.sender<>COALESCE((SELECT h.name FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id JOIN remote_channels c ON c.remote_name=r.name WHERE c.target=root.target AND h.local_principal_id=?),?))
 FROM human_threads t JOIN messages root ON root.id=t.root_id WHERE t.principal_id=? AND t.following=1 ORDER BY 8 DESC LIMIT 100`, p.ID, principalAddress(p), p.ID)
	if err != nil {
		apiError(w, 500, "Could not read threads")
		return
	}
	type item struct {
		id, target, sender                             string
		body                                           []byte
		created, position, replies, latest, at, unread int64
	}
	var list []item
	for rows.Next() {
		var item item
		if err = rows.Scan(&item.id, &item.target, &item.sender, &item.body, &item.created, &item.position, &item.replies, &item.latest, &item.at, &item.unread); err != nil {
			rows.Close()
			apiError(w, 500, "Could not read threads")
			return
		}
		list = append(list, item)
	}
	rows.Close()
	out := []map[string]any{}
	self := loadInboxSelf(s.db, p)
	for _, item := range list {
		if !s.can(p, item.target, false) {
			continue
		}
		target := item.target
		if strings.HasPrefix(target, "@") && self.owns(strings.TrimPrefix(target, "@"), target) {
			target = "@" + item.sender
		}
		out = append(out, map[string]any{"id": item.id, "target": target, "sender": item.sender, "body": string(item.body), "created_at": item.created, "reply_count": item.replies, "latest": item.latest, "last_reply_at": item.at, "unread": item.unread, "position": item.position})
	}
	writeJSON(w, 200, map[string]any{"threads": out})
}

// Keep call sites explicit about milliseconds, matching the message log.
func nowMS() int64 { return time.Now().UnixMilli() }

func followHumanMessage(db dbtx, p *Principal, message *Message) error {
	if p.Kind != "human" {
		return nil
	}
	_, err := db.Exec(`INSERT INTO human_threads(principal_id,root_id,target,following,position)
 SELECT ?,?,?,1,0 WHERE EXISTS(SELECT 1 FROM messages WHERE id=?)
 ON CONFLICT(principal_id,root_id) DO UPDATE SET following=1`, p.ID, message.ChainRoot, message.Target, message.ChainRoot)
	return err
}
