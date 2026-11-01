package main

// A wake directory is how the host tells the check worker that a ref it
// reads has moved, so a pass starts without waiting for the timer. It
// holds at most three files, whoever writes and however often:
//
//   - due, an empty file a writer creates with O_CREAT|O_EXCL. A due that
//     is already there is a mark already made, so a writer counts that as
//     success, and any number of moves leave one file. The host's
//     post-receive hook, the integrator after a landing, and the signer
//     publisher after it moves its ref are the writers. due carries no
//     payload, and nothing reads it as one: it says only "read the refs
//     again". What a pass builds still comes from the refs it reads.
//   - claimed, which due becomes when a pass takes it. A pass renames due
//     to claimed under the lock, before it reads any ref. A writer that
//     marks due after the rename leaves a new due beside claimed, for the
//     next pass.
//   - lock, the consumer's lock. A pass holds it from the claim to the
//     acknowledgement, so two passes over one wake directory take turns,
//     whatever state directories they were given.
//
// A pass removes claimed once it has read every project and recorded
// every failure it met (worker.go's handled). It never removes due:
// removing claimed by name cannot touch a mark made since the claim. A
// pass that read less, or a pass that was killed, leaves claimed. Nothing
// watches claimed; it says that the last claim was not handled, and the
// next pass to handle everything removes it. That pass reads the refs as
// they are when it runs, which covers every move the claim stood for.
//
// The host makes the directory, and the worker owns it. No file in it is
// a temporary, so nothing is left to clean up when a writer or a pass is
// killed.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// claimFailedStatus is the status a pass exits with when it could not
// claim its wake directory's due mark. The pass reads and builds all the
// same, and exits so whatever runs it can hold off before the next pass,
// rather than start one at once for a due that is still there.
const claimFailedStatus = 75

// claimFailure is the error a pass returns when it ran but could not
// claim due. main exits claimFailedStatus for it.
type claimFailure struct{ msg string }

func (c claimFailure) Error() string { return c.msg }

// wake is one pass's hold on a wake directory.
type wake struct {
	dir  string
	lock *os.File // the directory's lock; nil when it could not be taken
	err  error    // why due could not be claimed, or nil
}

// claimWake takes the wake directory's lock, waiting for any pass that
// holds it, and renames due to claimed. No due is nothing to claim, and
// not a failure. A lock or a rename that fails is kept in err, and the
// pass goes on without a claim.
func claimWake(dir string, stderr io.Writer) *wake {
	w := &wake{dir: dir}
	lock, err := lockState(filepath.Join(dir, "lock"), stderr)
	if err != nil {
		w.err = fmt.Errorf("could not take the lock in %s: %w", dir, err)
		return w
	}
	w.lock = lock
	err = os.Rename(filepath.Join(dir, "due"), filepath.Join(dir, "claimed"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		w.err = fmt.Errorf("could not claim %s: %w", filepath.Join(dir, "due"), err)
	}
	return w
}

// ack removes claimed, for a pass that handled what it read. A pass that
// could not claim removes nothing: it holds no claim to acknowledge.
func (w *wake) ack() error {
	if w.err != nil || w.lock == nil {
		return nil
	}
	err := os.Remove(filepath.Join(w.dir, "claimed"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (w *wake) close() {
	if w.lock != nil {
		w.lock.Close()
	}
}
