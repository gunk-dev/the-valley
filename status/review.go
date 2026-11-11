package main

// valley review: a change, or a stack layer by layer, paged for the
// operator to approve, reject or skip. With no change it walks what needs
// the operator (walk.go), and each step of the walk is this review of one
// change, called with the step's repository.
//
// A change is resolved before origin is fetched, as approve resolves it,
// and shown at the head its record names, or a plain branch at its tip on
// origin. A stack is read first (stack.go), and reviewed one layer at a
// time (reviewstack.go). Each page is set against the change's approved
// version (compare.go), paged through the configured diff pager into
// less, and read back for notes (reviewpage.go). The prompt is verdict.go's.
// [a]pprove is approve's attempt (approve.go), [b]ase onto main is the
// rebase verb's (rebase.go), and before either acts, and before
// [r]eject, the change is read again (standing.go, carry.go).
//
// A review is given a reviewStep: the repository it runs in, whose
// directory and environment every process it starts is given, unless the
// process targets a tree of its own; what a walk's queue held of the
// change; the terminal it reads answers from, a byte at a time, and the
// streams it writes to; the signer source its reads verify a transfer
// statement with; the caller's catch of SIGINT and SIGTERM; and the path
// an interrupt ends it by. It returns how it ended as a stepOutcome, the
// same for a walk's step as for valley review <change>, which turns it
// into the exit status.
//
// The caller catches SIGINT and SIGTERM from before the review starts
// until the process exits, so neither ever gets the default handling. The
// review takes them from the caller's catch while it listens, until its
// cleanup, and acts on each: it ends the review with status 130, its
// outcome interrupted, through the step's end path, which ends the
// process: once the command is recorded for valley review <change>, and
// once the walk's summary so far is printed for a step (walk.go). One that
// arrives while a program it runs in the foreground has the terminal, such
// as less or the editor less starts, ends it once that program has ended:
// less takes Ctrl-C itself. During [a]pprove's attempt, SIGINT is the
// attempt's to act on: it stops the checks, and the attempt ends the
// review through the same path. One that arrives during the cleanup stays
// in the caller's catch, and the caller acts on it once the review has
// returned: valley review <change> then exits 130. SIGHUP ends the review
// at once while it listens, with status 129. Every other ending returns, a
// failure included, so a walk goes on to its next step. However it ends,
// the review leaves nothing of its own behind: its pages and the
// background read of the checks.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"the-valley/change"
)

const reviewUsage = "usage: valley review [-v] [--build-here] [<change> | --repo <name>]"

// reviewStep is what one review of a change is given: by valley review
// <change> for the current checkout, and by the walk for each step.
type reviewStep struct {
	// g is the repository the review runs in, at its root for a walk's
	// step, with the environment every process the review starts is given.
	g                  git
	queued             *queuedChange // what the walk's queue held of the change; nil outside a walk
	verbose, buildHere bool
	stdin              *os.File // the terminal: read a byte at a time, and given to the programs that read it
	stdout, stderr     io.Writer
	signers            *signerSource // what the review's reads verify a transfer statement with
	// interrupts is the caller's catch of SIGINT and SIGTERM, which it
	// holds until the process exits. The review takes from it only while
	// it listens.
	interrupts <-chan os.Signal
	// end ends the process once a signal has ended the review and the
	// review has cleaned up, with the status and how the review ended. It
	// does not return.
	end func(status int, o stepOutcome)
}

// queuedChange is a change as the walk's queue held it.
type queuedChange struct {
	id      string
	version int
	head    string
}

// stepOutcome is how a review ended. kind is approved, rejected, kept,
// skipped, refused (an answer was refused, then the change skipped),
// moved (it moved while it was shown), not-reviewed, interrupted or error.
// status is the exit status valley review <change> gives it.
type stepOutcome struct {
	kind                      string
	id, handle, version, head string // approved: what was filed, the head alone for a plain branch
	branch                    string // rejected and kept: the branch
	reason                    string // not-reviewed, and an error that says why
	status                    int
}

// shownChange is the change review acts on: the one it was given, or a
// layer of its stack (useLayer). For a change with a record, id, handle,
// version, head, state and owner are its record's, with head the active
// version's. A plain branch has a name and a branch alone.
type shownChange struct {
	name, id, handle, version, head, branch, state, owner string
}

