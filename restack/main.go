// valley-restacker rebases ready changes onto main when main moves under
// them, as its own principal, so their evidence stays fresh without their
// owners rebasing and approving again by hand.
//
// `valley-restacker run` reconciles at its start and then every
// --interval, 15 seconds by default, until it is stopped. Each
// reconciliation is one write pass. `valley-restacker once` is one write
// pass on its own.
//
// A write pass first finishes the journal of earlier operations, if the
// state directory holds one (publish.go). It then reads one snapshot of
// origin, plans every family, builds the writes it would make for each
// family it would restack or hold (build.go), adds them to the journal,
// and makes them the way recovery would after a crash: the branch push,
// then the versions, then the reports, reading origin again between each
// (recover.go). A family the journal still holds, such as one whose
// versions a request has paused, stays there, and each family that
// shares a change or a branch with it waits: the pass builds nothing for
// it. So does each family that shares a change or a branch with another
// family the pass would build for, as two changes that name one branch
// do (overlapping). Every other family is planned as usual.
//
// `valley-restacker once --report-only` reads one snapshot of origin and
// says which families it would restack, which it would hold and why, and
// what capacity allows. For each family it would restack or hold, it
// builds every write a pass would make, in its scratch repository, and
// shows them: the one branch push, each change's next version and each
// recorded change's report. It decides the journal of earlier
// operations, if the state directory holds one, as recovery would. It
// pushes nothing, and writes no journal.
//
// It talks to origin only over git, as a client, from a scratch repository
// under its state directory. Hooks and external filters are off there:
// the scratch repository's hooks path is /dev/null, and git reads neither
// the system's nor the user's configuration, nor any the environment
// injects, so no filter driver is defined. No git variable that points git
// at another repository, program or configuration reaches anything a pass
// runs (passes). A service that needs ssh settings passes them in
// GIT_SSH_COMMAND. The scratch repository commits under the identity its
// flags give, and never under one inherited from the environment, whether
// as an identity variable or as configuration.
//
// A pass holds a lock on its state directory from start to end, so a
// second pass on the same directory fails while one runs; run holds it
// for as long as it runs. The directory belongs to one repository, target
// and principal, the first a pass used it for, and a pass for any other
// is refused. Every git, cue and attest a pass runs is killed, with what
// it started, once the pass's --timeout passes. A pass whose output
// nobody reads ends then too, and lets go of the lock. A message of its
// own that nobody reads, such as the error it ends with, is given up on
// after a few seconds.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"the-valley/change"
	"the-valley/composed"
	"the-valley/note"
	"the-valley/rebase"
)

const usage = `valley-restacker: rebase ready changes onto main when main moves.

usage: valley-restacker run [--interval DURATION] [flags]
       valley-restacker once [--report-only] [flags]

  run   reconcile at once and then every --interval until stopped: each
        reconciliation is one write pass. When a pass fails, write the
        error to stderr and double the wait before the next pass, up to
        5 minutes, until a pass succeeds
  once  one write pass: settle any open journal in the state directory,
        then plan every family, append the writes for each family to
        restack or hold to the journal, and push them in order: the
        branch push, each change's next version, and each change's
        report, reading origin again before each push. Any family still
        in the journal stays there, and any family that shares a change
        or branch with it, or with another family in the same pass, waits
  once --report-only
        read one snapshot of origin and print what a pass would do: each
        family, restack or hold with the reason, its capacity, the
        integration requests it considered, and the writes a pass would
        make for it, built in the scratch repository without pushing: the
        branch push, each change's next version, and each change's
        report. If the state directory holds a journal, print what
        recovery decides for each write it names.
        Pushes nothing, and writes no journal.

flags:
  --repo URL            origin: the repository the restacker serves (required)
  --target NAME         the target changes are rebased onto (only main)
  --state DIR           where the scratch repository lives (required). A pass
                        locks it, and it belongs to the first repository,
                        target and principal a pass used it for
  --principal NAME      the principal the restacker pushes as (required)
  --commit-name NAME    the name the restacker commits under (default: the
                        principal)
  --commit-email EMAIL  the email it commits under
                        (default: restacker@the-valley.invalid)
  --interval DURATION   run: how long to wait after one pass before the next
                        (default 15s)
  --timeout DURATION    the longest a pass may run, writing its output
                        included; what it runs is killed then (default 10m)
  --known-signers FILE  verifier keys, for landings proved by integrator evidence
  --integrator-name N   the name the integrator signs under
  --signer-projects F   the signer projects file the integrator reads; while
                        it names the --integrator-name, no landing is proved
                        by integrator evidence. A file that does not read
                        stops the pass
  --attest PATH         the attest helper that checks a note (default: attest on PATH)
  --instance-repo URL   the repository carrying the group's floor
  --instance-ref REF    the ref whose tip carries the floor, a full ref name
                        read by that exact name (refs/heads/main)
  --instance-policy DIR the floor's directory in that tip (policy/instance)
  --instance DIR        a materialized floor, for a host that serves none
  --project-policy DIR  the project layer, in the target tip (policy/project)
  --schema FILE         the verification schema (VALLEY_VERIFICATION_SCHEMA)
  --json                print each pass as JSON
`

