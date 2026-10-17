// valley-go is the part of the valley command written in Go: the verbs
// that read a change's state from origin, and the verbs that write its
// change record there. bin/valley runs it for status, ready, unready and
// withdraw, for changes and pending, which list every change across the
// repositories a host serves, and for resolve, which turns the name of a
// change into its branch for the verbs bin/valley runs itself, for
// stack, which lists the layers under a change for review (stack.go), for
// rebase, which rebases a change's whole stack onto main, and brings, which
// lists the changes a head would bring that have not landed, for approve
// (rebase.go), for knot, the plan graph's API (knot.go), which reads and
// writes a project's knots at refs/the-valley/knots, and for plan, which
// shows that graph as a view (plan.go).
//
// status reads one change's integration status (status.go). changes and
// pending read every record and how far each version has landed
// (inventory.go, pending.go). ready,
// unready and withdraw write the change's record,
// refs/the-valley/changes/main/<change ID> (ready.go), in the format the
// change module defines and the push hook guards. The integrator reads
// records only to refuse: it lands no request that would bring a version
// of a change that is not ready, and it voids a withdrawn change's
// requests. The integration request stays the only way
// to main. versions lists a change's versions, for approve.
//
// A change is named by its full change ID, by its handle (the shortest
// prefix of the ID, four letters at least, that no other change on origin
// shares), or by its branch (changes.go).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const usage = `usage: valley-go status [--json] <change>
       valley-go ready [--head <sha>] [--change <id>] [--if-active <sha>] [--stamp] [<branch>]
       valley-go unready <change>
       valley-go withdraw <change>
       valley-go resolve <change>
       valley-go stack <change>
       valley-go rebase [--head <sha>] <change>
       valley-go brings [--change <id>] <head>
       valley-go versions <change ID>
       valley-go changes --json [--all] [--repo <name>]
       valley-go pending [--json] [--all] [--repo <name>] [--checks]
       valley-go knot <verb> [args]   (valley-go knot for its verbs)
       valley-go plan [--project <name> | --all] [--owner <name>] [--json]`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// failure is a verb that ends without doing what it was asked: a message
// for stderr and nothing on stdout. Exit status 1, as bin/valley's die
// gives.
type failure string

func (f failure) Error() string { return string(f) }

func run(args []string, stdout, stderr io.Writer) int {
	g := git{env: gitEnvironment(os.Environ()), stderr: stderr}
	verb := ""
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	var err error
	switch verb {
	case "status":
		return runStatus(g, args, stdout, stderr)
	case "ready":
		err = runReady(g, args, stdout)
	case "unready":
		err = runUnready(g, args, stdout, stderr)
	case "withdraw":
		err = runWithdraw(g, args, stdout, stderr)
	case "resolve":
		err = runResolve(g, args, stdout)
	case "stack":
		err = runStack(g, args, stdout)
	case "rebase":
		return runRebase(g, args, stdout, stderr)
	case "brings":
		err = runBrings(g, args, stdout)
	case "versions":
		err = runVersions(g, args, stdout)
	case "changes":
		err = runChanges(g, args, stdout, stderr)
	case "pending":
		err = runPending(g, args, stdout, stderr)
	case "knot", "plan":
		if _, err := clockSetting(); err != nil {
			fmt.Fprintf(stderr, "valley: %s\n", err)
			return 2
		}
		if verb == "knot" {
			return runKnot(g, args, os.Stdin, stdout, stderr)
		}
		err = runPlan(g, args, stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	return 0
}

// git runs git in a repository, in the environment bin/valley gives every
// git it runs.
type git struct {
	env    []string
	stderr io.Writer // where git's own diagnostics go, unless quiet
	dir    string    // the repository's directory; "" for the current one
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
	return g.input(quiet, nil, args...)
}

// input is output with stdin.
func (g git) input(quiet bool, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = g.env
	cmd.Dir = g.dir
	cmd.Stdin = stdin
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

// clock is the time the knot and plan verbs take as now: the time
// VALLEY_NOW holds, when it is set, or else the system's clock. A knot's
// created date, a claim's time and a comment's stamp are written at it,
// and a claim's age is read against it. Checks set it, so the dates a
// graph records and the ages a view shows are the same on every run. It
// grants nothing: the push hook holds none of those times to its own
// clock, so a writer could record any of them by writing the file.
func clock() time.Time {
	if t, err := clockSetting(); err == nil && !t.IsZero() {
		return t
	}
	return time.Now().UTC()
}

// clockSetting is the time VALLEY_NOW holds, in UTC, or the zero time when
// it is unset. A value that is no RFC 3339 time is refused.
func clockSetting() (time.Time, error) {
	v := os.Getenv("VALLEY_NOW")
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, failure(fmt.Sprintf("VALLEY_NOW is %q, and it takes an RFC 3339 time, such as 2026-10-15T09:30:00Z", v))
	}
	return t.UTC(), nil
}

// exitCode is the status a failed git exited with, for passing it on.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exit.ExitCode()
	}
	return 1
}
