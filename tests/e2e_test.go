package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "grpvn-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	binPath = filepath.Join(tmpDir, "grpvn")
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/grpvn")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Build failed: %v\n%s\n", err, string(output))
		panic(err)
	}

	os.Exit(m.Run())
}

func TestWriteSide(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	
	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "GRPVN_STATE="+filepath.Join(cwd, ".grpvn", "state.json"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				t.Fatalf("failed to run grpvn: %v", err)
			}
		}
		return stdout.String(), stderr.String(), exitCode
	}

	// Init
	stdout, stderr, code := run("init", "--as", "alice")
	if code != 0 {
		t.Fatalf("init failed with code %d. stderr: %s", code, stderr)
	}
	if stdout != "alice\n" {
		t.Errorf("expected alice, got %q", stdout)
	}

	// Send to default channel (which is empty in state, so should error)
	_, stderr, code = run("s", "hello")
	if code == 0 {
		t.Error("expected error for missing default_channel")
	}

	// Set default channel in state manually for now
	statePath := filepath.Join(cwd, ".grpvn", "state.json")
	stateContent := `{"name": "alice", "default_channel": "#dev"}`
	os.MkdirAll(filepath.Join(cwd, ".grpvn"), 0755)
	os.WriteFile(statePath, []byte(stateContent), 0644)

	// Send to #dev
	_, _, code = run("s", "hello world")
	if code != 0 {
		t.Errorf("send failed with code %d", code)
	}

	// Ask
	stdout, stderr, code = run("q", "@bob", "you there?")
	if code != 0 {
		t.Fatalf("ask failed with code %d. stderr: %s", code, stderr)
	}
	ulid := strings.TrimSpace(stdout)

	// Reply to ask
	_, _, code = run("s", ulid, "yes")
	if code != 0 {
		t.Errorf("reply failed with code %d", code)
	}
}

func TestReadSide(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	
	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "GRPVN_STATE="+filepath.Join(cwd, ".grpvn", "state.json"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				t.Fatalf("failed to run grpvn: %v", err)
			}
		}
		return stdout.String(), stderr.String(), exitCode
	}

	run("init", "--as", "bob")
	
	statePath := filepath.Join(cwd, ".grpvn", "state.json")
	stateContent := `{"name": "bob", "default_channel": "#dev", "follow": ["#dev"]}`
	os.WriteFile(statePath, []byte(stateContent), 0644)

	_, _, code := run("check")
	if code != 2 {
		t.Errorf("expected exit 2 for empty check, got %d", code)
	}

	run("--as", "alice", "s", "#dev", "hello bob")

	stdout, _, code := run("check")
	if code != 0 {
		t.Errorf("expected exit 0 for non-empty check, got %d", code)
	}
	if stdout != "1 #dev\n" {
		t.Errorf("expected '1 #dev', got %q", stdout)
	}

	stdout, _, code = run("p")
	if !strings.Contains(stdout, "alice: hello bob") {
		t.Errorf("peek output missing message: %q", stdout)
	}

	run("r")
	_, _, code = run("check")
	if code != 2 {
		t.Errorf("check after read should be 2, got %d", code)
	}
}

func TestAuxVerbs(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	
	run := func(args ...string) (string, string, int) {
		cmd := exec.Command(binPath, args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "GRPVN_STATE="+filepath.Join(cwd, ".grpvn", "state.json"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				t.Fatalf("failed to run grpvn: %v", err)
			}
		}
		return stdout.String(), stderr.String(), exitCode
	}

	run("init", "--as", "charlie")
	
	statePath := filepath.Join(cwd, ".grpvn", "state.json")
	stateContent := `{"name": "charlie", "follow": ["#test", "#other"]}`
	os.WriteFile(statePath, []byte(stateContent), 0644)

	run("s", "#test", "message one")
	time.Sleep(10 * time.Millisecond)
	run("s", "#test", "message two")
	time.Sleep(10 * time.Millisecond)
	run("s", "#other", "unrelated")
	
	stdout, stderr, _ := run("g", "message")
	if !strings.Contains(stdout, "message one") || !strings.Contains(stdout, "message two") {
		t.Errorf("grep missing matches. stdout: %q, stderr: %s", stdout, stderr)
	}
	
	stdout, _, _ = run("l", "#test")
	if !strings.Contains(stdout, "message one") || !strings.Contains(stdout, "message two") {
		t.Errorf("log #test missing messages: %q", stdout)
	}
	
	stdout, _, _ = run("--full", "l", "#test", "-n", "1")
	id := strings.Fields(stdout)[0]
	
	run("m", id)
	stdout, stderr, _ = run("m")
	if !strings.Contains(stdout, id[:6]) {
		t.Errorf("mark list missing id %s. stdout: %q, stderr: %s", id, stdout, stderr)
	}
	
	run("m", "-d", id)
	stdout, _, _ = run("m")
	if strings.Contains(stdout, id[:6]) {
		t.Errorf("mark list should not contain id %s after delete: %q", id, stdout)
	}
}
