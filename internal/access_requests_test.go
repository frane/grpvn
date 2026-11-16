package internal

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOAuthAdmissionSeparatesMetadataAndDeduplicatesAlerts(t *testing.T) {
	s, _, call := inboxFixture(t)
	seedTestAgent(t, s.db, "human-token", "frane", []string{"*"})
	seedTestAgent(t, s.db, "pending-token", "patient-macaque-bc3f", nil)
	profile := credentialHash("pending-token")[:32]
	raw, _ := json.Marshal([]string{"oauth:grpvn.read grpvn.write", "redirect:claude.ai", "client:Claude"})
	if _, err := s.db.Exec("UPDATE principals SET status='pending',requested=? WHERE id=?", string(raw), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO oauth_clients(id,name,redirects,created_at) VALUES('client','Claude','[]',0)"); err != nil {
		t.Fatal(err)
	}
	request := NewMessage("", "", nil).ID
	if _, err := s.db.Exec(`INSERT INTO oauth_requests(id,client_id,principal_id,ticket_hash,code_hash,redirect_uri,state,challenge,scope,resource,expires_at)
 VALUES(?,'client',?,'ticket','code','https://claude.ai/api/callback','state','challenge','grpvn.read grpvn.write','https://example.test/mcp',?)`, request, profile, time.Now().Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	before, _ := readChatRevision(t.Context(), s.db)
	if _, err := s.db.Exec("INSERT INTO oauth_request_approvals(request_id,approval_code,approved) VALUES(?,'TESTCODE',0)", request); err != nil {
		t.Fatal(err)
	}
	after, _ := readChatRevision(t.Context(), s.db)
	if after.Metadata <= before.Metadata {
		t.Fatal("request did not wake open clients")
	}
	code, body := call("GET", "/v1/access-requests", nil)
	if code != 200 {
		t.Fatal(code, body)
	}
	items := body["requests"].([]any)
	if len(items) != 1 {
		t.Fatal(body)
	}
	q := items[0].(map[string]any)
	if q["client"] != "Claude" || q["redirect"] != "claude.ai" || len(q["channels"].([]any)) != 0 || q["code"] != "TESTCODE" {
		t.Fatal("OAuth metadata became channel grants", q)
	}
	id := q["id"].(string)
	_, claim := call("PATCH", "/v1/access-requests", map[string]any{"IDs": []string{id}})
	if len(claim["claimed"].([]any)) != 1 {
		t.Fatal(claim)
	}
	_, claim = call("PATCH", "/v1/access-requests", map[string]any{"IDs": []string{id}})
	if len(claim["claimed"].([]any)) != 0 {
		t.Fatal("request alert replayed", claim)
	}
	var status string
	_ = s.db.QueryRow("SELECT status FROM principals WHERE id=?", profile).Scan(&status)
	if status != "pending" {
		t.Fatal("notification admitted an agent")
	}
	// A stale desktop may still submit the old mixed metadata array.
	code, body = call("POST", "/v1/access-approvals", map[string]any{"Code": "TESTCODE", "Channels": []string{"oauth:grpvn.read grpvn.write", "redirect:claude.ai", "client:Claude"}})
	if code != 200 {
		t.Fatal(code, body)
	}
	p, err := findPrincipal(s.db, "id=?", profile)
	if err != nil || !principalPermission(s.db, p, "#general", true) || !principalPermission(s.db, p, "@*", true) {
		t.Fatal("normal admission grants missing", err)
	}
	_, body = call("GET", "/v1/access-requests", nil)
	if len(body["requests"].([]any)) != 0 {
		t.Fatal("admitted request still counted", body)
	}
}
