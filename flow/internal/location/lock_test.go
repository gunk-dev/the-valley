package location

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// lone is a build that locks one path alone, as one that predates the
// lock's move, or one that has finished it, does. flock excludes two open
// file descriptions of a file, in one process as across two.
func lone(t *testing.T, path string) (release func(), ok bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			t.Fatal(err)
		}
		return nil, false
	}
	return func() { f.Close() }, true
}

func TestLockExcludesEitherPathAlone(t *testing.T) {
	for i, name := range []string{"old-path-only", "new-path-only"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			paths := []string{filepath.Join(dir, "old", "x.lock"), filepath.Join(dir, "new", "x.lock")}
			alone := paths[i]

			// The lone locker first: this build waits its turn.
			release, ok := lone(t, alone)
			if !ok {
				t.Fatal("lone locker could not lock")
			}
			_, err := TryLock(paths...)
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Path != alone || !errors.Is(err, ErrBusy) {
				t.Fatalf("TryLock with %s held = %v, want busy at it", alone, err)
			}
			// A failure at the second path released the first.
			if i == 1 {
				if r, ok := lone(t, paths[0]); !ok {
					t.Error("the first path stayed locked after the second was busy")
				} else {
					r()
				}
			}
			release()

			// This build first: the lone locker waits its turn.
			l, err := TryLock(paths...)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := lone(t, alone); ok {
				t.Errorf("a lone locker of %s got the lock this build holds", alone)
			}
			l.Release()
			if r, ok := lone(t, alone); !ok {
				t.Error("the lock stayed held after Release")
			} else {
				r()
			}
			for _, p := range paths {
				if _, err := os.Stat(p); err != nil {
					t.Errorf("Release removed %s: %v", p, err)
				}
			}
		})
	}
}

// Two paths that are one file are locked once: a second open file
// description would wait on the first forever.
func TestLockSameFileOnce(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.lock")
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "flow", "new.lock")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	l, err := WaitLock(old, link)
	if err != nil {
		t.Fatalf("WaitLock of one file under two names: %v", err)
	}
	if _, ok := lone(t, old); ok {
		t.Error("a lone locker got the lock")
	}
	l.Release()
	if r, ok := lone(t, link); !ok {
		t.Error("the lock stayed held after Release")
	} else {
		r()
	}
}
