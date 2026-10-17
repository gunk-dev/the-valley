package rebase

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

// Git runs git in one repository. Every command runs under the context it
// is given, so a cancelled context ends the git it is running.
//
// The engine never changes the environment it is given. A caller that
// must not run the repository's hooks or filters says so in Env, as git
// configuration.
type Git struct {
	Dir    string    // the repository's directory; "" for the current one
	Env    []string  // git's environment; nil for this process's own
	Stdout io.Writer // where a rebase prints as it goes; nil discards it
	Stderr io.Writer // where git's own diagnostics go, unless quiet; nil discards them
}

// output runs git and returns what it printed, less trailing newlines, as
// a shell's command substitution reads it. A quiet run discards git's
// diagnostics; any other passes them on.
func (g Git) output(ctx context.Context, quiet bool, args ...string) (string, error) {
	return g.input(ctx, quiet, nil, args...)
}

// input is output with stdin.
func (g Git) input(ctx context.Context, quiet bool, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env, cmd.Dir, cmd.Stdin = g.Env, g.Dir, stdin
	if !quiet {
		cmd.Stderr = g.Stderr
	}
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err
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
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = g.Env, g.Dir, g.Stdout, g.Stderr
	return cmd.Run()
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
