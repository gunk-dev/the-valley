package main

// valley review with no change: a walk through what needs the operator
// (review.go).
//
// The walk reads the queue once, as valley pending reads it
// (operatorQueue, queue.go), across every project the serving host
// publishes, or the one --repo names, and steps through its rows that wait
// on an approval in landing order. A project that did not read, and a
// discovery that failed and left the walk with the current project alone,
// are said before the first step and again in the summary, and nothing
// says that nothing needs the operator while either stands.
//
// Each step prints a header naming the row's project, handle, version,
// stack and checks, then calls the single-change review (review.go) with
// a reviewStep for the row: the git handle on the row's repository root,
// the current checkout's top level or the cache clone a project other
// than the current one is read through (cacheRepo, list.go), with
// cacheEnvironment's environment; the change as the queue held it, its ID,
// version and head; the walk's -v and --build-here; the walk's terminal
// and streams; and the walk's signer source. Every process the step starts
// runs in that root and environment, unless it targets a tree of its own,
// and nothing is set in the walk's own environment or directory. A cache
// clone's configuration is its own, so the step's environment gives it
// the current checkout's valley.principal, user.name and user.email
// through GIT_CONFIG_COUNT, for each key the clone's own configuration
// does not set, and nothing is written into the clone.
//
// The walk's signer source trusts the integrator of the valley the
// current checkout's main names. A step in a cache clone whose project's
// main names another valley, or none, is not reviewed with it
// (walkValley): the step ends in error, saying which valley each names,
// unless a flag or the environment gives what the walk trusts.
//
// The review returns how the step ended (stepOutcome), and the walk goes
// on to the next row whatever it was, an error included. Each review a
// step runs is recorded as an untimed review command of its own, from the
// step's repository and environment, so a step in a cache clone is its
// project's.
//
// Once the queue is first read, until the process exits, the walk
// catches SIGINT and SIGTERM, and each step's review takes them from the
// walk's catch while it listens (review.go): SIGINT and SIGTERM end the
// step interrupted, SIGTERM sent to the walk alone included, and SIGHUP
// ends it with 129. The review ends the walk through the step's end path:
// once the step has cleaned up, the summary so far is printed (not for
// SIGHUP), the step and the walk are recorded, and the process exits with
// the review's status. One that arrives while no review listens, between
// steps or while a step cleans up, ends the walk before the next step:
// the step under way ends as it would and is recorded, the summary so far
// is printed, and the walk exits 130. One that arrives while the queue is
// read again ends the walk with 130 once the read has ended.
//
// The summary is one line per row walked, then what still needs the
// operator, read again from every project, and what either read did not
// cover.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"the-valley/change"
)

// walkConfigKeys are the settings a review in a cache clone borrows from
// the current checkout: who approves and rebases, and who commits.
var walkConfigKeys = []string{"valley.principal", "user.name", "user.email"}

// walkQueue is one read of the queue for a walk.
type walkQueue struct {
	repos            []listed
	h                handleMap
	rows, moving     []*entry
	gaps             []string // what the read did not cover, one line each
	drafts, branches int
}

// walkStep is one row walked and how its step ended.
type walkStep struct {
	row     *entry
	outcome stepOutcome
}