// review is one review of a change and the stack under it.
type review struct {
	step               reviewStep
	g                  git
	stdin              *os.File
	stdout, stderr     io.Writer
	verbose, buildHere bool

	c                           shownChange
	approveHead, approveVersion string // what [a]pprove files (bind)
	carriedVersion, carriedHead string // the version the answer carried to, which lands, and its head
	checksFailedAt              string // the head [a]pprove's checks failed at
	checksClause                string // the checks line for the prompt's header
	topName                     string // the name review was given
	layers                      []reviewLayer
	notes                       []string // taken in the pages since the last layer began
	does                        doers
	checks                      *checksRead

	said    *stepOutcome    // how the review ended, once it says; the last said stands
	refused bool            // an answer was refused
	attempt func(*approval) // runs [a]pprove's attempt: approval.run

	mu         sync.Mutex
	scratch    string // the directory holding the page being shown
	exiting    bool
	foreground int  // programs the review waits on with signals held
	pending    bool // a signal held until they end
}

// reviewEnded is a review that cannot go on, having said why. status is
// the exit status valley review <change> gives it.
type reviewEnded struct{ status int }

func (e reviewEnded) Error() string { return "the review ended with status " + strconv.Itoa(e.status) }

// endedStatus is the exit status of a review err ended.
func endedStatus(err error) int {
	var ended reviewEnded
	if errors.As(err, &ended) {
		return ended.status
	}
	return 1
}

func runReview(g git, args []string, stdout, stderr io.Writer, end func(int)) int {
	var name, repo string
	var verbose, buildHere, repoGiven bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-v":
			verbose = true
		case a == "--build-here":
			buildHere = true
		case a == "--repo":
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "valley: %s\n", reviewUsage)
				return 1
			}
			repo, repoGiven = args[i+1], true
			i++
		case strings.HasPrefix(a, "-") || name != "":
			fmt.Fprintf(stderr, "valley: %s\n", reviewUsage)
			return 1
		default:
			name = a
		}
	}
	if name == "" {
		var walk []string
		if verbose {
			walk = append(walk, "-v")
		}
		if buildHere {
			walk = append(walk, "--build-here")
		}
		if repoGiven {
			walk = append(walk, "--repo", repo)
		}
		if err := fetchOrigin(g, stdout, stderr); err != nil {
			return exitCode(err)
		}
		return runWalk(g, walk, stdout, stderr, end)
	}
	if repoGiven {
		fmt.Fprintf(stderr, "valley: %s\n", reviewUsage)
		return 1
	}
	commandTimed = false
	// Never stopped: the process exits holding it.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	o := reviewChange(reviewStep{g: g, verbose: verbose, buildHere: buildHere, stdin: os.Stdin,
		stdout: stdout, stderr: stderr, signers: verification, interrupts: interrupts,
		end: func(status int, _ stepOutcome) {
			verification.done()
			end(status)
		}}, name)
	if caught(interrupts) {
		return interruptedStatus
	}
	return o.status
}

// reviewChange is one review of the change named, and how it ended. Only
// a signal ends it any other way: through s.end.
func reviewChange(s reviewStep, name string) stepOutcome {
	return newReview(s).review(name)
}

// newReview is a review given s.
func newReview(s reviewStep) *review {
	return &review{step: s, g: s.g, stdin: s.stdin, stdout: s.stdout, stderr: s.stderr,
		verbose: s.verbose, buildHere: s.buildHere, attempt: func(a *approval) { a.run() }}
}

// review reviews the change named, and returns how it ended. It stops
// listening before it cleans up: a signal taken during the cleanup would
// find the review already ending and leave the listener parked in exit,
// and stop would wait for it forever. A signal that arrives during the
// cleanup stays in the caller's catch instead.
func (r *review) review(name string) stepOutcome {
	stop := r.listen()
	defer stop() // for an end that leaves this goroutine rather than the process
	status := 0
	if err := r.run(name); err != nil {
		status = endedStatus(err)
	}
	stop()
	o, _ := r.cleanup(status)
	return o
}

