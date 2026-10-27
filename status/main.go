// valley-go is the part of the valley command written in Go: the verbs
// that read a change's state from origin, and the verbs that write its
// change record there. bin/valley runs it for status, ready, unready and
// withdraw, for changes and pending, which list every change across the
// repositories a host serves, and for resolve, which turns the name of a
// change into its branch for the verbs bin/valley runs itself, for
// stack, which lists the layers under a change for review (stack.go), for
// rebase, which rebases a change's whole stack onto main, and brings, which
// lists the changes a head would bring that have not landed, for approve
// (rebase.go), for does, which lists the knots each change does, for review
// (does.go), for knot, the plan graph's API (knot.go), which reads and
// writes a project's knots at refs/the-valley/knots, for plan, which
// shows that graph as a view (plan.go), for compare, which sets a
// version review shows against the change's approved version
// (compare.go), and for self-update, which installs the tip of a branch of
// the-valley into the user's nix profile (selfupdate.go). checks prints
// which checks a change requires, as the Go deriver derives them
// (checks.go); no verb of bin/valley runs it. coverage says which of a
// head's required nix checks already have evidence on origin that approve
// can reuse rather than build (coverage.go). work is one pass of a check
// worker, which builds and publishes that evidence for ready changes
// before they are approved (worker.go); no verb of bin/valley runs it.
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

	"the-valley/rebase"
)

const usage = `usage: valley-go status [--json] [-v] <change>
       valley-go ready [--head <sha>] [--change <id>] [--if-active <sha>] [--stamp] [<branch>]
       valley-go unready <change>
       valley-go withdraw <change>
       valley-go resolve <change>
       valley-go stack <change>
       valley-go does [-v] <change ID>...
       valley-go rebase [--head <sha>] <change>
       valley-go brings [--change <id>] <head>
       valley-go versions <change ID>
       valley-go compare --tip <commit> <change ID> <version> <head>
       valley-go checks [--schema <file>] --instance <dir> --project <dir> <base> <head>
       valley-go coverage --head <sha> [--check <name>=<attribute>]...
       valley-go work --repo <name>... --key <file> [--name <name>] [--state <dir>]
       valley-go changes --json [--all] [--repo <name>] [-v]
       valley-go pending [--json] [--all] [--repo <name>] [--checks] [-v]
       valley-go knot <verb> [args]   (valley-go knot for its verbs)
       valley-go plan [--project <name> | --all] [--owner <name>] [--json]
       valley-go self-update [-v] [--retry | --rollback | --pin | --unpin] [--repo <url>] [--branch <name>]

status, stack, rebase, brings, changes, pending and plan read how far a
version has landed, and also take --known-signers <file> and
--integrator-name <name>: what an integrator's transfer statement is
verified with, ahead of VALLEY_KNOWN_SIGNERS, VALLEY_INTEGRATOR_SIGNER and
what the serving host publishes at refs/the-valley/signers.`

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
	// What this run verifies a transfer statement with (signers.go). Only
	// the verbs that read how far a version has landed take the flags.
	verification = &signerSource{g: g, stderr: stderr}
	defer verification.done()
	if signerVerbs[verb] {
		var err error
		if args, verification.keys, verification.name, err = takeSignerFlags(args); err != nil {
			fmt.Fprintf(stderr, "valley: %s\n", err)
			return 1
		}
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
	case "does":
		err = runDoes(g, args, stdout, stderr)
	case "rebase":
		return runRebase(g, args, stdout, stderr)
	case "brings":
		err = runBrings(g, args, stdout)
	case "versions":
		err = runVersions(g, args, stdout)
	case "compare":
		err = runCompare(g, args, stdout)
	case "checks":
		err = runChecks(g, args, stdout)
	case "coverage":
		err = runCoverage(g, args, stdout, stderr)
	case "changes":
		err = runChanges(g, args, stdout, stderr)
	case "pending":
		err = runPending(g, args, stdout, stderr)
	case "self-update":
		return runSelfUpdate(args, stdout, stderr)
	case "knot", "plan", "work":
		if _, err := clockSetting(); err != nil {
			fmt.Fprintf(stderr, "valley: %s\n", err)
			return 2
		}
		switch verb {
		case "knot":
			return runKnot(g, args, os.Stdin, stdout, stderr)
		case "plan":
			err = runPlan(g, args, stdout, stderr)
		default:
			err = runWork(g, args, stdout, stderr)
		}
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

// engine is g as the rebase engine runs git: in the same repository and
// environment, with git's own diagnostics on the same stderr.
func (g git) engine() rebase.Git {
	return rebase.Git{Dir: g.dir, Env: g.env, Stderr: g.stderr}
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

// ref reads the object ID that name, a full ref name, holds. It reads that
// one ref, with git show-ref. ok is false when there is no ref of that
// name. An error is a repository git could not read.
//
// git rev-parse is not used, because it does not stop at the name it is
// given. For a missing refs/heads/topic, it goes on to try other names,
// refs/heads/refs/heads/topic among them.
func (g git) ref(name string) (oid string, ok bool, err error) {
	// show-ref --exists exits 2 for a ref that is missing, and with
	// another status for a failure to read.
	_, err = g.output(true, "show-ref", "--exists", "--end-of-options", name)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	oid, err = g.output(true, "show-ref", "--verify", "--hash", "--end-of-options", name)
	if err != nil {
		return "", false, err
	}
	return oid, true, nil
}

// clock is the time the knot and plan verbs take as now: the time
// VALLEY_NOW holds, when it is set, or else the system's clock. A knot's
// created date, a claim's time and a comment's stamp are written at it,
// and a claim's age is read against it. Checks set it, so the dates a
// graph records and the ages a view shows are the same on every run. It
// grants nothing: the push hook holds none of those times to its own
// clock, so a writer could record any of them by writing the file. The
// check worker measures its waits after a failed batch from it too
// (worker.go).
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
