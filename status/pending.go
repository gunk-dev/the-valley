// valley pending: what is waiting to land, for a person to read.
//
// It prints three sections, in this order. ready holds the ready changes
// whose active version has not landed, by the first two landing rules
// (inventory.go). branches holds each branch on origin, other than main,
// that main does not reach and that is no change's active head. drafts
// holds the changes whose record says they are drafts. When no ready
// change is at the top of a stack, one line says so in place of ready.
//
// Each entry is a block. Its first line is its name, with the age and
// author of its head: the handle and version of a change, the name of a
// branch, and for a draft its handle and version on its branch. The lines
// indented under it give its title, then the commits and the diff the
// entry adds to its base:
//
//	act-on-a-change-by-handle-key-its-1902807a          9 hours ago · patflynn-agent
//	  change: share the record bounds and the c-<change ID> request name
//	  10 commits · 31 files +2412 −404
//	  └ find-changes-across-repos-valley-changes-1e788c4f  10 hours ago · patfly…
//	      valley: changes --json and pending by handle, across a host's projects
//	      9 commits · 17 files +2701 −137
//
// A change's title is its record's, for the active version. A branch's
// title is the subject of its first own commit: the oldest commit on its
// first-parent line that its base does not hold.
//
// An entry's base is the nearest other entry its head strictly contains,
// or main when there is none. That is the ancestry includes reads
// (inventory.go). Each entry is nested under the nearest entry that
// contains it, so approving the entry at the top of a stack lands every
// entry nested under it. A stack is printed in the section of its top
// entry, and a nested entry from another section says which it is in. Past
// the third level the stack is drawn no deeper, and an entry whose base is
// not drawn right under it says "on <base>".
//
// A change may say more, each on a line of its own. includes <handle>
// names a change its head contains that is not an entry here, such as a
// withdrawn one. uncertain says the active version may have landed without
// its evidence being stored. With --owes, it says how many checks the
// change owes, by cost.
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
	repos := readSources(list, verifierFromEnv())
	h := handles(repos)
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

// maxDepth is the deepest level a stack is drawn at. The top is level 0.
const maxDepth = 2

