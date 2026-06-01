package verbs

import (
	"database/sql"
	"fmt"
	"io"
	"strings"

	"grpvn/internal/msg"
	"grpvn/internal/render"
)

func Log(w io.Writer, db *sql.DB, name string, arg string, limit int, defaultChannel string, includeTS bool, fullID bool, human bool, color string) error {
	var query string
	var args []interface{}

	if strings.HasPrefix(arg, "#") || strings.HasPrefix(arg, "@") {
		query = "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE target = ? ORDER BY id ASC"
		args = []interface{}{arg}
	} else {
		parent, err := msg.FindByPrefix(db, arg)
		if err != nil {
			return err
		}
		query = "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE chain_root = ? ORDER BY id ASC"
		args = []interface{}{parent.ChainRoot}
	}

	if limit > 0 {
		if strings.HasPrefix(arg, "#") || strings.HasPrefix(arg, "@") {
			query = fmt.Sprintf("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM (SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE target = ? ORDER BY id DESC LIMIT ?) ORDER BY id ASC")
			args = append(args, limit)
		} else {
			query += fmt.Sprintf(" LIMIT %d", limit)
		}
	}

	rows, err := db.Query(query, args...)
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
