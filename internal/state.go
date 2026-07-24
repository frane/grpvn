package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Root is the project directory a project-scoped identity belongs to,
	// recorded at mint time so `grpvn doctor` can say which project a name
	// works in. Empty for runtime- and host-scoped identities.
	Root string `json:"root,omitempty"`
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
// base resolution chain is: explicit `--state` flag, $GRPVN_STATE env var,
// then $HOME/.grpvn/state.json.
//
// When project scope is active ($GRPVN_SCOPE=project, or the --scope flag,
// which the CLI forwards through the same env var), the base path becomes a
// template: the actual file is a sibling keyed by the current project root
// (state-claude-code.json → state-claude-code@grpvn-3f2a1b.json), so every
// project is its own grpvn participant with its own name, follows, and —
// because cursors are keyed by name — its own read position. Without this,
// one runtime is one identity and a session in project A consumes the
// unread meant for the session in project B. Claude Desktop and friends,
// which launch with unpredictable working directories, simply don't get
// scope wired and keep one identity per runtime.
func ResolveStatePath(override string) string {
	base := ResolveBaseStatePath(override)
	if os.Getenv("GRPVN_SCOPE") != "project" {
		return base
	}
	root := ProjectRoot()
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext) + "@" + projectSlug(root) + ext
}

// ResolveBaseStatePath is ResolveStatePath without the project-scope
// derivation: the runtime-level (or host-level) file a project-scoped
// identity seeds its follows from.
func ResolveBaseStatePath(override string) string {
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

// ProjectRoot finds the directory that identifies "this project": the
// nearest ancestor of the cwd containing .git (a directory or a worktree
// file), falling back to the cwd itself. The walk means `grpvn` run from a
// subdirectory of a repo still resolves to the same identity as one run at
// its root.
func ProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// projectSlug renders a project root as a short, filesystem-safe,
// human-scannable tag: a sanitized basename plus a hash so two directories
// with the same name stay distinct.
func projectSlug(root string) string {
	base := strings.ToLower(filepath.Base(root))
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 24 {
		slug = slug[:24]
	}
	if slug == "" {
		slug = "project"
	}
	sum := sha256.Sum256([]byte(root))
	return slug + "-" + hex.EncodeToString(sum[:3])
}

// LoadStateSeeded loads the state at path; when the file doesn't exist yet
// (a project's first grpvn touch), the new state inherits ONLY the default
// channel from seedPath — falling back to the host-level state.json — and
// records the project root. Deliberately not the follow list: inheriting
// every channel made each project's agent hear the whole host's chatter,
// and hooks turned that into constant interruptions about other projects.
// A project identity starts quiet instead — reachable by DM, following the
// default channel once it first posts (auto-follow), and subscribing to
// exactly the conversations it participates in.
func LoadStateSeeded(path, seedPath string) (*State, error) {
	if _, err := os.Stat(path); err == nil {
		return LoadState(path)
	}
	st := &State{Follow: []string{}}
	seed, err := LoadState(seedPath)
	if err != nil || seed.DefaultChannel == "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			if s2, err2 := LoadState(filepath.Join(home, ".grpvn", "state.json")); err2 == nil {
				seed = s2
			}
		}
	}
	if seed != nil {
		st.DefaultChannel = seed.DefaultChannel
	}
	st.Root = ProjectRoot()
	return st, nil
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
