package internal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Principal struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	ParentID     string   `json:"parent_id,omitempty"`
	Status       string   `json:"status"`
	ApprovalCode string   `json:"approval_code,omitempty"`
	Requested    []string `json:"requested,omitempty"`
	grants       map[string][2]bool
	parent       *Principal
	scopes       map[string]bool
	aliases      map[string]bool
}
type principalContextKey struct{}
type registrationRequest struct {
	Credential string   `json:"credential"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind,omitempty"`
	Invite     string   `json:"invite,omitempty"`
	Channels   []string `json:"channels"`
}
type credentialRecord struct{ Secret, ID, Status, Code string }

func newCredential() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func credentialHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
func requestPrincipal(r *http.Request) *Principal {
	p, _ := r.Context().Value(principalContextKey{}).(*Principal)
	return p
}

func findPrincipal(db dbtx, query string, arg any) (*Principal, error) {
	var p Principal
	var parent sql.NullString
	var requested string
	err := db.QueryRow("SELECT id,name,kind,parent_id,status,approval_code,requested FROM principals WHERE "+query, arg).Scan(&p.ID, &p.Name, &p.Kind, &parent, &p.Status, &p.ApprovalCode, &requested)
	if err != nil {
		return nil, err
	}
	p.ParentID = parent.String
	_ = json.Unmarshal([]byte(requested), &p.Requested)
	return &p, nil
}
func authenticateMember(db *sql.DB, r *http.Request) (*Principal, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return nil, nil
	}
	secret, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return nil, fmt.Errorf("invalid authorization")
	}
	p, err := findPrincipal(db, "credential_hash = ?", credentialHash(secret))
	if errors.Is(err, sql.ErrNoRows) {
		p, err = findPrincipal(db, "id = (SELECT principal_id FROM oauth_credentials WHERE credential_hash=?)", credentialHash(secret))
	}
	if err != nil {
		return nil, fmt.Errorf("unregistered credential")
	}
	if p.Status != "active" {
		// Pending/revoked credentials never authorize private access.
		return nil, fmt.Errorf("registration %s (%s)", p.Status, p.ApprovalCode)
	}
	var expires int64
	if err := db.QueryRow("SELECT expires_at FROM credential_expiry WHERE credential_hash=?", credentialHash(secret)).Scan(&expires); err == nil && expires <= time.Now().UnixMilli() {
		return nil, fmt.Errorf("credential expired")
	}
	if p.ParentID != "" {
		parent, err := findPrincipal(db, "id = ?", p.ParentID)
		if err != nil || parent.Status != "active" {
			return nil, fmt.Errorf("owner registration is inactive")
		}
	}
	if err := cachePrincipal(db, p); err != nil {
		return nil, err
	}
	var scope string
	if err := db.QueryRow("SELECT scope FROM oauth_access WHERE credential_hash=?", credentialHash(secret)).Scan(&scope); err == nil {
		p.scopes = map[string]bool{}
		for _, s := range strings.Fields(scope) {
			p.scopes[s] = true
		}
	}
	return p, nil
}

