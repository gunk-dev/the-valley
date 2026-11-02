package main

// The controller's own wake directory. watch --woken-by DIR starts a pass
// soon after a writer marks DIR due, instead of at the end of its interval.
// It holds at most one file, whoever writes and however often: due, an
// empty file a writer creates with O_CREAT|O_EXCL, exactly as markDue
// (bus.go) creates the check worker's. A due that is already there is a
// mark already made, so any number of moves leave one file. The host's
// post-receive hook and the controller that lands the floor are the
// writers. Nothing reads due: it says only "read the refs again", and what
// a pass decides still comes from the refs.
//
// The wake directory carries marks and nothing else. Which controller
// serves a repository is the repository's own lock (own_unix.go), taken
// before the controller sweeps or passes and checked before every pass. So
// a wake directory that is missing, replaced or watched by nothing changes
// how soon a pass starts, and never which controller runs it.
//
// The controller watches the directory with inotify before it removes due,
// and removes due before a pass reads any ref. A mark made after the
// removal is a new due, which the controller finds when the pass ends and
// starts the next pass for. A mark made before the removal is covered by
// the pass, which reads the refs after it. So a mark is never lost, and a
// pass never removes a mark made since it started reading.
//
// Before it waits, the controller looks for due on disk, so a notification
// it was given is never what starts a pass, and a notification left over
// from a due that a pass already removed starts nothing. A pass that lands
// anything is followed at once by another, with no mark: a request judged
// against the tip before the landing is judged again against the tip after
// it.
//
// The interval stays the recovery pass. A mark that never came, a watch
// that failed, a directory that was missing, and a crash between a ref
// move and its mark are all covered by the next timer pass, and by the
// pass every start makes. A due that cannot be removed is ignored until
// the next timer pass, so it starts one pass, and not one after another.

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// passReason says why a pass started, in the line that opens a woken
// controller's pass and in every pass's timing lines (timing.go).
type passReason string

const (
	reasonStartup  passReason = "startup"   // the first pass of every start
	reasonDue      passReason = "due"       // a writer marked the directory
	reasonTimer    passReason = "timer"     // the interval ran out
	reasonFollowUp passReason = "follow-up" // the pass before it landed something
	reasonWatcher  passReason = "watcher"   // the watch was restored after a failure
	reasonOnce     passReason = "reconcile" // the one pass of reconcile, run by hand
)

const (
	// watchRetry is how long a controller whose watch failed waits before it
	// tries to watch the directory again. Each try opens and locks the
	// directory and adds two inotify watches, and a failure is said once
	// until it changes.
	watchRetry = 5 * time.Second
	// failedPassHold is the least time from a pass that returned an error to
	// the next pass, however many marks arrive meanwhile. It is never longer
	// than the interval.
	failedPassHold = 10 * time.Second
)

// errLockHeld is what taking a repository's lock returns when another
// controller holds it.
var errLockHeld = errors.New("another controller holds its lock")

// errGone is what a watch ends with when the directory it holds is no
// longer the one the path names: it was removed, moved or replaced.
var errGone = errors.New("the directory was removed, moved or replaced")

// watcher is a watch on a wake directory, as the platform provides one. It
// holds the directory it watches, and looks for due and removes it there.
type watcher interface {
	// events is sent to, without blocking, when due is created in the
	// directory or the kernel dropped events. Its capacity is one, so any
	// number of notifications wait as one.
	events() <-chan struct{}
	// failed is sent the reason once the watch can no longer notify.
	failed() <-chan error
	// removeDue removes due from the directory watched. No due is no error.
	removeDue() error
	// hasDue says whether due is in the directory watched.
	hasDue() bool
	close()
}

// wakeLoop runs passes over the refs: one at start, one soon after each
// mark, one after each pass that landed, and one when the interval runs
// out with nothing else having started one.
type wakeLoop struct {
	dir      string
	interval time.Duration
	stderr   io.Writer
	// pass runs one pass and says whether it landed anything.
	pass func(reason passReason) (landed bool, err error)
	// owned says whether the controller still holds the repository it
	// serves (repoLock.check). It is asked before every pass, and an error
	// ends the loop before the pass. Nil asks nothing.
	owned func() error

	// The platform's watch (wake_linux.go). Tests may replace it.
	watch func(dir string) (watcher, error)
	// stop ends the loop when it is closed, for tests; nil never ends it.
	stop <-chan struct{}

	w        watcher   // nil while the directory is not watched
	retryAt  time.Time // when to try watching again, while w is nil
	failure  string    // the last failure said, so one is not said again
	restored bool      // the watch came back, and a pass is owed for it
}