func main() {
	if len(os.Args) < 2 {
		say(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "once", "run":
		// An interrupt or a stop ends the pass as its timeout does: what it
		// runs is killed, and the lock on its state is let go. It ends run
		// between passes too.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		command := once
		if os.Args[1] == "run" {
			command = loop
		}
		err := command(ctx, os.Args[2:], os.Stdout, os.Stderr)
		stop()
		if err != nil {
			say(os.Stderr, fmt.Sprintf("valley-restacker: %v\n", err))
			os.Exit(1)
		}
	case "help", "-h", "--help":
		say(os.Stdout, usage)
	default:
		say(os.Stderr, fmt.Sprintf("valley-restacker: unknown command %q\n\n%s", os.Args[1], usage))
		os.Exit(2)
	}
}

// sayBound is the longest the process waits to write a message of its
// own, such as the error it ends with.
const sayBound = 5 * time.Second

// say writes a message of the process's own, and gives up after sayBound.
// A stderr nobody reads would otherwise hold the process after its pass
// has ended and let go of its lock. The write given up on ends with the
// process.
func say(w io.Writer, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), sayBound)
	defer cancel()
	_ = writeOut(ctx, w, []byte(message))
}

// config is a pass's configuration, from flags.
type config struct {
	repo, target, state, principal string
	commitName, commitEmail        string
	timeout, interval              time.Duration
	verify                         rebase.Verifier
	floor                          composed.Floor
	instanceRepo, projectPolicy    string
	schema, cue                    string
	json                           bool
}

// defaultEmail is the email the restacker commits under unless
// --commit-email names another. The .invalid domain is reserved, so the
// address names no mailbox, as the integrator's does not.
const defaultEmail = "restacker@the-valley.invalid"

// maxBackoff is the longest run waits after a pass that failed.
const maxBackoff = 5 * time.Minute

