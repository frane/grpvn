package internal

import (
	"os"
	"path/filepath"
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
