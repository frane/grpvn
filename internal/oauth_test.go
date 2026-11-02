package internal

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestOAuthApprovalPKCEAndBidirectionalMCP(t *testing.T) {
	db := newTestDB(t)
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	handler, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: base})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(server.Close)
	postJSON := func(path string, body any, token string) (int, []byte) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, err := http.NewRequest("POST", base+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, out
	}
	status, data := postJSON("/oauth/register", map[string]any{"client_name": "ChatGPT test", "redirect_uris": []string{"https://chatgpt.com/connector_platform_oauth_redirect"}, "token_endpoint_auth_method": "none"}, "")
	if status != 201 {
		t.Fatalf("registration %d %s", status, data)
	}
	var client struct {
		ID string `json:"client_id"`
	}
	_ = json.Unmarshal(data, &client)
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {client.ID}, "redirect_uri": {"https://chatgpt.com/connector_platform_oauth_redirect"}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "state": {"caller-state"}, "resource": {base + "/mcp"}, "scope": {"grpvn.read grpvn.write"}}
	resp, err := server.Client().Get(base + "/oauth/authorize?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	attributes := regexp.MustCompile(`data-request="([^"]+)" data-ticket="([^"]+)"`).FindSubmatch(page)
	if len(attributes) != 3 {
		t.Fatalf("missing approval context: %s", page)
	}
	var approval string
	if err := db.QueryRow("SELECT p.approval_code FROM principals p JOIN oauth_requests o ON o.principal_id=p.id WHERE o.id=?", string(attributes[1])).Scan(&approval); err != nil {
		t.Fatal(err)
	}
	ready := func() string {
		t.Helper()
		r, err := server.Client().Get(base + "/oauth/ready?request=" + string(attributes[1]) + "&ticket=" + string(attributes[2]))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var result struct {
			Redirect string `json:"redirect"`
		}
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result.Redirect
	}
	if ready() != "" {
		t.Fatal("pending registration was authorized without oversight")
	}
	if err := ApproveMember(db, approval, "chatgpt", []string{"#team", "@*"}, true); err != nil {
		t.Fatal(err)
	}
	redirect, err := url.Parse(ready())
	if err != nil {
		t.Fatal(err)
	}
	if redirect.Query().Get("iss") != base || redirect.Query().Get("state") != "caller-state" {
		t.Fatalf("OAuth issuer/state mismatch: %s", redirect)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {client.ID}, "redirect_uri": {"https://chatgpt.com/connector_platform_oauth_redirect"}, "code": {redirect.Query().Get("code")}, "code_verifier": {"wrong"}, "resource": {base + "/mcp"}}
	exchange := func(form url.Values) (int, map[string]any) {
		t.Helper()
		r, err := server.Client().PostForm(base+"/oauth/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return r.StatusCode, body
	}
	if status, _ := exchange(form); status != 400 {
		t.Fatal("invalid PKCE verifier accepted")
	}
	form.Set("code_verifier", verifier)
	status, credentials := exchange(form)
	if status != 200 {
		t.Fatalf("token exchange: %d %+v", status, credentials)
	}
	token, _ := credentials["access_token"].(string)
	if token == "" {
		t.Fatal("missing access token")
	}
	if status, _ := exchange(form); status != 400 {
		t.Fatal("authorization code was reusable")
	}
	status, data = postJSON("/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "test", "version": "1"}}}, token)
	if status != 200 || !bytes.Contains(data, []byte(`"result"`)) {
		t.Fatalf("MCP initialize: %d %s", status, data)
	}
	call := func(name string, args any) string {
		t.Helper()
		status, data := postJSON("/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}, token)
		if status != 200 || bytes.Contains(data, []byte(`"isError":true`)) {
			t.Fatalf("MCP %s: %d %s", name, status, data)
		}
		return string(data)
	}
	original, err := Send(db, "alice", "@chatgpt", "please respond", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if out := call("read", map[string]any{"target": "@me"}); !strings.Contains(out, "please respond") {
		t.Fatalf("MCP missed DM: %s", out)
	}
	call("send", map[string]any{"target": original.ID, "body": "reply from ChatGPT", "idempotency_key": "reply-1"})
	reply := messageIn(t, db, "@alice", "reply from ChatGPT")
	if reply.ParentID == nil || *reply.ParentID != original.ID {
		t.Fatal("MCP reply lost DM thread")
	}
	call("send", map[string]any{"target": original.ID, "body": "reply from ChatGPT", "idempotency_key": "reply-1"})
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM messages WHERE body=?", []byte("reply from ChatGPT")).Scan(&count)
	if count != 1 {
		t.Fatal("MCP retry duplicated reply")
	}
	call("list_channels", map[string]any{})
	call("watch", map[string]any{"target": "@me", "timeout": 0.01})
	var principalID string
	if err := db.QueryRow("SELECT principal_id FROM oauth_requests WHERE id=?", string(attributes[1])).Scan(&principalID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE principals SET status='revoked' WHERE id=?", principalID); err != nil {
		t.Fatal(err)
	}
	status, _ = postJSON("/mcp", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"}, token)
	if status != 401 {
		t.Fatalf("revoked MCP token still worked: %d", status)
	}
}
