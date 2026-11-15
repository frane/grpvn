//go:build !darwin

package internal

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// inotify and ReadDirectoryChangesW watch the directory without opening the
// SQLite database or its shared-memory lock file.
func watchSQLiteFiles(ctx context.Context, path string, signal func()) {
	// SQLite reports forward-slash paths on Windows; filesystem events use
	// native separators. Comparing those verbatim misses WAL write events.
	path = filepath.Clean(filepath.FromSlash(path))
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
			name := filepath.Clean(event.Name)
			if name == path || name == path+"-wal" || (runtime.GOOS == "windows" && (strings.EqualFold(name, path) || strings.EqualFold(name, path+"-wal"))) {
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
