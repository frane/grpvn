package verbs

import (
	"database/sql"
	"fmt"
	"io"
	"strings"
)

func Check(w io.Writer, db *sql.DB, name string, cursor string, follow []string) (int, error) {
	visible := append([]string{}, follow...)
	visible = append(visible, "@"+name)

	placeholders := make([]string, len(visible))
	args := make([]interface{}, len(visible)+1)
	args[0] = cursor
	for i, v := range visible {
		placeholders[i] = "?"
		args[i+1] = v
	}

	query := fmt.Sprintf("SELECT target, COUNT(*) FROM messages WHERE id > ? AND target IN (%s) GROUP BY target", strings.Join(placeholders, ", "))
	rows, err := db.Query(query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	counts := []string{}
	total := 0
	for rows.Next() {
		var target string
		var count int
		if err := rows.Scan(&target, &count); err != nil {
			return 0, err
		}
		if target == "@"+name {
			target = "@me"
		}
		counts = append(counts, fmt.Sprintf("%d %s", count, target))
		total += count
	}

	if total == 0 {
		return 2, nil
	}

	fmt.Fprintln(w, strings.Join(counts, " "))
	return 0, nil
}