// run is the review of the change named. An error is a review that ended
// without a verdict, having said why.
func (r *review) run(name string) error {
	if err := r.resolve(name); err != nil {
		return r.die(err.Error())
	}
	if err := fetchOrigin(r.g, r.stdout, r.stderr); err != nil {
		return reviewEnded{exitCode(err)}
	}
	if !r.queued() {
		return nil
	}
	// Notes are kept under the branch, and [r]eject deletes it. approve
	// files the head alone, so it approves such a change.
	if r.c.id != "" && r.c.branch == "" {
		return r.die(fmt.Sprintf("change %s v%s records no branch, and review acts on a change's branch. Approve its head alone: valley approve --head %s %s",
			r.c.handle, r.c.version, r.c.head, r.c.handle))
	}
	branch := r.c.branch
	r.topName = name
	if r.ref("refs/remotes/origin/"+branch) == "" {
		return r.die("no such branch on origin: " + branch)
	}
	r.bind()
	// The verdict disposes of the change; the notes are what the human made
	// of it, so they are printed whatever the verdict was. A stack that
	// cannot be read is reviewed whole against main, as a change with
	// nothing under it is.
	if !r.readStack() {
		fmt.Fprintf(r.stderr, "valley: warning: the stack under %s could not be read; reviewing it whole, against main\n", name)
		r.layers = nil
	}
	r.readDoes()
	if len(r.layers) > 1 {
		if err := r.reviewStack(); err != nil {
			return err
		}
		r.printStackNotes()
		return nil
	}
	if err := r.showReview(branch); err != nil {
		return err
	}
	if err := r.decide(branch, "", 0); err != nil {
		return err
	}
	r.printNotes(branch, r.notes)
	return nil
}

// fetchOrigin brings this checkout's remote-tracking refs up to date.
func fetchOrigin(g git, stdout, stderr io.Writer) error {
	fetch := g.command("fetch", "--prune", "--quiet", "origin")
	fetch.Stdout, fetch.Stderr = stdout, stderr
	processStarted()
	return fetch.Run()
}

// listen takes SIGINT and SIGTERM from the caller's catch, and SIGHUP,
// until the stop it returns, which returns once every signal taken has
// been acted on: one that ends the review ends the process, so nothing of
// the review acts on a signal once stop has returned. A SIGINT or SIGTERM
// that arrives after is left in the caller's catch.
func (r *review) listen() (stop func()) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case s := <-r.step.interrupts:
				r.signalled(s)
			case s := <-hup:
				r.signalled(s)
			case <-quit:
				if caught(hup) {
					r.signalled(syscall.SIGHUP)
				}
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(hup)
			close(quit)
			<-done
		})
	}
}

// signalled acts on a signal: SIGHUP ends the review at once, and another
// ends it now, or once the programs in the foreground have ended.
func (r *review) signalled(s os.Signal) {
	if s == syscall.SIGHUP {
		r.exit(128 + int(syscall.SIGHUP))
	}
	r.mu.Lock()
	held := r.foreground > 0
	r.pending = r.pending || held
	r.mu.Unlock()
	if !held {
		r.exit(interruptedStatus)
	}
}

// inForeground runs f, which runs a program that has the terminal or
// handles signals itself, holding any signal until it returns.
func (r *review) inForeground(f func()) {
	r.mu.Lock()
	r.foreground++
	r.mu.Unlock()
	f()
	r.mu.Lock()
	r.foreground--
	act := r.foreground == 0 && r.pending
	r.mu.Unlock()
	if act {
		r.exit(interruptedStatus)
	}
}

// cleanup removes what the review leaves outside the repository, and
// returns how it ended with status: what it said, or else interrupted for
// a signal's status and error for any other. Only the first call acts: it
// reports false to any other, whose caller leaves the ending to the first.
func (r *review) cleanup(status int) (stepOutcome, bool) {
	r.mu.Lock()
	if r.exiting {
		r.mu.Unlock()
		return stepOutcome{}, false
	}
	r.exiting = true
	o := stepOutcome{kind: "error"}
	if r.said != nil {
		o = *r.said
	} else if status == interruptedStatus {
		o.kind = "interrupted"
	}
	o.status = status
	scratch, checks := r.scratch, r.checks
	r.scratch, r.checks = "", nil
	r.mu.Unlock()
	if scratch != "" {
		os.RemoveAll(scratch)
	}
	checks.stop()
	return o, true
}

// exit ends the review and the process, from anywhere, with status.
func (r *review) exit(status int) {
	o, first := r.cleanup(status)
	if !first {
		select {} // the ending under way ends the process
	}
	r.step.end(status, o)
}

