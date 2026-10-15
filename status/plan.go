// valley plan: the plan graph as a view, for the operator to read where
// things stand without asking anyone.
//
// It reads a project's graph at refs/the-valley/knots as the knot verbs
// do (knot.go): one ls-remote, one fetch and one batched read. Then it
// prints the graph as a tree. The roots are the outcomes with no parent,
// the milestones. Under each come the knots whose parent it is, indented,
// and theirs under them, at every depth. Within a level, knots are in the
// order they were created, then by title. Every knot shows a glyph for its
// state, its ID and its title. An active knot also shows its claim, with
// the claim's age, its owner, the blockers that still hold it or that
// ended without finishing, and where its work lands. A root shows its
// progress at the right margin: its descendants that are terminal and not
// abandoned, out of its descendants that are not abandoned, as a count and
// a bar. A root whose descendants are all terminal is one line. Active
// knots with no parent that are not outcomes are listed after the roots,
// under "no milestone".
//
//	◐ oc-1d4a2b7 Slice two lands                                    2/7 ██░░░░░░░░
//	  ✓ bd-2b3c4d5 The hook reads too much
//	  ◐ oc-3f4e5d6 Draw the tree · claimed by run-7 2h ago · owner klaus-env
//	    ⊘ oc-8a7b6c5 Golden files · blocked by oc-3f4e5d6
//
// Three sections follow the tree. "waiting on you" lists the active
// outcomes and bugs, and the proposed decisions, that the operator owns,
// then the changes ready for approval, as valley pending selects them
// (pending.go). The operator is the principal git config valley.principal
// names, or the one --owner names. "in flight" lists the claimed knots,
// the oldest claim first. "open residuals" lists the open bugs, grouped
// under their parent.
//
// Without a flag it shows the current repository's project, and --project
// shows another on the same host. --all shows every project the host
// publishes, as valley pending --all reads them (list.go), each under its
// name. A project with no graph is named on one line at the end, and one
// that cannot be read is reported in its own section.
//
// Lines fit the terminal as pending's do (terminal.go): a title is cut
// short with …, and an ID, a run or a principal never is. On a terminal,
// each glyph has a colour and each root's title is bold, unless NO_COLOR
// is set.
//
// With --json it prints valley-plan/v1, which docs/architecture.md
// documents: every knot with what the graph derives of it, and the three
// sections.

package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"the-valley/knot"
)

const (
	planUsage  = "usage: valley plan [--project P | --all] [--owner P] [--json]"
	planSchema = "valley-plan/v1"
	// planStart is the command that starts a graph.
	planStart = `valley knot new outcome "<title>"`
)

// planArgs are plan's arguments.
type planArgs struct {
	project, owner string
	all, json      bool
}

func parsePlanArgs(args []string) (planArgs, error) {
	var a planArgs
	for i := 0; i < len(args); i++ {
		flag, value, inline := strings.Cut(args[i], "=")
		switch flag {
		case "--json", "--all":
			if inline {
				return a, failure(flag + " takes no value")
			}
			if flag == "--json" {
				a.json = true
			} else {
				a.all = true
			}
		case "--project", "--owner":
			if !inline {
				if i+1 >= len(args) {
					return a, failure(flag + " needs a value")
				}
				i++
				value = args[i]
			}
			if !knot.IsName(value) {
				return a, failure(fmt.Sprintf("%s takes a name, and %q is not one", flag, value))
			}
			if flag == "--project" {
				a.project = value
			} else {
				a.owner = value
			}
		default:
			return a, failure(fmt.Sprintf("plan does not take %s\n%s", args[i], planUsage))
		}
	}
	if a.all && a.project != "" {
		return a, failure("plan takes --project or --all, not both")
	}
	return a, nil
}

// planProject is one project as plan reads it: where its graph is, the
// graph as read, and its changes ready for approval.
type planProject struct {
	remote
	url        string
	state      knotState
	err        error // why the graph could not be read
	view       *planView
	changes    []planChange // nil when they were not read
	changesErr error
}

// planChange is a change ready for approval, as valley pending selects
// them: ready, with an active version that has not landed.
type planChange struct {
	Repo     string `json:"repo"`
	Handle   string `json:"handle"`
	ChangeID string `json:"change_id"`
	Version  int    `json:"version"`
	Title    string `json:"title"`
	Head     string `json:"head"`
}

