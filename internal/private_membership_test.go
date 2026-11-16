package internal

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceRoleDoesNotJoinPrivateChannelsAndMembersCanBeRemoved(t *testing.T) {
	s, _, call := inboxFixture(t)
	seedTestAgent(t, s.db, "human-token", "frane", []string{"*"})
	if _, err := s.db.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1) ON CONFLICT(principal_id) DO UPDATE SET approved=1", credentialHash("human-token")[:32]); err != nil {
		t.Fatal(err)
	}
	seedTestAgent(t, s.db, "other-admin", "other-agent", []string{"*"})
	other, err := findPrincipal(s.db, "id=?", credentialHash("other-admin")[:32])
	if err != nil {
		t.Fatal(err)
	}
	code, body := call("POST", "/v1/chat/channels", map[string]any{"Channel": "#private"})
	if code != 201 {
		t.Fatal(code, body)
	}
	if principalPermission(s.db, other, "#private", false) {
		t.Fatal("workspace role enrolled an unrelated agent")
	}
	_ = cachePrincipal(s.db, other)
	if principalPermission(s.db, other, "#private", false) {
		t.Fatal("cached role bypassed private membership")
	}
	owner, err := findPrincipal(s.db, "id=?", credentialHash("human-token")[:32])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InviteWorkspaceAgent(s.db, s.access, owner, "#private", "other-agent", true); err != nil {
		t.Fatal(err)
	}
	other, _ = findPrincipal(s.db, "id=?", other.ID)
	if !principalPermission(s.db, other, "#private", true) {
		t.Fatal("explicit invite did not join")
	}
	code, body = call("GET", "/v1/chat/memberships?channel=%23private", nil)
	if code != 200 || len(body["members"].([]any)) != 2 {
		t.Fatal(code, body)
	}
	code, body = call("DELETE", "/v1/chat/memberships", map[string]any{"Channel": "#private", "MemberID": other.ID})
	if code != 200 {
		t.Fatal(code, body)
	}
	other, _ = findPrincipal(s.db, "id=?", other.ID)
	if principalPermission(s.db, other, "#private", false) || principalPermission(s.db, other, "#private", true) {
		t.Fatal("removed administrator retained private channel access")
	}
	if !principalPermission(s.db, other, "*", true) {
		t.Fatal("channel removal revoked workspace role")
	}
	if ensureSendChannel(s.db, s.access, other, "#private") {
		t.Fatal("removed agent silently rejoined private channel")
	}
}

func TestWorkspaceAdministrationCannotGrantAnUnjoinedPrivateChannel(t *testing.T) {
	s, handler, call := inboxFixture(t)
	seedTestAgent(t, s.db, "human-token", "frane", []string{"*"})
	_, _ = s.db.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1)", credentialHash("human-token")[:32])
	if code, body := call("POST", "/v1/chat/channels", map[string]any{"Channel": "#private"}); code != 201 {
		t.Fatal(code, body)
	}
	seedTestAgent(t, s.db, "outsider-admin", "outsider", []string{"*"})
	seedTestAgent(t, s.db, "peer", "peer", []string{"#private"})
	peer := credentialHash("peer")[:32]
	server := httptest.NewServer(handler)
	defer server.Close()
	if code, body := call("PATCH", "/v1/members/"+peer, map[string]any{"Admin": true}); code != 200 {
		t.Fatal(code, body)
	}
	member, _ := findPrincipal(s.db, "id=?", peer)
	if !principalPermission(s.db, member, "#private", true) {
		t.Fatal("role-only update erased a private membership the caller can manage")
	}
	for _, attempt := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/v1/members/" + peer, map[string]any{"Channels": []string{"#private"}, "Write": false}},
		{"POST", "/v1/access-approvals", map[string]any{"Code": "ANYCODE", "Channels": []string{"#private"}}},
		{"DELETE", "/v1/chat/memberships", map[string]any{"Channel": "#private", "MemberID": peer}},
	} {
		if status, raw := accessRequest(t, server, attempt.method, attempt.path, "outsider-admin", attempt.body); status != 403 {
			t.Fatal(attempt.method, attempt.path, status, raw)
		}
	}
	// A workspace-role change must not erase hidden private memberships.
	if status, raw := accessRequest(t, server, "PATCH", "/v1/members/"+peer, "outsider-admin", map[string]any{"Admin": true}); status != 200 {
		t.Fatal(status, raw)
	}
	p, _ := findPrincipal(s.db, "id=?", peer)
	if !principalPermission(s.db, p, "#private", true) {
		t.Fatal("workspace-role edit erased an uneditable private grant")
	}
	if status, raw := accessRequest(t, server, "GET", "/v1/members", "outsider-admin", nil); status != 200 || strings.Contains(raw, "#private") {
		t.Fatal("private channel names leaked through workspace administration", status, raw)
	}
	if status, raw := accessRequest(t, server, "POST", "/v1/chat/memberships", "peer", map[string]any{"Channel": "#private", "Action": "leave"}); status != 200 {
		t.Fatal(status, raw)
	}
	p, _ = findPrincipal(s.db, "id=?", peer)
	if principalPermission(s.db, p, "#private", false) {
		t.Fatal("leaving retained private access")
	}
}

