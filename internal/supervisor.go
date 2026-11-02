package internal

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Four execution workers share durable claims. Long native turns do not block
// provider status polling or the watcher. Queued work can restart safely;
// dispatching work with a lost acknowledgement is never blindly replayed.
func RunActivationWatcher(ctx context.Context, db *sql.DB, output io.Writer) {
	jobs := make(chan Activation, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				if ctx.Err() != nil {
					continue
				}
				_, _ = db.Exec(`UPDATE activation_runs SET status='suppressed',detail='addressed messages already consumed' WHERE id=? AND status='queued' AND NOT EXISTS(SELECT 1 FROM activation_run_events re JOIN activation_events e ON e.id=re.event_id WHERE re.run_id=activation_runs.id AND e.action='activate' AND e.consumed_at IS NULL)`, a.ID)
				_, _ = db.Exec(`UPDATE activation_runs SET status='suppressed',detail='delivery owner or activation policy changed' WHERE id=? AND status='queued' AND NOT EXISTS(SELECT 1 FROM activation_owners o JOIN agent_sessions s ON s.id=o.session_id WHERE o.session_id=activation_runs.session_id AND s.mode='activate')`, a.ID)
				_, _ = db.Exec(`UPDATE activation_events SET status='suppressed',detail='delivery owner or activation policy changed' WHERE id IN(SELECT re.event_id FROM activation_run_events re JOIN activation_runs r ON r.id=re.run_id WHERE r.id=? AND r.status='suppressed') AND consumed_at IS NULL`, a.ID)
				claim, err := db.Exec("UPDATE activation_runs SET status='dispatching',updated_at=? WHERE id=? AND status='queued'", time.Now().UnixMilli(), a.ID)
				if err != nil {
					if output != nil {
						fmt.Fprintln(output, "activation claim:", err)
					}
					continue
				}
				n, _ := claim.RowsAffected()
				if n == 0 {
					continue
				}
				if err := DispatchActivation(ctx, db, a); err != nil {
					_ = recordDispatchFailure(db, a.ID, err)
					if output != nil {
						fmt.Fprintf(output, "activation %s: %v\n", a.ID, err)
					}
				}
			}
		}()
	}
	defer func() { close(jobs); wg.Wait() }()
	enqueue := func(a Activation) error {
		if err := FinishActivation(db, a.ID, "queued", "waiting for an execution worker", "", ""); err != nil {
			return err
		}
		select {
		case jobs <- a:
		case <-ctx.Done():
		}
		return nil
	}
	rows, err := db.Query("SELECT id FROM activation_runs WHERE status='queued' ORDER BY created_at")
	if err != nil {
		if output != nil {
			fmt.Fprintln(output, "activation recovery:", err)
		}
		return
	}
	var pending []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return
		}
		pending = append(pending, id)
	}
	rows.Close()
	for _, id := range pending {
		a, err := loadActivation(db, id)
		if err != nil {
			if output != nil {
				fmt.Fprintln(output, "activation recovery:", err)
			}
			continue
		}
		select {
		case jobs <- *a:
		case <-ctx.Done():
			return
		}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := processActivations(ctx, db, output, enqueue); err != nil && output != nil && ctx.Err() == nil {
			fmt.Fprintln(output, "activation watcher:", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func loadActivation(db *sql.DB, id string) (*Activation, error) {
	a := &Activation{ID: id}
	var session string
	if err := db.QueryRow("SELECT session_id,thread_root FROM activation_runs WHERE id=?", id).Scan(&session, &a.Thread); err != nil {
		return nil, err
	}
	sessions, err := Sessions(db, false)
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		if s.ID == session {
			a.Session = s
		}
	}
	if a.Session.ID == "" {
		return nil, fmt.Errorf("activation session unavailable")
	}
	rows, err := db.Query("SELECT e.id,e.message_id,e.target,e.action FROM activation_events e JOIN activation_run_events re ON re.event_id=e.id WHERE re.run_id=? ORDER BY e.message_seq", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e ActivationEvent
		if err := rows.Scan(&e.ID, &e.MessageID, &e.Target, &e.Action); err != nil {
			return nil, err
		}
		a.Events = append(a.Events, e)
	}
	a.Prompt = activationPrompt(*a)
	return a, rows.Err()
}

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data) < 4096 {
		remaining := 4096 - len(b.data)
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func dispatchNativeResume(ctx context.Context, db *sql.DB, a Activation) error {
	s := a.Session
	if !s.Supervise {
		return fmt.Errorf("%s native resume requires --supervise", s.Runtime)
	}
	var binary string
	var args []string
	switch s.Runtime {
	case "grok":
		binary = "grok"
		args = []string{"--no-auto-update", "--resume", s.RuntimeSession, "-p", a.Prompt, "--output-format", "json", "--max-turns", "2"}
	case "claude-channel":
		if strings.HasPrefix(s.RuntimeSession, "mcp-") {
			return fmt.Errorf("Claude offline resume requires its real native session ID; register serve --session or ss reg --thread")
		}
		binary = "claude"
		args = []string{"--resume", s.RuntimeSession, "-p", a.Prompt, "--max-turns", "2", "--output-format", "json"}
	case "pi":
		binary = "pi"
		args = []string{"--session", s.RuntimeSession, "--print", a.Prompt}
	case "opencode":
		binary = "opencode"
		args = []string{"run", "--session", s.RuntimeSession, "--format", "json", a.Prompt}
	default:
		return fmt.Errorf("no native resume adapter for %s", s.Runtime)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := TouchSession(db, s.ID, "busy"); err != nil {
		return err
	}
	defer TouchSession(db, s.ID, "offline")
	heartbeatCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_, _ = db.Exec("UPDATE activation_runs SET updated_at=? WHERE id=? AND status='dispatching'", time.Now().UnixMilli(), a.ID)
				_, _ = db.Exec("UPDATE agent_sessions SET heartbeat_at=? WHERE id=?", time.Now().UnixMilli(), s.ID)
			}
		}
	}()
	defer func() { stop(); <-done }()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = s.Project
	command.Env = runtimeEnvironment(s)
	var stderr boundedOutput
	command.Stdout = io.Discard
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s resume: %w: %.1000s", binary, err, stderr.data)
	}
	return FinishActivation(db, a.ID, "accepted", "native resume finished; read and reply receipts remain separate", "", "")
}
