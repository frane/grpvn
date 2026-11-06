package internal

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDesktopWorkspaceSignInReusesHumanProfile(t *testing.T) {
	remoteDB := newTestDB(t)
	remote := httptest.NewUnstartedServer(nil)
	remoteBase := "http://" + remote.Listener.Addr().String()
	h, err := CoordinatorHandler(remoteDB, CoordinatorOptions{PublicURL: remoteBase, Web: true})
	if err != nil {
		t.Fatal(err)
	}
	remote.Config.Handler = h
	remote.Start()
	defer remote.Close()
	localDB := newTestDB(t)
	localH, err := CoordinatorHandler(localDB, CoordinatorOptions{LocalUI: true})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(localH)
	defer local.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, address, origin string, data any) (int, []byte, http.Header) {
		t.Helper()
		var body io.Reader
		if data != nil {
			b, _ := json.Marshal(data)
			body = bytes.NewReader(b)
		}
		r, _ := http.NewRequest(method, address, body)
		if data != nil {
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
		}
		response, err := browser.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(response.Body)
		return response.StatusCode, b, response.Header
	}
	call := func(method, address, origin string, data any) map[string]any {
		t.Helper()
		status, b, _ := request(method, address, origin, data)
		if status < 200 || status >= 300 {
			t.Fatalf("%s: %d %s", address, status, b)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	_, invite, err := CreateInvite(remoteDB, "Owner", []string{"#general", "@*"}, true, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	call("POST", remoteBase+"/v1/accounts", remoteBase, map[string]any{"Name": "frane", "Password": "a durable human password", "Invitation": invite})
	profile := call("GET", remoteBase+"/v1/auth/session", "", nil)["profile"].(map[string]any)
	ticket, err := LocalHumanTicket(localDB, "local-person")
	if err != nil {
		t.Fatal(err)
	}
	call("POST", local.URL+"/v1/auth/local-session", local.URL, map[string]string{"Ticket": ticket})
	flow := call("POST", local.URL+"/v1/workspace-connections", local.URL, map[string]string{"URL": remoteBase})
	authorize := flow["url"].(string)
	status, _, headers := request("GET", authorize, "", nil)
	if status != 303 {
		t.Fatal("existing human browser was not recognized", status)
	}
	wait := remoteBase + headers.Get("Location")
	status, page, _ := request("GET", wait, "", nil)
	if status != 200 || !strings.Contains(string(page), "Continue as frane") || strings.Contains(string(page), "Approval code:") {
		t.Fatalf("unexpected human connection screen: %d %s", status, page)
	}
	u, _ := url.Parse(wait)
	params := u.Query()
	form := url.Values{"request": {params.Get("request")}, "ticket": {params.Get("ticket")}}
	r, _ := http.NewRequest("POST", remoteBase+"/oauth/continue", strings.NewReader(form.Encode()))
	r.Header.Set("Origin", remoteBase)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	accepted, err := browser.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Body.Close()
	if accepted.StatusCode != 303 {
		t.Fatal(accepted.StatusCode)
	}
	status, _, headers = request("GET", wait, "", nil)
	if status != 303 {
		t.Fatal(status)
	}
	callback := headers.Get("Location")
	if !strings.HasPrefix(callback, local.URL+"/oauth/callback?") {
		t.Fatal("callback changed origin")
	}
	status, page, _ = request("GET", callback, "", nil)
	if status != 200 || !strings.Contains(string(page), "Connected") {
		t.Fatalf("callback %d %s", status, page)
	}
	state := call("GET", local.URL+"/v1/workspace-connections/"+flow["attempt"].(string), "", nil)
	if state["status"] != "connected" {
		t.Fatal(state)
	}
	var registeredID, name string
	if err := localDB.QueryRow("SELECT principal_id,name FROM remote_human_sessions").Scan(&registeredID, &name); err != nil || registeredID != profile["id"] || name != "frane" {
		t.Fatalf("profile changed %s %s %v", registeredID, name, err)
	}
	target := state["target"].(string)
	if !strings.HasSuffix(target, "/general") {
		t.Fatal("accessible channels were not discovered", state)
	}
	status, file := uploadTestFile(t, browser, local.URL, target, "", "desktop.go", []byte("package main\nfunc main() {}\n"))
	if status != 201 {
		t.Fatal("desktop upload", status, file)
	}
	if remoteFile, err := loadChatFile(remoteDB, file["id"].(string), "#general"); err != nil || remoteFile.Name != "desktop.go" {
		t.Fatal("desktop upload was not stored by the coordinator", err)
	}
	call("POST", local.URL+"/v1/chat/messages", local.URL, map[string]string{"Target": target, "Body": "From the desktop, same human", "IdempotencyKey": "native-human-profile"})
	var alias string
	_ = localDB.QueryRow("SELECT name FROM remotes").Scan(&alias)
	mustSync(t, localDB, alias)
	m := messageIn(t, remoteDB, "#general", "From the desktop, same human")
	var author string
	_ = remoteDB.QueryRow("SELECT principal_id FROM message_principals WHERE message_id=?", m.ID).Scan(&author)
	if author != profile["id"] {
		t.Fatal("desktop posted as another profile", author)
	}
	created := "#" + alias + "/desktop-project"
	call("POST", local.URL+"/v1/chat/channels", local.URL, map[string]any{"Channel": created, "Open": false})
	invitation := call("POST", local.URL+"/v1/chat/invitations", local.URL, map[string]any{"Channel": created, "Write": true})
	if !strings.HasPrefix(invitation["url"].(string), remoteBase+"/join?") {
		t.Fatal("desktop invitation did not use the remote workspace", invitation)
	}
	call("POST", local.URL+"/v1/chat/messages", local.URL, map[string]string{"Target": created, "Body": "Created from the desktop", "IdempotencyKey": "native-human-create"})
	mustSync(t, localDB, alias)
	messageIn(t, remoteDB, "#desktop-project", "Created from the desktop")
	management := local.URL + "/v1/chat/channels?workspace=" + url.QueryEscape(alias)
	call("POST", local.URL+"/v1/chat/memberships?workspace="+url.QueryEscape(alias), local.URL, map[string]string{"Channel": created, "Action": "leave"})
	call("POST", remoteBase+"/v1/chat/messages", remoteBase, map[string]string{"Target": "#desktop-project", "Body": "History after the desktop left", "IdempotencyKey": "archived-history"})
	call("PATCH", management, local.URL, map[string]any{"Channel": created, "Archived": true})
	if !channelArchived(remoteDB, "#desktop-project") || !channelArchived(localDB, created) {
		t.Fatal("desktop did not archive the remote channel")
	}
	call("GET", local.URL+"/v1/chat/messages?target="+url.QueryEscape(created)+"&refresh=1", "", nil)
	messageIn(t, localDB, created, "History after the desktop left")
	var localOwner string
	_ = localDB.QueryRow("SELECT local_principal_id FROM remote_human_sessions").Scan(&localOwner)
	if channelJoined(localDB, localOwner, created) {
		t.Fatal("viewing archived history rejoined the channel")
	}
	call("PATCH", management, local.URL, map[string]any{"Channel": created, "Archived": false})
	call("POST", local.URL+"/v1/chat/memberships?workspace="+url.QueryEscape(alias), local.URL, map[string]string{"Channel": created, "Action": "join"})
	if err := GrantWorkspaceChannels(remoteDB, profile["id"].(string), []string{"#later-read-only"}, false); err != nil {
		t.Fatal(err)
	}
	_, _ = localDB.Exec("UPDATE chat_metadata SET value=0 WHERE key LIKE 'remote:%'")
	mustSync(t, localDB, alias)
	later := "#" + alias + "/later-read-only"
	if status, _, _ := request("POST", local.URL+"/v1/chat/messages", local.URL, map[string]string{"Target": later, "Body": "Must not be queued"}); status != 403 {
		t.Fatal("desktop ignored changed write permission", status)
	}
	if _, err := remoteDB.Exec("DELETE FROM permissions WHERE principal_id=? AND channel='#later-read-only'", profile["id"]); err != nil {
		t.Fatal(err)
	}
	_, _ = localDB.Exec("UPDATE chat_metadata SET value=0 WHERE key LIKE 'remote:%'")
	mustSync(t, localDB, alias)
	if status, _, _ := request("GET", local.URL+"/v1/chat/messages?target="+url.QueryEscape(later), "", nil); status != 403 {
		t.Fatal("revoked remote read permission remained visible", status)
	}
	if _, err = localDB.Exec("UPDATE remote_human_sessions SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	mustSync(t, localDB, alias)
	reused := call("POST", local.URL+"/v1/workspace-connections", local.URL, map[string]string{"URL": remoteBase})
	if reused["status"] != "connected" {
		t.Fatal("reconnect asked for another sign-in", reused)
	}
	var count int
	_ = remoteDB.QueryRow("SELECT COUNT(*) FROM principals WHERE kind='human'").Scan(&count)
	if count != 1 {
		t.Fatal("reconnect created another human", count)
	}
	status, _, _ = request("GET", callback, "", nil)
	if status != 400 {
		t.Fatal("authorization callback was replayed")
	}
}
