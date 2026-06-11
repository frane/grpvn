package internal

import (
	"context"
	"database/sql"
	"io"
	"time"
)

// Wait blocks until the agent has unread messages, writes the same counts
// line Check produces, and returns 0. It returns 2 when the timeout (or the
// caller's context) expires first, mirroring Check's "nothing here" exit
// code. A timeout of zero or less means wait until the context is done.
//
// The poll primitive is PRAGMA data_version, which changes only when a
// different connection commits to the database — so the unread query runs
// once up front and then only when the store has actually moved. That keeps
// a blocked Wait at one PRAGMA per interval, cheap enough to leave running
// for hours. Writes always arrive on other connections in practice: other
// agents are other processes, and within `grpvn serve` every tool call
// opens its own handle.
//
// State is re-loaded through the load callback before every unread check so
// a concurrent read by the same agent (a CLI `r` next to a blocked MCP
// wait, say) advances the cursors out from under us instead of producing a
// stale wake-up.
func Wait(ctx context.Context, w io.Writer, db *sql.DB, load func() (*State, error), timeout, interval time.Duration) (int, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	prev := int64(-1) // sentinel: data_version is never negative, so the first check always runs
	for {
		var dv int64
		if err := db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&dv); err != nil {
			if ctx.Err() != nil {
				return 2, nil
			}
			return 0, err
		}
		if dv != prev {
			prev = dv
			st, err := load()
			if err != nil {
				return 0, err
			}
			code, err := Check(w, db, st)
			if err != nil {
				return 0, err
			}
			if code == 0 {
				return 0, nil
			}
		}
		select {
		case <-ctx.Done():
			return 2, nil
		case <-ticker.C:
		}
	}
}