// parse reads a command's flags into a configuration. --report-only is
// once's, and --interval is run's.
func parse(name string, args []string, errOut io.Writer) (c config, reportOnly bool, err error) {
	fs := flag.NewFlagSet("valley-restacker "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { say(errOut, usage) }
	var only *bool
	if name == "once" {
		only = fs.Bool("report-only", false, "")
	} else {
		fs.DurationVar(&c.interval, "interval", 15*time.Second, "")
	}
	fs.StringVar(&c.repo, "repo", "", "")
	fs.StringVar(&c.target, "target", rebase.Target, "")
	fs.StringVar(&c.state, "state", "", "")
	fs.StringVar(&c.principal, "principal", "", "")
	fs.StringVar(&c.commitName, "commit-name", "", "")
	fs.StringVar(&c.commitEmail, "commit-email", defaultEmail, "")
	fs.DurationVar(&c.timeout, "timeout", 10*time.Minute, "")
	fs.StringVar(&c.verify.Keys, "known-signers", "", "")
	fs.StringVar(&c.verify.Signer, "integrator-name", "", "")
	fs.StringVar(&c.verify.Attest, "attest", "", "")
	signerProjects := fs.String("signer-projects", "", "")
	fs.StringVar(&c.instanceRepo, "instance-repo", "", "")
	fs.StringVar(&c.floor.Ref, "instance-ref", "refs/heads/main", "")
	fs.StringVar(&c.floor.Dir, "instance-policy", "policy/instance", "")
	fs.StringVar(&c.floor.Path, "instance", "", "")
	fs.StringVar(&c.projectPolicy, "project-policy", "policy/project", "")
	fs.StringVar(&c.schema, "schema", os.Getenv("VALLEY_VERIFICATION_SCHEMA"), "")
	fs.BoolVar(&c.json, "json", false, "")
	if err := fs.Parse(args); err != nil {
		return c, false, err
	}
	if only != nil {
		reportOnly = *only
	}
	switch {
	case fs.NArg() > 0:
		return c, false, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case c.target != rebase.Target:
		return c, false, fmt.Errorf("--target %s: a change may name only %s as its target", c.target, rebase.Target)
	case name == "run" && c.interval <= 0:
		return c, false, fmt.Errorf("--interval %v: run waits some time between passes", c.interval)
	}
	for _, f := range []struct{ flag, value string }{
		{"--repo", c.repo}, {"--state", c.state}, {"--principal", c.principal}, {"--schema", c.schema},
	} {
		if f.value == "" {
			return c, false, fmt.Errorf("%s is required", f.flag)
		}
	}
	if !change.ValidPrincipal(c.principal) {
		return c, false, fmt.Errorf("--principal %q is not a principal", c.principal)
	}
	if c.commitName == "" {
		c.commitName = c.principal
	}
	if err := checkIdentity(c.commitName, c.commitEmail); err != nil {
		return c, false, err
	}
	if c.timeout <= 0 {
		return c, false, fmt.Errorf("--timeout %v: a pass needs some time to run", c.timeout)
	}
	// The floor comes from the instance repository's tip, or from a
	// directory on disk, and never from both, as for the integrator.
	if (c.instanceRepo == "") == (c.floor.Path == "") {
		return c, false, errors.New("exactly one of --instance-repo and --instance is required: the floor is read from the instance repository's tip, or from a materialized directory")
	}
	if c.instanceRepo != "" && !strings.HasPrefix(c.floor.Ref, "refs/") {
		return c, false, fmt.Errorf("--instance-ref %s is not a full ref name; name it whole, as refs/heads/main", c.floor.Ref)
	}
	if c.verify.Attest == "" {
		c.verify.Attest, _ = exec.LookPath("attest")
	}
	if *signerProjects != "" {
		data, err := os.ReadFile(*signerProjects)
		if err != nil {
			return c, false, fmt.Errorf("--signer-projects: %w", err)
		}
		if c.verify.SignerProjects, err = note.ParseSignerProjects(data); err != nil {
			return c, false, fmt.Errorf("--signer-projects %s does not read: %w", *signerProjects, err)
		}
	}
	if c.cue, err = exec.LookPath("cue"); err != nil {
		return c, false, errors.New("cue is not on the path; the restacker composes the policy with it")
	}
	return c, reportOnly, nil
}

// once is one pass, a write pass unless --report-only says otherwise.
func once(ctx context.Context, args []string, out, errOut io.Writer) error {
	// What the pass runs inherits no git variable that points git
	// elsewhere or gives it configuration (passes).
	controlEnvironment()
	c, reportOnly, err := parse("once", args, errOut)
	if err != nil {
		return err
	}
	release, err := lockState(c.state)
	if err != nil {
		return err
	}
	defer release()
	if err := bindState(c.state, c.binding()); err != nil {
		return err
	}
	_, err = pass(ctx, c, reportOnly, out, errOut, false)
	return err
}

// loop is run: a write pass at once, and another every interval after
// the last one ended, until ctx is done. It holds the lock on its state
// for as long as it runs. A pass that fails is said, and the wait before
// the next doubles, up to maxBackoff, until a pass succeeds. A pass that
// pushed nothing and leaves no journal open prints nothing.
func loop(ctx context.Context, args []string, out, errOut io.Writer) error {
	controlEnvironment()
	c, _, err := parse("run", args, errOut)
	if err != nil {
		return err
	}
	release, err := lockState(c.state)
	if err != nil {
		return err
	}
	defer release()
	if err := bindState(c.state, c.binding()); err != nil {
		return err
	}
	failures := 0
	for {
		_, err := pass(ctx, c, false, out, errOut, true)
		if ctx.Err() != nil {
			return nil
		}
		wait := c.interval
		if err != nil {
			failures++
			wait = backoff(c.interval, failures)
			say(errOut, fmt.Sprintf("valley-restacker: the pass failed, and the next starts in %v: %v\n", wait, err))
		} else {
			failures = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// backoff is how long run waits after the given number of passes in a row
// have failed: twice as long for each, from the interval, up to
// maxBackoff.
func backoff(interval time.Duration, failures int) time.Duration {
	wait := interval
	for i := 0; i < failures && wait < maxBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBackoff)
}

// pass runs one pass under its timeout and prints it: the plan and what
// it pushed, or, when quiet, only what it pushed and a journal it leaves
// open.
func pass(ctx context.Context, c config, reportOnly bool, out, errOut io.Writer, quiet bool) (*Plan, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	g, err := scratchRepo(ctx, c)
	if err != nil {
		return nil, timedOut(ctx, c, err)
	}
	g.Stderr = errOut
	var p *Plan
	if reportOnly {
		p, err = planOnce(ctx, g, c)
	} else {
		p, err = writeOnce(ctx, g, c)
	}
	if err != nil {
		return p, timedOut(ctx, c, err)
	}
	if quiet && len(p.Actions) == 0 && p.Pending == nil {
		return p, nil
	}
	var text bytes.Buffer
	switch {
	case c.json:
		enc := json.NewEncoder(&text)
		enc.SetIndent("", "  ")
		if err := enc.Encode(p); err != nil {
			return p, err
		}
	case quiet:
		printPublished(&text, p)
	default:
		printPlan(&text, p)
	}
	if err := writeOut(ctx, out, text.Bytes()); err != nil {
		return p, timedOut(ctx, c, err)
	}
	return p, nil
}

// writeOut writes data to out, and gives up once ctx is done. A reader
// that stops reading holds the write, and would otherwise hold the pass,
// and the lock on its state, past its --timeout. The write given up on
// ends with the process.
func writeOut(ctx context.Context, out io.Writer, data []byte) error {
	done := make(chan error, 1)
	go func() {
		_, err := out.Write(data)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("the plan was not written: %w", ctx.Err())
	}
}

// timedOut says so when a pass failed because it ran past its timeout.
func timedOut(ctx context.Context, c config, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("the pass ran past its --timeout of %v: %w", c.timeout, err)
	}
	return err
}

// checkIdentity refuses a name or an email git cannot commit under: an
// empty one, or one with a control character or an angle bracket, which
// would end the name or the email early in a commit's header.
func checkIdentity(name, email string) error {
	for _, f := range []struct{ flag, value string }{{"--commit-name", name}, {"--commit-email", email}} {
		if strings.TrimSpace(f.value) == "" || strings.ContainsAny(f.value, "<>") ||
			strings.ContainsFunc(f.value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("%s %q: a commit's name and email are not empty and hold no angle bracket or control character", f.flag, f.value)
		}
	}
	return nil
}

// What a pass passes on to the programs it runs. A git variable can point
// git at another repository (GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR,
// GIT_INDEX_FILE, GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES,
// GIT_NAMESPACE, GIT_GRAFT_FILE, GIT_REPLACE_REF_BASE), at other programs
// (GIT_EXEC_PATH, GIT_TEMPLATE_DIR), at other attributes (GIT_ATTR_SOURCE)
// or at another format (GIT_DEFAULT_HASH). It can inject configuration
// that beats the repository's own: GIT_CONFIG_COUNT with GIT_CONFIG_KEY_<n>
// and GIT_CONFIG_VALUE_<n>, and GIT_CONFIG_PARAMETERS, so an inherited
// author.name would beat user.name and an inherited core.hooksPath would
// turn hooks back on. The other GIT_CONFIG* variables name configuration
// files. And GIT_AUTHOR_*, GIT_COMMITTER_* and EMAIL give an identity
// ahead of the configuration. So a pass passes on no GIT_* variable but
// the ones that say how to reach origin over ssh, and no EMAIL. It drops
// them from its own environment as it starts (controlEnvironment), so
// cue, attest and the git that composes the policy run without them too.
var passedOn = map[string]bool{"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true}

// passes reports whether a pass passes on the environment variable named.
func passes(name string) bool {
	switch {
	case name == "EMAIL":
		return false
	case strings.HasPrefix(name, "GIT_"):
		return passedOn[name]
	}
	return true
}

// gitSettings are what a pass sets for every git it runs, scratch
// repository or not: no system or user configuration, no system
// attributes, and no question asked at a terminal.
var gitSettings = [][2]string{
	{"GIT_CONFIG_NOSYSTEM", "1"},
	{"GIT_CONFIG_GLOBAL", "/dev/null"},
	{"GIT_ATTR_NOSYSTEM", "1"},
	{"GIT_TERMINAL_PROMPT", "0"},
}

// controlEnvironment leaves the process's own environment holding only
// what a pass passes on, and gitSettings.
func controlEnvironment() {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !passes(name) {
			os.Unsetenv(name)
		}
	}
	for _, kv := range gitSettings {
		os.Setenv(kv[0], kv[1])
	}
}

// scratchRepo makes, or reuses, the repository a pass reads origin into,
// and returns git run in it with hooks and configured filters off,
// committing under the restacker's identity. git there reads no
// configuration but the repository's, and is given its author and
// committer explicitly. A commit it rebases keeps its own author: a
// rebase takes the author from the commit it picks. git there is
// unattended: it never asks a question, and every git it runs is killed
// whole once ctx is done.
//
// The repository has a work tree, though nothing is ever checked out in
// it: attest, which digests a version's tree and verifies the
// integrator's transfer statements, reads only a repository with a work
// tree.
func scratchRepo(ctx context.Context, c config) (rebase.Git, error) {
	dir := filepath.Join(c.state, "scratch")
	var env []string
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); passes(name) {
			env = append(env, kv)
		}
	}
	for _, kv := range gitSettings {
		env = append(env, kv[0]+"="+kv[1])
	}
	env = append(env,
		"GIT_AUTHOR_NAME="+c.commitName,
		"GIT_AUTHOR_EMAIL="+c.commitEmail,
		"GIT_COMMITTER_NAME="+c.commitName,
		"GIT_COMMITTER_EMAIL="+c.commitEmail,
	)
	g := rebase.Git{Dir: dir, Env: env, Stderr: os.Stderr, Unattended: true}
	if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return g, err
		}
		if err := run(ctx, g, "init", "--quiet"); err != nil {
			return g, err
		}
	}
	// user.useConfigOnly stops git guessing an identity from the host's
	// name and the user's account. core.attributesFile is set, since git
	// otherwise reads the user's own attributes file under
	// XDG_CONFIG_HOME or HOME whatever configuration it reads, and an
	// attribute such as merge=union there would change how a rebase
	// merges. core.fsync makes each object and ref a pass writes durable
	// before git reports it written, as a journal of writes relies on. The
	// state directory is bound to one repository (bindState), so origin's
	// URL is the same every pass.
	for _, kv := range [][2]string{
		{"core.hooksPath", "/dev/null"},
		{"core.fsmonitor", "false"},
		{"core.attributesFile", "/dev/null"},
		{"core.fsync", "objects,reference"},
		{"user.name", c.commitName},
		{"user.email", c.commitEmail},
		{"user.useConfigOnly", "true"},
		{"remote.origin.url", c.repo},
	} {
		if err := run(ctx, g, "config", kv[0], kv[1]); err != nil {
			return g, err
		}
	}
	return g, nil
}

