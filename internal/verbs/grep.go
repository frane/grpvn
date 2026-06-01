package verbs

import (
	"database/sql"
	"fmt"
	"io"
	"regexp"
	"strings"

	"grpvn/internal/msg"
	"grpvn/internal/render"
)

func Grep(w io.Writer, db *sql.DB, name string, follow []string, pattern string, scope string, limit int, defaultChannel string, includeTS bool, fullID bool, human bool, color string) error {
	var visible []string
	if scope != "" {
		visible = []string{scope}
	} else {
		visible = append([]string{}, follow...)
		visible = append(visible, "@"+name)
	}

	placeholders := make([]string, len(visible))
	args := make([]interface{}, len(visible))
	for i, v := range visible {
		placeholders[i] = "?"
		args[i] = v
	}

	query := fmt.Sprintf("SELECT id, sender, target, body, chain_root, chain_depth, parent_id, correlation, created_at FROM messages WHERE target IN (%s) ORDER BY id DESC", strings.Join(placeholders, ", "))

	rows, err := db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid regex: %w", err)
	}

	count := 0
	for rows.Next() {
		var m msg.Message
		var parentID, correlation sql.NullString
		if err := rows.Scan(&m.ID, &m.Sender, &m.Target, &m.Body, &m.ChainRoot, &m.ChainDepth, &parentID, &correlation, &m.CreatedAt); err != nil {
			return err
		}
		if !re.Match(m.Body) {
			continue
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
		if limit > 0 && count >= limit {
			break
		}
	}

	return nil
}
