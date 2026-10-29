// valley pending: what is waiting to land, for a person to read.
//
// It prints three sections, in this order. ready holds the ready changes
// whose active version has not landed, by the first two landing rules
// (inventory.go). branches holds each branch on origin, other than main,
// that main does not reach, that is no change's active head, and that no
// landed version's head reaches. drafts
// holds the changes whose record says they are drafts. When no ready
// change is at the top of a stack, one line says so in place of ready,
// and says where any ready change inside another section's stack is.
//
// Each entry is a block. Its first line is its name, with the age and
// author of its head: the handle and version of a change, the name of a
// branch, and for a draft its handle and version on its branch. The lines
// indented under it give its title, then the commits and the diff the
// entry adds to its base:
//
//	act-on-a-change-by-handle-key-its-1902807a          9 hours ago · alice-agent-vm
//	  change: share the record bounds and the c-<change ID> request name
//	  10 commits · 31 files +2412 −404
//	  approving it lands the 2 below too
//	  ├ find-changes-across-repos-valley-changes-1e788c4f  10 hours ago · alice-…
//	  │   valley: changes --json and pending by handle, across a host's projects
//	  │   9 commits · 17 files +2701 −137
//	  └ a-draft-state-valley-unready-and-what-56ed34ac  12 hours ago · alice-ag…
//	      change: read a draft as a third state
//	      14 commits · 20 files +1207 −219
//
// A change's title is its record's, for the active version. A branch's
// title is the subject of its first own commit: the oldest commit on its
// first-parent line that its base does not hold. When a knot in the plan
// graph links a change, the first knot's ID follows the title, as
// "<title> · oc-6335749" (does.go).
//
// An entry's base is the nearest other entry its head strictly contains,
// or main when there is none. That is the ancestry includes reads
// (inventory.go). Its commits and diff are counted from the nearer of its
// base and the head of a landed version its head contains: a landed
// version's commits are not on main, since it landed as one new commit,
// and its content is. A stack is an entry that no other entry contains, the
// top, then every entry the top contains, as one list at one indentation,
// however deep the stack is. Each entry is listed before the entries it
// contains. The top's block says how many entries approving it lands with
// it. Each entry is on the entry listed right after it, and says
// "on <base>" where it is not, as at a fork, where two entries are on one
// base and the base is listed under only one of them. An entry the top
// does not contain is never listed under it. A stack is printed in the
// section of its top, and a listed entry from another section says which
// it is in.
//
// A change may say more, each on a line of its own. includes <handle>
// names a change its head contains that is not an entry here, such as a
// withdrawn one. uncertain says the active version may have landed without
// its evidence being stored. With --checks, it says how many checks the
// policy requires of the change, by cost.
//
// Within a section, the stack with the latest commit comes first, and
// within a stack the entry with the latest commit does. Every line fits
// stdout's terminal, or 80 columns when stdout is no terminal or does not
// know its width. A title or an author is cut short with …; a handle or a
// branch never is. On a terminal, names are bold, unless NO_COLOR is set.
//
// With --json it prints valley-pending/v1: valley-changes/v1's shape, with
// the pending changes alone in changes, the drafts in an array of their
// own, drafts, and the same branches.

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"the-valley/change"
	"the-valley/rebase"
)

// pendingChange is a ready change whose active version has not landed.
func pendingChange(s *snapshot, n recorded) bool {
	return n.record.State == change.Ready && !s.isLanded(n.id, n.record.ActiveVersion())
}

// unlanded is a change in any state whose active version has not landed:
// the changes a pending change may include.
func unlanded(s *snapshot, n recorded) bool {
	return !s.isLanded(n.id, n.record.ActiveVersion())
}

func isDraft(_ *snapshot, n recorded) bool {
	return n.record.State == change.Draft
}

