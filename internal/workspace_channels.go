package internal

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type WorkspaceChannel struct {
	Channel string `json:"channel"`
	Open    bool   `json:"open"`
	Owner   string `json:"owner_id"`
	Creator string `json:"creator_id"`
}

func trustedParticipant(db dbtx, p *Principal) bool {
	return isChatParticipant(p) && trustedWorkspaceIdentity(db, p)
}

func trustedWorkspaceIdentity(db dbtx, p *Principal) bool {
	if p == nil || p.Status != "active" {
		return false
	}
	owner := p.ID
	if p.ParentID != "" {
		parent, err := findPrincipal(db, "id=?", p.ParentID)
		if err != nil || parent.Status != "active" {
			return false
		}
		owner = parent.ID
	}
	var approved bool
	return db.QueryRow("SELECT approved FROM principal_approvals WHERE principal_id=?", owner).Scan(&approved) == nil && approved
}

// A native cache may join a future channel before its first send. Return an
// empty page rather than querying messages after this check: another member
// might create a private channel with that name in between the two operations.
func prospectiveChannel(db dbtx, access *channelAccess, p *Principal, channel string) bool {
	if validateChannel(channel) != nil || !access.isServed(channel) || !trustedWorkspaceIdentity(db, p) {
		return false
	}
	if p.scopes != nil && !p.scopes["grpvn.read"] {
		return false
	}
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE target=?) OR EXISTS(SELECT 1 FROM permissions WHERE channel=?) OR EXISTS(SELECT 1 FROM workspace_channels WHERE channel=?) OR EXISTS(SELECT 1 FROM remote_channels WHERE target=?)`, channel, channel, channel, channel).Scan(&exists)
	return err == nil && !exists
}

func channelCandidates(db *sql.DB, access *channelAccess) ([]string, error) {
	rows, err := db.Query(`SELECT target FROM messages m WHERE target LIKE '#%' AND NOT EXISTS(SELECT 1 FROM remote_messages rm WHERE rm.local_id=m.id)
 UNION SELECT channel FROM permissions WHERE channel LIKE '#%'
 UNION SELECT channel FROM workspace_channels ORDER BY target`)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var ch string
		if err := rows.Scan(&ch); err != nil {
			rows.Close()
			return nil, err
		}
		seen[ch] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for ch := range access.served {
		seen[ch] = true
	}
	for ch := range access.overrides {
		seen[ch] = true
	}
	out := make([]string, 0, len(seen))
	for ch := range seen {
		out = append(out, ch)
	}
	sort.Strings(out)
	return out, nil
}

func channelGrant(db dbtx, p *Principal, channel string, write bool) error {
	for _, id := range []string{p.ID, p.ParentID} {
		if id == "" {
			continue
		}
		if _, err := db.Exec(`INSERT INTO permissions(principal_id,channel,can_read,can_write) VALUES(?,?,1,?)
   ON CONFLICT(principal_id,channel) DO UPDATE SET can_read=1,can_write=MAX(permissions.can_write,excluded.can_write)`, id, channel, write); err != nil {
			return err
		}
		if strings.HasPrefix(channel, "#") {
			if err := setChannelJoined(db, id, channel, true); err != nil {
				return err
			}
		}
	}
	return TouchChatMetadata(db)
}

func CreateWorkspaceChannel(db *sql.DB, access *channelAccess, p *Principal, channel string, open bool) (WorkspaceChannel, error) {
	if p == nil {
		return WorkspaceChannel{}, fmt.Errorf("workspace membership required")
	}
	result := WorkspaceChannel{Channel: channel, Open: open, Creator: p.ID, Owner: p.ID}
	if err := validateChannel(channel); err != nil {
		return result, err
	}
	if !access.isServed(channel) {
		return result, fmt.Errorf("channel is outside the coordinator's configured channel set")
	}
	if p.scopes != nil && !p.scopes["grpvn.write"] {
		return result, fmt.Errorf("credential does not grant write scope")
	}
	if p.ParentID != "" {
		result.Owner = p.ParentID
	}
	tx, err := beginWrite(db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if !trustedParticipant(tx, p) {
		return result, fmt.Errorf("workspace membership is required to create channels")
	}
	var cached bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM remote_channels WHERE target=?)", channel).Scan(&cached); err != nil {
		return result, err
	}
	if cached {
		return result, fmt.Errorf("cannot create a channel in a local remote-source namespace")
	}
	err = tx.QueryRow("SELECT is_open,owner_id,creator_id FROM workspace_channels WHERE channel=?", channel).Scan(&result.Open, &result.Owner, &result.Creator)
	if err == nil {
		if !access.isOpenWith(tx, channel) && !principalPermission(tx, p, channel, false) {
			return result, fmt.Errorf("channel permission denied")
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	var existing bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE target=?) OR EXISTS(SELECT 1 FROM permissions WHERE channel=?)`, channel, channel).Scan(&existing); err != nil {
		return result, err
	}
	if existing {
		if !access.isOpenWith(tx, channel) && !principalPermission(tx, p, channel, true) {
			return result, fmt.Errorf("channel permission denied; ask a member to invite you")
		}
		result.Open = access.isOpenWith(tx, channel)
		result.Owner = ""
		result.Creator = ""
		return result, tx.Commit()
	}
	if configured, ok := access.overrides[channel]; ok && configured != result.Open {
		return result, fmt.Errorf("this channel's visibility is configured by the server")
	}
	if _, err := tx.Exec("INSERT INTO workspace_channels(channel,is_open,owner_id,creator_id,created_at) VALUES(?,?,?,?,?)", channel, result.Open, result.Owner, p.ID, time.Now().UnixMilli()); err != nil {
		return result, err
	}
	if err := channelGrant(tx, p, channel, true); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, cachePrincipal(db, p)
}

