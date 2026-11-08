// valley pending: what waits on the operator, and what is moving, for a
// person to read.
//
// It opens with main's state (mainstate.go), one line for each project
// whose policy has classes that checks follow: clear, clearing or muddy.
// A project with no follow check has no line. A project that did not read
// says why on a line of its own.
//
// Then come two sections over every project listed, from the queue
// (queue.go). needs you holds the ready changes that wait on an approval,
// one row each, numbered in landing order. A stack of them is one row, its
// top's, which says how many changes it holds: approving the top lands
// every layer. Each row gives the change's handle and active version, its
// checks, and how long it has been ready, and its title goes on the line
// under it:
//
//	needs you (2, in landing order)
//	  1  smuk v4  checks 18/18 · stack of 2                              19m
//	     each ready change shows its active version's checks · oc-6335749
//	  2  wnzs v2  2 checks pending                                       42m
//	     the guest's tools follow main
//
// moving holds the ready changes that need no approval now, one line each,
// in the same order: an approved change, which waits on its checks and the
// integrator, with its checks; a change whose checks failed, naming them;
// and a change a restack holds, saying what its report says:
//
//	moving (3)
//	  ◐ mytm v5  approved · 6 checks pending
//	  ✗ rwzt v2  worker-e2e failed
//	  ✗ xzus v3  restack: a conflict rebasing xzus onto main at a1b2c3d
//
// Checks are counted as valley changes --checks reads them
// (versionchecks.go), which costs seconds for each project with a ready
// change and nothing for one without. When nothing waits on an approval,
// one line says so in place of needs you, and moving is left out when
// nothing moves. Older versions of a change are never listed.
//
// A last line counts the drafts and the branches on origin that no change
// is on, with the flags that list them in full. --drafts and --branches
// each print a section of blocks in place of their count. A block's first
// line is its name, with the age and author of its head: the handle and
// version of a draft on its branch, or the name of a branch. The lines
// indented under it give its title, then the commits and the diff the
// entry adds to its base:
//
//	deep-4                                    3 weeks ago · alice-agent-vm
//	  share the record bounds and the c-<change ID> request name
//	  10 commits · 31 files +2412 −404
//	  approving it lands the 2 below too
//	  ├ deep-3                                3 weeks ago · alice-agent-vm
//	  │   valley: changes --json and pending by handle, across a host's projects
//	  │   9 commits · 17 files +2701 −137
//	  └ deep-2                                3 weeks ago · alice-agent-vm
//	      change: read a draft as a third state
//	      14 commits · 20 files +1207 −219
//
// With -v, each row of needs you and moving is followed by its block,
// without the name line the row already gives, and a row's stack is
// drawn in full. branches holds each branch on origin, other than main,
// that main does not reach, that is no change's active head, and that no
// landed version's head reaches. drafts holds the changes whose record
// says they are drafts.
//
// A change's title is its record's, for the active version. A branch's
// title is the subject of its first own commit: the oldest commit on its
// first-parent line that its base does not hold. When a knot in the plan
// graph links a change, the first knot's ID follows the title, as
// "<title> · oc-6335749" (does.go).
//
// Each listing stacks the entries it lists, and only those. An entry's
// base is the nearest other listed entry its head strictly contains, or
// main when there is none. That is the ancestry includes reads
// (inventory.go). Its commits and diff are counted from the nearer of its
// base and the head of a landed version its head contains: a landed
// version's commits are not on main, since it landed as one new commit,
// and its content is. A stack is an entry that no other entry contains,
// the top, then every entry the top contains, as one list at one
// indentation, however deep the stack is. Each entry is listed before the
// entries it contains. The top's block says how many entries approving it
// lands with it. Each entry is on the entry listed right after it, and
// says "on <base>" where it is not, as at a fork, where two entries are on
// one base and the base is listed under only one of them. In drafts and
// branches, the stack with the latest commit comes first, and within a
// stack the entry with the latest commit does.
//
// A block may say more, each on a line of its own. includes <handle>
// names a change its head contains that is not listed, such as a
// withdrawn one. uncertain says the active version may have landed
// without its evidence being stored, and restack what a current restack
// report says. With --checks, a row says how many checks the policy
// requires of the change, by cost, and the full check state, as valley
// changes --checks lists it, naming those that failed: "checks: 18
// passed, 1 failed (valley-request)". When the host's worker telemetry
// holds the version's builds, the line goes on with what they cost and
// the checks that grew against their recent cost on the same capacity
// (checkcost.go): "checks: 21 passed in 9m10s (eval 24s); worker-e2e
// 431s, +68s against its recent 363s". -v gives the full check state
// with or without --checks.
//
// Several projects, as --all or --repo lists them, make one listing: each
// main line and each row names its project, and the rows of every project
// are numbered in one landing order.
//
// Every line fits stdout's terminal, or 80 columns when stdout is no
// terminal or does not know its width. A title or an author is cut short
// with …; a handle or a branch never is. On a terminal, names are bold and
// glyphs coloured, unless NO_COLOR is set.
//
// With --json it prints valley-pending/v1: valley-changes/v1's shape, with
// the pending changes alone in changes, each marked approved or not, the
// drafts in an array of their own, drafts, and the same branches. --checks
// gives each pending change its checks object.

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"the-valley/change"
	"the-valley/rebase"
)

