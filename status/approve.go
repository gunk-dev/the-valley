package main

// approve files a change's integration request: the whole of `valley
// approve`, which bin/valley hands to it, and review's [a]pprove, which
// bin/valley runs it for with --reviewing. Both run one attempt
// (approval.attempt), so the verb and the key cannot drift apart.
//
// An attempt binds the head it files, derives what the policy at main's
// tip requires of it (../derive), reuses the evidence origin already
// holds for the head's tree (coverage.go), runs the rest in a throwaway
// worktree through attest run, and files the request and the evidence in
// one atomic push. attest run stores each note in this repository and
// pushes nothing, so no evidence stands published for a change nobody has
// approved, and the request is never filed without its evidence. Nothing
// here writes the protected ref: the integrator judges the request and
// lands it. Every way it ends is said in one verdict line
// (verdict.go). With valley.telemetry on, each attempt is a telemetry run
// of its own, recording approve.start, approve.policy, approve.coverage,
// approve.checks, approve.request and approve.end, and attest records the
// checks it runs under the linked run <run>-checks. A record is never
// evidence, nothing reads one to decide anything, and a record that cannot
// be stored costs that record alone.
//
// Review's [a]pprove is told the change review bound, as flags, and runs
// on review's own terminal. It writes how the attempt ended to the file
// --outcome names, as one line of tab-separated fields:
//
//	approved <change ID> <handle> <version> <head>
//	checks-failed <head>
//	refused
//	unknown
//	interrupted
//
// The approved line names what was filed, after the attempt's own reads
// of the record, with empty fields for a plain branch. unknown is a
// request whose state the attempt could not establish after the push.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"the-valley/change"
	"the-valley/derive"
	"the-valley/telemetry"
)

const approveUsage = "usage: valley approve [-v] [--head <sha>] [--build-here] <change>"

// interruptedStatus is the exit status of an approval cut short by a
// signal, as a shell reports a process SIGINT ended.
const interruptedStatus = 130

// headArgument is the shape a --head value must have before it is read
// as a commit: an object ID, or an abbreviation of one.
var headArgument = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// changeRequest is the request name of a change with a record, which no
// branch is filed under.
var changeRequest = regexp.MustCompile(`^c-[k-z]{32}$`)

// approval is one approval attempt and what it was told.
type approval struct {
	g              git
	stdout, stderr io.Writer

	verbose, buildHere bool
	reviewing, carried bool
	outcome            string // the file review reads the outcome from

	// name is the change as it was named, read again before filing in case
	// a plain branch has come to name a change. For a change, id, handle,
	// version, head, branch, state and owner are what resolving that name
	// read; head is the active version's head.
	name                               string
	id, handle, version, head, branch  string
	state, owner                       string
	bound, boundVersion, title         string // the head filed, its version and title
	failedAt                           string // the head whose checks failed
	filedID, filedHandle, filedVersion string // what the approved line names
	tel                                *approveTelemetry
	mu                                 sync.Mutex
	interrupted, finished              bool
	child                              *os.Process
	childDone                          chan struct{}
	worktree                           string
	signals                            chan os.Signal
}

// errRefused is an attempt that ended without filing, having said why.
var errRefused = errors.New("refused")

// errUnknown is a request whose state could not be established after the
// push, having said so.
var errUnknown = errors.New("unknown")

func runApprove(g git, args []string, stdout, stderr io.Writer) int {
	a := &approval{g: g, stdout: stdout, stderr: stderr}
	if len(args) > 0 && args[0] == "--reviewing" {
		if err := a.reviewFlags(args[1:]); err != nil {
			fmt.Fprintf(stderr, "valley: %s\n", err)
			return 2
		}
		a.watch()
		return a.run()
	}
	want, err := a.verbFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		fmt.Fprintln(stderr, "valley: not a git repository")
		return 1
	}
	// The name is read before the fetch, as review reads it.
	r, err := resolveRecorded(g, a.name)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	a.id, a.handle, a.head, a.branch, a.state, a.owner = r.id, r.handle, r.head, r.branch, r.state, r.owner
	if r.id != "" {
		a.version = strconv.Itoa(r.version)
	}
	fetch := exec.Command("git", "fetch", "--prune", "--quiet", "origin")
	fetch.Env, fetch.Stdout, fetch.Stderr = g.env, stderr, stderr
	processStarted()
	if err := fetch.Run(); err != nil {
		return exitCode(err)
	}
	a.watch()
	if a.id != "" {
		if a.bindHead(want) != nil {
			return 1
		}
	} else {
		tip, ok, err := g.ref("refs/remotes/origin/" + a.branch)
		if err != nil || !ok {
			fmt.Fprintf(stderr, "valley: no such branch on origin: %s\n", a.branch)
			return 1
		}
		if want != "" {
			at, _ := g.output(true, "rev-parse", "--verify", "--quiet", want+"^{commit}")
			if at != tip {
				a.declined(fmt.Sprintf("origin/%s is at %s, not %s", a.branch, a.short(tip), want),
					act{run: "valley approve " + shellQuote(a.branch)})
				return 1
			}
		}
	}
	return a.run()
}

// verbFlags reads the approve verb's arguments, and returns --head's value.
func (a *approval) verbFlags(args []string) (want string, err error) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--build-here":
			a.buildHere = true
		case arg == "-v" || arg == "--verbose":
			a.verbose = true
		case arg == "--head" && i+1 < len(args):
			want = args[i+1]
			i++
		case strings.HasPrefix(arg, "--head="):
			want = strings.TrimPrefix(arg, "--head=")
		case strings.HasPrefix(arg, "-") || a.name != "":
			return "", failure(approveUsage)
		default:
			a.name = arg
		}
	}
	if a.name == "" {
		return "", failure(approveUsage)
	}
	return want, nil
}

