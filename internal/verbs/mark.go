package verbs

import (
	"database/sql"
	"io"
	"time"

	"grpvn/internal/msg"
	"grpvn/internal/render"
)

func Mark(w io.Writer, db *sql.DB, name string, messageArg string, delete bool, defaultChannel string, includeTS bool, fullID bool, human bool, color string) error {
	if messageArg == "" && !delete {
		query := "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages JOIN marks ON messages.id = marks.message_id WHERE marks.agent_name = ? ORDER BY id ASC"
		rows, err := db.Query(query, name)
		if err != nil {
			return err
		}
		defer rows.Close()

		count := 0
		for rows.Next() {
			var m msg.Message
			var parentID, correlation sql.NullString
			if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &parentID, &correlation, &m.CreatedAt); err != nil {
				return err
			}
			if parentID.Valid {
				m.ParentID = &parentID.String
			}
			if correlation.Valid {
				m.Correlation = &correlation.String
			}
			if human {
				if count == 0 {
					render.HumanHeader(w, render.ShouldColor(color))
				}
				render.Human(w, &m, name, render.ShouldColor(color))
			} else {
				render.AI(w, &m, name, defaultChannel, includeTS, fullID)
			}
			count++
		}
		return nil
	}

	m, err := msg.FindByPrefix(db, messageArg)
	if err != nil {
		return err
	}

	if delete {
		_, err := db.Exec("DELETE FROM marks WHERE agent_name = ? AND message_id = ?", name, m.ID)
		return err
	}

	_, err = db.Exec("INSERT OR REPLACE INTO marks (agent_name, message_id, marked_at) VALUES (?, ?, ?)", name, m.ID, time.Now().UnixMilli())
	return err
}
