package internal

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type directWire struct {
	wireMessage
	PeerID   string `json:"peer_id"`
	PeerName string `json:"peer_name"`
	PeerKind string `json:"peer_kind"`
}
type directPage struct {
	Messages []directWire `json:"messages"`
	Cursor   int64        `json:"cursor"`
	Name     string       `json:"name"`
	Write    bool         `json:"write"`
}
type directSend struct {
	ID, Target, Body, ParentID, IdempotencyKey string
	CreatedAt                                  int64
}

func splitRemoteDMTarget(target string) (string, string, bool) {
	if !strings.HasPrefix(target, "@") {
		return "", "", false
	}
	alias, peer, ok := strings.Cut(target[1:], "/")
	if !ok || alias == "" || peer == "" || strings.ContainsAny(peer, "/\r\n\x00") {
		return "", "", false
	}
	return alias, "@" + peer, true
}
func splitRemoteConversation(target string) (string, string, bool) {
	if strings.HasPrefix(target, "@") {
		return splitRemoteDMTarget(target)
	}
	return splitRemoteTarget(target)
}

func directConversationPredicate(db dbtx, self, peer string) (string, []any) {
	selfTargets := inboxAliases(db, self)
	peerTargets := inboxAliases(db, peer)
	selfSlots := strings.TrimRight(strings.Repeat("?,", len(selfTargets)), ",")
	peerSlots := strings.TrimRight(strings.Repeat("?,", len(peerTargets)), ",")
	args := []any{}
	if self == peer {
		for _, target := range selfTargets {
			args = append(args, target)
		}
		return "m.target IN (" + selfSlots + ")", args
	}
	for _, target := range selfTargets {
		args = append(args, strings.TrimPrefix(target, "@"))
	}
	for _, target := range peerTargets {
		args = append(args, target)
	}
	for _, target := range peerTargets {
		args = append(args, strings.TrimPrefix(target, "@"))
	}
	for _, target := range selfTargets {
		args = append(args, target)
	}
	return "((m.sender IN (" + selfSlots + ") AND m.target IN (" + peerSlots + ")) OR (m.sender IN (" + peerSlots + ") AND m.target IN (" + selfSlots + ")))", args
}

