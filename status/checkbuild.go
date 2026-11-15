package main

// checks --build asks one question: does origin hold passing evidence for
// every nix check HEAD requires? It asks the host's check worker to build
// what is missing, and waits.
//
// It derives HEAD's checks as the worker derives a check request's
// (worker.go): over HEAD's merge base with main as origin holds it now,
// under both policy layers read there (checks.go). It reads origin's
// evidence for HEAD's tree as approve does (coverage.go); evidence that
// does not count for a check, whatever it records, is no evidence for it.
// When one read covers every check, it passes without filing anything.
//
// Otherwise it takes the worktree's lock, reads or makes the worktree's
// token under it, deletes the requests the worktree left on origin, each
// leased on the commit listed, takes a cursor at the end of the worker's
// telemetry, and files a check request (../change/checkrequest.go) for
// HEAD with a new attempt token. It follows the telemetry from the cursor
// as a wake-up only: the wait ends when a batch for HEAD's tree fails or
// ends unpassed, when a publish for the tree ends (a batch that passed
// publishes before the evidence is on origin, so its batch.end is no
// wake-up), when a pass ends whose pass.start came after the stream's end
// read once the push returned, with no batch for the tree running in
// another pass, or when the stream cannot be read. A pass's records name
// it by the run its emitter writes. A pass ends only once none of its
// batches runs, so its end settles the batches for the tree it started,
// even those whose end the stream lost; a batch another pass runs, as
// another worker writing to the same stream does, holds the wait. The
// cursor comes before the filing so a worker quick to finish is not
// missed. It also reads origin's evidence every evidenceEvery. No record
// is taken as this request's result.
//
// A gap in the stream may hide a wake-up, and ends nothing: a record
// saying its producer dropped records before it (dropped_before), or the
// host saying records since the cursor are gone, makes the wait read
// origin's evidence once the records already received are taken, and go
// on. Reads for gaps and the periodic reads keep one pace, never two
// within evidenceEvery: a gap that comes sooner after a read waits for
// the next periodic one. The worker's records it prints are then
// labelled possibly incomplete. A lost wake-up for evidence that is
// published costs a read's wait; a gap that loses both a failed batch's
// end and its pass's end leaves nothing but the time limit to end the
// wait.
//
// A follow the host refuses for lack of a slot, as when the principal's
// other follows hold every one, ends nothing either: the wait says so
// once, reads origin's evidence and the request's ref every
// evidenceEvery, and follows again from the same cursor after each read.
// Records the stream still holds then arrive; those it no longer holds
// read as a gap. The command deletes its request only once the wait has
// ended, so a request origin lists gone or moved during the wait was
// deleted or moved by another, as a holder of the approve grant may. The
// worker starts no batch for a request origin does not list at its
// commit, and stops a running one unless a ready change holds the tree;
// with no follow to show which, such a request ends the wait.
//
// It has two outcomes. Passed (exit 0) only when one read, the last,
// completes and covers every check. Anything else is not established
// (exit 1): it says why it stopped, and prints the check.result records
// the worker wrote for HEAD's tree, labelled as such, with the nix log
// command that reads each failed check's build log from the derivation
// its result names, the error and build error its result records, or
// else the command
// that builds that check here.
//
// The command runs as a child of itself in a process group of its own.
// The parent fixes the deadline (--wait) before the child starts, and
// kills the child's group when the child ends, the deadline passes or a
// signal comes; a program that made a group of its own escapes that. The
// child tells the parent on a pipe, in validated values only, what it
// filed or is deleting, and whether it passed; the parent deletes, leased,
// what the child left, and exits 0 only on a reported pass. A push that
// did not report success leaves the request's existence unknown: it is
// deleted, leased, and unless origin then lists it absent after a push
// origin refused, the leased delete is printed.

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"the-valley/change"
	"the-valley/telemetry"
)

const checkBuildUsage = "usage: valley checks --build [--wait <duration>]"

const (
	// checkBuildChild is the verb the parent runs the command as, with
	// the state pipe at the descriptor checkBuildState names.
	checkBuildChild = "checks-build-child"
	checkBuildState = "VALLEY_CHECK_BUILD_STATE"
	// What the command keeps in a worktree's git directory.
	worktreeTokenFile = "valley-check-request"
	requestLockFile   = "valley-check-request.lock"
)

// defaultWait is --wait's default. A ready change has first claim on the
// worker, so a request can wait behind the host's whole queue; two hours
// outlasts a busy host's queue and one cold build of a large check set.
const defaultWait = 2 * time.Hour

// evidenceEvery is how often the wait reads origin's evidence whatever the
// telemetry says, since the stream can drop a wake-up unseen; cleanupBound
// is how long deleting the request may take.
const (
	evidenceEvery = 3 * time.Minute
	cleanupBound  = 30 * time.Second
)

// errorRunes is how much of a result's error is kept: attest clips the
// errors it records shorter, so this bounds only what another writer put
// in the worker's stream.
const errorRunes = 400

