package internal

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

type Message struct {
	ID          string
	Sender      string
	Target      string
	Body        []byte
	ChainRoot   string
	ChainDepth  int
	ParentID    *string
	Correlation *string
	CreatedAt   int64
}

func NewMessage(sender, target string, body []byte) *Message {
	id := ulid.Make().String()
	return &Message{
		ID:         id,
		Sender:     sender,
		Target:     target,
		Body:       body,
		ChainRoot:  id,
		ChainDepth: 0,
		CreatedAt:  time.Now().UnixMilli(),
	}
}

func (m *Message) Save(db *sql.DB) error {
	_, err := db.Exec("INSERT INTO messages (id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.ID, m.Sender, m.Target, m.Body, m.ChainRoot, m.ChainDepth, m.ParentID, m.Correlation, m.CreatedAt)
	return err
}

var ulidPrefixRegex = regexp.MustCompile("^[0-9A-HJKMNP-TV-Z]{6,26}$")

func ResolveTarget(db *sql.DB, input string, defaultChannel string) (target string, parent *Message, err error) {
	if input == "" {
		if defaultChannel == "" {
			return "", nil, errors.New("no target and no default")
		}
		return defaultChannel, nil, nil
	}
	if strings.HasPrefix(input, "#") || strings.HasPrefix(input, "@") {
		return input, nil, nil
	}
	if ulidPrefixRegex.MatchString(strings.ToUpper(input)) {
		parent, err = FindMessageByPrefix(db, input)
		if err == nil {
			return parent.Target, parent, nil
		}
	}
	return "", nil, fmt.Errorf("invalid target: %s", input)
}

func FindMessageByPrefix(db *sql.DB, prefix string) (*Message, error) {
	prefix = strings.ToUpper(prefix)
	row := db.QueryRow("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE id LIKE ?", prefix+"%")
	var m Message
	var parentID, correlation sql.NullString
	if err := row.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &parentID, &correlation, &m.CreatedAt); err != nil {
		return nil, err
	}
	if parentID.Valid {
		m.ParentID = &parentID.String
	}
	if correlation.Valid {
		m.Correlation = &correlation.String
	}
	return &m, nil
}
