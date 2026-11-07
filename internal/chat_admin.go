package internal

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func channelArchived(db dbtx, channel string) bool {
	if !strings.HasPrefix(channel, "#") {
		return false
	}
	var archived bool
	return db.QueryRow("SELECT EXISTS(SELECT 1 FROM channel_archives WHERE channel=?)", channel).Scan(&archived) == nil && archived
}
func TouchChatMetadata(db dbtx) error {
	_, err := db.Exec("INSERT INTO chat_metadata(key,value) VALUES('revision',1) ON CONFLICT(key) DO UPDATE SET value=chat_metadata.value+1")
	return err
}
func localSenderAllowed(db dbtx, name, target string) bool {
	var enabled, restricted bool
	err := db.QueryRow("SELECT enabled,restricted FROM local_participants WHERE name=?", name).Scan(&enabled, &restricted)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil || !enabled {
		return false
	}
	var explicit bool
	if db.QueryRow("SELECT can_write FROM local_channel_grants WHERE name=? AND channel=?", name, target).Scan(&explicit) == nil {
		return explicit
	}
	if !restricted {
		return true
	}
	dm := ""
	if strings.HasPrefix(target, "@") {
		dm = "@*"
	}
	var allowed bool
	return db.QueryRow("SELECT EXISTS(SELECT 1 FROM local_channel_grants WHERE name=? AND channel IN(?,?,'*') AND can_write=1)", name, target, dm).Scan(&allowed) == nil && allowed
}
func channelJoined(db dbtx, id, channel string) bool {
	var joined bool
	err := db.QueryRow("SELECT joined FROM channel_subscriptions WHERE principal_id=? AND channel=?", id, channel).Scan(&joined)
	return err == sql.ErrNoRows || (err == nil && joined)
}
func setChannelJoined(db dbtx, id, channel string, joined bool) error {
	_, err := db.Exec("INSERT INTO channel_subscriptions(principal_id,channel,joined) VALUES(?,?,?) ON CONFLICT(principal_id,channel) DO UPDATE SET joined=excluded.joined", id, channel, joined)
	return err
}
func canManageChannel(db dbtx, p *Principal, channel string) bool {
	if principalPermission(db, p, "*", true) {
		return true
	}
	var owner string
	return db.QueryRow("SELECT owner_id FROM workspace_channels WHERE channel=?", channel).Scan(&owner) == nil && (owner == p.ID || (p.ParentID != "" && owner == p.ParentID))
}
func (s *webServer) workspaceAccount(ctx context.Context, p *Principal, alias string) (Remote, error) {
	if p == nil {
		return Remote{}, fmt.Errorf("Sign in to continue")
	}
	remote, err := getRemote(s.db, alias)
	if err != nil {
		return Remote{}, err
	}
	var owner string
	if s.db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner) != nil || owner != p.ID {
		return Remote{}, fmt.Errorf("Use Add workspace to sign in with your own account")
	}
	key, err := localCredential(s.db, remote.ServerID, "")
	if err != nil {
		return Remote{}, err
	}
	remote.DB = s.db
	remote.Token = key.Secret
	return *remote, nil
}

