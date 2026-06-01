package state

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStateSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.json")

	s := &State{
		Name:           "test-agent",
		Cursor:         "01HQ7K",
		DefaultChannel: "#dev",
		Follow:         []string{"#dev", "#ops"},
	}

	if err := s.Save(path); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if !reflect.DeepEqual(s, loaded) {
		t.Errorf("loaded state %+v != original %+v", loaded, s)
	}
}

func TestLoadNonExistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load() non-existent failed: %v", err)
	}
	if s.Name != "" {
		t.Errorf("expected empty Name, got %q", s.Name)
	}
	if len(s.Follow) != 0 {
		t.Errorf("expected empty Follow, got %v", s.Follow)
	}
}