// pendingView is a repository as valley-pending/v1 writes it: the pending
// changes in changes and the drafts in drafts, each with includes among
// the changes that have not landed.
func (l listed) pendingView(h handleMap) jsonRepo {
	r := l.repoView(h, pendingChange, unlanded)
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
		for _, r := range repos {
			view := r.pendingView(h)
			utf8Branches(&view, stderr)
			doc.Repos = append(doc.Repos, view)
		}
		return writeDocument(stdout, doc)
	}
	t := terminalOf(stdout)
	for i, r := range repos {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		writePending(r, h, l, stdout, len(repos) > 1 || l.all || l.repo != "", t)
	}
	return nil
}

// sections are valley pending's sections, in the order they are printed,
// each with the word a nested entry from it is marked with.
var sections = []struct{ name, kind string }{
	{"ready", "ready"}, {"branches", "branch"}, {"drafts", "draft"},
}

// entry is one block of valley pending: a ready change, a branch no record
// is on, or a draft.
type entry struct {
	section  string
	name     string   // what the block's first line names
	ref      string   // what the entry is typed as: its handle, or its branch
	head     string   // the head commit
	title    string   // the record's title; for a branch, read by measure
	knot     string   // ID of the first knot that links the change (does.go)
	includes []string // what includes names that is no entry here
	notes    []string // uncertain and checks

	when        int64 // the head's commit time
	age, author string
	layer       string // the commits and the diff it adds to its base

	parent *entry   // the entry it is listed under, if any
	kids   []*entry // the entries nested right under it
	base   *entry   // nil for main
	inside []*entry // every entry whose head strictly contains its own
	below  []*entry // for the top of a stack, every entry listed under it
	latest int64    // for the top of a stack, the latest commit time in it
}

// writePending prints one repository's entries, section by section.
// Several repositories are each headed by their name.
func writePending(r listed, h handleMap, l listing, w io.Writer, headed bool, t terminal) {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	if r.err != nil {
		p("%s: %v\n", r.name, r.err)
		return
	}
	if headed {
		p("%s\n%s\n", r.name, strings.Repeat("─", min(columns(r.name), t.width)))
	}
	all := r.entries(r.pendingView(h), h, l)
	tops := r.snap.stack(all)
	gap := false
	for _, sec := range sections {
		var here []*entry
		for _, e := range tops {
			if e.section == sec.name {
				here = append(here, e)
			}
		}
		if sec.name == "ready" && len(here) == 0 {
			p("%s\n", readyElsewhere(tops))
			gap = true
			continue
		}
		if len(here) == 0 {
			continue
		}
		if gap {
			p("\n")
		}
		p("%s\n", sec.name)
		for i, e := range here {
			if i > 0 {
				p("\n")
			}
			for _, line := range t.drawStack(e) {
				p("%s\n", line)
			}
		}
		gap = true
	}
}

// entries are a repository's entries, as its pending view lists them, each
// with its head's commit time, age and author.
func (r listed) entries(view jsonRepo, h handleMap, l listing) []*entry {
	s := r.snap
	states := map[handleKey]string{}
	for _, n := range s.all {
		states[handleKey{r.name, n.record.Target, n.id}] = n.record.State
	}
	var all []*entry
	recorded := func(section string, c jsonChange) {
		v := c.Versions[c.Active-1]
		e := &entry{section: section, name: fmt.Sprintf("%s v%d", c.Handle, v.N), ref: c.Handle, head: v.Head, title: readable(v.Title)}
		if len(c.Knots) > 0 {
			e.knot = readable(c.Knots[0].ID)
		}
		if section == "drafts" && v.Branch != "" {
			e.name += " on " + branchName(v.Branch)
		}
		for _, id := range c.Includes {
			k := handleKey{r.name, c.Target, id}
			if st := states[k]; st != change.Ready && st != change.Draft {
				e.includes = append(e.includes, fmt.Sprintf("includes %s (%s)", h[k], st))
			}
		}
		if v.Landing == uncertain {
			e.notes = append(e.notes, "uncertain: it may have landed without its evidence being stored")
		}
		if c.Restack != nil && c.Restack.Current {
			name := func(id string) string { return h[handleKey{r.name, c.Target, id}] }
			e.notes = append(e.notes, "restack: "+readable(restackSays(c.Restack.Report, name)))
		}
		if l.checks {
			e.notes = append(e.notes, s.checks(v.Head))
		}
		all = append(all, e)
	}
	for _, c := range view.Changes {
		recorded("ready", c)
	}
	for _, b := range view.Branches {
		name := branchName(b.Branch)
		all = append(all, &entry{section: "branches", name: name, ref: name, head: b.Head})
	}
	for _, c := range *view.Drafts {
		recorded("drafts", c)
	}
	for _, e := range all {
		out, _ := s.g.output(true, "log", "-1", "--format=%ct%x00%cr%x00%an", e.head)
		if fields := strings.SplitN(out, "\x00", 3); len(fields) == 3 {
			e.when, _ = strconv.ParseInt(fields[0], 10, 64)
			e.age, e.author = fields[1], readable(fields[2])
		}
	}
	return all
}

