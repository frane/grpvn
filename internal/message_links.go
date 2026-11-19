package internal

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func messageReference(db dbtx, input string) (string, error) {
	u, err := url.Parse(input)
	if err != nil {
		return "", fmt.Errorf("invalid message link")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("message links cannot include credentials or query parameters")
	}
	if u.Scheme == "grpvn" && u.Host == "local" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		var server string
		_ = db.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&server)
		if len(parts) != 3 || parts[0] != server || parts[1] != "m" || !validMessageID(parts[2]) {
			return "", fmt.Errorf("this local message link belongs to another database")
		}
		return parts[2], nil
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || !strings.HasPrefix(u.Path, "/m/") || !validMessageID(strings.TrimPrefix(u.Path, "/m/")) {
		return "", fmt.Errorf("invalid message link")
	}
	base := u.Scheme + "://" + u.Host
	id := strings.TrimPrefix(u.Path, "/m/")
	var own string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='public_url'").Scan(&own) == nil && strings.TrimRight(own, "/") == base {
		return id, nil
	}
	var local string
	err = db.QueryRow("SELECT rm.local_id FROM remote_messages rm JOIN remotes r ON r.name=rm.remote_name WHERE r.url=? AND rm.remote_id=? ORDER BY r.name LIMIT 1", base, id).Scan(&local)
	if err == nil {
		return local, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	return "", fmt.Errorf("message from %s is not cached here; connect to that server and synchronize its channel, or use that server's MCP read tool", base)
}

func messageLink(db dbtx, id, base string) string {
	var remoteURL, native string
	if db.QueryRow("SELECT r.url,rm.remote_id FROM remote_messages rm JOIN remotes r ON r.name=rm.remote_name WHERE rm.local_id=?", id).Scan(&remoteURL, &native) == nil {
		return strings.TrimRight(remoteURL, "/") + "/m/" + native
	}
	if base == "" {
		_ = db.QueryRow("SELECT value FROM server_meta WHERE key='public_url'").Scan(&base)
	}
	if base != "" {
		return strings.TrimRight(base, "/") + "/m/" + id
	}
	server := messageDatabaseID(db)
	return "grpvn://local/" + server + "/m/" + id
}
func messageDatabaseID(db dbtx) string {
	var id string
	if db.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&id) != nil {
		id = NewMessage("", "", nil).ID
		_, _ = db.Exec("INSERT INTO server_meta(key,value) VALUES('id',?) ON CONFLICT(key) DO NOTHING", id)
		_ = db.QueryRow("SELECT value FROM server_meta WHERE key='id'").Scan(&id)
	}
	return id
}

func (s *webServer) messageContext(w http.ResponseWriter, r *http.Request, p *Principal) {
	_, message, err := ResolveTarget(s.db, r.URL.Query().Get("reference"), "")
	if err != nil || message == nil || !messageVisible(s, p, message, false) {
		apiError(w, 404, "Message unavailable. Sign in to the correct workspace and check channel access.")
		return
	}
	target := message.Target
	if strings.HasPrefix(target, "@") {
		if _, _, remote := splitRemoteDMTarget(target); !remote && principalOwnsAddress(p, strings.TrimPrefix(target, "@")) {
			target = "@" + message.Sender
		}
	}
	predicate := "m.target=?"
	args := []any{target}
	if strings.HasPrefix(target, "@") {
		if _, _, remote := splitRemoteDMTarget(target); !remote {
			predicate, args = directConversationPredicate(s.db, principalAddress(p), strings.TrimPrefix(target, "@"))
		}
	}
	root, err := findMessageByID(s.db, message.ChainRoot)
	if err != nil {
		root = message
	}
	before, err := s.messagePage(r.Context(), p, predicate+" AND m.seq<=?", append(append([]any{}, args...), root.Seq), "DESC", 51)
	if err != nil {
		apiError(w, 500, "Could not open message")
		return
	}
	after, err := s.messagePage(r.Context(), p, predicate+" AND m.seq>?", append(append([]any{}, args...), root.Seq), "ASC", 50)
	if err != nil {
		apiError(w, 500, "Could not open message")
		return
	}
	messages := []map[string]any{}
	for i := len(before) - 1; i >= 0; i-- {
		messages = append(messages, before[i])
	}
	messages = append(messages, after...)
	s.addThreadSummaries(p, target, messages)
	replies := []map[string]any{}
	if message.ParentID != nil {
		rows, e := s.messagePage(r.Context(), p, predicate+" AND m.chain_root=? AND m.seq<=?", append(append([]any{}, args...), message.ChainRoot, message.Seq), "DESC", 51)
		if e == nil {
			for i := len(rows) - 1; i >= 0; i-- {
				replies = append(replies, rows[i])
			}
		}
	}
	writeJSON(w, 200, map[string]any{"target": target, "message_id": message.ID, "root_id": message.ChainRoot, "reply": message.ParentID != nil, "messages": messages, "replies": replies, "more": len(before) == 51})
}