// reviewFlags reads what review bound for [a]pprove: the name as typed
// and the file the outcome goes to, then for a change its ID, handle,
// version and the head shown, or for a plain branch the branch and the
// head shown, when review showed one.
func (a *approval) reviewFlags(args []string) error {
	a.reviewing = true
	values := map[string]*string{"--name": &a.name, "--outcome": &a.outcome, "--change": &a.id,
		"--handle": &a.handle, "--version": &a.version, "--head": &a.bound, "--branch": &a.branch}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--build-here":
			a.buildHere = true
		case arg == "-v":
			a.verbose = true
		case arg == "--carried":
			a.carried = true
		case arg == "--shown" && i+1 < len(args):
			a.bound = args[i+1]
			i++
		case values[arg] != nil && i+1 < len(args):
			*values[arg] = args[i+1]
			i++
		default:
			return fmt.Errorf("approve --reviewing: unexpected %q", arg)
		}
	}
	switch {
	case a.name == "" || a.outcome == "":
		return errors.New("approve --reviewing needs --name and --outcome")
	case a.id != "" && (!change.ValidID(a.id) || a.handle == "" || a.version == "" || !isObjectID(a.bound)):
		return errors.New("approve --reviewing --change needs --handle, --version and --head")
	case a.id == "" && a.branch == "":
		return errors.New("approve --reviewing needs --change or --branch")
	}
	if a.id != "" {
		// Review shows a change at its active version's head, and binds
		// [a]pprove to it.
		a.head, a.boundVersion = a.bound, a.version
	}
	return nil
}

// run is one attempt, its telemetry run, and its outcome: 0 when it
// filed, 1 when it did not.
func (a *approval) run() int {
	// Off a terminal, the answer to review's prompt ends no line, so the
	// verdict starts its own, unless the carry line has ended it.
	if a.reviewing && !a.carried {
		if _, tty := windowColumns(os.Stdin); !tty {
			fmt.Fprintln(a.stdout)
		}
	}
	t := a.beginTelemetry()
	a.mu.Lock()
	a.tel = t
	a.mu.Unlock()
	err := a.attempt()
	if !a.finish() {
		select {} // the signal handler ends the process
	}
	switch {
	case err == nil:
		a.tel.end("filed")
		a.writeOutcome("approved", a.filedID, a.filedHandle, a.filedVersion, a.bound)
		return 0
	case a.failedAt != "":
		a.tel.end("refused")
		a.writeOutcome("checks-failed", a.failedAt)
	case errors.Is(err, errUnknown):
		a.tel.end("refused")
		a.writeOutcome("unknown")
	default:
		a.tel.end("refused")
		a.writeOutcome("refused")
	}
	return 1
}

// writeOutcome writes review's outcome line, when review asked for one.
func (a *approval) writeOutcome(fields ...string) {
	if a.outcome == "" {
		return
	}
	if err := os.WriteFile(a.outcome, []byte(strings.Join(fields, "\t")+"\n"), 0o600); err != nil {
		fmt.Fprintf(a.stderr, "valley: could not write the approval's outcome: %v\n", err)
	}
}

// watch takes over SIGINT and SIGTERM for the rest of the process: a
// signal interrupts the attempt.
func (a *approval) watch() {
	a.signals = make(chan os.Signal, 2)
	signal.Notify(a.signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-a.signals
		a.interrupt()
	}()
}

// interrupt ends an attempt a signal cut short: it stops the checks
// running, if any, waits for them to end, removes their worktree, records
// the attempt's end as interrupted, writes that outcome for review, and
// exits 130. A second signal while the checks are ending kills them. A
// signal that reaches the checks first ends them, and checking's caller
// interrupts the attempt itself; whichever comes first ends it.
func (a *approval) interrupt() {
	a.mu.Lock()
	if a.finished {
		a.mu.Unlock()
		exitWith(interruptedStatus)
	}
	if a.interrupted {
		a.mu.Unlock()
		select {} // the first interrupt ends the process
	}
	a.interrupted = true
	child, done, t := a.child, a.childDone, a.tel
	a.mu.Unlock()
	if child != nil {
		_ = child.Signal(os.Interrupt)
		select {
		case <-done:
		case <-a.signals:
			_ = child.Kill()
			<-done
		}
	}
	a.removeWorktree()
	t.end("interrupted")
	a.writeOutcome("interrupted")
	exitWith(interruptedStatus)
}

// finish marks the attempt ended, unless a signal has interrupted it,
// when it reports false and the signal handler ends the process.
func (a *approval) finish() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finished = !a.interrupted
	return a.finished
}

func (a *approval) stopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interrupted
}

// exitWith ends the process with status, recording the command first for
// a verb bin/valley execs this program for. main sets it.
var exitWith = os.Exit

// act is what a refusal tells the person to do next: a command to run,
// one to read, or what to do when it is no command.
type act struct{ run, see, then string }

// declined says what stops the approval, as a verdict on stderr, and
// returns errRefused. A change is named by its handle and the version
// being approved, a plain branch by its name.
func (a *approval) declined(why string, next act) error {
	return a.refused(verdict{why: why, run: next.run, see: next.see, then: next.then, required: -1, passed: -1})
}

func (a *approval) refused(v verdict) error {
	if a.stopped() {
		return errRefused
	}
	v.outcome, v.target, v.name = "refused", "main", a.label()
	if a.id != "" {
		v.version = a.boundVersion
		if v.version == "" {
			v.version = a.version
		}
	}
	fmt.Fprint(a.stderr, v.lines(terminalOf(a.stderr)))
	return errRefused
}

// label is the change as the verdict names it: its handle, or a plain
// branch's name.
func (a *approval) label() string {
	if a.handle != "" {
		return a.handle
	}
	return a.branch
}

// quoted is the change as a command offered names it.
func (a *approval) quoted() string { return shellQuote(a.label()) }

