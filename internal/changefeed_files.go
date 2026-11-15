//go:build !darwin

package internal

import (
	"context"
	"github.com/fsnotify/fsnotify"
	"path/filepath"
)

// inotify and ReadDirectoryChangesW watch the directory without opening the
// SQLite database or its shared-memory lock file.
func watchSQLiteFiles(ctx context.Context, path string, signal func()) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()
	if watcher.Add(filepath.Dir(path)) != nil {
		return
	}
	signal()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Name == path || event.Name == path+"-wal" {
				signal()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
			signal()
		}
	}
}