// die says what ends the review: nothing is left to offer. It returns the
// error that ends it, with status 1.
func (r *review) die(msg string) error {
	fmt.Fprintf(r.stderr, "valley: %s\n", msg)
	return reviewEnded{1}
}

// refuse reports what was not done, without leaving the review: the
// change is still under review and the operator can still answer. It
// returns false.
func (r *review) refuse(msg string) bool {
	fmt.Fprintf(r.stderr, "valley: %s\n", msg)
	return false
}

// newScratch makes the directory the next page is written to.
func (r *review) newScratch() (string, error) {
	dir, err := os.MkdirTemp("", "valley-review-")
	if err != nil {
		return "", r.die(fmt.Sprintf("no scratch directory for the review: %v", err))
	}
	r.mu.Lock()
	if r.exiting {
		r.mu.Unlock()
		os.RemoveAll(dir)
		select {}
	}
	r.scratch = dir
	r.mu.Unlock()
	return dir, nil
}

// dropScratch removes the page's directory.
func (r *review) dropScratch() {
	r.mu.Lock()
	dir := r.scratch
	r.scratch = ""
	r.mu.Unlock()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// outcome says how the review ended, unless it is already ending and
// says so itself. The last one said stands.
func (r *review) outcome(o stepOutcome) {
	r.mu.Lock()
	if !r.exiting {
		r.said = &o
	}
	r.mu.Unlock()
}

// skipped is the outcome of a review that was skipped or stopped: refused
// when an answer was refused first.
func (r *review) skipped() {
	if r.refused {
		r.outcome(stepOutcome{kind: "refused"})
	} else {
		r.outcome(stepOutcome{kind: "skipped"})
	}
}

// queued compares a walk's step with what the walker's queue held of it.
// A change that landed, is no longer ready or has no branch on origin is
// not reviewed, and the step ends; one at another version is reviewed at
// it, saying so first. It reports false when the step ends here.
func (r *review) queued() bool {
	q := r.step.queued
	if q == nil {
		return true
	}
	s, _ := r.readStanding()
	why := ""
	switch {
	case s.landed != "":
		why = "it landed as " + s.landed
	case r.c.state != "ready":
		why = "it is " + strings.Replace(r.c.state, "draft", "a draft", 1)
	case r.c.branch == "" || r.ref("refs/remotes/origin/"+r.c.branch) == "":
		why = "its branch is gone from origin"
	}
	if why != "" {
		fmt.Fprintf(r.stdout, "%s is not reviewed: %s\n", r.c.handle, why)
		r.outcome(stepOutcome{kind: "not-reviewed", reason: why})
		return false
	}
	if q.id != r.c.id || strconv.Itoa(q.version) != r.c.version || q.head != r.c.head {
		fmt.Fprintf(r.stdout, "%s moved to v%s since the queue was read\n", r.c.handle, r.c.version)
	}
	return true
}

// resolve reads the name as approve and rebase read a change's name: a
// change's ID, handle, or the branch its active version names, or else a
// plain branch.
func (r *review) resolve(name string) error {
	r.c = shownChange{name: name}
	if !r.g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	res, err := resolveRecorded(r.g, name)
	if err != nil {
		return err
	}
	r.c.branch = res.branch
	if res.id != "" {
		r.c.id, r.c.handle, r.c.version, r.c.head = res.id, res.handle, strconv.Itoa(res.version), res.head
		r.c.state, r.c.owner = res.state, res.owner
	}
	return nil
}

// verifier is what the review's reads verify a transfer statement with:
// the step's signer source.
func (r *review) verifier() verifier { return r.step.signers.verifier() }

// ref is the object ID a ref holds, read by its exact name, or "".
func (r *review) ref(name string) string {
	oid, ok, err := r.g.ref(name)
	if err != nil || !ok {
		return ""
	}
	return oid
}

func (r *review) short(oid string) string {
	s, _ := r.g.output(false, "rev-parse", "--short", oid)
	return s
}

// changeHead is the commit review acts on: the recorded head for a change
// with a record, and otherwise the branch's head on origin.
func (r *review) changeHead(branch string) string {
	if r.c.head != "" {
		return r.c.head
	}
	return r.ref("refs/remotes/origin/" + branch)
}

// fastForwards reports whether commit fast-forwards from main's tip as the
// fetch left it. A main that cannot be read is not one it fast-forwards
// from.
func (r *review) fastForwards(commit string) bool {
	tip := r.ref("refs/remotes/origin/main")
	return tip != "" && r.g.succeeds(false, "merge-base", "--is-ancestor", tip, commit)
}

// bind binds [a]pprove to the active version's head of a change with a
// record. A plain branch is bound to the head review pages, when it pages
// it (showReview, useLayer).
func (r *review) bind() {
	r.approveHead, r.approveVersion = "", ""
	if r.c.id != "" {
		r.approveHead, r.approveVersion = r.c.head, r.c.version
	}
}

// readStanding reads where the change under review stands now. A plain
// branch has no record, and reads as nothing. It reports false, having
// said why, when origin cannot be read.
func (r *review) readStanding() (standing, bool) {
	if r.c.id == "" {
		return standing{}, true
	}
	s, err := standingOf(r.g, r.c.id)
	if err != nil {
		fmt.Fprintf(r.stderr, "valley: %s\n", err)
		return standing{}, false
	}
	return s, true
}

// notReady is the line review shows for a change whose record is not
// ready, above its diff and in its prompt, or "" for a ready change and a
// plain branch. A draft is often a change an agent is still working on,
// so the prompt then offers only [v]iew diff and [s]kip: [a]pprove would
// be refused, and [r]eject would delete the branch its owner may still be
// working on.
func notReady(handle, version, state, owner string) string {
	switch state {
	case "draft":
		return fmt.Sprintf("%s v%s is a draft: its owner (%s) hasn't marked it ready, so it can't be approved yet.", handle, version, owner)
	case "withdrawn":
		return handle + " is withdrawn: there is nothing to approve."
	}
	return ""
}

// answer reads one answer from the terminal, a byte at a time so that
// nothing after it is taken from whatever reads the input next, with the
// spaces and tabs around it trimmed. ok is false when the input ends
// before the line does.
func (r *review) answer() (string, bool) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.stdin.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				return strings.Trim(string(line), " \t"), true
			}
			line = append(line, b[0])
		}
		if err != nil {
			return "", false
		}
	}
}

