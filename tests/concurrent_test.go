package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type runner struct {
	t    *testing.T
	bin  string
	home string
	cwd  string
	env  []string
}

func newRunner(t *testing.T, name string) *runner {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	statePath := filepath.Join(cwd, ".grpvn", "state.json")
	r := &runner{
		t:    t,
		bin:  binPath,
		home: home,
		cwd:  cwd,
		env: append(os.Environ(),
			"HOME="+home,
			"GRPVN_STATE="+statePath,
		),
	}
	if name != "" {
		out, _, code := r.run("init", "--as", name)
		if code != 0 {
			t.Fatalf("init %s failed: %s", name, out)
		}
	}
	return r
}

func (r *runner) withSharedDB(dbPath string) *runner {
	r.env = append(r.env, "GRPVN_DB="+dbPath)
	return r
}

func (r *runner) run(args ...string) (string, string, int) {
	r.t.Helper()
	cmd := exec.Command(r.bin, args...)
	cmd.Dir = r.cwd
	cmd.Env = r.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			r.t.Fatalf("exec %v: %v", args, err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func (r *runner) mustRun(args ...string) string {
	r.t.Helper()
	stdout, stderr, code := r.run(args...)
	if code != 0 {
		r.t.Fatalf("grpvn %v exited %d: stderr=%s stdout=%s", args, code, stderr, stdout)
	}
	return stdout
}

// Two agents share one SQLite DB. Each writes N messages concurrently to the
// same channel. All 2N rows must land on disk, ULIDs must remain unique, and
// the reader must see every message exactly once. This exercises the WAL claim
// the README makes.
func TestConcurrentWriters(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")

	alice := newRunner(t, "alice").withSharedDB(dbPath)
	bob := newRunner(t, "bob").withSharedDB(dbPath)
	carol := newRunner(t, "carol").withSharedDB(dbPath)
	alice.mustRun("follow", "#bench")
	bob.mustRun("follow", "#bench")
	carol.mustRun("follow", "#bench")

	const perAgent = 25
	var wg sync.WaitGroup
	errs := make(chan error, 3*perAgent)
	send := func(r *runner, label string) {
		defer wg.Done()
		for i := 0; i < perAgent; i++ {
			_, stderr, code := r.run("s", "#bench", fmt.Sprintf("%s-%d", label, i))
			if code != 0 {
				errs <- fmt.Errorf("%s send %d: %s", label, i, stderr)
				return
			}
		}
	}
	wg.Add(3)
	go send(alice, "alice")
	go send(bob, "bob")
	go send(carol, "carol")
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}

	out := carol.mustRun("--full", "l", "#bench")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3*perAgent {
		t.Fatalf("expected %d messages in #bench, got %d\n%s", 3*perAgent, len(lines), out)
	}
	seen := map[string]bool{}
	bodies := map[string]int{}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 1 {
			t.Fatalf("malformed line: %q", line)
		}
		id := fields[0]
		if seen[id] {
			t.Fatalf("duplicate ULID: %s", id)
		}
		seen[id] = true
		body := fields[len(fields)-1]
		bodies[body]++
	}
	for _, who := range []string{"alice", "bob", "carol"} {
		for i := 0; i < perAgent; i++ {
			key := fmt.Sprintf("%s-%d", who, i)
			if bodies[key] != 1 {
				t.Fatalf("expected exactly 1 %q, got %d", key, bodies[key])
			}
		}
	}
}

// A reader's cursor must advance monotonically across concurrent writes. Once
// `r` consumes a batch, a second `c` must see zero unread, even if a writer
// raced the read.
func TestReadCursorIsMonotonic(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	writer := newRunner(t, "writer").withSharedDB(dbPath)
	reader := newRunner(t, "reader").withSharedDB(dbPath)
	reader.mustRun("follow", "#ch")

	for i := 0; i < 10; i++ {
		writer.mustRun("s", "#ch", fmt.Sprintf("m%d", i))
	}
	reader.mustRun("r")
	_, _, code := reader.run("c")
	if code != 2 {
		t.Fatalf("expected exit 2 after reading all, got %d", code)
	}

	writer.mustRun("s", "#ch", "extra")
	stdout, _, code := reader.run("c")
	if code != 0 {
		t.Fatalf("expected exit 0 with one new message, got %d", code)
	}
	if !strings.Contains(stdout, "1 #ch") {
		t.Fatalf("expected '1 #ch', got %q", stdout)
	}
}