func (a *approval) short(oid string) string {
	s, _ := a.g.output(true, "rev-parse", "--short", oid)
	return s
}

// subject is one line of a commit, for saying which commit is meant. A
// request being replaced is usually this operator's own pre-rebase head;
// one another operator filed may not be in this repository.
func (a *approval) subject(oid string) string {
	if s, err := a.g.output(true, "log", "-1", "--format=%s", oid); err == nil && s != "" {
		return s
	}
	return "subject not in this repository"
}

// notReady refuses a change that is not ready, offering its owner, as
// git config valley.principal names them, the command that readies it
// again. A review is the operator's and the change is usually someone
// else's, so review is offered nothing.
func (a *approval) notReady(state, id, branch, owner string) error {
	why := "change is " + state
	if state == "draft" {
		why = "change is a draft"
	}
	return a.declined(why, a.readyHint(id, branch, owner))
}

func (a *approval) readyHint(id, branch, owner string) act {
	if a.reviewing || owner == "" {
		return act{}
	}
	if principal, _ := a.g.output(true, "config", "valley.principal"); principal != owner {
		return act{}
	}
	run := "valley ready --change " + id
	if branch != "" {
		run += " " + shellQuote(branch)
	}
	return act{run: run}
}

// bindHead binds the head approve files for a change with a record. With
// --head it is exactly that head, which must be one of the record's
// version heads while the change is ready, the active one or not: a front
// end that shows the change its own way passes the head it showed, so
// what is approved is what was seen. Without --head the person is shown
// the active version's head and title and asked to confirm them, which
// needs a terminal.
func (a *approval) bindHead(want string) error {
	if a.state != "ready" {
		return a.notReady(a.state, a.id, a.branch, a.owner)
	}
	if want != "" {
		var sha string
		var err error
		if headArgument.MatchString(want) {
			sha, err = a.g.output(true, "rev-parse", "--verify", "--quiet", want+"^{commit}")
		}
		if sha == "" || err != nil {
			return a.declined("--head "+want+" names no commit here", act{see: "valley status " + a.handle})
		}
		if err := a.checkVersion(sha); err != nil {
			return err
		}
		a.bound = sha
		return nil
	}
	if err := a.checkVersion(a.head); err != nil {
		return err
	}
	if _, tty := windowColumns(os.Stdin); !tty {
		return a.declined("no terminal to confirm the head, "+a.title,
			act{run: "valley approve --head " + a.head + " " + a.handle})
	}
	fmt.Fprintf(a.stdout, "approve change %s v%s at %s, %s? [y/N] ", a.handle, a.boundVersion, a.head, a.title)
	switch readLine(os.Stdin) {
	case "y", "Y", "yes":
		a.bound = a.head
		return nil
	}
	return a.declined("not confirmed", act{})
}

// readLine reads one line, a byte at a time so that nothing after it is
// taken from whatever reads the input next.
func readLine(r io.Reader) string {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			line = append(line, b[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSuffix(string(line), "\r")
}

// checkVersion reads the change's versions from origin again, and
// refuses unless the change is ready and head is one of its versions'
// heads, whichever version is active by then: a record that moves after
// review cannot change what lands, since the request names the head
// approved. It binds that version's number and title.
func (a *approval) checkVersion(head string) error {
	a.boundVersion, a.title = "", ""
	r, err := versionsOf(a.g, a.id)
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", err)
		return a.declined("could not read the change's versions", act{})
	}
	var known []string
	for _, v := range r.Versions {
		known = append(known, fmt.Sprintf("v%d at %s", v.N, headOf(v)))
		if headOf(v) == head {
			a.boundVersion, a.title = strconv.Itoa(v.N), v.Title
		}
	}
	if r.State != "ready" {
		return a.notReady(r.State, a.id, a.branch, a.owner)
	}
	if a.boundVersion == "" {
		return a.declined(fmt.Sprintf("%s is none of its versions (%s)", a.short(head), strings.Join(known, ", ")),
			act{see: "valley status " + a.handle})
	}
	return nil
}

// checkRecord reads the change's record again from origin, and refuses
// unless the change is still ready with the bound head among its
// versions. The record was read when review or approve began, and another
// principal may unready, withdraw or ready a new version of the change
// while a review is open or the checks run. These reads save a run that
// would come to nothing: the integrator makes the same check on origin.
//
// A name that read as a plain branch can come to name a change
// meanwhile, so the last read before filing, given the head about to be
// filed as acting, reads the name again: when it names a change now, the
// change must be ready, and its active head must be that head.
func (a *approval) checkRecord(acting string) error {
	if a.id == "" {
		if acting == "" {
			return nil
		}
		r, err := resolveRecorded(a.g, a.name)
		if err != nil {
			fmt.Fprintf(a.stderr, "valley: %s\n", err)
			return a.declined("could not read origin's records again", act{})
		}
		if r.id == "" {
			return nil
		}
		if r.state != "ready" {
			return a.declined(fmt.Sprintf("now change %s v%d, which is %s", r.handle, r.version, r.state),
				a.readyHint(r.id, r.branch, r.owner))
		}
		if r.head != acting {
			return a.declined(fmt.Sprintf("now change %s v%d, at another head", r.handle, r.version),
				act{run: "valley review " + r.handle})
		}
		return nil
	}
	r, err := resolveRecorded(a.g, a.id)
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", err)
		return a.declined("could not read the change's record again", act{})
	}
	if r.id != a.id {
		return a.declined("origin no longer holds the change's record", act{})
	}
	if r.state != "ready" {
		return a.notReady(r.state, a.id, r.branch, r.owner)
	}
	return a.checkVersion(a.bound)
}

