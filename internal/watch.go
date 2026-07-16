package internal

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// WatchOpts configures a Watch loop.
type WatchOpts struct {
	// Exec is a shell command run once per wake-up (`sh -c`, `cmd /c` on
	// Windows). Empty selects print mode: unread is rendered to out and
	// marked read, like `grpvn r`.
	Exec string
	// Env entries are appended to the responder's inherited environment.
	// The CLI pins GRPVN_STATE/GRPVN_SCOPE here so a responder that shells
	// back into grpvn resolves the same identity as the watcher.
	Env []string
	// Cooldown is the minimum delay between responder runs. It is the
	// anti-loop brake: a responder that exits without reading leaves the
	// inbox unread, which would otherwise re-fire it immediately.
	Cooldown time.Duration
	// Interval is the data_version poll cadence while blocked (see Wait).
	Interval time.Duration
	// Once handles a single wake-up and returns instead of looping.
	Once bool
	// Render flags for print mode, mirroring `grpvn r`.
	Limit           int
	TS, Full, Human bool
	Color           string
}

// Watch is the optional daemon: a foreground supervisor that blocks until
// unread messages arrive, handles them, and goes back to blocking — forever,
// or once with opts.Once. It opens no listener and forks nothing between
// wake-ups; "daemon" here means a long-lived process you choose to run (in a
// spare terminal, a background shell task, or under systemd/launchd), not a
// background service grpvn manages.
//
// Print mode (no Exec) turns the process into a live inbox: each wake-up
// renders the new messages and advances the cursors, so the next Wait blocks
// again naturally. Exec mode dispatches instead of reading: the responder
// command is expected to consume the inbox itself (`grpvn r` directly or via
// an agent), which keeps delivery at-least-once — a responder that crashes
// before reading leaves the messages unread for the next attempt, rather
// than losing a batch the watcher had already consumed on its behalf.
//
// Wake-ups can't self-amplify: Check ignores the agent's own messages, so a
// responder's replies never re-trigger the watcher, and the cooldown caps
// the retry rate when a responder exits without reading. A responder failure
// is logged and the loop continues — a flaky responder must not kill the
// monitor — but store errors are returned: a watcher that can't see the
// database is dead and should say so loudly.
func Watch(ctx context.Context, out, errw io.Writer, db *sql.DB, load func() (*State, error), opts WatchOpts) error {
	for {
		code, err := Wait(ctx, io.Discard, db, load, 0, opts.Interval)
		if err != nil {
			return err
		}
		if code != 0 {
			return nil // context done: a stopped watcher is a clean exit
		}
		st, err := load()
		if err != nil {
			return err
		}
		line, err := UnreadLine(db, st)
		if err != nil {
			return err
		}
		if line == "" {
			// A concurrent read by the same identity drained the inbox
			// between the wake-up and here. Nothing to do.
			continue
		}
		fmt.Fprintf(errw, "[grpvn watch] %s unread: %s\n", time.Now().Format("15:04:05"), line)
		if opts.Exec == "" {
			if _, err := Read(out, db, st, opts.Limit, true, opts.TS, opts.Full, opts.Human, opts.Color); err != nil {
				return err
			}
		} else {
			runResponder(ctx, out, errw, opts)
			if left, err := UnreadLine(db, st); err == nil && left != "" {
				fmt.Fprintf(errw, "[grpvn watch] responder left unread (%s); retrying after cooldown\n", left)
			}
		}
		if opts.Once {
			return nil
		}
		if opts.Exec != "" && opts.Cooldown > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(opts.Cooldown):
			}
		}
	}
}

func runResponder(ctx context.Context, out, errw io.Writer, opts WatchOpts) {
	shell, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		shell, flag = "cmd", "/c"
	}
	cmd := exec.CommandContext(ctx, shell, flag, opts.Exec)
	cmd.Stdout = out
	cmd.Stderr = errw
	cmd.Env = append(os.Environ(), opts.Env...)
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(errw, "[grpvn watch] responder: %v\n", err)
	}
}
