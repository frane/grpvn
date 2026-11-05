package internal

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHumanLocalCacheCreatesAndSyncsRemoteChannel(t *testing.T) {
	serverDB := newTestDB(t)
	h, err := CoordinatorHandler(serverDB, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	_, invite, err := CreateInvite(serverDB, "Human laptop", []string{"#general", "@*"}, true, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, invite, "#general")
	if _, err = LocalHumanTicket(client, "frane"); err != nil {
		t.Fatal(err)
	}
	m, _, err := SendSynced(context.Background(), client, "frane", "#work/new-project", "Human from the desktop app", "", false, "desktop-human")
	if err != nil || m.Pending {
		remote, _ := getRemote(client, "work")
		t.Fatalf("%+v %v; source=%+v", m, err, remote.LastError)
	}
	var kind string
	if err = serverDB.QueryRow("SELECT kind FROM principals WHERE name='frane'").Scan(&kind); err != nil || kind != "human" {
		t.Fatalf("human actor lost in synchronization: %q %v", kind, err)
	}
	received := messageIn(t, serverDB, "#new-project", "Human from the desktop app")
	if received.ParentID != nil {
		t.Fatal("unexpected thread")
	}
}

func TestDaemonPicksUpPendingAdmissionWithoutReconnect(t *testing.T) {
	serverDB := newTestDB(t)
	h, err := CoordinatorHandler(serverDB, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := newTestDB(t)
	mustConnect(t, client, "work", server.URL, "", "#general")
	registration, err := RemoteRegistration(client, "work")
	if err != nil || registration.Status != "pending" {
		t.Fatal(registration, err)
	}
	if err = ApproveMember(serverDB, registration.ApprovalCode, "", []string{"#general"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = Send(serverDB, "peer", "#general", "admission picked up", "", false); err != nil {
		t.Fatal(err)
	}
	mustSync(t, client, "work")
	messageIn(t, client, "#work/general", "admission picked up")
	registration, err = RemoteRegistration(client, "work")
	if err != nil || registration.Status != "active" {
		t.Fatal(registration, err)
	}
}
