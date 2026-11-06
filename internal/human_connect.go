package internal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Human desktop sign-in uses the coordinator's existing human account.
// OAuth PKCE returns to the app's loopback listener; no aliases, tokens,
// channel grants or approval codes are entered in the desktop application.
func browserHuman(db *sql.DB, r *http.Request) *Principal {
	s := &webServer{db: db}
	var id string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&id) == nil {
		s.cookieName = webSessionCookie + "_" + credentialHash(id)[:12]
	}
	return s.principal(r)
}
func humanConsent(db *sql.DB, r *http.Request, id string) bool {
	p := browserHuman(db, r)
	return p != nil && p.ID == id
}

func coordinatorURL(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	u, err := url.Parse(input)
	if err != nil || u.Host == "" || u.User != nil {
		return "", "", fmt.Errorf("Enter a workspace address or invitation link")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", "", fmt.Errorf("Use an HTTPS workspace address")
	}
	invite := u.Query().Get("invite")
	if invite == "" {
		invite = strings.TrimPrefix(u.Fragment, "invite=")
	}
	if u.Fragment != "" && !strings.HasPrefix(u.Fragment, "invite=") {
		invite = ""
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), invite, nil
}
func connectHTTP(ctx context.Context, method, endpoint string, payload any, out any) error {
	var body io.Reader
	headers := http.Header{"Accept": []string{"application/json"}}
	if form, ok := payload.(url.Values); ok {
		body = strings.NewReader(form.Encode())
		headers.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
		headers.Set("Content-Type", "application/json")
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	r.Header = headers
	response, err := syncHTTPClient.Do(r)
	if err != nil {
		return fmt.Errorf("Could not reach that workspace. Check the address and try again")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure)
		if failure.Error == "invalid_grant" {
			return fmt.Errorf("Sign in to this workspace again")
		}
		return fmt.Errorf("Workspace sign-in failed (%d)", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(out)
}

func (s *webServer) startWorkspace(w http.ResponseWriter, r *http.Request, p *Principal) {
	var a struct {
		URL   string
		Force bool
	}
	if !webDecode(w, r, &a) {
		return
	}
	base, invite, err := coordinatorURL(a.URL)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var info serverInfo
	if err = remoteServerInfo(ctx, Remote{URL: base}, &info); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if !info.Web {
		apiError(w, 400, "This server has web sign-in disabled. Ask its operator to enable --web.")
		return
	}
	if info.Protocol != syncProtocol || !validMessageID(info.ServerID) {
		apiError(w, 400, "This address is not a grpvn workspace")
		return
	}
	var alias string
	_ = s.db.QueryRow("SELECT name FROM remotes WHERE server_id=? ORDER BY name LIMIT 1", info.ServerID).Scan(&alias)
	if alias == "" {
		alias = "workspace-" + strings.ToLower(info.ServerID[len(info.ServerID)-8:])
	}
	var existing string
	if !a.Force && invite == "" && s.db.QueryRow("SELECT principal_id FROM remote_human_sessions WHERE server_id=? AND local_principal_id=?", info.ServerID, p.ID).Scan(&existing) == nil {
		remote, err := getRemote(s.db, alias)
		if err != nil {
			apiError(w, 400, "Could not restore this workspace")
			return
		}
		if err = discoverWorkspaceChannels(ctx, s.db, *remote); err != nil {
			apiError(w, 400, "Could not load workspace channels. Try again.")
			return
		}
		target := activeRemoteTarget(s.db, alias, p.ID)
		writeJSON(w, 200, map[string]string{"status": "connected", "source": alias, "target": target})
		return
	}
	callback := "http://" + r.Host + "/oauth/callback"
	var client struct {
		ID string `json:"client_id"`
	}
	if err = connectHTTP(ctx, "POST", base+"/oauth/register", map[string]any{"client_name": "grpvn Desktop", "redirect_uris": []string{callback}, "token_endpoint_auth_method": "none"}, &client); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	state, err := newCredential()
	if err != nil {
		apiError(w, 500, "Could not start sign-in")
		return
	}
	verifier, err := newCredential()
	if err != nil {
		apiError(w, 500, "Could not start sign-in")
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {callback}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "scope": {"grpvn.read grpvn.write"}, "resource": {base + "/mcp"}, "state": {state}, "actor": {"human"}}
	authorize := "/oauth/authorize?" + q.Encode()
	browserURL := base + authorize
	if invite != "" {
		browserURL = base + "/join?" + url.Values{"invite": {invite}, "next": {authorize}}.Encode()
	}
	_, err = s.db.Exec("INSERT INTO workspace_signins(state_hash,local_principal_id,base_url,server_id,alias,client_id,callback,verifier,status,expires_at) VALUES(?,?,?,?,?,?,?,?,'waiting',?)", credentialHash(state), p.ID, base, info.ServerID, alias, client.ID, callback, verifier, time.Now().Add(10*time.Minute).UnixMilli())
	if err != nil {
		apiError(w, 500, "Could not save sign-in")
		return
	}
	w.Header().Set("Location", "/v1/workspace-connections/"+credentialHash(state))
	writeJSON(w, 201, map[string]any{"status": "waiting", "attempt": credentialHash(state), "url": browserURL, "workspace": base})
}

type humanTokens struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Expires int64  `json:"expires_in"`
}

func remoteIdentity(ctx context.Context, remote Remote) (string, string, error) {
	var response struct {
		Result struct {
			Error   bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := remoteRequest(ctx, remote, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "identity", "arguments": map[string]any{}}}, &response); err != nil {
		return "", "", err
	}
	if response.Result.Error || len(response.Result.Content) == 0 {
		return "", "", fmt.Errorf("Could not read your workspace profile")
	}
	var identity struct{ ID, Name string }
	if json.Unmarshal([]byte(response.Result.Content[0].Text), &identity) != nil || identity.ID == "" || identity.Name == "" {
		return "", "", fmt.Errorf("Invalid workspace profile")
	}
	return identity.ID, identity.Name, nil
}

func remoteChatTool(ctx context.Context, remote Remote, name string, args any) (map[string]any, error) {
	var response struct {
		Result struct {
			Error   bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := remoteRequest(ctx, remote, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}, &response); err != nil {
		return nil, err
	}
	if len(response.Result.Content) == 0 {
		return nil, fmt.Errorf("Workspace returned an empty response")
	}
	if response.Result.Error {
		return nil, fmt.Errorf("%s", response.Result.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &out); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *webServer) remoteChannel(ctx context.Context, p *Principal, target string) (Remote, string, bool, error) {
	alias, channel, ok := splitRemoteTarget(target)
	if !s.local || !ok {
		return Remote{}, "", false, nil
	}
	remote, err := getRemote(s.db, alias)
	if err == sql.ErrNoRows {
		return Remote{}, "", false, nil
	}
	if err != nil {
		return Remote{}, "", true, err
	}
	key, err := localCredential(s.db, remote.ServerID, "")
	if err != nil {
		return *remote, channel, true, err
	}
	remote.DB = s.db
	remote.Token = key.Secret
	remote.MemberID = key.ID
	var owner string
	err = s.db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner)
	if err == nil {
		if owner != p.ID {
			return *remote, channel, true, fmt.Errorf("Sign in to this workspace with your own account")
		}
		return *remote, channel, true, nil
	}
	if err != sql.ErrNoRows {
		return *remote, channel, true, err
	}
	actor, err := agentRemote(ctx, s.db, *remote, principalAddress(p), channel)
	return actor, channel, true, err
}
func (s *webServer) workspaceCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	state := credentialHash(r.URL.Query().Get("state"))
	var base, server, alias, client, callback, verifier, owner, status string
	var expires int64
	err := s.db.QueryRow("SELECT base_url,server_id,alias,client_id,callback,verifier,local_principal_id,status,expires_at FROM workspace_signins WHERE state_hash=?", state).Scan(&base, &server, &alias, &client, &callback, &verifier, &owner, &status, &expires)
	if err != nil || status != "waiting" || expires <= time.Now().UnixMilli() || r.URL.Query().Get("iss") != base {
		http.Error(w, "This sign-in expired. Return to grpvn and try again.", 400)
		return
	}
	if r.URL.Query().Get("error") != "" {
		s.db.Exec("UPDATE workspace_signins SET status='failed',detail='Sign-in was cancelled' WHERE state_hash=?", state)
		http.Error(w, "Sign-in cancelled. Return to grpvn.", 400)
		return
	}
	claim, err := s.db.Exec("UPDATE workspace_signins SET status='connecting' WHERE state_hash=? AND status='waiting'", state)
	if err != nil {
		http.Error(w, "Could not finish sign-in", 500)
		return
	}
	n, _ := claim.RowsAffected()
	if n != 1 {
		http.Error(w, "This sign-in has already been used", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	fail := func(err error) {
		s.db.Exec("UPDATE workspace_signins SET status='failed',detail=? WHERE state_hash=?", err.Error(), state)
		http.Error(w, "Could not connect. Return to grpvn to try again.", 400)
	}
	var tokens humanTokens
	if err = connectHTTP(ctx, "POST", base+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "redirect_uri": {callback}, "code_verifier": {verifier}, "code": {r.URL.Query().Get("code")}, "resource": {base + "/mcp"}}, &tokens); err != nil {
		fail(err)
		return
	}
	remote := Remote{Name: alias, URL: base, ServerID: server, Token: tokens.Access}
	id, name, err := remoteIdentity(ctx, remote)
	if err != nil {
		fail(err)
		return
	}
	tx, err := beginWrite(s.db)
	if err != nil {
		fail(err)
		return
	}
	defer tx.Rollback()
	var pinned string
	err = tx.QueryRow("SELECT server_id FROM remotes WHERE name=?", alias).Scan(&pinned)
	if err != nil && err != sql.ErrNoRows {
		tx.Rollback()
		fail(err)
		return
	}
	if pinned != "" && pinned != server {
		tx.Rollback()
		fail(fmt.Errorf("Workspace identity changed"))
		return
	}
	_, err = tx.Exec("INSERT INTO remotes(name,url,token,server_id) VALUES(?,?,?,?) ON CONFLICT(name) DO UPDATE SET url=excluded.url,token=excluded.token", alias, base, tokens.Access, server)
	if err == nil {
		_, err = tx.Exec("INSERT INTO local_credentials(server_id,agent_name,credential,principal_id,status) VALUES(?,'',?,?,'active') ON CONFLICT(server_id,agent_name) DO UPDATE SET credential=excluded.credential,principal_id=excluded.principal_id,status='active'", server, tokens.Access, id)
	}
	if err == nil {
		_, err = tx.Exec(`INSERT INTO remote_human_sessions(server_id,local_principal_id,principal_id,name,client_id,refresh_token,expires_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(server_id) DO UPDATE SET local_principal_id=excluded.local_principal_id,principal_id=excluded.principal_id,name=excluded.name,client_id=excluded.client_id,refresh_token=excluded.refresh_token,expires_at=excluded.expires_at`, server, owner, id, name, client, tokens.Refresh, time.Now().Add(time.Duration(tokens.Expires)*time.Second).UnixMilli())
	}
	if err != nil {
		tx.Rollback()
		fail(err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(err)
		return
	}
	remote.DB = s.db
	if err = discoverWorkspaceChannels(ctx, s.db, remote); err != nil {
		fail(err)
		return
	}
	if _, err = s.db.Exec("UPDATE workspace_signins SET status='connected',verifier='' WHERE state_hash=?", state); err != nil {
		fail(err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width"><title>Connected to grpvn</title></head><body style="font:16px system-ui;max-width:32rem;margin:15vh auto;padding:24px"><h1>Connected</h1><p>Return to the grpvn app. You can close this tab.</p></body></html>`)
}
func (s *webServer) workspaceStatus(w http.ResponseWriter, r *http.Request, p *Principal) {
	var status, detail, alias string
	var expires int64
	err := s.db.QueryRow("SELECT status,detail,alias,expires_at FROM workspace_signins WHERE state_hash=? AND local_principal_id=?", strings.TrimPrefix(r.URL.Path, "/v1/workspace-connections/"), p.ID).Scan(&status, &detail, &alias, &expires)
	if err != nil {
		apiError(w, 404, "Sign-in not found")
		return
	}
	if status == "waiting" && expires <= time.Now().UnixMilli() {
		status = "failed"
		detail = "Sign-in expired. Try again."
	}
	target := ""
	if status == "connected" {
		target = activeRemoteTarget(s.db, alias, p.ID)
	}
	writeJSON(w, 200, map[string]string{"status": status, "detail": detail, "target": target})
}

func discoverWorkspaceChannels(ctx context.Context, db *sql.DB, remote Remote) error {
	var owner string
	if db.QueryRow("SELECT local_principal_id FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&owner) == nil {
		return discoverHumanChannels(ctx, db, remote, owner)
	}
	var list struct {
		Channels []string `json:"channels"`
	}
	if err := remoteRequest(ctx, remote, "GET", "/v1/channels", nil, &list); err != nil {
		return err
	}
	for _, channel := range list.Channels {
		if err := addRemoteChannel(db, remote.Name, channel); err != nil {
			return err
		}
	}
	return nil
}
func refreshHumanRemote(ctx context.Context, db *sql.DB, remote *Remote) error {
	var client, refresh string
	var expires int64
	err := db.QueryRow("SELECT client_id,refresh_token,expires_at FROM remote_human_sessions WHERE server_id=?", remote.ServerID).Scan(&client, &refresh, &expires)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if expires > time.Now().Add(time.Minute).UnixMilli() {
		return nil
	}
	var tokens humanTokens
	if err = connectHTTP(ctx, "POST", remote.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {refresh}, "resource": {remote.URL + "/mcp"}}, &tokens); err != nil {
		return err
	}
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE remote_human_sessions SET refresh_token=?,expires_at=? WHERE server_id=?", tokens.Refresh, time.Now().Add(time.Duration(tokens.Expires)*time.Second).UnixMilli(), remote.ServerID); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE local_credentials SET credential=? WHERE server_id=? AND agent_name=''", tokens.Access, remote.ServerID); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE remotes SET token=? WHERE server_id=?", tokens.Access, remote.ServerID); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		remote.Token = tokens.Access
	}
	return err
}