// mustAnswer is answer, ending the review when the input has ended.
func (r *review) mustAnswer() (string, error) {
	choice, ok := r.answer()
	if !ok {
		return "", r.die("stdin closed before an answer; nothing done")
	}
	return choice, nil
}

// prompt prints review's prompt: the header's clauses, the empty ones
// left out, and the keys.
func (r *review) prompt(header []string, keys ...string) {
	var shown []string
	for _, h := range header {
		if h != "" {
			shown = append(shown, h)
		}
	}
	fmt.Fprint(r.stdout, promptLines(terminalOf(r.stdout), shown, keys))
}

// decide takes the verdict on a change alone, and on the top of a stack
// of n, named name. Its prompt is one header line, the change and what
// stands in its way, and the keys it takes. A change is named by its
// handle and version, a plain branch by its name, and a change alone
// carries the checks line review read for it. The top of a stack says that
// approving it lands all n layers. [v]iew diff pages the change's whole
// diff, or the top layer's.
//
// A change that is not ready says so in its header, and is offered only
// [v]iew diff and [s]kip, or [s]top. The top of a stack with a layer under
// it that is not ready says why it can't be approved (layerHeld), as does
// a change whose checks failed at the head shown, and neither is offered
// [a]pprove. A change that can be approved and does not fast-forward from
// main says so (behindMain). Before [a]pprove and [r]eject act, moved
// reads the change again, and ends its review when it moved.
func (r *review) decide(branch, name string, n int) error {
	approve, reject, stop := "[a]pprove", "[r]eject", "[s]kip"
	stopped := "skipped " + branch + "; nothing done"
	header := []string{branch}
	own, held, behind := "", "", ""
	if r.c.id != "" {
		header = []string{r.c.handle + " v" + r.c.version}
		switch r.c.state {
		case "draft":
			own = "draft (its owner, " + r.c.owner + ", has not readied it)"
		case "withdrawn":
			own = "withdrawn"
		}
	}
	if n > 0 {
		header = append(header, fmt.Sprintf("stack of %d", n))
		approve, reject, stop = fmt.Sprintf("[a]pprove all %d", n), "[r]eject "+name, "[s]top"
		stopped = fmt.Sprintf("stopped reviewing %s at change %d of %d; nothing done", name, n, n)
		held = r.layerHeld(n - 1)
	} else if r.checksClause != "" {
		header = append(header, r.checksClause)
	}
	if own == "" && held == "" {
		behind = r.behindMain(branch)
	}
	for {
		var clause, answers string
		var keys []string
		failed := ""
		switch {
		case own != "":
			clause, keys, answers = own, []string{"[v]iew diff", stop}, "v or s"
		case held != "":
			clause, keys, answers = held, []string{"[v]iew diff", reject, stop}, "v, r, or s"
		default:
			if failed = r.approveFailedAt(branch); failed != "" {
				clause, keys, answers = "checks failed at "+failed, []string{"[v]iew diff", reject, stop}, "v, r, or s"
			} else {
				clause, keys, answers = behind, []string{approve, "[v]iew diff", reject, stop}, "a, v, r, or s"
			}
		}
		r.prompt(append(append([]string{}, header...), clause), keys...)
		// A refusal from approve is not a verdict: it says what it did not
		// do and the prompt comes back, so the branch can still be rejected
		// or skipped. When the checks failed, approving the same head again
		// is no longer offered, nor is approving a change that is not ready.
		// A change that moved since it was shown is not acted on, and its
		// review ends.
		choice, err := r.mustAnswer()
		if err != nil {
			return err
		}
		switch choice {
		case "a":
			if failed != "" || own != "" || held != "" {
				fmt.Fprintln(r.stdout, "answer "+answers)
				continue
			}
			moved, err := r.moved(true)
			if err != nil {
				return err
			}
			if moved || r.approve(branch) {
				return nil
			}
		case "v":
			var err error
			if n > 0 {
				err = r.viewLayer(n - 1)
			} else {
				err = r.viewFull(branch)
			}
			if err != nil {
				return err
			}
		case "r":
			if own != "" {
				fmt.Fprintln(r.stdout, "answer "+answers)
				continue
			}
			moved, err := r.moved(false)
			if err != nil {
				return err
			}
			if moved {
				return nil
			}
			if done, err := r.reject(branch); err != nil || done {
				return err
			}
		case "s":
			fmt.Fprintln(r.stdout, stopped)
			r.skipped()
			return nil
		default:
			fmt.Fprintln(r.stdout, "answer "+answers)
		}
	}
}

