//go:build unix

package main

// A controller owns the repository it serves. One controller serves a
// repository at a time: watch takes an exclusive flock on the repository's
// own directory before it sweeps scratch or runs a pass, and holds it until
// it exits. A second watch over the same repository finds the lock held and
// stops before it sweeps or passes.
//
// The lock is on the repository's directory rather than a file in the wake
// directory. The wake directory's writers may create and remove entries in
// it, which is how they mark it, so a lock file there could be removed and
// made again under a running controller, and a missing wake directory
// would leave nothing to lock. The repository's directory is there
// whenever the controller can do anything, and is the thing a second
// controller must not sweep or land into.
//
// A lock on a directory is a lock on the directory the path named when it
// was opened. So the lock counts only while the path still names that
// directory: it is checked after the lock is taken, before the sweep, and
// before every pass. A path that names another directory stops the
// controller, which then holds nothing the next controller has to wait
// for.

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ownTries is how many times ownRepo opens and locks the repository while
// the path comes to name another directory between the open and the check.
const ownTries = 3

// ownLocked, when set, runs once ownRepo has locked what it opened and
// before it checks the path. Tests set it to replace the directory there.
var ownLocked func()

// repoLock is a controller's hold on the repository it serves.
type repoLock struct {
	f    *os.File
	path string
}

// ownRepo takes the repository's lock, or says why it cannot. Every
// descriptor it opens and does not return is closed.
func ownRepo(repo string) (*repoLock, error) {
	for try := 1; ; try++ {
		f, err := os.Open(repo)
		if err != nil {
			return nil, fmt.Errorf("%s: could not open it to lock it: %w", repo, err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, fmt.Errorf("%s: %w; one controller serves a repository", repo, errLockHeld)
			}
			return nil, fmt.Errorf("%s: could not lock it: %w", repo, err)
		}
		if ownLocked != nil {
			ownLocked()
		}
		r := &repoLock{f: f, path: repo}
		err = r.check()
		if err == nil {
			return r, nil
		}
		f.Close()
		if try == ownTries {
			return nil, fmt.Errorf("%s: another directory took its place each of %d times it was locked, so this controller does not serve it", repo, ownTries)
		}
	}
}

// check says whether the path still names the directory the lock is on.
func (r *repoLock) check() error {
	if r == nil {
		return nil
	}
	held, err := r.f.Stat()
	if err != nil {
		return fmt.Errorf("%s: could not read the directory it locked: %w", r.path, err)
	}
	now, err := os.Stat(r.path)
	if err != nil || !os.SameFile(held, now) {
		return fmt.Errorf("%s is no longer the directory this controller locked; it stops, and serves it again only from a new start", r.path)
	}
	return nil
}

func (r *repoLock) Close() error {
	if r == nil {
		return nil
	}
	return r.f.Close()
}
