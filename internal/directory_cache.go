package internal

import (
	"database/sql"
	"encoding/json"
)

func cacheRemoteDirectory(db *sql.DB, remote string, result map[string]any) {
	people, ok := result["people"].([]any)
	if !ok {
		return
	}
	tx, err := beginWrite(db)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, entry := range people {
		person, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, _ := person["id"].(string)
		if id == "" {
			continue
		}
		encoded, err := json.Marshal(person)
		if err != nil {
			return
		}
		if _, err = tx.Exec("INSERT INTO remote_directory(remote_name,profile_id,profile) VALUES(?,?,?) ON CONFLICT(remote_name,profile_id) DO UPDATE SET profile=excluded.profile", remote, id, string(encoded)); err != nil {
			return
		}
	}
	_ = tx.Commit()
}
func cachedRemoteDirectory(db *sql.DB, remote, name string) (map[string]any, bool) {
	rows, err := db.Query("SELECT profile FROM remote_directory WHERE remote_name=? ORDER BY profile_id", remote)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	people := []any{}
	for rows.Next() {
		var encoded string
		if rows.Scan(&encoded) != nil {
			continue
		}
		var person map[string]any
		if json.Unmarshal([]byte(encoded), &person) != nil {
			continue
		}
		matches := name == "" || person["id"] == name || person["name"] == name
		if aliases, ok := person["aliases"].([]any); ok {
			for _, alias := range aliases {
				matches = matches || alias == name
			}
		}
		if matches {
			people = append(people, person)
		}
	}
	return map[string]any{"people": people, "cached": true}, len(people) > 0
}