func runPlan(g git, args []string, stdout, stderr io.Writer) error {
	a, err := parsePlanArgs(args)
	if err != nil {
		return err
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	now := clock()
	operator := a.owner
	if operator == "" {
		if p, err := g.output(true, "config", "valley.principal"); err == nil && knot.IsName(p) {
			operator = p
		}
	}

	var projects []*planProject
	if a.all {
		list, _, err := sources(g, listing{all: true}, stderr)
		if err != nil {
			return err
		}
		for _, s := range list {
			if s.g.dir != "" {
				// Another project's git speaks only through its error,
				// which its section reports.
				s.g.stderr = io.Discard
			}
			projects = append(projects, &planProject{remote: remote{g: s.g, name: s.name}, url: s.url, err: s.err})
		}
	} else {
		r, err := knotRemote(g, a.project)
		if err != nil {
			return err
		}
		url, _ := r.g.output(true, "remote", "get-url", "origin")
		projects = []*planProject{{remote: r, url: url}}
	}

	// Each graph is read once, and a reference to another project's knot
	// is resolved against the graphs read here before any other is read.
	quiet := g
	quiet.stderr = io.Discard
	foreign := &foreignGraphs{g: quiet, graphs: map[string]*knot.Graph{}, failed: map[string]error{}}
	for _, p := range projects {
		if p.err == nil {
			p.state, p.err = p.read()
		}
		if p.err != nil && !a.all {
			return p.err
		}
		if p.err == nil {
			foreign.graphs[p.name] = p.state.graph
		}
	}

	var sourced []source
	for _, p := range projects {
		if p.err == nil && p.state.tip != "" {
			p.view = newPlanView(p.remote, p.state, now, foreign.resolve)
			sourced = append(sourced, source{name: p.name, url: p.url, g: p.g})
		}
	}
	repos := readSources(sourced, verifierFromEnv())
	h := handles(repos)
	for _, l := range repos {
		for _, p := range projects {
			if p.name == l.name {
				p.changes, p.changesErr = readyForApproval(l, h)
			}
		}
	}
	warnUnverified(repos, stderr)

	if a.json {
		return writeKnotJSON(stdout, planDocument(projects, now, operator))
	}
	t := terminalOf(stdout)
	_, err = io.WriteString(stdout, t.planText(projects, operator, a.all || a.project != "", a.project))
	return err
}

// readyForApproval are a repository's changes that valley pending lists
// as ready: ready, with an active version that has not landed.
func readyForApproval(l listed, h handleMap) ([]planChange, error) {
	view := l.repoView(h, pendingChange, unlanded)
	if view.Error != nil {
		return nil, failure(*view.Error)
	}
	list := []planChange{}
	for _, c := range view.Changes {
		v := c.Versions[c.Active-1]
		list = append(list, planChange{Repo: l.name, Handle: c.Handle, ChangeID: c.ChangeID, Version: v.N,
			Title: v.Title, Head: v.Head})
	}
	return list, nil
}

// foreignGraphs resolves references to other projects' knots, reading
// each project's graph at most once.
type foreignGraphs struct {
	g      git
	graphs map[string]*knot.Graph
	failed map[string]error
}

func (f *foreignGraphs) resolve(ref string) (*knot.Knot, bool, error) {
	project, id, _ := strings.Cut(ref, ":")
	if err := f.failed[project]; err != nil {
		return nil, false, err
	}
	graph := f.graphs[project]
	if graph == nil {
		r, err := knotRemote(f.g, project)
		var s knotState
		if err == nil {
			s, err = r.read()
		}
		if err != nil {
			f.failed[project] = err
			return nil, false, err
		}
		graph = s.graph
		f.graphs[project] = graph
	}
	k, ok := graph.Knots[id]
	return k, ok, nil
}

// planEndings are the terminal statuses that end a knot without finishing
// it, as the knot module's view reads them (knot/derive.go).
var planEndings = []string{"abandoned", "discarded", "superseded"}

// planView is one project's graph as plan shows it.
type planView struct {
	graph    *knot.Graph
	knots    map[string]*planKnot
	children map[string][]string // each knot's children, in tree order
	order    []string            // every knot, in the order the tree lists them
	rank     map[string]int      // each knot's place in order
	roots    []string            // the outcomes with no parent
	loose    []string            // the active knots with no parent that are no outcome
}

// planKnot is one knot as valley-plan/v1 writes it: as valley knot list
// writes it, with its children in tree order and its progress counted
// over every descendant, and with its state and its edges to other
// projects' knots.
type planKnot struct {
	*jsonKnot
	State string     `json:"state"`
	Edges []planEdge `json:"edges"`
}

// planEdge is a reference to another project's knot, from blocked_by or
// target. It is resolved when that project's graph holds the knot, and
// dangling when it does not (why: missing) or could not be read (why:
// unreadable).
type planEdge struct {
	Field   string  `json:"field"`
	Ref     string  `json:"ref"`
	Project string  `json:"project"`
	ID      string  `json:"id"`
	State   string  `json:"state"`
	Why     *string `json:"why"`
	Status  *string `json:"status"`
	Title   *string `json:"title"`
}

func newPlanView(r remote, s knotState, now time.Time, foreign knot.Foreign) *planView {
	g := s.graph
	v := &planView{graph: g, knots: map[string]*planKnot{}, children: map[string][]string{}, rank: map[string]int{}}
	inOrder := func(ids []string) {
		sort.Slice(ids, func(i, j int) bool { return before(g.Knots[ids[i]], g.Knots[ids[j]]) })
	}
	var unparented []string
	for _, id := range g.IDs() {
		switch k := g.Knots[id]; {
		case k.Parent != "":
			v.children[k.Parent] = append(v.children[k.Parent], id)
		case k.Type == "outcome":
			v.roots = append(v.roots, id)
		default:
			unparented = append(unparented, id)
		}
	}
	for _, list := range v.children {
		inOrder(list)
	}
	inOrder(v.roots)
	inOrder(unparented)
	var walk func(id string)
	walk = func(id string) {
		v.rank[id] = len(v.order)
		v.order = append(v.order, id)
		for _, c := range v.children[id] {
			walk(c)
		}
	}
	for _, id := range v.roots {
		walk(id)
	}
	for _, id := range unparented {
		walk(id)
		if g.Knots[id].Active() {
			v.loose = append(v.loose, id)
		}
	}
	progress := v.progress()
	for _, id := range v.order {
		k := g.Knots[id]
		j := r.jsonKnot(s, k, false, foreign)
		j.Children = append([]string{}, v.children[id]...)
		j.Progress = nil
		if len(v.children[id]) > 0 {
			p := progress[id]
			j.Progress = &p
		}
		v.knots[id] = &planKnot{jsonKnot: j, State: stateOf(k, j.View), Edges: edgesOf(k, foreign)}
	}
	return v
}

// before orders the knots of one level: by the date each was created,
// then by title, then by ID, so two knots of one title keep one order.
func before(a, b *knot.Knot) bool {
	if a.Created != b.Created {
		return a.Created < b.Created
	}
	if a.Title != b.Title {
		return a.Title < b.Title
	}
	return a.ID < b.ID
}

// progress is each knot's progress over every descendant: those that are
// terminal and not abandoned, out of those that are not abandoned. A
// descendant under an abandoned knot counts as any other does.
func (v *planView) progress() map[string]knot.Progress {
	all := map[string]knot.Progress{}
	// order lists a knot before its descendants, so walking it backwards
	// counts every child before its parent.
	for i := len(v.order) - 1; i >= 0; i-- {
		id := v.order[i]
		var p knot.Progress
		for _, c := range v.children[id] {
			if k := v.graph.Knots[c]; k.Status != "abandoned" {
				p.Of++
				if !k.Active() {
					p.Done++
				}
			}
			p.Done += all[c].Done
			p.Of += all[c].Of
		}
		all[id] = p
	}
	return all
}

// stateOf is the state a knot's glyph shows. A terminal knot is done, or
// abandoned when it ended without finishing. An active one is blocked
// while a blocker holds it, in progress while it is claimed or its status
// says so, ready when it is an outcome or a bug the graph reads as ready,
// and otherwise active, as an open idea or a proposed decision is.
func stateOf(k *knot.Knot, view knot.View) string {
	switch {
	case !k.Active() && slices.Contains(planEndings, k.Status):
		return "abandoned"
	case !k.Active():
		return "done"
	case len(view.WaitingOn) > 0:
		return "blocked"
	case k.ClaimedBy != "" || k.Status == "in-progress":
		return "in-progress"
	case view.Ready && (k.Type == "outcome" || k.Type == "bug"):
		return "ready"
	}
	return "active"
}

// edgesOf are a knot's references to other projects' knots, resolved.
func edgesOf(k *knot.Knot, foreign knot.Foreign) []planEdge {
	edges := []planEdge{}
	add := func(field, ref string) {
		project, id, _ := strings.Cut(ref, ":")
		e := planEdge{Field: field, Ref: ref, Project: project, ID: id, State: "resolved"}
		var found *knot.Knot
		ok, err := false, error(failure("no resolver"))
		if foreign != nil {
			found, ok, err = foreign(ref)
		}
		switch {
		case err != nil:
			e.State, e.Why = "dangling", optional("unreadable")
		case !ok:
			e.State, e.Why = "dangling", optional("missing")
		default:
			e.Status, e.Title = optional(found.Status), optional(found.Title)
		}
		edges = append(edges, e)
	}
	for _, ref := range k.BlockedBy {
		if knot.IsQualifiedID(ref) {
			add("blocked_by", ref)
		}
	}
	if knot.IsQualifiedID(k.Target) {
		add("target", k.Target)
	}
	return edges
}

// collapsed reports whether a root's descendants are all terminal, so it
// is shown as one line.
func (v *planView) collapsed(id string) bool {
	for _, c := range v.children[id] {
		if v.graph.Knots[c].Active() || !v.collapsed(c) {
			return false
		}
	}
	return true
}

// waiting are the knots that wait on the operator, in tree order: the
// active outcomes and bugs they own, and the proposed decisions they own.
func (v *planView) waiting(operator string) []string {
	ids := []string{}
	for _, id := range v.order {
		k := v.graph.Knots[id]
		if k.Owner != operator || !k.Active() {
			continue
		}
		if k.Type == "outcome" || k.Type == "bug" || k.Type == "decision" && k.Status == "proposed" {
			ids = append(ids, id)
		}
	}
	return ids
}

// inFlight are the active knots that are claimed, the oldest claim first.
// claimed_at is a UTC time of one fixed form, so its text sorts as its
// time does.
func (v *planView) inFlight() []string {
	ids := []string{}
	for _, id := range v.order {
		if k := v.graph.Knots[id]; k.ClaimedBy != "" && k.Active() {
			ids = append(ids, id)
		}
	}
	sort.SliceStable(ids, func(i, j int) bool {
		return v.graph.Knots[ids[i]].ClaimedAt < v.graph.Knots[ids[j]].ClaimedAt
	})
	return ids
}

// residualGroup is the open bugs under one parent, or under none.
type residualGroup struct {
	parent string
	bugs   []string
}

// residuals are the open bugs: first those with no parent, then those
// with one, grouped under it, the groups in tree order.
func (v *planView) residuals() []residualGroup {
	loose := residualGroup{}
	var groups []residualGroup
	at := map[string]int{}
	for _, id := range v.order {
		k := v.graph.Knots[id]
		switch {
		case k.Type != "bug" || !k.Active():
		case k.Parent == "":
			loose.bugs = append(loose.bugs, id)
		default:
			i, ok := at[k.Parent]
			if !ok {
				i = len(groups)
				at[k.Parent] = i
				groups = append(groups, residualGroup{parent: k.Parent})
			}
			groups[i].bugs = append(groups[i].bugs, id)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return v.rank[groups[i].parent] < v.rank[groups[j].parent] })
	if len(loose.bugs) > 0 {
		groups = append([]residualGroup{loose}, groups...)
	}
	return groups
}

// notes are what a line says of an active knot after its title: its
// claim, its owner, its blockers, and where its work lands. A blocker is
// listed while it holds the knot, and when it ended without finishing,
// marked with how it ended. A reference to another project's knot that
// cannot be read is marked dangling.
func (v *planView) notes(id string) []string {
	k, pk := v.graph.Knots[id], v.knots[id]
	if !k.Active() {
		return nil
	}
	dangling := map[string]bool{}
	for _, e := range pk.Edges {
		if e.State == "dangling" {
			dangling[e.Ref] = true
		}
	}
	var notes []string
	if c := pk.Claim; c != nil {
		note := fmt.Sprintf("claimed by %s %s ago", visible(c.By), age(c.AgeSeconds))
		if c.Stale {
			note += ", stale"
		}
		notes = append(notes, note)
	}
	if k.Owner != "" {
		notes = append(notes, "owner "+visible(k.Owner))
	}
	var blockers []string
	for _, ref := range k.BlockedBy {
		switch {
		case dangling[ref]:
			blockers = append(blockers, ref+" (dangling)")
		case slices.Contains(pk.ClearedByEnding, ref):
			blockers = append(blockers, ref+" ("+v.statusOf(ref, pk.Edges)+")")
		case slices.Contains(pk.WaitingOn, ref):
			blockers = append(blockers, ref)
		}
	}
	if len(blockers) > 0 {
		notes = append(notes, "blocked by "+strings.Join(blockers, ", "))
	}
	if k.Target != "" {
		note := "lands in " + visible(k.Target)
		if dangling[k.Target] {
			note += " (dangling)"
		}
		notes = append(notes, note)
	}
	return notes
}

// statusOf is the status of the knot a blocker names, in this graph or,
// through the knot's edges, in another.
func (v *planView) statusOf(ref string, edges []planEdge) string {
	if k, ok := v.graph.Knots[ref]; ok {
		return k.Status
	}
	for _, e := range edges {
		if e.Ref == ref && e.Status != nil {
			return *e.Status
		}
	}
	return "ended"
}

// planGlyphs are the glyph of each state, in the order the legend lists
// them, with the SGR code of the colour a terminal shows it in.
var planGlyphs = []struct{ state, glyph, word, colour string }{
	{"ready", "○", "ready", "32"},
	{"in-progress", "◐", "in progress", "33"},
	{"blocked", "⊘", "blocked", "31"},
	{"active", "◇", "active", "36"},
	{"done", "✓", "done", "2"},
	{"abandoned", "✗", "abandoned", "2"},
}

func (t terminal) glyph(state string) (plain, shown string) {
	for _, g := range planGlyphs {
		if g.state == state {
			return g.glyph, t.tint(g.colour, g.glyph)
		}
	}
	return "?", "?"
}

// barWidth is how many cells a root's progress bar has.
const barWidth = 10

// bar is a root's progress as a count and a bar of barWidth cells, a cell
// filled for each whole tenth done.
func bar(p knot.Progress) string {
	filled := 0
	if p.Of > 0 {
		filled = p.Done * barWidth / p.Of
	}
	return fmt.Sprintf("%d/%d %s%s", p.Done, p.Of, strings.Repeat("█", filled), strings.Repeat("░", barWidth-filled))
}

// minTitle is the fewest columns a title is cut to before a knot's notes
// move to a line of their own.
const minTitle = 24

// knotLine is a knot's line: indent, its glyph, its ID, its title, and its
// notes, with right, when given, at the right margin. The title is cut
// short so every note fits after it. When that would leave the title
// fewer than minTitle columns, it keeps the room it needs, and the notes
// run on after it and over lines of their own under the title, as many to
// a line as fit. A note breaks only after a comma in its list, so an ID,
// a run or a principal is never cut.
func (t terminal) knotLine(v *planView, id, indent, right string, strong bool) []string {
	k := v.graph.Knots[id]
	plain, shown := t.glyph(v.knots[id].State)
	name := fmt.Sprintf("%-11s ", id)
	lead := indent + plain + " " + name
	notes := v.notes(id)
	margin := 0
	if right != "" {
		margin = columns(right) + 2
	}
	room := t.width - columns(lead) - margin
	title := visible(k.Title)
	tail := ""
	if len(notes) > 0 {
		tail = " · " + strings.Join(notes, " · ")
	}
	var rest []string
	if columns(title+tail) <= room || room-columns(tail) >= minTitle {
		title = fit(title, room-columns(tail))
	} else {
		title = fit(title, room)
		// Each note is a run of pieces, broken after each comma. A piece
		// that opens a note follows another after " · ", and one that
		// continues a note follows it directly.
		type piece struct {
			text  string
			opens bool
		}
		var pieces []piece
		for _, n := range notes {
			for i, p := range strings.SplitAfter(n, ", ") {
				pieces = append(pieces, piece{p, i == 0})
			}
		}
		join := func(line string, p piece) string {
			if line == "" || !p.opens {
				return line + p.text
			}
			return line + " · " + p.text
		}
		fill := func(line, lead string, width int) string {
			for len(pieces) > 0 && columns(lead+strings.TrimRight(join(line, pieces[0]), " ")) <= width {
				line, pieces = join(line, pieces[0]), pieces[1:]
			}
			return strings.TrimRight(line, " ")
		}
		tail = strings.TrimPrefix(fill(title, "", room), title)
		under := strings.Repeat(" ", columns(lead))
		for len(pieces) > 0 {
			first := pieces[0].text
			pieces = pieces[1:]
			rest = append(rest, under+fill(first, under, t.width))
		}
	}
	styled := title
	if strong {
		styled = t.strong(title)
	}
	line := indent + shown + " " + name + styled + tail
	if right != "" {
		gap := max(t.width-columns(lead+title+tail)-columns(right), 2)
		line += strings.Repeat(" ", gap) + right
	}
	return append([]string{line}, rest...)
}

// subtree is the lines of every knot under id, each level indented two
// columns more.
func (t terminal) subtree(v *planView, id, indent string) []string {
	var lines []string
	for _, c := range v.children[id] {
		lines = append(lines, t.knotLine(v, c, indent, "", false)...)
		lines = append(lines, t.subtree(v, c, indent+"  ")...)
	}
	return lines
}

// planText is the whole of plan's text: each project's section, the
// projects with no graph, and the legend.
func (t terminal) planText(projects []*planProject, operator string, headed bool, named string) string {
	var b strings.Builder
	var blank []string // the projects with no graph
	shown := false
	for _, p := range projects {
		if p.err == nil && p.state.tip == "" {
			blank = append(blank, p.name)
			continue
		}
		if shown {
			b.WriteString("\n")
		}
		shown = true
		if headed {
			fmt.Fprintf(&b, "%s\n%s\n", t.strong(p.name), strings.Repeat("─", min(columns(p.name), t.width)))
		}
		if p.err != nil {
			fmt.Fprintf(&b, "its plan graph could not be read: %v\n", p.err)
			continue
		}
		for _, line := range t.projectLines(p, operator) {
			b.WriteString(line + "\n")
		}
	}
	switch {
	case !headed && len(blank) > 0:
		fmt.Fprintf(&b, "%s has no plan yet. Start one with: %s\n", blank[0], planStart)
	case named != "" && len(blank) > 0:
		fmt.Fprintf(&b, "%s has no plan yet. Start one with: %s --project %s\n", blank[0], planStart, blank[0])
	case len(blank) > 0:
		if shown {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "no plan yet: %s. Start one with: %s --project <name>\n", strings.Join(blank, ", "), planStart)
	}
	if shown {
		var legend []string
		for _, g := range planGlyphs {
			legend = append(legend, t.tint(g.colour, g.glyph)+" "+g.word)
		}
		fmt.Fprintf(&b, "\n%s\n", strings.Join(legend, "  "))
	}
	return b.String()
}

// projectLines are one project's tree and its three sections.
func (t terminal) projectLines(p *planProject, operator string) []string {
	v := p.view
	var lines []string
	gap := func() {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
	}
	for _, id := range v.roots {
		gap()
		right := ""
		if pk := v.knots[id]; pk.Progress != nil {
			right = bar(*pk.Progress)
		} else {
			right = bar(knot.Progress{})
		}
		lines = append(lines, t.knotLine(v, id, "", right, true)...)
		if !v.collapsed(id) {
			lines = append(lines, t.subtree(v, id, "  ")...)
		}
	}
	if len(v.loose) > 0 {
		gap()
		lines = append(lines, "no milestone")
		for _, id := range v.loose {
			lines = append(lines, t.knotLine(v, id, "  ", "", false)...)
		}
	}

	gap()
	if operator == "" {
		lines = append(lines, "waiting on you",
			"  set git config valley.principal <name>, or pass --owner <name>, to see the knots you own")
	} else {
		lines = append(lines, "waiting on you ("+operator+")")
		waiting := v.waiting(operator)
		for _, id := range waiting {
			lines = append(lines, t.knotLine(v, id, "  ", "", false)...)
		}
		if len(waiting) == 0 {
			lines = append(lines, "  no open knot is yours")
		}
	}
	lines = append(lines, "  ready for approval")
	switch {
	case p.changesErr != nil:
		lines = append(lines, "    its changes could not be read: "+p.changesErr.Error())
	case len(p.changes) == 0:
		lines = append(lines, "    no change is ready for approval")
	}
	for _, c := range p.changes {
		lead := fmt.Sprintf("    %s v%d  %s  ", c.Handle, c.Version, c.Repo)
		styled := fmt.Sprintf("    %s  %s  ", t.strong(fmt.Sprintf("%s v%d", c.Handle, c.Version)), c.Repo)
		lines = append(lines, styled+t.fit(readable(c.Title), lead))
	}

	lines = append(lines, "", "in flight")
	flying := v.inFlight()
	for _, id := range flying {
		lines = append(lines, t.knotLine(v, id, "  ", "", false)...)
	}
	if len(flying) == 0 {
		lines = append(lines, "  nothing is claimed")
	}

	lines = append(lines, "", "open residuals")
	groups := v.residuals()
	for _, group := range groups {
		indent := "  "
		if group.parent != "" {
			lead := fmt.Sprintf("  under %-11s ", group.parent)
			lines = append(lines, lead+t.fit(visible(v.graph.Knots[group.parent].Title), lead))
			indent = "    "
		}
		for _, id := range group.bugs {
			lines = append(lines, t.knotLine(v, id, indent, "", false)...)
		}
	}
	if len(groups) == 0 {
		lines = append(lines, "  no bug is open")
	}
	return lines
}

// The valley-plan/v1 document. docs/architecture.md documents it, and a
// change to any field's name or meaning is a new schema version.
type jsonPlan struct {
	Schema   string            `json:"schema"`
	Now      string            `json:"now"`
	Operator *string           `json:"operator"`
	Projects []jsonPlanProject `json:"projects"`
}

type jsonPlanProject struct {
	Project       string          `json:"project"`
	Error         *string         `json:"error"`
	Tip           string          `json:"tip"`
	Roots         []string        `json:"roots"`
	Knots         []*planKnot     `json:"knots"`
	WaitingOnYou  *jsonWaiting    `json:"waiting_on_you"`
	InFlight      []string        `json:"in_flight"`
	OpenResiduals []jsonResiduals `json:"open_residuals"`
}

type jsonWaiting struct {
	Knots        []string     `json:"knots"`
	Changes      []planChange `json:"changes"`
	ChangesError *string      `json:"changes_error"`
}

type jsonResiduals struct {
	Parent *string  `json:"parent"`
	Bugs   []string `json:"bugs"`
}

func planDocument(projects []*planProject, now time.Time, operator string) jsonPlan {
	doc := jsonPlan{Schema: planSchema, Now: now.Format(time.RFC3339), Operator: optional(operator),
		Projects: []jsonPlanProject{}}
	for _, p := range projects {
		j := jsonPlanProject{Project: p.name, Tip: p.state.tip, Roots: []string{}, Knots: []*planKnot{}}
		if p.err != nil {
			msg := p.err.Error()
			j.Error = &msg
		}
		if v := p.view; v != nil {
			j.Roots = append(j.Roots, v.roots...)
			for _, id := range v.order {
				j.Knots = append(j.Knots, v.knots[id])
			}
			w := &jsonWaiting{Changes: p.changes}
			if operator != "" {
				w.Knots = v.waiting(operator)
			}
			if p.changesErr != nil {
				msg := p.changesErr.Error()
				w.ChangesError = &msg
			}
			j.WaitingOnYou = w
			j.InFlight = v.inFlight()
			j.OpenResiduals = []jsonResiduals{}
			for _, g := range v.residuals() {
				j.OpenResiduals = append(j.OpenResiduals, jsonResiduals{Parent: optional(g.parent), Bugs: g.bugs})
			}
		}
		doc.Projects = append(doc.Projects, j)
	}
	return doc
}
