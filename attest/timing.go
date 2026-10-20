package main

// How long each check took, and where that is written down.
//
// A timing is not evidence. It is never signed, never stored at a ref and
// never pushed: run prints one line per check, and with --timings it also
// appends one JSON line per check to a log file on this machine. The log
// is the start of a record of where time goes, and nothing reads it to
// decide anything. So a log that cannot be written is a warning and never
// a failed run.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// How a check was satisfied, as far as run can tell.
const (
	// satisfiedRan: the command ran, or nix built or fetched the output.
	satisfiedRan = "ran"
	// satisfiedCached: the output was already in the local nix store
	// before the build, so nix built nothing.
	satisfiedCached = "cached"
	// satisfiedUnknown: the nix store could not be asked whether the
	// output was already there.
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
	DurationMS int64  `json:"duration_ms"`
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
	if err := appendLine(l.path, t); err != nil {
		l.failed = true
		fmt.Fprintf(l.warn, "attest: warning: timings not logged: %v\n", err)
	}
}

// appendLine writes v as one JSON line at the end of the file at path,
// creating the file and its directory when they are missing. The line is
// one write to a file opened for appending, so two runs logging at once
// do not interleave within a line.
func appendLine(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
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

// repositoryName names the repository a run is in, for the log: the URL of
// its origin remote with any user name or password removed, or, with no
// origin, the directory of its main working tree. run is usually given a
// throwaway worktree, so the worktree's own directory names nothing.
func repositoryName(root string) string {
	if origin, err := gitLine(root, "config", "--get", "remote.origin.url"); err == nil && origin != "" {
		return withoutUserinfo(origin)
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

// withoutUserinfo drops the user name and password from a URL, since an
// https remote can carry a token there. Anything that is not a URL with a
// scheme, such as a path or user@host:path, is returned as it is.
func withoutUserinfo(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" || u.User == nil {
		return remote
	}
	u.User = nil
	return u.String()
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

// outputsCached says whether every output of a derivation is already in
// the local nix store, so that building it does nothing. It asks before
// the build, which is the only time the answer means anything.
func outputsCached(drv string) string {
	outputs, _, err := nixStore("--query", "--outputs", drv)
	if err != nil {
		return satisfiedUnknown
	}
	paths := strings.Fields(outputs)
	if len(paths) == 0 {
		return satisfiedUnknown
	}
	invalid, _, err := nixStore(append([]string{"--check-validity", "--print-invalid"}, paths...)...)
	if err != nil {
		return satisfiedUnknown
	}
	if strings.TrimSpace(invalid) != "" {
		return satisfiedRan
	}
	return satisfiedCached
}
