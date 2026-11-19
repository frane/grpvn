package internal

import (
	"net/http"
	"sort"
)

// Fetching the first unread page never marks it read.
func (s *webServer) unreadEntry(w http.ResponseWriter, r *http.Request, p *Principal, target, predicate string, args []any) bool {
	position, held := conversationReadPosition(s.db, p.ID, target)
	query := `SELECT m.id,m.seq,m.chain_root FROM messages m WHERE ` + predicate + ` AND m.seq>? AND (?=1 OR (m.sender<>? AND NOT EXISTS(SELECT 1 FROM message_principals mp WHERE mp.message_id=m.id AND mp.principal_id=?) AND NOT EXISTS(SELECT 1 FROM remote_attributions a JOIN remote_messages rm ON rm.local_id=a.local_id JOIN remotes rr ON rr.name=rm.remote_name JOIN remote_human_sessions h ON h.server_id=rr.server_id WHERE a.local_id=m.id AND a.principal_id=h.principal_id AND h.local_principal_id=?))) AND NOT EXISTS(SELECT 1 FROM human_threads t WHERE t.principal_id=? AND t.root_id=m.chain_root AND m.parent_id IS NOT NULL AND t.position>=m.seq) ORDER BY m.seq LIMIT 1`
	values := append(append([]any{}, args...), position, held, loadInboxSelf(s.db, p).name(target), p.ID, p.ID, p.ID)
	var id, root string
	var seq int64
	if err := s.db.QueryRowContext(r.Context(), query, values...).Scan(&id, &seq, &root); err != nil {
		return false
	}
	before, err := s.messagePage(r.Context(), p, predicate+" AND m.seq<?", append(append([]any{}, args...), seq), "DESC", 16)
	if err != nil {
		apiError(w, 500, "Could not open unread messages")
		return true
	}
	after, err := s.messagePage(r.Context(), p, predicate+" AND m.seq>=?", append(append([]any{}, args...), seq), "ASC", 101)
	if err != nil {
		apiError(w, 500, "Could not open unread messages")
		return true
	}
	more, newer := len(before) > 15, len(after) > 100
	if more {
		before = before[:15]
	}
	if newer {
		after = after[:100]
	}
	messages := append(before, after...)
	hasRoot := false
	for _, m := range messages {
		hasRoot = hasRoot || m["id"] == root
	}
	if !hasRoot && root != id {
		roots, e := s.messagePage(r.Context(), p, predicate+" AND m.id=?", append(append([]any{}, args...), root), "ASC", 1)
		if e == nil {
			messages = append(messages, roots...)
		}
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i]["seq"].(int64) < messages[j]["seq"].(int64) })
	s.addThreadSummaries(p, target, messages)
	state := conversationReadSnapshot(s.db, p.ID, target, "")
	writeJSON(w, 200, map[string]any{"messages": messages, "more": more, "newer": newer, "read_position": position, "read_held": held, "read_version": state.Version, "first_unread_id": id, "first_unread_root": root})
	return true
}