// branchAtShown refuses unless the plain branch on origin is still at the
// head bound to this approval. The operator reviewed a branch, and a
// branch that moved holds commits nobody has read. origin itself is read,
// not the tracking ref: a push from another checkout moves the branch
// without moving this clone's ref.
func (a *approval) branchAtShown() error {
	now, err := a.remoteRef("refs/heads/" + a.branch)
	switch {
	case err != nil:
		return a.declined("could not read origin/"+a.branch+" again", act{})
	case now == "":
		return a.declined("origin/"+a.branch+" is gone", act{})
	case now != a.bound:
		return a.declined(fmt.Sprintf("origin/%s moved from %s, the head shown, to %s", a.branch, a.short(a.bound), a.short(now)),
			act{run: "valley review " + shellQuote(a.branch)})
	}
	return nil
}

// lsRemote lists origin's refs matching the patterns. ls-remote matches a
// pattern against the tail of a ref name, so a caller compares each ref
// it lists whole. A listing that fails is an error, never an empty one.
func (a *approval) lsRemote(patterns ...string) (map[string]string, error) {
	out, err := a.g.output(true, append([]string{"ls-remote", "origin"}, patterns...)...)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if oid, ref, ok := strings.Cut(line, "\t"); ok {
			refs[ref] = oid
		}
	}
	return refs, nil
}

// remoteRef is the value of a ref on origin, or "" when origin has none.
func (a *approval) remoteRef(ref string) (string, error) {
	refs, err := a.lsRemote(ref)
	return refs[ref], err
}

// pushLookalike is a ref origin holds whose name ends in one of these,
// other than the ref itself, or "". git push matches a destination as
// rev-parse matches a name: with the ref missing on origin, a push to it
// writes refs/heads/<ref> instead, and with both there it is refused as
// ambiguous. So nothing is pushed while origin holds such a ref.
func (a *approval) pushLookalike(refs ...string) (string, error) {
	var patterns []string
	exact := map[string]bool{}
	for _, ref := range refs {
		patterns = append(patterns, ref, ref+"/HEAD")
		exact[ref] = true
	}
	listed, err := a.lsRemote(patterns...)
	if err != nil {
		return "", err
	}
	var others []string
	for ref := range listed {
		if !exact[ref] {
			others = append(others, ref)
		}
	}
	sort.Strings(others)
	if len(others) == 0 {
		return "", nil
	}
	return others[0], nil
}

// reportEvidence says, with -v, where the checks this run signed are
// stored, for every failure after they have run.
func (a *approval) reportEvidence(refs []string) {
	if len(refs) == 0 || !a.verbose || a.stopped() {
		return
	}
	fmt.Fprintln(a.stderr, "valley: the evidence is stored locally at:")
	for _, ref := range refs {
		fmt.Fprintf(a.stderr, "  %s\n", ref)
	}
}

// attestationStandsElsewhere reports whether origin holds one of these
// attestation refs at a value other than the one being pushed. approve
// pushes a ref of its own notes alone, and origin takes an existing one
// only with every note it holds kept, so that is the one refusal
// approving again will never clear.
func (a *approval) attestationStandsElsewhere(digest string, refs []string) bool {
	remote, err := a.lsRemote(attestationRefs + digest + "/*")
	if err != nil {
		return false
	}
	for _, ref := range refs {
		have := remote[ref]
		if have == "" {
			continue
		}
		if want, err := a.g.output(true, "rev-parse", ref); err == nil && have != want {
			return true
		}
	}
	return false
}

// diagnosePush says why a push carrying the request ref failed, as
// precisely as the state allows. Every such push is leased, and a lease
// refuses when the ref is not where the read before it saw it: the
// request was consumed, or another approval filed or moved it. Reading
// the ref again tells them apart. Anything else is origin's own refusal,
// which git has already printed.
func (a *approval) diagnosePush(ref, pending string) error {
	now, err := a.remoteRef(ref)
	switch {
	case err != nil || now == pending:
		return a.declined("origin refused the push", act{})
	case now == "":
		return a.declined("the pending request was consumed meanwhile", act{see: "valley status " + a.quoted()})
	}
	return a.declined(fmt.Sprintf("another approval filed %s (%s) meanwhile", a.short(now), a.subject(now)),
		act{run: "valley approve " + a.quoted()})
}

