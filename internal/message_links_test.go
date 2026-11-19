package internal

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMessageLinksRetainServerThreadAndPrivateAccess(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "writer-token", "writer", []string{"#general", "@*"})
	seedTestAgent(t, db, "other-token", "other", []string{"#other", "@*"})
	handler, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: "https://chat.example.test", Web: true})
	if err != nil {
		t.Fatal(err)
	}
	root, failed := remoteTool(t, handler, "writer-token", "send", map[string]any{"target": "#general", "body": "old root"})
	if failed {
		t.Fatal(root)
	}
	rootID := root["id"].(string)
	reply, failed := remoteTool(t, handler, "writer-token", "send", map[string]any{"target": rootID, "body": "linked reply"})
	if failed {
		t.Fatal(reply)
	}
	replyID := reply["id"].(string)
	for i := 0; i < 120; i++ {
		if _, _, err := SendIdempotent(db, "writer", "#general", "later message", "", false, ""); err != nil {
			t.Fatal(err)
		}
	}
	link := messageLink(db, replyID, "")
	if link != "https://chat.example.test/m/"+replyID {
		t.Fatal(link)
	}
	if value, failed := remoteTool(t, handler, "writer-token", "read", map[string]any{"target": link, "peek": true}); failed || value["messages"].([]any)[0].(map[string]any)["id"] != replyID {
		t.Fatal(value)
	}
	if value, failed := remoteTool(t, handler, "other-token", "read", map[string]any{"target": link, "peek": true}); !failed {
		t.Fatal("link granted private channel access", value)
	}
	if _, _, err := ResolveTarget(db, strings.Replace(link, "chat.example.test", "another.example.test", 1), ""); err == nil {
		t.Fatal("foreign server link resolved against local ID")
	}
	call := func(token string) (int, map[string]any) {
		r := httptest.NewRequest("GET", "/v1/chat/message?reference="+url.QueryEscape(link), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var result map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return w.Code, result
	}
	code, result := call("writer-token")
	if code != 200 || result["target"] != "#general" || result["root_id"] != rootID || result["message_id"] != replyID || result["reply"] != true || len(result["replies"].([]any)) != 2 {
		t.Fatal("link did not recover an older thread", code, result)
	}
	if code, _ := call("other-token"); code != 404 {
		t.Fatal("message context leaked access", code)
	}
	r := httptest.NewRequest("GET", "/m/"+replyID, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/") {
		t.Fatal("browser link did not load sign-in shell", w.Code)
	}
	local := newTestDB(t)
	m, _, err := SendIdempotent(local, "agent", "#local", "local only", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	localLink := messageLink(local, m.ID, "")
	_, resolved, err := ResolveTarget(local, localLink, "")
	if err != nil || resolved.ID != m.ID {
		t.Fatal(localLink, resolved, err)
	}
	if _, _, err := ResolveTarget(db, localLink, ""); err == nil {
		t.Fatal("local link crossed database identity")
	}
}
