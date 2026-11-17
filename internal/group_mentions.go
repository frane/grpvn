package internal

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

var groupNames = map[string]bool{"team": true, "here": true, "agents": true, "humans": true}

// Parse prose only, using the same boundaries as individual mentions. Quoted
// code and email addresses do not notify a group.
func mentionedGroups(body string) map[string]bool {
	groups := map[string]bool{}
	for i, part := range strings.Split(body, "```") {
		if i%2 != 0 {
			continue
		}
		part = inlineMentionCode.ReplaceAllString(part, "")
		for _, match := range humanMentionPattern.FindAllStringSubmatch(part, -1) {
			name := strings.TrimRight(match[1], ".")
			if groupNames[name] {
				groups[name] = true
			}
		}
	}
	return groups
}

func touchParticipant(db dbtx, id string) {
	if id == "" {
		return
	}
	now := time.Now().UnixMilli()
	_, _ = db.Exec("INSERT INTO participant_activity(identity,last_seen) VALUES(?,?) ON CONFLICT(identity) DO UPDATE SET last_seen=excluded.last_seen WHERE participant_activity.last_seen<?", id, now, now-30000)
}

func groupRecipients(db dbtx, m *Message) ([]string, error) {
	groups := mentionedGroups(string(m.Body))
	out := []string{}
	if len(groups) == 0 || !strings.HasPrefix(m.Target, "#") {
		return out, nil
	}
	// Remote namespace caches must use the coordinator's snapshot, never the
	// membership of another local conversation with a similar name.
	if _, _, remote := splitRemoteTarget(m.Target); remote {
		return out, nil
	}
	rows, err := db.Query("SELECT id FROM principals WHERE kind IN ('agent','human') AND status='active'")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	recipients := map[string]bool{}
	eligible := func(id, name, kind string) {
		if name == m.Sender {
			return
		}
		include := groups["team"] || groups["agents"] && kind == "agent" || groups["humans"] && kind == "human"
		if groups["here"] && !include {
			var last int64
			_ = db.QueryRow("SELECT COALESCE(MAX(last_seen),0) FROM participant_activity WHERE identity=?", id).Scan(&last)
			var seen int64
			_ = db.QueryRow("SELECT COALESCE(MAX(heartbeat_at),0) FROM agent_sessions WHERE agent_name=? AND status<>'closed'", name).Scan(&seen)
			if seen > last {
				last = seen
			}
			_ = db.QueryRow("SELECT COALESCE(MAX(updated_at),0) FROM cursors WHERE agent_name IN(?,?)", id, name).Scan(&seen)
			if seen > last {
				last = seen
			}
			include = last >= m.CreatedAt-5*60*1000 && last <= m.CreatedAt+30000
		}
		if include {
			recipients[id] = true
		}
	}
	for _, id := range ids {
		p, err := findPrincipal(db, "id=?", id)
		if err != nil {
			return nil, err
		}
		if err = cachePrincipal(db, p); err != nil {
			return nil, err
		}
		name := principalAddress(p)
		known[name] = true
		known[p.ID] = true
		for alias := range p.aliases {
			known[alias] = true
		}
		member := principalPermission(db, p, m.Target, false)
		if strings.HasPrefix(id, "local:") {
			var present bool
			_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM cursors WHERE agent_name=? AND target=?) OR EXISTS(SELECT 1 FROM messages WHERE sender=? AND target=?) OR EXISTS(SELECT 1 FROM local_channel_grants WHERE name=? AND channel=?)", name, m.Target, name, m.Target, name, m.Target).Scan(&present)
			enabled, restricted := true, false
			_ = db.QueryRow("SELECT enabled,restricted FROM local_participants WHERE name=?", name).Scan(&enabled, &restricted)
			if restricted {
				var granted bool
				_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM local_channel_grants WHERE name=? AND channel=?)", name, m.Target).Scan(&granted)
				present = present && granted
			}
			member = enabled && present
		}
		if member && channelJoined(db, id, m.Target) && !p.aliases[m.Sender] {
			eligible(id, name, p.Kind)
		}
	}
	rows, err = db.Query(`SELECT DISTINCT sender FROM messages m WHERE target=? AND NOT EXISTS(SELECT 1 FROM remote_messages r WHERE r.local_id=m.id)
 UNION SELECT name FROM local_channel_grants WHERE channel=?
 UNION SELECT agent_name FROM cursors WHERE target=?`, m.Target, m.Target, m.Target)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if known[name] || name == m.Sender {
			continue
		}
		var enabled, restricted bool
		enabled = true
		_ = db.QueryRow("SELECT enabled,restricted FROM local_participants WHERE name=?", name).Scan(&enabled, &restricted)
		if !enabled || !channelJoined(db, "local:"+name, m.Target) {
			continue
		}
		if restricted {
			var granted bool
			_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM local_channel_grants WHERE name=? AND channel=?)", name, m.Target).Scan(&granted)
			if !granted {
				continue
			}
		}
		eligible("local:"+name, name, "agent")
	}
	for id := range recipients {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func loadGroupRecipients(db dbtx, id string) *[]string {
	var encoded string
	if db.QueryRow("SELECT recipients FROM message_groups WHERE message_id=?", id).Scan(&encoded) != nil {
		return nil
	}
	out := []string{}
	if json.Unmarshal([]byte(encoded), &out) != nil {
		return nil
	}
	return &out
}

func groupRecipientIdentity(db dbtx, target, name, id string) string {
	if alias, _, remote := splitRemoteConversation(target); remote {
		var remoteID string
		if db.QueryRow("SELECT c.principal_id FROM local_credentials c JOIN remotes r ON r.server_id=c.server_id WHERE r.name=? AND c.agent_name=?", alias, name).Scan(&remoteID) == nil && remoteID != "" {
			return remoteID
		}
		if db.QueryRow("SELECT h.principal_id FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE r.name=? AND h.local_principal_id=?", alias, id).Scan(&remoteID) == nil && remoteID != "" {
			return remoteID
		}
		return ""
	}
	if id != "" {
		return id
	}
	var known string
	if db.QueryRow("SELECT principal_id FROM principal_aliases WHERE address=?", name).Scan(&known) == nil {
		return known
	}
	if p, err := participantByAddress(db, name); err == nil {
		return p.ID
	}
	return "local:" + name
}
func groupMentioned(db dbtx, m *Message, name, id string) bool {
	if len(mentionedGroups(string(m.Body))) == 0 {
		return false
	}
	key := groupRecipientIdentity(db, m.Target, name, id)
	if key == "" {
		return false
	}
	recipients := loadGroupRecipients(db, m.ID)
	if recipients == nil {
		return false
	}
	for _, recipient := range *recipients {
		if recipient == key {
			return true
		}
	}
	return false
}
