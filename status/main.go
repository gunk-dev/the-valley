// valley-status reads one change's integration status from origin: the
// change's branch head, its integration request, and the integrator's
// last recorded outcome for it. It is `valley status`; bin/valley runs it.
//
// It writes nothing. The objects it reads are fetched without writing
// FETCH_HEAD or moving a branch, so a status read leaves the checkout and
// its working tree exactly as it found them. No check runs and no request
// is filed.
//
// Without flags it prints the sentences an operator reads. With --json it
// prints one object, valley-status/v1, which is a contract front ends
// read; docs/architecture.md documents it. Both are rendered from one
// decision (decide), so the two forms cannot disagree about a change.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"
)

const usage = "usage: valley status [--json] <branch>"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// failure is a read that ends without a status: a message for stderr and
// nothing on stdout. Exit status 1, as bin/valley's die gives.
type failure string

func (f failure) Error() string { return string(f) }

func run(args []string, stdout, stderr io.Writer) int {
	g := git{env: gitEnvironment(os.Environ()), stderr: stderr}
	asJSON, branch, err := parseArgs(args)
	if err == nil && asJSON && !utf8.ValidString(branch) {
		err = failure(branch + ": not UTF-8, so JSON cannot carry the change name")
	}
	var s *status
	if err == nil {
		s, err = read(g, branch)
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	if asJSON {
		return writeJSON(s, stdout, stderr)
	}
	return writeHuman(g, s, stdout)
}

// parseArgs takes one change and an optional --json, in either order. The
// change is the branch name, and an origin/ prefix is dropped once, the
// way a remote-tracking name would be written.
func parseArgs(args []string) (asJSON bool, branch string, err error) {
	var rest []string
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 {
		return false, "", failure(usage)
	}
	return asJSON, strings.TrimPrefix(rest[0], "origin/"), nil
}

// git runs git in the current directory, in the environment bin/valley
// gives every git it runs.
type git struct {
	env    []string
	stderr io.Writer // where git's own diagnostics go, unless quiet
}

// gitEnvironment is the caller's environment with replacement refs off.
// A replacement ref makes one object stand in for another wherever git
// looks it up, so a fetched or planted refs/replace/* could change which
// commit this read takes for a change's head. git is told to ignore them,
// and the variables that point it at other objects or at another
// replacement namespace are dropped. The person's own configuration is
// kept: reaching origin needs it, and it does not change what an object
// id reads as once replacement refs are off.
func gitEnvironment(inherited []string) []string {
	var kept []string
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_GRAFT_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
			continue
		}
		kept = append(kept, kv)
	}
	return append(kept, "GIT_NO_REPLACE_OBJECTS=1")
}

// output runs git and returns what it printed, less trailing newlines, as
// a shell's command substitution reads it. A quiet run discards git's
// diagnostics; any other passes them on.
func (g git) output(quiet bool, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = g.env
	if !quiet {
		cmd.Stderr = g.stderr
	}
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err
}

// succeeds reports whether git exits zero. A failure to run git at all is
// a no as well, as it is to a shell condition.
func (g git) succeeds(quiet bool, args ...string) bool {
	_, err := g.output(quiet, args...)
	return err == nil
}

// exitCode is the status a failed git exited with, for passing it on.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exit.ExitCode()
	}
	return 1
}
