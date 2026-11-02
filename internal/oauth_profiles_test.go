package internal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestOAuthReconnectKeepsProfileAndRequiresApproval(t *testing.T) {
	db := newTestDB(t)
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	handler, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: base})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := server.Client()
	client.Jar = jar
	verifier := strings.Repeat("p", 64)
	sum := sha256.Sum256([]byte(verifier))
	type flow struct{ id, profile, name, approval, ticket, client, redirect string }
	begin := func(browser *http.Client, label, redirect string) flow {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"client_name": label, "redirect_uris": []string{redirect}})
		response, err := browser.Post(base+"/oauth/register", "application/json", strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		var registration struct {
			ID string `json:"client_id"`
		}
		json.NewDecoder(response.Body).Decode(&registration)
		response.Body.Close()
		query := url.Values{"client_id": {registration.ID}, "redirect_uri": {redirect}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "scope": {"grpvn.read grpvn.write"}}
		response, err = browser.Get(base + "/oauth/authorize?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("authorize %d: %s", response.StatusCode, page)
		}
		attributes := regexp.MustCompile("data-request=\"([^\"]+)\" data-ticket=\"([^\"]+)\"").FindSubmatch(page)
		if len(attributes) != 3 {
			t.Fatalf("missing approval context: %s", page)
		}
		f := flow{id: string(attributes[1]), ticket: string(attributes[2]), client: registration.ID, redirect: redirect}
		if err := db.QueryRow("SELECT o.principal_id,p.name,a.approval_code FROM oauth_requests o JOIN principals p ON p.id=o.principal_id JOIN oauth_request_approvals a ON a.request_id=o.id WHERE o.id=?", f.id).Scan(&f.profile, &f.name, &f.approval); err != nil {
			t.Fatal(err)
		}
		return f
	}
	ready := func(f flow) map[string]any {
		t.Helper()
		response, err := client.Get(base + "/oauth/ready?" + url.Values{"request": {f.id}, "ticket": {f.ticket}}.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	token := func(f flow) (int, string) {
		t.Helper()
		form := url.Values{"grant_type": {"authorization_code"}, "client_id": {f.client}, "redirect_uri": {f.redirect}, "code": {credentialHash("code:" + f.ticket)}, "code_verifier": {verifier}, "resource": {base + "/mcp"}}
		response, err := client.PostForm(base+"/oauth/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var value map[string]any
		json.NewDecoder(response.Body).Decode(&value)
		result, _ := value["access_token"].(string)
		return response.StatusCode, result
	}
	first := begin(client, "ChatGPT", "https://chatgpt.com/callback")
	if !regexp.MustCompile("^[a-z]+-[a-z]+-[a-f0-9]{4}$").MatchString(first.name) {
		t.Fatalf("runtime name assigned instead of generated identity: %s", first.name)
	}
	if status, _ := token(first); status != 400 {
		t.Fatal("unapproved authorization was exchanged")
	}
	if err := ApproveMember(db, first.approval, "", []string{"#general", "@*"}, true); err != nil {
		t.Fatal(err)
	}
	status, token1 := token(first)
	if status != 200 {
		t.Fatal("initial token exchange failed")
	}
	if _, err := RenamePrincipal(db, first.profile, "rook"); err != nil {
		t.Fatal(err)
	}
	second := begin(client, "ChatGPT", "https://chatgpt.com/callback")
	if second.profile != first.profile || second.name != "rook" {
		t.Fatalf("reconnect created another profile: %+v %+v", first, second)
	}
	if ready(second)["status"] != "pending" {
		t.Fatal("browser cookie silently authorized another OAuth request")
	}
	if status, _ := token(second); status != 400 {
		t.Fatal("reconnect bypassed approval")
	}
	if _, failed := remoteTool(t, handler, token1, "identity", map[string]any{}); failed {
		t.Fatal("pending reconnect disrupted existing session")
	}
	if err := ApproveMember(db, second.approval, "", []string{"#general", "@*"}, true); err != nil {
		t.Fatal(err)
	}
	status, token2 := token(second)
	if status != 200 {
		t.Fatal("reconnect exchange failed")
	}
	for _, credential := range []string{token1, token2} {
		identity, failed := remoteTool(t, handler, credential, "identity", map[string]any{})
		if failed || identity["id"] != first.profile || identity["name"] != "rook" {
			t.Fatalf("profile or existing session lost: %v", identity)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM principals").Scan(&count); err != nil || count != 1 {
		t.Fatalf("reconnect multiplied profiles: %d %v", count, err)
	}
	otherJar, _ := cookiejar.New(nil)
	otherBrowser := *client
	otherBrowser.Jar = otherJar
	recovery := begin(&otherBrowser, "ChatGPT", "https://chatgpt.com/callback")
	if recovery.profile == first.profile {
		t.Fatal("another browser inherited the profile by app name")
	}
	if err := ApproveMember(db, recovery.approval, "", []string{"#general", "@*"}, true, first.profile); err != nil {
		t.Fatal(err)
	}
	status, recovered := token(recovery)
	if status != 200 {
		t.Fatal("explicit profile recovery failed")
	}
	identity, failed := remoteTool(t, handler, recovered, "identity", map[string]any{})
	if failed || identity["id"] != first.profile || identity["name"] != "rook" {
		t.Fatalf("explicit recovery lost profile: %v", identity)
	}
	if err := db.QueryRow("SELECT count(*) FROM principals").Scan(&count); err != nil || count != 1 {
		t.Fatalf("recovery retained a throwaway profile: %d %v", count, err)
	}
	codex := begin(client, "Codex", "http://127.0.0.1:12345/callback")
	codexAgain := begin(client, "Codex", "http://127.0.0.1:54321/callback")
	if codex.profile != codexAgain.profile || codex.profile == first.profile {
		t.Fatal("loopback reconnect/profile isolation failed")
	}
}