// stack nests each entry under the nearest entry whose head strictly
// contains its own, and gives each entry its base: the nearest entry its
// own head strictly contains, as the rebase engine stacks layers
// (rebase.Snapshot.Nest). Where two are equally near, the one with the
// later commit is taken. It returns the entries nested under none, each the
// top of a stack, the stack with the latest commit first. Each top lists
// every entry nested under it, at any depth, in its below.
func (s *snapshot) stack(all []*entry) []*entry {
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
		s.measure(e)
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

// readyElsewhere is what the ready section says when no ready change is at
// the top of a stack: that nothing is ready, or where the ready changes in
// other sections' stacks are.
func readyElsewhere(tops []*entry) string {
	n, stacks := 0, 0
	var where []string
	for _, sec := range sections {
		found := false
		for _, top := range tops {
			held := 0
			for _, e := range top.below {
				if e.section == "ready" {
					held++
				}
			}
			if top.section == sec.name && held > 0 {
				n, stacks, found = n+held, stacks+1, true
			}
		}
		if found {
			where = append(where, sec.name)
		}
	}
	if n == 0 {
		return "nothing is ready"
	}
	verb, in := "are", fmt.Sprintf("%d stacks", stacks)
	if n == 1 {
		verb = "is"
	}
	if stacks == 1 {
		in = "a stack"
	}
	return fmt.Sprintf("nothing is ready on its own; %s %s in %s under %s", counted(n, "ready change"), verb, in, strings.Join(where, " and "))
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

// drawStack is the lines of a stack: the top entry's block, then the
// block of each entry listed under it, at one indentation however deep the
// stack is. Each listed entry is on the entry listed right after it, and
// says "on <base>" where it is not.
func (t terminal) drawStack(top *entry) []string {
	list := append([]*entry{top}, top.below...)
	var lines []string
	for i, e := range list {
		var next *entry
		if i+1 < len(list) {
			next = list[i+1]
		}
		first, rest := "", "  "
		switch {
		case i > 0 && next != nil:
			first, rest = "  ├ ", "  │   "
		case i > 0:
			first, rest = "  └ ", "      "
		}
		suffix := ""
		if e.section != top.section {
			for _, sec := range sections {
				if sec.name == e.section {
					suffix = " (" + sec.kind + ")"
				}
			}
		}
		lines = append(lines, t.nameLine(first, e.name, suffix, e, rest)...)
		lines = append(lines, rest+t.titled(e, rest), rest+t.fit(e.layer, rest))
		switch {
		case e.base == next:
		case e.base != nil:
			lines = append(lines, rest+"on "+e.base.ref)
		default:
			lines = append(lines, rest+"on "+recordTarget)
		}
		lines = append(lines, prefixed(rest, e.includes)...)
		for _, note := range e.notes {
			lines = append(lines, rest+t.fit(note, rest))
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
func (t terminal) nameLine(lead, name, suffix string, e *entry, rest string) []string {
	left := lead + t.strong(name) + suffix
	used := columns(lead + name + suffix)
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
