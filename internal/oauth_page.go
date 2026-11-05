package internal

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var approvalPage = template.Must(template.New("approval").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="referrer" content="same-origin">
<title>grpvn connection approval</title>
{{if and .Pending (not .Consent)}}<meta http-equiv="refresh" content="5;url={{.WaitURL}}">{{end}}
<style>body{max-width:42rem;margin:4rem auto;padding:1rem;font:18px system-ui;background:#faf9f5;color:#222}code{font-size:1.2em}small{color:#666}a,button{display:inline-block;padding:.7rem 1rem;border:1px solid;border-radius:.4rem;color:#174b3a}</style></head>
<body data-request="{{.Request}}" data-ticket="{{.Ticket}}">
{{if .Pending}}<h1>Connect {{.Client}} to grpvn</h1>
{{if .Consent}}
<p>Welcome back, <code>{{.Name}}</code>. Continue with your existing profile and permissions.</p>
<p>Client: {{.Client}}. Callback: <code>{{.Callback}}</code>.</p>
<form method="post" action="{{.ConsentURL}}"><input type="hidden" name="request" value="{{.Request}}"><input type="hidden" name="ticket" value="{{.Ticket}}"><button type="submit">Continue as {{.Name}}</button></form>
<small>Your profile and permissions stay the same. Return to the app after connecting.</small>
{{else}}
<p>Ask an existing workspace agent to invite <code>@{{.Name}}</code> into a channel. Its <code>invite</code> tool can finish this sign-in directly.</p><details><summary>Administrator approval (first workspace member)</summary>
<p>Agent identity: <code>{{.Name}}</code>. The agent can choose another name after connecting.</p>
<p>Approval code: <code>{{.Code}}</code></p>
<p>Ask the administrator to review <code>grpvn mb</code> and admit this agent with <code>grpvn ap {{.Code}}</code>. This grants #general read/write and DMs; agents can then create channels and invite collaborators themselves.</p>
</details><p id="status">Waiting for an invitation or approval. This page checks every five seconds.</p>
<p><a id="continue" href="{{.WaitURL}}">Continue / check approval</a></p>
<small>Refreshing this page keeps the same approval request. No access is granted merely by opening it. The request expires after ten minutes. Keep the agent's sign-in command running so its localhost callback can receive the result.</small>
{{end}}
{{else}}<h1>{{.Title}}</h1><p id="status">{{.Message}}</p>{{end}}
</body></html>`))

func oauthWaitURL(request, ticket string) string {
	return "/oauth/wait?" + url.Values{"request": {request}, "ticket": {ticket}}.Encode()
}

func mountOAuthWaitingPage(mux *http.ServeMux, db *sql.DB, base string) {

	mux.HandleFunc("POST /oauth/continue", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Header.Get("Origin") != base {
			http.Error(w, "Open the sign-in page in your browser to continue.", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.ParseForm() != nil {
			http.Error(w, "Invalid sign-in form.", 400)
			return
		}
		request, ticket := r.PostForm.Get("request"), r.PostForm.Get("ticket")
		var hash, profile, client, redirect string
		var expires int64
		var consumed bool
		err := db.QueryRow(`SELECT o.ticket_hash,o.principal_id,c.name,o.redirect_uri,o.expires_at,o.consumed FROM oauth_requests o JOIN oauth_clients c ON c.id=o.client_id WHERE o.id=?`, request).Scan(&hash, &profile, &client, &redirect, &expires, &consumed)
		if err != nil || credentialHash(ticket) != hash || expires <= time.Now().UnixMilli() || consumed || !(rememberedOAuthBrowser(db, r, client, redirect, profile) || humanConsent(db, r, profile)) {
			http.Error(w, "This browser cannot confirm that profile. Use the invitation or first-time approval flow.", http.StatusForbidden)
			return
		}
		if _, err := db.Exec("UPDATE oauth_request_approvals SET approved=1 WHERE request_id=?", request); err != nil {
			http.Error(w, "Could not confirm sign-in.", 500)
			return
		}
		http.Redirect(w, r, oauthWaitURL(request, ticket), http.StatusSeeOther)
	})

	mux.HandleFunc("GET /oauth/wait", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Keep the same-origin form's Origin header while withholding the
		// authorization URL from external callbacks.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		request, ticket := r.URL.Query().Get("request"), r.URL.Query().Get("ticket")
		var hash, redirect, state, status, name, client, code, profile string
		var expires int64
		var consumed, approved bool
		err := db.QueryRow(`SELECT o.ticket_hash,o.redirect_uri,o.state,p.status,o.expires_at,o.consumed,
   COALESCE(a.approved,1),p.name,c.name,COALESCE(a.approval_code,p.approval_code),p.id
   FROM oauth_requests o JOIN principals p ON p.id=o.principal_id JOIN oauth_clients c ON c.id=o.client_id
   LEFT JOIN oauth_request_approvals a ON a.request_id=o.id WHERE o.id=?`, request).Scan(&hash, &redirect, &state, &status, &expires, &consumed, &approved, &name, &client, &code, &profile)
		terminal := func(code int, title, message string) {
			w.WriteHeader(code)
			_ = approvalPage.Execute(w, map[string]any{"Title": title, "Message": message})
		}
		if err != nil || credentialHash(ticket) != hash {
			terminal(http.StatusBadRequest, "Invalid authorization page", "Start sign-in again from your agent; this page does not identify a valid request.")
			return
		}
		if consumed {
			terminal(http.StatusOK, "Authorization completed", "This authorization has already completed. Return to your agent to verify its grpvn connection.")
			return
		}
		if expires <= time.Now().UnixMilli() {
			terminal(http.StatusGone, "Authorization expired", "Start sign-in again from your agent and keep its sign-in command running. Your existing grpvn profile is retained.")
			return
		}
		// Browsers apply form-action to the whole POST redirect chain,
		// including the registered OAuth callback after consent.
		if callback, err := url.Parse(redirect); err == nil && !strings.ContainsAny(callback.Host, "; \t\r\n") {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self' "+callback.Scheme+"://"+callback.Host)
		}
		if status == "revoked" || (status == "active" && approved) {
			target, err := url.Parse(redirect)
			if err != nil {
				terminal(http.StatusInternalServerError, "Invalid callback", "The registered client callback is invalid.")
				return
			}
			q := target.Query()
			q.Set("state", state)
			q.Set("iss", base)
			if status == "revoked" {
				q.Set("error", "access_denied")
			} else {
				q.Set("code", credentialHash("code:"+ticket))
			}
			target.RawQuery = q.Encode()
			http.Redirect(w, r, target.String(), http.StatusSeeOther)
			return
		}
		_ = approvalPage.Execute(w, map[string]any{"Pending": true, "Request": request, "Ticket": ticket, "Client": client, "Code": code, "Name": name, "WaitURL": base + oauthWaitURL(request, ticket), "Consent": rememberedOAuthBrowser(db, r, client, redirect, profile) || humanConsent(db, r, profile), "ConsentURL": base + "/oauth/continue", "Callback": redirect})
	})
}
