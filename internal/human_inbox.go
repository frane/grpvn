package internal

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type notificationSettings struct {
	Level   string `json:"level"`
	Desktop bool   `json:"desktop"`
	Preview bool   `json:"preview"`
	Sound   bool   `json:"sound"`
}

func inboxNotificationSettings(db dbtx, id string) notificationSettings {
	settings := notificationSettings{Level: "important"}
	_ = db.QueryRow("SELECT level,desktop,preview,sound FROM human_notifications WHERE principal_id=?", id).Scan(&settings.Level, &settings.Desktop, &settings.Preview, &settings.Sound)
	return settings
}
func touchPersonalMetadata(db dbtx, id string) error {
	_, err := db.Exec("INSERT INTO chat_metadata(key,value) VALUES(?,1) ON CONFLICT(key) DO UPDATE SET value=chat_metadata.value+1", "personal:"+id)
	return err
}

type inboxSelf struct {
	p      *Principal
	remote map[string]string
}

func loadInboxSelf(db dbtx, p *Principal) inboxSelf {
	self := inboxSelf{p: p, remote: map[string]string{}}
	rows, err := db.Query("SELECT r.name,h.name FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE h.local_principal_id=?", p.ID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var alias, name string
			if rows.Scan(&alias, &name) == nil {
				self.remote[alias] = name
			}
		}
	}
	return self
}
func (s inboxSelf) name(target string) string {
	if alias, _, ok := splitRemoteConversation(target); ok {
		if name := s.remote[alias]; name != "" {
			return name
		}
	}
	return principalAddress(s.p)
}
func (s inboxSelf) owns(sender, target string) bool {
	if alias, _, ok := splitRemoteConversation(target); ok && s.remote[alias] != "" {
		return sender == s.remote[alias]
	}
	return principalOwnsAddress(s.p, sender)
}

var humanMentionPattern = regexp.MustCompile(`(?:^|[\s([{])@([\p{L}\p{N}_.~\-]+(?:@[\p{L}\p{N}_.~\-]+)?)`)
var inlineMentionCode = regexp.MustCompile("`[^`]*`")

func (s inboxSelf) mentioned(body, target string) bool {
	for i, part := range strings.Split(body, "```") {
		if i%2 != 0 {
			continue
		}
		part = inlineMentionCode.ReplaceAllString(part, "")
		for _, match := range humanMentionPattern.FindAllStringSubmatch(part, -1) {
			name := strings.TrimRight(match[1], ".")
			if name == s.name(target) {
				return true
			}
			if _, _, remote := splitRemoteTarget(target); !remote && s.p.aliases[name] {
				return true
			}
		}
	}
	return false
}

type humanThreadPreference struct {
	following bool
	position  int64
	target    string
}

