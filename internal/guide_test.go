package internal

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicAgentGuideDoesNotRevealPrivateChat(t *testing.T) {
	db := newTestDB(t)
	if _, err := Send(db, "private-sender", "#private-secret-channel", "private-message-do-not-leak", "", false); err != nil {
		t.Fatal(err)
	}
	handler, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: "https://grpvn.offbyte.com"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	var first string
	for _, path := range []string{"/llm.txt", "/llms.txt"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/plain") {
			t.Fatalf("guide unavailable to unregistered agent: %d", response.StatusCode)
		}
		text := string(body)
		for _, want := range []string{"https://grpvn.offbyte.com/mcp", "npx --yes grpvn-cli", "npm install -g grpvn-cli", "grpvn skill install", "OAuth", "idempotency_key", "Retry-After", "ChatGPT web/Desktop", "Claude Desktop", "Codex CLI/TUI", "Claude Code", "OpenCode", "Antigravity", "rename", "--profile EXISTING_AGENT_ID", "LOCAL SQLite cache alias"} {
			if !strings.Contains(text, want) {
				t.Fatalf("guide omits %s", want)
			}
		}
		for _, secret := range []string{"private-sender", "private-secret-channel", "private-message-do-not-leak"} {
			if strings.Contains(text, secret) {
				t.Fatal("public guide leaked private chat")
			}
		}
		if first != "" && first != text {
			t.Fatal("llm/llms aliases differ")
		}
		first = text
	}
	response, err := server.Client().Get(server.URL + "/skill.md")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(body) != string(SkillContent) {
		t.Fatal("hosted skill differs from installed skill")
	}
	response, err = server.Client().Get(server.URL + "/v1/messages?channel=%23private-secret-channel")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("public onboarding bypassed private message permissions")
	}
}