func run(ctx context.Context, g rebase.Git, args ...string) error {
	_, err := gitOut(ctx, g, args...)
	return err
}

// gitOut runs git as the rebase engine runs it (rebase.Git.Command): killed
// with what it started once ctx is done, and with the wait for its output
// bounded.
func gitOut(ctx context.Context, g rebase.Git, args ...string) (string, error) {
	cmd := g.Command(ctx, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("git %s did not finish: %w", strings.Join(args, " "), ctx.Err())
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(b), nil
}

// ancestry is merge-base --is-ancestor, telling "no" (exit 1) apart from a
// question git could not answer.
func ancestry(ctx context.Context, g rebase.Git) Ancestry {
	return func(a, b string) (bool, error) {
		err := g.Command(ctx, "merge-base", "--is-ancestor", a, b).Run()
		var exit *exec.ExitError
		switch {
		case err == nil || errors.Is(err, exec.ErrWaitDelay):
			return true, nil
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return false, nil
		}
		return false, fmt.Errorf("could not tell whether %s is in %s", short(a), short(b))
	}
}

// incoming reads, once for each head asked about, the commits it reaches
// that tip does not. A head that is not a commit, or is missing, is an
// error, asked about again as the same error.
func incoming(ctx context.Context, g rebase.Git, tip string) Incoming {
	type read struct {
		commits map[string]bool
		err     error
	}
	seen := map[string]read{}
	return func(head string) (map[string]bool, error) {
		if r, ok := seen[head]; ok {
			return r.commits, r.err
		}
		var r read
		out, err := gitOut(ctx, g, "rev-list", head+"^{commit}", "--not", tip)
		if err != nil {
			r.err = fmt.Errorf("could not read the commits %s would bring to %s", short(head), rebase.Target)
		} else {
			r.commits = map[string]bool{}
			for _, c := range strings.Fields(out) {
				r.commits[c] = true
			}
		}
		if ctx.Err() == nil {
			seen[head] = r
		}
		return r.commits, r.err
	}
}

// policyCap composes the policy at the target tip and reads the cap on
// automatic versions: the floor from the instance repository's tip, read
// over git into the scratch repository, or from --instance; the project
// layer from the tip.
func policyCap(ctx context.Context, g rebase.Git, c config, tip string) (int, error) {
	floor := c.floor
	if c.instanceRepo != "" {
		out, err := gitOut(ctx, g, "ls-remote", c.instanceRepo, floor.Ref)
		if ctx.Err() != nil {
			return 0, err
		}
		// ls-remote matches its pattern against the tail of each name,
		// so it lists refs/heads/refs/heads/main for refs/heads/main, a
		// branch a pusher can make. Only the ref of that exact name counts.
		var oid string
		for _, line := range strings.Split(out, "\n") {
			if id, name, ok := strings.Cut(line, "\t"); ok && name == floor.Ref {
				oid = id
			}
		}
		if err != nil || !rebase.IsObjectID(oid) {
			return 0, fmt.Errorf("no instance policy layer: %s has no %s: %s", c.instanceRepo, floor.Ref, composed.Floorless)
		}
		if err := run(ctx, g, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", c.instanceRepo, oid); err != nil {
			return 0, err
		}
		floor.Repo, floor.Ref = g.Dir, oid
	}
	instance, done, err := floor.Open(ctx, "valley-restacker-floor")
	if err != nil {
		return 0, err
	}
	defer done()
	project, _, doneProject, err := composed.Project(ctx, g.Dir, tip, c.projectPolicy, "valley-restacker-project")
	if err != nil {
		return 0, err
	}
	defer doneProject()
	pol, err := composed.Compose(ctx, c.cue, c.schema, instance, project)
	if err != nil {
		return 0, err
	}
	return CapOf(pol.MaxAutomaticVersions)
}

// planOnce reads one snapshot and the cap at its tip, decides the journal
// in the state directory if there is one, plans every family, and builds
// the writes a pass would make. It pushes nothing, writes no journal, and
// lets go of what it prepared.
func planOnce(ctx context.Context, g rebase.Git, c config) (*Plan, error) {
	s, p, err := planned(ctx, g, c)
	if err != nil {
		return nil, err
	}
	// --report-only: what is built is shown, and neither kept nor pushed.
	out, err := build(ctx, g, s, p, c.binding(), p.Operation)
	if err != nil {
		return nil, err
	}
	out.releasePrepared(ctx, g)
	return p, nil
}

// planned reads one snapshot and the cap at its tip, and plans it.
func planned(ctx context.Context, g rebase.Git, c config) (*rebase.Snapshot, *Plan, error) {
	s, err := rebase.ReadSnapshot(ctx, g, c.verify)
	if err != nil {
		return nil, nil, err
	}
	if s.Tip == "" {
		return nil, nil, fmt.Errorf("origin has no %s", rebase.Target)
	}
	cap, err := policyCap(ctx, g, c, s.Tip)
	if err != nil {
		return nil, nil, err
	}
	p, err := plan(ctx, g, c, s, cap)
	if err != nil {
		return nil, nil, err
	}
	return s, p, nil
}

// writeOnce is a write pass. It settles the journal of earlier
// operations first, if the state directory holds one. It then plans, with
// each family that shares a change or a branch with a family the journal
// still holds waiting for it, builds the pass's writes, adds them to the
// journal, keeps it, and settles it (publish.go). A journal that does not
// read, or is another's, stops the pass: the pass would otherwise write
// its own in its place, and lose what that one names.
func writeOnce(ctx context.Context, g rebase.Git, c config) (*Plan, error) {
	path := filepath.Join(c.state, journalFile)
	j, err := loadJournal(c.state, c.binding())
	if err != nil {
		return nil, fmt.Errorf("%w. A write pass refuses to run while this journal is in place: check origin by hand and move the journal file aside", err)
	}
	actions := []Action{}
	var found *Recovery
	if j != nil {
		st, err := settle(ctx, g, c, j)
		if err != nil {
			return nil, err
		}
		actions, found = st.actions, st.last
		found.Journal = path
		if st.closed {
			j = nil
		}
	}
	// The planner reads the journal settle left in the state directory.
	s, p, err := planned(ctx, g, c)
	if err != nil {
		return nil, err
	}
	p.Recovery, p.Actions = found, actions
	out, err := build(ctx, g, s, p, c.binding(), p.Operation)
	if err != nil {
		return nil, err
	}
	defer out.releasePrepared(ctx, g)
	if len(out.journal.Families) == 0 {
		if j != nil {
			p.Pending = found
		}
		return p, nil
	}
	if j != nil {
		out.journal.Families = append(j.Families, out.journal.Families...)
	}
	if err := keep(ctx, g, c.state, out.journal); err != nil {
		return nil, err
	}
	// The journal keeps every object its writes push reachable now.
	out.releasePrepared(ctx, g)
	st, err := settle(ctx, g, c, out.journal)
	p.Actions = append(p.Actions, st.actions...)
	if err != nil {
		return nil, err
	}
	if !st.closed {
		p.Pending = st.last
		p.Pending.Journal = path
	}
	return p, nil
}

// binding is what a pass's state directory and journal belong to.
func (c config) binding() binding {
	return binding{Repository: c.repo, Target: c.target, Principal: c.principal}
}

// requestsOf is every integration request the snapshot lists, in name
// order, and the evidence fingerprint of every attestation ref. The refs
// are a map, read in no fixed order, so the requests are put in name
// order before anything judges them: their reasons and notes, and the
// reports that carry them, are then the same for the same snapshot.
//
// The snapshot lists request heads and does not fetch them. They are
// fetched here, before any ancestry is read, all at once and then one by
// one when that fails, so one head origin cannot serve leaves the others
// readable.
func requestsOf(ctx context.Context, g rebase.Git, s *rebase.Snapshot) ([]Request, string) {
	var attestations []string
	var requests []Request
	for ref, oid := range s.Refs {
		switch {
		case strings.HasPrefix(ref, rebase.AttestationRefs):
			attestations = append(attestations, ref+" "+oid)
		case strings.HasPrefix(ref, rebase.RequestPrefix):
			requests = append(requests, Request{Name: strings.TrimPrefix(ref, rebase.RequestPrefix), Head: oid})
		}
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].Name < requests[j].Name })
	var heads []string
	for _, r := range requests {
		heads = append(heads, r.Head)
	}
	if rebase.Fetch(ctx, g, heads...) != nil {
		for _, h := range heads {
			_ = rebase.Fetch(ctx, g, h)
		}
	}
	return requests, change.EvidenceFingerprint(attestations)
}

