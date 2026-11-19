package internal

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type messageAction struct {
	Seq       int64  `json:"seq"`
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	ActorID   string `json:"actor_id"`
	ActorName string `json:"actor_name"`
	Kind      string `json:"kind"`
	Body      string `json:"body,omitempty"`
	Emoji     string `json:"emoji,omitempty"`
	Added     bool   `json:"added"`
	CreatedAt int64  `json:"created_at"`
}
type actionRequest struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	Emoji     string `json:"emoji"`
	Added     bool   `json:"added"`
	// An empty version means the original message; nil allows deliberate
	// unconditional edits from a tool. Human clients always supply a version.
	Version *string `json:"version,omitempty"`
}
type reactionPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type messageReaction struct {
	Emoji  string           `json:"emoji"`
	Count  int              `json:"count"`
	Mine   bool             `json:"mine"`
	People []reactionPerson `json:"people"`
}
type messagePresentation struct {
	Body        string            `json:"body"`
	EditedAt    int64             `json:"edited_at,omitempty"`
	EditVersion string            `json:"edit_version"`
	Reactions   []messageReaction `json:"reactions"`
}

var uuidAction = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func validActionID(id string) bool { return validMessageID(id) || uuidAction.MatchString(id) }
func validReaction(emoji string) bool {
	if emoji == "" || len(emoji) > 64 || !utf8.ValidString(emoji) {
		return false
	}
	symbol := false
	for _, r := range emoji {
		switch {
		case r == 0x20e3:
			symbol = true
		case r == 0x200d || r == 0xfe0f || r == 0xfe0e:
		case unicode.IsSymbol(r):
			symbol = true
		case r >= '0' && r <= '9' || r == '#' || r == '*':
		default:
			return false
		}
	}
	return symbol
}

func messageVisible(s *webServer, p *Principal, m *Message, write bool) bool {
	if s.local && p.Kind == "agent" && localSenderAllowed(s.db, principalAddress(p), m.Target) {
		return true
	}
	if strings.HasPrefix(m.Target, "@") {
		if _, _, remote := splitRemoteDMTarget(m.Target); !remote {
			if !principalOwnsAddress(p, m.Sender) && !principalOwnsAddress(p, strings.TrimPrefix(m.Target, "@")) {
				return false
			}
			return principalPermission(s.db, p, "@*", write) || s.local && principalPermission(s.db, p, "*", write)
		}
	}
	return s.can(p, m.Target, write)
}

func messageView(db dbtx, m *Message, self string) (messagePresentation, error) {
	view := messagePresentation{Body: string(m.Body), Reactions: []messageReaction{}}
	err := db.QueryRow(`SELECT body,created_at,id FROM message_actions WHERE message_id=? AND kind='edit' ORDER BY COALESCE(origin_seq,seq) DESC LIMIT 1`, m.ID).Scan(&view.Body, &view.EditedAt, &view.EditVersion)
	if err != nil && err != sql.ErrNoRows {
		return view, err
	}
	rows, err := db.Query(`SELECT a.emoji,a.actor_id,COALESCE(p.name,a.actor_name) FROM message_actions a LEFT JOIN principals p ON p.id=a.actor_id
 WHERE a.message_id=? AND a.kind='reaction' AND a.added=1 AND NOT EXISTS(SELECT 1 FROM message_actions newer
 WHERE newer.message_id=a.message_id AND newer.kind='reaction' AND newer.actor_id=a.actor_id AND newer.emoji=a.emoji AND COALESCE(newer.origin_seq,newer.seq)>COALESCE(a.origin_seq,a.seq)) ORDER BY a.emoji,a.actor_id`, m.ID)
	if err != nil {
		return view, err
	}
	defer rows.Close()
	for rows.Next() {
		var emoji string
		var person reactionPerson
		if err = rows.Scan(&emoji, &person.ID, &person.Name); err != nil {
			return view, err
		}
		at := len(view.Reactions) - 1
		if at < 0 || view.Reactions[at].Emoji != emoji {
			view.Reactions = append(view.Reactions, messageReaction{Emoji: emoji, People: []reactionPerson{}})
			at++
		}
		reaction := &view.Reactions[at]
		reaction.Count++
		reaction.People = append(reaction.People, person)
		reaction.Mine = reaction.Mine || person.ID == self
	}
	return view, rows.Err()
}

