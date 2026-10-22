package main

// How long each check took, and where that is written down.
//
// A timing is not evidence. It is never signed, never stored at a ref and
// never pushed: run prints one line per check, and with --timings it also
// appends one JSON line per check to a log file on this machine. The log
// is the start of a record of where time goes, and nothing reads it to
// decide anything. So a log that cannot be written is a warning and never
// a failed run.
//
// Nor may timing hold a run up. Every piece of timing output is given
// timingDeadline to finish, and is dropped when it fails or takes longer:
// the time line, the warning, and the line appended to the log.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// How a check was satisfied, as far as run can tell.
const (
	// satisfiedRan: the command ran, or nix built or fetched the output.
	satisfiedRan = "ran"
	// satisfiedCached: the output was already in the local nix store
	// before the build, so nix built nothing.
	satisfiedCached = "cached"
	// satisfiedUnknown: the nix store was not asked whether the output
	// was already there, because the run has no --timings, or it could
	// not be asked, or it did not answer within probeDeadline.
	satisfiedUnknown = "unknown"
)

// checkTiming is one line of the timing log: one check, run once.
type checkTiming struct {
	Time       string `json:"time"` // when the check started, UTC
	Repository string `json:"repository"`
	Change     string `json:"change,omitempty"`
	Version    int    `json:"version,omitempty"`
	Head       string `json:"head"`
	Check      string `json:"check"`
	Runner     string `json:"runner"`
	Cost       string `json:"cost,omitempty"`
	Result     string `json:"result"` // passed, failed, or error
	Satisfied  string `json:"satisfied"`
	// DurationMS is the check's wall time. Pure checks are built together
	// in one nix build, so for a pure check that evaluated it is the wall
	// time of the whole nix stage: evaluation, build, and deciding every
	// outcome. EvalMS is how long evaluating the check took, and a command
	// check has none.
	DurationMS int64 `json:"duration_ms"`
	EvalMS     int64 `json:"eval_ms,omitempty"`
}

// timingsPath is the timing log: $XDG_STATE_HOME/valley/timings.jsonl,
// where an unset or relative XDG_STATE_HOME means ~/.local/state, as the
// XDG base directory specification says.
func timingsPath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "valley", "timings.jsonl"), nil
}

// timingLog appends check timings to the log. Its zero value logs nothing.
// After one write fails it warns once and stops trying, so a log that
// cannot be written costs one line of output and not one per check.
type timingLog struct {
	path   string
	warn   io.Writer
	failed bool
}

func (l *timingLog) append(t checkTiming) {
	if l.path == "" || l.failed {
		return
	}
	if err := bounded(func() error { return appendLine(l.path, t) }); err != nil {
		l.failed = true
		fmt.Fprintf(l.warn, "attest: warning: timings not logged: %v\n", err)
	}
}