// plan decides the journal in the state directory, if there is one, and
// plans every family in the snapshot, with the cap given.
func plan(ctx context.Context, g rebase.Git, c config, s *rebase.Snapshot, cap int) (*Plan, error) {
	requests, evidence := requestsOf(ctx, g, s)
	p := &Plan{
		Target: rebase.Target, Tip: s.Tip, Evidence: evidence, Cap: cap,
		Operation: newOperation(time.Now()), Actions: []Action{},
	}
	in := incoming(ctx, g, s.Tip)

	j, err := loadJournal(c.state, c.binding())
	switch {
	case err != nil:
		p.Recovery = &Recovery{Journal: filepath.Join(c.state, journalFile), Error: err.Error(), Decisions: []Decision{}}
	case j != nil:
		p.Recovery = decide(j, reading{
			s: s, requests: requests, evidence: p.Evidence, in: in, is: ancestry(ctx, g),
			have:   func(oid string) bool { return run(ctx, g, "cat-file", "-e", oid) == nil },
			landed: s.IsLanded,
		})
		p.Recovery.Journal = filepath.Join(c.state, journalFile)
	}
	journaled := journaledLayers(j)

	seen := map[string]bool{}
	for _, n := range s.Changes {
		if seen[n.ID] || !s.Unlanded(n) {
			continue
		}
		f, err := rebase.Capture(s, rebase.Selection{ID: n.ID, Whole: true})
		if err != nil {
			return nil, err
		}
		for _, l := range f.Layers {
			if l.ID != "" {
				seen[l.ID] = true
			}
		}
		fp := judge(f, s, c.principal, p, requests, in)
		if why := waits(f, journaled); why != "" {
			fp.Action, fp.Reasons = wait, []string{why}
		}
		p.Families = append(p.Families, fp)
		p.families = append(p.families, f)
	}
	overlapping(p)
	return p, nil
}

