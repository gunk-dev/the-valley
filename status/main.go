// valley is the command line of a valley project: the public verbs
// `valley help` lists, each with its help topic (help.go), and the
// internal verbs the valley's own programs and checks run.
//
// status reads one change's integration status (status.go). changes and
// pending read every record and how far each version has landed
// (inventory.go, pending.go). ready, unready and withdraw write the
// change's record, refs/the-valley/changes/main/<change ID> (ready.go), in
// the format the change module defines and the push hook guards. The
// integrator reads records only to refuse: it lands no request that would
// bring a version of a change that is not ready, and it voids a withdrawn
// change's requests. The integration request stays the only way to main.
// review pages a change or a stack for the operator's answer, and with no
// change walks what needs the operator (review.go, walk.go). approve is
// review's [a]pprove without the pager (approve.go), and rebase its
// [b]ase, which rebases a change's whole stack onto main (rebase.go). knot
// is the plan graph's API (knot.go), which reads and writes a project's
// knots at refs/the-valley/knots, and plan shows that graph as a view
// (plan.go). checks prints which checks a change requires, as the Go
// deriver derives them (checks.go). tail and replay watch the bus and
// fill it from a repository's refs (bus.go). self-update installs the tip
// of a branch of the-valley into the user's nix profile (selfupdate.go),
// and telemetry reads a host's diagnostics and sends the workstation's
// own (telemetry.go).
//
// The internal verbs are in neither the usage nor any help topic. resolve
// turns the name of a change into its record or branch (changes.go), stack
// lists the layers under a change (stack.go), checkline says what review
// shows of a change's checks and their build times (versionchecks.go), and
// style styles review's page for a terminal (style.go): review runs
// checkline in the background while the operator reads, and style in its
// pager. work is one pass of a check worker, which builds and publishes
// the evidence for ready changes before they are approved (worker.go);
// valley-worker runs it.
//
// review reads the rest in process: the knots each change does (does.go),
// where a change stands now (standing.go), whether its answer carries to
// the version the change moved to (carry.go), the version it shows set
// against the change's approved version (compare.go), and the lines
// approve and its prompt print (verdict.go). coverage says which of a
// head's required nix checks already have evidence on origin that approve
// can reuse rather than build (coverage.go).
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

func main() {
	takeEnvironment()
	// A public verb records its command record once it has run
	// (command.go), in the current checkout. A verb a signal ends records
	// it too, through end.
	inv := commandOf(os.Args[1:])
	here := git{env: gitEnvironment(os.Environ()), stderr: io.Discard}
	end := func(status int) {
		inv.record(here, status, commandTimed)
		os.Exit(status)
	}
	end(cli(os.Args[1:], os.Stdout, os.Stderr, end))
}

// versionStamp is the version the package stamped on this valley when nix built
// it (nix/packages.nix): the CLI's version and the commit it was built
// from, or "" for a build the package did not stamp. --version prints it,
// self-update reads which commit is installed from it, and telemetry
// names the producer of its records with it.
var versionStamp string

// takeEnvironment sets up the environment this process, and every program
// it starts, runs in. The stamp is taken from VALLEY_VERSION, which is
// removed, so no program this one starts takes the stamp for its own. git
// ignores replacement refs, whatever configuration says, and loses the
// variables that point it at other objects or at another replacement
// namespace, for the reason gitEnvironment gives.
func takeEnvironment() {
	versionStamp = os.Getenv("VALLEY_VERSION")
	os.Unsetenv("VALLEY_VERSION")
	os.Setenv("GIT_NO_REPLACE_OBJECTS", "1")
	for _, name := range []string{"GIT_REPLACE_REF_BASE", "GIT_GRAFT_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
		os.Unsetenv(name)
	}
}

// commandTimed is whether the command record holds each phase's time.
// A review of one change clears it: its time is the operator's reading,
// which no phase of the program accounts for.
var commandTimed = true

// failure is a verb that ends without doing what it was asked: a message
// for stderr and nothing on stdout. Exit status 1.
type failure string