// pendingChange is a ready change that has not landed as any version.
func pendingChange(s *snapshot, n recorded) bool {
	return n.record.State == change.Ready && !s.isLanded(n)
}

// approvedVersion reports whether a ready change's active version has an
// integration request at its head. That is the simple rule: the integrator
// also accepts a request that names an earlier head whose approval carries
// to this version, which needs the delta between the two and the policy's
// carry mode. A version approved only by carry is not counted here, so
// valley pending lists it with the changes awaiting approval.
func (s *snapshot) approvedVersion(id string, r *change.Record) bool {
	return r.State == change.Ready && s.request(id, r.ActiveVersion()) != nil
}

// unlanded is a change in any state that has not landed as any version:
// the changes a pending change may include.
func unlanded(s *snapshot, n recorded) bool {
	return !s.isLanded(n)
}

func isDraft(_ *snapshot, n recorded) bool {
	return n.record.State == change.Draft
}

// pendingView is a repository as valley-pending/v1 writes it: the pending
// changes in changes and the drafts in drafts, each with includes among
// the changes that have not landed.
func (l listed) pendingView(h handleMap) jsonRepo {
	r := l.repoView(h, pendingChange, unlanded)
	if l.err == nil {
		for i, c := range r.Changes {
			approved := c.Versions[c.Active-1].Request != nil
			r.Changes[i].Approved = &approved
		}
	}
	drafts := []jsonChange{}
	if l.err == nil {
		drafts = l.repoView(h, isDraft, unlanded).Changes
	}
	r.Drafts = &drafts
	return r
}

func runPending(g git, args []string, stdout, stderr io.Writer) error {
	l, err := parseListing("pending", args, true)
	if err != nil {
		return err
	}
	list, host, err := sources(g, l, stderr)
	if err != nil {
		return err
	}
	repos := readSources(list, newVerifier())
	readKnots(repos, l.verbose, stderr)
	h := handles(repos)
	writeHandleCache(g, repos, h)
	defer warnUnverified(repos, stderr)
	if l.asJSON {
		doc := jsonChanges{Schema: pendingSchema, Host: host, Repos: []jsonRepo{}}
		c := newCosts()
		for _, r := range repos {
			view := r.pendingView(h)
			if l.checks {
				r.addChecks(&view, c, stderr)
			}
			utf8Branches(&view, stderr)
			doc.Repos = append(doc.Repos, view)
		}
		return writeDocument(stdout, doc)
	}
	writePending(repos, h, l, newCosts(), stdout, stderr, terminalOf(stdout), len(repos) > 1 || l.all || l.repo != "")
	return nil
}

