package internal

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMessageReactionsAndCorrectionsKeepOriginalDelivery(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "writer-token", "writer", []string{"#general", "@*"})
	seedTestAgent(t, db, "reader-token", "reader", []string{"#general", "@*"})
	h, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sent, failed := remoteTool(t, h, "writer-token", "send", map[string]any{"target": "#general", "body": "original text"})
	if failed {
		t.Fatal(sent)
	}
	id := sent["id"].(string)
	key := NewMessage("", "", nil).ID
	for i := 0; i < 2; i++ {
		value, failed := remoteTool(t, h, "reader-token", "react", map[string]any{"message": id, "emoji": "👍", "action_id": key})
		if failed {
			t.Fatal(value)
		}
	}
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM message_actions WHERE message_id=?", id).Scan(&count)
	if count != 1 {
		t.Fatal("retried reaction duplicated its event", count)
	}
	value, failed := remoteTool(t, h, "writer-token", "react", map[string]any{"message": id, "emoji": "👍"})
	if failed {
		t.Fatal(value)
	}
	read, failed := remoteTool(t, h, "writer-token", "read", map[string]any{"target": id, "peek": true})
	if failed {
		t.Fatal(read)
	}
	message := read["messages"].([]any)[0].(map[string]any)
	reaction := message["reactions"].([]any)[0].(map[string]any)
	if reaction["count"] != float64(2) || len(reaction["people"].([]any)) != 2 || reaction["mine"] != true {
		t.Fatal(message)
	}
	if value, failed = remoteTool(t, h, "reader-token", "edit", map[string]any{"message": id, "body": "forged correction"}); !failed {
		t.Fatal("another participant edited a message", value)
	}
	value, failed = remoteTool(t, h, "writer-token", "edit", map[string]any{"message": id, "body": "corrected text", "version": ""})
	if failed {
		t.Fatal(value)
	}
	if value, failed = remoteTool(t, h, "writer-token", "edit", map[string]any{"message": id, "body": "stale overwrite", "version": ""}); !failed {
		t.Fatal("concurrent edit silently overwrote content", value)
	}
	if value, failed = remoteTool(t, h, "reader-token", "react", map[string]any{"message": id, "emoji": "👍", "remove": true}); failed {
		t.Fatal(value)
	}
	original, err := findMessageByID(db, id)
	if err != nil || string(original.Body) != "original text" {
		t.Fatal("immutable delivery payload changed", original, err)
	}
	view, err := messageView(db, original, "")
	if err != nil || view.Body != "corrected text" || view.EditedAt == 0 || len(view.Reactions) != 1 || view.Reactions[0].Count != 1 {
		t.Fatal(view, err)
	}
	var messages int
	_ = db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages)
	if messages != 1 {
		t.Fatal("a reaction or edit posted new chat messages", messages)
	}
	before, _ := readChatRevision(context.Background(), db)
	_, _ = remoteTool(t, h, "writer-token", "react", map[string]any{"message": id, "emoji": "❤️"})
	after, _ := readChatRevision(context.Background(), db)
	if before.Sequence != after.Sequence || before.Actions == after.Actions {
		t.Fatal("reaction didn't update its independent change cursor", before, after)
	}
	// Revocation takes effect for actions too; public readability is not a write grant.
	_, _ = db.Exec("UPDATE permissions SET can_write=0 WHERE principal_id=(SELECT id FROM principals WHERE name='reader')")
	if value, failed = remoteTool(t, h, "reader-token", "react", map[string]any{"message": id, "emoji": "🎉"}); !failed {
		t.Fatal("read-only member reacted", value)
	}
}

func TestMessageActionsSyncOldMessagesAndKeepLatestOriginOrder(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "writer-token", "writer", []string{"#general"})
	handler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, invite, err := CreateInvite(db, "laptop", []string{"#general"}, true, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, invite, "#general")
	sent, failed := remoteTool(t, handler, "writer-token", "send", map[string]any{"target": "#general", "body": "already cached"})
	if failed {
		t.Fatal(sent)
	}
	id := sent["id"].(string)
	mustSync(t, client, "work")
	_, failed = remoteTool(t, handler, "writer-token", "edit", map[string]any{"message": id, "body": "updated long after delivery"})
	if failed {
		t.Fatal("edit failed")
	}
	_, failed = remoteTool(t, handler, "writer-token", "react", map[string]any{"message": id, "emoji": "✅"})
	if failed {
		t.Fatal("reaction failed")
	}
	mustSync(t, client, "work")
	var localID string
	if err = client.QueryRow("SELECT local_id FROM remote_messages WHERE remote_name='work' AND remote_id=?", id).Scan(&localID); err != nil {
		t.Fatal(err)
	}
	m, err := findMessageByID(client, localID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := messageView(client, m, "")
	if err != nil || view.Body != "updated long after delivery" || len(view.Reactions) != 1 {
		t.Fatal("old message changes did not reach cache", view, err)
	}
	// A successful write ack can arrive before an older sync page. Folding uses
	// the coordinator sequence, so delayed history cannot undo the newer state.
	newer := messageAction{Seq: 1000, ID: NewMessage("", "", nil).ID, MessageID: id, ActorID: "peer", ActorName: "peer", Kind: "reaction", Emoji: "👀", Added: false, CreatedAt: time.Now().UnixMilli()}
	older := newer
	older.Seq = 999
	older.ID = NewMessage("", "", nil).ID
	older.Added = true
	if err = importMessageActions(client, "work", []messageAction{newer, older}, nil); err != nil {
		t.Fatal(err)
	}
	view, _ = messageView(client, m, "")
	for _, reaction := range view.Reactions {
		if reaction.Emoji == "👀" {
			t.Fatal("delayed page restored a removed reaction", view)
		}
	}
	// Local MCP/CLI uses the same source identity rather than writing only to cache.
	value, err := ReactMessage(context.Background(), client, "reviewer", localID, "💡", "", false)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	if len(data) == 0 {
		t.Fatal("no action result")
	}
	original, _ := findMessageByID(db, id)
	view, _ = messageView(db, original, "")
	found := false
	for _, reaction := range view.Reactions {
		found = found || reaction.Emoji == "💡"
	}
	if !found {
		t.Fatal("local tool failed to send reaction to source", view)
	}
}
