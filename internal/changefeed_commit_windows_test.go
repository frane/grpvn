package internal

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsCommitEventNormalizesPaths(t *testing.T) {
	a := windows.UTF16PtrToString(sqliteCommitEventName(`C:/Users/Agent/grpvn.db`))
	b := windows.UTF16PtrToString(sqliteCommitEventName(`c:\users\agent\grpvn.db`))
	if a != b {
		t.Fatal("database paths did not share a commit signal", a, b)
	}
}

func TestWindowsCommitEventWakesForRepeatedChatCommits(t *testing.T) {
	db := newTestDB(t)
	var n int
	var name, path string
	if err := db.QueryRow("PRAGMA database_list").Scan(&n, &name, &path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wake := make(chan struct{}, 32)
	go watchSQLiteCommits(ctx, path, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("native watcher did not start")
	}
	for i := 0; i < 4; i++ {
		// Let the prior advisory wake settle, then require a fresh signal.
		time.Sleep(40 * time.Millisecond)
		for len(wake) > 0 {
			<-wake
		}
		if _, err := Send(db, "peer", "#dev", "commit", "", false); err != nil {
			t.Fatal(err)
		}
		select {
		case <-wake:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("commit was left to filesystem polling")
		}
	}
}
