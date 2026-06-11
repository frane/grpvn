package internal

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// CurrentSchemaVersion is 2: messages gained an explicit seq INTEGER
// PRIMARY KEY AUTOINCREMENT and read cursors moved from state.json into the
// cursors table. See schema.go for why.
const CurrentSchemaVersion = 2

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
	// Retry SQLITE_BUSY on the schema apply. The DSN busy_timeout covers
	// row-level contention but the journal_mode=WAL transition wants an
	// exclusive lock that Windows file-locking will occasionally bounce
	// past the timeout when three processes hit a fresh DB at the same time.
	// A short backoff per attempt is plenty: WAL only gets set once per DB.
	const attempts = 8
	for attempt := 0; attempt < attempts; attempt++ {
		err := migrateOnce(db)
		if err == nil {
			return nil
		}
		if attempt+1 < attempts && strings.Contains(err.Error(), "database is locked") {
			time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
			continue
		}
		return err
	}
	return nil
}

func migrateOnce(db *sql.DB) error {
	// A v1 store predates the seq column, so the v2 CREATE TABLE IF NOT
	// EXISTS would no-op against the old shape. Detect v1 BEFORE applying
	// the base schema: the rebuild below replaces the table wholesale.
	var hasMessages, hasSeq bool
	if err := db.QueryRow(
		"SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = 'messages'",
	).Scan(&hasMessages); err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	if hasMessages {
		if err := db.QueryRow(
			"SELECT COUNT(*) > 0 FROM pragma_table_info('messages') WHERE name = 'seq'",
		).Scan(&hasSeq); err != nil {
			return fmt.Errorf("inspect messages shape: %w", err)
		}
	}

	if hasMessages && !hasSeq {
		// v1 -> v2: rebuild messages with the seq column. Foreign keys must
		// be off for the rebuild — marks rows reference messages(id), and
		// with enforcement on, DROP TABLE messages performs an implicit
		// DELETE that the marks FK rejects. The pragma is per-connection and
		// cannot run inside a transaction; MaxOpenConns(1) guarantees the
		// transaction below lands on the same connection.
		if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("disable fk for migration: %w", err)
		}
		err := func() error {
			tx, err := db.Begin()
			if err != nil {
				return fmt.Errorf("begin v1->v2: %w", err)
			}
			defer tx.Rollback()
			// Re-check inside the transaction: a sibling process may have
			// completed the rebuild while we waited on the write lock.
			var stillV1 bool
			if err := tx.QueryRow(
				"SELECT COUNT(*) = 0 FROM pragma_table_info('messages') WHERE name = 'seq'",
			).Scan(&stillV1); err != nil {
				return fmt.Errorf("recheck v1: %w", err)
			}
			if stillV1 {
				if _, err := tx.Exec(migrateV1toV2); err != nil {
					return fmt.Errorf("rebuild messages v1->v2: %w", err)
				}
				if _, err := tx.Exec(
					"INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
					2, time.Now().UnixMilli(),
				); err != nil {
					return fmt.Errorf("record schema_version 2: %w", err)
				}
			}
			return tx.Commit()
		}()
		if _, fkErr := db.Exec("PRAGMA foreign_keys = ON"); fkErr != nil && err == nil {
			err = fmt.Errorf("re-enable fk after migration: %w", fkErr)
		}
		if err != nil {
			return err
		}
	}

	if _, err := db.Exec(Schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	var current int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema_version: %w", err)
	}
	if current < CurrentSchemaVersion {
		// OR IGNORE so concurrent first-time migrations across processes race
		// harmlessly: one wins, the rest no-op.
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
