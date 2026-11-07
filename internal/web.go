package internal

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	webassets "github.com/frane/grpvn/ui"
)

const webSessionCookie = "grpvn_human"

type webServer struct {
	db         *sql.DB
	access     *channelAccess
	base       string
	local      bool
	cookieName string
	mu         sync.Mutex
	attempts   map[string][]time.Time
}

func isChatParticipant(p *Principal) bool {
	return p != nil && (p.Kind == "agent" || p.Kind == "human")
}

func (s *webServer) principal(r *http.Request) *Principal {
	c, err := r.Cookie(s.sessionCookie())
	if err != nil {
		return nil
	}
	var id string
	if s.db.QueryRow("SELECT principal_id FROM web_sessions WHERE secret_hash=? AND expires_at>?", credentialHash(c.Value), time.Now().UnixMilli()).Scan(&id) != nil {
		return nil
	}
	p, err := findPrincipal(s.db, "id=?", id)
	if err != nil || p.Kind != "human" || p.Status != "active" {
		return nil
	}
	if cachePrincipal(s.db, p) != nil {
		return nil
	}
	return p
}
func (s *webServer) actor(r *http.Request) *Principal {
	p := s.principal(r)
	if p == nil && r.Header.Get("Authorization") != "" {
		p, _ = authenticateMember(s.db, r)
	}
	return p
}
func (s *webServer) cookie(w http.ResponseWriter, secret string, age int) {
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookie(), Value: secret, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(s.base, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (s *webServer) sessionCookie() string {
	if s.cookieName != "" {
		return s.cookieName
	}
	return webSessionCookie
}
func (s *webServer) session(w http.ResponseWriter, id string) error {
	secret, err := newCredential()
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO web_sessions(secret_hash,principal_id,expires_at) VALUES(?,?,?)", credentialHash(secret), id, time.Now().Add(30*24*time.Hour).UnixMilli())
	if err == nil {
		s.cookie(w, secret, 30*24*3600)
	}
	return err
}
func humanPassword(password string, salt []byte) []byte {
	key, _ := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	return key
}
func humanName(name string) bool {
	return name != "" && len(name) <= 60 && !strings.ContainsAny(name, " \t\r\n\x00@/~#")
}
func inviteSecret(text string) string {
	text = strings.TrimSpace(text)
	u, err := url.Parse(text)
	if err == nil && u.Host != "" {
		if v := u.Query().Get("invite"); v != "" {
			return v
		}
		return strings.TrimPrefix(u.Fragment, "invite=")
	}
	return text
}
func webDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes*2+4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(&struct{}{}) != io.EOF {
		apiError(w, 400, "Invalid request")
		return false
	}
	return true
}
func (s *webServer) can(p *Principal, target string, write bool) bool {
	if p == nil {
		return false
	}
	if write && channelArchived(s.db, target) {
		return false
	}
	if s.local {
		if alias, native, ok := splitRemoteTarget(target); ok {
			var read, post bool
			if err := s.db.QueryRow("SELECT can_read,can_write FROM remote_channel_details WHERE remote_name=? AND channel=?", alias, native).Scan(&read, &post); err == nil {
				return read && (!write || post) && principalPermission(s.db, p, target, write)
			}
		}
	}
	if strings.HasPrefix(target, "@") && p.Kind == "human" {
		return principalPermission(s.db, p, "@*", write) || principalPermission(s.db, p, "*", write)
	}
	if s.local && principalPermission(s.db, p, "*", write) {
		return true
	}
	return rpcAllowed(s.db, s.access, p, target, write)
}
func (s *webServer) limited(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var recent []time.Time
	for _, t := range s.attempts[key] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(s.attempts) > 10000 {
		s.attempts = map[string][]time.Time{}
	}
	s.attempts[key] = append(recent, now)
	return len(recent) >= 10
}

func mountWeb(mux *http.ServeMux, db *sql.DB, access *channelAccess, base string, local bool) {
	s := &webServer{db: db, access: access, base: strings.TrimRight(base, "/"), local: local, attempts: map[string][]time.Time{}}
	var serverID string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&serverID) == nil {
		s.cookieName = webSessionCookie + "_" + credentialHash(serverID)[:12]
	}
	mux.Handle("GET /{$}", webassets.Handler())
	mux.Handle("GET /assets/", webassets.Handler())
	mux.Handle("GET /favicon.svg", webassets.Handler())
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != "GET" && r.Header.Get("Authorization") == "" {
			origin := s.base
			if origin == "" {
				origin = "http://" + r.Host
			}
			if r.Header.Get("Origin") != origin {
				apiError(w, 403, "Open grpvn in its own window to continue")
				return
			}
		}
		p := s.actor(r)
		switch r.URL.Path {
		case "/oauth/callback":
			if local && r.Method == "GET" {
				s.workspaceCallback(w, r)
				return
			}
		case "/v1/auth/session":
			if r.Method == "GET" {
				if local && r.URL.Query().Get("workspace") != "" {
					remote, err := s.workspaceAccount(r.Context(), p, r.URL.Query().Get("workspace"))
					if err != nil {
						apiError(w, 403, err.Error())
						return
					}
					var result map[string]any
					if err = remoteRequest(r.Context(), remote, "GET", "/v1/auth/session", nil, &result); err != nil {
						apiError(w, 503, "Workspace is unavailable")
						return
					}
					writeJSON(w, 200, result)
					return
				}
				if p == nil {
					writeJSON(w, 200, map[string]any{"authenticated": false, "local": local})
					return
				}
				writeJSON(w, 200, map[string]any{"authenticated": true, "profile": map[string]any{"id": p.ID, "name": principalAddress(p), "kind": p.Kind, "admin": principalPermission(db, p, "*", true)}, "local": local})
				return
			}
		case "/v1/auth/login":
			if r.Method == "POST" {
				s.login(w, r)
				return
			}
		case "/v1/accounts":
			if r.Method == "POST" {
				s.signup(w, r)
				return
			}
		case "/v1/auth/local-session":
			if r.Method == "POST" && local {
				s.bootstrap(w, r)
				return
			}
		case "/v1/auth/logout":
			if r.Method == "POST" {
				if c, err := r.Cookie(s.sessionCookie()); err == nil {
					db.Exec("DELETE FROM web_sessions WHERE secret_hash=?", credentialHash(c.Value))
				}
				s.cookie(w, "", -1)
				writeJSON(w, 200, map[string]bool{"ok": true})
				return
			}
		}
		if p == nil {
			apiError(w, 401, "Sign in to continue")
			return
		}
		if p.scopes != nil {
			scope := "grpvn.read"
			if r.Method != "GET" && r.URL.Path != "/v1/chat/read-markers" && r.URL.Path != "/v1/chat/preferences" {
				scope = "grpvn.write"
			}
			if !p.scopes[scope] {
				apiError(w, 403, "Credential does not grant "+scope)
				return
			}
		}
		s.handle(w, r, p)
		if r.Method != "GET" && r.URL.Path != "/v1/chat/read-markers" {
			_ = TouchChatMetadata(db)
		}
	})
	for _, prefix := range []string{"/v1/auth/", "/v1/accounts", "/v1/chat/", "/v1/members", "/v1/members/", "/v1/access-requests", "/v1/access-approvals", "/v1/workspaces", "/v1/workspace-connections", "/v1/workspace-connections/"} {
		mux.Handle(prefix, handler)
	}
	if local {
		mux.Handle("GET /oauth/callback", handler)
	}
}