// run runs passes until stop is closed, or until the controller no longer
// holds the repository it serves. A watch that fails changes only how soon
// passes start.
func (l *wakeLoop) run() error {
	// A directory that is missing or cannot be watched is said here, and
	// tried again later. The start's pass runs either way.
	l.arm()
	defer l.disarm()
	reason := reasonStartup
	for {
		if l.owned != nil {
			if err := l.owned(); err != nil {
				return err
			}
		}
		claimed := l.claim()
		landed, err := l.pass(reason)
		end := time.Now()
		next := end.Add(l.interval)
		var earliest, holdDue time.Time
		if err != nil {
			fmt.Fprintf(l.stderr, "integrator: %v\n", err)
			earliest = end.Add(min(failedPassHold, l.interval))
		}
		if !claimed {
			holdDue = next
		}
		if landed && err == nil {
			reason = reasonFollowUp
			continue
		}
		if reason = l.wait(next, earliest, holdDue); reason == "" {
			return nil
		}
	}
}

// claim removes due, before the pass reads any ref, and says whether the
// mark is taken care of. No due, or no watch to consume it through, is
// nothing to claim. A due that cannot be removed is said, and the caller
// ignores it until the next timer pass.
func (l *wakeLoop) claim() bool {
	if l.w == nil {
		return true
	}
	err := l.w.removeDue()
	if err == nil {
		return true
	}
	fmt.Fprintf(l.stderr, "integrator: could not remove the due in %s, so it starts no pass before the next timer pass: %v\n",
		l.dir, err)
	return false
}

// due says whether a writer has marked the directory. It is the durable
// state, and what decides whether a pass starts.
func (l *wakeLoop) due() bool {
	return l.w != nil && l.w.hasDue()
}

// wait waits for the reason of the next pass: a due, once earliest has
// passed and holdDue has; a restored watch, once earliest has passed; or
// the timer at next. It returns "" when stop is closed.
func (l *wakeLoop) wait(next, earliest, holdDue time.Time) passReason {
	for {
		now := time.Now()
		if !now.Before(next) {
			return reasonTimer
		}
		if l.w == nil && !now.Before(l.retryAt) {
			l.arm()
		}
		if !now.Before(earliest) {
			if l.restored {
				l.restored = false
				return reasonWatcher
			}
			if !now.Before(holdDue) && l.due() {
				return reasonDue
			}
		}

		wake := next
		if l.w == nil && l.retryAt.Before(wake) {
			wake = l.retryAt
		}
		if earliest.After(now) && earliest.Before(wake) {
			wake = earliest
		}
		timer := time.NewTimer(wake.Sub(now))
		var events <-chan struct{}
		var failed <-chan error
		if l.w != nil {
			events, failed = l.w.events(), l.w.failed()
		}
		select {
		case <-events:
		case err := <-failed:
			l.fail(fmt.Sprintf("the watch on %s ended: %v", l.dir, err))
		case <-timer.C:
		case <-l.stop:
			timer.Stop()
			return ""
		}
		timer.Stop()
	}
}

// arm watches the directory. Watching starts before anything looks for
// due. A failure is said, and arm is tried again after watchRetry; until
// then, passes come from the timer. A watch restored after a failure owes
// one pass, since a mark may have been made where nothing watched.
func (l *wakeLoop) arm() {
	w, err := l.watch(l.dir)
	if err != nil {
		l.fail(fmt.Sprintf("not watching %s: %v", l.dir, err))
		return
	}
	l.w = w
	if l.failure != "" {
		fmt.Fprintf(l.stderr, "integrator: watching %s again\n", l.dir)
		l.failure = ""
		l.restored = true
	}
}

// fail drops the watch, so the next try watches again. The repository's
// lock is not the watch's, and stays held.
func (l *wakeLoop) fail(why string) {
	l.disarm()
	l.retryAt = time.Now().Add(watchRetry)
	if why != l.failure {
		fmt.Fprintf(l.stderr, "integrator: %s; passes come every %s, and the watch is tried again every %s\n",
			why, l.interval, watchRetry)
		l.failure = why
	}
}

func (l *wakeLoop) disarm() {
	if l.w != nil {
		l.w.close()
		l.w = nil
	}
}

// passOutput is a pass's standard output. It opens with the line naming
// why the pass started. A timer pass that says nothing else says nothing
// at all, so an idle controller's journal is not one line per interval.
type passOutput struct {
	w      io.Writer
	header string // the opening line, until it is written
}

func newPassOutput(w io.Writer, reason passReason) *passOutput {
	p := &passOutput{w: w, header: fmt.Sprintf("pass: %s\n", reason)}
	if reason != reasonTimer {
		p.Write(nil)
	}
	return p
}

func (p *passOutput) Write(b []byte) (int, error) {
	if p.header != "" {
		io.WriteString(p.w, p.header)
		p.header = ""
	}
	return p.w.Write(b)
}
