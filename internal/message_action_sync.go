package internal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type actionSyncPage struct {
	ServerID  string           `json:"server_id"`
	Actions   []messageAction  `json:"actions"`
	Positions map[string]int64 `json:"positions"`
	More      bool             `json:"more"`
}

func actionSyncHandler(db *sql.DB, access *channelAccess, serverID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := requestPrincipal(r)
		if p == nil || p.Status != "active" || (p.scopes != nil && !p.scopes["grpvn.read"]) {
			apiError(w, 403, "An active member is required")
			return
		}
		var request syncBatchRequest
		if !webDecode(w, r, &request) {
			return
		}
		if len(request.Channels) == 0 || len(request.Channels) > maxSyncChannels {
			apiError(w, 400, "Choose 1–256 conversations")
			return
		}
		page := actionSyncPage{ServerID: serverID, Actions: []messageAction{}, Positions: map[string]int64{}}
		var predicates []string
		var args []any
		for _, position := range request.Channels {
			if _, duplicate := page.Positions[position.Channel]; duplicate || position.After < 0 {
				apiError(w, 400, "Invalid action cursor")
				return
			}
			if position.Channel == "@me" {
				if !principalPermission(db, p, "@*", false) || (p.scopes != nil && !p.scopes["grpvn.read"]) {
					continue
				}
				aliases := inboxAliases(db, principalAddress(p))
				var clause []string
				for _, alias := range aliases {
					clause = append(clause, "m.target=? OR m.sender=?")
					args = append(args, alias, strings.TrimPrefix(alias, "@"))
				}
				predicates = append(predicates, "(("+strings.Join(clause, " OR ")+") AND substr(m.target,1,1)='@' AND a.seq>?)")
				args = append(args, position.After)
			} else {
				if validateChannel(position.Channel) != nil {
					apiError(w, 400, "Invalid conversation")
					return
				}
				if !access.canAccess(r, position.Channel) {
					continue
				}
				predicates = append(predicates, "(m.target=? AND a.seq>?)")
				args = append(args, position.Channel, position.After)
			}
			page.Positions[position.Channel] = position.After
		}
		if len(predicates) > 0 {
			rows, err := db.QueryContext(r.Context(), `SELECT a.seq,a.id,a.message_id,a.actor_id,a.actor_name,a.kind,a.body,a.emoji,a.added,a.created_at,m.target FROM message_actions a JOIN messages m ON m.id=a.message_id WHERE a.source='' AND (`+strings.Join(predicates, " OR ")+`) ORDER BY a.seq LIMIT 101`, args...)
			if err != nil {
				apiError(w, 500, "Could not read message changes")
				return
			}
			for rows.Next() {
				var a messageAction
				var target string
				if err = rows.Scan(&a.Seq, &a.ID, &a.MessageID, &a.ActorID, &a.ActorName, &a.Kind, &a.Body, &a.Emoji, &a.Added, &a.CreatedAt, &target); err != nil {
					rows.Close()
					apiError(w, 500, "Could not read message change")
					return
				}
				if len(page.Actions) == 100 {
					page.More = true
					break
				}
				page.Actions = append(page.Actions, a)
				if strings.HasPrefix(target, "@") {
					target = "@me"
				}
				page.Positions[target] = a.Seq
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				apiError(w, 500, "Could not read message changes")
				return
			}
		}
		writeJSON(w, 200, page)
	}
}

func syncMessageActions(ctx context.Context, db *sql.DB, remote Remote, channels []RemoteChannel) error {
	targets := []string{}
	for _, channel := range channels {
		targets = append(targets, channel.Channel)
	}
	var human bool
	_ = db.QueryRow("SELECT EXISTS(SELECT 1 FROM remote_human_sessions WHERE server_id=?)", remote.ServerID).Scan(&human)
	if human {
		targets = append(targets, "@me")
	}
	for start := 0; start < len(targets); start += maxSyncChannels {
		end := min(start+maxSyncChannels, len(targets))
		request := syncBatchRequest{}
		for _, target := range targets[start:end] {
			var position int64
			_ = db.QueryRow("SELECT position FROM remote_action_positions WHERE remote_name=? AND target=?", remote.Name, target).Scan(&position)
			request.Channels = append(request.Channels, syncPosition{target, position})
		}
		for pageNumber := 0; pageNumber < 10; pageNumber++ {
			var page actionSyncPage
			err := remoteRequest(ctx, remote, "POST", "/v1/message-actions/sync", request, &page)
			var failure *remoteHTTPError
			if errors.As(err, &failure) && failure.Status == 404 {
				return nil
			}
			if err != nil {
				return err
			}
			if page.ServerID != remote.ServerID {
				return fmt.Errorf("coordinator identity changed")
			}
			requested := map[string]int64{}
			for _, position := range request.Channels {
				requested[position.Channel] = position.After
			}
			for target, position := range page.Positions {
				previous, ok := requested[target]
				if !ok || position < previous {
					return fmt.Errorf("invalid message change cursor")
				}
			}
			if err = importMessageActions(db, remote.Name, page.Actions, page.Positions); err != nil {
				return err
			}
			if !page.More {
				break
			}
			progress := false
			for i, position := range request.Channels {
				next := page.Positions[position.Channel]
				if next > position.After {
					progress = true
					request.Channels[i].After = next
				}
			}
			if !progress {
				return fmt.Errorf("message change cursor did not advance")
			}
		}
	}
	return nil
}
