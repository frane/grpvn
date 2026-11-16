package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestAgentCLIFileUploadsAndThreadedRemoteAttachments(t *testing.T) {
	t.Parallel()
	host := newRunner(t, "host")
	host.mustRun("s", "#project", "Project opener")
	endpoint, stop := startCoordinator(t, host)
	invite := strings.TrimSpace(host.mustRun("iv", "--to", "team", "--channel", "#project", "--write"))
	client := newRunner(t, "reviewer")
	client.mustRun("j", "work", endpoint, "#project", "--invite", invite)
	path := filepath.Join(t.TempDir(), "original-deck.pdf")
	data := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("deck contents\n"), 8000)...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ack := client.mustRun("s", "#work/project", "Here is the deck", "--file", path, "--idempotency", "deck-send-v1")
	client.mustRun("sy", "work")
	retry := client.mustRun("s", "#work/project", "Here is the deck", "--file", path, "--idempotency", "deck-send-v1")
	if strings.Fields(ack)[0] != strings.Fields(retry)[0] {
		t.Fatal("retry posted a second attachment message", ack, retry)
	}
	history := host.mustRun("l", "#project", "--full")
	if strings.Count(history, "Here is the deck") != 1 {
		t.Fatal("message delivery duplicated", history)
	}
	match := regexp.MustCompile(`grpvn-file:([0-9A-HJKMNP-TV-Z]{26})`).FindStringSubmatch(history)
	if len(match) != 2 {
		t.Fatal("attachment link missing", history)
	}
	root := strings.Fields(ack)[0]
	client.mustRun("s", root, "--file", path, "--idempotency", "thread-deck")
	client.mustRun("sy", "work")
	if history := host.mustRun("l", "#project", "--full"); !strings.Contains(history, "original-deck.pdf") {
		t.Fatal(history)
	}
	var upload map[string]any
	if err := json.Unmarshal([]byte(client.mustRun("fi", "--upload", path, "#work/project", "--idempotency", "standalone-upload")), &upload); err != nil || upload["markdown"] == nil {
		t.Fatal(upload, err)
	}
	// Uploaded bytes are retained in the same local DB for offline retrieval.
	stop()
	saved := filepath.Join(t.TempDir(), "downloaded.pdf")
	client.mustRun("fi", match[1], "#work/project", "--out", saved)
	got, err := os.ReadFile(saved)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("cached bytes changed or required a live server", err)
	}
	if _, _, code := client.run("s", "#work/project", "Offline upload must not be claimed as delivered", "--file", path, "--idempotency", "offline-file"); code == 0 {
		t.Fatal("unacknowledged upload reported success")
	}
}