func (s *webServer) login(w http.ResponseWriter, r *http.Request) {
	var a struct{ Name, Password string }
	if !webDecode(w, r, &a) {
		return
	}
	if s.limited("login:" + strings.ToLower(a.Name)) {
		w.Header().Set("Retry-After", "60")
		apiError(w, 429, "Too many attempts. Try again in a minute.")
		return
	}
	var id, salt, hash string
	err := s.db.QueryRow("SELECT principal_id,salt,password_hash FROM web_accounts WHERE login=?", a.Name).Scan(&id, &salt, &hash)
	decoded, _ := base64.RawStdEncoding.DecodeString(salt)
	if len(decoded) == 0 {
		decoded = make([]byte, 32)
	}
	actual := base64.RawStdEncoding.EncodeToString(humanPassword(a.Password, decoded))
	p, findErr := findPrincipal(s.db, "id=?", id)
	if err != nil || findErr != nil || p.Status != "active" || subtle.ConstantTimeCompare([]byte(hash), []byte(actual)) != 1 {
		apiError(w, 401, "Name or password is incorrect")
		return
	}
	if s.session(w, id) != nil {
		apiError(w, 500, "Could not start session")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *webServer) signup(w http.ResponseWriter, r *http.Request) {
	var a struct{ Name, Password, Invitation string }
	if !webDecode(w, r, &a) {
		return
	}
	if !humanName(a.Name) || len(a.Password) < 12 || len(a.Password) > 1024 {
		apiError(w, 400, "Choose a handle and a password of 12–1024 characters")
		return
	}
	if s.limited("signup:" + r.RemoteAddr) {
		apiError(w, 429, "Try again in a minute")
		return
	}
	if a.Invitation == "" {
		if c, err := r.Cookie("grpvn_invite"); err == nil {
			a.Invitation = c.Value
		}
	}
	secret := inviteSecret(a.Invitation)
	var valid bool
	if s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM invites WHERE secret_hash=? AND revoked=0 AND uses_left>0 AND expires_at>?)", credentialHash(secret), time.Now().UnixMilli()).Scan(&valid) != nil || !valid {
		apiError(w, 403, "This invitation expired or was already used. Ask a member for another link.")
		return
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		apiError(w, 500, "Could not create account")
		return
	}
	hash := humanPassword(a.Password, salt)
	tx, err := beginWrite(s.db)
	if err != nil {
		apiError(w, 500, "Could not create account")
		return
	}
	defer tx.Rollback()
	var taken bool
	if tx.QueryRow("SELECT EXISTS(SELECT 1 FROM principals WHERE name=? AND parent_id IS NULL) OR EXISTS(SELECT 1 FROM principal_aliases WHERE address=?)", a.Name, a.Name).Scan(&taken) != nil || taken {
		apiError(w, 409, "That handle is already in use")
		return
	}
	credential, err := newCredential()
	if err != nil {
		apiError(w, 500, "Could not create account")
		return
	}
	id := NewMessage("", "", nil).ID
	_, err = tx.Exec("INSERT INTO principals(id,credential_hash,name,kind,status,approval_code,created_at) VALUES(?,?,?,'human','pending',?,?)", id, credentialHash(credential), a.Name, id, time.Now().UnixMilli())
	if err != nil {
		apiError(w, 500, "Could not create account")
		return
	}
	if err = redeemOAuthInvitation(tx, s.access, id, secret); err != nil {
		apiError(w, 403, err.Error())
		return
	}
	if _, err = tx.Exec("INSERT INTO principal_aliases(address,principal_id) VALUES(?,?)", a.Name, id); err == nil {
		_, err = tx.Exec("INSERT INTO web_accounts(principal_id,login,salt,password_hash) VALUES(?,?,?,?)", id, a.Name, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
	}
	if err != nil || tx.Commit() != nil {
		apiError(w, 500, "Could not create account")
		return
	}
	if s.session(w, id) != nil {
		apiError(w, 500, "Could not start session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "grpvn_invite", Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(s.base, "https://"), SameSite: http.SameSiteLaxMode})
	writeJSON(w, 201, map[string]bool{"ok": true})
}
func (s *webServer) bootstrap(w http.ResponseWriter, r *http.Request) {
	var a struct{ Ticket string }
	if !webDecode(w, r, &a) {
		return
	}
	tx, err := beginWrite(s.db)
	if err != nil {
		apiError(w, 500, "Could not start session")
		return
	}
	defer tx.Rollback()
	var id string
	if tx.QueryRow("DELETE FROM web_tickets WHERE secret_hash=? AND expires_at>? RETURNING principal_id", credentialHash(a.Ticket), time.Now().UnixMilli()).Scan(&id) != nil {
		apiError(w, 401, "This local sign-in expired. Reopen the grpvn app.")
		return
	}
	if tx.Commit() != nil || s.session(w, id) != nil {
		apiError(w, 500, "Could not start session")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// LocalHumanTicket is reachable only through the trusted local CLI/desktop.
// It gives a human its own durable identity, never reuses an agent's profile.
func LocalHumanTicket(db *sql.DB, name string) (string, error) {
	if !humanName(name) {
		name = "local-user"
	}
	tx, err := beginWrite(db)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRow("SELECT value FROM server_meta WHERE key='local_human_id'").Scan(&id)
	if err == sql.ErrNoRows {
		id = NewMessage("", "", nil).ID
		var taken bool
		_ = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM principals WHERE name=?) OR EXISTS(SELECT 1 FROM principal_aliases WHERE address=?)", name, name).Scan(&taken)
		if taken {
			name, err = generatedPrincipalName(tx)
			if err != nil {
				return "", err
			}
		}
		secret, secretErr := newCredential()
		if secretErr != nil {
			return "", secretErr
		}
		_, err = tx.Exec("INSERT INTO principals(id,credential_hash,name,kind,status,approval_code,created_at) VALUES(?,?,?,'human','active',?,?)", id, credentialHash(secret), name, id, time.Now().UnixMilli())
		if err == nil {
			_, err = tx.Exec("INSERT INTO server_meta(key,value) VALUES('local_human_id',?)", id)
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO permissions(principal_id,channel,can_read,can_write) VALUES(?,'*',1,1)", id)
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1)", id)
		}
		if err == nil {
			_, err = tx.Exec("INSERT INTO principal_aliases(address,principal_id) VALUES(?,?)", name, id)
		}
	}
	if err != nil {
		return "", err
	}
	ticket, err := newCredential()
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec("INSERT INTO web_tickets(secret_hash,principal_id,expires_at) VALUES(?,?,?)", credentialHash(ticket), id, time.Now().Add(5*time.Minute).UnixMilli()); err != nil {
		return "", err
	}
	return ticket, tx.Commit()
}

func (s *webServer) handle(w http.ResponseWriter, r *http.Request, p *Principal) {
	if s.manage(w, r, p) {
		return
	}
	if s.local && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/workspace-connections/") {
		s.workspaceStatus(w, r, p)
		return
	}
	switch r.URL.Path {
	case "/v1/chat/channels":
		if r.Method == "GET" {
			s.channels(w, r, p)
			return
		}

		if r.Method == "POST" {
			var a struct {
				Channel string
				Open    bool
			}
			if !webDecode(w, r, &a) {
				return
			}
			if remote, native, ok, err := s.remoteChannel(r.Context(), p, a.Channel); ok {
				if err != nil {
					apiError(w, 403, err.Error())
					return
				}
				result, err := remoteChatTool(r.Context(), remote, "create_channel", map[string]any{"channel": native, "open": a.Open})
				if err != nil {
					apiError(w, 400, err.Error())
					return
				}
				if err = JoinRemoteTargets(s.db, a.Channel); err != nil {
					apiError(w, 500, err.Error())
					return
				}
				writeJSON(w, 201, result)
				return
			}
			v, err := CreateWorkspaceChannel(s.db, s.access, p, a.Channel, a.Open)
			if err != nil {
				apiError(w, 403, err.Error())
				return
			}
			writeJSON(w, 201, v)
			return
		}
	case "/v1/chat/messages":
		if r.Method == "GET" {
			s.messages(w, r, p)
			return
		}
		if r.Method == "POST" {
			s.send(w, r, p)
			return
		}
	case "/v1/chat/events":
		if r.Method == "GET" {
			s.events(w, r, p)
			return
		}
	case "/v1/chat/preferences":
		s.preferences(w, r, p)
		return
	case "/v1/chat/read-markers":
		if r.Method == "POST" {
			s.readMarker(w, r, p)
			return
		}

	case "/v1/chat/invitations":
		if r.Method == "POST" {
			var a struct {
				Channel, Agent, Recipient string
				Write                     *bool
			}
			if !webDecode(w, r, &a) {
				return
			}
			recipient := a.Recipient
			if recipient == "" {
				recipient = a.Agent
			}
			normalized, email, err := invitationRecipient(recipient)
			if err != nil {
				apiError(w, 400, err.Error())
				return
			}
			write := a.Write == nil || *a.Write
			if remote, native, ok, err := s.remoteChannel(r.Context(), p, a.Channel); ok {
				if err != nil {
					apiError(w, 403, err.Error())
					return
				}
				result, err := remoteChatTool(r.Context(), remote, "invite", map[string]any{"channel": native, "agent": normalized, "write": write})
				if err != nil && strings.Contains(strings.ToLower(err.Error()), "agent not found") {
					result, err = remoteChatTool(r.Context(), remote, "invite", map[string]any{"channel": native, "write": write})
				}
				if err != nil {
					apiError(w, 400, err.Error())
					return
				}
				writeJSON(w, 201, result)
				return
			}
			if s.local && normalized != "" {
				if email != "" {
					apiError(w, 400, "Choose a remote workspace to invite someone on another device")
					return
				}
				if _, err := participantByAddress(s.db, normalized); err != nil {
					if !write {
						apiError(w, 400, "Use Manage members to edit local posting access")
						return
					}
					if err = s.inviteLocalAgent(p, a.Channel, normalized); err != nil {
						apiError(w, 403, err.Error())
					} else {
						writeJSON(w, 200, map[string]string{"kind": "member", "name": strings.TrimPrefix(normalized, "@"), "status": "joined"})
					}
					return
				}
			}
			base := s.base
			if base == "" {
				base = "http://" + r.Host
			}
			result, err := inviteWorkspaceRecipient(s.db, s.access, p, a.Channel, normalized, write, base)
			if err != nil {
				apiError(w, 403, err.Error())
				return
			}
			status := 200
			if result.Kind == "invitation" {
				status = 201
			}
			writeJSON(w, status, result)
			return
		}

	case "/v1/chat/participants":
		if r.Method == "GET" {
			s.people(w, r, p)
			return
		}
	case "/v1/workspaces":
		if !s.local || !principalPermission(s.db, p, "*", true) {
			apiError(w, 403, "Source management is available in your local app")
			return
		}

		if r.Method == "GET" {
			remotes, err := ListRemotes(s.db)
			if err != nil {
				apiError(w, 500, "Could not list sources")
				return
			}
			out := []map[string]any{}
			for _, remote := range remotes {
				reg, _ := RemoteRegistration(s.db, remote.Name)
				out = append(out, map[string]any{"name": remote.Name, "url": remote.URL, "status": reg.Status, "error": remote.LastError, "last_sync": remote.LastSync})
			}
			writeJSON(w, 200, map[string]any{"sources": out})
			return
		}
	case "/v1/workspace-connections":
		if s.local && r.Method == "POST" {
			s.startWorkspace(w, r, p)
			return
		}
		if s.local && r.Method == "GET" {
			s.workspaceStatus(w, r, p)
			return
		}

	case "/v1/access-requests", "/v1/access-approvals":
		if principalPermission(s.db, p, "*", true) {
			s.approvals(w, r)
			return
		}
	}
	apiError(w, 404, "Not found")
}

func (s *webServer) channels(w http.ResponseWriter, r *http.Request, p *Principal) {
	query := `SELECT target FROM messages WHERE target LIKE '#%' UNION SELECT channel FROM workspace_channels UNION SELECT channel FROM permissions WHERE principal_id=? AND channel LIKE '#%'`
	if s.local {
		query += " UNION SELECT target FROM remote_channels"
	}
	query += " ORDER BY 1"
	rows, err := s.db.Query(query, p.ID)
	if err != nil {
		apiError(w, 500, "Could not list channels")
		return
	}
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			names = append(names, n)
		}
	}
	rows.Close()
	positions, err := loadCursors(s.db, p.ID)
	if err != nil {
		apiError(w, 500, "Could not read conversation state")
		return
	}
	preferences, err := loadConversationSettings(s.db, p.ID)
	if err != nil {
		apiError(w, 500, "Could not read conversation settings")
		return
	}
	out := []map[string]any{}
	for _, n := range names {
		if !s.can(p, n, false) {
			continue
		}
		archived := channelArchived(s.db, n)
		joined := channelJoined(s.db, p.ID, n)
		if r.URL.Query().Get("browse") != "1" && (archived || !joined) {
			continue
		}
		var unread, latest int64
		settings := preferences[n]
		position, held := positions[n], settings.Held.Valid
		if held {
			position = settings.Held.Int64
		}

		source := "workspace"
		sourceID := ""
		account := ""
		native := n
		if s.local {
			var remote string
			if s.db.QueryRow("SELECT remote_name,channel FROM remote_channels WHERE target=?", n).Scan(&remote, &native) == nil {
				source = remote
				sourceID = remote
				var address string
				if s.db.QueryRow("SELECT url FROM remotes WHERE name=?", remote).Scan(&address) == nil {
					if u, err := url.Parse(address); err == nil {
						source = u.Host
					}
				}
				_ = s.db.QueryRow("SELECT h.name FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE r.name=? AND h.local_principal_id=?", remote, p.ID).Scan(&account)
			} else {
				source = "local"
			}
		}
		open, manage := s.access.isOpen(n), canManageChannel(s.db, p, n)
		if sourceID != "" {
			_ = s.db.QueryRow("SELECT is_open,can_manage FROM remote_channel_details WHERE remote_name=? AND channel=?", sourceID, native).Scan(&open, &manage)
		}
		self := principalAddress(p)
		if account != "" {
			self = account
		}
		_ = s.db.QueryRow("SELECT COUNT(*),COALESCE(MAX(seq),0) FROM messages m WHERE target=? AND seq>? AND (sender<>? OR ?=1)", n, position, self, held).Scan(&unread, &latest)
		out = append(out, map[string]any{"muted": settings.Muted, "read_position": position, "read_held": held, "name": n, "label": native, "source": source, "source_id": sourceID, "account_name": account, "unread": unread, "latest": latest, "write": s.can(p, n, true), "open": open, "joined": joined, "archived": archived, "manage": manage})
	}
	writeJSON(w, 200, map[string]any{"channels": out})
}
func (s *webServer) messages(w http.ResponseWriter, r *http.Request, p *Principal) {
	target := r.URL.Query().Get("target")
	if target == "@me" {
		target = "@" + principalAddress(p)
	}
	if !s.can(p, target, false) {
		apiError(w, 403, "Channel access denied")
		return
	}
	if s.local && r.URL.Query().Get("refresh") == "1" {
		if err := SyncTarget(r.Context(), s.db, target); err != nil {
			var cached bool
			_ = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE target=?)", target).Scan(&cached)
			if !cached {
				apiError(w, 503, "History is unavailable while this workspace is offline. Try again shortly.")
				return
			}
		}
	}
	before := int64(0)
	if raw := r.URL.Query().Get("before"); raw != "" {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before < 0 {
			apiError(w, 400, "Invalid position")
			return
		}
	}
	// Both sides of the human's DM conversation are visible, including aliases.
	peer := strings.TrimPrefix(target, "@")
	dm := strings.HasPrefix(target, "@")
	if dm && peer != principalAddress(p) {
		if !principalPermission(s.db, p, "@*", false) {
			apiError(w, 403, "DM access denied")
			return
		}
	}
	predicate := "m.target=?"
	args := []any{target}
	if dm && peer == principalAddress(p) {
		predicate = "m.target=?"
		args = []any{"@" + principalAddress(p)}
	} else if dm {
		predicate = `((m.sender=? AND m.target=?) OR (m.sender=? AND m.target=?))`
		args = []any{principalAddress(p), target, peer, "@" + principalAddress(p)}
	}
	if before > 0 {
		predicate += " AND m.seq<?"
		args = append(args, before)
	}
	rows, err := s.db.Query(`SELECT m.seq,m.id,m.sender,m.target,m.body,m.chain_root,m.chain_depth,m.parent_id,m.correlation,m.created_at FROM messages m WHERE `+predicate+" ORDER BY m.seq DESC LIMIT 100", args...)
	if err != nil {
		apiError(w, 500, "Could not read messages")
		return
	}
	var list []*Message
	for rows.Next() {
		m, err := scanMessage(rows, true)
		if err != nil {
			rows.Close()
			apiError(w, 500, "Could not read messages")
			return
		}
		list = append(list, m)
	}
	rows.Close()
	out := []map[string]any{}
	for i := len(list) - 1; i >= 0; i-- {
		m := list[i]
		kind := "agent"
		mine := m.Sender == principalAddress(p)
		displaySender := m.Sender
		var remoteSelf string
		if s.db.QueryRow("SELECT h.name FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id JOIN remote_channels c ON c.remote_name=r.name WHERE c.target=? AND h.local_principal_id=?", m.Target, p.ID).Scan(&remoteSelf) == nil && m.Sender == remoteSelf {
			kind = "human"
			mine = true
		}
		_ = s.db.QueryRow("SELECT p.kind FROM principals p JOIN message_principals mp ON mp.principal_id=p.id WHERE mp.message_id=?", m.ID).Scan(&kind)
		var remoteAccount string
		if s.db.QueryRow(`SELECT h.name FROM remote_attributions a JOIN remote_messages m ON m.local_id=a.local_id JOIN remotes r ON r.name=m.remote_name JOIN remote_human_sessions h ON h.server_id=r.server_id AND h.principal_id=a.principal_id WHERE a.local_id=? AND h.local_principal_id=?`, m.ID, p.ID).Scan(&remoteAccount) == nil {
			mine = true
			kind = "human"
			displaySender = remoteAccount
		}
		out = append(out, map[string]any{"seq": m.Seq, "id": m.ID, "sender": displaySender, "target": m.Target, "body": string(m.Body), "chain_root": m.ChainRoot, "parent_id": m.ParentID, "created_at": m.CreatedAt, "kind": kind, "mine": mine, "pending": m.Pending})
	}
	position, held := conversationReadPosition(s.db, p.ID, target)
	writeJSON(w, 200, map[string]any{"messages": out, "more": len(list) == 100, "read_position": position, "read_held": held})
}
func (s *webServer) send(w http.ResponseWriter, r *http.Request, p *Principal) {
	if !isChatParticipant(p) {
		apiError(w, 401, "An agent or human profile is required to post")
		return
	}
	var a struct{ Target, Body, IdempotencyKey string }
	if !webDecode(w, r, &a) {
		return
	}
	target, parent, err := ResolveTarget(s.db, a.Target, "")
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if parent != nil && strings.HasPrefix(target, "@") && !principalOwnsAddress(p, parent.Sender) {
		if !principalOwnsAddress(p, strings.TrimPrefix(target, "@")) {
			apiError(w, 403, "Private conversation")
			return
		}
		target = "@" + parent.Sender
	}
	if !s.can(p, target, true) && !ensureSendChannel(s.db, s.access, p, target) {
		apiError(w, 403, "You need write access to this channel")
		return
	}
	var m *Message
	var replay bool
	if s.local {
		if err := JoinRemoteTargets(s.db, target); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		var cached bool
		_ = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM remote_channels WHERE target=?)", target).Scan(&cached)
		if cached {
			sender := principalAddress(p)
			var remoteName string
			if s.db.QueryRow("SELECT remote_name FROM remote_channels WHERE target=?", target).Scan(&remoteName) == nil {
				var name string
				if s.db.QueryRow("SELECT h.name FROM remote_human_sessions h JOIN remotes r ON r.server_id=h.server_id WHERE r.name=? AND h.local_principal_id=?", remoteName, p.ID).Scan(&name) == nil {
					sender = name
				}
			}
			m, replay, err = SendSynced(r.Context(), s.db, sender, a.Target, a.Body, "", false, a.IdempotencyKey)
		} else {
			m, replay, err = sendRegistered(s.db, p, a.Target, a.Body, a.IdempotencyKey)
		}
	} else {
		m, replay, err = sendRegistered(s.db, p, a.Target, a.Body, a.IdempotencyKey)
	}
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"id": m.ID, "replayed": replay, "pending": m.Pending})
}
func (s *webServer) people(w http.ResponseWriter, r *http.Request, viewer *Principal) {
	requested := r.URL.Query().Get("name")
	query := "SELECT id FROM principals WHERE kind IN ('agent','human') AND status IN ('active','pending') ORDER BY name LIMIT 200"
	var args []any
	if requested != "" {
		query = "SELECT id FROM principals WHERE kind IN ('agent','human') AND (name=? OR id IN (SELECT principal_id FROM principal_aliases WHERE address=?)) ORDER BY name LIMIT 200"
		args = []any{requested, requested}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		apiError(w, 500, "Could not list people")
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
	people := []map[string]any{}
	seen := map[string]bool{}
	for _, id := range ids {
		p, err := findPrincipal(s.db, "id=?", id)
		if err != nil || cachePrincipal(s.db, p) != nil {
			continue
		}
		name := principalAddress(p)
		seen[name] = true
		people = append(people, map[string]any{"id": p.ID, "name": name, "kind": p.Kind, "status": p.Status})
	}
	if s.local {
		query := "SELECT DISTINCT sender FROM messages m WHERE NOT EXISTS(SELECT 1 FROM remote_messages rm WHERE rm.local_id=m.id) AND NOT EXISTS(SELECT 1 FROM local_participants lp WHERE lp.name=m.sender AND lp.enabled=0) UNION SELECT agent_name FROM agent_sessions a WHERE NOT EXISTS(SELECT 1 FROM local_participants lp WHERE lp.name=a.agent_name AND lp.enabled=0) ORDER BY 1 LIMIT 200"
		var args []any
		if requested != "" {
			query = "SELECT ? WHERE EXISTS(SELECT 1 FROM messages m WHERE sender=? AND NOT EXISTS(SELECT 1 FROM remote_messages rm WHERE rm.local_id=m.id)) OR EXISTS(SELECT 1 FROM agent_sessions WHERE agent_name=?) OR EXISTS(SELECT 1 FROM local_participants WHERE name=?)"
			args = []any{requested, requested, requested, requested}
		}
		rows, err := s.db.Query(query, args...)
		if err == nil {
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil && !seen[name] {
					people = append(people, map[string]any{"id": "local:" + name, "name": name, "kind": "agent", "status": "active"})
					seen[name] = true
				}
			}
			rows.Close()
		}
	}
	if name := r.URL.Query().Get("name"); name != "" {
		selected := []map[string]any{}
		for _, person := range people {
			if person["name"] != name {
				continue
			}
			if s.local {
				var runtime string
				var seen int64
				if s.db.QueryRow("SELECT runtime,heartbeat_at FROM agent_sessions WHERE agent_name=? ORDER BY heartbeat_at DESC LIMIT 1", name).Scan(&runtime, &seen) == nil {
					person["runtime"] = runtime
					person["last_seen"] = seen
				}
			}
			if channel := r.URL.Query().Get("channel"); channel != "" && s.can(viewer, channel, false) {
				var last int64
				if s.db.QueryRow("SELECT COALESCE(MAX(created_at),0) FROM messages WHERE sender=? AND target=?", name, channel).Scan(&last) == nil && last > 0 {
					person["last_message"] = last
				}
			}
			selected = append(selected, person)
		}
		people = selected
	}
	muted := map[string]bool{}
	if rows, err := s.db.Query("SELECT target,muted FROM conversation_preferences WHERE principal_id=? AND target LIKE '@%'", viewer.ID); err == nil {
		for rows.Next() {
			var target string
			var mute bool
			if rows.Scan(&target, &mute) == nil {
				muted[target] = mute
			}
		}
		rows.Close()
	}
	counts := map[string]int64{}
	if rows, err := s.db.Query(`SELECT q.peer,COUNT(*) FROM (SELECT CASE WHEN sender=? THEN target ELSE '@'||sender END AS peer,seq,sender FROM messages WHERE target LIKE '@%' AND (sender=? OR target=?)) q LEFT JOIN cursors c ON c.agent_name=? AND c.target=q.peer LEFT JOIN conversation_preferences f ON f.principal_id=? AND f.target=q.peer WHERE q.seq>COALESCE(f.held_position,c.position,0) AND (q.sender<>? OR f.held_position IS NOT NULL) GROUP BY q.peer`, principalAddress(viewer), principalAddress(viewer), "@"+principalAddress(viewer), viewer.ID, viewer.ID, principalAddress(viewer)); err == nil {
		for rows.Next() {
			var peer string
			var count int64
			if rows.Scan(&peer, &count) == nil {
				counts[peer] = count
			}
		}
		rows.Close()
	}
	for _, person := range people {
		target := "@" + person["name"].(string)
		person["muted"] = muted[target]
		person["unread"] = counts[target]
	}
	writeJSON(w, 200, map[string]any{"people": people})
}
func (s *webServer) approvals(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var a struct {
			Code     string
			Channels []string
		}
		if !webDecode(w, r, &a) {
			return
		}
		if len(a.Channels) == 0 {
			a.Channels = []string{"#general", "@*"}
		}
		if err := ApproveMember(s.db, a.Code, "", a.Channels, true); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method != "GET" {
		apiError(w, 405, "Method not allowed")
		return
	}
	rows, err := s.db.Query("SELECT id,name,approval_code,requested FROM principals WHERE status='pending' ORDER BY created_at DESC LIMIT 100")
	if err != nil {
		apiError(w, 500, "Could not list requests")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, code, requested string
		if rows.Scan(&id, &name, &code, &requested) == nil {
			var channels []string
			_ = json.Unmarshal([]byte(requested), &channels)
			out = append(out, map[string]any{"id": id, "name": name, "code": code, "channels": channels})
		}
	}
	writeJSON(w, 200, map[string]any{"requests": out})
}