// approveFailedAt is the head the checks failed at in this review, short,
// while the change is still at it, or "". Approving that head again would
// run the same checks over the same tree and fail the same way, so the
// prompt stops offering [a]pprove until the change has a new head.
func (r *review) approveFailedAt(branch string) string {
	if r.checksFailedAt == "" || r.checksFailedAt != r.changeHead(branch) {
		return ""
	}
	return r.short(r.checksFailedAt)
}

// behindMain is the clause review's prompt shows for a change that does
// not fast-forward from main, or "". Being behind main asks nothing of the
// operator: the integrator applies the change's delta onto main as main
// stands, with the evidence for the head approved, and the host's
// restacker rebases ready changes itself. When the restacker reports that
// rebasing the change conflicted, the clause says so instead, and
// [a]pprove stays offered: the integrator decides whether the change
// applies.
func (r *review) behindMain(branch string) string {
	if r.fastForwards(r.changeHead(branch)) {
		return ""
	}
	if s, _ := r.readStanding(); s.conflict == "conflict" {
		return "conflicts with main (owner resolving)"
	}
	return "behind main (integrator merges)"
}

// moved reads a change with a record again before review acts on an
// answer about it: another principal may return it to draft, ready a new
// version, or withdraw it while the operator reads, and it may land. When
// it is no longer what review showed, moved says in one line what changed
// and what happens next, and the review of that change ends: the prompt's
// answers were about what was shown, so it is not asked again. A new
// version the answer carries to does not end it (carried), nor does
// approving the new version in place, which moved offers for an approval.
// It reports false when the change is still as shown, the answer carries
// or the new version is to be approved, and for a plain branch, which
// approve and reject check themselves.
func (r *review) moved(approving bool) (bool, error) {
	h := r.c.handle
	r.carriedVersion, r.carriedHead = "", ""
	if r.c.id == "" {
		return false, nil
	}
	s, ok := r.readStanding()
	switch {
	case !ok:
		fmt.Fprintf(r.stdout, "could not read %s again from origin; nothing done\n", h)
		r.outcome(stepOutcome{kind: "error"})
		return true, nil
	case s.state == "withdrawn":
		fmt.Fprintf(r.stdout, "%s changed while you were reviewing: it was withdrawn (since %s), so there is nothing to approve.\n", h, s.since)
	case s.state == "draft":
		fmt.Fprintf(r.stdout, "%s changed while you were reviewing: it was returned to draft (since %s), so there is nothing to approve now. It comes back when it is ready again.\n", h, s.since)
	case s.landed != "":
		fmt.Fprintf(r.stdout, "%s landed as %s while you were reviewing; there is nothing left to approve.\n", h, s.landed)
	case s.version == r.c.version && s.head == r.c.head:
		return false, nil
	default:
		carried, err := r.carriedTo(s, approving)
		if err != nil || carried {
			return false, err
		}
	}
	r.outcome(stepOutcome{kind: "moved"})
	return true, nil
}

