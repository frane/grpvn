package internal

import (
	"context"
	"golang.org/x/sys/unix"
	"path/filepath"
)

// kqueue directory emulation in fsnotify opens every child, including the
// database and -shm file. Closing ANY descriptor for those files releases the
// process's POSIX locks, even when SQLite owns another descriptor. Never open
// either here. SQLite locks the database and shared memory; its WAL is safe to
// watch. A directory-only watch detects WAL creation/replacement.
func watchSQLiteFiles(ctx context.Context, path string, signal func()) {
	queue, err := unix.Kqueue()
	if err != nil {
		return
	}
	defer unix.Close(queue)
	dir, err := unix.Open(filepath.Dir(path), unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	defer unix.Close(dir)
	add := func(fd int) error {
		event := unix.Kevent_t{Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_ATTRIB | unix.NOTE_DELETE | unix.NOTE_RENAME}
		event.Ident = uint64(fd)
		_, err := unix.Kevent(queue, []unix.Kevent_t{event}, nil, nil)
		return err
	}
	if add(dir) != nil {
		return
	}
	wal := -1
	defer func() {
		if wal >= 0 {
			unix.Close(wal)
		}
	}()
	update := func() {
		var disk, opened unix.Stat_t
		exists := unix.Stat(path+"-wal", &disk) == nil
		if wal >= 0 && exists && unix.Fstat(wal, &opened) == nil && disk.Ino == opened.Ino && disk.Dev == opened.Dev {
			return
		}
		if wal >= 0 {
			unix.Close(wal)
			wal = -1
		}
		if exists {
			if fd, err := unix.Open(path+"-wal", unix.O_EVTONLY|unix.O_CLOEXEC, 0); err == nil {
				if add(fd) == nil {
					wal = fd
				} else {
					unix.Close(fd)
				}
			}
		}
	}
	update()
	signal()
	events := make([]unix.Kevent_t, 8)
	for ctx.Err() == nil {
		timeout := unix.NsecToTimespec(250_000_000)
		n, err := unix.Kevent(queue, nil, events, &timeout)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		update()
		signal()
	}
}
