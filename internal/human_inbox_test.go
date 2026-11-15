package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func inboxFixture(t *testing.T) (*webServer, http.Handler, func(string, string, any) (int, map[string]any)) {
	t.Helper()
	db := newTestDB(t)
	seedTestAgent(t, db, "human-token", "frane", []string{"#dev", "@*"})
	if _, err := db.Exec("UPDATE principals SET kind='human' WHERE name='frane'"); err != nil {
		t.Fatal(err)
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	access, _ := newChannelAccess(CoordinatorOptions{})
	access.db = db
	s := &webServer{db: db, access: access}
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer human-token")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(w.Code, w.Body.String(), err)
		}
		return w.Code, result
	}
	return s, h, call
}
func TestHumanInboxUsesExplicitSignalsAndRetainsUnreadThreads(t *testing.T) {
	s, _, call := inboxFixture(t)
	code, posted := call("POST", "/v1/chat/messages", map[string]any{"Target": "#dev", "Body": "Please review this work"})
	if code != 201 {
		t.Fatal(code, posted)
	}
	root := posted["id"].(string)
	if code, _ := call("GET", "/v1/chat/activity", nil); code != 200 {
		t.Fatal(code)
	}
	reply, err := Send(s.db, "reef", root, "Review completed", "", false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = Send(s.db, "reef", "#dev", "Routine progress stays quiet", "", false)
	_, _ = Send(s.db, "reef", "#dev", "Email someone@frane and `@frane` in code", "", false)
	_, _ = Send(s.db, "reef", "#secret", "@frane private work", "", false)
	_, _ = Send(s.db, "alice", "@bob", "@frane foreign DM", "", false)
	mention, err := Send(s.db, "reef", "#dev", "@frane, choose the next step", "", false)
	if err != nil {
		t.Fatal(err)
	}
	dm, err := Send(s.db, "reef", "@frane", "A question for the owner", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// Merely opening a channel must not mark its hidden thread replies read.
	if code, _ := call("POST", "/v1/chat/read-markers", map[string]any{"Target": "#dev", "Position": mention.Seq}); code != 200 {
		t.Fatal(code)
	}
	code, inbox := call("GET", "/v1/chat/activity", nil)
	if code != 200 {
		t.Fatal(code, inbox)
	}
	items := inbox["items"].([]any)
	if len(items) != 3 || inbox["unread"] != float64(3) {
		t.Fatal("inbox included chatter or hid an addressed item", inbox)
	}
	found := map[string]map[string]any{}
	for _, item := range items {
		entry := item.(map[string]any)
		found[entry["id"].(string)] = entry
	}
	if found[reply.ID]["reason"] != "reply" || found[mention.ID]["reason"] != "mention" || found[dm.ID]["question"] != true {
		t.Fatal(inbox)
	}
	for _, entry := range found {
		if entry["unread"] != true {
			t.Fatal("channel read cleared hidden attention", entry)
		}
	}
	_, claimed := call("PATCH", "/v1/chat/activity", map[string]any{"IDs": []string{reply.ID}, "Notify": true})
	if len(claimed["claimed"].([]any)) != 1 {
		t.Fatal(claimed)
	}
	_, again := call("PATCH", "/v1/chat/activity", map[string]any{"IDs": []string{reply.ID}, "Notify": true})
	if len(again["claimed"].([]any)) != 0 {
		t.Fatal("notification replayed", again)
	}
	code, threads := call("GET", "/v1/chat/threads", nil)
	if code != 200 || len(threads["threads"].([]any)) != 1 {
		t.Fatal(code, threads)
	}
	if code, result := call("PATCH", "/v1/chat/threads", map[string]any{"RootID": root, "Position": reply.Seq}); code != 200 {
		t.Fatal(code, result)
	}
	_, inbox = call("GET", "/v1/chat/activity", nil)
	if inbox["unread"] != float64(2) {
		t.Fatal("reading the actual thread failed to clear its inbox item", inbox)
	}
	if code, _ := call("PATCH", "/v1/chat/preferences", map[string]any{"Target": "#dev", "Muted": true}); code != 200 {
		t.Fatal(code)
	}
	_, inbox = call("GET", "/v1/chat/activity", nil)
	if inbox["unread"] != float64(1) {
		t.Fatal("muted channel still raised attention badge", inbox)
	}
	_, _ = call("PATCH", "/v1/chat/activity", map[string]any{"All": true})
	_, inbox = call("GET", "/v1/chat/activity", nil)
	if inbox["unread"] != float64(0) {
		t.Fatal(inbox)
	}
}
func TestHumanThreadHistoryIncludesOriginalAndPaginatesReplies(t *testing.T) {
	s, _, call := inboxFixture(t)
	_, posted := call("POST", "/v1/chat/messages", map[string]any{"Target": "#dev", "Body": "Original context beyond the recent page"})
	root := posted["id"].(string)
	for i := 0; i < 125; i++ {
		if _, err := Send(s.db, "reef", root, fmt.Sprintf("Reply %03d", i), "", false); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 110; i++ {
		_, _ = Send(s.db, "reef", "#dev", fmt.Sprint("Unrelated ", i), "", false)
	}
	code, page := call("GET", "/v1/chat/messages?target=%23dev&thread="+root, nil)
	if code != 200 {
		t.Fatal(code, page)
	}
	if page["root"].(map[string]any)["id"] != root || page["more"] != true || len(page["messages"].([]any)) != 100 {
		t.Fatal("thread lost its original context", page)
	}
	first := page["messages"].([]any)[0].(map[string]any)["seq"].(float64)
	_, earlier := call("GET", fmt.Sprintf("/v1/chat/messages?target=%%23dev&thread=%s&before=%.0f", root, first), nil)
	if earlier["root"].(map[string]any)["id"] != root || len(earlier["messages"].([]any)) != 26 || earlier["more"] != false {
		t.Fatal(earlier)
	}
	if code, _ := call("PATCH", "/v1/chat/threads", map[string]any{"RootID": root, "Following": false}); code != 200 {
		t.Fatal(code)
	}
	_, threads := call("GET", "/v1/chat/threads", nil)
	if len(threads["threads"].([]any)) != 0 {
		t.Fatal("unfollowed thread still followed", threads)
	}
}
func TestHumanThreadRootCannotLeakAnotherDirectConversation(t *testing.T) {
	s, _, call := inboxFixture(t)
	root, err := Send(s.db, "alice", "@bob", "Private original", "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "&after=0", "&before=100000"} {
		code, page := call("GET", "/v1/chat/messages?target=%40alice&thread="+root.ID+suffix, nil)
		if code != 200 || len(page["messages"].([]any)) != 0 || page["root"] != nil {
			t.Fatal("another DM's root was exposed", code, page)
		}
	}
	if code, _ := call("PATCH", "/v1/chat/threads", map[string]any{"RootID": root.ID, "Following": true}); code != 403 {
		t.Fatal("followed another person's DM", code)
	}
}
func TestHumanNotificationSettingsAndPersonalHints(t *testing.T) {
	s, _, call := inboxFixture(t)
	before, _ := readChatRevision(t.Context(), s.db)
	code, result := call("PATCH", "/v1/chat/notification-settings", map[string]any{"level": "important", "desktop": true, "preview": false, "sound": true})
	if code != 200 || result["desktop"] != true {
		t.Fatal(code, result)
	}
	after, _ := readChatRevision(t.Context(), s.db)
	if after.Metadata != before.Metadata || after.Personal <= before.Personal {
		t.Fatal("personal settings forced a global channel reload", before, after)
	}
	if code, _ := call("PATCH", "/v1/chat/notification-settings", map[string]any{"level": "something"}); code != 400 {
		t.Fatal(code)
	}
	if !strings.Contains(fmt.Sprint(result), "important") {
		t.Fatal(result)
	}
}

func TestHumanNotificationSettingsKeepCrossConnectionMessagesVisible(t *testing.T) {
	s, _, call := inboxFixture(t)
	if isPostgres(s.db) {
		t.Skip("SQLite connection regression")
	}
	var n int
	var name, path string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&n, &name, &path); err != nil {
		t.Fatal(err)
	}
	_, _ = call("POST", "/v1/chat/messages", map[string]any{"Target": "#dev", "Body": "Original"})
	_, _ = call("GET", "/v1/chat/activity", nil)
	_, _ = call("PATCH", "/v1/chat/notification-settings", map[string]any{"level": "important", "desktop": true})
	other, err := OpenDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	m, err := Send(other, "rook", "#dev", "@frane review", "", false)
	if err != nil {
		t.Fatal(err)
	}
	code, page := call("GET", fmt.Sprintf("/v1/chat/messages?target=%%23dev&after=%d", m.Seq-1), nil)
	if code != 200 || len(page["messages"].([]any)) != 1 {
		t.Fatal(code, page)
	}
	_, activity := call("GET", "/v1/chat/activity", nil)
	if activity["unread"] != float64(1) {
		t.Fatal(activity)
	}
}

func TestHumanInboxPaginatesHistoryAndCatchupWithoutSkipping(t *testing.T) {
	s, _, call := inboxFixture(t)
	_, initial := call("GET", "/v1/chat/activity", nil)
	for i := 0; i < 215; i++ {
		if _, err := Send(s.db, "rook", "#dev", fmt.Sprintf("@frane decision %d", i), "", false); err != nil {
			t.Fatal(err)
		}
	}
	code, recent := call("GET", "/v1/chat/activity", nil)
	if code != 200 || recent["more"] != true || len(recent["items"].([]any)) != 200 || recent["unread"] != float64(215) {
		t.Fatal(code, recent)
	}
	_, older := call("GET", fmt.Sprintf("/v1/chat/activity?before=%.0f", recent["before"]), nil)
	if len(older["items"].([]any)) != 15 || older["more"] != false {
		t.Fatal(older)
	}
	_, delta := call("GET", fmt.Sprintf("/v1/chat/activity?after=%.0f", initial["cursor"]), nil)
	if len(delta["items"].([]any)) != 200 || delta["more"] != true {
		t.Fatal(delta)
	}
	_, tail := call("GET", fmt.Sprintf("/v1/chat/activity?after=%.0f", delta["cursor"]), nil)
	if len(tail["items"].([]any)) != 15 || tail["more"] != false || tail["cursor"] != recent["cursor"] {
		t.Fatal(tail)
	}
}
