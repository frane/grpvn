package internal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatAdministrationAndChannelLifecycle(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "owner-token", "owner", []string{"*"})
	seedTestAgent(t, db, "peer-token", "peer", []string{"#project", "@*"})
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	peer, err := findPrincipal(db, "name=?", "peer")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, token string, body any) map[string]any {
		t.Helper()
		status, raw := accessRequest(t, server, method, path, token, body)
		if status < 200 || status >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, status, raw)
		}
		var result map[string]any
		if json.Unmarshal([]byte(raw), &result) != nil {
			t.Fatal(raw)
		}
		return result
	}
	if status, _ := accessRequest(t, server, "GET", "/v1/members", "peer-token", nil); status != 403 {
		t.Fatal("nonadmin saw workspace grants", status)
	}
	call("PATCH", "/v1/members/"+peer.ID, "owner-token", map[string]any{"Channels": []string{"#project"}, "Write": false, "DM": true})
	if _, failed := remoteTool(t, h, "peer-token", "send", map[string]any{"target": "#project", "body": "denied"}); !failed {
		t.Fatal("updated read-only permission was ignored")
	}
	call("PATCH", "/v1/members/"+peer.ID, "owner-token", map[string]any{"Channels": []string{"#project"}, "Write": true})
	call("PATCH", "/v1/members/"+peer.ID, "owner-token", map[string]any{"Grants": []map[string]any{{"Channel": "#project", "Write": true}, {"Channel": "#audit", "Write": false}}})
	if _, failed := remoteTool(t, h, "peer-token", "send", map[string]any{"target": "#audit", "body": "Read-only channel"}); !failed {
		t.Fatal("mixed access promoted a read-only channel")
	}
	call("PATCH", "/v1/members/"+peer.ID, "owner-token", map[string]any{"Channels": []string{"#project"}, "Write": true})
	call("POST", "/v1/chat/memberships", "peer-token", map[string]string{"Channel": "#project", "Action": "leave"})
	if len(call("GET", "/v1/chat/channels", "peer-token", nil)["channels"].([]any)) != 0 {
		t.Fatal("left channel remains in sidebar")
	}
	if len(call("GET", "/v1/chat/channels?browse=1", "peer-token", nil)["channels"].([]any)) != 1 {
		t.Fatal("channel cannot be rejoined")
	}
	call("POST", "/v1/chat/memberships", "peer-token", map[string]string{"Channel": "#project", "Action": "join"})
	if len(call("GET", "/v1/chat/channels", "peer-token", nil)["channels"].([]any)) != 1 {
		t.Fatal("rejoined channel missing")
	}
	if status, _ := accessRequest(t, server, "PATCH", "/v1/chat/channels", "peer-token", map[string]any{"Channel": "#project", "Archived": true}); status != 403 {
		t.Fatal("nonowner archived channel")
	}
	call("PATCH", "/v1/chat/channels", "owner-token", map[string]any{"Channel": "#project", "Archived": true})
	if err := NewMessage("old-cli", "#project", []byte("Older client bypass")).Save(db); err == nil {
		t.Fatal("database did not enforce archive for older clients")
	}
	if _, err := Send(db, "local-agent", "#project", "blocked locally", "", false); err == nil {
		t.Fatal("local CLI bypassed archive")
	}
	if _, failed := remoteTool(t, h, "peer-token", "send", map[string]any{"target": "#project", "body": "blocked over MCP"}); !failed {
		t.Fatal("MCP bypassed archive")
	}
	if _, err := acceptRemoteMessage(db, toWire(NewMessage("peer", "#project", []byte("blocked over sync")))); err == nil {
		t.Fatal("sync bypassed archive")
	}
	archived := call("GET", "/v1/chat/channels?browse=1", "peer-token", nil)["channels"].([]any)[0].(map[string]any)
	if archived["archived"] != true || archived["write"] != false {
		t.Fatal("archive not visible", archived)
	}
	call("PATCH", "/v1/chat/channels", "owner-token", map[string]any{"Channel": "#project", "Archived": false})
	if _, failed := remoteTool(t, h, "peer-token", "send", map[string]any{"target": "#project", "body": "reopened"}); failed {
		t.Fatal("cannot post after reopening")
	}
	owner, _ := findPrincipal(db, "name=?", "owner")
	if status, _ := accessRequest(t, server, "DELETE", "/v1/members/"+owner.ID, "owner-token", nil); status != http.StatusConflict {
		t.Fatal("admin could revoke itself")
	}
	call("DELETE", "/v1/members/"+peer.ID, "owner-token", nil)
	if status, _ := accessRequest(t, server, "GET", "/v1/chat/channels", "peer-token", nil); status != 401 {
		t.Fatal("removed agent still authenticates", status)
	}
}