func cachePrincipal(db dbtx, p *Principal) error {
	rows, err := db.Query("SELECT channel,can_read,can_write FROM permissions WHERE principal_id=?", p.ID)
	if err != nil {
		return err
	}
	p.grants = map[string][2]bool{}
	for rows.Next() {
		var channel string
		var read, write bool
		if err := rows.Scan(&channel, &read, &write); err != nil {
			rows.Close()
			return err
		}
		p.grants[channel] = [2]bool{read, write}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	aliases, err := db.Query("SELECT address FROM principal_aliases WHERE principal_id=?", p.ID)
	if err != nil {
		return err
	}
	p.aliases = map[string]bool{}
	for aliases.Next() {
		var address string
		if err := aliases.Scan(&address); err != nil {
			aliases.Close()
			return err
		}
		p.aliases[address] = true
	}
	err = aliases.Err()
	aliases.Close()
	if err != nil {
		return err
	}
	if p.ParentID != "" {
		parent, err := findPrincipal(db, "id=?", p.ParentID)
		if err != nil {
			return err
		}
		if parent.Status != "active" {
			return fmt.Errorf("owner registration is inactive")
		}
		if err := cachePrincipal(db, parent); err != nil {
			return err
		}
		p.parent = parent
	}
	return nil
}
func principalPermission(db dbtx, p *Principal, channel string, write bool) bool {
	if p != nil && p.scopes != nil {
		scope := "grpvn.read"
		if write {
			scope = "grpvn.write"
		}
		if !p.scopes[scope] {
			return false
		}
	}
	if p == nil || p.Status != "active" {
		return false
	}
	if p.grants != nil {
		index := 0
		if write {
			index = 1
		}
		allowed := p.grants[channel][index] || p.grants["*"][index]
		if p.parent != nil {
			return allowed && principalPermission(db, p.parent, channel, write)
		}
		return allowed
	}
	column := "can_read"
	if write {
		column = "can_write"
	}
	var allowed bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM permissions WHERE principal_id = ? AND channel IN (?, '*') AND "+column+" = 1)", p.ID, channel).Scan(&allowed); err != nil || !allowed {
		return false
	}
	if p.ParentID != "" {
		parent, err := findPrincipal(db, "id = ?", p.ParentID)
		return err == nil && principalPermission(db, parent, channel, write)
	}
	return true
}

func localCredential(db *sql.DB, serverID, agent string) (credentialRecord, error) {
	secret, err := newCredential()
	if err != nil {
		return credentialRecord{}, err
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO local_credentials (server_id,agent_name,credential) VALUES (?,?,?)", serverID, agent, secret); err != nil {
		return credentialRecord{}, err
	}
	var c credentialRecord
	err = db.QueryRow("SELECT credential,principal_id,status,approval_code FROM local_credentials WHERE server_id = ? AND agent_name = ?", serverID, agent).Scan(&c.Secret, &c.ID, &c.Status, &c.Code)
	return c, err
}

func CreateInvite(db dbtx, person string, channels []string, write bool, expires time.Duration, uses int) (string, string, error) {
	if person == "" || len(person) > 100 || strings.ContainsAny(person, "\r\n\x00") {
		return "", "", fmt.Errorf("pass a person or device label")
	}
	if len(channels) == 0 || expires <= 0 || uses < 1 {
		return "", "", fmt.Errorf("invite needs channels, a positive expiry and at least one use")
	}
	for _, ch := range channels {
		if ch != "*" && ch != "@*" {
			if err := validateChannel(ch); err != nil {
				return "", "", err
			}
		}
	}
	secret, err := newCredential()
	if err != nil {
		return "", "", err
	}
	id := NewMessage("", "", nil).ID
	encoded, _ := json.Marshal(channels)
	_, err = db.Exec("INSERT INTO invites (id,secret_hash,person,channels,can_write,expires_at,uses_left,created_at) VALUES (?,?,?,?,?,?,?,?)", id, credentialHash(secret), person, string(encoded), write, time.Now().Add(expires).UnixMilli(), uses, time.Now().UnixMilli())
	return id, secret, err
}

