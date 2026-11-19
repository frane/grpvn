package internal

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

// Two indexed queries decorate a whole message page, rather than querying for
// each message. Corrections never mutate the immutable transport payload.
func (s *webServer) decorateMessages(p *Principal, list []map[string]any) error {
	if len(list) == 0 {
		return nil
	}
	byID := map[string]map[string]any{}
	args := []any{}
	for _, message := range list {
		id := message["id"].(string)
		byID[id] = message
		args = append(args, id)
		message["edit_version"] = ""
		message["link"] = strings.TrimRight(s.base, "/") + "/m/" + id
		message["reactions"] = []messageReaction{}
	}
	slots := strings.TrimRight(strings.Repeat("?,", len(args)), ",")
	if s.local {
		server := messageDatabaseID(s.db)
		for id, message := range byID {
			message["link"] = "grpvn://local/" + server + "/m/" + id
		}
		rows, err := s.db.Query("SELECT rm.local_id,r.url,rm.remote_id FROM remote_messages rm JOIN remotes r ON r.name=rm.remote_name WHERE rm.local_id IN ("+slots+")", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, address, native string
			if err = rows.Scan(&id, &address, &native); err != nil {
				rows.Close()
				return err
			}
			byID[id]["link"] = strings.TrimRight(address, "/") + "/m/" + native
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	rows, err := s.db.Query(`SELECT a.message_id,a.body,a.created_at,a.id FROM message_actions a WHERE a.message_id IN (`+slots+`) AND a.kind='edit' AND NOT EXISTS(SELECT 1 FROM message_actions n WHERE n.message_id=a.message_id AND n.kind='edit' AND COALESCE(n.origin_seq,n.seq)>COALESCE(a.origin_seq,a.seq))`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, body, version string
		var at int64
		if err = rows.Scan(&id, &body, &at, &version); err != nil {
			rows.Close()
			return err
		}
		m := byID[id]
		m["body"] = body
		m["edited_at"] = at
		m["edit_version"] = version
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	remoteSelf := map[string]string{}
	if s.local {
		for _, message := range list {
			target := message["target"].(string)
			if alias, _, ok := splitRemoteConversation(target); ok {
				remoteSelf[target] = alias
			}
		}
		for target, alias := range remoteSelf {
			var id string
			_ = s.db.QueryRow("SELECT h.principal_id FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE r.name=? AND h.local_principal_id=?", alias, p.ID).Scan(&id)
			remoteSelf[target] = id
		}
	}
	rows, err = s.db.Query(`SELECT a.message_id,a.emoji,a.actor_id,COALESCE(p.name,a.actor_name) FROM message_actions a LEFT JOIN principals p ON p.id=a.actor_id WHERE a.message_id IN (`+slots+`) AND a.kind='reaction' AND a.added=1 AND NOT EXISTS(SELECT 1 FROM message_actions n WHERE n.message_id=a.message_id AND n.kind='reaction' AND n.actor_id=a.actor_id AND n.emoji=a.emoji AND COALESCE(n.origin_seq,n.seq)>COALESCE(a.origin_seq,a.seq)) ORDER BY a.message_id,a.emoji,a.actor_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, emoji string
		var person reactionPerson
		if err = rows.Scan(&id, &emoji, &person.ID, &person.Name); err != nil {
			return err
		}
		m := byID[id]
		reactions := m["reactions"].([]messageReaction)
		at := len(reactions) - 1
		if at < 0 || reactions[at].Emoji != emoji {
			reactions = append(reactions, messageReaction{Emoji: emoji, People: []reactionPerson{}})
			at++
		}
		reaction := &reactions[at]
		reaction.Count++
		reaction.People = append(reaction.People, person)
		reaction.Mine = reaction.Mine || person.ID == p.ID || person.ID == remoteSelf[m["target"].(string)]
		m["reactions"] = reactions
	}
	return rows.Err()
}

func (s *webServer) changedMessageActions(r *http.Request, p *Principal, after, through int64) ([]string, []string, error) {
	ids := []string{}
	targets := []string{}
	if after >= through {
		return ids, targets, nil
	}
	rows, err := s.db.QueryContext(r.Context(), "SELECT DISTINCT m.id,m.sender,m.target FROM message_actions a JOIN messages m ON m.id=a.message_id WHERE a.seq>? AND a.seq<=? LIMIT 500", after, through)
	if err != nil {
		return nil, nil, err
	}
	var messages []*Message
	for rows.Next() {
		m := &Message{}
		if err = rows.Scan(&m.ID, &m.Sender, &m.Target); err != nil {
			rows.Close()
			return nil, nil, err
		}
		messages = append(messages, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	for _, m := range messages {
		if r.URL.Path == "/v1/events" {
			selected := r.URL.Query()["channel"]
			if !strings.HasPrefix(m.Target, "#") || !s.access.canAccess(r, m.Target) {
				continue
			}
			if len(selected) > 0 {
				found := false
				for _, target := range selected {
					found = found || m.Target == target
				}
				if !found {
					continue
				}
			}
		} else if !messageVisible(s, p, m, false) {
			continue
		}
		ids = append(ids, m.ID)
		targets = append(targets, m.Target)
	}
	return ids, targets, nil
}

func presentedWire(db *sql.DB, m *Message, self string) map[string]any {
	wire := attributedWire(db, m)
	view, err := messageView(db, m, self)
	value := map[string]any{}
	data, _ := json.Marshal(wire)
	_ = json.Unmarshal(data, &value)
	value["link"] = messageLink(db, m.ID, "")
	if err == nil {
		value["body"] = view.Body
		value["edited_at"] = view.EditedAt
		value["edit_version"] = view.EditVersion
		value["reactions"] = view.Reactions
	}
	if wire.Correlation != nil {
		value["correlation"] = wire.Correlation
	}
	if wire.GroupRecipients != nil {
		value["group_recipients"] = wire.GroupRecipients
	}
	return value
}
