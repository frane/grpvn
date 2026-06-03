package internal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const CurrentSchemaVersion = 1

func dbPath() (string, error) {
	if env := os.Getenv("GRPVN_DB"); env != "" {
		if err := os.MkdirAll(filepath.Dir(env), 0755); err != nil {
			return "", fmt.Errorf("create db dir: %w", err)
		}
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	dir := filepath.Join(home, ".grpvn")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return filepath.Join(dir, "grpvn.db"), nil
}

func OpenDB() (*sql.DB, error) {
	path, err := dbPath()
	if err != nil {
		return nil, err
	}
	// Use DSN-level pragmas so every connection in the pool inherits WAL mode
	// and a 5s busy timeout. Without this, concurrent processes racing on the
	// first CREATE TABLE IF NOT EXISTS observe SQLITE_BUSY before the
	// per-connection PRAGMA in Schema has a chance to apply.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One writer connection avoids in-process write-write contention; readers
	// still come from the same handle but writes serialize naturally.
	db.SetMaxOpenConns(1)
	if err := Migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	if _, err := db.Exec(Schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	var current int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current)
	if err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}
	if current < CurrentSchemaVersion {
		// OR IGNORE so concurrent first-time migrations across processes race
		// harmlessly: one wins, the rest no-op. (Without this, two processes
		// both observing current=0 each attempted the INSERT and one hit
		// SQLITE_CONSTRAINT on the schema_version PRIMARY KEY.)
		if _, err := db.Exec(
			"INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
			CurrentSchemaVersion, time.Now().UnixMilli(),
		); err != nil {
			return fmt.Errorf("record schema_version: %w", err)
		}
	}
	return nil
}

func SchemaVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&v)
	return v, err
}
