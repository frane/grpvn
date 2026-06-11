package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State is the per-agent identity, follow list, and default channel.
// Persisted as JSON at the path returned by ResolveStatePath — by default
// $HOME/.grpvn/state.json so the file stays stable across cwds, with
// $GRPVN_STATE or --state to override per-agent.
//
// Read cursors live in the database since schema v2 (see cursors.go).
// Cursor (the v0.1.x scalar) and Cursors (the v0.1.5 per-target map) are
// parsed only so MigrateLegacyCursors can move them into the DB; they are
// cleared on the next save and never written by new code.
type State struct {
	Name           string            `json:"name"`
	Cursor         string            `json:"cursor,omitempty"`
	Cursors        map[string]string `json:"cursors,omitempty"`
	DefaultChannel string            `json:"default_channel,omitempty"`
	Follow         []string          `json:"follow"`
}

func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Follow: []string{}}, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}
	if s.Follow == nil {
		s.Follow = []string{}
	}
	return &s, nil
}

// ResolveStatePath returns the absolute path of the active state file. The
// override chain is: explicit `--state` flag, $GRPVN_STATE env var, then
// $HOME/.grpvn/state.json. The historic cwd-relative `./.grpvn/state.json`
// is no longer the default — Claude Desktop and other MCP hosts launch with
// unpredictable working directories, so a per-cwd default caused fresh
// identities on every call.
func ResolveStatePath(override string) string {
	if override != "" {
		return override
	}
	if p := os.Getenv("GRPVN_STATE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".grpvn/state.json"
	}
	return filepath.Join(home, ".grpvn", "state.json")
}

func (s *State) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	defer os.Remove(tmpPath)
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		f.Close()
		return fmt.Errorf("marshal state: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fsync state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename state: %w", err)
	}
	return nil
}
