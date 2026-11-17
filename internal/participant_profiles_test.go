package internal

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestLocalRenameKeepsProfileUnreadAndLongRunningSession(t *testing.T) {
	db := newTestDB(t)
	if _, err := Send(db, "reef-skua", "#dev", "Working on the project", "", false); err != nil {
		t.Fatal(err)
	}
	incoming, err := Send(db, "friend", "@reef-skua", "A DM before the rename", "", false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := RegisterSession(db, AgentSession{Agent: "reef-skua", Runtime: "codex", RuntimeSession: "long-running-thread", Mode: "activate", Channels: []string{"#dev"}}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	p, err := EnsureLocalAgent(db, "reef-skua")
	if err != nil {
		t.Fatal(err)
	}
	before, replayed, err := SendIdempotent(db, "reef-skua", "#dev", "An idempotent send before rename", "", false, "rename-retry")
	if err != nil || replayed {
		t.Fatal(err)
	}
	role, runtime, description := "coding", "Codex", "Maintains the project and reviews changes."
	if _, err = UpdateLocalProfile(db, p.ID, p.Name, ProfileUpdate{Role: &role, Runtime: &runtime, Description: &description}); err != nil {
		t.Fatal(err)
	}
	renamed, err := RenameLocalAgent(db, "reef-skua", "rook")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != p.ID {
		t.Fatal("rename created another profile")
	}
	if unread, err := countUnread(db, "rook", "#dev", 0, false); err != nil || unread != 0 {
		t.Fatal("rename made the agent's own history unread", unread, err)
	}
	if _, err := Send(db, "friend", before.ID, "A reply addressed to the old handle", "", false); err != nil {
		t.Fatal(err)
	}
	if unread, err := countUnread(db, "rook", "#dev", 0, true); err != nil || unread != 1 {
		t.Fatal("rename lost an actionable reply", unread, err)
	}
	after, replayed, err := SendIdempotent(db, "rook", "#dev", "An idempotent send before rename", "", false, "rename-retry")
	if err != nil || !replayed || after.ID != before.ID {
		t.Fatal("rename broke retry deduplication", after, replayed, err)
	}
	loaded := readParticipantProfile(db, p.ID, "rook")
	if loaded.Role != role || loaded.Runtime != runtime || loaded.Description != description {
		t.Fatal("rename lost profile metadata", loaded)
	}
	state := &State{Name: "reef-skua", Follow: []string{"#dev"}}
	path := filepath.Join(t.TempDir(), "state.json")
	name, err := RefreshLocalStateIdentity(db, state, path, "reef-skua")
	if err != nil || name != "rook" || state.Name != "rook" {
		t.Fatal(name, state, err)
	}
	resumed, err := RegisterSession(db, AgentSession{Agent: "rook", Runtime: "codex", RuntimeSession: "long-running-thread", KeepPolicy: true}, false, false)
	if err != nil || resumed != session {
		t.Fatal("long-running session was replaced", resumed, session, err)
	}
	sessions, err := Sessions(db, true)
	if err != nil || len(sessions) != 1 || sessions[0].Agent != "rook" || sessions[0].Mode != "activate" {
		t.Fatal(sessions, err)
	}
	var out bytes.Buffer
	code, err := Read(&out, db, state, 0, true, false, true, false, "never", "@me")
	if err != nil || code != 0 || !bytes.Contains(out.Bytes(), []byte(incoming.ID)) {
		t.Fatal("old DM alias disappeared", code, out.String(), err)
	}
	if _, err := Send(db, "friend", "@reef-skua", "The old DM address still works", "", false); err != nil {
		t.Fatal(err)
	}
	messageIn(t, db, "@rook", "The old DM address still works")
}

func TestAgentProfileCannotSelfAssignHumanOwner(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "agent-token", "agent", []string{"#general"})
	seedTestAgent(t, db, "human-token", "human", []string{"#general"})
	if _, err := db.Exec("UPDATE principals SET kind='human' WHERE name='human'"); err != nil {
		t.Fatal(err)
	}
	agent, err := participantByAddress(db, "agent")
	if err != nil {
		t.Fatal(err)
	}
	human, err := participantByAddress(db, "human")
	if err != nil {
		t.Fatal(err)
	}
	if err := updateParticipantProfile(db, agent.ID, ProfileUpdate{OwnerID: &human.ID}, false); err == nil {
		t.Fatal("agent could claim arbitrary human ownership")
	}
	if err := updateParticipantProfile(db, agent.ID, ProfileUpdate{OwnerID: &human.ID}, true); err != nil {
		t.Fatal(err)
	}
	profile := readParticipantProfile(db, agent.ID, agent.Name)
	if profile.OwnerName != "human" {
		t.Fatal(profile)
	}
	if principalPermission(db, agent, "#private", false) {
		t.Fatal("ownership granted private channel access")
	}
}