// overlapping has each family the pass would build writes for wait when
// it shares a change or a branch with another such family. Two changes
// that name one branch are two families when neither's head descends
// from the other's, and the journal takes no two families that share a
// branch, since their writes would race. So neither is built while both
// name it, and every other family is planned as usual.
func overlapping(p *Plan) {
	type holder struct {
		family int
		name   string
	}
	whose := map[string][]holder{} // the families that would be written, by each change and branch
	for i, fp := range p.Families {
		if fp.Action != restack && fp.Action != hold {
			continue
		}
		for _, l := range p.families[i].Layers {
			for _, key := range layerKeys(l.ID, l.Branch) {
				if hs := whose[key]; len(hs) == 0 || hs[len(hs)-1].family != i {
					whose[key] = append(whose[key], holder{i, l.Name})
				}
			}
		}
	}
	for i := range p.Families {
		fp := &p.Families[i]
		if fp.Action != restack && fp.Action != hold {
			continue
		}
		why := ""
		for _, l := range p.families[i].Layers {
			for _, key := range layerKeys(l.ID, l.Branch) {
				for _, h := range whose[key] {
					if h.family != i && why == "" {
						why = fmt.Sprintf("%s shares the %s with %s in another family, so both families wait while both name it", l.Name, key, h.name)
					}
				}
			}
		}
		if why != "" {
			fp.Action, fp.Reasons = wait, []string{why}
		}
	}
}