// entry is one block of valley pending: a ready change, a branch no record
// is on, or a draft.
type entry struct {
	section  string
	name     string   // what the block's first line names
	ref      string   // what the entry is typed as: its handle, or its branch
	head     string   // the head commit
	title    string   // the record's title; for a branch, read by measure
	includes []string // what includes names that is not nested under it
	notes    []string // uncertain and owes

	when        int64 // the head's commit time
	age, author string
	layer       string // the commits and the diff it adds to its base

	parent *entry
	kids   []*entry
	base   *entry // nil for main
	latest int64  // the latest commit time in the stack it tops
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
			nested := false
			for _, e := range all {
				nested = nested || e.section == "ready"
			}
			if nested {
				p("nothing is ready at the top of a stack\n")
			} else {
				p("nothing is ready\n")
			}
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
		if l.owes {
			e.notes = append(e.notes, s.owes(v.Head))
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
// own head strictly contains. Where two are equally near, the one with the
// later commit is taken. It returns the entries nested under none, the
// stack with the latest commit first, with every list of nested entries
// ordered the same way.
func (s *snapshot) stack(all []*entry) []*entry {
	in := make([][]bool, len(all)) // in[i][j]: entry i's head strictly contains entry j's
	for i := range all {
		in[i] = make([]bool, len(all))
		for j := range all {
			in[i][j] = i != j && s.strictAncestor(all[j].head, all[i].head)
		}
	}
	// nearest is the entries i contains, or that contain i, with none
	// between.
	nearest := func(i int, above bool) []*entry {
		var near []*entry
		for j := range all {
			outer, inner := j, i
			if !above {
				outer, inner = i, j
			}
			if !in[outer][inner] {
				continue
			}
			between := false
			for k := range all {
				between = between || in[outer][k] && in[k][inner]
			}
			if !between {
				near = append(near, all[j])
			}
		}
		byActivity(near, func(e *entry) int64 { return e.when })
		return near
	}
	for i, e := range all {
		if above := nearest(i, true); len(above) > 0 {
			e.parent = above[0]
			e.parent.kids = append(e.parent.kids, e)
		}
	}
	var tops []*entry
	for i, e := range all {
		below := nearest(i, false)
		for _, d := range below {
			if d.parent == e {
				e.base = d
				break
			}
		}
		if e.base == nil && len(below) > 0 {
			e.base = below[0]
		}
		s.measure(e)
		if e.parent == nil {
			tops = append(tops, e)
		}
	}
	var latest func(e *entry) int64
	latest = func(e *entry) int64 {
		e.latest = e.when
		for _, k := range e.kids {
			e.latest = max(e.latest, latest(k))
		}
		byActivity(e.kids, func(e *entry) int64 { return e.latest })
		return e.latest
	}
	for _, e := range tops {
		latest(e)
	}
	byActivity(tops, func(e *entry) int64 { return e.latest })
	return tops
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

// measure reads what an entry adds to its base: how many commits, and the
// diff from the base, or from its merge base with main. A branch's title
// is the subject of the first of those commits on its first-parent line.
func (s *snapshot) measure(e *entry) {
	var not []string
	from := s.tip
	if e.base != nil {
		not, from = append(not, "^"+e.base.head), e.base.head
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
		out, _ := s.g.output(true, append([]string{"log", "--first-parent", "--reverse", "--format=%s", e.head}, not...)...)
		first, _, _ := strings.Cut(out, "\n")
		e.title = readable(change.Title(first))
	}
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

// drawStack is the lines of a stack: each entry's block, every nested
// entry indented under the one that contains it, and joined to it by a
// line drawn in the margin.
func (t terminal) drawStack(top *entry) []string {
	type drawn struct {
		e     *entry
		depth int
		last  bool // no later entry at its depth before one nearer the top
	}
	var list []drawn
	var walk func(e *entry, depth int)
	walk = func(e *entry, depth int) {
		list = append(list, drawn{e: e, depth: min(depth, maxDepth)})
		for _, k := range e.kids {
			walk(k, depth+1)
		}
	}
	walk(top, 0)
	for i := range list {
		list[i].last = true
		for j := i + 1; j < len(list) && list[j].depth >= list[i].depth; j++ {
			if list[j].depth == list[i].depth {
				list[i].last = false
				break
			}
		}
	}
	rail := func(more bool) string {
		if more {
			return "│   "
		}
		return "    "
	}
	var lines []string
	more := make([]bool, maxDepth+1) // more[k]: the entry last drawn at depth k has more after it
	for _, d := range list {
		more[d.depth] = !d.last
		first, rest := "", "  "
		if d.depth > 0 {
			lead := "  "
			for k := 1; k < d.depth; k++ {
				lead += rail(more[k])
			}
			first, rest = lead+"├ ", lead+rail(!d.last)
			if d.last {
				first = lead + "└ "
			}
		}
		e := d.e
		suffix := ""
		if e.section != top.section {
			for _, sec := range sections {
				if sec.name == e.section {
					suffix = " (" + sec.kind + ")"
				}
			}
		}
		lines = append(lines, t.nameLine(first, e.name, suffix, e, rest)...)
		lines = append(lines, rest+t.fit(e.title, rest), rest+t.fit(e.layer, rest))
		if e.base != nil && (e.base.parent != e || d.depth == maxDepth) {
			lines = append(lines, rest+"on "+e.base.ref)
		}
		lines = append(lines, prefixed(rest, e.includes)...)
		for _, note := range e.notes {
			lines = append(lines, rest+t.fit(note, rest))
		}
	}
	return lines
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

// owes says how many checks a head owes under the policy, by cost, as
// valley checks derives them against the merge base with main. It runs
// the valley command bin/valley names in VALLEY_COMMAND, inside the
// repository the change is in.
func (s *snapshot) owes(head string) string {
	valley := os.Getenv("VALLEY_COMMAND")
	if valley == "" {
		valley = "valley"
	}
	base, err := s.g.output(true, "merge-base", s.tip, head)
	if err != nil {
		return "owes ? (no merge base with main)"
	}
	cmd := exec.Command(valley, "checks", base, head)
	cmd.Dir = s.g.dir
	cmd.Env = s.g.env
	out, err := cmd.Output()
	if err != nil {
		return "owes ? (the policy did not derive)"
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
		return "owes nothing"
	}
	return fmt.Sprintf("owes %d quick, %d slow", quick, slow)
}
