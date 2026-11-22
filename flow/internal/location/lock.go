package location

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrBusy is a lock another process holds.
var ErrBusy = errors.New("held by another process")

// BusyError is the lock file another process holds.
type BusyError struct{ Path string }

func (e *BusyError) Error() string        { return e.Path + " is " + ErrBusy.Error() }
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// Lock is an exclusive flock held on each of a lock's files.
type Lock struct{ files []*os.File }

// TryLock locks each of paths in turn, without waiting, creating them and
// their directories as needed. A path another process holds is a
// BusyError, and releases what it took. Paths that are one file are locked
// once. A lock that moves is the legacy path then the flow one: a build
// that locks either alone excludes, and is excluded by, one that locks
// both.
func TryLock(paths ...string) (*Lock, error) { return lock(paths, false) }

// WaitLock is TryLock waiting for each path in turn.
func WaitLock(paths ...string) (*Lock, error) { return lock(paths, true) }

func lock(paths []string, wait bool) (*Lock, error) {
	l := &Lock{}
	var held []os.FileInfo
	for _, path := range paths {
		f, fi, err := openLock(path)
		if err != nil {
			l.Release()
			return nil, err
		}
		if sameAsAny(fi, held) {
			// This description holds no lock, so closing it drops none.
			f.Close()
			continue
		}
		how := syscall.LOCK_EX
		if !wait {
			how |= syscall.LOCK_NB
		}
		if err := syscall.Flock(int(f.Fd()), how); err != nil {
			f.Close()
			l.Release()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, &BusyError{Path: path}
			}
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		l.files = append(l.files, f)
		held = append(held, fi)
	}
	return l, nil
}

func openLock(path string) (*os.File, os.FileInfo, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

func sameAsAny(fi os.FileInfo, held []os.FileInfo) bool {
	for _, h := range held {
		if os.SameFile(fi, h) {
			return true
		}
	}
	return false
}

// Release drops the lock, last file first. Its files stay: another process
// may hold or wait on them.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	for i := len(l.files) - 1; i >= 0; i-- {
		l.files[i].Close() // close drops the flock
	}
	l.files = nil
}
