package internal

import (
	"database/sql"
	"errors"
	"net/http"
)

func (s *webServer) appearance(w http.ResponseWriter, r *http.Request, p *Principal) {
	if p.Kind != "human" {
		apiError(w, 403, "A human profile is required")
		return
	}
	mode := "system"
	if r.Method == "GET" {
		err := s.db.QueryRow("SELECT value FROM human_preferences WHERE principal_id=? AND name='appearance'", p.ID).Scan(&mode)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			apiError(w, 500, "Could not load appearance")
			return
		}
		writeJSON(w, 200, map[string]string{"mode": mode})
		return
	}
	if r.Method != "PATCH" {
		apiError(w, 405, "Use GET or PATCH")
		return
	}
	var update struct{ Mode string }
	if !webDecode(w, r, &update) {
		return
	}
	if update.Mode != "system" && update.Mode != "light" && update.Mode != "dark" {
		apiError(w, 400, "Choose System, Light or Dark")
		return
	}
	if _, err := s.db.Exec("INSERT INTO human_preferences(principal_id,name,value) VALUES(?,'appearance',?) ON CONFLICT(principal_id,name) DO UPDATE SET value=excluded.value", p.ID, update.Mode); err != nil {
		apiError(w, 500, "Could not save appearance")
		return
	}
	_ = touchPersonalMetadata(s.db, p.ID)
	writeJSON(w, 200, map[string]string{"mode": update.Mode})
}