// appendLine writes v as one JSON line at the end of the file at path,
// creating the file and its directory when they are missing. The line is
// one write to a file opened for appending, so two runs logging at once
// do not interleave within a line.
//
// The log is a regular file and nothing else. Whatever else is at its path
// is refused before it is opened, and again after, since the path can
// change in between. The open does not wait: opening a FIFO to write would
// wait for a reader, which may never come. The file and its directory are
// created readable by their owner alone, and a log others can read, such
// as one an earlier attest created, is made so before a line is added.
func appendLine(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return fmt.Errorf("%s is not a regular file", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		if err := f.Chmod(perm &^ 0o077); err != nil {
			f.Close()
			return err
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// timingDeadline is how long one piece of timing output may take.
const timingDeadline = time.Second

// errDropped is what timing output that was dropped for taking too long
// says.
var errDropped = errors.New("dropped: not written within " + timingDeadline.String())

// bounded runs f and waits at most timingDeadline for it. After that it
// returns errDropped, and f is left to finish or not on its own.
func bounded(f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	timer := time.NewTimer(timingDeadline)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errDropped
	}
}

// droppable writes timing output that may be lost. Each write is bounded,
// and one that fails or takes too long is dropped. While a write is still
// stuck, every later one is dropped at once rather than queued behind it.
type droppable struct {
	w    io.Writer
	busy chan struct{} // holds a token while a write is in flight
}

func newDroppable(w io.Writer) *droppable {
	return &droppable{w: w, busy: make(chan struct{}, 1)}
}

func (d *droppable) Write(p []byte) (int, error) {
	select {
	case d.busy <- struct{}{}:
	default:
		return 0, errDropped
	}
	line := bytes.Clone(p)
	err := bounded(func() error {
		defer func() { <-d.busy }()
		_, err := d.w.Write(line)
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// timingStdout and timingStderr are where run prints its timing: the time
// lines, and the warning that the log was not written.
var (
	timingStdout = sync.OnceValue(func() io.Writer { return newDroppable(duplicate(syscall.Stdout, "stdout")) })
	timingStderr = sync.OnceValue(func() io.Writer { return newDroppable(duplicate(syscall.Stderr, "stderr")) })
)

// duplicate duplicates the descriptor of standard output or standard
// error. Go ends a process whose write to its own standard output or error
// fails because nobody reads it, as SIGPIPE would. Through a duplicate,
// the same write fails with EPIPE and nothing more. A stream that cannot
// be duplicated gets no timing output.
func duplicate(fd int, name string) io.Writer {
	syscall.ForkLock.RLock()
	dup, err := syscall.Dup(fd)
	if err == nil {
		syscall.CloseOnExec(dup)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return io.Discard
	}
	return os.NewFile(uintptr(dup), name)
}

// timingLine is what run prints after each check: its name, its result,
// how long it took and how it was satisfied.
func timingLine(t checkTiming, took time.Duration) string {
	return fmt.Sprintf("time    %-24s %-8s %s (%s)", t.Check, t.Result, shortDuration(took), t.Satisfied)
}

// shortDuration rounds a duration to what a person reading it wants:
// milliseconds under a second, tenths of a second above.
func shortDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// repositoryName names the repository a run is in, for the log: its origin
// remote, as remoteIdentifier reduces it, or, with no origin or one that
// does not reduce, the directory of its main working tree. run is usually
// given a throwaway worktree, so the worktree's own directory names nothing.
func repositoryName(root string) string {
	if origin, err := gitLine(root, "config", "--get", "remote.origin.url"); err == nil && origin != "" {
		if name := remoteIdentifier(origin); name != "" {
			return name
		}
	}
	common, err := gitLine(root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return root
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// scpLike is git's other way to write an ssh remote, [user@]host:path, in
// the shapes that cannot hold a password, a query or a second colon.
var scpLike = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+@)?([A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\]):([^:@?#]+)$`)

// remoteIdentifier reduces a remote to what names its repository, since a
// remote can carry a token. A URL keeps its scheme, host and path, and
// loses its user name, password, query and fragment. The scp-like form
// keeps its host and path. A local path, which has no colon before its
// first slash, is kept as it is. Anything else is withheld: it returns "".
func remoteIdentifier(remote string) string {
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Scheme == "" || u.Opaque != "" {
			return ""
		}
		return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
	}
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		return m[1] + ":" + m[2]
	}
	colon, slash := strings.IndexByte(remote, ':'), strings.IndexByte(remote, '/')
	if colon < 0 || (slash >= 0 && slash < colon) {
		return remote
	}
	return ""
}

// costClasses reads the --cost flags: NAME=CLASS, the cost class the
// policy declares for each check.
func costClasses(costs repeatable) (map[string]string, error) {
	classes := map[string]string{}
	for _, c := range costs {
		name, class, found := strings.Cut(c, "=")
		if !found || name == "" {
			return nil, fmt.Errorf("--cost %q: expected NAME=CLASS", c)
		}
		classes[name] = class
	}
	return classes, nil
}

// probeDeadline bounds asking the nix store about a check's outputs. The
// answer only labels a timing, so a store slow to give it gets "unknown".
var probeDeadline = 5 * time.Second

// outputsCached says whether every output of a derivation is already in
// the local nix store, so that building it does nothing. It asks before
// the build, which is the only time the answer means anything, and only
// for a run with --timings.
func outputsCached(drv string) string {
	ctx, cancel := context.WithTimeout(context.Background(), probeDeadline)
	defer cancel()
	_, present, err := storeOutputs(ctx, drv)
	switch {
	case err != nil:
		return satisfiedUnknown
	case present:
		return satisfiedCached
	default:
		return satisfiedRan
	}
}