// runWalk walks the queue. end ends the process when a signal ends a step,
// after the walk has said what it walked; it does not return.
func runWalk(g git, args []string, stdout, stderr io.Writer, end func(int)) int {
	l := listing{all: true}
	var flags []string // the flags each step's review is recorded with
	var verbose, buildHere bool
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-v" || a == "--build-here":
			flags = append(flags, a)
			verbose, buildHere = verbose || a == "-v", buildHere || a == "--build-here"
		case a == "--repo" && i+1 < len(args):
			i++
			l.repo = args[i]
		case strings.HasPrefix(a, "--repo="):
			l.repo = strings.TrimPrefix(a, "--repo=")
		default:
			fmt.Fprintf(stderr, "valley: walk does not take %s\n", a)
			return 2
		}
	}
	if l.repo != "" && !change.ValidPrincipal(l.repo) {
		fmt.Fprintf(stderr, "valley: --repo %s: not a project name\n", l.repo)
		return 1
	}
	q, err := readWalkQueue(g, l, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	defer closeSources(q.repos)
	warnUnverified(q.repos, stderr)
	t := terminalOf(stdout)
	p := func(format string, args ...any) { fmt.Fprintf(stdout, format, args...) }
	if len(q.rows) == 0 {
		for _, line := range q.gapLines(t) {
			p("%s\n", line)
		}
		if len(q.gaps) > 0 {
			return 1
		}
		p("%s\n", q.nothing())
		return 0
	}
	p("needs you (%d, in landing order)\n", len(q.rows))
	for _, line := range q.gapLines(t) {
		p("%s\n", line)
	}

	top, _ := g.output(true, "rev-parse", "--show-toplevel")
	repos := map[string]listed{}
	for _, r := range q.repos {
		repos[r.name] = r
	}
	own := &walkValley{g: g, given: verification.given(), published: map[string]publication{}}
	var walked []walkStep
	summary := func() int {
		status := 0
		p("\nwalked (%d of %d)\n", len(walked), len(q.rows))
		for _, s := range walked {
			p("%s\n", s.line(t, q.h))
			if s.outcome.kind == "error" {
				status = 1
			}
		}
		return status
	}
	// Never stopped: the process exits holding it.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	interrupted := false
	for i, e := range q.rows {
		if interrupted = interrupted || caught(interrupts); interrupted {
			break
		}
		p("\n%s\n", stepHeader(t, i+1, len(q.rows), e))
		r := repos[e.repo]
		step := git{env: g.env, stderr: g.stderr, dir: top}
		if r.g.dir != "" {
			step.dir, step.env = r.g.dir, withConfig(r.g.env, borrowedConfig(g, r.g))
		}
		if step.dir == "" {
			walked = append(walked, walkStep{row: e, outcome: stepOutcome{kind: "error", reason: "no work tree here to review it in"}})
			continue
		}
		if why := own.elsewhere(r.g); why != "" {
			fmt.Fprintf(stderr, "valley: %s: %s\n", e.repo, why)
			walked = append(walked, walkStep{row: e, outcome: stepOutcome{kind: "error", reason: why}})
			continue
		}
		inv := &invocation{args: append(append([]string{"review"}, flags...), e.id), since: time.Now(), version: versionStamp}
		left := phase("steps")
		o := reviewChange(reviewStep{g: step, queued: &queuedChange{id: e.id, version: e.version, head: e.head},
			verbose: verbose, buildHere: buildHere, stdin: os.Stdin, stdout: stdout, stderr: stderr,
			signers: verification, interrupts: interrupts,
			end: func(status int, o stepOutcome) {
				walked = append(walked, walkStep{row: e, outcome: o})
				if status == interruptedStatus {
					summary()
				}
				inv.record(step, status, false)
				closeSources(q.repos)
				verification.done()
				end(status)
			}}, e.id)
		left()
		inv.record(step, o.status, false)
		walked = append(walked, walkStep{row: e, outcome: o})
		interrupted = caught(interrupts)
	}

	status := summary()
	if interrupted {
		return interruptedStatus
	}
	again, err := readWalkQueue(g, l, io.Discard)
	if caught(interrupts) {
		closeSources(again.repos)
		return interruptedStatus
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley: the queue did not read again: %s\n", err)
		return 1
	}
	closeSources(again.repos)
	// The summary repeats what the first read did not cover, since the walk
	// left it out, and adds what the second read did not.
	for _, gap := range again.gaps {
		if !slices.Contains(q.gaps, gap) {
			q.gaps = append(q.gaps, gap)
		}
	}
	p("\n")
	for _, line := range q.gapLines(t) {
		p("%s\n", line)
	}
	switch {
	case len(again.rows) > 0:
		var names []string
		for _, e := range again.rows {
			names = append(names, e.repo+" "+e.ref)
		}
		p("still needs you (%d): %s\n", len(again.rows), strings.Join(names, ", "))
	case len(again.gaps) == 0:
		p("%s\n", again.nothing())
	}
	if len(q.gaps) > 0 {
		status = 1
	}
	return status
}