// Direct messages must only reach the addressed user. Two agents on the same
// DB should not see each other's @-mail when not addressed.
func TestDirectMessagesAreScoped(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	alice := newRunner(t, "alice").withSharedDB(dbPath)
	bob := newRunner(t, "bob").withSharedDB(dbPath)
	carol := newRunner(t, "carol").withSharedDB(dbPath)

	alice.mustRun("s", "@bob", "for you")
	_, _, code := carol.run("c")
	if code != 2 {
		t.Fatalf("carol should not see alice->bob DM, got exit %d", code)
	}
	out := bob.mustRun("c")
	if !strings.Contains(out, "@me") {
		t.Fatalf("bob should see 1 @me, got %q", out)
	}
}

// q (ask) returns a correlation ULID. Replying with `s <ULID> ...` must thread
// under the original (chain_root equal, chain_depth increment).
func TestAskAndReplyChain(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	alice := newRunner(t, "alice").withSharedDB(dbPath)
	bob := newRunner(t, "bob").withSharedDB(dbPath)

	id := strings.TrimSpace(alice.mustRun("q", "@bob", "ready?"))
	if len(id) != 26 {
		t.Fatalf("ask should return 26-char ULID, got %q", id)
	}
	bob.mustRun("s", id, "yes")

	out := alice.mustRun("--full", "l", id)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 messages in thread, got %d\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "reply:"+id) {
		t.Fatalf("second line should have reply trailer: %q", lines[1])
	}
}

// Chain depth is capped at 8. The ninth reply must be rejected.
func TestChainDepthLimit(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	a := newRunner(t, "agent").withSharedDB(dbPath)
	a.mustRun("follow", "#deep")
	a.mustRun("default", "#deep")

	id := strings.TrimSpace(a.mustRun("q", "#deep", "root"))
	current := id
	for i := 0; i < 8; i++ {
		// Each reply derives its ULID from stdout when isAsk is set; here we
		// chain through `s`, so we need to fetch the new ULID via log.
		a.mustRun("s", current, fmt.Sprintf("reply-%d", i))
		out := a.mustRun("--full", "l", id)
		lines := strings.Split(strings.TrimSpace(out), "\n")
		fields := strings.Fields(lines[len(lines)-1])
		current = fields[0]
	}
	_, stderr, code := a.run("s", current, "ninth")
	if code == 0 {
		t.Fatalf("ninth reply should fail; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "chain depth") {
		t.Fatalf("expected chain depth error, got %q", stderr)
	}
}

// `follow` and `default` subcommands must round-trip through state.json.
func TestFollowAndDefaultCommands(t *testing.T) {
	t.Parallel()
	a := newRunner(t, "a")
	a.mustRun("follow", "#one", "#two")
	a.mustRun("default", "#one")

	out := a.mustRun("follow")
	if !strings.Contains(out, "#one") || !strings.Contains(out, "#two") {
		t.Fatalf("follow list missing entries: %q", out)
	}
	out = a.mustRun("default")
	if strings.TrimSpace(out) != "#one" {
		t.Fatalf("default should be #one, got %q", out)
	}

	a.mustRun("follow", "-d", "#one")
	out = a.mustRun("follow")
	if strings.Contains(out, "#one") {
		t.Fatalf("#one should be removed: %q", out)
	}
	if !strings.Contains(out, "#two") {
		t.Fatalf("#two should remain: %q", out)
	}
}

// `peek` shows unread without advancing the cursor; subsequent `read` returns
// the same messages again.
func TestPeekDoesNotAdvance(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	writer := newRunner(t, "w").withSharedDB(dbPath)
	reader := newRunner(t, "r").withSharedDB(dbPath)
	reader.mustRun("follow", "#x")

	writer.mustRun("s", "#x", "hello")
	first := reader.mustRun("p")
	second := reader.mustRun("p")
	if first != second {
		t.Fatalf("peek should be idempotent: %q vs %q", first, second)
	}
	reader.mustRun("r")
	_, _, code := reader.run("c")
	if code != 2 {
		t.Fatalf("after read, check should be 2, got %d", code)
	}
}

// `grep` returns only matching messages, scoped to follows + @me by default.
func TestGrepScope(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	a := newRunner(t, "a").withSharedDB(dbPath)
	a.mustRun("follow", "#ch1")
	a.mustRun("s", "#ch1", "apple")
	a.mustRun("s", "#ch1", "apricot")
	a.mustRun("s", "#ch2", "banana") // not followed

	out := a.mustRun("g", "^ap")
	if !strings.Contains(out, "apple") || !strings.Contains(out, "apricot") {
		t.Fatalf("grep should match apple/apricot: %q", out)
	}
	if strings.Contains(out, "banana") {
		t.Fatalf("grep should not include unfollowed channel: %q", out)
	}

	// Explicit scope override.
	out = a.mustRun("g", "banana", "#ch2")
	if !strings.Contains(out, "banana") {
		t.Fatalf("explicit scope should hit: %q", out)
	}
}

// Marks survive across invocations and disappear on delete.
func TestMarksRoundTrip(t *testing.T) {
	t.Parallel()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "grpvn.db")
	a := newRunner(t, "a").withSharedDB(dbPath)
	a.mustRun("follow", "#m")
	a.mustRun("s", "#m", "to-mark")
	out := a.mustRun("--full", "l", "#m", "-n", "1")
	id := strings.Fields(out)[0]

	a.mustRun("m", id)
	out = a.mustRun("m")
	if !strings.Contains(out, id[:6]) {
		t.Fatalf("mark list should include id: %q", out)
	}
	a.mustRun("m", "-d", id)
	out = a.mustRun("m")
	if strings.Contains(out, id[:6]) {
		t.Fatalf("mark list should not include id after delete: %q", out)
	}
}

