package tests

import (
	"os"
	"strings"
)

// cleanEnviron returns os.Environ() minus every GRPVN_* variable. The skill
// installer sets GRPVN_STATE session-wide in agent runtimes (and developers
// export GRPVN_DB/GRPVN_AS themselves), so a bare os.Environ() in a spawned
// grpvn would silently redirect state or the store away from the test's
// temp HOME. Tests that need an override append it after this.
func cleanEnviron() []string {
	env := os.Environ()
	out := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "GRPVN_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
