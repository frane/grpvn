package internal

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestOAuthWaitingPageContinuesWithoutJavaScript(t *testing.T) {
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
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	registration, _ := json.Marshal(map[string]any{"client_name": "Claude Code test", "redirect_uris": []string{"http://localhost:54321/callback"}})
	response, err := client.Post(base+"/oauth/register", "application/json", bytes.NewReader(registration))
	if err != nil {
		t.Fatal(err)
	}
	var registered struct {
		ID string `json:"client_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	verifier := strings.Repeat("w", 64)
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {registered.ID}, "redirect_uri": {"http://localhost:54321/callback"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "scope": {"grpvn.read grpvn.write"}, "state": {"same-client-state"}}
	response, err = client.Get(base + "/oauth/authorize?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("authorization should redirect to stable waiting page: %d", response.StatusCode)
	}
	waiting, err := response.Location()
	if err != nil || waiting.Path != "/oauth/wait" {
		t.Fatalf("no stable wait URL: %v %v", waiting, err)
	}
	var approval string
	if err := db.QueryRow("SELECT approval_code FROM oauth_request_approvals WHERE request_id=?", waiting.Query().Get("request")).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		response, err = client.Get(waiting.String())
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || bytes.Contains(page, []byte("<script")) || !bytes.Contains(page, []byte(`http-equiv="refresh"`)) || !bytes.Contains(page, []byte("Continue / check approval")) || !bytes.Contains(page, []byte(approval)) {
			t.Fatalf("waiting page requires scripts or loses its request: %d %s", response.StatusCode, page)
		}
		if response.Header.Get("Referrer-Policy") != "same-origin" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("waiting page must preserve same-origin consent and prevent external referrers or caching")
		}
		link := regexp.MustCompile(`id="continue" href="([^"]+)"`).FindSubmatch(page)
		if len(link) != 2 || html.UnescapeString(string(link[1])) != waiting.String() {
			t.Fatalf("continue link cannot reach the same waiting page from an embedded viewer: %s", page)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM oauth_requests").Scan(&count); err != nil || count != 1 {
		t.Fatalf("refresh created new requests: %d %v", count, err)
	}
	if err := ApproveMember(db, approval, "", []string{"#general"}, true); err != nil {
		t.Fatal(err)
	}
	response, err = client.Get(waiting.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	callback, err := response.Location()
	if err != nil || response.StatusCode != http.StatusSeeOther || callback.Host != "localhost:54321" || callback.Query().Get("state") != "same-client-state" || callback.Query().Get("code") == "" {
		t.Fatalf("approved page failed to redirect without scripts: %d %v %v", response.StatusCode, callback, err)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {registered.ID}, "redirect_uri": {"http://localhost:54321/callback"}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}}
	response, err = client.PostForm(base+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("browser callback code cannot complete PKCE: %d", response.StatusCode)
	}
	response, err = client.Get(waiting.String())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(page, []byte("Authorization completed")) || bytes.Contains(page, []byte(`http-equiv="refresh"`)) {
		t.Fatalf("completed page keeps redirecting/polling: %d %s", response.StatusCode, page)
	}
	bad := *waiting
	values := bad.Query()
	values.Set("ticket", "wrong")
	bad.RawQuery = values.Encode()
	response, err = client.Get(bad.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("waiting page accepted an invalid ticket")
	}
}
