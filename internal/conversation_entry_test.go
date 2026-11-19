package internal

import (
	"fmt"
	"testing"
)

func TestConversationEntryStartsAtFirstUnreadWithoutReadingPage(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "person-token", "person", []string{"#general", "@*"})
	_, _ = db.Exec("UPDATE principals SET kind='human' WHERE name='person'")
	handler, err := CoordinatorHandler(db, CoordinatorOptions{Web: true})
	if err != nil {
		t.Fatal(err)
	}
	messages := []*Message{}
	for i := 0; i < 140; i++ {
		m, e := Send(db, "peer", "#general", fmt.Sprintf("Message %d", i+1), "", false)
		if e != nil {
			t.Fatal(e)
		}
		messages = append(messages, m)
	}
	readStateCall(t, handler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Position": messages[4].Seq})
	page := readStateCall(t, handler, "person-token", "GET", "/v1/chat/messages?target=%23general&initial=1", nil)
	if page["first_unread_id"] != messages[5].ID || page["read_position"] != float64(messages[4].Seq) || page["newer"] != true {
		t.Fatal("did not open oldest unread beyond recent history", page["first_unread_id"], page["read_position"], page["newer"])
	}
	found := false
	for _, raw := range page["messages"].([]any) {
		found = found || raw.(map[string]any)["id"] == messages[5].ID
	}
	if !found {
		t.Fatal("first unread absent from initial page")
	}
	person, _ := participantByAddress(db, "person")
	if position, _ := conversationReadPosition(db, person.ID, "#general"); position != messages[4].Seq {
		t.Fatal("opening a page marked unseen messages read", position)
	}
	readStateCall(t, handler, "person-token", "POST", "/v1/chat/read-markers", map[string]any{"Target": "#general", "Resume": true})
	page = readStateCall(t, handler, "person-token", "GET", "/v1/chat/messages?target=%23general&initial=1", nil)
	if page["first_unread_id"] != nil {
		t.Fatal("all-read conversation kept an unread anchor", page["first_unread_id"])
	}
	list := page["messages"].([]any)
	if list[len(list)-1].(map[string]any)["id"] != messages[139].ID {
		t.Fatal("all-read conversation did not load latest history")
	}
}