// The desktop forwards management to the selected workspace with its existing
// OAuth identity. The coordinator applies exactly the same rights as the web app.
func (s *webServer) manage(w http.ResponseWriter, r *http.Request, p *Principal) bool {
	path := r.URL.Path
	supported := strings.HasPrefix(path, "/v1/members") || path == "/v1/chat/channels" || path == "/v1/chat/memberships" || path == "/v1/chat/participants" || path == "/v1/chat/invitations" || path == "/v1/access-requests" || path == "/v1/access-approvals"
	if s.local && supported && r.URL.Query().Get("workspace") != "" {
		alias := r.URL.Query().Get("workspace")
		remote, err := s.workspaceAccount(r.Context(), p, alias)
		if err != nil {
			apiError(w, 403, err.Error())
			return true
		}
		q := r.URL.Query()
		q.Del("workspace")
		var body map[string]any
		if r.Method != "GET" {
			if !webDecode(w, r, &body) {
				return true
			}
			if ch, ok := body["Channel"].(string); ok {
				if source, native, ok := splitRemoteTarget(ch); ok && source == alias {
					body["Channel"] = native
				}
			}
		}
		var result map[string]any
		endpoint := path
		if len(q) > 0 {
			endpoint += "?" + q.Encode()
		}
		if err = remoteRequest(r.Context(), remote, r.Method, endpoint, body, &result); err != nil {
			apiError(w, 403, err.Error())
			return true
		}
		if path == "/v1/chat/channels" && r.Method == "GET" {
			if list, ok := result["channels"].([]any); ok {
				for _, item := range list {
					c, ok := item.(map[string]any)
					if !ok {
						continue
					}
					native, _ := c["name"].(string)
					c["label"] = native
					c["name"] = "#" + alias + "/" + strings.TrimPrefix(native, "#")
					c["source_id"] = alias
					u, _ := url.Parse(remote.URL)
					c["source"] = u.Host
				}
			}
		}
		if path == "/v1/chat/memberships" && r.Method == "POST" {
			if channel, ok := body["Channel"].(string); ok && channel != "" {
				target := "#" + alias + "/" + strings.TrimPrefix(channel, "#")
				joined := body["Action"] != "leave"
				_ = setChannelJoined(s.db, p.ID, target, joined)
				if joined {
					_ = JoinRemoteTargets(s.db, target)
				}
			}
		}
		if path == "/v1/chat/channels" && r.Method == "PATCH" {
			if channel, ok := body["Channel"].(string); ok {
				target := "#" + alias + "/" + strings.TrimPrefix(channel, "#")
				if body["Archived"] == true {
					_, _ = s.db.Exec("INSERT INTO channel_archives(channel,principal_id,created_at) VALUES(?,?,?) ON CONFLICT(channel) DO NOTHING", target, p.ID, time.Now().UnixMilli())
				} else {
					_, _ = s.db.Exec("DELETE FROM channel_archives WHERE channel=?", target)
				}
			}
		}
		writeJSON(w, 200, result)
		return true
	}
	if path == "/v1/members" || strings.HasPrefix(path, "/v1/members/") {
		if !principalPermission(s.db, p, "*", true) {
			apiError(w, 403, "Workspace administrator access required")
			return true
		}
		if path == "/v1/members" && r.Method == "GET" {
			s.memberList(w, "", p)
			return true
		}
		id := strings.TrimPrefix(path, "/v1/members/")
		if s.local && strings.HasPrefix(id, "local:") {
			s.localMember(w, r, strings.TrimPrefix(id, "local:"))
			return true
		}
		if id == p.ID || id == p.ParentID {
			apiError(w, 409, "You cannot remove or restrict your own administrator profile")
			return true
		}
		member, err := findPrincipal(s.db, "id=?", id)
		if err != nil {
			apiError(w, 404, "Member not found")
			return true
		}
		if r.Method == "DELETE" {
			_, err = s.db.Exec("UPDATE principals SET status='revoked' WHERE id=?", id)
			if err != nil {
				apiError(w, 500, "Could not remove member")
			} else {
				writeJSON(w, 200, map[string]bool{"removed": true})
			}
			return true
		}
		if r.Method == "PATCH" {
			var a struct {
				Channels []string
				Grants   []struct {
					Channel string
					Write   bool
				}
				Write, DM, Admin bool
				Active           *bool
			}
			if !webDecode(w, r, &a) {
				return true
			}
			if a.Active != nil {
				status := "revoked"
				if *a.Active {
					status = "active"
				}
				if _, err := s.db.Exec("UPDATE principals SET status=? WHERE id=?", status, id); err != nil {
					apiError(w, 500, "Could not update member")
				} else {
					writeJSON(w, 200, map[string]bool{"updated": true})
				}
				return true
			}
			if a.Admin {
				a.Channels = []string{"*"}
				a.Write = true
			}
			if a.DM {
				a.Channels = append(a.Channels, "@*")
			}
			rights := map[string]bool{}
			if a.Grants != nil && !a.Admin {
				a.Channels = nil
				for _, g := range a.Grants {
					a.Channels = append(a.Channels, g.Channel)
					rights[g.Channel] = g.Write
				}
			}
			for _, ch := range a.Channels {
				if ch != "*" && ch != "@*" && validateChannel(ch) != nil {
					apiError(w, 400, "Invalid channel")
					return true
				}
			}
			tx, err := beginWrite(s.db)
			if err != nil {
				apiError(w, 500, "Could not update access")
				return true
			}
			defer tx.Rollback()
			_, err = tx.Exec("DELETE FROM permissions WHERE principal_id=?", id)
			for _, ch := range a.Channels {
				if err != nil {
					break
				}
				_, err = tx.Exec("INSERT INTO permissions(principal_id,channel,can_read,can_write) VALUES(?,?,1,?) ON CONFLICT(principal_id,channel) DO UPDATE SET can_read=1,can_write=excluded.can_write", id, ch, func() bool {
					if a.Grants != nil && !a.Admin {
						return rights[ch]
					}
					return a.Write
				}())
				if err == nil && member.ParentID != "" {
					parent, findErr := findPrincipal(tx, "id=?", member.ParentID)
					if findErr != nil {
						err = findErr
					} else {
						posting := a.Write
						if a.Grants != nil && !a.Admin {
							posting = rights[ch]
						}
						err = channelGrant(tx, parent, ch, posting)
					}
				}
			}
			if err != nil || tx.Commit() != nil {
				apiError(w, 500, "Could not update access")
				return true
			}
			_ = cachePrincipal(s.db, member)
			writeJSON(w, 200, map[string]bool{"updated": true})
			return true
		}
		apiError(w, 405, "Method not allowed")
		return true
	}
	if path == "/v1/chat/channels" && r.Method == "PATCH" {
		var a struct {
			Channel  string
			Archived bool
		}
		if !webDecode(w, r, &a) {
			return true
		}
		if validateChannel(a.Channel) != nil || !canManageChannel(s.db, p, a.Channel) {
			apiError(w, 403, "A channel owner or workspace administrator is required")
			return true
		}
		var err error
		if a.Archived {
			_, err = s.db.Exec("INSERT INTO channel_archives(channel,principal_id,created_at) VALUES(?,?,?) ON CONFLICT(channel) DO NOTHING", a.Channel, p.ID, time.Now().UnixMilli())
		} else {
			_, err = s.db.Exec("DELETE FROM channel_archives WHERE channel=?", a.Channel)
		}
		if err != nil {
			apiError(w, 500, "Could not update channel")
		} else {
			writeJSON(w, 200, map[string]bool{"archived": a.Archived})
		}
		return true
	}
	if path == "/v1/chat/memberships" && (r.Method == "GET" || r.Method == "DELETE") {
		channel := r.URL.Query().Get("channel")
		if r.Method == "DELETE" {
			var a struct{ Channel, MemberID string }
			if !webDecode(w, r, &a) {
				return true
			}
			channel = a.Channel
			if !canManageChannel(s.db, p, channel) {
				apiError(w, 403, "Channel owner or administrator required")
				return true
			}
			if a.MemberID == p.ID {
				apiError(w, 409, "Use Leave channel to leave this conversation")
				return true
			}
			if s.local && strings.HasPrefix(a.MemberID, "local:") {
				name := strings.TrimPrefix(a.MemberID, "local:")
				tx, err := beginWrite(s.db)
				if err != nil {
					apiError(w, 500, "Could not remove agent")
					return true
				}
				defer tx.Rollback()
				_, err = tx.Exec("INSERT INTO local_participants(name,enabled) VALUES(?,1) ON CONFLICT(name) DO NOTHING", name)
				if err == nil {
					_, err = tx.Exec("INSERT INTO local_channel_grants(name,channel,can_write) VALUES(?,?,0) ON CONFLICT(name,channel) DO UPDATE SET can_write=0", name, channel)
				}
				if err == nil {
					err = setChannelJoined(tx, a.MemberID, channel, false)
				}
				if err != nil || tx.Commit() != nil {
					apiError(w, 500, "Could not remove agent")
				} else {
					writeJSON(w, 200, map[string]bool{"removed": true})
				}
				return true
			}
			peer, err := findPrincipal(s.db, "id=?", a.MemberID)
			if err != nil {
				apiError(w, 404, "Member not found")
				return true
			}
			if principalPermission(s.db, peer, "*", false) {
				apiError(w, 409, "This member has workspace-wide access; edit their workspace access instead")
				return true
			}
			_, err = s.db.Exec("DELETE FROM permissions WHERE principal_id=? AND channel=?", a.MemberID, channel)
			if err != nil {
				apiError(w, 500, "Could not remove member")
			} else {
				writeJSON(w, 200, map[string]bool{"removed": true})
			}
			return true
		}
		if !s.can(p, channel, false) {
			apiError(w, 403, "Channel access denied")
			return true
		}
		s.memberList(w, channel, p)
		return true
	}
	if path == "/v1/chat/memberships" && r.Method == "POST" {
		var a struct{ Channel, Action, Invitation string }
		if !webDecode(w, r, &a) {
			return true
		}
		if a.Action == "" && a.Invitation != "" {
			if err := JoinWorkspaceChannel(s.db, s.access, p, "", a.Invitation); err != nil {
				apiError(w, 403, err.Error())
			} else {
				writeJSON(w, 200, map[string]bool{"joined": true})
			}
			return true
		}
		if validateChannel(a.Channel) != nil {
			apiError(w, 400, "Choose a channel")
			return true
		}
		joined := a.Action != "leave"
		if joined {
			if channelArchived(s.db, a.Channel) {
				apiError(w, 409, "This channel is archived")
				return true
			}
			if !s.local {
				if err := JoinWorkspaceChannel(s.db, s.access, p, a.Channel, ""); err != nil {
					apiError(w, 403, err.Error())
					return true
				}
			}
		} else if !s.can(p, a.Channel, false) {
			apiError(w, 403, "Channel access denied")
			return true
		}
		if err := setChannelJoined(s.db, p.ID, a.Channel, joined); err != nil {
			apiError(w, 500, "Could not update membership")
		} else {
			writeJSON(w, 200, map[string]bool{"joined": joined})
		}
		return true
	}
	return false
}

