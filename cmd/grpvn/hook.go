package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var hookFormatFlag string

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Agent-runtime hook entry points",
	Long: `Entry points the skill installer wires into agent-runtime lifecycle hooks.
Each subcommand reads the store once and emits the runtime's expected shape,
selected with --format (claude, codex, gemini, cursor). Every subcommand
fails open: a broken DB, missing state file, or unreadable stdin exits 0
with a note on stderr — a chat tool must never be able to break an agent's
turn lifecycle.`,
}

// failOpen is the shared error posture of every hook: report to stderr,
// exit 0.
func failOpen(name string, fn func() error) {
	if err := fn(); err != nil {
		fmt.Fprintf(os.Stderr, "grpvn hook %s: %v\n", name, err)
	}
}

// markerPath returns the throttle marker for a hook, kept next to the state
// file so per-runtime identities throttle independently.
func markerPath(kind, name string) string {
	return filepath.Join(filepath.Dir(internal.ResolveStatePath(statePathFlag)), "."+kind+"-"+name)
}

// hookStopCmd blocks ending the turn while unread messages exist (Claude
// Code and Codex: {"decision":"block"}; Cursor: followup_message). Claude
// Code's stop_hook_active caps the nudge at once per natural stop; Codex
// documents no such flag, so a marker file throttles it to one block per
// two minutes instead. Gemini is rejected — its nearest event retries the
// response on deny, a loop with no brake.
var hookStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop hook: block stopping while unread messages exist",
	Run: func(cmd *cobra.Command, args []string) {
		var payload struct {
			StopHookActive bool `json:"stop_hook_active"`
		}
		data, _ := io.ReadAll(os.Stdin)
		_ = json.Unmarshal(data, &payload)
		failOpen("stop", func() error {
			n, st, db, err := session()
			if err != nil {
				return err
			}
			defer db.Close()
			marker := ""
			if hookFormatFlag == internal.DialectCodex {
				marker = markerPath("stop", n)
			}
			return internal.HookStop(os.Stdout, db, st, hookFormatFlag, payload.StopHookActive, marker, 2*time.Minute)
		})
	},
}

// hookSessionStartCmd injects identity, follows, and pending unread into
// the session context, so the agent starts every session knowing who it is
// on grpvn without asking.
var hookSessionStartCmd = &cobra.Command{
	Use:   "session-start",
	Short: "SessionStart hook: inject identity and unread into context",
	Run: func(cmd *cobra.Command, args []string) {
		failOpen("session-start", func() error {
			_, st, db, err := session()
			if err != nil {
				return err
			}
			defer db.Close()
			return internal.HookSessionStart(os.Stdout, db, st, hookFormatFlag)
		})
	},
}

// hookPromptCmd surfaces unread at turn start (UserPromptSubmit on Claude
// Code and Codex, BeforeAgent on Gemini); silent when the inbox is clean.
var hookPromptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Prompt-submit hook: surface unread at turn start",
	Run: func(cmd *cobra.Command, args []string) {
		failOpen("prompt", func() error {
			_, st, db, err := session()
			if err != nil {
				return err
			}
			defer db.Close()
			return internal.HookPrompt(os.Stdout, db, st, hookFormatFlag)
		})
	},
}

var postToolEveryFlag time.Duration

// hookPostToolCmd gives mid-turn awareness during long-running work,
// throttled via a marker file next to the state file so at most one nudge
// lands per --every window.
var hookPostToolCmd = &cobra.Command{
	Use:   "posttool",
	Short: "Post-tool hook: throttled mid-turn unread notice",
	Run: func(cmd *cobra.Command, args []string) {
		failOpen("posttool", func() error {
			n, st, db, err := session()
			if err != nil {
				return err
			}
			defer db.Close()
			return internal.HookPostTool(os.Stdout, db, st, markerPath("posttool", n), postToolEveryFlag, hookFormatFlag)
		})
	},
}

func init() {
	hookCmd.PersistentFlags().StringVar(&hookFormatFlag, "format", internal.DialectClaude, "output dialect: claude, codex, gemini, or cursor")
	hookPostToolCmd.Flags().DurationVar(&postToolEveryFlag, "every", 60*time.Second, "minimum interval between mid-turn notices")
	hookCmd.AddCommand(hookStopCmd, hookSessionStartCmd, hookPromptCmd, hookPostToolCmd)
	rootCmd.AddCommand(hookCmd)
}
