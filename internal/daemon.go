package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const daemonHeartbeat = time.Second
const daemonLeaseTTL = 15 * time.Second

type DaemonState struct {
	Owner         string
	PID           int
	StartedAt     int64
	HeartbeatAt   int64
	IntervalMS    int64
	StopRequested bool
}

func (s DaemonState) Running() bool {
	return s.Owner != "" && s.HeartbeatAt > time.Now().Add(-daemonLeaseTTL).UnixMilli()
}

func ReadDaemonState(ctx context.Context, db *sql.DB) (DaemonState, error) {
	var state DaemonState
	err := db.QueryRowContext(ctx, `SELECT owner, pid, started_at, heartbeat_at, interval_ms, stop_requested
		FROM daemon_state WHERE singleton = 1`).Scan(&state.Owner, &state.PID, &state.StartedAt, &state.HeartbeatAt, &state.IntervalMS, &state.StopRequested)
	if errors.Is(err, sql.ErrNoRows) {
		return DaemonState{}, nil
	}
	return state, err
}

func DaemonRunning(ctx context.Context, db *sql.DB) (bool, error) {
	state, err := ReadDaemonState(ctx, db)
	return state.Running(), err
}

func DaemonLogPath(db *sql.DB) (string, error) {
	var seq int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("background daemon requires a file-backed database")
	}
	return path + ".daemon.log", nil
}

type DaemonAlreadyRunningError struct{ PID int }

func (e *DaemonAlreadyRunningError) Error() string {
	return fmt.Sprintf("daemon already running (pid %d)", e.PID)
}

func acquireDaemon(db *sql.DB, interval time.Duration) (DaemonState, error) {
	tx, err := beginWrite(db)
	if err != nil {
		return DaemonState{}, err
	}
	defer tx.Rollback()
	var pid int
	var heartbeat int64
	err = tx.QueryRow("SELECT pid, heartbeat_at FROM daemon_state WHERE singleton = 1").Scan(&pid, &heartbeat)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DaemonState{}, err
	}
	if err == nil && heartbeat > time.Now().Add(-daemonLeaseTTL).UnixMilli() {
		return DaemonState{}, &DaemonAlreadyRunningError{PID: pid}
	}
	now := time.Now().UnixMilli()
	state := DaemonState{Owner: NewMessage("", "", nil).ID, PID: os.Getpid(), StartedAt: now, HeartbeatAt: now, IntervalMS: interval.Milliseconds()}
	_, err = tx.Exec(`INSERT INTO daemon_state (singleton, owner, pid, started_at, heartbeat_at, interval_ms, stop_requested)
		VALUES (1, ?, ?, ?, ?, ?, 0) ON CONFLICT(singleton) DO UPDATE SET owner = excluded.owner,
		pid = excluded.pid, started_at = excluded.started_at, heartbeat_at = excluded.heartbeat_at,
		interval_ms = excluded.interval_ms, stop_requested = 0`, state.Owner, state.PID, now, now, state.IntervalMS)
	if err != nil {
		return DaemonState{}, err
	}
	return state, tx.Commit()
}

// RunDaemon owns one renewable SQLite lease and continuously synchronizes
// all configured sources. The heartbeat and stop request are handled apart
// from HTTP requests so a remote outage cannot make the worker unresponsive.
func RunDaemon(ctx context.Context, db *sql.DB, interval time.Duration, output io.Writer) error {
	if interval < minRemoteInterval {
		return fmt.Errorf("--interval must be at least %s", minRemoteInterval)
	}
	state, err := acquireDaemon(db, interval)
	if err != nil {
		return err
	}
	defer db.Exec("DELETE FROM daemon_state WHERE singleton = 1 AND owner = ?", state.Owner)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if output == nil {
		output = io.Discard
	}
	fmt.Fprintf(output, "daemon running pid=%d interval=%s\n", state.PID, interval)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		runDaemonSync(ctx, db, interval, output)
	}()
	activationDone := make(chan struct{})
	go func() {
		defer close(activationDone)
		RunActivationWatcher(ctx, db, output)
	}()
	// Always cancel and join before releasing the lease. A successor must
	// not overlap network work still belonging to a stopped worker.
	defer func() { cancel(); <-workerDone; <-activationDone }()
	ticker := time.NewTicker(daemonHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			res, err := db.ExecContext(ctx, `UPDATE daemon_state SET heartbeat_at = ?
				WHERE singleton = 1 AND owner = ? AND stop_requested = 0`, time.Now().UnixMilli(), state.Owner)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				current, err := ReadDaemonState(ctx, db)
				if err != nil {
					return err
				}
				if current.Owner == state.Owner && current.StopRequested {
					return nil
				}
				return fmt.Errorf("daemon lease lost")
			}
		}
	}
}

func runDaemonSync(ctx context.Context, db *sql.DB, interval time.Duration, output io.Writer) {
	previousError := ""
	for ctx.Err() == nil {
		stats, err := SyncDue(ctx, db, "", interval)
		if ctx.Err() != nil {
			return
		}
		line := ""
		if err != nil {
			line = SyncErrorLine(err)
		}
		if stats.Pulled+stats.Pushed > 0 {
			fmt.Fprintf(output, "%s pushed %d, pulled %d\n", time.Now().Format(time.RFC3339), stats.Pushed, stats.Pulled)
		}
		if line != previousError {
			if line != "" {
				fmt.Fprintf(output, "%s sync: %s\n", time.Now().Format(time.RFC3339), line)
			} else if previousError != "" {
				fmt.Fprintf(output, "%s sync recovered\n", time.Now().Format(time.RFC3339))
			}
		}
		previousError = line
		timer := time.NewTimer(minRemoteInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// StopDaemon requests a graceful shutdown of the observed owner. It never
// signals a PID and never stops a different worker that subsequently starts.
func StopDaemon(ctx context.Context, db *sql.DB) (bool, error) {
	state, err := ReadDaemonState(ctx, db)
	if err != nil {
		return false, err
	}
	if !state.Running() {
		return false, nil
	}
	if _, err := db.ExecContext(ctx, "UPDATE daemon_state SET stop_requested = 1 WHERE singleton = 1 AND owner = ?", state.Owner); err != nil {
		return false, err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := ReadDaemonState(ctx, db)
		if err != nil {
			return true, err
		}
		if current.Owner != state.Owner || !current.Running() {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-ticker.C:
		}
	}
}
