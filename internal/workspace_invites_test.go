package internal

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestInvitationLinkCompletesOAuthWithoutAdminRelay(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "owner-token", "owner", []string{"#project", "@*"})
	if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='owner'"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	handler, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: base})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	call := func(name string, args any) map[string]any {
		t.Helper()
		v, failed := remoteTool(t, handler, "owner-token", name, args)
		if failed {
			t.Fatal(v)
		}
		return v
	}
	invite := call("invite", map[string]any{"channel": "#project"})
	jar, _ := cookiejar.New(nil)
	browser := server.Client()
	browser.Jar = jar
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := browser.Get(invite["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("browser invitation unavailable: %d", response.StatusCode)
	}
	verifier := strings.Repeat("i", 64)
	sum := sha256.Sum256([]byte(verifier))
	data, _ := json.Marshal(map[string]any{"client_name": "Invited Claude", "redirect_uris": []string{"https://client.example/callback"}})
	response, err = browser.Post(base+"/oauth/register", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var registration struct {
		ID string `json:"client_id"`
	}
	json.NewDecoder(response.Body).Decode(&registration)
	response.Body.Close()
	query := url.Values{"client_id": {registration.ID}, "redirect_uri": {"https://client.example/callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "scope": {"grpvn.read grpvn.write"}}
	authorize := func() *url.URL {
		t.Helper()
		r, err := browser.Get(base + "/oauth/authorize?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 303 {
			t.Fatalf("authorize failed: %d", r.StatusCode)
		}
		u, err := r.Location()
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	waiting := authorize()
	response, err = browser.Get(waiting.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	callback, err := response.Location()
	if err != nil || response.StatusCode != 303 || callback.Host != "client.example" {
		t.Fatalf("invited peer was forced through administrator approval: %d %v %v", response.StatusCode, callback, err)
	}
	exchange := func(code string) (int, string) {
		t.Helper()
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {registration.ID}, "redirect_uri": {"https://client.example/callback"}, "code": {code}, "code_verifier": {verifier}}
		r, err := browser.PostForm(base+"/oauth/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		token, _ := v["access_token"].(string)
		return r.StatusCode, token
	}
	status, credential := exchange(callback.Query().Get("code"))
	if status != 200 {
		t.Fatal("invited peer could not finish PKCE")
	}
	identity, failed := remoteTool(t, handler, credential, "identity", map[string]any{})
	if failed {
		t.Fatal(identity)
	}
	if v, failed := remoteTool(t, handler, credential, "send", map[string]any{"target": "#project", "body": "joined without manual code"}); failed {
		t.Fatal(v)
	}
	other := *browser
	other.Jar, _ = cookiejar.New(nil)
	response, err = other.Get(invite["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 410 {
		t.Fatal("single-use invitation was reusable")
	}
	// A link may join an existing profile to another channel, but must not
	// silently authorize a new client against that profile's other rights.
	call("send", map[string]any{"target": "#extra", "body": "another project"})
	second := call("invite", map[string]any{"channel": "#extra"})
	response, err = browser.Get(second["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	waiting = authorize()
	response, err = browser.Get(waiting.String())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(page, []byte("Continue as")) {
		t.Fatalf("existing profile bypassed browser consent: %d %s", response.StatusCode, page)
	}
	ticket := waiting.Query().Get("ticket")
	if status, _ := exchange(credentialHash("code:" + ticket)); status != 400 {
		t.Fatal("invitation link silently authorized existing-profile credentials")
	}
	form := url.Values{"request": {waiting.Query().Get("request")}, "ticket": {ticket}}
	request, _ := http.NewRequest("POST", base+"/oauth/continue", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", base)
	response, err = browser.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatal("existing profile could not consent without an admin code")
	}
	status, credential2 := exchange(credentialHash("code:" + ticket))
	if status != 200 {
		t.Fatal("existing profile failed to reconnect")
	}
	after, failed := remoteTool(t, handler, credential2, "identity", map[string]any{})
	if failed || after["id"] != identity["id"] {
		t.Fatal("reconnection replaced the profile")
	}
	if v, failed := remoteTool(t, handler, credential2, "read", map[string]any{"target": "#extra"}); failed {
		t.Fatal(v)
	}
	call("send", map[string]any{"target": "#readonly", "body": "shared read-only"})
	third := call("invite", map[string]any{"channel": "#readonly", "write": false})
	if v, failed := remoteTool(t, handler, credential, "join", map[string]any{"invitation": third["url"]}); failed {
		t.Fatal(v)
	}
	if v, failed := remoteTool(t, handler, credential, "read", map[string]any{"target": "#readonly"}); failed {
		t.Fatal(v)
	}
	if v, failed := remoteTool(t, handler, credential, "send", map[string]any{"target": "#readonly", "body": "must remain read-only"}); !failed {
		t.Fatalf("read-only invitation was widened: %v", v)
	}
	last := call("invite", map[string]any{"channel": "#project"})
	secret := strings.TrimPrefix(strings.Split(last["cli_url"].(string), "#invite=")[1], "invite=")
	var ownerID string
	if err := db.QueryRow("SELECT id FROM principals WHERE name='owner'").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE principals SET status='revoked' WHERE id=?", ownerID); err != nil {
		t.Fatal(err)
	}
	access, _ := newChannelAccess(CoordinatorOptions{})
	access.db = db
	if _, err := registerMember(db, access, registrationRequest{Credential: strings.Repeat("x", 43), Invite: secret, Channels: []string{"#project"}}, nil); err == nil {
		t.Fatal("CLI redemption trusted a revoked invitation issuer")
	}
}

func TestOpenChannelRegistrationAndExistingProfileJoin(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "owner-token", "owner", []string{"#general"})
	if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='owner'"); err != nil {
		t.Fatal(err)
	}
	handler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, failed := remoteTool(t, handler, "owner-token", "create_channel", map[string]any{"channel": "#lobby", "open": true}); failed {
		t.Fatal(v)
	}
	post := func(path, token string, body any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "https://test"+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != 200 {
			t.Fatalf("%s: %d %s", path, out.Code, out.Body.String())
		}
		var value map[string]any
		json.Unmarshal(out.Body.Bytes(), &value)
		return value
	}
	device := strings.Repeat("d", 43)
	agent := strings.Repeat("a", 43)
	post("/v1/register", "", map[string]any{"credential": device, "channels": []string{"#lobby"}})
	peer := post("/v1/agents", device, map[string]any{"credential": agent, "channels": []string{"#lobby"}})
	if peer["name"] == "" {
		t.Fatal("unnamed public agent did not get a name")
	}
	if v, failed := remoteTool(t, handler, agent, "create_channel", map[string]any{"channel": "#not-admitted"}); !failed {
		t.Fatalf("public guest could create private workspace channels: %v", v)
	}
}
