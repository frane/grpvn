package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startCoordinator(t *testing.T, r *runner) (string, func()) {
	t.Helper()
	return startCoordinatorArgs(t, r, "--private")
}

func startCoordinatorArgs(t *testing.T, r *runner, flags ...string) (string, func()) {
	t.Helper()
	args := append([]string{"sv", "--listen", "127.0.0.1:0"}, flags...)
	cmd := exec.Command(r.bin, args...)
	cmd.Dir, cmd.Env = r.cwd, r.env
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(stop)
	ready := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stderr)
		line, _ := reader.ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ready:
		const prefix = "grpvn coordinator listening on "
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("coordinator failed to start: %s", line)
		}
		return "http://" + strings.TrimPrefix(line, prefix), stop
	case <-time.After(10 * time.Second):
		t.Fatal("coordinator did not start")
	}
	return "", stop
}

func TestDistributedCLI(t *testing.T) {
	t.Parallel()
	host := newRunner(t, "host").withSharedDB(filepath.Join(t.TempDir(), "coordinator.db"))
	host.mustRun("q", "#dev", "review this")
	endpoint, stop := startCoordinator(t, host)
	a, b := newRunner(t, "alice"), newRunner(t, "bob")
	invite := strings.TrimSpace(host.mustRun("iv", "--to", "team", "--channel", "*", "--write", "--uses", "2"))
	a.mustRun("j", "work", endpoint, "#dev", "#ops", "--invite", invite)
	b.mustRun("connect", "work", endpoint, "#dev", "--invite", invite)
	rootID := strings.Fields(b.mustRun("l", "#work/dev", "--full"))[0]
	b.mustRun("s", rootID, "approved")
	a.mustRun("sy", "work") // explicit freshness bypasses the idle polling delay
	if out := a.mustRun("r", "#work/dev"); !strings.Contains(out, "review this") || !strings.Contains(out, "approved") {
		t.Fatalf("remote read: %s", out)
	}
	if out := host.mustRun("l", "#dev"); !strings.Contains(out, "approved") {
		t.Fatalf("reply missing from coordinator: %s", out)
	}
	a.mustRun("s", "#dev", "local only")
	if out := host.mustRun("l", "#dev"); strings.Contains(out, "local only") {
		t.Fatal("local message uploaded")
	}
	b.mustRun("follow", "#work/new")
	b.mustRun("default", "#work/new")
	b.mustRun("s", "new channel")
	if out := host.mustRun("l", "#new"); !strings.Contains(out, "new channel") {
		t.Fatalf("remote default/follow failed: %s", out)
	}
	stop()
	if out := a.mustRun("s", "#work/dev", "queued offline", "--idempotency", "offline-1"); !strings.Contains(out, " queued") {
		t.Fatalf("missing queued ack: %s", out)
	}
	if out := a.mustRun("l", "#work/dev"); !strings.Contains(out, "approved") || !strings.Contains(out, "queued offline") {
		t.Fatalf("offline cache unreadable: %s", out)
	}
	if out := a.mustRun("re"); !strings.Contains(out, "pending=1") || strings.Contains(out, "test-secret") {
		t.Fatalf("remote status: %s", out)
	}
	newEndpoint, _ := startCoordinator(t, host)
	a.mustRun("connect", "work", newEndpoint, "#dev")
	a.mustRun("sy", "work")
	a.mustRun("s", "#work/dev", "queued offline", "--idempotency", "offline-1")
	if out := host.mustRun("l", "#dev"); strings.Count(out, "queued offline") != 1 {
		t.Fatalf("retry duplicated offline message: %s", out)
	}
}

func TestMCPConnectRemote(t *testing.T) {
	t.Parallel()
	host := newRunner(t, "host")
	host.mustRun("s", "#dev", "remote MCP message")
	endpoint, _ := startCoordinator(t, host)
	invite := strings.TrimSpace(host.mustRun("iv", "--to", "team", "--channel", "*", "--write"))
	client := newRunner(t, "alice")
	cmd := exec.Command(client.bin, "serve")
	cmd.Dir, cmd.Env = client.cwd, client.env
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	decoder := json.NewDecoder(out)
	call := func(id int, method string, params any) string {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := fmt.Fprintln(in, string(request)); err != nil {
			t.Fatal(err)
		}
		done := make(chan string, 1)
		go func() {
			var response json.RawMessage
			if err := decoder.Decode(&response); err != nil {
				done <- "decode failed: " + err.Error()
				return
			}
			done <- string(response)
		}()
		select {
		case response := <-done:
			if strings.Contains(response, `"isError":true`) || strings.Contains(response, `"error":`) || strings.HasPrefix(response, "decode failed:") {
				t.Fatalf("MCP %s: %s", method, response)
			}
			return response
		case <-time.After(10 * time.Second):
			t.Fatalf("MCP %s timed out", method)
		}
		return ""
	}
	call(1, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	fmt.Fprintln(in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	call(2, "tools/call", map[string]any{"name": "connect", "arguments": map[string]any{"name": "work", "url": endpoint, "channels": []string{"#dev"}, "invite": invite}})
	if response := call(3, "tools/call", map[string]any{"name": "r", "arguments": map[string]any{"target": "#work/dev"}}); !strings.Contains(response, "remote MCP message") {
		t.Fatalf("MCP did not read remote: %s", response)
	}
	call(4, "tools/call", map[string]any{"name": "s", "arguments": map[string]any{"target": "#work/dev", "body": "MCP reply"}})
	if out := host.mustRun("l", "#dev"); !strings.Contains(out, "MCP reply") {
		t.Fatalf("MCP message not uploaded: %s", out)
	}
}