// Store an action only after verifying the authenticated author and current
// channel rights. Administrators and human owners cannot edit agents' words.
func (s *webServer) applyMessageAction(ctx context.Context, p *Principal, args actionRequest) (messageAction, error) {
	if !validMessageID(args.MessageID) || !validActionID(args.ID) {
		return messageAction{}, fmt.Errorf("invalid message or action ID")
	}
	if args.Kind != "edit" && args.Kind != "reaction" {
		return messageAction{}, fmt.Errorf("choose edit or reaction")
	}
	if args.Kind == "edit" && (strings.TrimSpace(args.Body) == "" || len(args.Body) > MaxBodyBytes || !utf8.ValidString(args.Body)) {
		return messageAction{}, fmt.Errorf("message must contain UTF-8 text, at most %d bytes", MaxBodyBytes)
	}
	if args.Kind == "reaction" && !validReaction(args.Emoji) {
		return messageAction{}, fmt.Errorf("choose an emoji reaction")
	}
	m, err := findMessageByID(s.db, args.MessageID)
	if err != nil {
		return messageAction{}, fmt.Errorf("message not found")
	}
	allowed := messageVisible(s, p, m, true)
	if !allowed && !s.local && strings.HasPrefix(m.Target, "#") {
		allowed = ensureSendChannel(s.db, s.access, p, m.Target)
	}
	if !allowed {
		return messageAction{}, fmt.Errorf("message write permission denied")
	}
	if remote, _, mapped, err := s.remoteChannel(ctx, p, m.Target); mapped {
		if err != nil {
			return messageAction{}, err
		}
		var native string
		if err = s.db.QueryRow("SELECT remote_id FROM remote_messages WHERE local_id=? AND remote_seq IS NOT NULL", m.ID).Scan(&native); err != nil {
			return messageAction{}, fmt.Errorf("wait until the message has been delivered before changing it")
		}
		args.MessageID = native
		var action messageAction
		if err = remoteRequest(ctx, remote, "POST", "/v1/message-actions", args, &action); err != nil {
			return action, err
		}
		err = importMessageActions(s.db, remote.Name, []messageAction{action}, nil)
		return action, err
	}
	var cached bool
	_ = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM remote_messages WHERE local_id=?)", m.ID).Scan(&cached)
	if cached {
		return messageAction{}, fmt.Errorf("use the message's source connection")
	}
	tx, err := beginWrite(s.db)
	if err != nil {
		return messageAction{}, err
	}
	defer tx.Rollback()
	if isPostgres(s.db) {
		var id string
		if err = tx.QueryRow("SELECT id FROM messages WHERE id=? FOR UPDATE", m.ID).Scan(&id); err != nil {
			return messageAction{}, err
		}
	}
	var previous messageAction
	err = tx.QueryRow("SELECT seq,id,message_id,actor_id,actor_name,kind,body,emoji,added,created_at FROM message_actions WHERE source='' AND id=?", args.ID).Scan(&previous.Seq, &previous.ID, &previous.MessageID, &previous.ActorID, &previous.ActorName, &previous.Kind, &previous.Body, &previous.Emoji, &previous.Added, &previous.CreatedAt)
	if err == nil {
		if previous.MessageID != m.ID || previous.ActorID != p.ID || previous.Kind != args.Kind || previous.Body != args.Body || previous.Emoji != args.Emoji || previous.Added != args.Added {
			return previous, fmt.Errorf("action ID already used for another change")
		}
		return previous, nil
	}
	if err != sql.ErrNoRows {
		return previous, err
	}
	if args.Kind == "edit" {
		var author string
		err = tx.QueryRow("SELECT principal_id FROM message_principals WHERE message_id=?", m.ID).Scan(&author)
		if (err != nil && err != sql.ErrNoRows) || (author != "" && author != p.ID) || (author == "" && !principalOwnsAddress(p, m.Sender)) {
			return messageAction{}, fmt.Errorf("only the author can edit this message")
		}
		if args.Version != nil {
			var version string
			err = tx.QueryRow("SELECT id FROM message_actions WHERE message_id=? AND kind='edit' ORDER BY seq DESC LIMIT 1", m.ID).Scan(&version)
			if err != nil && err != sql.ErrNoRows {
				return messageAction{}, err
			}
			if version != *args.Version {
				return messageAction{}, fmt.Errorf("message changed since you opened the editor; your draft is kept. Reload the message before saving again")
			}
		}
	}
	action := messageAction{ID: args.ID, MessageID: m.ID, ActorID: p.ID, ActorName: principalAddress(p), Kind: args.Kind, Body: args.Body, Emoji: args.Emoji, Added: args.Added, CreatedAt: time.Now().UnixMilli()}
	err = tx.QueryRow(`INSERT INTO message_actions(source,id,message_id,actor_id,actor_name,kind,body,emoji,added,created_at) VALUES('',?,?,?,?,?,?,?,?,?) RETURNING seq`, action.ID, m.ID, p.ID, action.ActorName, action.Kind, action.Body, action.Emoji, action.Added, action.CreatedAt).Scan(&action.Seq)
	if err != nil {
		return action, err
	}
	return action, tx.Commit()
}

func (s *webServer) messageActions(w http.ResponseWriter, r *http.Request, p *Principal) {
	if r.Method != "POST" {
		apiError(w, 405, "Use POST")
		return
	}
	var args actionRequest
	if !webDecode(w, r, &args) {
		return
	}
	action, err := s.applyMessageAction(r.Context(), p, args)
	if err != nil {
		apiError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, action)
}

func importMessageActions(db *sql.DB, remote string, actions []messageAction, positions map[string]int64) error {
	tx, err := beginWrite(db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, a := range actions {
		if !validActionID(a.ID) || !validMessageID(a.MessageID) || a.Seq <= 0 || a.ActorID == "" || a.ActorName == "" || a.CreatedAt <= 0 || (a.Kind != "edit" && a.Kind != "reaction") || len(a.Body) > MaxBodyBytes || !utf8.ValidString(a.Body) || (a.Kind == "reaction" && !validReaction(a.Emoji)) {
			return fmt.Errorf("invalid message action from coordinator")
		}
		local, err := localRemoteID(tx, remote, a.MessageID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO message_actions(source,id,message_id,actor_id,actor_name,kind,body,emoji,added,created_at,origin_seq) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source,id) DO NOTHING`, remote, a.ID, local, a.ActorID, a.ActorName, a.Kind, a.Body, a.Emoji, a.Added, a.CreatedAt, a.Seq); err != nil {
			return err
		}
	}
	for target, position := range positions {
		if _, err = tx.Exec("INSERT INTO remote_action_positions(remote_name,target,position) VALUES(?,?,?) ON CONFLICT(remote_name,target) DO UPDATE SET position=MAX(remote_action_positions.position,excluded.position)", remote, target, position); err != nil {
			return err
		}
	}
	return tx.Commit()
}