func (s *webServer) directMessages(w http.ResponseWriter, r *http.Request, p *Principal) bool {
	if r.URL.Path != "/v1/chat/direct-messages" {
		return false
	}
	if !principalPermission(s.db, p, "@*", r.Method != "GET") {
		apiError(w, 403, "Direct-message access is not granted to this profile")
		return true
	}
	if r.Method == "POST" {
		var args directSend
		if !webDecode(w, r, &args) {
			return true
		}
		if !validMessageID(args.ID) || args.CreatedAt <= 0 || args.CreatedAt > time.Now().Add(5*time.Minute).UnixMilli() || !strings.HasPrefix(args.Target, "@") || strings.Contains(args.Target, "/") {
			apiError(w, 400, "Invalid direct message")
			return true
		}
		target := args.Target
		if args.ParentID != "" {
			parent, err := findMessageByID(s.db, args.ParentID)
			if err != nil || !strings.HasPrefix(parent.Target, "@") || (!principalOwnsAddress(p, parent.Sender) && !principalOwnsAddress(p, strings.TrimPrefix(parent.Target, "@"))) {
				apiError(w, 403, "Private conversation")
				return true
			}
			target = args.ParentID
		}
		message, replayed, err := sendRegistered(s.db, p, target, args.Body, args.IdempotencyKey, &Message{ID: args.ID, CreatedAt: args.CreatedAt})
		if err != nil {
			apiError(w, 400, err.Error())
			return true
		}
		writeJSON(w, 201, map[string]any{"message": attributedWire(s.db, message), "replayed": replayed})
		return true
	}
	if r.Method != "GET" {
		apiError(w, 405, "Use GET or POST")
		return true
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after = 0
		err = nil
	}
	if err != nil || after < 0 {
		apiError(w, 400, "Invalid cursor")
		return true
	}
	var high int64
	if s.db.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages").Scan(&high) != nil {
		apiError(w, 500, "Could not read direct messages")
		return true
	}
	names := []string{principalAddress(p)}
	for alias := range p.aliases {
		if alias != principalAddress(p) {
			names = append(names, alias)
		}
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
	args := []any{after, high}
	for _, name := range names {
		args = append(args, name)
	}
	for _, name := range names {
		args = append(args, "@"+name)
	}
	rows, err := s.db.Query(`SELECT seq,id,sender,target,body,chain_root,chain_depth,parent_id,correlation,created_at FROM messages m
 WHERE seq>? AND seq<=? AND target LIKE '@%' AND (sender IN (`+placeholders+`) OR target IN (`+placeholders+`))
 AND NOT EXISTS(SELECT 1 FROM remote_messages r WHERE r.local_id=m.id) ORDER BY seq LIMIT 100`, args...)
	if err != nil {
		apiError(w, 500, "Could not read direct messages")
		return true
	}
	messages := []*Message{}
	for rows.Next() {
		message, readErr := scanMessage(rows, true)
		if readErr != nil {
			rows.Close()
			apiError(w, 500, "Could not read direct messages")
			return true
		}
		messages = append(messages, message)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		apiError(w, 500, "Could not read direct messages")
		return true
	}
	page := directPage{Messages: []directWire{}, Cursor: high, Name: principalAddress(p), Write: principalPermission(s.db, p, "@*", true)}
	if len(messages) == 100 {
		page.Cursor = messages[len(messages)-1].Seq
	}
	for _, message := range messages {
		peer := message.Sender
		if principalOwnsAddress(p, message.Sender) {
			peer = strings.TrimPrefix(message.Target, "@")
		}
		peerID, kind := "address:"+peer, "agent"
		if member, err := participantByAddress(s.db, peer); err == nil && cachePrincipal(s.db, member) == nil {
			peerID, peer, kind = member.ID, principalAddress(member), member.Kind
		}
		wire := attributedWire(s.db, message)
		wire.Sender = message.Sender
		page.Messages = append(page.Messages, directWire{wire, peerID, peer, kind})
	}
	writeJSON(w, 200, page)
	return true
}

func ensureDirectThread(db dbtx, remote string, owner string, peerID, name, kind string) (string, error) {
	var target string
	err := db.QueryRow("SELECT target FROM remote_direct_threads WHERE remote_name=? AND peer_id=?", remote, peerID).Scan(&target)
	if err == sql.ErrNoRows {
		target = "@" + remote + "/" + name
	} else if err != nil {
		return "", err
	}
	_, err = db.Exec("INSERT INTO remote_direct_threads(remote_name,peer_id,peer_name,peer_kind,target,local_principal_id) VALUES(?,?,?,?,?,?) ON CONFLICT(remote_name,peer_id) DO UPDATE SET peer_name=excluded.peer_name,peer_kind=excluded.peer_kind", remote, peerID, name, kind, target, owner)
	return target, err
}