// journaledLayers is the operation of each family the journal holds, by
// each of the family's changes and branches (layerKeys).
func journaledLayers(j *Journal) map[string]string {
	held := map[string]string{}
	if j == nil {
		return held
	}
	for _, f := range j.Families {
		for _, l := range f.Observed.Layers {
			for _, key := range layerKeys(l.ID, l.Branch) {
				held[key] = f.Operation
			}
		}
	}
	return held
}

// waits says why a family waits for a family the journal holds: it
// shares a change or a branch with it, and writes built for it would race
// that family's. "" is a family that shares neither.
func waits(f *rebase.Family, journaled map[string]string) string {
	for _, l := range f.Layers {
		for _, key := range layerKeys(l.ID, l.Branch) {
			if op, found := journaled[key]; found {
				return fmt.Sprintf("%s is in a family of operation %s, which the journal still holds, so this family waits for it", l.Name, op)
			}
		}
	}
	return ""
}

// judge decides what a pass would do with one family. It judges requests
// in the order given, which plan makes name order, so the family's
// requests, reasons and notes come in that order too.
func judge(f *rebase.Family, s *rebase.Snapshot, principal string, p *Plan, requests []Request, in Incoming) FamilyPlan {
	fp := FamilyPlan{Reasons: []string{}, Notes: []string{}, Requests: []RequestPlan{}}
	ready := false
	for _, l := range f.Layers {
		lp := LayerPlan{Name: l.Name, ID: l.ID, Branch: l.Branch, Head: l.Head, State: string(l.State())}
		if l.Below != nil {
			lp.Below = l.Below.Name
		}
		if l.State() == rebase.Ready {
			ready = true
		} else {
			fp.Reasons = append(fp.Reasons, stateLine(l))
		}
		fp.Layers = append(fp.Layers, lp)
	}
	switch {
	case !ready:
		fp.Action = leave
		return fp
	case f.FastForwards():
		fp.Action, fp.Reasons = current, []string{"it already descends from " + rebase.Target + "'s tip"}
		return fp
	}
	if err := f.Check(); err != nil {
		fp.Reasons = append(fp.Reasons, err.Error())
	}

	m := membersOf(f)
	for _, r := range requests {
		by, err := Relate(r, m, in)
		if err != nil {
			fp.Requests = append(fp.Requests, RequestPlan{Name: r.Name, Head: r.Head, By: []string{}, Why: err.Error()})
			fp.Reasons = append(fp.Reasons, fmt.Sprintf("request %s: whether it is related cannot be determined: %v", r.Name, err))
			continue
		}
		if by == nil {
			continue
		}
		o, found := s.OutcomeOf(r.Name)
		rp := RequestPlan{Name: r.Name, Head: r.Head, By: by}
		if found {
			rp.Decision = o.Decision
		}
		rp.Current, rp.Releases, rp.Why = Currency(o, found, r.Head, p.Tip, p.Evidence)
		switch {
		case !rp.Releases:
			fp.Reasons = append(fp.Reasons, fmt.Sprintf("request %s might still land: %s", r.Name, rp.Why))
		case o.Decision == "reject":
			fp.Notes = append(fp.Notes, fmt.Sprintf("request %s was rejected at this head, tip and evidence", r.Name))
		}
		fp.Requests = append(fp.Requests, rp)
	}

	for i, l := range f.Layers {
		if l.ID == "" || l.Record == nil {
			continue
		}
		cp := CapacityOf(l.Record, principal, p.Cap, nextSize(l.Name, l.Record, principal))
		fp.Layers[i].Capacity = cp
		fp.Reasons = append(fp.Reasons, cp.Over...)
	}
	fp.Action = restack
	if len(fp.Reasons) > 0 {
		fp.Action = hold
	}
	return fp
}

