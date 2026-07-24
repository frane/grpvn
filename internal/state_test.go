package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "state.json")
	s := &State{
		Name:           "alice",
		Cursor:         "01HQ7P0000000000000000000A",
		DefaultChannel: "#dev",
		Follow:         []string{"#dev", "#ops"},
	}
	if err := s.Save(p); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadState(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Name != s.Name || loaded.Cursor != s.Cursor || loaded.DefaultChannel != s.DefaultChannel {
		t.Fatalf("scalar mismatch: %+v vs %+v", loaded, s)
	}
	if len(loaded.Follow) != 2 || loaded.Follow[0] != "#dev" || loaded.Follow[1] != "#ops" {
		t.Fatalf("follow mismatch: %v", loaded.Follow)
	}
}

func TestStateLoadMissingReturnsZero(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nope.json")
	s, err := LoadState(p)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if s.Name != "" || s.Cursor != "" || s.DefaultChannel != "" {
		t.Fatalf("expected zero state, got %+v", s)
	}
	if s.Follow == nil {
		t.Fatal("Follow should be initialized to non-nil empty slice")
	}
	if len(s.Follow) != 0 {
		t.Fatalf("Follow should be empty, got %v", s.Follow)
	}
}

func TestStateSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	// pre-populate with valid content
	s := &State{Name: "a", Follow: []string{}}
	if err := s.Save(p); err != nil {
		t.Fatal(err)
	}
	// confirm tmp file from previous save was cleaned up
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Fatalf("orphan file from atomic save: %s", e.Name())
		}
	}
}

func TestStateLoadCorruptReturnsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := os.WriteFile(p, []byte("not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(p); err == nil {
		t.Fatal("corrupt state should error")
	}
}

// Project scope keys the state file by the project root: same root, same
// file; different root, different file; scope off, base file.
func TestResolveStatePathProjectScope(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("GRPVN_STATE", filepath.Join(home, ".grpvn", "state-claude-code.json"))
	t.Setenv("GRPVN_SCOPE", "")

	base := ResolveStatePath("")
	if filepath.Base(base) != "state-claude-code.json" {
		t.Fatalf("unscoped resolution should return the base file, got %q", base)
	}

	repo := filepath.Join(home, "work", "myrepo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, repo)
	t.Setenv("GRPVN_SCOPE", "project")

	scoped := ResolveStatePath("")
	name := filepath.Base(scoped)
	if !strings.HasPrefix(name, "state-claude-code@myrepo-") || !strings.HasSuffix(name, ".json") {
		t.Fatalf("scoped file should be keyed by project slug, got %q", name)
	}
	// From a subdirectory of the same repo the identity must not change.
	chdir(t, sub)
	if got := ResolveStatePath(""); got != scoped {
		t.Fatalf("subdir resolved to a different identity: %q vs %q", got, scoped)
	}
	// A different project resolves elsewhere.
	other := filepath.Join(home, "work", "other")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)
	if got := ResolveStatePath(""); got == scoped {
		t.Fatalf("two projects resolved to the same identity: %q", got)
	}
}

// A project's first grpvn touch inherits ONLY the default channel from the
// runtime base file — inheriting the follow list made every project hear
// the whole host's chatter.
func TestLoadStateSeededInheritsFollows(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	basePath := filepath.Join(home, ".grpvn", "state-claude-code.json")
	base := &State{Name: "runtime-name", DefaultChannel: "#dev", Follow: []string{"#dev", "#ops"}}
	if err := base.Save(basePath); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	chdir(t, repo)

	st, err := LoadStateSeeded(filepath.Join(home, ".grpvn", "state-claude-code@repo-abc123.json"), basePath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "" {
		t.Fatalf("seeded project state must not inherit the runtime name, got %q", st.Name)
	}
	if st.DefaultChannel != "#dev" {
		t.Fatalf("seeded state should inherit the default channel: %#v", st)
	}
	if len(st.Follow) != 0 {
		t.Fatalf("seeded state must not inherit follows: %#v", st.Follow)
	}
	// Compare against Getwd, the same source ProjectRoot uses — macOS
	// resolves /var -> /private/var and Windows may report 8.3 short
	// paths, so any independently-derived expectation drifts.
	wantRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if st.Root != wantRoot {
		t.Fatalf("seeded state should record the project root %q, got %q", wantRoot, st.Root)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
