package main

// valley review with no change: a walk through what needs the operator.
// bin/valley runs valley-go walk for it.
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
// stack and checks, then runs the single-change review as a child:
//
//	$VALLEY_COMMAND review [-v] [--build-here] <change ID>
//
// in the row's repository root: the current checkout's top level, or the
// cache clone a project other than the current one is read through
// (cacheRepo, list.go), with cacheEnvironment's environment. The child has
// the walker's stdin, stdout and stderr. A cache clone's configuration is
// its own, so the child is given the current checkout's valley.principal,
// user.name and user.email through GIT_CONFIG_COUNT, for each key the
// clone's own configuration does not set, and nothing is written into the
// clone. Two variables tell the child it is a step of a walk:
//
//	VALLEY_WALK_QUEUED   <change ID> <version> <head>, as the queue held it
//	VALLEY_WALK_OUTCOME  the file the child writes how the step ended to
//
// The outcome is one line of tab-separated fields, its first field one of:
//
//	approved <change ID> <handle> <version> <head>   what the step approved
//	rejected <branch>        the branch was deleted from origin
//	kept <branch>            the rejection was not confirmed
//	skipped
//	refused                  an answer was refused, then the change skipped
//	moved                    it moved while it was shown
//	not-reviewed <reason>    it landed, is not ready, or has no branch now
//	interrupted
//	error
//
// A child that leaves no outcome the walker reads ended in error. While a
// child runs, the walker catches SIGINT rather than ignoring it, so the
// child, which a terminal's Ctrl-C reaches too, keeps its own handling.
// A step interrupted, by the child exiting 130 or by SIGINT reaching the
// walker, ends the walk: the summary so far is printed and the walk exits
// 130.
//
// The summary is one line per row walked, then what still needs the
// operator, read again from every project, and what either read did not
// cover.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

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

// stepOutcome is how one step ended, as its outcome file says.
type stepOutcome struct {
	kind   string
	fields []string
}

// walkStep is one row walked and how its step ended.
type walkStep struct {
	row     *entry
	outcome stepOutcome
}

func runWalk(g git, args []string, stdout, stderr io.Writer) int {
	l := listing{all: true}
	var child []string // the flags each step's review is given
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-v" || a == "--build-here":
			child = append(child, a)
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
	valley, err := walkCommand()
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	q, err := readWalkQueue(g, l, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
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

	scratch, err := os.MkdirTemp("", "valley-walk-")
	if err != nil {
		fmt.Fprintf(stderr, "valley: no scratch directory for the walk's outcomes: %v\n", err)
		return 1
	}
	defer os.RemoveAll(scratch)
	top, _ := g.output(true, "rev-parse", "--show-toplevel")
	repos := map[string]listed{}
	for _, r := range q.repos {
		repos[r.name] = r
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	var walked []walkStep
	interrupted, broken := false, false
	for i, e := range q.rows {
		if interrupted = interrupted || caught(interrupts); interrupted || broken {
			break
		}
		p("\n%s\n", stepHeader(t, i+1, len(q.rows), e))
		step := walkStep{row: e}
		r := repos[e.repo]
		dir := r.g.dir
		if dir == "" {
			dir = top
		}
		if dir == "" {
			step.outcome = stepOutcome{kind: "error", fields: []string{"no work tree here to review it in"}}
			walked = append(walked, step)
			continue
		}
		outcome := filepath.Join(scratch, fmt.Sprintf("step-%d", i+1))
		cmd := exec.Command(valley, append(append([]string{"review"}, child...), e.id)...)
		cmd.Dir = dir
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, stdout, stderr
		env := os.Environ()
		if r.g.dir != "" {
			env = withConfig(cacheEnvironment(env), borrowedConfig(g, r.g))
		}
		cmd.Env = append(env,
			fmt.Sprintf("VALLEY_WALK_QUEUED=%s %d %s", e.id, e.version, e.head),
			"VALLEY_WALK_OUTCOME="+outcome)
		end := phase("steps")
		processStarted()
		if err := cmd.Start(); err != nil {
			end()
			step.outcome = stepOutcome{kind: "error", fields: []string{fmt.Sprintf("could not run %s: %v", valley, err)}}
			walked, broken = append(walked, step), true
			continue
		}
		_ = cmd.Wait()
		end()
		interrupted = caught(interrupts) || cmd.ProcessState.ExitCode() == 130
		var ok bool
		if step.outcome, ok = readOutcome(outcome); !ok {
			step.outcome = stepOutcome{kind: "error"}
			if interrupted {
				step.outcome.kind = "interrupted"
			}
		}
		walked = append(walked, step)
	}
	signal.Stop(interrupts)

	status := 0
	p("\nwalked (%d of %d)\n", len(walked), len(q.rows))
	for _, s := range walked {
		p("%s\n", s.line(t, q.h))
		if s.outcome.kind == "error" {
			status = 1
		}
	}
	if interrupted {
		return 130
	}
	again, err := readWalkQueue(g, l, io.Discard)
	if err != nil {
		fmt.Fprintf(stderr, "valley: the queue did not read again: %s\n", err)
		return 1
	}
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

// caught reports whether a signal is waiting on c, and takes it.
func caught(c chan os.Signal) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// walkCommand is the valley command each step runs, as an absolute path,
// since a step runs in another directory: VALLEY_COMMAND, which bin/valley
// sets to its own path, or valley on PATH.
func walkCommand() (string, error) {
	v := os.Getenv("VALLEY_COMMAND")
	if v == "" {
		v = "valley"
	}
	if !strings.Contains(v, "/") {
		found, err := exec.LookPath(v)
		if err != nil {
			return "", fmt.Errorf("no %s command to review each change with: %v", v, err)
		}
		v = found
	}
	return filepath.Abs(v)
}

// readWalkQueue reads the queue across the repositories l covers, and
// what the read did not cover.
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
	q.rows, q.moving = operatorQueue(q.repos, q.h, l, newCosts(), stderr, func(_ listed, view jsonRepo) {
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

// outcomeFields is how many fields each outcome takes after its name.
var outcomeFields = map[string]int{
	"approved": 4, "rejected": 1, "kept": 1, "skipped": 0, "refused": 0,
	"moved": 0, "not-reviewed": 1, "interrupted": 0, "error": 0,
}

// readOutcome reads a step's outcome file. ok is false when there is none,
// or it is no outcome.
func readOutcome(path string) (o stepOutcome, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return o, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Split(line, "\t")
	n, known := outcomeFields[f[0]]
	if !known || len(f)-1 != n {
		return o, false
	}
	return stepOutcome{kind: f[0], fields: f[1:]}, true
}

// line is the summary's line for a step: its glyph, the row's project,
// handle and version, and what became of it.
func (s walkStep) line(t terminal, h handleMap) string {
	e, f := s.row, s.outcome.fields
	glyph, colour, says := "◇", colourWaiting, s.outcome.kind
	switch s.outcome.kind {
	case "approved":
		glyph, colour = "✓", colourDone
		id, handle, version := f[0], f[1], f[2]
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
		glyph, colour, says = "⊘", colourRefused, "not reviewed · "+f[0]
	case "error":
		glyph, colour = "✗", colourRefused
		if len(f) > 0 {
			says += " · " + f[0]
		}
	}
	lead := "  " + glyph + " " + e.repo + " "
	return "  " + t.tint(colour, glyph) + " " + e.repo + " " + t.strong(e.name) + "  " +
		fit(says, t.width-columns(lead)-columns(e.name)-2)
}