func ensureSendChannel(db *sql.DB, access *channelAccess, p *Principal, channel string) bool {
	if rpcAllowed(db, access, p, channel, true) {
		return true
	}
	if !strings.HasPrefix(channel, "#") {
		return false
	}
	if _, native, ok := strings.Cut(strings.TrimPrefix(channel, "#"), "/"); ok && rpcAllowed(db, access, p, "#"+native, false) {
		return false
	}
	_, err := CreateWorkspaceChannel(db, access, p, channel, false)
	return err == nil && rpcAllowed(db, access, p, channel, true)
}

func participantByAddress(db dbtx, address string) (*Principal, error) {
	address = strings.TrimPrefix(address, "@")
	p, err := findPrincipal(db, "id=?", address)
	if err == nil && isChatParticipant(p) {
		return p, nil
	}
	var id string
	err = db.QueryRow("SELECT principal_id FROM principal_aliases WHERE address=?", address).Scan(&id)
	if err == nil {
		return findPrincipal(db, "id=?", id)
	}
	rows, err := db.Query("SELECT id,name,COALESCE(parent_id,'') FROM principals WHERE kind IN ('agent','human') AND status IN ('active','pending')")
	if err != nil {
		return nil, err
	}
	var candidates []*Principal
	for rows.Next() {
		p := &Principal{Kind: "agent"}
		if err := rows.Scan(&p.ID, &p.Name, &p.ParentID); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, p := range candidates {
		if p.ParentID != "" {
			p.parent, err = findPrincipal(db, "id=?", p.ParentID)
			if err != nil {
				continue
			}
		}
		if principalAddress(p) == address {
			return findPrincipal(db, "id=?", p.ID)
		}
	}
	return nil, errParticipantNotFound
}

func InviteWorkspaceAgent(db *sql.DB, access *channelAccess, inviter *Principal, channel, address string, write bool) (*Principal, error) {
	if err := validateChannel(channel); err != nil {
		return nil, err
	}
	if inviter == nil {
		return nil, fmt.Errorf("workspace membership required")
	}
	if inviter.scopes != nil && (!inviter.scopes["grpvn.write"] || !inviter.scopes["grpvn.read"]) {
		return nil, fmt.Errorf("inviting collaborators requires grpvn.read and grpvn.write scopes")
	}
	if !trustedParticipant(db, inviter) {
		return nil, fmt.Errorf("workspace membership is required")
	}
	if !rpcAllowed(db, access, inviter, channel, true) {
		return nil, fmt.Errorf("channel write permission denied")
	}
	// Channel members can invite peers within their existing rights, just as
	// they can already share messages there. No global/admin access is granted.
	if !access.isServed(channel) {
		return nil, fmt.Errorf("channel is not served")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	recipient, err := participantByAddress(tx, address)
	if err != nil {
		return nil, err
	}
	if recipient.Status == "revoked" || !isChatParticipant(recipient) {
		return nil, fmt.Errorf("this participant cannot be invited")
	}
	if recipient.ParentID != "" {
		parent, err := findPrincipal(tx, "id=?", recipient.ParentID)
		if err != nil || parent.Status != "active" {
			return nil, fmt.Errorf("agent owner is inactive")
		}
	}
	if err := channelGrant(tx, recipient, channel, write); err != nil {
		return nil, err
	}
	// A trusted channel owner can admit a peer that has registered but is
	// still waiting at its first OAuth sign-in. No human code relay is needed.
	if _, err := tx.Exec("UPDATE principals SET status='active' WHERE id=? AND status='pending'", recipient.ID); err != nil {
		return nil, err
	}
	approvalOwner := recipient.ID
	if recipient.ParentID != "" {
		approvalOwner = recipient.ParentID
	}
	if _, err := tx.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1) ON CONFLICT(principal_id) DO UPDATE SET approved=1", approvalOwner); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE oauth_request_approvals SET approved=1 WHERE request_id IN(SELECT id FROM oauth_requests WHERE principal_id=? AND consumed=0 AND expires_at>?)`, recipient.ID, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	recipient.Status = "active"
	return recipient, cachePrincipal(db, recipient)
}

func GrantWorkspaceChannels(db *sql.DB, id string, channels []string, write bool) error {
	p, err := findPrincipal(db, "id=?", id)
	if err != nil || p.Status != "active" {
		return fmt.Errorf("active member or agent ID required")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(channels) == 0 {
		return fmt.Errorf("explicit channel grants required")
	}
	for _, channel := range channels {
		if channel != "@*" && channel != "*" {
			if err := validateChannel(channel); err != nil {
				return err
			}
		}
		if err := channelGrant(tx, p, channel, write); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func WorkspaceAgents(db *sql.DB, pending bool) ([]map[string]string, error) {
	rows, err := db.Query(`SELECT p.id,p.name,p.status,COALESCE(owner.id,''),COALESCE(owner.name,''),COALESCE(owner.status,'active'),COALESCE(a.approved,0)
 FROM principals p LEFT JOIN principals owner ON owner.id=p.parent_id
 LEFT JOIN principal_approvals a ON a.principal_id=COALESCE(p.parent_id,p.id)
 WHERE p.kind='agent' AND p.status IN ('active','pending') ORDER BY p.name LIMIT 201`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, name, status, owner, ownerName, ownerStatus string
		var approved bool
		if err := rows.Scan(&id, &name, &status, &owner, &ownerName, &ownerStatus, &approved); err != nil {
			return nil, err
		}
		if ownerStatus != "active" {
			continue
		}
		if status != "active" || !approved {
			if !pending {
				continue
			}
			status = "pending"
		}
		if owner != "" {
			name = registeredSender(name, ownerName, owner)
		}
		out = append(out, map[string]string{"id": id, "name": name, "dm": "@" + name, "status": status})
	}
	return out, rows.Err()
}