// runCheckBuild is valley checks --build: the parent, which watches the
// child that does the work.
func runCheckBuild(g git, args []string, stdout, stderr io.Writer) int {
	wait, err := buildWait(args)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %v\n%s\n", err, checkBuildUsage)
		return 1
	}
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "valley: %v\n", err)
		return 1
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	return watch(g, self, wait, stdout, stderr, signals)
}

// buildMode reports whether a checks command line asks for --build.
func buildMode(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--build" {
			return true
		}
	}
	return false
}

// buildWait reads a checks --build command line: --build, and --wait with
// a positive duration.
func buildWait(args []string) (time.Duration, error) {
	wait := defaultWait
	for i := 0; i < len(args); i++ {
		opt, value, inline := strings.Cut(args[i], "=")
		switch {
		case args[i] == "--build":
		case opt == "--wait":
			if !inline {
				if i+1 >= len(args) {
					return 0, errors.New("--wait needs a duration, such as 30m")
				}
				i++
				value = args[i]
			}
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return 0, fmt.Errorf("--wait takes a positive duration, such as 30m or 2h, not %q", value)
			}
			wait = d
		default:
			return 0, errors.New("--build works on HEAD, and takes no option but --wait and no revision")
		}
	}
	return wait, nil
}

// watch runs self as the child, and returns the exit status. When wait
// passes or a signal comes first, it kills the child's process group,
// deletes the request the child filed and did not delete, and says the
// outcome is not established.
func watch(g git, self string, wait time.Duration, stdout, stderr io.Writer, signals <-chan os.Signal) int {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	r, w, err := os.Pipe()
	if err != nil {
		fmt.Fprintf(stderr, "valley: %v\n", err)
		return 1
	}
	defer r.Close()
	cmd := exec.Command(self, checkBuildChild)
	cmd.Env = append(os.Environ(), checkBuildState+"=3")
	cmd.Stdout, cmd.Stderr, cmd.ExtraFiles = stdout, stderr, []*os.File{w}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err = cmd.Start()
	w.Close()
	if err != nil {
		fmt.Fprintf(stderr, "valley: %v\n", err)
		return 1
	}
	// The pipe ends when the child does, since no program it starts holds
	// it. The child is then unreaped, so its group can still be killed,
	// with whatever the child left running.
	var s childState
	read := make(chan struct{})
	go func() { s.read(r); close(read) }()
	why := ""
	select {
	case <-read:
	case <-deadline.C:
		why = "time limit"
	case <-signals:
		why = "interrupted"
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	err = cmd.Wait()
	<-read
	s.settle(g, stderr, signals)
	var exit *exec.ExitError
	switch {
	case s.err != nil || err != nil && !errors.As(err, &exit):
		fmt.Fprintf(stderr, "valley: the command did not end cleanly (%v), so whether its requests are deleted is not established\n", oneLine(errors.Join(err, s.err).Error()))
		return 1
	case why == "" && err == nil && s.passed:
		return 0
	case why == "" && err == nil:
		fmt.Fprintln(stderr, "valley: the command ended without an outcome")
		return 1
	case why == "":
		return exit.ExitCode()
	}
	t := terminalOf(stdout)
	subject := s.short
	if subject == "" {
		subject = "HEAD"
	}
	fmt.Fprintln(stdout, t.tint(colourWaiting, "◇")+" "+t.strong(subject)+"  not established · "+why)
	report(stdout, s.results, s.attrs, s.lost)
	if why == "interrupted" {
		return interruptedStatus
	}
	return 1
}

// childState is what the child told the parent. settled means the child
// tried to delete its request, and said so when it could not; lost that
// the worker's stream had a gap while it waited; stale is each earlier
// request whose delete the child started and did not end.
type childState struct {
	short, ref, head, pushed string
	settled, passed, lost    bool
	attrs, stale             map[string]string
	results                  []treeResult
	err                      error
}

// treeResult is a check.result the worker wrote for HEAD's tree: drv is
// the derivation whose build decided it, a validated store path or "";
// err the error it records and buildErr how its nix build ended, both
// untrusted. A build that ended before its builder started leaves nix no
// log, and buildErr is then the whole diagnosis.
type treeResult struct {
	run, check, result string
	ms                 int64
	drv, err, buildErr string
}

func (s *childState) read(r io.Reader) {
	s.attrs, s.stale = map[string]string{}, map[string]string{}
	sc := bufio.NewScanner(r)
	defer func() { s.err = sc.Err() }()
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 2 && f[0] == "head":
			s.short = f[1]
		case len(f) == 3 && f[0] == "check":
			s.attrs[f[1]] = f[2]
		case len(f) == 8 && f[0] == "result":
			ms, _ := strconv.ParseInt(f[4], 10, 64)
			r := treeResult{run: f[1], check: f[2], result: f[3], ms: ms, err: decoded(f[6]), buildErr: decoded(f[7])}
			if storeDrv.MatchString(f[5]) {
				r.drv = f[5]
			}
			s.results = append(s.results, r)
		case len(f) == 3 && f[0] == "filing":
			s.ref, s.head, s.pushed, s.settled = f[1], f[2], "unknown", false
		case len(f) == 2 && f[0] == "pushed":
			s.pushed = f[1]
		case len(f) == 1 && f[0] == "settled":
			s.settled = true
		case len(f) == 1 && f[0] == "passed":
			s.passed = true
		case len(f) == 1 && f[0] == "lost":
			s.lost = true
		case len(f) == 3 && f[0] == "stale":
			s.stale[f[1]] = f[2]
		case len(f) == 2 && f[0] == "cleared":
			delete(s.stale, f[1])
		}
	}
}