func TestLocalAgentRemovalAndRestorationPreserveHistory(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "admin-token", "admin", []string{"*"})
	_, _ = db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='admin'")
	message, err := Send(db, "local-agent", "#project", "Existing history", "", false)
	if err != nil {
		t.Fatal(err)
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{LocalUI: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	if status, body := accessRequest(t, server, "DELETE", "/v1/members/local:local-agent", "admin-token", nil); status != 200 {
		t.Fatal(status, body)
	}
	if _, err := Send(db, "local-agent", "#project", "Removed agents cannot post", "", false); err == nil {
		t.Fatal("removed local agent posted")
	}
	if err := NewMessage("local-agent", "#project", []byte("Older client bypass")).Save(db); err == nil {
		t.Fatal("database did not enforce local removal for older clients")
	}
	if _, err := findMessageByID(db, message.ID); err != nil {
		t.Fatal("removing an agent deleted history", err)
	}
	if status, body := accessRequest(t, server, "PATCH", "/v1/members/local:local-agent", "admin-token", map[string]bool{"Active": true}); status != 200 {
		t.Fatal(status, body)
	}
	if _, err := Send(db, "local-agent", "#project", "Restored agent", "", false); err != nil {
		t.Fatal(err)
	}
	if status, body := accessRequest(t, server, "PATCH", "/v1/members/local:local-agent", "admin-token", map[string]any{"Channels": []string{"#project"}, "Write": true}); status != 200 {
		t.Fatal(status, body)
	}
	if _, err := Send(db, "local-agent", "#other", "Unpermitted channel", "", false); err == nil {
		t.Fatal("local channel restriction ignored")
	}
	if _, err := Send(db, "local-agent", "#project", "Permitted channel", "", false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if status, body := accessRequest(t, server, "POST", "/v1/chat/invitations", "admin-token", map[string]any{"Channel": "#other", "Agent": "local-agent", "Write": true}); status != 200 {
			t.Fatal("could not invite an existing local agent", status, body)
		}
	}
	if _, err := Send(db, "local-agent", "#other", "Invitation granted posting access", "", false); err != nil {
		t.Fatal(err)
	}
	var notices int
	_ = db.QueryRow("SELECT COUNT(*) FROM messages WHERE target='@local-agent'").Scan(&notices)
	if notices != 1 {
		t.Fatal("invitation did not notify exactly once", notices)
	}
}

func TestAgentAccessEditExtendsOwnerWithoutPromotingReadOnlyGrants(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "admin-token", "admin", []string{"*"})
	seedTestAgent(t, db, "owner-token", "device", []string{"#general"})
	seedTestAgent(t, db, "child-token", "worker", []string{"#general"})
	owner, _ := findPrincipal(db, "name=?", "device")
	child, _ := findPrincipal(db, "name=?", "worker")
	_, _ = db.Exec("UPDATE principals SET kind='member' WHERE id=?", owner.ID)
	_, _ = db.Exec("UPDATE principals SET parent_id=? WHERE id=?", owner.ID, child.ID)
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	status, body := accessRequest(t, server, "PATCH", "/v1/members/"+child.ID, "admin-token", map[string]any{"Grants": []map[string]any{{"Channel": "#new", "Write": true}, {"Channel": "#audit", "Write": false}}})
	if status != 200 {
		t.Fatal(status, body)
	}
	if _, failed := remoteTool(t, h, "child-token", "send", map[string]string{"target": "#new", "body": "Granted without a second owner approval"}); failed {
		t.Fatal("owner cap blocked the edited channel grant")
	}
	if _, failed := remoteTool(t, h, "child-token", "send", map[string]string{"target": "#audit", "body": "Must remain read-only"}); !failed {
		t.Fatal("read-only channel was promoted")
	}
}

