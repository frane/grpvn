package internal

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHumanChatsShareAgentPermissionsAndThreads(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "agent-token", "reef", []string{"#general", "@*"})
	_, _ = db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name='reef'")
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	human := &http.Client{Jar: jar}
	call := func(client *http.Client, method, path string, body any, origin string) (int, map[string]any) {
		t.Helper()
		var payload io.Reader
		if body != nil {
			v, _ := json.Marshal(body)
			payload = bytes.NewReader(v)
		}
		r, _ := http.NewRequest(method, server.URL+path, payload)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", origin)
		}
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(res.Body).Decode(&result)
		return res.StatusCode, result
	}
	_, invite, err := CreateInvite(db, "Human test", []string{"#general", "@*"}, true, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	signup := map[string]any{"Name": "frane", "Password": "a long test password", "Invitation": invite}
	if code, _ := call(human, "POST", "/v1/accounts", signup, "https://foreign.invalid"); code != 403 {
		t.Fatal("signup accepted a cross-origin mutation")
	}
	if code, result := call(human, "POST", "/v1/accounts", signup, server.URL); code != 201 {
		t.Fatal(code, result)
	}
	code, session := call(human, "GET", "/v1/auth/session", nil, "")
	if code != 200 || session["authenticated"] != true {
		t.Fatal(session)
	}
	profile := session["profile"].(map[string]any)
	id := profile["id"].(string)
	if profile["name"] != "frane" || profile["kind"] != "human" {
		t.Fatal(profile)
	}
	root, failed := remoteTool(t, h, "agent-token", "send", map[string]any{"target": "#general", "body": "Can you review this?"})
	if failed {
		t.Fatal(root)
	}
	code, page := call(human, "GET", "/v1/chat/messages?target=%23general", nil, "")
	if code != 200 || len(page["messages"].([]any)) != 1 {
		t.Fatal(code, page)
	}
	send := map[string]any{"Target": root["id"], "Body": "Human review complete", "IdempotencyKey": "human-reply"}
	code, reply := call(human, "POST", "/v1/chat/messages", send, server.URL)
	if code != 201 {
		t.Fatal(code, reply)
	}
	_, retry := call(human, "POST", "/v1/chat/messages", send, server.URL)
	if retry["id"] != reply["id"] || retry["replayed"] != true {
		t.Fatal("human retry duplicated", retry)
	}
	m := messageIn(t, db, "#general", "Human review complete")
	if m.ParentID == nil || *m.ParentID != root["id"] || m.Sender != "frane" {
		t.Fatal(m)
	}
	_, page = call(human, "GET", "/v1/chat/messages?target=%23general", nil, "")
	messages := page["messages"].([]any)
	if len(messages) != 2 || messages[1].(map[string]any)["kind"] != "human" {
		t.Fatal("UI hid or misattributed own message", page)
	}
	if code, _ := call(human, "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Position": m.Seq}, server.URL); code != 200 {
		t.Fatal(code)
	}
	var agentCursor int
	_ = db.QueryRow("SELECT COUNT(*) FROM cursors WHERE agent_name='reef'").Scan(&agentCursor)
	if agentCursor != 0 {
		t.Fatal("human reading advanced agent cursor")
	}
	if code, _ := call(human, "POST", "/v1/chat/channels", map[string]any{"Channel": "#human-project"}, server.URL); code != 201 {
		t.Fatal(code)
	}
	if code, _ := call(human, "POST", "/v1/chat/invitations", map[string]any{"Channel": "#human-project", "Agent": "@reef"}, server.URL); code != 200 {
		t.Fatal(code)
	}
	if v, failed := remoteTool(t, h, "agent-token", "send", map[string]any{"target": "#human-project", "body": "Agent joins immediately"}); failed {
		t.Fatal(v)
	}
	if code, _ := call(human, "POST", "/v1/chat/messages", map[string]any{"Target": "@reef", "Body": "Direct human message", "IdempotencyKey": "human-dm"}, server.URL); code != 201 {
		t.Fatal(code)
	}
	if v, failed := remoteTool(t, h, "agent-token", "read", map[string]any{"target": "@me", "peek": true}); failed || len(v["messages"].([]any)) != 1 {
		t.Fatal(v)
	}
	if code, page := call(human, "GET", "/v1/chat/messages?target=%40reef", nil, ""); code != 200 || len(page["messages"].([]any)) != 1 {
		t.Fatal(code, page)
	}
	// Password login retains the human profile across browsers and restarts.
	second := &http.Client{Jar: func() *cookiejar.Jar { j, _ := cookiejar.New(nil); return j }()}
	if code, _ := call(second, "POST", "/v1/auth/login", map[string]any{"Name": "frane", "Password": "a long test password"}, server.URL); code != 200 {
		t.Fatal(code)
	}
	_, again := call(second, "GET", "/v1/auth/session", nil, "")
	if again["profile"].(map[string]any)["id"] != id {
		t.Fatal("login created a new profile")
	}
	if code, _ := call(human, "POST", "/v1/auth/logout", map[string]any{}, "https://foreign.invalid"); code != 403 {
		t.Fatal("cross-site logout accepted")
	}
	if _, err := db.Exec("UPDATE principals SET status='revoked' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(second, "GET", "/v1/chat/messages?target=%23general", nil, ""); code != 401 {
		t.Fatal("revoked human session still works")
	}
}

func TestHumanReadOnlyInvitationAndLocalBootstrap(t *testing.T) {
	db := newTestDB(t)
	access, _ := newChannelAccess(CoordinatorOptions{Web: true})
	access.db = db
	_, secret, err := CreateInvite(db, "Reader", []string{"#read-only"}, false, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := &webServer{db: db, access: access, attempts: map[string][]time.Time{}}
	signup := func(invite string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]string{"Name": "observer", "Password": "sufficiently long password", "Invitation": invite})
		r := httptest.NewRequest("POST", "/v1/accounts", bytes.NewReader(payload))
		w := httptest.NewRecorder()
		s.signup(w, r)
		return w
	}
	w := signup(secret)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range w.Result().Cookies() {
		if c.Name == webSessionCookie {
			r.AddCookie(c)
		}
	}
	p := s.principal(r)
	if p == nil {
		t.Fatal("human cookie not persisted")
	}
	if s.can(p, "#read-only", true) || s.can(p, "#someone-else", false) {
		t.Fatal("read-only invitation exceeded scope")
	}
	if s.can(p, "@someone", true) {
		t.Fatal("read-only invite gained DMs")
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM principals WHERE kind='human'").Scan(&count)
	w = signup(secret)
	if w.Code == 201 {
		t.Fatal("invitation reused")
	}
	var after int
	_ = db.QueryRow("SELECT COUNT(*) FROM principals WHERE kind='human'").Scan(&after)
	if after != count {
		t.Fatal("failed signup left a phantom profile")
	}
	ticket, err := LocalHumanTicket(db, "local-owner")
	if err != nil {
		t.Fatal(err)
	}
	s.local = true
	bootstrap := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/auth/local-session", strings.NewReader(`{"Ticket":"`+ticket+`"}`))
		w := httptest.NewRecorder()
		s.bootstrap(w, r)
		return w
	}
	if w := bootstrap(); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if bootstrap().Code != 401 {
		t.Fatal("one-use local sign-in ticket reused")
	}
}
