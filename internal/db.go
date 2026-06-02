package internal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func OpenDB() (*sql.DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	dir := filepath.Join(home, ".grpvn")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	path := filepath.Join(dir, "grpvn.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	if _, err := db.Exec(Schema); err != nil {
		return err
	}
	var version int
	err := db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		_, _ = db.Exec("INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (0, ?)", time.Now().UnixMilli())
	}
	return nil
}