package db

import (
	"os"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	tmpDir := t.TempDir()
	// os.UserHomeDir usually respects HOME env var on unix/darwin
	os.Setenv("HOME", tmpDir)

	db, err := Open()
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer db.Close()

	// Check if table exists
	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='messages'").Scan(&name)
	if err != nil {
		t.Fatalf("messages table not found: %v", err)
	}

	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'").Scan(&name)
	if err != nil {
		t.Fatalf("schema_version table not found: %v", err)
	}
}