// entry is one row or block of valley pending: a ready change, a draft,
// or a branch no record is on.
type entry struct {
	section  string
	repo     string             // the project it is in
	id       string             // the change ID, for a change
	record   string             // the change's record commit
	name     string             // what the block's first line names
	ref      string             // what the entry is typed as: its handle, or its branch
	head     string             // the head commit
	title    string             // the record's title; for a branch, read by measure
	knot     string             // ID of the first knot that links the change (does.go)
	includes []string           // what includes names that is no entry here
	notes    []string           // uncertain and restack
	requires string             // with --checks, the checks the policy requires of it, by cost
	checks   string             // the check state line of a ready change
	state    *jsonVersionChecks // the check state its row counts, or nil when its checks were not read

	glyph, colour, says string // for a moving change, its glyph and what its row says

	when        int64 // the head's commit time
	since       int64 // for a change, its record's commit time: when its active version became ready
	age, author string
	layer       string // the commits and the diff it adds to its base

	parent *entry   // the entry it is listed under, if any
	kids   []*entry // the entries nested right under it
	base   *entry   // nil for main
	inside []*entry // every entry whose head strictly contains its own
	below  []*entry // for the top of a stack, every entry listed under it
	latest int64    // for the top of a stack, the latest commit time in it
}

// writePending prints the listing of every repository: the main lines,
// needs you, moving, and the drafts and branches. named says whether each
// line names its project.
func writePending(repos []listed, h handleMap, l listing, c *costs, w, stderr io.Writer, t terminal, named bool) {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	head := false
	for _, r := range repos {
		if r.err != nil {
			p("%s: %v\n", r.name, r.err)
			head = true
			continue
		}
		for _, line := range r.mainState(h, c, stderr, t, named) {
			p("%s\n", line)
			head = true
		}
	}
	if head {
		p("\n")
	}
	var waiting, moving []*entry
	var drafts, branches []block
	nDrafts, nBranches := 0, 0
	for _, r := range repos {
		if r.err != nil {
			continue
		}
		view := r.pendingView(h)
		r.addChecks(&view, c, stderr)
		wa, mo := r.queue(view, h, l, l.verbose)
		waiting, moving = append(waiting, wa...), append(moving, mo...)
		nDrafts, nBranches = nDrafts+len(*view.Drafts), nBranches+len(view.Branches)
		if l.drafts && len(*view.Drafts) > 0 {
			drafts = append(drafts, block{r.name, r.draftStacks(view, h, l)})
		}
		if l.branches && len(view.Branches) > 0 {
			branches = append(branches, block{r.name, r.branchStacks(view)})
		}
	}
	byLanding(waiting)
	byLanding(moving)
	for _, line := range t.rows(waiting, moving, l, named) {
		p("%s\n", line)
	}
	for _, b := range []struct {
		name   string
		blocks []block
	}{{"drafts", drafts}, {"branches", branches}} {
		for _, bl := range b.blocks {
			header := b.name
			if named {
				header += " · " + bl.repo
			}
			p("\n%s\n", header)
			for i, top := range bl.tops {
				if i > 0 {
					p("\n")
				}
				for _, line := range t.drawStack(top, "", true) {
					p("%s\n", line)
				}
			}
		}
	}
	var counts, flags []string
	if n := nDrafts; n > 0 && !l.drafts {
		counts, flags = append(counts, counted(n, "draft")), append(flags, "--drafts")
	}
	if n := nBranches; n > 0 && !l.branches {
		noun := "branches"
		if n == 1 {
			noun = "branch"
		}
		counts, flags = append(counts, fmt.Sprintf("%d %s no change is on", n, noun)), append(flags, "--branches")
	}
	if len(counts) > 0 {
		p("\n%s\n", strings.Join(counts, " · ")+" · valley pending "+strings.Join(flags, " "))
	}
}