func loadHumanThreads(db dbtx, id string) (map[string]humanThreadPreference, error) {
	rows, err := db.Query("SELECT root_id,target,following,position FROM human_threads WHERE principal_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]humanThreadPreference{}
	for rows.Next() {
		var root string
		var thread humanThreadPreference
		if err = rows.Scan(&root, &thread.target, &thread.following, &thread.position); err != nil {
			return nil, err
		}
		out[root] = thread
	}
	return out, rows.Err()
}

// Build attention records lazily from the durable log, once per profile and
// commit position. No model calls or background fan-out are needed for local
// agent chat. Read checks are repeated when items are returned to a client.
func (s *webServer) materializeInbox(p *Principal) error {
	var position int64
	_ = s.db.QueryRow("SELECT position FROM human_inbox_state WHERE principal_id=?", p.ID).Scan(&position)
	self := loadInboxSelf(s.db, p)
	threads, err := loadHumanThreads(s.db, p.ID)
	if err != nil {
		return err
	}
	cursors, err := loadCursors(s.db, p.ID)
	if err != nil {
		return err
	}
	settings := inboxNotificationSettings(s.db, p.ID)
	allowed := map[string]bool{}
	for {
		rows, err := s.db.Query("SELECT seq,id,sender,target,body,chain_root,chain_depth,parent_id,correlation,created_at FROM messages WHERE seq>? ORDER BY seq LIMIT 500", position)
		if err != nil {
			return err
		}
		var messages []*Message
		for rows.Next() {
			m, err := scanMessage(rows, true)
			if err != nil {
				rows.Close()
				return err
			}
			messages = append(messages, m)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		type entry struct {
			message        *Message
			target, reason string
		}
		var entries []entry
		follows := map[string]humanThreadPreference{}
		for _, m := range messages {
			position = m.Seq
			target := m.Target
			_, _, scopedDM := splitRemoteDMTarget(target)
			if strings.HasPrefix(target, "@") && !scopedDM {
				if self.owns(strings.TrimPrefix(target, "@"), target) {
					target = "@" + m.Sender
				} else if !self.owns(m.Sender, target) {
					continue
				}
			}
			if _, ok := allowed[target]; !ok {
				allowed[target] = s.can(p, target, false)
			}
			if !allowed[target] {
				continue
			}
			mine := self.owns(m.Sender, m.Target)
			if mine {
				if _, exists := threads[m.ChainRoot]; !exists {
					thread := humanThreadPreference{following: true, position: cursors[target], target: target}
					threads[m.ChainRoot] = thread
					follows[m.ChainRoot] = thread
				}
				continue
			}
			reason := ""
			thread, followed := threads[m.ChainRoot]
			if strings.HasPrefix(target, "@") {
				reason = "dm"
			} else if self.mentioned(string(m.Body), m.Target) || groupMentioned(s.db, m, self.name(m.Target), p.ID) {
				reason = "mention"
			} else if m.ParentID != nil && followed && thread.following {
				reason = "reply"
			} else if settings.Level == "all" {
				reason = "message"
			}
			if reason == "" {
				continue
			}
			entries = append(entries, entry{m, target, reason})
		}
		tx, err := beginWrite(s.db)
		if err != nil {
			return err
		}
		save := func() error {
			if err := lockHumanRead(s.db, tx, p.ID); err != nil {
				return err
			}
			for root, thread := range follows {
				if _, err := tx.Exec("INSERT OR IGNORE INTO human_threads(principal_id,root_id,target,following,position) SELECT ?,?,?,1,? WHERE EXISTS(SELECT 1 FROM messages WHERE id=?)", p.ID, root, thread.target, thread.position, root); err != nil {
					return err
				}
			}
			for _, item := range entries {
				// Recheck under the account lock: another device may have read
				// this message while the attention page was being prepared.
				var read, through int64
				_ = tx.QueryRow("SELECT read_at FROM human_message_reads WHERE principal_id=? AND message_id=?", p.ID, item.message.ID).Scan(&read)
				if item.message.ParentID == nil {
					_ = tx.QueryRow("SELECT position FROM cursors WHERE agent_name=? AND target=?", p.ID, item.target).Scan(&through)
				} else {
					_ = tx.QueryRow("SELECT position FROM human_threads WHERE principal_id=? AND root_id=?", p.ID, item.message.ChainRoot).Scan(&through)
				}
				if item.message.Seq <= through {
					read = nowMS()
				}
				if _, err := tx.Exec("INSERT OR IGNORE INTO human_inbox(principal_id,message_id,target,root_id,reason,question,read_at) VALUES(?,?,?,?,?,?,?)", p.ID, item.message.ID, item.target, item.message.ChainRoot, item.reason, item.message.Correlation != nil, read); err != nil {
					return err
				}
			}
			_, err := tx.Exec("INSERT INTO human_inbox_state(principal_id,position) VALUES(?,?) ON CONFLICT(principal_id) DO UPDATE SET position=MAX(human_inbox_state.position,excluded.position)", p.ID, position)
			return err
		}()
		if save != nil {
			tx.Rollback()
			return save
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if len(messages) < 500 {
			return nil
		}
	}
}

func (s *webServer) activity(w http.ResponseWriter, r *http.Request, p *Principal) {
	if p.Kind != "human" {
		apiError(w, 403, "A human profile is required")
		return
	}
	if r.Method == "PATCH" {
		var a struct {
			IDs    []string
			All    bool
			Notify bool
		}
		if !webDecode(w, r, &a) {
			return
		}
		if len(a.IDs) > 100 {
			apiError(w, 400, "Select up to 100 items")
			return
		}
		if !a.Notify {
			if err := s.materializeInbox(p); err != nil {
				apiError(w, 500, "Could not update inbox")
				return
			}
		}
		tx, err := beginWrite(s.db)
		if err != nil {
			apiError(w, 500, "Could not update inbox")
			return
		}
		defer tx.Rollback()
		if err = lockHumanRead(s.db, tx, p.ID); err != nil {
			apiError(w, 500, "Could not update inbox")
			return
		}
		readIDs := []string{}
		if a.All {
			rows, e := tx.Query("SELECT message_id FROM human_inbox WHERE principal_id=? AND read_at=0", p.ID)
			if e != nil {
				apiError(w, 500, "Could not update inbox")
				return
			}
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					readIDs = append(readIDs, id)
				}
			}
			rows.Close()
		} else if !a.Notify {
			readIDs = a.IDs
		}

		now := time.Now().UnixMilli()
		claimed := []string{}
		if a.All {
			_, err = tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND read_at=0", now, p.ID)
		} else {
			for _, id := range a.IDs {
				if a.Notify {
					result, e := tx.Exec("UPDATE human_inbox SET notified_at=? WHERE principal_id=? AND message_id=? AND notified_at=0 AND read_at=0", now, p.ID, id)
					if e != nil {
						err = e
						break
					}
					if count, _ := result.RowsAffected(); count > 0 {
						claimed = append(claimed, id)
					}
				} else {
					_, err = tx.Exec("UPDATE human_inbox SET read_at=? WHERE principal_id=? AND message_id=? AND read_at=0", now, p.ID, id)
					if err != nil {
						break
					}
				}
			}
		}
		if err == nil && !a.Notify && len(readIDs) > 0 {
			for start := 0; start < len(readIDs); start += 100 {
				if err = s.recordHumanRead(tx, p, humanReadChange{Kind: "inbox", IDs: readIDs[start:min(start+100, len(readIDs))]}); err != nil {
					break
				}
			}
		}
		if err == nil && !a.Notify {
			err = touchPersonalMetadata(tx, p.ID)
		}
		if err != nil || tx.Commit() != nil {
			apiError(w, 500, "Could not update inbox")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "claimed": claimed})
		return
	}
	if r.Method != "GET" {
		apiError(w, 405, "Method not allowed")
		return
	}
	if err := s.materializeInbox(p); err != nil {
		apiError(w, 500, "Could not update inbox")
		return
	}
	after := int64(-1)
	if value := r.URL.Query().Get("after"); value != "" {
		var err error
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < 0 {
			apiError(w, 400, "Invalid inbox position")
			return
		}
	}
	before := int64(0)
	if value := r.URL.Query().Get("before"); value != "" {
		var err error
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before <= 0 || after >= 0 {
			apiError(w, 400, "Invalid inbox position")
			return
		}
	}
	query := `SELECT m.seq,m.id,m.sender,i.target,m.body,i.root_id,i.reason,i.question,i.read_at,m.created_at,COALESCE(f.muted,0),COALESCE(root.body,'')
 FROM human_inbox i JOIN messages m ON m.id=i.message_id LEFT JOIN messages root ON root.id=i.root_id
 LEFT JOIN conversation_preferences f ON f.principal_id=i.principal_id AND f.target=i.target
 WHERE i.principal_id=?`
	args := []any{p.ID}
	if after >= 0 {
		query += " AND m.seq>?"
		args = append(args, after)
	}
	if before > 0 {
		query += " AND m.seq<?"
		args = append(args, before)
	}
	if after >= 0 {
		query += " ORDER BY m.seq ASC LIMIT 201"
	} else {
		query += " ORDER BY m.seq DESC LIMIT 201"
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		apiError(w, 500, "Could not read inbox")
		return
	}
	type item struct {
		seq, read, created               int64
		id, sender, target, root, reason string
		body, rootBody                   []byte
		question, muted                  bool
	}
	var list []item
	for rows.Next() {
		var item item
		if err = rows.Scan(&item.seq, &item.id, &item.sender, &item.target, &item.body, &item.root, &item.reason, &item.question, &item.read, &item.created, &item.muted, &item.rootBody); err != nil {
			rows.Close()
			apiError(w, 500, "Could not read inbox")
			return
		}
		list = append(list, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		apiError(w, 500, "Could not read inbox")
		return
	}
	more := len(list) > 200
	if more {
		list = list[:200]
	}
	nextBefore := int64(0)
	if len(list) > 0 {
		nextBefore = list[len(list)-1].seq
	}
	out := []map[string]any{}
	for _, item := range list {
		if !s.can(p, item.target, false) {
			continue
		}
		out = append(out, map[string]any{"seq": item.seq, "id": item.id, "sender": item.sender, "target": item.target, "body": string(item.body), "root_id": item.root, "root_body": string(item.rootBody), "reason": item.reason, "question": item.question, "unread": item.read == 0, "muted": item.muted, "created_at": item.created})
	}
	// Count only currently visible, unmuted conversations, including items older
	// than the first page. Muting keeps history available in the inbox.
	unread := 0
	counts, err := s.db.Query("SELECT i.target,COUNT(*) FROM human_inbox i LEFT JOIN conversation_preferences f ON f.principal_id=i.principal_id AND f.target=i.target WHERE i.principal_id=? AND i.read_at=0 AND COALESCE(f.muted,0)=0 GROUP BY i.target", p.ID)
	if err == nil {
		type count struct {
			target string
			n      int
		}
		var groups []count
		for counts.Next() {
			var c count
			if counts.Scan(&c.target, &c.n) == nil {
				groups = append(groups, c)
			}
		}
		counts.Close()
		for _, c := range groups {
			if s.can(p, c.target, false) {
				unread += c.n
			}
		}
	}
	var cursor int64
	_ = s.db.QueryRow("SELECT position FROM human_inbox_state WHERE principal_id=?", p.ID).Scan(&cursor)
	if after >= 0 && more && len(list) > 0 {
		cursor = list[len(list)-1].seq
	}
	writeJSON(w, 200, map[string]any{"items": out, "unread": unread, "cursor": cursor, "more": more, "before": nextBefore, "settings": inboxNotificationSettings(s.db, p.ID)})
}
func (s *webServer) notificationPreferences(w http.ResponseWriter, r *http.Request, p *Principal) {
	if p.Kind != "human" {
		apiError(w, 403, "A human profile is required")
		return
	}
	if r.Method == "GET" {
		writeJSON(w, 200, inboxNotificationSettings(s.db, p.ID))
		return
	}
	if r.Method != "PATCH" {
		apiError(w, 405, "Method not allowed")
		return
	}
	var settings notificationSettings
	if !webDecode(w, r, &settings) {
		return
	}
	if settings.Level != "important" && settings.Level != "all" && settings.Level != "none" {
		apiError(w, 400, "Choose a notification level")
		return
	}
	_, err := s.db.Exec("INSERT INTO human_notifications(principal_id,level,desktop,preview,sound) VALUES(?,?,?,?,?) ON CONFLICT(principal_id) DO UPDATE SET level=excluded.level,desktop=excluded.desktop,preview=excluded.preview,sound=excluded.sound", p.ID, settings.Level, settings.Desktop, settings.Preview, settings.Sound)
	if err != nil {
		apiError(w, 500, "Could not save notification settings")
		return
	}
	_ = touchPersonalMetadata(s.db, p.ID)
	writeJSON(w, 200, settings)
}
