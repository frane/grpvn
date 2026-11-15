package internal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"modernc.org/sqlite"
)

func sqliteCommitEventName(path string) *uint16 {
	path, _ = filepath.Abs(filepath.Clean(filepath.FromSlash(path)))
	hash := sha256.Sum256([]byte(strings.ToLower(path)))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Local\grpvn-chat-%x`, hash[:16]))
	return name
}

func init() {
	// ReadDirectoryChangesW may defer WAL write events while SQLite keeps the
	// file open. A native event wakes every local viewer without network or
	// database polling. Only chat-log/metadata transactions need a wake-up.
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		hooks, ok := conn.(sqlite.HookRegisterer)
		if !ok {
			return nil
		}
		path, _, _ := strings.Cut(strings.TrimPrefix(dsn, "file:"), "?")
		name := sqliteCommitEventName(path)
		dirty := false
		hooks.RegisterPreUpdateHook(func(change sqlite.SQLitePreUpdateData) {
			if change.TableName == "messages" || change.TableName == "chat_metadata" {
				dirty = true
			}
		})
		hooks.RegisterRollbackHook(func() { dirty = false })
		hooks.RegisterCommitHook(func() int32 {
			if dirty {
				dirty = false
				if event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name); err == nil {
					_ = windows.SetEvent(event)
					_ = windows.CloseHandle(event)
				}
			}
			return 0
		})
		return nil
	})
}

func watchSQLiteCommits(ctx context.Context, path string, signal func()) {
	event, err := windows.CreateEvent(nil, 1, 0, sqliteCommitEventName(path))
	if err != nil {
		return
	}
	defer windows.CloseHandle(event)
	signal()
	for ctx.Err() == nil {
		status, err := windows.WaitForSingleObject(event, 100)
		if err != nil {
			return
		}
		if status != windows.WAIT_OBJECT_0 {
			continue
		}
		_ = windows.ResetEvent(event)
		signal()
		// SQLite's commit callback precedes the final commit. Recheck once
		// after settling; only a changed committed revision is ever published.
		time.AfterFunc(25*time.Millisecond, func() {
			if ctx.Err() == nil {
				signal()
			}
		})
	}
}
