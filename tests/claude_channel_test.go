package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestClaudeChannelStdioHandshakeAndIdleWake(t *testing.T) {
	r := newRunner(t, "claude-channel-bot")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, "serve", "--claude-channel", "--activation", "activate", "--session", "native-claude-session")
	cmd.Dir, cmd.Env = r.cwd, r.env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() }()
	lines := make(chan map[string]json.RawMessage, 16)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var message map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &message) == nil {
				lines <- message
			}
		}
		close(lines)
	}()
	request := func(value any) {
		t.Helper()
		data, _ := json.Marshal(value)
		if _, err := fmt.Fprintln(stdin, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	request(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "Claude fixture", "version": "1"}}})
	select {
	case result := <-lines:
		if !strings.Contains(string(result["result"]), `"claude/channel"`) {
			t.Fatalf("native capability absent: %s", result["result"])
		}
	case <-ctx.Done():
		t.Fatal("MCP handshake timed out")
	}
	request(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.mustRun("ss", "list"), "native-claude-session") {
		if time.Now().After(deadline) {
			t.Fatal("channel did not register native session")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.mustRun("--as", "peer", "s", "@claude-channel-bot", "wake idle Claude")
	for {
		select {
		case message, ok := <-lines:
			if !ok {
				t.Fatal("MCP subprocess closed before idle wake")
			}
			var method string
			_ = json.Unmarshal(message["method"], &method)
			if method == "notifications/claude/channel" {
				return
			}
		case <-ctx.Done():
			t.Fatal("no unsolicited channel notification while idle")
		}
	}
}
