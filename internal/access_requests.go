package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type pendingConnectionRequest struct {
	ID        string   `json:"id"`
	ProfileID string   `json:"profile_id"`
	Name      string   `json:"name"`
	Code      string   `json:"code"`
	Channels  []string `json:"channels"`
	Client    string   `json:"client,omitempty"`
	Redirect  string   `json:"redirect,omitempty"`
	Expires   int64    `json:"expires_at,omitempty"`
	Notified  bool     `json:"notified"`
}

// Older OAuth profiles stored display annotations alongside requested channels.
// Those annotations are context, never channel grants.
func approvalChannels(requested []string) ([]string, error) {
	channels := []string{}
	for _, ch := range requested {
		if strings.HasPrefix(ch, "oauth:") || strings.HasPrefix(ch, "client:") || strings.HasPrefix(ch, "redirect:") {
			continue
		}
		if ch != "*" && ch != "@*" {
			if err := validateChannel(ch); err != nil {
				return nil, err
			}
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

func pendingAccessRequests(db dbtx, viewer string) ([]pendingConnectionRequest, error) {
	// Active profiles reconnect through their own browser consent. Only new,
	// unapproved profiles require an administrator's attention.
	rows, err := db.Query(`SELECT o.id,p.id,p.name,a.approval_code,p.requested,c.name,o.redirect_uri,o.expires_at,
 EXISTS(SELECT 1 FROM human_request_notifications n WHERE n.principal_id=? AND n.request_id='oauth:'||p.id)
 FROM oauth_request_approvals a JOIN oauth_requests o ON o.id=a.request_id JOIN principals p ON p.id=o.principal_id JOIN oauth_clients c ON c.id=o.client_id
 WHERE a.approved=0 AND o.consumed=0 AND o.expires_at>? AND p.status='pending' ORDER BY o.id DESC LIMIT 100`, viewer, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	out := []pendingConnectionRequest{}
	seen := map[string]bool{}
	for rows.Next() {
		var q pendingConnectionRequest
		var requested, redirect string
		if err = rows.Scan(&q.ID, &q.ProfileID, &q.Name, &q.Code, &requested, &q.Client, &redirect, &q.Expires, &q.Notified); err != nil {
			rows.Close()
			return nil, err
		}
		if seen[q.ProfileID] {
			continue
		}
		seen[q.ProfileID] = true
		q.ID = "oauth:" + q.ProfileID
		var raw []string
		_ = json.Unmarshal([]byte(requested), &raw)
		q.Channels, _ = approvalChannels(raw)
		if q.Channels == nil {
			q.Channels = []string{}
		}
		if u, e := url.Parse(redirect); e == nil {
			q.Redirect = u.Host
		}
		out = append(out, q)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.Query(`SELECT p.id,p.name,p.approval_code,p.requested,EXISTS(SELECT 1 FROM human_request_notifications n WHERE n.principal_id=? AND n.request_id='profile:'||p.id)
 FROM principals p WHERE p.status='pending' AND NOT EXISTS(SELECT 1 FROM oauth_requests o WHERE o.principal_id=p.id) ORDER BY p.created_at DESC LIMIT 100`, viewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var q pendingConnectionRequest
		var requested string
		if err = rows.Scan(&q.ProfileID, &q.Name, &q.Code, &requested, &q.Notified); err != nil {
			return nil, err
		}
		q.ID = "profile:" + q.ProfileID
		var raw []string
		_ = json.Unmarshal([]byte(requested), &raw)
		q.Channels, _ = approvalChannels(raw)
		if q.Channels == nil {
			q.Channels = []string{}
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *webServer) approvals(w http.ResponseWriter, r *http.Request, p *Principal) {
	switch r.Method {
	case "GET":
		requests, err := pendingAccessRequests(s.db, p.ID)
		if err != nil {
			apiError(w, 500, "Could not list requests")
			return
		}
		writeJSON(w, 200, map[string]any{"requests": requests})
	case "POST":
		var a struct {
			Code     string
			Channels []string
		}
		if !webDecode(w, r, &a) {
			return
		}
		channels, err := approvalChannels(a.Channels)
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		if len(channels) == 0 {
			channels = []string{"#general", "@*"}
		}
		for _, ch := range channels {
			if strings.HasPrefix(ch, "#") && !s.access.isPublic(ch) && !canManageChannel(s.db, s.access, p, ch) && !prospectiveChannel(s.db, s.access, p, ch) {
				apiError(w, 403, "A channel member must invite this agent to the private channel")
				return
			}
		}
		if err = ApproveMember(s.db, a.Code, "", channels, true); err != nil {
			apiError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case "PATCH":
		var a struct{ IDs []string }
		if !webDecode(w, r, &a) {
			return
		}
		if len(a.IDs) > 100 {
			apiError(w, 400, "Select up to 100 requests")
			return
		}
		requests, err := pendingAccessRequests(s.db, p.ID)
		if err != nil {
			apiError(w, 500, "Could not update requests")
			return
		}
		pending := map[string]bool{}
		for _, q := range requests {
			pending[q.ID] = true
		}
		tx, err := beginWrite(s.db)
		if err != nil {
			apiError(w, 500, "Could not update requests")
			return
		}
		defer tx.Rollback()
		claimed := []string{}
		for _, id := range a.IDs {
			if !pending[id] {
				continue
			}
			var result interface{ RowsAffected() (int64, error) }
			result, err = tx.Exec("INSERT OR IGNORE INTO human_request_notifications(principal_id,request_id,notified_at) VALUES(?,?,?)", p.ID, id, time.Now().UnixMilli())
			if err != nil {
				break
			}
			if count, _ := result.RowsAffected(); count > 0 {
				claimed = append(claimed, id)
			}
		}
		if err != nil || tx.Commit() != nil {
			apiError(w, 500, "Could not update requests")
			return
		}
		writeJSON(w, 200, map[string]any{"claimed": claimed})
	default:
		apiError(w, 405, fmt.Sprintf("Method %s is not supported", r.Method))
	}
}