// block is one project's stacks of drafts or of branches.
type block struct {
	repo string
	tops []*entry
}

// rows is needs you and moving as lines: each section's heading, then its
// rows. A row of needs you is numbered and has its title under it; with
// -v, a row of either is followed by its block.
func (t terminal) rows(waiting, moving []*entry, l listing, named bool) []string {
	var lines []string
	projects, names := 0, 0
	for _, e := range append(append([]*entry{}, waiting...), moving...) {
		projects, names = max(projects, columns(e.repo)), max(names, columns(e.name))
	}
	// lead is a row's project and name, each padded to its column.
	lead := func(e *entry) string {
		s := t.strong(e.name) + strings.Repeat(" ", names-columns(e.name))
		if named {
			s = e.repo + strings.Repeat(" ", projects-columns(e.repo)) + "  " + s
		}
		return s
	}
	width := names
	if named {
		width += projects + 2
	}
	if len(waiting) == 0 {
		lines = append(lines, "nothing needs you")
	} else {
		lines = append(lines, fmt.Sprintf("needs you (%d, in landing order)", len(waiting)))
	}
	digits := len(strconv.Itoa(len(waiting)))
	now := time.Now().Unix()
	for i, e := range waiting {
		num := fmt.Sprintf("  %*d  ", digits, i+1)
		under := strings.Repeat(" ", columns(num))
		says := checkCount(e.state)
		if len(e.below) > 0 {
			says = strings.TrimPrefix(says+" · stack of "+strconv.Itoa(len(e.below)+1), " · ")
		}
		left := num + lead(e) + "  " + says
		used := columns(num) + width + 2 + columns(says)
		age := ago(now, e.since)
		if pad := t.width - used - columns(age); pad >= 2 && e.since > 0 {
			left += strings.Repeat(" ", pad) + age
		} else if e.since > 0 {
			left += "  " + age
		}
		lines = append(lines, strings.TrimRight(left, " "))
		lines = append(lines, t.details(e, under, l)...)
	}
	if len(moving) == 0 {
		return lines
	}
	lines = append(lines, "", fmt.Sprintf("moving (%d)", len(moving)))
	for _, e := range moving {
		first := "  " + t.tint(e.colour, e.glyph) + " "
		used := columns("  "+e.glyph+" ") + width + 2
		lines = append(lines, first+lead(e)+"  "+fit(e.says, t.width-used))
		lines = append(lines, t.details(e, "    ", l)...)
	}
	return lines
}

// details are the lines under a row, each led by under: for a row of
// needs you, its title; with --checks, its checks; with -v, its block in
// place of both.
func (t terminal) details(e *entry, under string, l listing) []string {
	if l.verbose {
		return t.drawStack(e, under, false)
	}
	var lines []string
	if e.section == "ready" && e.glyph == "" {
		lines = append(lines, under+t.titled(e, under))
	}
	if l.checks {
		for _, s := range []string{e.requires, e.checks} {
			if s != "" {
				lines = append(lines, under+t.fit(s, under))
			}
		}
	}
	return lines
}

// ago is how long ago then was, from now, in its largest whole unit: 40s,
// 19m, 5h, 3d.
func ago(now, then int64) string {
	d := max(now-then, 0)
	switch {
	case d < 60:
		return fmt.Sprintf("%ds", d)
	case d < 3600:
		return fmt.Sprintf("%dm", d/60)
	case d < 2*86400:
		return fmt.Sprintf("%dh", d/3600)
	}
	return fmt.Sprintf("%dd", d/86400)
}