func (f failure) Error() string { return string(f) }

// cli runs the valley command line args. No verb at all is help. A public
// verb given one argument, -h or --help, prints its help topic before
// anything reads its arguments. pending and rebase read origin's branches
// through this checkout's remote-tracking refs, so they fetch origin
// first, and a fetch that fails exits with git's status. end ends the
// process from a verb a signal interrupts, as run's does.
func cli(args []string, stdout, stderr io.Writer, end func(int)) int {
	verb := "help"
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}
	if publicVerb(verb) && len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return runHelp([]string{verb}, stdout, stderr)
	}
	g := git{env: gitEnvironment(os.Environ()), stderr: stderr}
	switch verb {
	case "help", "-h", "--help":
		return runHelp(args, stdout, stderr)
	case "--version":
		v := versionStamp
		if v == "" {
			v = "unstamped (built without the package's version stamp)"
		}
		fmt.Fprintf(stdout, "valley %s\n", v)
		return 0
	case "tail":
		return runTail(args, stderr)
	case "replay":
		return runReplay(g, args, stdout, stderr)
	case "pending", "rebase":
		if verb == "rebase" {
			var err error
			if args, err = rebaseCommandArgs(args); err != nil {
				fmt.Fprintf(stderr, "valley: %s\n", err)
				return 1
			}
		}
		fetched := phase("fetch")
		err := fetchOrigin(g, stdout, stderr)
		fetched()
		if err != nil {
			return exitCode(err)
		}
	}
	return run(append([]string{verb}, args...), stdout, stderr, end)
}

// run runs the verb args name. end ends the process from a verb a signal
// interrupts, once the verb has cleaned up; it does not return.
func run(args []string, stdout, stderr io.Writer, end func(int)) int {
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
	case "checkline":
		err = runCheckLine(g, args, stdout, stderr)
	case "rebase":
		return runRebase(g, args, stdout, stderr)
	case "style":
		err = runStyle(args, os.Stdin, stdout)
	case "checks":
		err = runChecks(g, args, stdout)
	case "approve":
		return runApprove(g, args, stdout, stderr, end)
	case "changes":
		err = runChanges(g, args, stdout, stderr)
	case "pending":
		err = runPending(g, args, stdout, stderr)
	case "review":
		return runReview(g, args, stdout, stderr, end)
	case "telemetry":
		err = runTelemetry(g, args, stdout, stderr)
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
			var unclaimed claimFailure
			if errors.As(err, &unclaimed) {
				fmt.Fprintf(stderr, "valley: %s\n", err)
				return claimFailedStatus
			}
		}
	default:
		stderr = droppable(stderr)
		fmt.Fprintf(stderr, "valley: unknown verb: %s\n\n", verb)
		io.WriteString(stderr, helpPart("usage"))
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	return 0
}

// git runs git in a repository, in the environment valley gives every git
// it runs.
type git struct {
	env    []string
	stderr io.Writer // where git's own diagnostics go, unless quiet
	dir    string    // the repository's directory; "" for the current one
}

// engine is g as the rebase engine runs git: in the same repository and
// environment, with git's own diagnostics on the same stderr.
func (g git) engine() rebase.Git {
	return rebase.Git{Dir: g.dir, Env: g.env, Stderr: g.stderr, Phase: phase, Started: processStarted}
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

// command is git run with args in g's repository and environment.
func (g git) command(args ...string) *exec.Cmd { return g.program("git", args...) }

// program is the program name run with args in g's repository directory
// and environment, the current directory's when g names none.
func (g git) program(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env, cmd.Dir = g.env, g.dir
	return cmd
}

// output runs git and returns what it printed, less trailing newlines, as
// a shell's command substitution reads it. A quiet run discards git's
// diagnostics; any other passes them on.
func (g git) output(quiet bool, args ...string) (string, error) {
	return g.input(quiet, nil, args...)
}

// input is output with stdin.
func (g git) input(quiet bool, stdin io.Reader, args ...string) (string, error) {
	processStarted()
	cmd := g.command(args...)
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