// walkValley is the valley the walk's checkout's main names, whose
// publication the walk's signer source reads, set against the valley each
// step's cache clone names (instanceOf). A step whose project names
// another valley, or none, is not verified with the walk's source: the
// integrator that signs its transfer statements is its own valley's.
type walkValley struct {
	g     git  // the walk's checkout
	given bool // a flag or the environment gives what the walk trusts

	read      bool
	instance  string
	err       error
	published map[string]publication // what each instance URL read publishes
}

// publication is the signers tree an instance repository publishes, ""
// for none, or the error of reading it.
type publication struct {
	tree string
	err  error
}

// elsewhere is why a step in the cache clone c is not reviewed with the
// walk's signer source, or "" when its project's main names the walk's
// valley. Two URLs are one valley when they are the same string, or when
// both read the same signers publication, which holds for two spellings
// of one repository and makes the trust the same either way. When the
// operator gave what the walk trusts, every step is reviewed with it.
func (v *walkValley) elsewhere(c git) string {
	if v.given || c.dir == "" {
		return ""
	}
	const there = " · review it from a checkout of its own valley"
	theirs, err := instanceOf(c)
	var none noInstance
	switch {
	case errors.As(err, &none):
		return "its main names no valley" + there
	case err != nil:
		return fmt.Sprintf("could not read which valley its main names: %v", err)
	}
	if !v.read {
		v.read = true
		v.instance, v.err = instanceOf(v.g)
	}
	if v.err != nil {
		return fmt.Sprintf("could not read which valley this checkout's main names: %v", v.err)
	}
	if theirs == v.instance {
		return ""
	}
	if a, b := v.publication(theirs), v.publication(v.instance); a.err == nil && b.err == nil && a.tree == b.tree {
		return ""
	}
	return fmt.Sprintf("its main names the valley %s, not this checkout's %s%s", theirs, v.instance, there)
}

// publication is what the instance repository at url publishes, read once.
func (v *walkValley) publication(url string) publication {
	if p, ok := v.published[url]; ok {
		return p
	}
	tree, err := remoteRef(v.g.restricted(), url, signersRef)
	p := publication{tree: tree, err: err}
	v.published[url] = p
	return p
}

// caught reports whether a signal is waiting on c, and takes it.
func caught(c chan os.Signal) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// readWalkQueue reads the queue across the repositories l covers, and
// what the read did not cover. closeSources removes what it read.
func readWalkQueue(g git, l listing, stderr io.Writer) (walkQueue, error) {
	list, _, gap, err := findSources(g, l)
	if err != nil {
		return walkQueue{}, err
	}
	q := walkQueue{repos: readSources(list, newVerifier())}
	q.h = handles(q.repos)
	if gap != nil {
		q.gaps = append(q.gaps, fmt.Sprintf("discovery is unavailable: %v · walking %s only", gap, list[0].name))
	}
	for _, r := range q.repos {
		if r.err != nil {
			q.gaps = append(q.gaps, fmt.Sprintf("%s did not read: %v", r.name, r.err))
		}
	}
	q.rows, q.moving = operatorQueue(q.repos, q.h, l, newCosts(), nil, stderr, func(_ listed, view jsonRepo) {
		q.drafts, q.branches = q.drafts+len(*view.Drafts), q.branches+len(view.Branches)
	})
	return q, nil
}

// gapLines are the read's gaps, one failed line each.
func (q walkQueue) gapLines(t terminal) []string {
	var lines []string
	for _, gap := range q.gaps {
		lines = append(lines, t.tint(colourRefused, "✗")+" "+fit(gap, t.width-2))
	}
	return lines
}