// settle deletes the requests the child left, within cleanupBound or until
// a second signal.
func (s *childState) settle(g git, stderr io.Writer, signals <-chan os.Signal) {
	if (s.ref == "" || s.settled) && len(s.stale) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBound)
	defer cancel()
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()
	if s.ref != "" && !s.settled && !deleteRequest(ctx, g, true, s.ref, s.head, s.pushed) {
		unsettled(stderr, s.ref, s.head)
	}
	for ref, oid := range s.stale {
		if !deleteRequest(ctx, g, true, ref, oid, "ok") {
			unsettled(stderr, ref, oid)
		}
	}
}

// deleteRequest deletes ref on origin, leased on head, after a push that
// reported pushed: ok, rejected or unknown. It reports whether origin is
// known not to hold the request: the delete went through, or origin lists
// it absent and the push did not leave its existence unknown.
func deleteRequest(ctx context.Context, g git, group bool, ref, head, pushed string) bool {
	if pushed != "rejected" {
		if result, _ := pushOrigin(ctx, g, group, "--force-with-lease="+ref+":"+head, ":"+ref); result == "ok" {
			return true
		}
	}
	out, _, err := gitIn(ctx, g, group, "ls-remote", "--refs", "origin", ref)
	return err == nil && !strings.Contains(out, "\t"+ref+"\n") && pushed != "unknown"
}

func unsettled(stderr io.Writer, ref, head string) {
	fmt.Fprintf(stderr, "valley: whether origin still holds the check request is not established\n  run: git push --force-with-lease=%s:%s origin :%s\n", ref, head, ref)
}

// pushOrigin pushes spec to origin, and says what origin reported: ok,
// rejected when it refused the update, or unknown when the push ended
// without saying, as a transport that failed does.
func pushOrigin(ctx context.Context, g git, group bool, lease, spec string) (string, error) {
	out, errs, err := gitIn(ctx, g, group, "push", "--porcelain", lease, "origin", spec)
	if err == nil {
		return "ok", nil
	}
	var said []string
	for _, line := range strings.Split(errs, "\n") {
		if remote, ok := strings.CutPrefix(line, "remote: "); ok && strings.TrimSpace(remote) != "" {
			said = append(said, strings.TrimSpace(remote))
		}
	}
	if len(said) == 0 {
		said = []string{strings.TrimSpace(errs)}
	}
	return pushStatus(out, spec[strings.Index(spec, ":")+1:]), errors.New(strings.Join(said, "; "))
}

// pushStatus is what git push --porcelain reported for ref: rejected only
// when its line rejects that ref, and unknown otherwise, as for a remote
// that failed to report status.
func pushStatus(out, ref string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) == 3 && f[0] == "!" && strings.HasSuffix(f[1], ":"+ref) &&
			(strings.HasPrefix(f[2], "[rejected]") || strings.HasPrefix(f[2], "[remote rejected]")) {
			return "rejected"
		}
	}
	return "unknown"
}

// gitIn runs git under ctx. In a group of its own, ending ctx kills the
// group; the parent runs its git so, while the child runs its git in the
// child's group, which the parent kills whole.
func gitIn(ctx context.Context, g git, group bool, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env, cmd.Dir = g.env, g.dir
	if group {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	}
	cmd.WaitDelay = 2 * time.Second
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	processStarted()
	err = cmd.Run()
	return out.String(), errs.String(), err
}

// runCheckBuildChild is the child: the command's work.
func runCheckBuildChild(g git, args []string, stdout, stderr io.Writer) int {
	if os.Getenv(checkBuildState) != "3" || len(args) != 0 {
		fmt.Fprintf(stderr, "valley: unknown verb: %s\n", checkBuildChild)
		return 1
	}
	syscall.CloseOnExec(3)
	a := newAsking(g, stdout, stderr)
	a.states = os.NewFile(3, "state")
	status := a.run()
	if status == 0 {
		a.state("passed")
	}
	return status
}

// asking is the child's work.
type asking struct {
	g              git
	stdout, stderr io.Writer
	t              terminal
	states         io.Writer // the pipe to the parent
	began          time.Time
	readEvery      time.Duration
	now            func() time.Time    // the wait's clock, which paces its reads
	sleep          func(time.Duration) // waits on that clock
	lock           *os.File            // the worktree's lock, held while the process runs

	head, short, project, tree string
	mark                       int64 // the stream's newest record once the request was filed
	required                   int
	nix                        []nixCheck
	commands                   []string
	ref                        string
	results                    []treeResult
	// lost is whether the worker's stream had a gap during the wait, and
	// unread whether origin's evidence has gone unread since a gap.
	lost, unread bool

	derive func() error
	cover  func(checks []nixCheck) (evidence, error)
	stream workerStream
}