func (s *webServer) memberList(w http.ResponseWriter, channel string, viewer *Principal) {
	rows, err := s.db.Query("SELECT id FROM principals WHERE status IN ('active','revoked') ORDER BY name")
	if err != nil {
		apiError(w, 500, "Could not list members")
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	list := []map[string]any{}
	seen := map[string]bool{}
	for _, id := range ids {
		p, err := findPrincipal(s.db, "id=?", id)
		if err != nil || cachePrincipal(s.db, p) != nil {
			continue
		}
		if channel != "" && (!principalPermission(s.db, p, channel, false) || !channelJoined(s.db, p.ID, channel)) {
			continue
		}
		seen[principalAddress(p)] = true
		grants := []map[string]any{}
		for ch, g := range p.grants {
			if channel != "" && ch != channel {
				continue
			}
			if g[0] {
				grants = append(grants, map[string]any{"channel": ch, "write": g[1]})
			}
		}
		list = append(list, map[string]any{"id": p.ID, "name": principalAddress(p), "kind": p.Kind, "status": p.Status, "grants": grants, "admin": principalPermission(s.db, p, "*", true), "self": p.ID == viewer.ID || p.ID == viewer.ParentID})
	}
	if s.local {
		rows, err := s.db.Query("SELECT DISTINCT sender FROM messages m WHERE NOT EXISTS(SELECT 1 FROM remote_messages rm WHERE rm.local_id=m.id) UNION SELECT name FROM local_participants UNION SELECT agent_name FROM agent_sessions ORDER BY 1")
		if err == nil {
			var names []string
			for rows.Next() {
				var n string
				if rows.Scan(&n) == nil && !seen[n] {
					names = append(names, n)
				}
			}
			rows.Close()
			for _, name := range names {
				if channel != "" {
					var participant bool
					_ = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages m WHERE sender=? AND target=? AND NOT EXISTS(SELECT 1 FROM remote_messages rm WHERE rm.local_id=m.id)) OR EXISTS(SELECT 1 FROM local_channel_grants WHERE name=? AND channel=?)", name, channel, name, channel).Scan(&participant)
					if !participant || !channelJoined(s.db, "local:"+name, channel) {
						continue
					}
				}
				enabled := true
				restricted := false
				_ = s.db.QueryRow("SELECT enabled,restricted FROM local_participants WHERE name=?", name).Scan(&enabled, &restricted)
				if channel != "" && !enabled {
					continue
				}
				status := "active"
				if !enabled {
					status = "revoked"
				}
				grants := []map[string]any{}
				if channel != "" {
					grants = append(grants, map[string]any{"channel": channel, "write": localSenderAllowed(s.db, name, channel)})
				} else if !restricted {
					grants = append(grants, map[string]any{"channel": "*", "write": true})
				} else {
					rows, err := s.db.Query("SELECT channel,can_write FROM local_channel_grants WHERE name=? ORDER BY channel", name)
					if err == nil {
						for rows.Next() {
							var ch string
							var wr bool
							if rows.Scan(&ch, &wr) == nil {
								grants = append(grants, map[string]any{"channel": ch, "write": wr})
							}
						}
						rows.Close()
					}
				}
				list = append(list, map[string]any{"id": "local:" + name, "name": name, "kind": "agent", "status": status, "grants": grants, "admin": false, "local": true})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"members": list})
}
func (s *webServer) localMember(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		apiError(w, 400, "Choose a local agent")
		return
	}
	var registered bool
	if s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM principals WHERE name=? AND parent_id IS NULL) OR EXISTS(SELECT 1 FROM principal_aliases WHERE address=?)", name, name).Scan(&registered) != nil || registered {
		apiError(w, 409, "Manage this registered profile using its profile ID")
		return
	}
	if r.Method == "DELETE" {
		_, err := s.db.Exec("INSERT INTO local_participants(name,enabled) VALUES(?,0) ON CONFLICT(name) DO UPDATE SET enabled=0", name)
		if err != nil {
			apiError(w, 500, "Could not remove agent")
		} else {
			writeJSON(w, 200, map[string]bool{"removed": true})
		}
		return
	}
	if r.Method != "PATCH" {
		apiError(w, 405, "Method not allowed")
		return
	}
	var a struct {
		Channels []string
		Grants   []struct {
			Channel string
			Write   bool
		}
		Write, DM, Admin bool
		Active           *bool
	}
	if !webDecode(w, r, &a) {
		return
	}
	if a.Active != nil {
		_, err := s.db.Exec("INSERT INTO local_participants(name,enabled) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET enabled=excluded.enabled", name, *a.Active)
		if err != nil {
			apiError(w, 500, "Could not restore agent")
		} else {
			writeJSON(w, 200, map[string]bool{"updated": true})
		}
		return
	}
	if a.Admin {
		a.Channels = []string{"*"}
		a.Write = true
	}
	if a.DM {
		a.Channels = append(a.Channels, "@*")
	}
	rights := map[string]bool{}
	if a.Grants != nil && !a.Admin {
		a.Channels = nil
		for _, g := range a.Grants {
			a.Channels = append(a.Channels, g.Channel)
			rights[g.Channel] = g.Write
		}
	}
	for _, ch := range a.Channels {
		if ch != "*" && ch != "@*" && validateChannel(ch) != nil {
			apiError(w, 400, "Invalid channel")
			return
		}
	}
	tx, err := beginWrite(s.db)
	if err != nil {
		apiError(w, 500, "Could not update agent")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO local_participants(name,enabled,restricted) VALUES(?,1,1) ON CONFLICT(name) DO UPDATE SET restricted=1", name)
	if err == nil {
		_, err = tx.Exec("DELETE FROM local_channel_grants WHERE name=?", name)
	}
	for _, ch := range a.Channels {
		if err != nil {
			break
		}
		_, err = tx.Exec("INSERT INTO local_channel_grants(name,channel,can_write) VALUES(?,?,?) ON CONFLICT(name,channel) DO UPDATE SET can_write=excluded.can_write", name, ch, func() bool {
			if a.Grants != nil && !a.Admin {
				return rights[ch]
			}
			return a.Write
		}())
	}
	if err != nil || tx.Commit() != nil {
		apiError(w, 500, "Could not update agent")
	} else {
		writeJSON(w, 200, map[string]bool{"updated": true})
	}
}
