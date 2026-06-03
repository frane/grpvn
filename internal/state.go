package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// State is the per-agent identity, follow list, default channel, and
// per-target read cursors. Persisted as JSON at the path returned by
// ResolveStatePath — by default $HOME/.grpvn/state.json so the file stays
// stable across cwds, with $GRPVN_STATE or --state to override per-agent.
//
// Cursor (singular, scalar) is the legacy v0.1.x field; new state files use
// Cursors. LoadState honours the scalar as a fallback so existing state
// files keep working through the transition.
type State struct {
	Name           string            `json:"name"`
	Cursor         string            `json:"cursor,omitempty"`
	Cursors        map[string]string `json:"cursors,omitempty"`
	DefaultChannel string            `json:"default_channel,omitempty"`
	Follow         []string          `json:"follow"`
}

// CursorFor returns the cursor for a given target (#channel or @user). An
// absent entry means "never read from this target" which the queries treat
// as "show me everything".
func (s *State) CursorFor(target string) string {
	if s.Cursors != nil {
		if c, ok := s.Cursors[target]; ok {
			return c
		}
	}
	// Legacy fallback: a v0.1.x state file has a single scalar applied to
	// every target. Honour it on first read; SetCursor migrates it across
	// and clears the scalar on the next save.
	return s.Cursor
}

// SetCursor records a per-target cursor. Always migrate via this helper so
// the legacy scalar field gets cleared once any per-target cursor is set.
func (s *State) SetCursor(target, id string) {
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
	}
	s.Cursors[target] = id
	s.Cursor = ""
}

func LoadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Follow: []string{}, Cursors: map[string]string{}}, nil
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
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
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