// requestName is the change segment of a request ref for a plain branch.
// The integrator refuses a segment holding a slash (integrator/refs.go),
// so a slash is written as a dash. The mapping is lossy: topic/x and
// topic-x are one request, and approve cannot tell a sibling's request
// from its own, so it prints the subject of any request it replaces.
func requestName(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

// attempt is one approval attempt, from main's tip to the verdict.
func (a *approval) attempt() error {
	g := a.g
	root, err := g.output(true, "rev-parse", "--show-toplevel")
	if err != nil {
		return a.declined("not in a git repository", act{})
	}
	// main's tip as the fetch left it, read once by its exact name. The
	// merge base, the fast-forward and both policy layers are read at
	// that one commit.
	tip, ok, err := g.ref("refs/remotes/origin/main")
	switch {
	case err != nil:
		return a.declined("could not read origin/main", act{run: "git fetch origin"})
	case !ok:
		return a.declined("origin has no main to approve onto", act{})
	}

	// A change with a record is filed at exactly the bound head, under its
	// own request name, c-<change ID>, and only while its record says it
	// is ready and lists that head as a version's. Where its branch is
	// does not matter: the request names the head. A plain branch is
	// filed at the head review showed, or else at its tip on origin.
	var head, request, what, named string
	switch {
	case a.id != "":
		if a.bound == "" {
			return a.declined("no head is bound to this approval", act{})
		}
		if err := a.checkRecord(""); err != nil {
			return err
		}
		head, request, named = a.bound, "c-"+a.id, a.handle
		what = a.handle + " v" + a.boundVersion
	case a.bound != "":
		head, request, what, named = a.bound, requestName(a.branch), a.branch, a.branch
	default:
		oid, ok, err := g.ref("refs/remotes/origin/" + a.branch)
		if err != nil || !ok {
			return a.declined("origin has no branch "+a.branch, act{})
		}
		head, request, what, named = oid, requestName(a.branch), a.branch, a.branch
	}
	if a.id == "" && changeRequest.MatchString(request) {
		return a.declined("the branch's request name "+request+" is change "+strings.TrimPrefix(request, "c-")+"'s",
			act{then: "rename the branch, or name the change by its ID"})
	}
	base, err := g.output(true, "merge-base", tip, head)
	if err != nil || !isObjectID(base) {
		return a.declined("shares no history with main", act{})
	}
	ref := "refs/the-valley/integration-requests/main/" + request
	a.tel.context(a.id, a.boundVersion, head)
	a.tel.emit("approve.start", map[string]any{"what": what, "request": ref})

	// A head that does not fast-forward from main, and would bring a
	// version of another change that has not landed, is a layer above the
	// bottom of a stale stack. The integrator could land it all the same,
	// as one commit recorded against this request alone, and the changes
	// under it would have no landing record of their own. So the stack is
	// rebased first. This is the project's choice, not a rule the
	// integrator needs: a stale head with no change under it is approved
	// as it is, and the integrator merges it onto main.
	if !g.succeeds(true, "merge-base", "--is-ancestor", tip, head) {
		under, err := bringsOf(g, a.id, head)
		if err != nil {
			fmt.Fprintf(a.stderr, "valley: %s\n", err)
			return a.declined("could not read which changes it would bring to main", act{})
		}
		if len(under) > 0 {
			return a.declined("stack is behind main (includes "+strings.Join(under, ", ")+")",
				act{run: "valley rebase " + shellQuote(named)})
		}
	}

	// What the policy requires of the change, read out of exactly the
	// sources the integrator reads: both layers at the target's tip, and
	// the floor from the owning valley's own tip when the tip names one. A
	// change never supplies the policy that gates it, and neither does
	// the checkout this runs from.
	res, label, plabel, err := a.derive(root, tip, base, head)
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", err)
		return a.declined("the policy did not derive", act{})
	}
	required := res.Required
	names := make([]string, len(required))
	for i, q := range required {
		names[i] = q.Name
	}
	a.tel.emit("approve.policy", map[string]any{"required": names, "instance": label, "project": plabel})

	// A change the policy asks nothing of is still a change, and the
	// integrator lands a request that requires no check: nothing is
	// signed, no key is needed, and one request ref is published.
	//
	// A required nix check whose evidence origin already holds is reused,
	// not built again: a check worker, or an earlier approval, may have
	// published it. A read of origin that fails ends the approval rather
	// than reading as no evidence.
	var reused, missing, unbuilt []string
	var e evidence
	if len(required) > 0 {
		if _, err := exec.LookPath("attest"); err != nil {
			return a.declined("the attest helper is not installed", act{then: "nix run .#valley bundles it"})
		}
		var asked []nixCheck
		for _, q := range required {
			if q.Runner == "nix" {
				asked = append(asked, nixCheck{q.Name, q.Attribute})
			}
		}
		// What coverage and the git it runs say is shown only with -v.
		quiet := g
		var said io.Writer = io.Discard
		if a.verbose {
			said = a.stderr
		} else {
			quiet.stderr = io.Discard
		}
		if e, err = coverageOf(quiet, head, asked, said); err != nil {
			if a.verbose {
				fmt.Fprintf(a.stderr, "valley: %s\n", err)
			}
			return a.declined("could not read the evidence origin holds", act{run: "valley approve -v " + a.quoted()})
		}
		// A check whose evidence on origin records a failure is judged by
		// that evidence, so building it again changes nothing for this
		// tree, and nothing is filed.
		var failed []string
		for _, c := range asked {
			if _, ok := e.failed[c.name]; ok {
				failed = append(failed, c.name)
			}
		}
		if len(failed) > 0 {
			a.failedAt = head
			return a.refused(verdict{required: len(required), passed: -1, failed: failed})
		}
		var signedBy []string
		for _, q := range required {
			if by, ok := e.covered[q.Name]; ok {
				reused = append(reused, q.Name)
				signedBy = append(signedBy, q.Name+"="+signerOf(by))
			} else {
				missing = append(missing, q.Name)
			}
		}
		a.tel.emit("approve.coverage", map[string]any{"tree": e.digest, "reused": list(reused),
			"reused_by": list(signedBy), "missing": list(missing)})
		if a.verbose && len(reused) > 0 {
			fmt.Fprintf(a.stdout, "reusing the evidence origin holds for %s at %s, tree %s:\n", what, a.short(head), e.digest)
			for _, check := range reused {
				fmt.Fprintf(a.stdout, "  %-24s signed by %s\n", check, signerOf(e.covered[check]))
			}
		}
	}

	// A required nix check with no evidence on origin is built here only
	// with --build-here. The host's check worker builds the same checks
	// for a ready change and publishes the evidence, so without the flag
	// approve files the request at once without it, and the integrator
	// holds the request until the evidence arrives. Whether the worker
	// publishes at all is not assured. A command check is never reused and
	// no worker runs one, so it runs here either way. From here on,
	// missing is what runs here, and unbuilt what nothing here builds.
	runner := map[string]derive.Required{}
	for _, q := range required {
		runner[q.Name] = q
	}
	if !a.buildHere {
		var here []string
		for _, check := range missing {
			if runner[check].Runner == "nix" {
				unbuilt = append(unbuilt, check)
			} else {
				here = append(here, check)
			}
		}
		missing = here
	}

	var evidenceRefs []string
	took := ""
	if len(missing) > 0 {
		if evidenceRefs, took, err = a.check(head, e, required, reused, missing, runner); err != nil {
			return err
		}
	} else if len(required) == 0 && a.verbose {
		fmt.Fprintf(a.stdout, "%s requires no check under %s + %s; filing the request with no evidence\n", what, label, plabel)
	}

	// What origin holds for this request, read once. Approving the same
	// change twice is ordinary: evidence expires, and a change whose input
	// closure moved under it is told to re-attest. So the request ref may
	// already exist. A ref whose name ends in the request's, or in an
	// evidence ref's, would take the push in its place.
	pending, err := a.remoteRef(ref)
	lookalike := ""
	if err == nil {
		lookalike, err = a.pushLookalike(append([]string{ref}, evidenceRefs...)...)
	}
	if err != nil {
		a.declined("could not read the request ref on origin", act{})
		a.reportEvidence(evidenceRefs)
		return errRefused
	}
	if lookalike != "" {
		a.declined("origin holds "+lookalike+", which git would push to in place of a ref approve pushes",
			act{then: "delete " + lookalike + " on origin"})
		a.reportEvidence(evidenceRefs)
		return errRefused
	}

	// The one push: every evidence ref, and the request ref wherever it
	// has to go, --atomic, so a hook that refuses any of them leaves
	// origin as it was.
	//
	// Every write of the request ref is leased against what the read above
	// saw, including nothing: --force-with-lease=<ref>: refuses if the ref
	// exists at all, so filing for the first time is a compare-and-swap
	// too, and two operators filing one change at once cannot have the
	// second silently replace the first. The lease is the whole of the
	// force and no refspec carries a +, which would defeat the lease.
	//
	// A request already at this head is a re-attestation: the ref does not
	// move, so it is left out of the push, which carries no
	// compare-and-swap on a ref it does not change. Where it stands
	// afterwards is read again below. A request at another head is
	// replaced: a rebased head is no descendant of the old one.
	var push, lease []string
	for _, spec := range evidenceRefs {
		push = append(push, spec+":"+spec)
	}
	if pending != head {
		lease = append(lease, "--force-with-lease="+ref+":"+pending)
		push = append(push, head+":"+ref)
	}

	// The checks can take minutes, and the record may have moved
	// meanwhile, so it is read once more just before anything is filed.
	//
	// A window remains between this read and the push. A change unreadied
	// or withdrawn in it is filed all the same: git compares only the refs
	// a push changes, and the record ref is not one of them, so no client
	// can make the filing conditional on the record. The integrator
	// narrows the window: it lands no request that would bring a version
	// of a change that is not ready, and reads the records last just
	// before it moves the target, so only a record written in that moment
	// is missed.
	if err := a.checkRecord(head); err != nil {
		a.reportEvidence(evidenceRefs)
		return err
	}
	if a.id == "" && a.bound != "" {
		if err := a.branchAtShown(); err != nil {
			a.reportEvidence(evidenceRefs)
			return err
		}
	}

	// With no new evidence and the request already at this head there is
	// nothing to push, and a push with no refspec would fall back to
	// origin's default one, so none is made.
	pushed := false
	if len(push) > 0 {
		pushed = true
		args := append(append([]string{"push", "--atomic", "--quiet"}, lease...), "origin")
		cmd := exec.Command("git", append(args, push...)...)
		cmd.Env, cmd.Stdout, cmd.Stderr = g.env, a.stderr, a.stderr
		processStarted()
		if err := cmd.Run(); err != nil {
			a.tel.emit("approve.request", map[string]any{"ref": ref, "result": "refused"})
			if len(lease) == 0 && a.attestationStandsElsewhere(e.digest, evidenceRefs) {
				a.declined("this key's evidence for the tree is on origin already",
					act{then: "attest with another key: set VALLEY_ATTEST_KEY"})
			} else {
				a.diagnosePush(ref, pending)
			}
			a.reportEvidence(evidenceRefs)
			return errRefused
		}
		a.tel.emit("approve.request", map[string]any{"ref": ref, "result": "pushed"})
	}

	// The lines -v adds under the request line.
	var standing []string
	if pending == head {
		// The request ref was not in that push, so where it stands now is
		// read again: the integrator may have landed the change and
		// consumed it, or another approval replaced it. Nothing here
		// writes it back. Re-creating a consumed request would file a
		// change already in the stream, replacing another approval's
		// would clobber the winner, and either would be a second push. The
		// approval stood before this run, so a request that cannot be read
		// leaves its state unknown rather than unapproved; approving again
		// takes the create or replace path against what is there.
		after, err := a.remoteRef(ref)
		if err != nil || after == "" {
			v := verdict{outcome: "unknown", name: a.label(), required: -1, passed: -1}
			if a.id != "" {
				v.version = a.boundVersion
			}
			if err == nil {
				v.why = "request consumed meanwhile"
			}
			if !a.stopped() {
				fmt.Fprint(a.stdout, v.lines(terminalOf(a.stdout)))
			}
			a.reportEvidence(evidenceRefs)
			return errUnknown
		}
		if after != head {
			a.declined(fmt.Sprintf("another approval filed %s (%s) meanwhile", a.short(after), a.subject(after)),
				act{run: "valley approve " + a.quoted()})
			a.reportEvidence(evidenceRefs)
			return errRefused
		}
		switch {
		case pushed:
			standing = append(standing, "the request already stood at this head; the evidence is what was published")
		case len(unbuilt) > 0:
			standing = append(standing, "the request already stood at this head, and nothing was built or published")
		case len(required) > 0:
			standing = append(standing, "the request already stood at this head and origin already held the evidence, so nothing needed publishing")
		default:
			standing = append(standing, "the request already stood at this head and nothing was required, so nothing needed publishing")
		}
	} else if pending != "" {
		standing = append(standing, fmt.Sprintf("replaced the pending request, which stood at %s (%s)", a.short(pending), a.subject(pending)))
	}

	if a.verbose {
		w := a.stdout
		fmt.Fprintf(w, "  request  %s -> %s\n", ref, a.short(head))
		for _, s := range standing {
			fmt.Fprintf(w, "  %s\n", s)
		}
		if len(required) == 0 {
			fmt.Fprintln(w, "  evidence none — nothing was required, so nothing was signed")
			fmt.Fprintln(w, "  checks   none — the policy requires no check of this change")
		} else {
			fmt.Fprintf(w, "  tree     %s\n", e.digest)
			for _, spec := range evidenceRefs {
				fmt.Fprintf(w, "  evidence %s\n", spec)
			}
			// The refs on origin whose evidence was reused, each once.
			shown := map[string]bool{}
			for _, check := range reused {
				spec, _, _ := strings.Cut(e.covered[check], " ")
				if !shown[spec] {
					shown[spec] = true
					fmt.Fprintf(w, "  reused   %s\n", spec)
				}
			}
			if len(unbuilt) > 0 {
				fmt.Fprintf(w, "  missing  %s\n", strings.Join(unbuilt, ", "))
			}
			fmt.Fprintf(w, "  checks   %s\n", strings.Join(names, ", "))
		}
	}

	// The verdict. Checks with no evidence yet are built by a check
	// worker only for a ready change's active version. Elsewhere approve
	// offers to build them here, unless a command check that ran here put
	// this signer's evidence for the tree on origin, which approve cannot
	// add to, so a plain branch is offered readying instead, which lets a
	// worker build it.
	v := verdict{outcome: "approved", name: a.label(), target: "main", required: len(required),
		passed: len(required) - len(unbuilt), took: took, waiting: unbuilt}
	if a.id != "" {
		v.version = a.boundVersion
	}
	if len(unbuilt) > 0 {
		switch {
		case a.id != "" && head == a.head:
			v.worker = true
		case len(missing) == 0:
			at := ""
			if a.id != "" {
				at = "--head " + a.short(head) + " "
			}
			v.run = "valley approve --build-here " + at + a.quoted()
		case a.id == "":
			v.run = "valley ready " + a.quoted()
		default:
			v.see = "valley status " + a.quoted()
		}
	}
	if !a.stopped() {
		fmt.Fprint(a.stdout, v.lines(terminalOf(a.stdout)))
	}
	a.filedID, a.filedHandle, a.filedVersion = a.id, a.handle, a.boundVersion
	if a.id == "" {
		a.filedVersion = ""
	}
	return nil
}

