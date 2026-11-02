package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func accessRequest(t *testing.T, server *httptest.Server, method, path, token string, body any) (int, string) {
	t.Helper()
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, server.URL+path, data)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(response)
}

func TestCoordinatorPrivateDefaultAndExplicitOpen(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "secret", "alice", []string{"*"})
	privateHandler, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	privateServer := httptest.NewServer(privateHandler)
	t.Cleanup(privateServer.Close)
	msg := toWire(NewMessage("alice", "#dev", []byte("private")))
	for _, token := range []string{"", "wrong"} {
		if status, _ := accessRequest(t, privateServer, "GET", "/v1/info", token, nil); status != http.StatusOK {
			t.Fatalf("enrollment metadata returned %d", status)
		}
		if status, _ := accessRequest(t, privateServer, "POST", "/v1/messages", token, msg); status != http.StatusUnauthorized {
			t.Fatalf("private write returned %d", status)
		}
	}
	if status, _ := accessRequest(t, privateServer, "POST", "/v1/messages", "secret", msg); status != http.StatusOK {
		t.Fatalf("private token write returned %d", status)
	}
	openHandler, err := CoordinatorHandler(db, CoordinatorOptions{Open: true})
	if err != nil {
		t.Fatal(err)
	}
	openServer := httptest.NewServer(openHandler)
	t.Cleanup(openServer.Close)
	if status, body := accessRequest(t, openServer, "GET", "/v1/messages?channel=%23dev", "", nil); status != http.StatusOK || !strings.Contains(body, `"body":"private"`) {
		t.Fatalf("explicit open read: %d %s", status, body)
	}
	public := toWire(NewMessage("anyone", "#lobby", []byte("anonymous")))
	seedTestAgent(t, db, "guest", "anyone", []string{"#lobby"})
	if status, _ := accessRequest(t, openServer, "POST", "/v1/messages", "", public); status != http.StatusUnauthorized {
		t.Fatalf("anonymous sender accepted: %d", status)
	}
	if status, _ := accessRequest(t, openServer, "POST", "/v1/messages", "guest", public); status != http.StatusOK {
		t.Fatalf("open write returned %d", status)
	}
	client := newTestDB(t)
	mustConnect(t, client, "public", openServer.URL, "", "#lobby")
	if stats := mustSync(t, client, "public"); stats.Pulled != 1 {
		t.Fatalf("anonymous client sync: %+v", stats)
	}
}

func TestCoordinatorMixedChannelAccess(t *testing.T) {
	for _, openDefault := range []bool{false, true} {
		t.Run(map[bool]string{false: "private_default", true: "open_default"}[openDefault], func(t *testing.T) {
			db := newTestDB(t)
			for _, channel := range []string{"#lobby", "#work"} {
				if _, err := Send(db, "alice", channel, channel+" message", "", false); err != nil {
					t.Fatal(err)
				}
			}
			seedTestAgent(t, db, "secret", "alice", []string{"*"})
			options := CoordinatorOptions{Open: openDefault, Channels: []string{"#lobby", "#work", "#empty-open", "#empty-private"}}
			if openDefault {
				options.PrivateChannels = []string{"#work", "#empty-private"}
			} else {
				options.OpenChannels = []string{"#lobby", "#empty-open"}
			}
			handler, err := CoordinatorHandler(db, options)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			if status, _ := accessRequest(t, server, "GET", "/v1/info", "", nil); status != http.StatusOK {
				t.Fatalf("open client cannot discover server: %d", status)
			}
			status, body := accessRequest(t, server, "GET", "/v1/channels", "", nil)
			if status != http.StatusOK || !strings.Contains(body, `"#lobby"`) || !strings.Contains(body, `"#empty-open"`) || strings.Contains(body, "work") || strings.Contains(body, "private") {
				t.Fatalf("private channels leaked in discovery: %d %s", status, body)
			}
			status, body = accessRequest(t, server, "GET", "/v1/channels", "secret", nil)
			if status != http.StatusOK || !strings.Contains(body, `"#work"`) || !strings.Contains(body, `"#empty-private"`) {
				t.Fatalf("private discovery failed: %d %s", status, body)
			}
			for _, token := range []string{"", "wrong"} {
				if status, body := accessRequest(t, server, "GET", "/v1/messages?channel=%23work", token, nil); status != http.StatusUnauthorized || strings.Contains(body, "#work message") {
					t.Fatalf("private content leaked: %d %s", status, body)
				}
				private := toWire(NewMessage("mallory", "#work", []byte("denied")))
				if status, _ := accessRequest(t, server, "POST", "/v1/messages", token, private); status != http.StatusUnauthorized {
					t.Fatalf("unauthorized private write: %d", status)
				}
			}
			if status, _ := accessRequest(t, server, "GET", "/v1/messages?channel=%23work", "secret", nil); status != http.StatusOK {
				t.Fatalf("authorized private read: %d", status)
			}
			public := toWire(NewMessage("guest", "#lobby", []byte("open post")))
			seedTestAgent(t, db, "guest", "guest", []string{"#lobby"})
			if status, _ := accessRequest(t, server, "POST", "/v1/messages", "guest", public); status != http.StatusOK {
				t.Fatalf("anonymous open write: %d", status)
			}
			if status, _ := accessRequest(t, server, "GET", "/v1/messages?channel=%23unserved", "secret", nil); status != http.StatusForbidden {
				t.Fatalf("token bypassed served channel restriction: %d", status)
			}
			client := newTestDB(t)
			mustConnect(t, client, "mixed", server.URL, "", "#lobby", "#work")
			stats, err := SyncRemotes(context.Background(), client, "mixed")
			if err == nil || stats.Pulled != 2 {
				t.Fatalf("mixed sync failed to isolate channels: %+v %v", stats, err)
			}
		})
	}
}

func TestCoordinatorRejectsContradictoryAccessConfiguration(t *testing.T) {
	db := newTestDB(t)
	for _, options := range []CoordinatorOptions{
		{OpenChannels: []string{"#lobby"}, PrivateChannels: []string{"#lobby"}},
		{Channels: []string{"#dev"}, OpenChannels: []string{"#other"}},
		{OpenChannels: []string{"@alice"}},
	} {
		if _, err := CoordinatorHandler(db, options); err == nil {
			t.Fatalf("accepted invalid policy: %+v", options)
		}
	}
}

func TestPrivateOverridesProtectMetadataWhenEveryServedChannelIsPrivate(t *testing.T) {
	db := newTestDB(t)
	handler, err := CoordinatorHandler(db, CoordinatorOptions{Open: true, Channels: []string{"#work"}, PrivateChannels: []string{"#work"}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if status, _ := accessRequest(t, server, "GET", "/v1/info", "", nil); status != http.StatusOK {
		t.Fatalf("all-private metadata returned %d", status)
	}
}
