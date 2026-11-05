package internal

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func mountOAuth(mux *http.ServeMux, db *sql.DB, base string, access *channelAccess) error {
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return fmt.Errorf("--public-url needs a root HTTPS origin (HTTP is supported only on loopback)")
	}
	mountOAuthWaitingPage(mux, db, base)
	mountBrowserInvitations(mux, db, base)
	resource := base + "/mcp"
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"resource": resource, "authorization_servers": []string{base}, "scopes_supported": []string{"grpvn.read", "grpvn.write"}})
	})
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register", "authorization_response_iss_parameter_supported": true, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "scopes_supported": []string{"grpvn.read", "grpvn.write"}})
	})
	mux.HandleFunc("POST /oauth/register", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
		var request struct {
			ClientName string   `json:"client_name"`
			Redirects  []string `json:"redirect_uris"`
			AuthMethod string   `json:"token_endpoint_auth_method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Redirects) == 0 || len(request.Redirects) > 10 || len(request.ClientName) > 100 || strings.ContainsAny(request.ClientName, "\r\n\x00") || (request.AuthMethod != "" && request.AuthMethod != "none") {
			oauthError(w, "invalid_client_metadata")
			return
		}
		for _, raw := range request.Redirects {
			redirect, err := url.Parse(raw)
			if err != nil || redirect.Host == "" || redirect.User != nil || redirect.Fragment != "" || (redirect.Scheme != "https" && !(redirect.Scheme == "http" && (redirect.Hostname() == "localhost" || redirect.Hostname() == "127.0.0.1"))) {
				oauthError(w, "invalid_redirect_uri")
				return
			}
		}
		id := NewMessage("", "", nil).ID
		encoded, _ := json.Marshal(request.Redirects)
		if _, err := db.Exec("INSERT INTO oauth_clients(id,name,redirects,created_at) VALUES(?,?,?,?)", id, request.ClientName, string(encoded), time.Now().UnixMilli()); err != nil {
			apiError(w, 500, "cannot register client")
			return
		}
		writeJSON(w, 201, map[string]any{"client_id": id, "client_name": request.ClientName, "redirect_uris": request.Redirects, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}})
	})
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var name, encoded string
		if err := db.QueryRow("SELECT name,redirects FROM oauth_clients WHERE id=?", q.Get("client_id")).Scan(&name, &encoded); err != nil {
			oauthError(w, "invalid_client")
			return
		}
		var redirects []string
		_ = json.Unmarshal([]byte(encoded), &redirects)
		valid := false
		for _, uri := range redirects {
			valid = valid || uri == q.Get("redirect_uri")
		}
		challenge, err := base64.RawURLEncoding.DecodeString(q.Get("code_challenge"))
		if !valid || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || err != nil || len(challenge) != 32 || len(q.Get("state")) > 2048 || (q.Get("resource") != "" && q.Get("resource") != resource) {
			oauthError(w, "invalid_request")
			return
		}
		scope := q.Get("scope")
		if scope == "" {
			scope = "grpvn.read"
		}
		for _, s := range strings.Fields(scope) {
			if s != "grpvn.read" && s != "grpvn.write" {
				oauthError(w, "invalid_scope")
				return
			}
		}
		var humanProfile *Principal
		if q.Get("actor") == "human" {
			humanProfile = browserHuman(db, r)
			if humanProfile == nil {
				http.Redirect(w, r, "/?"+url.Values{"next": {r.URL.RequestURI()}}.Encode(), http.StatusSeeOther)
				return
			}
		}
		secret, err := newCredential()
		if err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		ticket, err := newCredential()
		if err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		id := NewMessage("", "", nil).ID
		principalID := credentialHash(secret)[:32]
		code := strings.ToUpper(id[18:])
		redirect, _ := url.Parse(q.Get("redirect_uri"))
		requested, _ := json.Marshal([]string{"oauth:" + scope, "redirect:" + redirect.Host, "client:" + name})
		tx, err := beginWrite(db)
		if err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		defer tx.Rollback()
		browserSecret, browserHash, appKey, err := browserProfile(r, name, q.Get("redirect_uri"))
		if err != nil {
			apiError(w, 500, "cannot remember profile")
			return
		}
		var profile *Principal
		if humanProfile != nil {
			profile, err = findPrincipal(tx, "id=?", humanProfile.ID)
		} else {
			profile, err = existingOAuthProfile(tx, browserHash, appKey)
		}
		if err != nil {
			apiError(w, 500, "cannot find profile")
			return
		}
		agentName := ""
		alreadyActive := profile != nil && profile.Status == "active"
		if profile != nil {
			if profile.Status == "revoked" {
				oauthError(w, "access_denied")
				return
			}
			principalID, agentName = profile.ID, profile.Name
		} else {
			agentName, err = generatedPrincipalName(tx)
			if err != nil {
				apiError(w, 500, "cannot assign identity")
				return
			}
			if _, err := tx.Exec("INSERT INTO principals(id,credential_hash,name,kind,status,approval_code,requested,created_at) VALUES(?,?,?,'agent','pending',?,?,?)", principalID, credentialHash(secret), agentName, code, string(requested), time.Now().UnixMilli()); err != nil {
				apiError(w, 500, "cannot create authorization")
				return
			}
			if _, err := tx.Exec("INSERT INTO oauth_profiles(browser_hash,app_key,principal_id) VALUES(?,?,?)", browserHash, appKey, principalID); err != nil {
				apiError(w, 500, "cannot remember profile")
				return
			}
		}
		invited := false
		if invitation, err := r.Cookie("grpvn_invite"); err == nil {
			if _, err := tx.Exec("SAVEPOINT accept_invitation"); err != nil {
				apiError(w, 500, "cannot accept invitation")
				return
			}
			if err := redeemOAuthInvitation(tx, access, principalID, invitation.Value); err != nil {
				if _, rollbackErr := tx.Exec("ROLLBACK TO accept_invitation"); rollbackErr != nil {
					apiError(w, 500, "cannot accept invitation")
					return
				}
				if !alreadyActive {
					oauthError(w, "invalid_invitation")
					return
				}
			} else {
				invited = !alreadyActive
			}
			if _, err := tx.Exec("RELEASE accept_invitation"); err != nil {
				apiError(w, 500, "cannot accept invitation")
				return
			}
		}
		authCode := credentialHash("code:" + ticket)
		if _, err := tx.Exec("INSERT INTO oauth_requests(id,client_id,principal_id,ticket_hash,code_hash,redirect_uri,state,challenge,scope,resource,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, q.Get("client_id"), principalID, credentialHash(ticket), credentialHash(authCode), q.Get("redirect_uri"), q.Get("state"), q.Get("code_challenge"), scope, resource, time.Now().Add(10*time.Minute).UnixMilli()); err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		if _, err := tx.Exec("INSERT INTO oauth_request_approvals(request_id,approval_code,approved) VALUES(?,?,?)", id, code, invited); err != nil {
			apiError(w, 500, "cannot create approval")
			return
		}
		if err := tx.Commit(); err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.SetCookie(w, &http.Cookie{Name: "grpvn_profile", Value: browserSecret, Path: "/", Secure: u.Scheme == "https", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 60 * 60})
		if _, err := r.Cookie("grpvn_invite"); err == nil {
			http.SetCookie(w, &http.Cookie{Name: "grpvn_invite", Path: "/oauth", MaxAge: -1, HttpOnly: true, Secure: u.Scheme == "https", SameSite: http.SameSiteLaxMode})
			http.SetCookie(w, &http.Cookie{Name: "grpvn_invite", Value: "", Path: "/", Secure: u.Scheme == "https", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		}
		http.Redirect(w, r, oauthWaitURL(id, ticket), http.StatusSeeOther)
	})
	mux.HandleFunc("GET /oauth/ready", func(w http.ResponseWriter, r *http.Request) {
		var ticketHash, redirect, state, status string
		var expires int64
		var consumed bool
		var approved bool
		err := db.QueryRow("SELECT o.ticket_hash,o.redirect_uri,o.state,p.status,o.expires_at,o.consumed,COALESCE(a.approved,1) FROM oauth_requests o JOIN principals p ON p.id=o.principal_id LEFT JOIN oauth_request_approvals a ON a.request_id=o.id WHERE o.id=?", r.URL.Query().Get("request")).Scan(&ticketHash, &redirect, &state, &status, &expires, &consumed, &approved)
		ticket := r.URL.Query().Get("ticket")
		if err != nil || credentialHash(ticket) != ticketHash || expires <= time.Now().UnixMilli() || consumed {
			writeJSON(w, 400, map[string]string{"error": "Authorization request is invalid or expired."})
			return
		}
		if status != "revoked" && (status != "active" || !approved) {
			writeJSON(w, 200, map[string]string{"status": "pending"})
			return
		}
		target, _ := url.Parse(redirect)
		q := target.Query()
		q.Set("state", state)
		q.Set("iss", base)
		if status == "active" {
			q.Set("code", credentialHash("code:"+ticket))
		} else {
			q.Set("error", "access_denied")
		}
		target.RawQuery = q.Encode()
		writeJSON(w, 200, map[string]string{"redirect": target.String()})
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
		if r.ParseForm() != nil {
			oauthError(w, "invalid_request")
			return
		}
		f := r.PostForm
		if f.Get("resource") != "" && f.Get("resource") != resource {
			oauthError(w, "invalid_target")
			return
		}
		tx, err := beginWrite(db)
		if err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		defer tx.Rollback()
		var principalID, scope string
		switch f.Get("grant_type") {
		case "authorization_code":
			var client, redirect, challenge, target string
			var expires int64
			var consumed bool
			var approved bool
			err := tx.QueryRow("SELECT o.client_id,o.principal_id,o.redirect_uri,o.challenge,o.scope,o.resource,o.expires_at,o.consumed,COALESCE(a.approved,1) FROM oauth_requests o LEFT JOIN oauth_request_approvals a ON a.request_id=o.id WHERE o.code_hash=?", credentialHash(f.Get("code"))).Scan(&client, &principalID, &redirect, &challenge, &scope, &target, &expires, &consumed, &approved)
			verifier := f.Get("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			if err != nil || !approved || consumed || expires <= time.Now().UnixMilli() || client != f.Get("client_id") || redirect != f.Get("redirect_uri") || len(verifier) < 43 || len(verifier) > 128 || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || target != resource {
				oauthError(w, "invalid_grant")
				return
			}
			if _, err := tx.Exec("UPDATE oauth_requests SET consumed=1 WHERE code_hash=?", credentialHash(f.Get("code"))); err != nil {
				apiError(w, 500, "cannot issue token")
				return
			}
		case "refresh_token":
			var client, target string
			var expires int64
			err := tx.QueryRow("SELECT principal_id,client_id,resource,scope,expires_at FROM oauth_refresh WHERE secret_hash=?", credentialHash(f.Get("refresh_token"))).Scan(&principalID, &client, &target, &scope, &expires)
			if err != nil || expires <= time.Now().UnixMilli() || client != f.Get("client_id") || target != resource {
				oauthError(w, "invalid_grant")
				return
			}
			if _, err := tx.Exec("DELETE FROM oauth_refresh WHERE secret_hash=?", credentialHash(f.Get("refresh_token"))); err != nil {
				apiError(w, 500, "cannot refresh token")
				return
			}
		default:
			oauthError(w, "unsupported_grant_type")
			return
		}
		p, err := findPrincipal(tx, "id=?", principalID)
		if err != nil || p.Status != "active" {
			oauthError(w, "access_denied")
			return
		}
		access, err := newCredential()
		if err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		refresh, err := newCredential()
		if err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("UPDATE principals SET credential_hash=? WHERE id=?", credentialHash(access), principalID); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("INSERT INTO oauth_credentials(credential_hash,principal_id) VALUES(?,?)", credentialHash(access), principalID); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("INSERT INTO credential_expiry(credential_hash,expires_at) VALUES(?,?)", credentialHash(access), time.Now().Add(time.Hour).UnixMilli()); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("INSERT INTO oauth_refresh(secret_hash,principal_id,client_id,resource,scope,expires_at) VALUES(?,?,?,?,?,?)", credentialHash(refresh), principalID, f.Get("client_id"), resource, scope, time.Now().Add(30*24*time.Hour).UnixMilli()); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("INSERT INTO oauth_access(credential_hash,scope,resource) VALUES(?,?,?) ON CONFLICT(credential_hash) DO UPDATE SET scope=excluded.scope,resource=excluded.resource", credentialHash(access), scope, resource); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if err := tx.Commit(); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600, "scope": scope, "resource": resource})
	})
	return nil
}
func oauthError(w http.ResponseWriter, kind string) {
	writeJSON(w, 400, map[string]string{"error": kind})
}