// derive is what the policy at main's tip requires of the change from
// base to head, and where each layer was read from.
func (a *approval) derive(root, tip, base, head string) (res *derive.Result, label, plabel string, err error) {
	g := a.g
	schema, err := schemaFile(g, "")
	if err != nil {
		return nil, "", "", err
	}
	src, done, err := resolveLayers(g, tip, "", "")
	defer done()
	if err != nil {
		return nil, "", "", fmt.Errorf("%v; nothing derived", err)
	}
	model, err := policyModel(schema, src.instance, src.project, false)
	if err != nil {
		return nil, "", "", err
	}
	paths, err := derive.Diff(context.Background(), g.dir, g.env, base, head)
	if err != nil {
		return nil, "", "", fmt.Errorf("%v; nothing derived", err)
	}
	if res, err = derive.Derive(model, paths); err != nil {
		return nil, "", "", fmt.Errorf("%v; nothing derived", err)
	}
	label, plabel = src.labels(root, "", "", "origin/main "+a.short(tip))
	return res, label, plabel, nil
}

// check runs the missing checks over head in a throwaway worktree, signed
// with this host's key, and returns every attestation ref stored here for
// the head's tree and how long the checks took.
func (a *approval) check(head string, e evidence, required []derive.Required, reused, missing []string, runner map[string]derive.Required) (refs []string, took string, err error) {
	key := os.Getenv("VALLEY_ATTEST_KEY")
	if key == "" {
		return nil, "", a.declined("no signing key", act{then: "set VALLEY_ATTEST_KEY to the ed25519 key this host attests with"})
	}
	if st, err := os.Stat(key); err != nil || !st.Mode().IsRegular() {
		return nil, "", a.declined("no signing key at "+key, act{})
	}
	signer := []string{"--key", key}
	if name := os.Getenv("VALLEY_ATTEST_NAME"); name != "" {
		signer = append(signer, "--name", name)
	}

	// The ref this run's evidence goes to is keyed by the tree and this
	// signer's key hash, and approve pushes it holding this run's notes
	// alone, which origin refuses once it holds the ref. So origin
	// holding it is refused before anything is built.
	keyCmd := exec.Command("attest", append([]string{"key"}, signer...)...)
	keyCmd.Stderr = a.stderr
	processStarted()
	verifier, err := keyCmd.Output()
	if err != nil {
		return nil, "", a.declined("could not read the signing key at "+key, act{})
	}
	_, keyhash, _ := strings.Cut(strings.TrimRight(string(verifier), "\n"), "+")
	keyhash, _, _ = strings.Cut(keyhash, "+")
	if e.listed[attestationRefs+e.digest+"/"+keyhash] != "" {
		return nil, "", a.declined("this key's evidence for the tree, on origin, lacks "+strings.Join(missing, ", "),
			act{then: "attest with another key: set VALLEY_ATTEST_KEY"})
	}

	// attest logs how long each check took, on this machine and outside
	// the evidence. What it cannot know itself is passed in: the change,
	// and each check's declared cost.
	timing := []string{"--timings"}
	if a.id != "" {
		timing = append(timing, "--change", a.id)
	}
	if a.boundVersion != "" {
		timing = append(timing, "--change-version", a.boundVersion)
	}
	var run []string
	for _, check := range missing {
		q := runner[check]
		switch q.Runner {
		case "nix":
			run = append(run, "--check", check+"="+q.Attribute)
		case "command":
			run = append(run, "--command", check+"="+q.Command)
		default:
			return nil, "", a.declined("the policy requires "+check+" but says no way to run it", act{})
		}
		if q.Cost != "" {
			timing = append(timing, "--cost", check+"="+q.Cost)
		}
	}

	wt, err := os.MkdirTemp("", "valley-approve-")
	if err != nil {
		return nil, "", a.declined("no scratch directory to check it in", act{})
	}
	a.mu.Lock()
	a.worktree = wt
	a.mu.Unlock()
	defer a.removeWorktree()
	add := exec.Command("git", "worktree", "add", "--quiet", "--detach", wt, head)
	add.Env, add.Stdout, add.Stderr = a.g.env, a.stderr, a.stderr
	processStarted()
	if add.Run() != nil {
		a.removeWorktree()
		return nil, "", a.declined("could not check the head out to run its checks", act{})
	}

	if a.tel != nil {
		timing = append(timing, "--telemetry", a.tel.spool, "--telemetry-run", a.tel.run+"-checks", "--project", a.tel.project)
		a.tel.emit("approve.checks", map[string]any{"attest_run": a.tel.run + "-checks", "checks": list(missing)})
	}
	v := verdict{name: a.label(), required: len(required), passed: len(reused)}
	if a.id != "" {
		v.version = a.boundVersion
	}
	args := append(append(append([]string{"run", "--repo", wt}, signer...), timing...), run...)
	cmd := exec.Command("attest", args...)
	cmd.Stdin = os.Stdin
	began := time.Now()
	done := make(chan struct{})
	a.mu.Lock()
	a.childDone = done
	a.mu.Unlock()
	status := v.checking(missing, cmd, a.verbose, droppable(a.stdout), droppable(a.stderr), a.started, a.stopped)
	a.mu.Lock()
	a.child = nil
	close(done)
	a.mu.Unlock()
	if a.stopped() || status == 128+int(syscall.SIGINT) || status == 128+int(syscall.SIGTERM) {
		a.interrupt()
	}
	if status != 0 {
		a.failedAt = head
		return nil, "", errRefused
	}
	took = fmt.Sprintf("%ds", int(time.Since(began).Seconds()))
	a.removeWorktree()

	// Every attestation stored here for the tree, whoever signed it: the
	// evidence for this change is what the tree's digest keys, not what
	// this run happened to add.
	out, err := a.g.output(true, "for-each-ref", "--format=%(refname)", attestationRefs+e.digest)
	if err == nil && out != "" {
		refs = strings.Split(out, "\n")
	}
	if len(refs) == 0 {
		return nil, "", a.declined("no attestation is stored for the tree", act{})
	}
	return refs, took, nil
}