func registerMember(db *sql.DB, access *channelAccess, request registrationRequest, parent *Principal) (*Principal, error) {
	if len(request.Credential) < 32 || len(request.Credential) > 256 || len(request.Name) > 100 || strings.ContainsAny(request.Name, "\r\n\x00") {
		return nil, fmt.Errorf("invalid registration")
	}
	for _, ch := range request.Channels {
		if err := validateChannel(ch); err != nil {
			return nil, err
		}
	}
	tx, err := beginWrite(db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	hash := credentialHash(request.Credential)
	existing, err := findPrincipal(tx, "credential_hash = ?", hash)
	if err == nil && existing.Status == "revoked" {
		return nil, fmt.Errorf("registration revoked")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existing != nil && existing.Status == "active" {
		if parent == nil {
			return existing, tx.Commit()
		}
		if err := cachePrincipal(tx, existing); err != nil {
			return nil, err
		}
		if existing.ParentID != parent.ID || (request.Name != "" && !principalOwnsLocalName(existing, request.Name)) {
			return nil, fmt.Errorf("agent identity belongs to a different owner or name")
		}
	}
	p := Principal{ID: hash[:32], Name: request.Name, Kind: "member", Status: "pending", ApprovalCode: strings.ToUpper(NewMessage("", "", nil).ID[18:]), Requested: request.Channels}
	if existing != nil {
		p = *existing
	}
	var grants []string
	write := false
	if parent != nil {
		if (parent.Kind != "member" && parent.Kind != "human") || parent.Status != "active" {
			return nil, fmt.Errorf("only an active member can register an agent")
		}
		p.Kind, p.ParentID, p.Status = "agent", parent.ID, "active"
		if request.Kind == "human" {
			p.Kind = "human"
		} else if request.Kind != "" && request.Kind != "agent" {
			return nil, fmt.Errorf("invalid participant kind")
		}
		prospective := map[string]bool{}
		for _, ch := range request.Channels {
			if !access.isServed(ch) || (!access.isOpenWith(tx, ch) && !principalPermission(tx, parent, ch, false)) {
				if prospectiveChannel(tx, access, parent, ch) {
					prospective[ch] = true
				} else {
					return nil, fmt.Errorf("owner has no access to %s", ch)
				}
			}
		}
		if p.Name == "" {
			p.Name, err = generatedPrincipalName(tx)
			if err != nil {
				return nil, err
			}
		}
		if strings.ContainsAny(p.Name, " \t\r\n\x00@/~#") {
			return nil, fmt.Errorf("invalid agent name")
		}
		address := registeredSender(p.Name, parent.Name, parent.ID)
		var taken bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM principals WHERE kind IN ('agent','human') AND parent_id=? AND name=? AND id<>?) OR EXISTS(SELECT 1 FROM principal_aliases WHERE address=? AND principal_id<>?)`, parent.ID, p.Name, p.ID, address, p.ID).Scan(&taken); err != nil {
			return nil, err
		}
		if taken {
			return nil, fmt.Errorf("that agent name is already assigned")
		}
		write = true
		for _, ch := range request.Channels {
			if !prospective[ch] {
				grants = append(grants, ch)
			}
		}
		rows, err := tx.Query("SELECT channel FROM permissions WHERE principal_id=?", parent.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ch string
			if err := rows.Scan(&ch); err != nil {
				rows.Close()
				return nil, err
			}
			grants = append(grants, ch)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	} else if request.Invite != "" {
		var label, encoded string
		var expires int64
		var uses int
		var revoked bool
		err := tx.QueryRow("SELECT person,channels,can_write,expires_at,uses_left,revoked FROM invites WHERE secret_hash = ?", credentialHash(request.Invite)).Scan(&label, &encoded, &write, &expires, &uses, &revoked)
		if err != nil || revoked || uses < 1 || expires <= time.Now().UnixMilli() {
			return nil, fmt.Errorf("invite is invalid, expired, revoked or exhausted")
		}
		if err := json.Unmarshal([]byte(encoded), &grants); err != nil {
			return nil, err
		}
		var invitationID string
		if err := tx.QueryRow("SELECT id FROM invites WHERE secret_hash=?", credentialHash(request.Invite)).Scan(&invitationID); err != nil {
			return nil, err
		}
		if err := validateInvitationIssuer(tx, access, invitationID, grants, write); err != nil {
			return nil, err
		}
		p.Name, p.Status = label, "active"
		if _, err := tx.Exec("UPDATE invites SET uses_left = uses_left - 1 WHERE secret_hash = ?", credentialHash(request.Invite)); err != nil {
			return nil, err
		}
	} else if access.hasAnonymousAccessWith(tx) {
		p.Name, p.Status = "guest-"+p.ID[:8], "active"
		for _, ch := range request.Channels {
			if access.isServed(ch) && access.isOpenWith(tx, ch) {
				grants = append(grants, ch)
			}
		}
		write = true
	}
	if p.Name == "" {
		p.Name, err = generatedPrincipalName(tx)
		if err != nil {
			return nil, err
		}
	}
	if existing == nil {
		encoded, _ := json.Marshal(request.Channels)
		_, err = tx.Exec("INSERT INTO principals (id,credential_hash,name,kind,parent_id,status,approval_code,requested,created_at) VALUES (?,?,?,?,?,?,?,?,?)", p.ID, hash, p.Name, p.Kind, nullString(p.ParentID), p.Status, p.ApprovalCode, string(encoded), time.Now().UnixMilli())
	} else {
		_, err = tx.Exec("UPDATE principals SET name = ?, status = ? WHERE id = ?", p.Name, p.Status, p.ID)
	}
	if err != nil {
		return nil, err
	}
	for _, ch := range grants {
		canWrite := write
		if parent != nil && !access.isOpenWith(tx, ch) {
			canWrite = principalPermission(tx, parent, ch, true)
		}
		if _, err := tx.Exec("INSERT INTO permissions (principal_id,channel,can_read,can_write) VALUES (?,?,1,?) ON CONFLICT(principal_id,channel) DO UPDATE SET can_read=1,can_write=excluded.can_write", p.ID, ch, canWrite); err != nil {
			return nil, err
		}
	}
	if request.Invite != "" && parent == nil {
		if _, err := tx.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1) ON CONFLICT(principal_id) DO UPDATE SET approved=1", p.ID); err != nil {
			return nil, err
		}
	}
	return &p, tx.Commit()
}

func RemoteRegistration(db *sql.DB, name string) (Principal, error) {
	var p Principal
	err := db.QueryRow(`SELECT c.principal_id,c.status,c.approval_code FROM local_credentials c JOIN remotes r ON r.server_id=c.server_id WHERE r.name=? AND c.agent_name=''`, name).Scan(&p.ID, &p.Status, &p.ApprovalCode)
	return p, err
}
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func enrollmentHandler(db *sql.DB, access *channelAccess, agent bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request registrationRequest
		if err := decoder.Decode(&request); err != nil {
			apiError(w, 400, "invalid registration JSON")
			return
		}
		var parent *Principal
		if agent {
			parent = requestPrincipal(r)
			if parent == nil {
				apiError(w, 401, "member registration required")
				return
			}
		}
		p, err := registerMember(db, access, request, parent)
		if err != nil {
			apiError(w, 403, err.Error())
			return
		}
		writeJSON(w, 200, p)
	}
}

func ApproveMember(db *sql.DB, code, name string, channels []string, write bool, profile ...string) error {
	if len(channels) == 0 {
		return fmt.Errorf("approval needs explicit --channel grants")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var requestID, profileID string
	err = tx.QueryRow("SELECT a.request_id,o.principal_id FROM oauth_request_approvals a JOIN oauth_requests o ON o.id=a.request_id WHERE a.approval_code=?", strings.ToUpper(code)).Scan(&requestID, &profileID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var p *Principal
	if requestID != "" {
		p, err = findPrincipal(tx, "id=?", profileID)
	} else {
		p, err = findPrincipal(tx, "approval_code = ?", strings.ToUpper(code))
	}
	if err != nil {
		return fmt.Errorf("unknown approval code")
	}
	if len(profile) > 0 && profile[0] != "" {
		if requestID == "" {
			return fmt.Errorf("--profile requires an OAuth connection approval code")
		}
		existing, err := findPrincipal(tx, "id=?", profile[0])
		if err != nil || existing.Kind != "agent" || existing.Status != "active" {
			return fmt.Errorf("--profile must name an active agent ID")
		}
		if _, err := tx.Exec("UPDATE oauth_profiles SET principal_id=? WHERE principal_id=?", existing.ID, p.ID); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE oauth_requests SET principal_id=? WHERE id=?", existing.ID, requestID); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM principals WHERE id=? AND status='pending' AND NOT EXISTS(SELECT 1 FROM oauth_requests WHERE principal_id=?)", p.ID, p.ID); err != nil {
			return err
		}
		p = existing
	}
	if name == "" {
		name = p.Name
	}
	if name == "" {
		return fmt.Errorf("pass --name for the person/device")
	}
	if p.Kind == "agent" && p.ParentID == "" {
		if len(name) > 100 || strings.ContainsAny(name, " \t\r\n\x00@/") {
			return fmt.Errorf("agent identity must be a short name without whitespace, @ or /")
		}
		var duplicate bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM principals WHERE id<>? AND kind='agent' AND parent_id IS NULL AND status='active' AND name=?)", p.ID, name).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return fmt.Errorf("agent identity name is already registered")
		}
	}
	if p.Kind == "agent" {
		if err := cachePrincipal(tx, p); err != nil {
			return err
		}
		if err := renamePrincipal(tx, p, name); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE principals SET status='active',name=? WHERE id=?", name, p.ID); err != nil {
		return err
	}
	for _, ch := range channels {
		if ch != "*" && ch != "@*" {
			if err := validateChannel(ch); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("INSERT INTO permissions (principal_id,channel,can_read,can_write) VALUES (?,?,1,?) ON CONFLICT(principal_id,channel) DO UPDATE SET can_read=1,can_write=excluded.can_write", p.ID, ch, write); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1) ON CONFLICT(principal_id) DO UPDATE SET approved=1", p.ID); err != nil {
		return err
	}
	if requestID != "" {
		if _, err := tx.Exec("UPDATE oauth_request_approvals SET approved=1 WHERE request_id=?", requestID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func registerRemote(ctx context.Context, db *sql.DB, remote *Remote, invite string, channels []string) (*Principal, error) {
	key, err := localCredential(db, remote.ServerID, "")
	if err != nil {
		return nil, err
	}
	var p Principal
	if err := remoteRequest(ctx, *remote, "POST", "/v1/register", registrationRequest{Credential: key.Secret, Invite: invite, Channels: channels}, &p); err != nil {
		return nil, err
	}
	if _, err := db.Exec("UPDATE local_credentials SET principal_id=?,status=?,approval_code=? WHERE server_id=? AND agent_name=''", p.ID, p.Status, p.ApprovalCode, remote.ServerID); err != nil {
		return nil, err
	}
	remote.Token, remote.MemberID = key.Secret, p.ID
	return &p, nil
}

func agentRemote(ctx context.Context, db *sql.DB, remote Remote, name, channel string) (Remote, error) {
	var humanName string
	if db.QueryRow("SELECT name FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&humanName) == nil && name == humanName {
		return remote, nil
	}

	key, err := localCredential(db, remote.ServerID, name)
	if err != nil {
		return remote, err
	}
	if key.Status != "active" {
		var p Principal
		registration := registrationRequest{Credential: key.Secret, Name: name, Kind: localActorKind(db, name), Channels: []string{channel}}
		err := remoteRequest(ctx, remote, "POST", "/v1/agents", registration, &p)
		var failure *remoteHTTPError
		if registration.Kind == "human" && errors.As(err, &failure) && failure.Status == 400 {
			// Older coordinators do not understand the optional actor-kind
			// field. Preserve their existing owner-scoped participant flow.
			registration.Kind = ""
			err = remoteRequest(ctx, remote, "POST", "/v1/agents", registration, &p)
		}
		if err != nil {
			return remote, err
		}
		if _, err := db.Exec("UPDATE local_credentials SET principal_id=?,status=? WHERE server_id=? AND agent_name=?", p.ID, p.Status, remote.ServerID, name); err != nil {
			return remote, err
		}
	}
	remote.Token = key.Secret
	return remote, nil
}

func localActorKind(db *sql.DB, name string) string {
	var kind string
	_ = db.QueryRow("SELECT kind FROM principals WHERE name=? AND kind='human' AND status='active'", name).Scan(&kind)
	return kind
}