func newAsking(g git, stdout, stderr io.Writer) *asking {
	a := &asking{g: g, stdout: stdout, stderr: stderr, t: terminalOf(stdout), states: io.Discard,
		began: time.Now(), readEvery: evidenceEvery, now: time.Now, sleep: time.Sleep}
	a.derive = a.prepare
	a.cover = func(checks []nixCheck) (evidence, error) {
		if _, err := exec.LookPath("attest"); err != nil {
			return evidence{}, failure("the attest helper is not installed; nix run .#valley bundles it")
		}
		quiet := a.g
		quiet.stderr = io.Discard
		return coverageOf(quiet, a.head, checks, io.Discard)
	}
	return a
}

func (a *asking) run() int {
	if err := a.derive(); err != nil {
		return a.fail(err)
	}
	a.state("head", a.short)
	if a.required == 0 {
		a.say("✓", colourDone, a.short, "no checks required")
		return 0
	}
	for _, c := range a.nix {
		a.state("check", c.name, c.attribute)
	}
	covered, err := a.read()
	if err != nil {
		return a.fail(err)
	}
	if len(covered) == len(a.nix) {
		return a.verdict(covered, nil, "")
	}
	principal, err := a.g.output(true, "config", "valley.principal")
	if err != nil || !change.ValidPrincipal(principal) {
		return a.fail(failure("a check request is filed under the principal your key pushes as, and git config valley.principal names none; set it with git config valley.principal <name>"))
	}
	token, err := a.claim(principal)
	if err != nil {
		return a.fail(err)
	}
	a.recoverStale(principal, token)
	a.say("◐", colourWorking, a.short, countOf(len(a.nix), "nix check")+" required",
		fmt.Sprintf("%d with evidence", len(covered)), "asking the worker")
	cursor, err := a.stream.end(context.Background())
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(err.Error()))
		covered, err = a.read()
		return a.verdict(covered, err, "telemetry unreadable")
	}
	attempt, err := change.NewCheckRequestToken()
	if err != nil {
		return a.fail(err)
	}
	a.ref, _ = change.CheckRequestRef(principal, token, attempt)
	a.state("filing", a.ref, a.head)
	pushed, err := pushOrigin(context.Background(), a.g, false, "--force-with-lease="+a.ref+":", a.head+":"+a.ref)
	a.state("pushed", pushed)
	why := ""
	covered = nil
	switch pushed {
	case "rejected":
		fmt.Fprintf(a.stderr, "valley: origin refused the check request: %s\n", oneLine(err.Error()))
		why = "the request was refused"
	case "unknown":
		fmt.Fprintf(a.stderr, "valley: the check request's push did not finish: %s\n", oneLine(err.Error()))
		why = "the request's push did not finish"
	default:
		why, covered = a.wait(cursor)
	}
	var readErr error
	if covered == nil {
		covered, readErr = a.read()
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBound)
	defer cancel()
	if !deleteRequest(ctx, a.g, false, a.ref, a.head, pushed) {
		unsettled(a.stderr, a.ref, a.head)
	}
	a.state("settled")
	return a.verdict(covered, readErr, why)
}

// state tells the parent one fact. A value outside stateValue, which no
// newline or space is in, is never sent.
func (a *asking) state(fields ...string) {
	for _, f := range fields {
		if !stateValue.MatchString(f) {
			return
		}
	}
	fmt.Fprintln(a.states, strings.Join(fields, " "))
}

