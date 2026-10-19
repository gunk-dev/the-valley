package rebase

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Git runs git in one repository. Every command runs under the context it
// is given, so a cancelled context ends the git it is running, and what
// that git started.
//
// The engine never changes the environment it is given. A caller that
// must not run the repository's hooks or filters says so in Env, as git
// configuration.
type Git struct {
	Dir    string    // the repository's directory; "" for the current one
	Env    []string  // git's environment; nil for this process's own
	Stdout io.Writer // where a rebase prints as it goes; nil discards it
	Stderr io.Writer // where git's own diagnostics go, unless quiet; nil discards them

	// Unattended is git that nobody is at a terminal for, as a service's
	// is. Every command runs in a process group of its own, a command that
	// reaches origin included, so a cancelled context kills what that
	// command started too, such as ssh. A caller that sets it never lets git
	// or ssh ask a question: see command.
	Unattended bool
}

// waitDelay is how long a wait for git's output may go on after git has
// exited, or after a cancelled context has killed it. Something git
// started may still hold the output open.
const waitDelay = 5 * time.Second

// reachesOrigin is the git commands that talk to origin.
var reachesOrigin = map[string]bool{"ls-remote": true, "fetch": true, "push": true}

// command is git with the arguments given, run in g's repository and
// environment under ctx.
//
// A cancelled context kills git and everything git started, such as a
// hook or a filter. Killing git alone is not enough: a hook it started
// can keep git's output open, and a wait for that output would not
// return. So git runs in a process group of its own, and the whole group
// is killed. A wait for git's output is also bounded by waitDelay, for a
// process that has left the group.
//
// A command that reaches origin stays in the caller's process group, and
// is killed alone, unless g is Unattended. A process group of its own is
// not the terminal's foreground group, and a process outside that group
// that reads the terminal is stopped. ssh reads the terminal to ask for a
// passphrase, and git reads it to ask for credentials.
func (g Git) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env, cmd.Dir, cmd.WaitDelay = g.Env, g.Dir, waitDelay
	if g.Unattended || len(args) == 0 || !reachesOrigin[args[0]] {
		ownGroup(cmd)
	}
	return cmd
}

// Command is git with the arguments given, run as the engine runs its
// own: in g's repository and environment, killed with what it started
// once ctx is done, and with the wait for its output bounded. A caller
// that runs git beside the engine runs it this way, so its git is bounded
// as the engine's is.
func (g Git) Command(ctx context.Context, args ...string) *exec.Cmd {
	return g.command(ctx, args...)
}

// tool is a program other than git, such as attest, run in g's
// environment. It never reads the terminal, so it always runs in a
// process group of its own, killed whole once ctx is done, and the wait
// for its output is bounded as git's is.
func (g Git) tool(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.WaitDelay = g.Env, waitDelay
	ownGroup(cmd)
	return cmd
}

// cleanupBound is the longest cleanup may run: removing a worktree,
// aborting its rebase, and deleting the refs that kept a prepared family.
var cleanupBound = time.Minute

// cleanupContext is the context cleanup runs under. It is not cancelled
// with ctx, since cleanup has to run once the caller's work is cancelled
// or past its deadline. It is done after cleanupBound, so cleanup cannot
// hang on a hook or a filter that never returns.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupBound)
}

// finished is the error of git that has ended. git that exits zero has
// succeeded even when the wait for its output was cut short: only
// something git started can still hold the output then.
func finished(err error) error {
	if errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	return err
}

// output runs git and returns what it printed, less trailing newlines, as
// a shell's command substitution reads it. A quiet run discards git's
// diagnostics; any other passes them on.
func (g Git) output(ctx context.Context, quiet bool, args ...string) (string, error) {
	return g.input(ctx, quiet, nil, args...)
}

// input is output with stdin.
func (g Git) input(ctx context.Context, quiet bool, stdin io.Reader, args ...string) (string, error) {
	cmd := g.command(ctx, args...)
	cmd.Stdin = stdin
	if !quiet {
		cmd.Stderr = g.Stderr
	}
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), finished(err)
}

// succeeds reports whether git exits zero. A failure to run git at all is
// a no as well, as it is to a shell condition.
func (g Git) succeeds(ctx context.Context, quiet bool, args ...string) bool {
	_, err := g.output(ctx, quiet, args...)
	return err == nil
}

// passing runs git with its output on Stdout and its diagnostics on
// Stderr.
func (g Git) passing(ctx context.Context, args ...string) error {
	cmd := g.command(ctx, args...)
	cmd.Stdout, cmd.Stderr = g.Stdout, g.Stderr
	return finished(cmd.Run())
}

// in is g run in another directory.
func (g Git) in(dir string) Git {
	g.Dir = dir
	return g
}

// short is an object ID cut to seven digits, as messages name a commit.
func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// IsObjectID reports whether s is a full SHA-1 or SHA-256 object ID,
// lowercase.
func IsObjectID(s string) bool {
	return isHex(s, 40) || isHex(s, 64)
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
