package internal

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var approvalPage = template.Must(template.New("approval").Parse(`<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>grpvn connection approval</title><style>body{max-width:42rem;margin:4rem auto;padding:1rem;font:18px system-ui;background:#faf9f5;color:#222}code{font-size:1.3em}small{color:#666}</style><body data-request="{{.Request}}" data-ticket="{{.Ticket}}"><h1>Connect {{.Client}} to grpvn</h1><p>This connection needs an administrator's approval.</p><p>Approval code: <code>{{.Code}}</code></p><p>Ask the workspace administrator to review this request with <code>grpvn mb</code>, then grant the agent a name and explicit channel rights with <code>grpvn ap {{.Code}} --name chatgpt --channel '#team' --write</code>.</p><p id="status">Waiting for approval…</p><small>No access is granted by opening this page. This request expires in 10 minutes.</small><script>let delay=2500;async function poll(){try{const q=new URLSearchParams({request:document.body.dataset.request,ticket:document.body.dataset.ticket});const r=await fetch('/oauth/ready?'+q,{cache:'no-store'});const v=await r.json();if(v.redirect){location.replace(v.redirect);return}if(v.error){document.getElementById('status').textContent=v.error;return}delay=Math.min(delay*1.2,10000)}catch{delay=Math.min(delay*2,30000)}setTimeout(poll,delay+Math.random()*500)}poll()</script></body>`))

func mountOAuth(mux *http.ServeMux, db *sql.DB, base string) error {
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return fmt.Errorf("--public-url needs a root HTTPS origin (HTTP is supported only on loopback)")
	}
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
		requested, _ := json.Marshal([]string{"oauth:" + scope, "redirect:" + redirect.Host})
		tx, err := beginWrite(db)
		if err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		defer tx.Rollback()
		if _, err := tx.Exec("INSERT INTO principals(id,credential_hash,name,kind,status,approval_code,requested,created_at) VALUES(?,?,?,'agent','pending',?,?,?)", principalID, credentialHash(secret), "ChatGPT: "+name+" @ "+redirect.Host, code, string(requested), time.Now().UnixMilli()); err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		authCode := credentialHash("code:" + ticket)
		if _, err := tx.Exec("INSERT INTO oauth_requests(id,client_id,principal_id,ticket_hash,code_hash,redirect_uri,state,challenge,scope,resource,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, q.Get("client_id"), principalID, credentialHash(ticket), credentialHash(authCode), q.Get("redirect_uri"), q.Get("state"), q.Get("code_challenge"), scope, resource, time.Now().Add(10*time.Minute).UnixMilli()); err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		if err := tx.Commit(); err != nil {
			apiError(w, 500, "cannot create authorization")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		_ = approvalPage.Execute(w, map[string]string{"Request": id, "Ticket": ticket, "Client": name, "Code": code})
	})
	mux.HandleFunc("GET /oauth/ready", func(w http.ResponseWriter, r *http.Request) {
		var ticketHash, redirect, state, status string
		var expires int64
		var consumed bool
		err := db.QueryRow("SELECT o.ticket_hash,o.redirect_uri,o.state,p.status,o.expires_at,o.consumed FROM oauth_requests o JOIN principals p ON p.id=o.principal_id WHERE o.id=?", r.URL.Query().Get("request")).Scan(&ticketHash, &redirect, &state, &status, &expires, &consumed)
		ticket := r.URL.Query().Get("ticket")
		if err != nil || credentialHash(ticket) != ticketHash || expires <= time.Now().UnixMilli() || consumed {
			writeJSON(w, 400, map[string]string{"error": "Authorization request is invalid or expired."})
			return
		}
		if status != "active" && status != "revoked" {
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
			err := tx.QueryRow("SELECT client_id,principal_id,redirect_uri,challenge,scope,resource,expires_at,consumed FROM oauth_requests WHERE code_hash=?", credentialHash(f.Get("code"))).Scan(&client, &principalID, &redirect, &challenge, &scope, &target, &expires, &consumed)
			verifier := f.Get("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			if err != nil || consumed || expires <= time.Now().UnixMilli() || client != f.Get("client_id") || redirect != f.Get("redirect_uri") || len(verifier) < 43 || len(verifier) > 128 || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || target != resource {
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
		if _, err := tx.Exec("INSERT INTO credential_expiry(credential_hash,expires_at) VALUES(?,?)", credentialHash(access), time.Now().Add(time.Hour).UnixMilli()); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("INSERT INTO oauth_refresh(secret_hash,principal_id,client_id,resource,scope,expires_at) VALUES(?,?,?,?,?,?)", credentialHash(refresh), principalID, f.Get("client_id"), resource, scope, time.Now().Add(30*24*time.Hour).UnixMilli()); err != nil {
			apiError(w, 500, "cannot issue token")
			return
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO oauth_access(credential_hash,scope,resource) VALUES(?,?,?)", credentialHash(access), scope, resource); err != nil {
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
