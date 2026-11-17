package internal

import (
	"encoding/json"
	"testing"
)

func TestGroupMentionsSnapshotOnlyEligibleParticipants(t *testing.T) {
	db := newTestDB(t)
	for _, name := range []string{"alice", "bob", "coding", "outsider"} {
		channel := "#dev"
		if name == "outsider" {
			channel = "#private"
		}
		seedTestAgent(t, db, name+"-token", name, []string{channel, "@*"})
	}
	if _, err := db.Exec("UPDATE principals SET kind='human' WHERE name IN('alice','bob')"); err != nil {
		t.Fatal(err)
	}
	principals := map[string]*Principal{}
	for _, name := range []string{"alice", "bob", "coding", "outsider"} {
		p, err := participantByAddress(db, name)
		if err != nil {
			t.Fatal(err)
		}
		principals[name] = p
	}
	touchParticipant(db, principals["alice"].ID)
	touchParticipant(db, principals["coding"].ID)
	for _, item := range []struct {
		body string
		want map[string]bool
	}{
		{"@agents review this", map[string]bool{"coding": true}},
		{"@humans a decision is needed", map[string]bool{"alice": true, "bob": true}},
		{"@team share context", map[string]bool{"alice": true, "bob": true, "coding": true}},
		{"@here quick question", map[string]bool{"alice": true, "coding": true}},
		{"`@team` and email@humans are examples", map[string]bool{}},
	} {
		m, err := Send(db, "publisher", "#dev", item.body, "", false)
		if err != nil {
			t.Fatal(err)
		}
		for name, p := range principals {
			if got := groupMentioned(db, m, name, p.ID); got != item.want[name] {
				t.Errorf("%q: %s got %v want %v", item.body, name, got, item.want[name])
			}
		}
	}
	message, err := Send(db, "publisher", "#dev", "@here current audience only", "", false)
	if err != nil {
		t.Fatal(err)
	}
	touchParticipant(db, principals["bob"].ID)
	if groupMentioned(db, message, "bob", principals["bob"].ID) {
		t.Fatal("@here audience expanded after sending")
	}
	if _, err := db.Exec("UPDATE principals SET name='renamed' WHERE id=?", principals["alice"].ID); err != nil {
		t.Fatal(err)
	}
	if !groupMentioned(db, message, "renamed", principals["alice"].ID) {
		t.Fatal("rename lost durable mention audience")
	}
	wire := attributedWire(db, message)
	if wire.GroupRecipients == nil {
		t.Fatal("sync omitted the group audience")
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip wireMessage
	if err = json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if !roundtrip.message().groupSnapshot {
		t.Fatal("sync would recompute @here using a different machine's presence")
	}
}

func TestLocalAgentGroupMentionIsActionable(t *testing.T) {
	db := newTestDB(t)
	if _, err := Send(db, "coder", "#dev", "I work here", "", false); err != nil {
		t.Fatal(err)
	}
	m, err := Send(db, "human", "#dev", "@agents please review", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !groupMentioned(db, m, "coder", "") {
		t.Fatal("local agent was not addressed")
	}
	n, err := countUnread(db, "coder", "#dev", 0, true)
	if err != nil || n != 1 {
		t.Fatalf("actionable count %d: %v", n, err)
	}
	if action := activationAction(AgentSession{Agent: "coder", Mode: "notify"}, m, "", false, true); action != "notify" {
		t.Fatal(action)
	}
	if action := activationAction(AgentSession{Agent: "coder", Mode: "activate", ActivateMentions: true}, m, "", true, true); action != "passive" {
		t.Fatal("group mention bypassed automated-loop prevention")
	}
}