func printPlan(w io.Writer, p *Plan) {
	fmt.Fprintf(w, "%s at %s, evidence %s, at most %d automatic versions a change\n", p.Target, short(p.Tip), p.Evidence, p.Cap)
	if r := p.Recovery; r != nil {
		printRecovery(w, r)
	}
	if len(p.Families) == 0 {
		fmt.Fprintln(w, "no change to restack")
	}
	for _, f := range p.Families {
		fmt.Fprintf(w, "\n%s: %s\n", f.Layers[0].Name, f.Action)
		for _, l := range f.Layers {
			fmt.Fprintf(w, "  %-24s %-10s %s", l.Name, l.State, short(l.Head))
			if l.Below != "" {
				fmt.Fprintf(w, " on %s", l.Below)
			}
			if c := l.Capacity; c != nil {
				fmt.Fprintf(w, "; %d/%d automatic, %d/%d versions, update %d/%d, %d/%d bytes",
					c.Uploads, c.Cap, c.Versions, c.MaxVersions, c.Seq, c.MaxSeq, c.Bytes, c.MaxBytes)
			}
			fmt.Fprintln(w)
		}
		for _, r := range f.Requests {
			fmt.Fprintf(w, "  request %s at %s, by %s: %s\n", r.Name, short(r.Head), strings.Join(r.By, ", "), r.Why)
		}
		for _, why := range f.Reasons {
			fmt.Fprintf(w, "  - %s\n", why)
		}
		for _, n := range f.Notes {
			fmt.Fprintf(w, "  note: %s\n", n)
		}
		if f.Writes != nil {
			printWrites(w, f.Writes)
		}
	}
	printPublished(w, p)
}

// printPublished prints what a write pass pushed, and the journal it
// leaves open, if it leaves one.
func printPublished(w io.Writer, p *Plan) {
	for _, a := range p.Actions {
		what := a.Write
		if a.ID != "" {
			what += " " + a.ID
		}
		fmt.Fprintf(w, "operation %s, family %d: %s: %s", a.Operation, a.Family, what, a.Result)
		if a.Commit != "" {
			fmt.Fprintf(w, " %s", short(a.Commit))
		}
		if a.Why != "" {
			fmt.Fprintf(w, ": %s", a.Why)
		}
		fmt.Fprintln(w)
	}
	if r := p.Pending; r != nil {
		fmt.Fprintln(w, "the journal stays open; any family that shares a change or branch with an open family waits:")
		for _, d := range r.Decisions {
			if d.settled() {
				continue
			}
			what := d.Write
			if d.ID != "" {
				what += " " + d.ID
			}
			fmt.Fprintf(w, "  operation %s, family %d %s: %s: %s\n", d.Operation, d.Family, what, d.Action, d.Why)
		}
	}
}

// printWrites prints what a pass would push for a family.
func printWrites(w io.Writer, ws *Writes) {
	fmt.Fprintf(w, "  would write, as %s:\n", ws.Result)
	for _, b := range ws.Branches {
		fmt.Fprintf(w, "    push %s %s -> %s\n", b.Branch, short(b.Old), short(b.New))
	}
	for _, v := range ws.Versions {
		fmt.Fprintf(w, "    add %s v%d at %s: record %s, leased on %s\n", v.ID, v.Version, short(v.Head), short(v.Record), short(v.Lease))
	}
	for _, r := range ws.Reports {
		switch {
		case r.Error != "":
			fmt.Fprintf(w, "    report %s: none fits: %s\n", r.ID, r.Error)
		case r.Unchanged:
			fmt.Fprintf(w, "    report %s %s: not written, as the current report says the same\n", r.ID, r.Result)
		default:
			lease := "none"
			if r.Lease != "" {
				lease = short(r.Lease)
			}
			fmt.Fprintf(w, "    report %s %s: %s, replacing %s\n", r.ID, r.Result, short(r.Commit), lease)
		}
		if len(r.Omitted) > 0 {
			fmt.Fprintf(w, "      leaves out %s\n", strings.Join(r.Omitted, ", "))
		}
	}
}

// printRecovery prints what recovery decided of the journal.
func printRecovery(w io.Writer, r *Recovery) {
	if r.Error != "" {
		fmt.Fprintf(w, "recovery: %s\n", r.Error)
		return
	}
	state := "settled"
	if !r.Settled {
		state = "not settled"
	}
	fmt.Fprintf(w, "recovery: %s, %s\n", r.Journal, state)
	for _, d := range r.Decisions {
		what := d.Write
		if d.ID != "" {
			what += " " + d.ID
		}
		action := d.Action
		switch {
		case d.Terminal:
			action += " (terminal)"
		case d.Final:
			action += " (final)"
		}
		fmt.Fprintf(w, "  operation %s, family %d %s: %s: %s\n", d.Operation, d.Family, what, action, d.Why)
	}
}
