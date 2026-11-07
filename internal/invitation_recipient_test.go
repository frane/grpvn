package internal

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSmartInvitationRecipients(t *testing.T) {
	db := newTestDB(t)
	for _, name := range []string{"owner", "person", "agent", "reader"} {
		seedTestAgent(t, db, name+"-token", name, []string{"#elsewhere"})
	}
	for _, statement := range []string{
		"UPDATE principals SET kind='human' WHERE name='person'",
		"INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='owner'",
		"INSERT INTO permissions(principal_id,channel,can_read,can_write) SELECT id,'#project',1,1 FROM principals WHERE name='owner'",
		"INSERT INTO permissions(principal_id,channel,can_read,can_write) SELECT id,'#project',1,0 FROM principals WHERE name='reader'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true, PublicURL: "https://chat.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	var before int
	if err := db.QueryRow("SELECT COUNT(*) FROM principals").Scan(&before); err != nil {
		t.Fatal(err)
	}
	post := func(field, recipient string, want int) workspaceInvitation {
		t.Helper()
		status, body := accessRequest(t, s, "POST", "/v1/chat/invitations", "owner-token", map[string]any{"Channel": "#project", field: recipient, "Write": false})
		if status != want {
			t.Fatalf("%s: %d %s", recipient, status, body)
		}
		var result workspaceInvitation
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, name := range []string{"person", "agent"} {
		var id string
		if err := db.QueryRow("SELECT id FROM principals WHERE name=?", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, recipient := range []string{"@" + name, id} {
			result := post("Recipient", recipient, 200)
			if result.Kind != "member" || result.ID != id || result.URL != "" {
				t.Fatalf("existing profile replaced: %+v", result)
			}
		}
		if value, failed := remoteTool(t, h, name+"-token", "read", map[string]any{"target": "#project"}); failed {
			t.Fatal(value)
		}
		if value, failed := remoteTool(t, h, name+"-token", "send", map[string]any{"target": "#project", "body": "must remain read only"}); !failed {
			t.Fatal(value)
		}
	}
	for _, recipient := range []string{"New person", "someone@example.test"} {
		// Agent is retained for clients that have not updated their field name.
		result := post("Agent", recipient, 201)
		if result.Kind != "invitation" || result.Status != "invitation_ready" || result.Recipient != recipient || result.Delivery != "link" {
			t.Fatalf("new recipient: %+v", result)
		}
		link, err := url.Parse(result.URL)
		if err != nil {
			t.Fatal(err)
		}
		var channels string
		var write bool
		var uses int
		if err := db.QueryRow("SELECT channels,can_write,uses_left FROM invites WHERE secret_hash=?", credentialHash(link.Query().Get("invite"))).Scan(&channels, &write, &uses); err != nil {
			t.Fatal(err)
		}
		if channels != `["#project"]` || write || uses != 1 {
			t.Fatalf("incorrect scope %s %v %d", channels, write, uses)
		}
	}
	value, failed := remoteTool(t, h, "owner-token", "invite", map[string]any{"channel": "#project", "recipient": "Agent friend <agent@example.test>"})
	if failed || value["email"] != "agent@example.test" || value["url"] == "" {
		t.Fatal(value)
	}
	for _, token := range []string{"reader-token", "person-token"} {
		if value, failed := remoteTool(t, h, token, "invite", map[string]any{"channel": "#project", "recipient": "new@example.test"}); !failed {
			t.Fatalf("read-only member issued invitation: %v", value)
		}
	}
	for _, recipient := range []string{"bad@@example.test", "a@example.test,b@example.test", "a@example.test\nBcc: b@example.test"} {
		status, body := accessRequest(t, s, "POST", "/v1/chat/invitations", "owner-token", map[string]any{"Channel": "#project", "Recipient": recipient})
		if status != 400 || strings.Contains(body, "agent not found") {
			t.Fatalf("invalid recipient: %d %s", status, body)
		}
	}
	var after int
	if err := db.QueryRow("SELECT COUNT(*) FROM principals").Scan(&after); err != nil || after != before {
		t.Fatalf("invitation created profiles: %d -> %d (%v)", before, after, err)
	}
}

func TestSmartInvitationDoesNotReplaceRevokedProfile(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "owner-token", "owner", []string{"#project"})
	seedTestAgent(t, db, "peer-token", "peer", nil)
	if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='owner'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE principals SET status='revoked' WHERE name='peer'"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow("SELECT id FROM principals WHERE name='peer'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: "https://chat.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if result, failed := remoteTool(t, h, "owner-token", "invite", map[string]any{"channel": "#project", "recipient": id}); !failed {
		t.Fatal(result)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM invites").Scan(&count); err != nil || count != 0 {
		t.Fatalf("revoked identity replaced with link: %d %v", count, err)
	}
}