func parseUnix(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// draftStacks are a repository's drafts as stacks, measured, the stack
// with the latest commit first.
func (r listed) draftStacks(view jsonRepo, h handleMap, l listing) []*entry {
	states := r.states()
	var all []*entry
	for _, c := range *view.Drafts {
		all = append(all, r.changeEntry("drafts", c, h, l, states))
	}
	r.dated(all)
	return r.snap.stack(all, true)
}

// branchStacks are a repository's branches that no change is on as
// stacks, measured, the stack with the latest commit first.
func (r listed) branchStacks(view jsonRepo) []*entry {
	var all []*entry
	for _, b := range view.Branches {
		name := branchName(b.Branch)
		all = append(all, &entry{section: "branches", repo: r.name, name: name, ref: name, head: b.Head})
	}
	r.dated(all)
	return r.snap.stack(all, true)
}

// mainState is the project's main state (mainstate.go) as lines, or none
// when origin has no main or the policy at its tip declares no nix follow
// check. named puts the project's name after main. A policy or a walk that
// did not read, or worker telemetry that did not, is said on stderr.
func (r listed) mainState(h handleMap, c *costs, stderr io.Writer, t terminal, named bool) []string {
	if r.snap.tip == "" {
		return nil
	}
	instance, project, done, err := policyLayers(r.g, r.snap.tip)
	defer done()
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s: the policy at main did not read, so main's state is not shown: %v\n", r.name, err)
		return nil
	}
	walk, err := mainFollows(r.g, r.snap.tip, instance, project, io.Discard)
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s: main's state did not read: %v\n", r.name, err)
		return nil
	}
	if len(walk.checks) == 0 {
		return nil
	}
	read := c.start(r.g)
	<-read.done
	if read.why != "" {
		fmt.Fprintf(stderr, "valley: %s: main's state shows no worker runs: %s\n", r.name, read.why)
	}
	lead := "main  "
	if named {
		lead += r.name + "  "
	}
	return t.mainLines(walk, lead, r.name, h, read.builds, read.mains)
}

// stack nests each entry under the nearest entry whose head strictly
// contains its own, and gives each entry its base: the nearest entry its
// own head strictly contains, as the rebase engine stacks layers
// (rebase.Snapshot.Nest). Where two are equally near, the one with the
// later commit is taken. It returns the entries nested under none, each the
// top of a stack, the stack with the latest commit first. Each top lists
// every entry nested under it, at any depth, in its below. With measured,
// each entry is measured for its block.
func (s *snapshot) stack(all []*entry, measured bool) []*entry {
	nodes := make([]rebase.Node, len(all))
	for i, e := range all {
		nodes[i] = rebase.Node{Name: e.name, Head: e.head, When: e.when}
	}
	nest := s.land.Nest(nodes)
	for i, e := range all {
		for _, j := range nest.Inside[i] {
			e.inside = append(e.inside, all[j])
		}
		if p := nest.Parent[i]; p >= 0 {
			e.parent = all[p]
			e.parent.kids = append(e.parent.kids, e)
		}
	}
	var tops []*entry
	for i, e := range all {
		if b := nest.Base[i]; b >= 0 {
			e.base = all[b]
		}
		if measured {
			s.measure(e)
		}
		if e.parent == nil {
			tops = append(tops, e)
		}
	}
	for _, top := range tops {
		top.list()
	}
	byActivity(tops, func(e *entry) int64 { return e.latest })
	return tops
}

// list puts every entry nested under the top of a stack in its below, each
// before the entries it contains. Where it can, it lists next the base of
// the entry it listed last, so a chain is listed unbroken. Otherwise it
// lists next the entry with the latest commit. It sets latest, the latest
// commit time in the stack.
func (top *entry) list() {
	nested := map[*entry]bool{}
	var walk func(e *entry)
	walk = func(e *entry) {
		for _, k := range e.kids {
			nested[k] = true
			walk(k)
		}
	}
	walk(top)
	top.latest = top.when
	for last := top; len(nested) > 0; {
		var free []*entry // those every entry containing them is listed before
		for e := range nested {
			listable := true
			for _, c := range e.inside {
				listable = listable && !nested[c]
			}
			if listable {
				free = append(free, e)
			}
		}
		byActivity(free, func(e *entry) int64 { return e.when })
		next := free[0]
		for _, e := range free {
			if e == last.base {
				next = e
			}
		}
		delete(nested, next)
		top.below = append(top.below, next)
		top.latest = max(top.latest, next.when)
		last = next
	}
}

