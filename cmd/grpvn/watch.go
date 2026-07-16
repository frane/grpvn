package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var (
	watchExecFlag     string
	watchCooldownFlag time.Duration
	watchOnceFlag     bool
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Optional daemon: block on the store and handle messages as they arrive",
	Long: `A foreground supervisor loop: w in a loop. Block until unread messages
arrive, handle them, block again. Without --exec each batch is printed and
marked read — a live inbox. With --exec the command runs once per wake-up
(sh -c) and is expected to read the inbox itself, e.g.:

  grpvn watch --exec 'claude -p "$(grpvn r)"'

Reading stays with the responder so a crash before reading leaves the
messages unread for the next attempt. While the responder leaves the inbox
unread it is re-run at most once per --cooldown; its own replies never
re-trigger a wake-up. Blocking costs one PRAGMA data_version poll per
quarter-second — cheap enough to leave running for days. Stop with Ctrl-C.

The responder inherits this watcher's identity: GRPVN_STATE is pinned in
its environment, so the $(grpvn r) substitution above reads the watcher's
inbox (expanded fresh at each wake-up, before the agent launches). Agent
runtimes wired by 'grpvn skill install' set their own GRPVN_STATE, so
grpvn calls made from INSIDE such an agent read that runtime's identity
instead — keep the $(grpvn r) idiom, or point watch at the runtime's state
file with --state so the identities coincide. And the usual rule applies:
one identity, one reader — don't run watch as an identity that also has a
live session, or the two race for the same unread.

This is opt-in: nothing in grpvn starts or supervises it. Run it in a spare
terminal, a background shell task, or under systemd/launchd.`,
	Run: func(cmd *cobra.Command, args []string) {
		_, _, db := mustSession()
		defer db.Close()
		load := func() (*internal.State, error) {
			_, st, err := bootstrap()
			return st, err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		// Pin the responder's identity resolution to the watcher's: the
		// resolved path already carries any project scoping, so scope must
		// read as inert in the child or it would be applied twice.
		env := []string{
			"GRPVN_STATE=" + internal.ResolveStatePath(statePathFlag),
			"GRPVN_SCOPE=host",
		}
		if asFlag != "" {
			env = append(env, "GRPVN_AS="+asFlag)
		}
		err := internal.Watch(ctx, os.Stdout, os.Stderr, db, load, internal.WatchOpts{
			Exec:     watchExecFlag,
			Env:      env,
			Cooldown: watchCooldownFlag,
			Interval: 250 * time.Millisecond,
			Once:     watchOnceFlag,
			Limit:    countFlag,
			TS:       tsFlag,
			Full:     fullFlag,
			Human:    humanFlag,
			Color:    colorFlag,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

func init() {
	watchCmd.Flags().StringVar(&watchExecFlag, "exec", "", "shell command run on each wake-up; it should read the inbox (default: print and mark read)")
	watchCmd.Flags().DurationVar(&watchCooldownFlag, "cooldown", 60*time.Second, "minimum interval between --exec runs while unread persists")
	watchCmd.Flags().BoolVar(&watchOnceFlag, "once", false, "handle one wake-up, then exit")
	rootCmd.AddCommand(watchCmd)
}