func syncDirectMessages(ctx context.Context, db *sql.DB, remote Remote) (SyncStats, error) {
	stats := SyncStats{}
	var owner string
	if db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner) != nil {
		return stats, nil
	}
	var position int64
	_ = db.QueryRow("SELECT position FROM remote_direct_state WHERE remote_name=?", remote.Name).Scan(&position)
	canWrite := false
	for pageNumber := 0; pageNumber < 5; pageNumber++ {
		page := directPage{}
		if err := remoteRequest(ctx, remote, "GET", "/v1/chat/direct-messages?after="+strconv.FormatInt(position, 10), nil, &page); err != nil {
			if httpError, ok := err.(*remoteHTTPError); ok && httpError.Status == 404 {
				return stats, nil
			}
			return stats, err
		}
		canWrite = page.Write
		tx, err := beginWrite(db)
		if err != nil {
			return stats, err
		}
		save := func() error {
			for _, wire := range page.Messages {
				if wire.Seq <= position || !validMessageID(wire.ID) || !strings.HasPrefix(wire.Target, "@") || wire.PeerID == "" || wire.PeerName == "" || strings.Contains(wire.PeerName, "/") || len(wire.Body) > MaxBodyBytes {
					return fmt.Errorf("invalid direct-message history")
				}
				target, err := ensureDirectThread(tx, remote.Name, owner, wire.PeerID, wire.PeerName, wire.PeerKind)
				if err != nil {
					return err
				}
				id, err := localRemoteID(tx, remote.Name, wire.ID)
				if err != nil {
					return err
				}
				message := wire.message()
				message.ID = id
				message.Target = target
				message.ChainRoot, err = localRemoteID(tx, remote.Name, wire.ChainRoot)
				if err != nil {
					return err
				}
				if wire.ParentID != nil {
					parent, err := localRemoteID(tx, remote.Name, *wire.ParentID)
					if err != nil {
						return err
					}
					message.ParentID = &parent
				}
				if message.Correlation != nil {
					message.Correlation = &message.ID
				}
				existing, err := findMessageByID(tx, id)
				if err == sql.ErrNoRows {
					if err = message.save(tx); err != nil {
						return err
					}
					stats.Pulled++
				} else if err != nil {
					return err
				} else if !sameWire(toWire(existing), toWire(message)) {
					return fmt.Errorf("workspace changed a direct message")
				}
				if _, err = tx.Exec("UPDATE remote_messages SET remote_seq=? WHERE local_id=?", wire.Seq, id); err != nil {
					return err
				}
				if wire.PrincipalID != "" {
					if _, err = tx.Exec("INSERT INTO remote_attributions(local_id,principal_id,owner_id,approved) VALUES(?,?,?,?) ON CONFLICT(local_id) DO UPDATE SET principal_id=excluded.principal_id,owner_id=excluded.owner_id,approved=excluded.approved", id, wire.PrincipalID, wire.OwnerID, wire.OwnerApproved); err != nil {
						return err
					}
				}
				if err = queueMessageFiles(tx, remote.Name, target, wire.Body); err != nil {
					return err
				}
				for _, file := range fileLinks.FindAllStringSubmatch(wire.Body, 5) {
					if _, err = tx.Exec("INSERT INTO remote_file_origins(remote_name,file_id,target,native_target) VALUES(?,?,?,?) ON CONFLICT(remote_name,file_id,target) DO NOTHING", remote.Name, file[1], target, wire.Target); err != nil {
						return err
					}
				}
			}
			_, err := tx.Exec("INSERT INTO remote_direct_state(remote_name,local_principal_id,position,can_write) VALUES(?,?,?,?) ON CONFLICT(remote_name) DO UPDATE SET position=MAX(remote_direct_state.position,excluded.position),can_write=excluded.can_write", remote.Name, owner, page.Cursor, page.Write)
			if err != nil {
				return err
			}
			if page.Name != "" {
				_, err = tx.Exec("UPDATE remote_human_sessions SET name=? WHERE server_id=?", page.Name, remote.ServerID)
			}
			return err
		}()
		if save != nil {
			tx.Rollback()
			return stats, save
		}
		if err = tx.Commit(); err != nil {
			return stats, err
		}
		position = page.Cursor
		if len(page.Messages) < 100 {
			break
		}
	}
	if !canWrite {
		return stats, nil
	}
	for {
		var id, peer, key string
		err := db.QueryRow("SELECT o.local_id,o.peer_name,o.idempotency_key FROM remote_direct_outbox o JOIN remote_messages r ON r.local_id=o.local_id WHERE o.remote_name=? AND r.remote_seq IS NULL ORDER BY (SELECT seq FROM messages WHERE id=o.local_id) LIMIT 1", remote.Name).Scan(&id, &peer, &key)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return stats, err
		}
		message, err := findMessageByID(db, id)
		if err != nil {
			return stats, err
		}
		request := directSend{ID: id, Target: "@" + peer, Body: string(message.Body), CreatedAt: message.CreatedAt, IdempotencyKey: key}
		if message.ParentID != nil {
			if err = db.QueryRow("SELECT remote_id FROM remote_messages WHERE local_id=? AND remote_name=? AND remote_seq IS NOT NULL", *message.ParentID, remote.Name).Scan(&request.ParentID); err != nil {
				return stats, fmt.Errorf("reply waits for its original message: %w", err)
			}
		}
		var response struct {
			Message wireMessage `json:"message"`
		}
		if err = remoteRequest(ctx, remote, "POST", "/v1/chat/direct-messages", request, &response); err != nil {
			return stats, err
		}
		if response.Message.ID != message.ID || response.Message.Body != string(message.Body) || response.Message.Seq <= 0 {
			return stats, fmt.Errorf("workspace acknowledged a different direct message")
		}
		if _, err = db.Exec("UPDATE remote_messages SET remote_seq=? WHERE local_id=? AND remote_name=?", response.Message.Seq, id, remote.Name); err != nil {
			return stats, err
		}
		stats.Pushed++
	}
	return stats, nil
}