// byActivity orders entries latest first, and then by name.
func byActivity(list []*entry, when func(*entry) int64) {
	sort.SliceStable(list, func(i, j int) bool {
		if a, b := when(list[i]), when(list[j]); a != b {
			return a > b
		}
		return list[i].name < list[j].name
	})
}

// measure reads what an entry adds to what it stands on: how many
// commits, and the diff. It stands on the nearest of its base and every
// landed version's head below it, as the rebase engine cuts a landed
// version's commits from a family, or on its merge base with main when
// there is none. A landed version's commits are not on main, since it
// landed as one new commit, and its content is. A branch's title is the
// subject of the first of the entry's own commits on its first-parent
// line.
func (s *snapshot) measure(e *entry) {
	var below []string
	for _, h := range s.land.LandedUnder(e.head) {
		if h != e.head {
			below = append(below, h)
		}
	}
	if e.base != nil {
		below = append(below, e.base.head)
	}
	var not []string
	from := s.tip
	for _, h := range below {
		not = append(not, "^"+h)
		// The heads below are all the entry's ancestors, and the nearest
		// is the one that contains every other.
		if from == s.tip || s.strictAncestor(from, h) {
			from = h
		}
	}
	if s.tip != "" {
		not = append(not, "^"+s.tip)
	}
	if from == "" {
		from, _ = s.g.input(true, strings.NewReader(""), "hash-object", "-t", "tree", "--stdin")
	}
	commits := "? commits"
	if out, err := s.g.output(true, append([]string{"rev-list", "--count", e.head}, not...)...); err == nil {
		n, _ := strconv.Atoi(out)
		commits = counted(n, "commit")
	}
	files, added, deleted := 0, 0, 0
	out, _ := s.g.output(true, "diff", "--numstat", from+"..."+e.head)
	for _, line := range strings.Split(out, "\n") {
		if f := strings.SplitN(line, "\t", 3); len(f) == 3 {
			a, _ := strconv.Atoi(f[0]) // a binary file's counts are -, read as 0
			d, _ := strconv.Atoi(f[1])
			files, added, deleted = files+1, added+a, deleted+d
		}
	}
	diff := "no file changes"
	if files > 0 {
		diff = fmt.Sprintf("%s +%d −%d", counted(files, "file"), added, deleted)
	}
	e.layer = commits + " · " + diff
	if e.section == "branches" {
		e.title = s.branchTitle(e.head, not)
	}
}

// branchTitle is a branch's title: the subject of the first of its own
// commits on its first-parent line, those its head reaches and not leaves
// out.
func (s *snapshot) branchTitle(head string, not []string) string {
	out, _ := s.g.output(true, append([]string{"log", "--first-parent", "--reverse", "--format=%s", head}, not...)...)
	first, _, _ := strings.Cut(out, "\n")
	return readable(change.Title(first))
}