func TestPrivateMembershipMigrationPreservesAuthorsOnlyAndRunsOnce(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "author", "active-agent", []string{"*"})
	seedTestAgent(t, db, "unrelated", "unrelated-agent", []string{"*"})
	seedTestAgent(t, db, "old-owner", "owner", []string{"*", "#project"})
	seedTestAgent(t, db, "copied-child", "copied-agent", []string{"*", "#project"})
	if _, err := db.Exec("UPDATE principals SET parent_id=? WHERE id=?", credentialHash("old-owner")[:32], credentialHash("copied-child")[:32]); err != nil {
		t.Fatal(err)
	}
	author := credentialHash("author")[:32]
	unrelated := credentialHash("unrelated")[:32]
	m, err := Send(db, "active-agent", "#project", "Existing collaboration", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO message_principals(message_id,principal_id) VALUES(?,?)", m.ID, author); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("DELETE FROM schema_version WHERE version>=4"); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	p, _ := findPrincipal(db, "id=?", author)
	q, _ := findPrincipal(db, "id=?", unrelated)
	if !principalPermission(db, p, "#project", true) || principalPermission(db, q, "#project", false) {
		t.Fatal("migration mixed participation with workspace role")
	}
	child, _ := findPrincipal(db, "id=?", credentialHash("copied-child")[:32])
	if principalPermission(db, child, "#project", false) {
		t.Fatal("unused copied owner grant silently enrolled another agent")
	}
	if _, err = db.Exec("DELETE FROM permissions WHERE principal_id=? AND channel='#project'", author); err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	p, _ = findPrincipal(db, "id=?", author)
	if principalPermission(db, p, "#project", false) {
		t.Fatal("historical posts undid a channel removal")
	}
}

func TestPublicWorkspaceChannelsAndAutomaticGeneralMembership(t *testing.T) {
	s, _, call := inboxFixture(t)
	owner := credentialHash("human-token")[:32]
	seedTestAgent(t, s.db, "human-token", "frane", []string{"*"})
	if _, err := s.db.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1) ON CONFLICT(principal_id) DO UPDATE SET approved=1", owner); err != nil {
		t.Fatal(err)
	}
	seedTestAgent(t, s.db, "new-member", "new-member", nil)
	peerID := credentialHash("new-member")[:32]
	if _, err := s.db.Exec("UPDATE principals SET status='pending',approval_code='JOINTEST' WHERE id=?", peerID); err != nil {
		t.Fatal(err)
	}
	if err := ApproveMember(s.db, "JOINTEST", "", []string{"@*"}, true); err != nil {
		t.Fatal(err)
	}
	peer, _ := findPrincipal(s.db, "id=?", peerID)
	if !principalPermission(s.db, peer, "#general", true) || !channelJoined(s.db, peer.ID, "#general") || !s.access.isPublic("#general") || s.access.isOpen("#general") {
		t.Fatal("general is not an automatic workspace public channel")
	}
	code, body := call("POST", "/v1/chat/channels", map[string]any{"Channel": "#lobby", "Open": true})
	if code != 201 {
		t.Fatal(code, body)
	}
	if !rpcAllowed(s.db, s.access, peer, "#lobby", false) || principalPermission(s.db, peer, "#lobby", true) {
		t.Fatal("public discovery was confused with membership")
	}
	if err := JoinWorkspaceChannel(s.db, s.access, peer, "#lobby", ""); err != nil {
		t.Fatal(err)
	}
	if !principalPermission(s.db, peer, "#lobby", true) {
		t.Fatal("workspace member could not join public channel")
	}
	code, body = call("POST", "/v1/chat/channels", map[string]any{"Channel": "#secret"})
	if code != 201 {
		t.Fatal(code, body)
	}
	if err := JoinWorkspaceChannel(s.db, s.access, peer, "#secret", ""); err == nil {
		t.Fatal("public member joined a private channel without an invitation")
	}
	if _, err := s.db.Exec("UPDATE permissions SET can_write=0 WHERE principal_id=? AND channel='#lobby'", peer.ID); err != nil {
		t.Fatal(err)
	}
	peer, _ = findPrincipal(s.db, "id=?", peer.ID)
	if err := JoinWorkspaceChannel(s.db, s.access, peer, "#lobby", ""); err != nil {
		t.Fatal(err)
	}
	if rpcAllowed(s.db, s.access, peer, "#lobby", true) || ensureSendChannel(s.db, s.access, peer, "#lobby") {
		t.Fatal("rejoining upgraded read-only access")
	}
}

func TestRegisteredAgentDoesNotInheritOwnerPrivateRoomsOrAdministratorRole(t *testing.T) {
	db := newTestDB(t)
	access, _ := newChannelAccess(CoordinatorOptions{})
	access.db = db
	seedTestAgent(t, db, "owner", "owner", []string{"*", "#general", "#secret"})
	id := credentialHash("owner")[:32]
	if _, err := db.Exec("UPDATE principals SET kind='member' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) VALUES(?,1)", id); err != nil {
		t.Fatal(err)
	}
	owner, _ := findPrincipal(db, "id=?", id)
	agent, err := registerMember(db, access, registrationRequest{Credential: "a sufficiently long unique child credential", Name: "new-agent", Channels: []string{"#general"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !principalPermission(db, agent, "#general", true) || principalPermission(db, agent, "#secret", false) || principalPermission(db, agent, "*", true) {
		t.Fatal("agent inherited unrelated private rooms or workspace administration")
	}
}