// carriedTo reports whether the answer about the version review showed
// carries to the version s the change moved to, as carry decides
// (carry.go). When it does, it says so in one line, with carry's reason,
// and the answer stays bound to the head shown: an approval names what the
// reviewer read, and the integrator applies its delta onto main as main
// stands. The version moved to is what lands, so the approval's verdict
// names it (carriedVerdict, approve.go). When it does
// not, it says why in one line and offers to page only what changed since
// the version shown. Answering an approval, it also offers to approve the
// version the change moved to, when carry says: at once, or after the
// page. [a] binds the approval to that version's head, and the attempt
// reads the record again before it files. It reports false unless the
// answer carries or [a] is taken.
func (r *review) carriedTo(s standing, approving bool) (bool, error) {
	h := r.c.handle
	r.bind()
	dir, err := r.newScratch()
	if err != nil {
		return false, err
	}
	defer r.dropScratch()
	var out bytes.Buffer
	verdict, reason, offer, text := "", "", "", ""
	if err := runCarry(r.g, []string{r.c.id, r.c.version, s.version}, &out); err != nil {
		fmt.Fprintf(r.stderr, "valley: %s\n", err)
	} else {
		first, rest, _ := strings.Cut(out.String(), "\n")
		f := strings.Fields(first)
		if len(f) > 0 {
			verdict = f[0]
		}
		if t := strings.Split(first, "\t"); len(t) > 1 {
			reason = t[1]
			if len(t) > 2 {
				offer = t[2]
			}
		}
		text = rest
	}
	if !approving {
		offer = "none"
	}
	switch verdict {
	case "carries":
		answer := "rejection"
		if approving {
			answer = "approval"
		}
		fmt.Fprintf(r.stdout, "%s moved to v%s while you were reviewing. %s, so your %s covers it.\n", h, s.version, reason, answer)
		r.carriedVersion, r.carriedHead = s.version, s.head
		return true, nil
	case "review":
		keys := "[v]iew what changed since v" + r.c.version + ", [s]kip?"
		switch offer {
		case "approve":
			keys = "[a]pprove v" + s.version + ", " + keys
		case "view":
		default:
			offer, keys = "", "review it again to approve it · "+keys
		}
		fmt.Fprintf(r.stdout, "%s moved to v%s while you were reviewing (%s) · %s ", h, s.version, reason, keys)
		choice, _ := r.answer()
		if choice == "v" {
			changed := dir + "/changed"
			if err := os.WriteFile(changed, []byte(text), 0o600); err == nil {
				r.page(changed)
			}
			if offer != "" {
				offer = "approve"
				fmt.Fprintf(r.stdout, "[a]pprove v%s, [s]kip? ", s.version)
				choice, _ = r.answer()
			}
		}
		if choice == "a" && offer == "approve" {
			r.approveVersion, r.approveHead = s.version, s.head
			return true, nil
		}
	default:
		fmt.Fprintf(r.stdout, "%s moved to v%s while you were reviewing; review it again to approve what would land.\n", h, s.version)
	}
	return false, nil
}