func counted(n int, noun string) string {
	switch n {
	case 0:
		return "no " + noun + "s"
	case 1:
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// drawStack is the lines of a stack, each led by lead: the top entry's
// block, then the block of each entry listed under it, at one indentation
// however deep the stack is. Each listed entry is on the entry listed
// right after it, and says "on <base>" where it is not. With named false,
// the top's name line is left out, and its other lines are at lead
// itself, for a row that already names it.
func (t terminal) drawStack(top *entry, lead string, named bool) []string {
	list := append([]*entry{top}, top.below...)
	in := lead
	if named {
		in = lead + "  "
	}
	var lines []string
	for i, e := range list {
		var next *entry
		if i+1 < len(list) {
			next = list[i+1]
		}
		first, rest := lead, in
		switch {
		case i > 0 && next != nil:
			first, rest = in+"├ ", in+"│   "
		case i > 0:
			first, rest = in+"└ ", in+"    "
		}
		if i > 0 || named {
			lines = append(lines, t.nameLine(first, e.name, e, rest)...)
		}
		lines = append(lines, rest+t.titled(e, rest), rest+t.fit(e.layer, rest))
		switch {
		case e.base == next:
		case e.base != nil:
			lines = append(lines, rest+"on "+e.base.ref)
		default:
			lines = append(lines, rest+"on "+recordTarget)
		}
		lines = append(lines, prefixed(rest, e.includes)...)
		for _, note := range append(append([]string{}, e.notes...), e.requires, e.checks) {
			if note != "" {
				lines = append(lines, rest+t.fit(note, rest))
			}
		}
		if i == 0 && len(top.below) > 0 {
			lands := fmt.Sprintf("approving it lands the %d below too", len(top.below))
			if len(top.below) == 1 {
				lands = "approving it lands the one below too"
			}
			lines = append(lines, rest+t.fit(lands, rest))
		}
	}
	return lines
}

// titled formats e's title line after lead, appending " · <knot ID>" when a
// knot links the change. Long titles are truncated so the knot ID fits in
// full.
func (t terminal) titled(e *entry, lead string) string {
	if e.knot == "" {
		return t.fit(e.title, lead)
	}
	tail := " · " + e.knot
	return fit(e.title, t.width-columns(lead+tail)) + tail
}

func prefixed(lead string, lines []string) []string {
	var out []string
	for _, l := range lines {
		out = append(out, lead+l)
	}
	return out
}

// nameLine is an entry's first line: its name, in bold on a terminal, and
// the head's age and author at the right margin. When that leaves too
// little room, the author is cut short. When even that leaves too little,
// the age and author go on a line of their own, indented as rest is.
func (t terminal) nameLine(lead, name string, e *entry, rest string) []string {
	left := lead + t.strong(name)
	used := columns(lead + name)
	if e.age == "" && e.author == "" {
		return []string{left}
	}
	right := e.age + " · " + e.author
	if room := t.width - used - 2; columns(right) > room {
		short := room - columns(e.age+" · ")
		if short < 4 {
			return []string{left, rest + t.fit(right, rest)}
		}
		right = e.age + " · " + fit(e.author, short)
	}
	return []string{left + strings.Repeat(" ", t.width-used-columns(right)) + right}
}

// branchName is a branch's name as printed: as it is, or, when it holds a
// character that is not printable or bytes that are not UTF-8, in the
// $'...' form a shell reads back as the same name.
func branchName(b string) string {
	if printable(b) {
		return b
	}
	return shellQuote(b)
}

// readable is s as text to print: valid UTF-8, with every control
// character, which a terminal could act on, made a space.
func readable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
}

// checks says how many checks the policy requires of a head, by cost, as
// valley checks derives them against the merge base with main. It runs
// the valley command bin/valley names in VALLEY_COMMAND, inside the
// repository the change is in.
func (s *snapshot) checks(head string) string {
	valley := os.Getenv("VALLEY_COMMAND")
	if valley == "" {
		valley = "valley"
	}
	base, err := s.g.output(true, "merge-base", s.tip, head)
	if err != nil {
		return "requires ? (no merge base with main)"
	}
	cmd := exec.Command(valley, "checks", base, head)
	cmd.Dir = s.g.dir
	cmd.Env = s.g.env
	out, err := cmd.Output()
	if err != nil {
		return "requires ? (the policy did not derive)"
	}
	count := func(prefix string) int {
		for _, line := range strings.Split(string(out), "\n") {
			if rest, ok := strings.CutPrefix(line, prefix); ok && rest != "none" {
				return len(strings.Split(rest, ", "))
			}
		}
		return 0
	}
	quick, slow := count("quick: "), count("slow: ")
	if quick+slow == 0 {
		return "requires no checks"
	}
	noun := "checks"
	if quick == 1 {
		noun = "check"
	}
	return fmt.Sprintf("requires %d quick %s, %d slow", quick, noun, slow)
}
