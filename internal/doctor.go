package internal

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Doctor prints a diagnosis of the local grpvn setup, aimed at the failure
// modes that make notifications silently dead: an identity that follows no
// channels (a mailbox channel traffic can never reach), multiple identities
// on one host that other agents don't know to address, and a Claude Code
// settings file missing the hooks or permissions that carry the nudges.
// Every problem is a "warn" line with the command that fixes it; hard
// failures (unreadable home) are the only returned errors.
func Doctor(w io.Writer, home, activeStatePath string) error {
	dir := filepath.Join(home, ".grpvn")
	paths, err := filepath.Glob(filepath.Join(dir, "state*.json"))
	if err != nil {
		return err
	}
	sort.Strings(paths)
	names := map[string]string{} // identity name -> state file
	for _, p := range paths {
		st, err := LoadState(p)
		if err != nil {
			fmt.Fprintf(w, "warn  %s: unreadable: %v\n", p, err)
			continue
		}
		active := ""
		if p == activeStatePath {
			active = " (active here)"
		}
		scope := ""
		if st.Root != "" {
			scope = " project=" + st.Root
		}
		fmt.Fprintf(w, "ok    %s: %s follows %d channel(s)%s%s\n", filepath.Base(p), st.Name, len(st.Follow), scope, active)
		if st.Name != "" {
			names[st.Name] = p
		}
		// A quiet start is normal for project identities (they subscribe by
		// posting); a runtime- or host-level identity with nothing at all
		// is still a dead mailbox.
		if len(st.Follow) == 0 && st.DefaultChannel == "" && st.Root == "" {
			fmt.Fprintf(w, "warn  %s follows no channels — channel traffic will never show as unread for it; run `grpvn --state %q follow '#channel'` or re-run `grpvn skill install` to seed it\n", st.Name, p)
		}
	}
	if len(paths) == 0 {
		fmt.Fprintf(w, "warn  no state files under %s — run `grpvn init`\n", dir)
	}
	if len(names) > 1 {
		list := make([]string, 0, len(names))
		for n := range names {
			list = append(list, n)
		}
		sort.Strings(list)
		fmt.Fprintf(w, "note  %d identities on this host (%s) — DMs must address the runtime that should receive them\n", len(list), strings.Join(list, ", "))
	}

	if st, err := LoadState(activeStatePath); err == nil && st.Name != "" {
		if db, err := OpenDB(); err != nil {
			fmt.Fprintf(w, "warn  database: %v\n", err)
		} else {
			defer db.Close()
			line, err := UnreadLine(db, st)
			switch {
			case err != nil:
				fmt.Fprintf(w, "warn  unread check as %s: %v\n", st.Name, err)
			case line == "":
				fmt.Fprintf(w, "ok    no unread for %s\n", st.Name)
			default:
				fmt.Fprintf(w, "ok    unread for %s: %s\n", st.Name, line)
			}
		}
	}

	doctorClaudeSettings(w, filepath.Join(home, ".claude"))
	return nil
}

// doctorClaudeSettings verifies that the Claude Code settings carry every
// grpvn hook and permission the installer writes. Skipped entirely when
// ~/.claude doesn't exist.
func doctorClaudeSettings(w io.Writer, claudeDir string) {
	if _, err := os.Stat(claudeDir); err != nil {
		return
	}
	path := filepath.Join(claudeDir, "settings.json")
	var doc map[string]interface{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			fmt.Fprintf(w, "warn  %s: unparseable: %v\n", path, err)
			return
		}
	}
	var missing []string
	hooks, _ := doc["hooks"].(map[string]interface{})
	for _, spec := range grpvnHooks {
		present := false
		groups, _ := hooks[spec.Event].([]interface{})
		for _, group := range groups {
			g, _ := group.(map[string]interface{})
			entries, _ := g["hooks"].([]interface{})
			for _, entry := range entries {
				e, _ := entry.(map[string]interface{})
				c, _ := e["command"].(string)
				if strings.Contains(c, "grpvn") && strings.Contains(c, spec.Sub) {
					present = true
				}
			}
		}
		if !present {
			missing = append(missing, spec.Event+" hook")
		}
	}
	perms, _ := doc["permissions"].(map[string]interface{})
	allow, _ := perms["allow"].([]interface{})
	for _, want := range grpvnPermissions {
		present := false
		for _, have := range allow {
			if s, _ := have.(string); s == want {
				present = true
			}
		}
		if !present {
			missing = append(missing, "permission "+want)
		}
	}
	env, _ := doc["env"].(map[string]interface{})
	if _, ok := env["GRPVN_STATE"]; !ok {
		missing = append(missing, "env GRPVN_STATE")
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "warn  %s missing: %s — run `grpvn skill install`\n", path, strings.Join(missing, ", "))
		return
	}
	fmt.Fprintf(w, "ok    %s carries all grpvn hooks, permissions, and env\n", path)
}