// `id` prints a real newline-terminated identity, not the literal "\n".
func TestIdCommandPrintsNewline(t *testing.T) {
	t.Parallel()
	a := newRunner(t, "agent42")
	out := a.mustRun("id")
	if strings.Contains(out, `\n`) {
		t.Fatalf("id output contains literal backslash-n: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("id output should end with newline: %q", out)
	}
	if !strings.HasPrefix(out, "agent42@") {
		t.Fatalf("id output should start with agent42@: %q", out)
	}
}

// `version` prints something.
func TestVersionCommand(t *testing.T) {
	t.Parallel()
	a := newRunner(t, "")
	out := a.mustRun("version")
	if strings.TrimSpace(out) == "" {
		t.Fatalf("version should not be empty")
	}
}

// init --force replaces an existing state file.
func TestInitForce(t *testing.T) {
	t.Parallel()
	a := newRunner(t, "first")
	_, stderr, code := a.run("init", "--as", "second")
	if code == 0 {
		t.Fatalf("init without --force should fail when state exists; stderr=%s", stderr)
	}
	out := a.mustRun("init", "--force", "--as", "second")
	if strings.TrimSpace(out) != "second" {
		t.Fatalf("init --force should reset name: %q", out)
	}
}

// MCP serve must start and respond to a basic JSON-RPC initialize call over
// stdio. This protects the MCP integration claimed in the README.
func TestMCPServeInitialize(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cwd := t.TempDir()
	cmd := exec.Command(binPath, "serve")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GRPVN_STATE="+filepath.Join(cwd, ".grpvn", "state.json"),
	)
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
	defer cmd.Process.Kill()

	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}` + "\n"
	if _, err := stdin.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	n, err := stdout.Read(buf)
	if err != nil {
		t.Fatalf("read mcp response: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, `"jsonrpc"`) {
		t.Fatalf("expected jsonrpc response, got %q", resp)
	}
	if !strings.Contains(resp, `"result"`) && !strings.Contains(resp, `"error"`) {
		t.Fatalf("expected result/error in response, got %q", resp)
	}
}