// One inexpensive shared database check per second wakes every connected UI.
// Events carry no chat content; clients fetch only their authorized view.
type webFeed struct {
	mu        sync.Mutex
	listeners map[chan struct{}]bool
	cancel    context.CancelFunc
}

var webFeeds sync.Map

func subscribeWeb(db *sql.DB) (chan struct{}, func()) {
	v, _ := webFeeds.LoadOrStore(db, &webFeed{listeners: map[chan struct{}]bool{}})
	f := v.(*webFeed)
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	f.listeners[ch] = true
	if f.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		f.cancel = cancel
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			var last int64 = -1
			var lastMetadata int64 = -1
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					var seq int64
					var metadata int64
					if db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0),COALESCE((SELECT value FROM chat_metadata WHERE key='revision'),0) FROM messages").Scan(&seq, &metadata) != nil {
						continue
					}
					if seq != last || metadata != lastMetadata {
						last = seq
						lastMetadata = metadata
						f.mu.Lock()
						for c := range f.listeners {
							select {
							case c <- struct{}{}:
							default:
							}
						}
						f.mu.Unlock()
					}
				}
			}
		}()
	}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.listeners, ch)
		if len(f.listeners) == 0 && f.cancel != nil {
			f.cancel()
			f.cancel = nil
		}
		f.mu.Unlock()
	}
}
func (s *webServer) events(w http.ResponseWriter, r *http.Request, p *Principal) {
	flush, ok := w.(http.Flusher)
	if !ok {
		apiError(w, 500, "Streaming unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	controller := http.NewResponseController(w)
	push := func(text string) bool {
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_, err := fmt.Fprint(w, text)
		if err == nil {
			flush.Flush()
		}
		_ = controller.SetWriteDeadline(time.Time{})
		return err == nil
	}
	if !push("retry: 3000\nevent: connected\ndata: {}\n\n") {
		return
	}
	ch, done := subscribeWeb(s.db)
	defer done()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if s.actor(r) == nil {
				fmt.Fprint(w, "event: signed-out\ndata: {}\n\n")
				flush.Flush()
				return
			}
			if !push("event: changed\ndata: {}\n\n") {
				return
			}
		case <-heartbeat.C:
			if s.actor(r) == nil {
				return
			}
			if !push(": keep-alive\n\n") {
				return
			}
		}
	}
}

func humanAPIPath(path string) bool {
	for _, prefix := range []string{"/v1/auth/", "/v1/accounts", "/v1/chat/", "/v1/files", "/v1/members", "/v1/access-requests", "/v1/access-approvals", "/v1/workspaces", "/v1/workspace-connections"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