// nothing is the line of a queue with no row, with the counts valley
// pending gives of what else there is.
func (q walkQueue) nothing() string {
	parts := []string{"nothing needs you"}
	if n := len(q.moving); n > 0 {
		parts = append(parts, fmt.Sprintf("%d moving", n))
	}
	if q.drafts > 0 {
		parts = append(parts, counted(q.drafts, "draft"))
	}
	switch q.branches {
	case 0:
	case 1:
		parts = append(parts, "1 branch no change is on")
	default:
		parts = append(parts, fmt.Sprintf("%d branches no change is on", q.branches))
	}
	return strings.Join(parts, " · ")
}

// stepHeader is the line that opens step n of all: the row's project, its
// change, the stack it tops and its checks, as the queue read them.
func stepHeader(t terminal, n, all int, e *entry) string {
	parts := []string{fmt.Sprintf("[%d/%d] %s", n, all, e.repo), t.strong(e.name)}
	if len(e.below) > 0 {
		parts = append(parts, "stack of "+strconv.Itoa(len(e.below)+1))
	}
	if c := checkCount(e.state); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, " · ")
}

// borrowedConfig is the configuration a review in the cache clone at
// clone takes from the current checkout here: each of walkConfigKeys that
// here sets and the clone's own configuration does not.
func borrowedConfig(here, clone git) [][2]string {
	var kv [][2]string
	for _, k := range walkConfigKeys {
		v, err := here.output(true, "config", "--get", k)
		if err != nil || v == "" || clone.succeeds(true, "config", "--local", "--get", k) {
			continue
		}
		kv = append(kv, [2]string{k, v})
	}
	return kv
}

// withConfig is env with each pair added to git's configuration through
// GIT_CONFIG_COUNT, after the entries env gives it already. A count git
// would refuse has failed every git the walk ran before this.
func withConfig(env []string, kv [][2]string) []string {
	if len(kv) == 0 {
		return env
	}
	n := 0
	var kept []string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "GIT_CONFIG_COUNT="); ok {
			n, _ = strconv.Atoi(v)
			continue
		}
		kept = append(kept, e)
	}
	for i, p := range kv {
		kept = append(kept, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n+i, p[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n+i, p[1]))
	}
	return append(kept, fmt.Sprintf("GIT_CONFIG_COUNT=%d", n+len(kv)))
}

// line is the summary's line for a step: its glyph, the row's project,
// handle and version, and what became of it.
func (s walkStep) line(t terminal, h handleMap) string {
	e, o := s.row, s.outcome
	glyph, colour, says := "◇", colourWaiting, o.kind
	switch o.kind {
	case "approved":
		glyph, colour = "✓", colourDone
		id, handle, version := o.id, o.handle, o.version
		if known := h[handleKey{e.repo, recordTarget, id}]; known != "" {
			handle = known
		}
		switch {
		case id != e.id:
			says = "approved " + handle + " v" + version + ", a layer under it"
		case version != strconv.Itoa(e.version):
			says = "approved v" + version
		}
	case "rejected":
		glyph, colour, says = "✓", colourDone, "rejected · branch deleted"
	case "kept":
		says = "kept · rejection not confirmed"
	case "refused":
		glyph, colour, says = "⊘", colourRefused, "refused, then skipped"
	case "moved":
		says = "moved while shown"
	case "not-reviewed":
		glyph, colour, says = "⊘", colourRefused, "not reviewed · "+o.reason
	case "error":
		glyph, colour = "✗", colourRefused
		if o.reason != "" {
			says += " · " + o.reason
		}
	}
	lead := "  " + glyph + " " + e.repo + " "
	return "  " + t.tint(colour, glyph) + " " + e.repo + " " + t.strong(e.name) + "  " +
		fit(says, t.width-columns(lead)-columns(e.name)-2)
}