// approve is review's [a]pprove: one attempt of approve's own for each
// answer, a stack's layers included (approve.go), told the change and head
// review bound and the name as typed, which it reads again before filing.
// It ends approved, with what it filed; checks-failed, with the head the
// checks failed at, which the prompt stops offering [a]pprove for
// (approveFailedAt); refused or unknown; or interrupted, when the attempt
// has ended the review itself. It reports false unless it filed.
func (r *review) approve(branch string) bool {
	c := r.c
	if c.id == "" {
		c.branch = branch
	}
	a := reviewApproval(r, c)
	r.inForeground(func() {
		a.watch(os.Interrupt)
		r.attempt(a)
		a.unwatch()
	})
	result := a.result
	switch {
	case len(result) == 5 && result[0] == "approved":
		r.outcome(stepOutcome{kind: "approved", id: result[1], handle: result[2], version: result[3], head: result[4]})
		return true
	case len(result) == 2 && result[0] == "checks-failed":
		r.checksFailedAt = result[1]
	case len(result) == 1 && (result[0] == "refused" || result[0] == "unknown"):
	default:
		fmt.Fprintln(r.stderr, "valley: warning: the approval left no outcome; taking it as refused")
	}
	r.refused = true
	return false
}

// reject deletes the change's branch on origin, once the operator confirms
// it, and withdraws a change with a record (rejected). For a change with a
// record, what is rejected is the head its record names, so the branch is
// deleted only while its tip is that head, and the delete is leased on it.
// When the tip is another commit, reject refuses and names both, and
// deletes nothing. A plain branch is deleted as it stands. It reports
// false when it refused.
func (r *review) reject(branch string) (bool, error) {
	var lease []string
	if r.c.head != "" {
		if !r.recordedTip(branch, "nothing deleted") {
			r.refused = true
			return false, nil
		}
		lease = []string{"--force-with-lease=refs/heads/" + branch + ":" + r.c.head}
	}
	fmt.Fprintf(r.stdout, "really delete origin/%s? [y/N] ", branch)
	switch choice, _ := r.answer(); choice {
	case "y", "Y", "yes":
		// The change may have moved while the confirmation waited: say so,
		// and delete nothing.
		if moved, err := r.moved(false); err != nil || moved {
			return true, err
		}
		push := r.g.command(append(append([]string{"push", "--quiet"}, lease...), "origin", "--delete", branch)...)
		push.Stdout, push.Stderr = r.stdout, r.stderr
		var err error
		r.inForeground(func() { err = push.Run() })
		if err != nil {
			r.refuse("origin did not delete " + branch + ", which may have moved since the fetch; nothing deleted")
			r.refused = true
			return false, nil
		}
		r.rejected(branch)
	default:
		fmt.Fprintf(r.stdout, "kept %s\n", branch)
		r.outcome(stepOutcome{kind: "kept", branch: branch})
	}
	return true, nil
}

// rejected says reject deleted the branch. A change with a record is
// withdrawn too, by valley withdraw's own code and under its rules, while
// its active version is still at the head deleted: a rejection says the
// change is not wanted, and withdrawing it voids what is outstanding on it
// and takes it off what needs the operator. When the
// withdrawal fails, the line says so and names the command to run. The
// branch is gone either way, so the outcome is rejected. Signals are held
// until the line is printed, so a deleted branch is always reported.
func (r *review) rejected(branch string) {
	r.inForeground(func() {
		line, run := "rejected "+branch+": deleted from origin", ""
		if r.c.id != "" {
			if err := setState(r.g, []string{r.c.id}, io.Discard, r.stderr, "withdraw", change.Withdrawn, r.c.head); err != nil {
				fmt.Fprintf(r.stderr, "valley: %s\n", err)
				line += " · withdrawing " + r.c.handle + " failed"
				run = "  run: valley withdraw " + r.c.handle + "\n"
			} else {
				line += " · " + r.c.handle + " withdrawn"
			}
		}
		fmt.Fprint(r.stdout, line+"\n"+run)
		r.outcome(stepOutcome{kind: "rejected", branch: branch})
	})
}

// recordedTip reports whether the branch's tip on origin is the head a
// change's record names, and refuses, naming both, when it is not:
// [r]eject acts on the branch itself, and deleting it while it is
// elsewhere would delete a commit no version records. undone is what the
// refusal leaves undone.
func (r *review) recordedTip(branch, undone string) bool {
	tip := r.ref("refs/remotes/origin/" + branch)
	switch {
	case tip == "":
		return r.refuse("no such branch on origin: " + branch + "; " + undone)
	case tip != r.c.head:
		return r.refuse(fmt.Sprintf("change %s v%s records the head %s, and origin/%s is at %s; %s. Ready the branch's head as a new version of %s to act on it",
			r.c.handle, r.c.version, r.c.head, branch, tip, undone, r.c.handle))
	}
	return true
}
