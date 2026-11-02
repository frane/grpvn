package internal

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

func doctorActivations(w io.Writer, db *sql.DB) {
	sessions, err := Sessions(db, true)
	if err != nil {
		fmt.Fprintf(w, "warn  activation registry: %v\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Fprintln(w, "note  no activation owner registered; hooks surface unread at natural turns")
	}
	for _, s := range sessions {
		var accepted, consumed int
		_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE session_id=? AND accepted_at IS NOT NULL", s.ID).Scan(&accepted)
		_ = db.QueryRow("SELECT COUNT(*) FROM activation_events WHERE session_id=? AND consumed_at IS NOT NULL", s.ID).Scan(&consumed)
		fmt.Fprintf(w, "note  activation %s as %s runtime=%s session=%s mode=%s status=%s accepted=%d consumed=%d\n", s.ID, s.Agent, s.Runtime, s.RuntimeSession, s.Mode, s.Status, accepted, consumed)
		switch s.Runtime {
		case "codex":
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := exec.CommandContext(ctx, "codex", "queue", "--help").Run()
			cancel()
			fmt.Fprintf(w, "note  codex queue supported=%t; native session last observed %s; delivery acceptance and consumption are separate\n", err == nil, time.UnixMilli(s.HeartbeatAt).Format(time.RFC3339))
		case "claude-channel", "pi", "opencode":
			var live bool
			_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM bridge_leases WHERE session_id=? AND expires_at>?)", s.ID, time.Now().UnixMilli()).Scan(&live)
			fmt.Fprintf(w, "note  native event bridge live=%t\n", live)
			fmt.Fprintf(w, "note  offline native supervisor=%t\n", s.Supervise)
			if s.Runtime == "claude-channel" {
				fmt.Fprintln(w, "note  Claude Channel host opt-in cannot be inferred from transport acceptance; consumed receipts confirm processing")
			}
			if s.Runtime == "opencode" && s.Endpoint != "" {
				fmt.Fprintf(w, "note  OpenCode direct activation endpoint configured; server liveness unverified\n")
			}
		case "grok":
			_, err := exec.LookPath("grok")
			fmt.Fprintf(w, "note  Grok resume executable present=%t supervisor=%t; requires its locally authenticated session\n", err == nil, s.Supervise)
		case "openai", "workspace-agent":
			env := s.CredentialEnv
			if env == "" {
				env = "OPENAI_API_KEY"
				if s.Runtime == "workspace-agent" {
					env = "GRPVN_WORKSPACE_AGENT_TOKEN"
				}
			}
			fmt.Fprintf(w, "note  API activation credential %s present=%t; provider access unverified; daemon must inherit it\n", env, os.Getenv(env) != "")
			var status, link string
			if err := db.QueryRow("SELECT status,conversation_url FROM activation_runs WHERE session_id=? ORDER BY created_at DESC LIMIT 1", s.ID).Scan(&status, &link); err == nil {
				fmt.Fprintf(w, "note  latest provider delivery status=%s", status)
				if link != "" {
					fmt.Fprintf(w, " conversation=%s", link)
				}
				fmt.Fprintln(w)
			}
		}
	}
}
