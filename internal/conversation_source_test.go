package internal

import (
	"bytes"
	"strings"
	"testing"
)

func TestRemoteConversationContextSurvivesDifferentLocalAliases(t *testing.T) {
	serverDB := newTestDB(t)
	server := testCoordinator(t, serverDB, "", "#dealflow", "#empty")
	if _, err := Send(serverDB, "peer", "#dealflow", "A shared remote conversation", "", false); err != nil {
		t.Fatal(err)
	}
	var serverID string
	for _, alias := range []string{"work", "workspace-zfsvgxxa"} {
		db := newTestDB(t)
		mustConnect(t, db, alias, server.URL, "", "#dealflow", "#empty")
		mustSync(t, db, alias)
		target := remoteTarget(alias, "#dealflow")
		source := conversationSource(db, target)
		if source == nil || source.URL != server.URL || source.Channel != "#dealflow" || source.Target != target {
			t.Fatal("lost the source of a cached conversation", source)
		}
		if serverID != "" && source.ServerID != serverID {
			t.Fatal("different local aliases changed the coordinator identity")
		}
		serverID = source.ServerID
		var listing, history bytes.Buffer
		if err := Channels(&listing, db, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(listing.String(), target) || !strings.Contains(listing.String(), "remote #dealflow on "+server.URL) || !strings.Contains(listing.String(), remoteTarget(alias, "#empty")+" 0 - | remote #empty on "+server.URL) {
			t.Fatal("channel discovery hides the native name or coordinator", listing.String())
		}
		if err := Log(&history, db, "reader", target, 1, "", false, true, false, "never"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(history.String(), "remote #dealflow on "+server.URL) || !strings.Contains(history.String(), "local target "+target) {
			t.Fatal("history hides the server context", history.String())
		}
		message := messageIn(t, db, target, "A shared remote conversation")
		ack := FormatSendAck(message, false, db)
		if !strings.HasPrefix(ack, message.ID+" "+target+"\n") || !strings.Contains(ack, "remote #dealflow on "+server.URL) {
			t.Fatal("send acknowledgement hides the server context", ack)
		}
		if question := FormatAskAck(message, db); !strings.HasPrefix(question, message.ID+"\n") || !strings.Contains(question, "remote #dealflow on "+server.URL) {
			t.Fatal("question acknowledgement hides the server context", question)
		}
		local, err := Send(db, "local-peer", "#dealflow", "A different local conversation", "", false)
		if err != nil || conversationSource(db, local.Target) != nil {
			t.Fatal("a local namesake was confused with a remote channel", err)
		}
	}
}

func TestRemoteMCPResultsIdentifyTheCoordinator(t *testing.T) {
	db := newTestDB(t)
	seedTestAgent(t, db, "agent-token", "agent", []string{"#dealflow", "@*"})
	handler, err := CoordinatorHandler(db, CoordinatorOptions{PublicURL: "https://chat.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"identity", "list_channels"} {
		result, failed := remoteTool(t, handler, "agent-token", tool, map[string]any{})
		if failed {
			t.Fatal(result)
		}
		source, ok := result["server"].(map[string]any)
		if !ok || source["url"] != "https://chat.example.test" || source["id"] == "" {
			t.Fatal("remote MCP omitted its coordinator identity", result)
		}
	}
}