func TestLocalChannelRosterIncludesAuthorsAndSupportsRemoval(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "admin-token", "admin", []string{"*"})
	_, _ = db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='admin'")
	_, _ = Send(db, "reef", "#project", "Existing local participant", "", false)
	_, _ = Send(db, "elsewhere", "#other", "Other channel participant", "", false)
	h, err := CoordinatorHandler(db, CoordinatorOptions{LocalUI: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	roster := func() map[string]bool {
		t.Helper()
		status, raw := accessRequest(t, server, "GET", "/v1/chat/memberships?channel=%23project", "admin-token", nil)
		if status != 200 {
			t.Fatal(status, raw)
		}
		var result struct{ Members []struct{ Name string } }
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, m := range result.Members {
			names[m.Name] = true
		}
		return names
	}
	if names := roster(); !names["reef"] || names["elsewhere"] {
		t.Fatal("incorrect local channel roster", names)
	}
	_, err = db.Exec("INSERT INTO agent_sessions(id,agent_name,runtime,runtime_session,heartbeat_at) VALUES('reef-session','reef','codex','runtime-session',1700000000000)")
	if err != nil {
		t.Fatal(err)
	}
	status, raw := accessRequest(t, server, "GET", "/v1/chat/participants?name=reef&channel=%23project", "admin-token", nil)
	if status != 200 {
		t.Fatal(status, raw)
	}
	var profile struct {
		People []struct {
			Name, Kind, Runtime string
			LastSeen            int64 `json:"last_seen"`
			LastMessage         int64 `json:"last_message"`
		}
	}
	if err := json.Unmarshal([]byte(raw), &profile); err != nil {
		t.Fatal(err)
	}
	if len(profile.People) != 1 || profile.People[0].Kind != "agent" || profile.People[0].Runtime != "codex" || profile.People[0].LastSeen != 1700000000000 || profile.People[0].LastMessage == 0 {
		t.Fatal("incorrect participant activity", raw)
	}
	status, raw = accessRequest(t, server, "DELETE", "/v1/chat/memberships", "admin-token", map[string]string{"Channel": "#project", "MemberID": "local:reef"})
	if status != 200 {
		t.Fatal(status, raw)
	}
	if roster()["reef"] {
		t.Fatal("removed local agent is still listed")
	}
	if _, err := Send(db, "reef", "#project", "Removed", "", false); err == nil {
		t.Fatal("removed agent posted")
	}
	if err := NewMessage("reef", "#project", []byte("Old writer")).Save(db); err == nil {
		t.Fatal("older writer bypassed channel removal")
	}
	if _, err := Send(db, "reef", "#other", "Other access is retained", "", false); err != nil {
		t.Fatal(err)
	}
	status, raw = accessRequest(t, server, "POST", "/v1/chat/invitations", "admin-token", map[string]any{"Channel": "#project", "Agent": "reef", "Write": true})
	if status != 200 {
		t.Fatal(status, raw)
	}
	if !roster()["reef"] {
		t.Fatal("reinvited agent missing")
	}
	if _, err := Send(db, "reef", "#project", "Rejoined", "", false); err != nil {
		t.Fatal(err)
	}
}
