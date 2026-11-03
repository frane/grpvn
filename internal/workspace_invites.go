package internal

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func CreateWorkspaceInvitation(db *sql.DB, access *channelAccess, p *Principal, channel string, write bool) (string, error) {
	if p != nil && p.scopes != nil && (!p.scopes["grpvn.read"] || !p.scopes["grpvn.write"]) {
		return "", fmt.Errorf("inviting collaborators requires grpvn.read and grpvn.write scopes")
	}
	if err := validateChannel(channel); err != nil {
		return "", err
	}
	if p == nil || !trustedParticipant(db, p) || !rpcAllowed(db, access, p, channel, true) {
		return "", fmt.Errorf("channel membership with write access is required")
	}
	tx, err := beginWrite(db)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	channels := []string{channel}
	if principalPermission(tx, p, "@*", true) {
		channels = append(channels, "@*")
	}
	id, secret, err := CreateInvite(tx, "Invited by "+principalAddress(p), channels, write, 24*time.Hour, 1)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec("INSERT INTO invite_issuers(invite_id,principal_id) VALUES(?,?)", id, p.ID); err != nil {
		return "", err
	}
	return secret, tx.Commit()
}

func validateInvitationIssuer(db dbtx, access *channelAccess, id string, channels []string, write bool) error {
	var issuerID string
	err := db.QueryRow("SELECT principal_id FROM invite_issuers WHERE invite_id=?", id).Scan(&issuerID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if issuerID != "" {
		issuer, err := findPrincipal(db, "id=?", issuerID)
		if err != nil {
			return fmt.Errorf("invitation issuer is no longer a member")
		}
		if err := cachePrincipal(db, issuer); err != nil {
			return err
		}
		if !trustedParticipant(db, issuer) {
			return fmt.Errorf("invitation issuer is no longer a member")
		}
		for _, channel := range channels {
			allowed := principalPermission(db, issuer, channel, write)
			if strings.HasPrefix(channel, "#") && access.isServed(channel) && access.isOpenWith(db, channel) {
				allowed = true
			}
			if !allowed {
				return fmt.Errorf("invitation issuer no longer has access to %s", channel)
			}
		}
	}
	return nil
}

func redeemOAuthInvitation(db dbtx, access *channelAccess, profile, secret string) error {
	var id, encoded string
	var write, revoked bool
	var expires int64
	var uses int
	if err := db.QueryRow("SELECT id,channels,can_write,expires_at,uses_left,revoked FROM invites WHERE secret_hash=?", credentialHash(secret)).Scan(&id, &encoded, &write, &expires, &uses, &revoked); err != nil || revoked || uses < 1 || expires <= time.Now().UnixMilli() {
		return fmt.Errorf("invitation expired, revoked or already used; ask a channel member for a new invitation")
	}
	var channels []string
	if err := json.Unmarshal([]byte(encoded), &channels); err != nil {
		return err
	}
	if err := validateInvitationIssuer(db, access, id, channels, write); err != nil {
		return err
	}
	p, err := findPrincipal(db, "id=?", profile)
	if err != nil || p.Status == "revoked" {
		return fmt.Errorf("profile cannot accept this invitation")
	}
	approvalOwner := p.ID
	if p.ParentID != "" {
		parent, err := findPrincipal(db, "id=?", p.ParentID)
		if err != nil || parent.Status != "active" {
			return fmt.Errorf("agent owner is inactive")
		}
		approvalOwner = p.ParentID
	}
	for _, channel := range channels {
		if err := channelGrant(db, p, channel, write); err != nil {
			return err
		}
	}
	if _, err := db.Exec("UPDATE invites SET uses_left=uses_left-1 WHERE id=? AND uses_left>0", id); err != nil {
		return err
	}
	if _, err := db.Exec("UPDATE principals SET status='active' WHERE id=?", p.ID); err != nil {
		return err
	}
	if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1) ON CONFLICT(principal_id) DO UPDATE SET approved=1", approvalOwner); err != nil {
		return err
	}
	return nil
}

func mountBrowserInvitations(mux *http.ServeMux, db *sql.DB, base string) {
	mux.HandleFunc("GET /join", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		secret := r.URL.Query().Get("invite")
		var valid bool
		err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM invites WHERE secret_hash=? AND revoked=0 AND uses_left>0 AND expires_at>?)", credentialHash(secret), time.Now().UnixMilli()).Scan(&valid)
		if err != nil || !valid {
			http.Error(w, "This invitation is invalid, expired or already used. Ask a channel member for another link.", http.StatusGone)
			return
		}
		origin, _ := url.Parse(base)
		http.SetCookie(w, &http.Cookie{Name: "grpvn_invite", Value: secret, Path: "/oauth", Secure: origin.Scheme == "https", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
		http.Redirect(w, r, "/llm.txt#invited", http.StatusSeeOther)
	})
}

func JoinWorkspaceChannel(db *sql.DB, access *channelAccess, p *Principal, channel, invitation string) error {
	if p == nil || p.Status != "active" {
		return fmt.Errorf("registered agent required")
	}
	if p.scopes != nil && !p.scopes["grpvn.write"] {
		return fmt.Errorf("credential does not grant write scope")
	}
	if invitation != "" {
		secret := strings.TrimSpace(invitation)
		if link, err := url.Parse(secret); err == nil && link.Host != "" {
			secret = link.Query().Get("invite")
			if secret == "" {
				secret = strings.TrimPrefix(link.Fragment, "invite=")
			}
		}
		tx, err := beginWrite(db)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := redeemOAuthInvitation(tx, access, p.ID, secret); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return cachePrincipal(db, p)
	}
	if err := validateChannel(channel); err != nil {
		return err
	}
	if !rpcAllowed(db, access, p, channel, false) {
		return fmt.Errorf("ask a channel member to invite you, use an invitation link, or create a new channel")
	}
	if access.isOpen(channel) {
		return GrantWorkspaceChannels(db, p.ID, []string{channel}, true)
	}
	return nil
}
