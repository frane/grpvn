package internal

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestConversationMuteAndUnreadOverride(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "reader-token", "reader", []string{"#project", "@*"})
	_, _ = db.Exec("UPDATE principals SET kind='human' WHERE name='reader'")
	seedTestAgent(t, db, "writer-token", "writer", []string{"#project", "@*"})
	first, _ := Send(db, "writer", "#project", "First message", "", false)
	last, _ := Send(db, "writer", "#project", "Second message", "", false)
	h, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		status, raw := accessRequest(t, server, method, path, "reader-token", body)
		if status != 200 {
			t.Fatal(status, raw)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	channel := func() map[string]any {
		t.Helper()
		return call("GET", "/v1/chat/channels", nil)["channels"].([]any)[0].(map[string]any)
	}
	call("POST", "/v1/chat/read-markers", map[string]any{"Target": "#project", "Resume": true})
	if channel()["unread"] != float64(0) {
		t.Fatal("read channel remains unread")
	}
	call("POST", "/v1/chat/read-markers", map[string]any{"Target": "#project", "Unread": true, "Position": first.Seq - 1})
	// A read already in flight must not erase an explicit human unread marker.
	call("POST", "/v1/chat/read-markers", map[string]any{"Target": "#project", "Position": last.Seq})
	if c := channel(); c["unread"] != float64(2) || c["read_held"] != true {
		t.Fatal("automatic read erased the marker", c)
	}
	call("PATCH", "/v1/chat/preferences", map[string]any{"Target": "#project", "Muted": true})
	if c := channel(); c["muted"] != true || c["unread"] != float64(2) {
		t.Fatal("mute changed unread state", c)
	}
	page := call("GET", "/v1/chat/messages?target=%23project", nil)
	if page["read_held"] != true {
		t.Fatal("reconnected reader lost unread override", page)
	}
	call("POST", "/v1/chat/read-markers", map[string]any{"Target": "#project", "Resume": true})
	if c := channel(); c["read_held"] != false || c["unread"] != float64(0) || c["muted"] != true {
		t.Fatal("resume did not retain independent mute setting", c)
	}
	for _, path := range []string{"/v1/chat/read-markers", "/v1/chat/preferences"} {
		method := "POST"
		if path == "/v1/chat/preferences" {
			method = "PATCH"
		}
		if status, _ := accessRequest(t, server, method, path, "reader-token", map[string]any{"Target": "#private"}); status != 403 {
			t.Fatal("private conversation preferences accepted", status)
		}
	}
	_, _ = Send(db, "writer", "@reader", "Direct message", "", false)
	call("POST", "/v1/chat/read-markers", map[string]any{"Target": "@writer", "Resume": true})
	call("POST", "/v1/chat/read-markers", map[string]any{"Target": "@writer", "Unread": true})
	call("PATCH", "/v1/chat/preferences", map[string]any{"Target": "@writer", "Muted": true})
	people := call("GET", "/v1/chat/participants?name=writer", nil)["people"].([]any)
	if len(people) != 1 || people[0].(map[string]any)["unread"] != float64(1) || people[0].(map[string]any)["muted"] != true {
		t.Fatal("DM preferences are incorrect", people)
	}
}
