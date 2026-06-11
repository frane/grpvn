package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Agent-runtime hook entry points",
}

// hookStopCmd is the command `grpvn skill install` wires into Claude Code's
// Stop hook. When the agent tries to end its turn with unread grpvn
// messages, it emits {"decision":"block", ...} so the agent reads and
// replies first. Two properties matter more than anything it reports:
//
//   - It must never loop. Claude Code sets stop_hook_active in the hook
//     payload when the agent is already continuing because a stop hook
//     blocked it; we let that stop through unconditionally, so the agent is
//     nudged at most once per natural stop.
//   - It must fail open. A broken DB, missing state file, or unreadable
//     stdin exits 0 with a note on stderr — a chat tool must not be able to
//     trap an agent in its turn.
var hookStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Claude Code Stop hook: block stopping while unread messages exist",
	Run: func(cmd *cobra.Command, args []string) {
		var payload struct {
			StopHookActive bool `json:"stop_hook_active"`
		}
		data, _ := io.ReadAll(os.Stdin)
		_ = json.Unmarshal(data, &payload)
		if payload.StopHookActive {
			return
		}
		_, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, "grpvn hook stop:", err)
			return
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, "grpvn hook stop:", err)
			return
		}
		defer db.Close()
		var buf bytes.Buffer
		code, err := internal.Check(&buf, db, st)
		if err != nil {
			fmt.Fprintln(os.Stderr, "grpvn hook stop:", err)
			return
		}
		if code != 0 {
			return
		}
		out, err := json.Marshal(map[string]string{
			"decision": "block",
			"reason": fmt.Sprintf(
				"Unread grpvn messages: %s. Read them with the grpvn r tool (or `grpvn r`) and reply to any questions before stopping.",
				strings.TrimSpace(buf.String()),
			),
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "grpvn hook stop:", err)
			return
		}
		fmt.Println(string(out))
	},
}

func init() {
	hookCmd.AddCommand(hookStopCmd)
	rootCmd.AddCommand(hookCmd)
}