// started is told of the attest run checking starts, so a signal can stop
// it.
func (a *approval) started(p *os.Process) {
	a.mu.Lock()
	a.child = p
	a.mu.Unlock()
}

// removeWorktree removes the checks' worktree, if there is one.
func (a *approval) removeWorktree() {
	a.mu.Lock()
	wt := a.worktree
	a.worktree = ""
	a.mu.Unlock()
	if wt == "" {
		return
	}
	remove := exec.Command("git", "worktree", "remove", "--force", wt)
	remove.Env = a.g.env
	_ = remove.Run()
	_ = os.RemoveAll(wt)
}

// signerOf is the signer of a covered check, as coverage maps it to
// "<ref> <signer>".
func signerOf(by string) string {
	_, signer, _ := strings.Cut(by, " ")
	return signer
}

// list is a list as a telemetry record holds one: empty rather than null.
func list(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

// approveTelemetry is an approval attempt's telemetry run, when the
// checkout has valley.telemetry on.
type approveTelemetry struct {
	emitter             *telemetry.Emitter
	run, spool, project string
	began               time.Time
	mu                  sync.Mutex
	about               telemetry.Record
	ended               bool
}

// beginTelemetry starts the attempt's run, bound to the spool of the
// destination the checkout's telemetry remote names, and a flusher that
// sends it. Telemetry off is nil.
func (a *approval) beginTelemetry() *approveTelemetry {
	if on, _ := a.g.output(true, "config", "--get", "--bool", "valley.telemetry"); on != "true" {
		return nil
	}
	dest, err := telemetryRemote(a.g, "")
	if err == nil {
		var dir string
		if dir, err = bindSpool(dest); err == nil {
			version := producerStamp
			if version == "" {
				version = "unstamped"
			}
			t := &approveTelemetry{run: telemetry.NewRunID(), spool: dir, project: dest.project(), began: time.Now()}
			t.emitter = telemetry.NewEmitter(dir, telemetry.Self("valley", version), t.run)
			t.about.Project = t.project
			startFlusher()
			return t
		}
	}
	fmt.Fprintf(a.stderr, "valley: telemetry: %s\n", err)
	return nil
}

// context is what every record of the attempt from here on is about.
func (t *approveTelemetry) context(id, version, head string) {
	if t == nil {
		return
	}
	n, _ := strconv.Atoi(version)
	t.mu.Lock()
	t.about.Change, t.about.Version, t.about.Head = id, n, head
	t.mu.Unlock()
}

// emit records one step of the attempt. elapsed_ms is this machine's wall
// clock since the attempt began.
func (t *approveTelemetry) emit(typ string, data map[string]any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ended {
		return
	}
	data["elapsed_ms"] = time.Since(t.began).Milliseconds()
	t.emitter.Emit(typ, t.about, data)
}

// end records how the attempt ended: filed, refused or interrupted. Only
// the first end is recorded.
func (t *approveTelemetry) end(result string) {
	if t == nil {
		return
	}
	t.emit("approve.end", map[string]any{"result": result})
	t.mu.Lock()
	t.ended = true
	t.mu.Unlock()
}