func queueDirectMessage(db *sql.DB, p *Principal, target, body, key string, parent *Message) (*Message, bool, error) {
	if len(body) > MaxBodyBytes || len(key) > MaxIdempotencyKeyBytes {
		return nil, false, fmt.Errorf("message or retry key is too long")
	}
	var remote, peer, owner, sender string
	if err := db.QueryRow("SELECT d.remote_name,d.peer_name,d.local_principal_id,h.name FROM remote_direct_threads d JOIN remotes r ON r.name=d.remote_name JOIN remote_human_sessions h ON h.server_id=r.server_id WHERE d.target=?", target).Scan(&remote, &peer, &owner, &sender); err != nil || owner != p.ID {
		return nil, false, fmt.Errorf("this direct conversation belongs to another account")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if key != "" {
		var id string
		if tx.QueryRow("SELECT message_id FROM idempotency WHERE agent_name=? AND key=?", p.ID, key).Scan(&id) == nil {
			message, err := findMessageByID(tx, id)
			if err == nil {
				var pending bool
				_ = tx.QueryRow("SELECT remote_seq IS NULL FROM remote_messages WHERE local_id=?", id).Scan(&pending)
				message.Pending = pending
			}
			return message, true, err
		}
	}
	message := NewMessage(sender, target, []byte(body))
	message.Pending = true
	if parent != nil {
		if parent.Target != target {
			return nil, false, fmt.Errorf("reply belongs to another conversation")
		}
		if parent.ChainDepth >= 8 {
			return nil, false, fmt.Errorf("thread is too deep")
		}
		message.ChainRoot = parent.ChainRoot
		message.ChainDepth = parent.ChainDepth + 1
		message.ParentID = &parent.ID
	}
	if err = message.save(tx); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec("INSERT INTO remote_messages(remote_name,remote_id,local_id) VALUES(?,?,?)", remote, message.ID, message.ID); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec("INSERT INTO remote_direct_outbox(local_id,remote_name,peer_name,idempotency_key) VALUES(?,?,?,?)", message.ID, remote, peer, "desktop-dm:"+message.ID); err != nil {
		return nil, false, err
	}
	if _, err = tx.Exec("INSERT INTO message_principals(message_id,principal_id) VALUES(?,?)", message.ID, p.ID); err != nil {
		return nil, false, err
	}
	if key != "" {
		if _, err = tx.Exec("INSERT INTO idempotency(agent_name,key,message_id,created_at) VALUES(?,?,?,?)", p.ID, key, message.ID, message.CreatedAt); err != nil {
			return nil, false, err
		}
	}
	if err = followHumanMessage(tx, p, message); err != nil {
		return nil, false, err
	}
	if err = wakeRemote(tx, remote); err != nil {
		return nil, false, err
	}
	return message, false, tx.Commit()
}

func (s *webServer) directConversations(w http.ResponseWriter, r *http.Request, p *Principal) bool {
	if r.URL.Path != "/v1/chat/direct-conversations" {
		return false
	}
	if !s.local {
		writeJSON(w, 200, map[string]any{"conversations": []any{}, "read_states": s.directReadStates(p)})
		return true
	}
	if r.Method == "POST" {
		var args struct{ Workspace, Peer string }
		if !webDecode(w, r, &args) {
			return true
		}
		remote, err := s.workspaceAccount(r.Context(), p, args.Workspace)
		if err != nil {
			apiError(w, 403, err.Error())
			return true
		}
		var capabilities serverInfo
		if err = remoteRequest(r.Context(), remote, "GET", "/v1/server", nil, &capabilities); err != nil {
			apiError(w, 503, "Workspace is unavailable")
			return true
		}
		if !capabilities.DirectMessages {
			apiError(w, 409, "Update this workspace's coordinator to use direct messages in the desktop app")
			return true
		}
		var result struct {
			People []struct{ ID, Name, Kind string } `json:"people"`
		}
		if err = remoteRequest(r.Context(), remote, "GET", "/v1/chat/participants?name="+url.QueryEscape(args.Peer), nil, &result); err != nil || len(result.People) != 1 {
			apiError(w, 400, "Choose an existing participant in this workspace")
			return true
		}
		peer := result.People[0]
		target, err := ensureDirectThread(s.db, remote.Name, p.ID, peer.ID, peer.Name, peer.Kind)
		if err != nil {
			apiError(w, 500, "Could not open the conversation")
			return true
		}
		writeJSON(w, 200, map[string]any{"target": target, "conversation": map[string]any{"name": target, "label": peer.Name, "kind": peer.Kind, "peer_id": peer.ID, "source_id": remote.Name, "source": remote.Name, "write": true, "unread": 0}})
		return true
	}
	if r.Method != "GET" {
		apiError(w, 405, "Use GET or POST")
		return true
	}
	rows, err := s.db.Query("SELECT d.target,d.peer_name,d.peer_kind,d.peer_id,d.remote_name,COALESCE(s.can_write,1) FROM remote_direct_threads d LEFT JOIN remote_direct_state s ON s.remote_name=d.remote_name WHERE d.local_principal_id=? ORDER BY d.peer_name", p.ID)
	if err != nil {
		apiError(w, 500, "Could not list direct conversations")
		return true
	}
	list := []map[string]any{}
	for rows.Next() {
		var target, name, kind, id, source string
		var write bool
		if rows.Scan(&target, &name, &kind, &id, &source, &write) != nil {
			continue
		}
		list = append(list, map[string]any{"name": target, "label": name, "kind": kind, "peer_id": id, "source_id": source, "source": source, "write": write, "unread": 0})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		apiError(w, 500, "Could not list direct conversations")
		return true
	}
	for _, item := range list {
		target := item["name"].(string)
		item["muted"] = conversationSettings(s.db, p.ID, target).Muted
		position, _ := conversationReadPosition(s.db, p.ID, target)
		var count int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM messages m WHERE m.target=? AND m.seq>? AND NOT EXISTS(SELECT 1 FROM message_principals mp WHERE mp.message_id=m.id AND mp.principal_id=?) AND NOT EXISTS(SELECT 1 FROM remote_attributions a JOIN remotes r ON r.name=? JOIN remote_human_sessions h ON h.server_id=r.server_id WHERE a.local_id=m.id AND a.principal_id=h.principal_id) AND NOT EXISTS(SELECT 1 FROM human_threads t WHERE t.principal_id=? AND t.root_id=m.chain_root AND m.parent_id IS NOT NULL AND t.position>=m.seq)`, target, position, p.ID, item["source_id"], p.ID).Scan(&count)
		item["unread"] = count
	}
	writeJSON(w, 200, map[string]any{"conversations": list, "read_states": s.directReadStates(p)})
	return true
}
