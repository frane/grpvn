package internal

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"
)

func (a Activation) activates() bool {
	for _, e := range a.Events {
		if e.Action == "activate" {
			return true
		}
	}
	return false
}

func runtimeEnvironment(s AgentSession) []string {
	values := map[string]string{"GRPVN_AS": s.Agent}
	if s.StatePath != "" {
		values["GRPVN_STATE"] = s.StatePath
		values["GRPVN_SCOPE"] = "host"
	}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := values[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

// TouchSession records runtime presence, not message consumption. Only native
// lifecycle bridges report busy/idle; queue acceptance never invents liveness.
func TouchSession(db *sql.DB, id, status string) error {
	switch status {
	case "idle", "busy", "offline", "registered":
	default:
		return fmt.Errorf("status must be idle, busy, offline or registered")
	}
	result, err := db.Exec("UPDATE agent_sessions SET status=?,heartbeat_at=? WHERE id=?", status, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("unknown registry ID")
	}
	return nil
}

// RunSessionBridge is shared by stdio channels and native plugins. One leased
// bridge owns a session's event stream. Delivery acknowledgement remains a
// separate operation so writing to a pipe cannot imply runtime acceptance.
func RunSessionBridge(ctx context.Context, db *sql.DB, id string, notifications bool, emit func(Activation) error) error {
	owner := NewMessage("", "", nil).ID
	now := time.Now()
	result, err := db.Exec(`INSERT INTO bridge_leases(session_id,owner,expires_at) VALUES(?,?,?)
	ON CONFLICT(session_id) DO UPDATE SET owner=excluded.owner,expires_at=excluded.expires_at WHERE expires_at<=?`, id, owner, now.Add(15*time.Second).UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("a native bridge is already connected for this session")
	}
	defer db.Exec("DELETE FROM bridge_leases WHERE session_id=? AND owner=?", id, owner)
	defer TouchSession(db, id, "offline")
	if err := TouchSession(db, id, "idle"); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.Now()
	var lastSync time.Time
	for ctx.Err() == nil {
		if time.Since(lastSync) >= time.Second {
			_, _ = SyncAvailable(ctx, db)
			lastSync = time.Now()
		}
		if time.Since(heartbeat) >= 5*time.Second {
			result, err := db.Exec("UPDATE bridge_leases SET expires_at=? WHERE session_id=? AND owner=?", time.Now().Add(15*time.Second).UnixMilli(), id, owner)
			if err != nil {
				return err
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				return fmt.Errorf("native bridge lease lost")
			}
			if _, err := db.Exec("UPDATE agent_sessions SET heartbeat_at=? WHERE id=?", time.Now().UnixMilli(), id); err != nil {
				return err
			}
			if _, err := db.Exec("INSERT OR IGNORE INTO activation_owners(agent_name,session_id) SELECT agent_name,id FROM agent_sessions WHERE id=?", id); err != nil {
				return err
			}
			heartbeat = time.Now()
		}
		if err := ScanActivationEvents(db); err != nil {
			return err
		}
		sessions, err := Sessions(db, true)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if s.ID != id {
				continue
			}
			a, err := ClaimDelivery(db, s, notifications, 2*time.Second)
			if err != nil {
				return err
			}
			if a == nil {
				continue
			}
			if err := emit(*a); err != nil {
				_ = FinishActivation(db, a.ID, "uncertain", "native bridge failed before acknowledgement", "", "")
				return err
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

// A reply to a claimed delivery is automation provenance regardless of whether
// it came from a native CLI, a ChatGPT MCP tool or an API-managed worker.
func markAutomaticReply(db dbtx, m *Message) error {
	if m.ParentID == nil {
		return nil
	}
	var run string
	err := db.QueryRow(`SELECT r.id FROM activation_runs r JOIN agent_sessions s ON s.id=r.session_id
	JOIN activation_run_events re ON re.run_id=r.id JOIN activation_events e ON e.id=re.event_id
	WHERE s.agent_name=? AND (e.message_id=? OR e.thread_root=?) AND r.created_at>?
	AND r.status IN ('dispatching','accepted','running','completed','uncertain') ORDER BY r.created_at DESC LIMIT 1`, m.Sender, *m.ParentID, m.ChainRoot, time.Now().Add(-time.Hour).UnixMilli()).Scan(&run)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT OR IGNORE INTO automated_messages(message_id,activation_id,hops) VALUES(?,?,1)", m.ID, run)
	return err
}
