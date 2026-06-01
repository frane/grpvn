package verbs

import (
	"database/sql"
	"io"
	"strings"

	"grpvn/internal/msg"
	"grpvn/internal/render"
)

func Read(w io.Writer, db *sql.DB, name string, cursor string, follow []string, limit int, advance bool, defaultChannel string, includeTS bool, fullID bool, human bool, color string) (string, int, error) {
	visible := append([]string{}, follow...)
	visible = append(visible, "@"+name)

	placeholders := make([]string, len(visible))
	args := make([]interface{}, len(visible)+1)
	args[0] = cursor
	for i, v := range visible {
		placeholders[i] = "?"
		args[i+1] = v
	}

	query := "SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE id > ? AND target IN (" + strings.Join(placeholders, ", ") + ") ORDER BY id ASC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return cursor, 0, err
	}
	defer rows.Close()

	newCursor := cursor
	count := 0
	for rows.Next() {
		var m msg.Message
		var parentID, correlation sql.NullString
		if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &parentID, &correlation, &m.CreatedAt); err != nil {
			return cursor, 0, err
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
		newCursor = m.ID
		count++
	}

	if count == 0 {
		return cursor, 2, nil
	}

	if !advance {
		newCursor = cursor
	}

	return newCursor, 0, nil
}