var (
	stateValue = regexp.MustCompile(`^[A-Za-z0-9._/:@+?=-]+$`)
	runName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`) // telemetry's run names
	// storeDrv is a derivation's path in the nix store, the only form of
	// a check.result's drv printed as a command to run.
	storeDrv = regexp.MustCompile(`^/nix/store/[0-9a-df-np-sv-z]{32}-[A-Za-z0-9+_?=-][A-Za-z0-9+._?=-]{0,210}\.drv$`)
)

// read reads origin's evidence for HEAD's tree, and returns the checks it
// covers.
func (a *asking) read() (map[string]bool, error) {
	e, err := a.cover(a.nix)
	if err != nil {
		return nil, fmt.Errorf("could not read the evidence origin holds for HEAD's tree: %v", err)
	}
	a.tree = e.digest
	covered := map[string]bool{}
	for _, c := range a.nix {
		if _, ok := e.covered[c.name]; ok {
			covered[c.name] = true
		}
	}
	return covered, nil
}

// claim takes the worktree's lock, held until the process ends, and reads
// or makes the worktree's token under it.
func (a *asking) claim(principal string) (string, error) {
	gitDir, err := a.g.output(true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", failure("not a git repository")
	}
	lock, err := os.OpenFile(filepath.Join(gitDir, requestLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return "", failure("another valley checks --build is running in this worktree")
	}
	a.lock = lock
	path := filepath.Join(gitDir, worktreeTokenFile)
	if data, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(data))
		if _, ok := change.CheckRequestRef(principal, token, token); ok {
			return token, nil
		}
		return "", failure(path + " holds no check request token; remove it, and the next run makes one")
	} else if !os.IsNotExist(err) {
		return "", err
	}
	token, err := change.NewCheckRequestToken()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".new", []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, os.Rename(path+".new", path)
}

// recoverStale deletes the requests origin lists under the worktree's
// token, each leased on the commit listed. One it cannot delete is said,
// with the leased delete.
func (a *asking) recoverStale(principal, token string) {
	prefix, _ := change.CheckRequestRef(principal, token, token)
	prefix = strings.TrimSuffix(prefix, token)
	out, err := a.g.output(true, "ls-remote", "--refs", "origin", prefix+"*")
	if err != nil {
		fmt.Fprintf(a.stderr, "valley: could not list this worktree's earlier check requests on origin: %s\n", oneLine(err.Error()))
		return
	}
	for _, line := range strings.Split(out, "\n") {
		oid, ref, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(ref, prefix) {
			continue
		}
		if _, _, _, valid := change.ParseCheckRequestRef(ref); !valid || !isObjectID(oid) {
			fmt.Fprintf(a.stderr, "valley: %s is no check request, so it is skipped\n", oneLine(ref))
			continue
		}
		a.state("stale", ref, oid)
		if result, _ := pushOrigin(context.Background(), a.g, false, "--force-with-lease="+ref+":"+oid, ":"+ref); result != "ok" {
			unsettled(a.stderr, ref, oid)
		}
		a.state("cleared", ref)
	}
}

// wait follows the worker's telemetry from cursor, taken before the
// filing, until a wake-up, reading origin's evidence every a.readEvery and
// after a gap, never twice within a.readEvery. While the host refuses the
// follow for lack of a slot, it reads origin every a.readEvery instead,
// and follows again after each read. It returns why it stopped, or the
// read that covered every check.
func (a *asking) wait(cursor string) (string, map[string]bool) {
	// Where the stream ended once the push returned: a pass that starts
	// after it started after the filing.
	mark, err := a.stream.end(context.Background())
	if c, perr := telemetry.ParseCursor(mark); err == nil && perr == nil && c[workerSource] != (telemetry.Position{}) {
		a.mark = c[workerSource].Seq
	} else {
		fmt.Fprintf(a.stderr, "valley: the worker's stream gave no position after the filing: %s\n", oneLine(fmt.Sprint(err)))
		return "telemetry unreadable", nil
	}
	a.say("◇", colourWaiting, a.short, "waiting for the worker")
	started, running := map[string]bool{}, map[string]map[string]bool{}
	// last is when the wait last read origin's evidence, or began, and
	// paces the periodic reads; lastRead is when it last read, zero before
	// its first, and paces the reads for gaps. Every read sets both.
	last, lastRead := a.now(), time.Time{}
	// refused is whether the host refused the last follow for lack of a
	// slot; said whether the wait has said so.
	refused, said := false, false
	for {
		if refused {
			// Nothing tells the wait when a slot frees or evidence lands,
			// so it keeps the periodic reads' pace.
			a.sleep(a.readEvery - a.now().Sub(last))
			last, lastRead = a.now(), a.now()
			if covered := a.covering(); covered != nil {
				return "", covered
			}
			if why := a.requestGone(); why != "" {
				return why, nil
			}
		}
		seconds := int(max(a.readEvery-a.now().Sub(last), 0)/time.Second) + 1
		ctx, cancel := context.WithCancel(context.Background())
		records := make(chan workerRecord, 256)
		var next string
		var ferr error
		go func() {
			defer close(records)
			next, ferr = a.stream.follow(ctx, cursor, seconds, records)
		}()
		why, covered := "", map[string]bool(nil)
		for r := range records {
			if why != "" || covered != nil {
				continue
			}
			why = a.wake(r, started, running)
			// A gap is read for once no received record waits, so a
			// burst of gaps costs one read, and only when the wait has
			// not read within a.readEvery, so gaps that come one at a
			// time wait for the periodic read.
			if why == "" && a.unread && len(records) == 0 && a.now().Sub(lastRead) >= a.readEvery {
				last, lastRead = a.now(), a.now()
				covered = a.covering()
			}
			if why != "" || covered != nil {
				cancel()
			}
		}
		cancel()
		var busy noSlot
		switch {
		case covered != nil:
			return "", covered
		case why != "":
			return why, nil
		case errors.As(ferr, &busy):
			if !said {
				fmt.Fprintf(a.stderr, "valley: %s · waiting by reading origin every %s\n", oneLine(ferr.Error()), a.readEvery)
			}
			refused, said = true, true
			continue
		case ferr != nil:
			fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(ferr.Error()))
			return "telemetry unreadable", nil
		}
		refused = false
		cursor = next
		if a.now().Sub(last) >= a.readEvery {
			last, lastRead = a.now(), a.now()
			if covered := a.covering(); covered != nil {
				return "", covered
			}
		}
	}
}

// covering reads origin's evidence during the wait, and returns the read
// when it covers every check, or nil.
func (a *asking) covering() map[string]bool {
	a.unread = false
	if covered, err := a.read(); err == nil && len(covered) == len(a.nix) {
		return covered
	}
	return nil
}

// requestGone reads the request's ref on origin, and says why the wait
// ends when origin lists it gone or naming another commit, as after a
// holder of the approve grant deleted it: the command itself deletes it
// only after the wait. A listing that fails says nothing.
func (a *asking) requestGone() string {
	out, err := a.g.output(true, "ls-remote", "--refs", "origin", a.ref)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if oid, ref, _ := strings.Cut(line, "\t"); ref == a.ref && oid == a.head {
			return ""
		}
	}
	return "the check request is gone from origin"
}

// wake takes one worker record, keeps a check.result for the tree, notes
// a gap, and says why the wait should end, or "". started is whether each
// pass started after the filing, and running each batch for the tree the
// stream showed a pass start and not end, both by the pass's run.
func (a *asking) wake(r workerRecord, started map[string]bool, running map[string]map[string]bool) string {
	if n, _ := r.Data["dropped_before"].(float64); n > 0 || r.gap {
		if !a.lost {
			a.state("lost")
		}
		a.lost, a.unread = true, true
	}
	switch {
	case r.gap:
		return ""
	case r.Type == "pass.start":
		started[r.Run] = r.Seq > a.mark
		return ""
	case r.Type == "pass.end":
		delete(running, r.Run)
		if started[r.Run] && len(running) == 0 {
			return "a worker pass ended"
		}
		return ""
	case r.Project != a.project || r.Tree != a.tree:
		return ""
	}
	run, _ := r.Data["attest_run"].(string)
	result, _ := r.Data["result"].(string)
	switch r.Type {
	case "batch.start":
		if running[r.Run] == nil {
			running[r.Run] = map[string]bool{}
		}
		running[r.Run][run] = true
	case "check.result":
		if _, required := a.attrs()[r.Check]; !required || !runName.MatchString(r.Run) ||
			result != checkPassed && result != checkFailed && result != "error" {
			return ""
		}
		ms := int64(-1)
		for _, key := range []string{"check_ms", "build_ms"} {
			if v, ok := r.Data[key].(float64); ok {
				ms = int64(v)
				break
			}
		}
		t := treeResult{run: r.Run, check: r.Check, result: result, ms: ms}
		if drv, _ := r.Data["drv"].(string); storeDrv.MatchString(drv) {
			t.drv = drv
		}
		t.err, t.buildErr = errorText(r.Data["error"]), errorText(r.Data["build_error"])
		a.results = append(a.results, t)
		// "-" is no derivation, and the errors, untrusted, go encoded.
		a.state("result", r.Run, r.Check, result, strconv.FormatInt(ms, 10),
			cmp.Or(t.drv, "-"), encoded(t.err), encoded(t.buildErr))
	case "batch.end":
		if delete(running[r.Run], run); len(running[r.Run]) == 0 {
			delete(running, r.Run)
		}
		// A batch that passed publishes next, and its publish.end says so.
		if result == checkFailed {
			return "the worker's batch for this tree ended with failures"
		} else if result != checkPassed {
			return "the worker's batch for this tree ended"
		}
	case "publish.end":
		if result == checkPassed {
			return "the worker published for this tree"
		}
		return "the worker could not publish for this tree"
	}
	return ""
}

// verdict prints the outcome, and returns the exit status.
func (a *asking) verdict(covered map[string]bool, readErr error, why string) int {
	took := time.Since(a.began).Round(time.Second).String()
	if readErr == nil && len(covered) == len(a.nix) {
		clauses := []string{countOf(len(a.nix), "check") + " passed"}
		if len(a.nix) == 0 {
			clauses = []string{"no nix checks required"}
		}
		if len(a.commands) > 0 {
			clauses = append(clauses, fmt.Sprintf("%d run at approval", len(a.commands)))
		}
		a.say("✓", colourDone, a.short, append(clauses, took)...)
		return 0
	}
	if readErr != nil {
		fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(readErr.Error()))
		why = strings.TrimPrefix(why+" · evidence unreadable", " · ")
	}
	for _, c := range a.nix {
		if !covered[c.name] {
			a.say("◇", colourWaiting, c.name, "no evidence on origin")
		}
	}
	a.say("◇", colourWaiting, a.short, "not established", why,
		fmt.Sprintf("%d of %d with evidence", len(covered), len(a.nix)), took)
	report(a.stdout, a.results, a.attrs(), a.lost)
	return 1
}

// attrs is each required nix check's flake attribute, by check.
func (a *asking) attrs() map[string]string {
	attrs := map[string]string{}
	for _, c := range a.nix {
		attrs[c.name] = c.attribute
	}
	return attrs
}

// report prints the check.results the worker wrote for HEAD's tree while
// the command waited, which any batch for the tree may have written, and
// with lost says the stream had a gap, so some may be missing. For
// each failed check it prints the error its result records, and the nix
// log command that reads the build log of the derivation it names from
// the worker's nix store, or, with neither, the command that builds the
// check here. With any failed check it points at the worker's output:
// nix holds no log for a check whose dependency failed to build, and the
// worker's output names that dependency.
func report(w io.Writer, results []treeResult, attrs map[string]string, lost bool) {
	if lost {
		fmt.Fprintln(w, "  the worker's stream lost records, so those for this tree may be incomplete")
	}
	if len(results) > 0 {
		fmt.Fprintln(w, "  the worker's recent records for this tree, not this request's result:")
	}
	failed := map[string]treeResult{}
	var order []string
	for _, r := range results {
		took := ""
		if r.ms >= 0 {
			took = " · " + duration(r.ms)
		}
		fmt.Fprintf(w, "    %s  %s%s\n", oneLine(r.check), oneLine(r.result), took)
		if r.result != checkPassed {
			if _, seen := failed[r.check]; !seen {
				order = append(order, r.check)
			}
			failed[r.check] = r
		}
	}
	sort.Strings(order)
	for _, name := range order {
		r := failed[name]
		line := "  " + oneLine(name) + " · " + oneLine(r.result)
		if r.err != "" {
			line += " · the worker's error, untrusted: " + oneLine(r.err)
		}
		if r.buildErr != "" {
			line += " · the worker's build error, untrusted: " + oneLine(r.buildErr)
		}
		fmt.Fprintln(w, line)
		switch attr := attrs[name]; {
		case r.drv != "":
			// A store name may hold ? and =, which a shell expands;
			// storeDrv admits no quote, so single quotes keep it whole.
			drv := r.drv
			if strings.ContainsAny(drv, "?=") {
				drv = "'" + drv + "'"
			}
			fmt.Fprintf(w, "  run: nix log %s\n", drv)
		case r.err == "" && attr != "":
			fmt.Fprintf(w, "  run: nix build --no-link -L .#checks.%s.%s\n", buildSystem(), attr)
		}
	}
	if len(order) > 0 {
		fmt.Fprintln(w, "  see: valley telemetry read --source worker --logs · when nix log has none")
	}
}

// errorText is a record's error, bounded by errorRunes, or "".
func errorText(v any) string {
	e, _ := v.(string)
	if runes := []rune(e); len(runes) > errorRunes {
		e = string(runes[:errorRunes]) + "…"
	}
	return e
}

// encoded is untrusted text as one state value, "-" when empty; decoded
// reads it back.
func encoded(s string) string {
	return cmp.Or(base64.RawURLEncoding.EncodeToString([]byte(s)), "-")
}

func decoded(v string) string {
	if b, err := base64.RawURLEncoding.DecodeString(v); v != "-" && err == nil {
		return string(b)
	}
	return ""
}

// oneLine is untrusted text made safe to show on one line.
func oneLine(s string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(telemetry.Sanitize(s))
}

// buildSystem is the flake system attest builds a check for here
// (attest/check.go).
func buildSystem() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64", "386": "i686"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	return arch + "-" + runtime.GOOS
}

// prepare reads HEAD, fetches main from origin, and derives the checks
// HEAD requires under the policy at that tip.
func (a *asking) prepare() error {
	g := a.g
	head, err := commitOf(g, "HEAD")
	if err != nil {
		return err
	}
	a.head = head
	if a.short, err = g.output(true, "rev-parse", "--short", head); err != nil {
		return failure("could not read HEAD")
	}
	if changed, err := g.output(true, "status", "--porcelain"); err == nil && changed != "" {
		fmt.Fprintln(a.stderr, "valley: the working tree has uncommitted changes; these are HEAD's checks, not the working tree's")
	}
	// The worker derives under main as origin holds it now; a local copy
	// may be older, with another policy or another merge base.
	if _, err := g.output(false, "fetch", "--quiet", "origin", "+refs/heads/main:refs/remotes/origin/main"); err != nil {
		return failure("could not fetch main from origin; nothing derived")
	}
	tip, ok, err := g.ref("refs/remotes/origin/main")
	if err != nil || !ok {
		return failure("could not read origin/main; nothing derived")
	}
	base, err := g.output(true, "merge-base", tip, head)
	if err != nil || !isObjectID(base) {
		return failure("HEAD shares no history with main on origin; nothing derived")
	}
	instance, layer, done, err := policyLayers(g, tip)
	defer done()
	if err != nil {
		return failure(err.Error() + "; nothing derived")
	}
	doc, err := derivedChecks(g, "", instance, layer, base, head)
	if err != nil {
		return err
	}
	a.required = len(doc.Required)
	for _, q := range doc.Required {
		if q.Runner == "nix" {
			a.nix = append(a.nix, nixCheck{q.Name, q.Attribute})
		} else {
			a.commands = append(a.commands, q.Name)
		}
	}
	a.project = project(g)
	if a.stream == nil {
		if dest, err := telemetryRemote(g, ""); err == nil {
			a.stream = sshStream{dest}
		} else {
			a.stream = brokenStream{err}
		}
	}
	return nil
}

// say prints a line: the glyph in its colour, the subject in bold, and the
// clauses.
func (a *asking) say(glyph, colour, subject string, clauses ...string) {
	var kept []string
	for _, c := range clauses {
		if c != "" {
			kept = append(kept, c)
		}
	}
	fmt.Fprintln(a.stdout, a.t.tint(colour, glyph)+" "+a.t.strong(subject)+"  "+strings.Join(kept, " · "))
}

func (a *asking) fail(err error) int {
	fmt.Fprintf(a.stderr, "valley: %s\n", oneLine(err.Error()))
	return 1
}

// workerRecord is one record of the worker's telemetry stream.
type workerRecord struct {
	Seq     int64          `json:"seq"`
	Type    string         `json:"type"`
	Run     string         `json:"run"`
	Project string         `json:"project"`
	Tree    string         `json:"tree"`
	Check   string         `json:"check"`
	Data    map[string]any `json:"data"`
	// gap is set, on a record holding nothing else, where the host said
	// the stream lost records since the cursor.
	gap bool
}

// workerStream reads the worker's telemetry.
type workerStream interface {
	// end is a cursor at the stream's newest record.
	end(ctx context.Context) (string, error)
	// follow hands records each record stored after cursor, for up to
	// seconds, and returns the cursor it ended at.
	follow(ctx context.Context, cursor string, seconds int, records chan<- workerRecord) (string, error)
}

// noSlot is the host's refusal of a request for lack of a slot: the
// principal already has every request of its kind open that it may, and
// one of them ending admits it. The host's word is taken: a refusal of
// that shape keeps the wait going, up to --wait, and never makes it
// pass, which only evidence on origin does.
type noSlot struct{ why string }

func (e noSlot) Error() string { return e.why }

// brokenStream is a stream that cannot be reached, and why.
type brokenStream struct{ err error }

func (b brokenStream) end(context.Context) (string, error) { return "", b.err }
func (b brokenStream) follow(context.Context, string, int, chan<- workerRecord) (string, error) {
	return "", b.err
}

// sshStream reads the worker source of a host over ssh, as valley
// telemetry does.
type sshStream struct{ dest sshDest }

func (s sshStream) end(ctx context.Context) (string, error) {
	cursor, err := s.answer(ctx, telemetry.Request{V: 1, Op: telemetry.OpRead, Source: workerSource, Limit: 1}, time.Minute, nil)
	if c, perr := telemetry.ParseCursor(cursor); err == nil && (perr != nil || c[workerSource] == (telemetry.Position{})) {
		err = fmt.Errorf("%s holds no stream for its %s source", s.dest.host, workerSource)
	}
	return cursor, err
}

func (s sshStream) follow(ctx context.Context, cursor string, seconds int, records chan<- workerRecord) (string, error) {
	req := telemetry.Request{V: 1, Op: telemetry.OpFollow, Source: workerSource, Cursor: cursor, Seconds: min(seconds, telemetry.MaxFollowSeconds)}
	return s.answer(ctx, req, time.Duration(req.Seconds+60)*time.Second, func(r workerRecord) {
		select {
		case records <- r:
		case <-ctx.Done():
		}
	})
}

// answer runs one telemetry request on the host, hands each record of
// the worker's stream it answers with to onRecord, and each gap in it as
// a record marked so, and returns the cursor it ended at. A refusal, a
// line that is not JSON, a source that could not be read, or an answer
// that never ends is an error; a refusal for lack of a slot is a noSlot.
func (s sshStream) answer(ctx context.Context, req telemetry.Request, timeout time.Duration, onRecord func(workerRecord)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := sshCommand(ctx, s.dest, req.Command())
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var sshErr bytes.Buffer
	cmd.Stderr = &sshErr
	processStarted()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not run ssh: %v", err)
	}
	cursor, ended, why, busy := "", false, "", false
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), telemetry.MaxResponseBytes)
	for why == "" && sc.Scan() {
		var m struct {
			Kind, Error, What, Cursor, Stream string
			workerRecord
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			why = "an answer line is not JSON"
			break
		}
		switch {
		case m.Kind == "error":
			why, busy = "the host refused: "+telemetry.Sanitize(m.Error), telemetry.IsSlotRefusal(m.Error)
		case m.Kind == "unavailable" && m.What != "journal":
			why = fmt.Sprintf("the host could not read %s: %s", telemetry.Sanitize(m.What), telemetry.Sanitize(m.Error))
		case m.Kind == "gap" && m.Stream == workerSource && onRecord != nil:
			onRecord(workerRecord{gap: true})
		case m.Kind == "record" && m.Stream == workerSource && onRecord != nil:
			onRecord(m.workerRecord)
		case m.Kind == "end":
			cursor, ended = m.Cursor, true
		}
	}
	if why != "" {
		cancel()
	}
	waitErr := cmd.Wait()
	switch {
	case busy:
		return "", noSlot{why}
	case why != "":
		return "", errors.New(why)
	case !ended:
		why = strings.TrimSpace(telemetry.Sanitize(sshErr.String()))
		if why == "" && waitErr != nil {
			why = waitErr.Error()
		}
		return "", fmt.Errorf("the telemetry answer from %s did not end: %s", s.dest.host, why)
	}
	return cursor, nil
}
