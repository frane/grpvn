package msg

import (
	"database/sql"
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

func New(sender, target string, body []byte) *Message {
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
