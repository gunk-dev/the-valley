package server

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"the-valley/knot"
	"the-valley/web/export"
)

// index is one reader's view of an export: the projects the authorization
// lets the reader read and their knots, by name, where each of those
// projects' pages are, and the plan page's structure. It is built once for
// each generation, authorization and reader, from what visible returns
// alone, so no page built from it names a project the reader may not read.
type index struct {
	// building is set only while the worker constructs the index.
	building context.Context
	projects map[string]*export.Project
	knots    map[string]map[string]*export.Knot
	// public are the visible projects' names, in the export's order: those
	// that have a web URL, which a project in no group does not.
	public []string
	// bases are each public project's URL, /<group name>/<web name>,
	// escaped, by the project's identifier. groups are the public
	// projects by group name and then by web name. formers are where the
	// URLs under a public project's former names redirect to, its base,
	// by group name and then by former name. aliases are where each
	// alias's URLs redirect to: a group's alias to /<group name>, and a
	// project's short alias to its base.
	bases   map[string]string
	groups  map[string]map[string]*export.Project
	formers map[string]map[string]string
	aliases map[string]string
	// plan is the plan page, but for what is in flight, whose ages are
	// read at each request. Requests share it and do not change it.
	plan *planPage
}

// indexCancelled unwinds the nested graph walks when their worker stops.
type indexCancelled struct{ err error }

func (idx *index) checkBuild() {
	if idx.building != nil {
		if err := idx.building.Err(); err != nil {
			panic(indexCancelled{err})
		}
	}
}

// visible is the one project-view filter: the projects of e that the
// authorization lets reader read, by name, each named with the identity e
// gives it. A nil authorization lets no one read anything. Every view a
// server builds, of the plan, the navigation, the groups, the references
// between projects and the redirects, is built through it.
func visible(e *export.Export, a *export.Authorization, reader string) map[string]bool {
	shown := map[string]bool{}
	for _, p := range e.Projects {
		if a.Allows(reader, p.Name, p.Identity) {
			shown[p.Name] = true
		}
	}
	return shown
}

// newIndex is the index of e's projects that shown names.
func newIndex(ctx context.Context, e *export.Export, shown map[string]bool) (result *index, err error) {
	defer func() {
		if p := recover(); p != nil {
			if stopped, ok := p.(indexCancelled); ok {
				result, err = nil, stopped.err
			} else {
				panic(p)
			}
		}
	}()
	idx := &index{building: ctx, projects: map[string]*export.Project{}, knots: map[string]map[string]*export.Knot{},
		bases: map[string]string{}, groups: map[string]map[string]*export.Project{},
		formers: map[string]map[string]string{}, aliases: map[string]string{}}
	idx.checkBuild()
	defer func() { idx.building = nil }()
	idx.place(e, shown)
	for i := range e.Projects {
		idx.checkBuild()
		p := &e.Projects[i]
		if !shown[p.Name] {
			continue
		}
		idx.projects[p.Name] = p
		byID := map[string]*export.Knot{}
		for j := range p.Knots {
			idx.checkBuild()
			byID[p.Knots[j].ID] = &p.Knots[j]
		}
		idx.knots[p.Name] = byID
	}
	idx.plan = idx.planPage()
	idx.checkBuild()
	return idx, nil
}

// place indexes where each project shown names has its pages, and where
// each alias and former name redirects to. A project in no group has no
// pages, and a group with no project shown has no alias. It checks for
// cancellation before each group, project and name, so the work between
// two checks is bounded by a name's length.
func (idx *index) place(e *export.Export, shown map[string]bool) {
	groups := map[string]export.Group{}
	for _, g := range e.Groups {
		idx.checkBuild()
		groups[g.ID] = g
	}
	used := map[string]bool{}
	for i := range e.Projects {
		idx.checkBuild()
		p := &e.Projects[i]
		g, grouped := groups[p.Group]
		if !shown[p.Name] || !grouped {
			continue
		}
		used[g.ID] = true
		idx.public = append(idx.public, p.Name)
		base := "/" + url.PathEscape(g.Name) + "/" + url.PathEscape(p.WebName)
		idx.bases[p.Name] = base
		if idx.groups[g.Name] == nil {
			idx.groups[g.Name] = map[string]*export.Project{}
			idx.formers[g.Name] = map[string]string{}
		}
		idx.groups[g.Name][p.WebName] = p
		for _, former := range p.FormerNames {
			idx.checkBuild()
			idx.formers[g.Name][former] = base
		}
		for _, alias := range p.Aliases {
			idx.checkBuild()
			idx.aliases[alias] = base
		}
	}
	for _, g := range e.Groups {
		idx.checkBuild()
		if !used[g.ID] {
			continue
		}
		for _, alias := range g.Aliases {
			idx.checkBuild()
			idx.aliases[alias] = "/" + url.PathEscape(g.Name)
		}
	}
}

// knotURL is a knot's page, under its project's base.
func knotURL(base, id string) string {
	return base + "/knots/" + url.PathEscape(id)
}

// knotLink is a knot as a page links to it.
type knotLink struct {
	Project string
	ID      string
	Title   string
	State   string
	URL     string
}

func (idx *index) linkOf(project string, k *export.Knot) knotLink {
	return knotLink{Project: project, ID: k.ID, Title: k.Title, State: k.State, URL: knotURL(idx.bases[project], k.ID)}
}

// The plan page, in its reading order: what waits on the reader, what is
// in flight, the paths, and the milestones.
type planPage struct {
	Projects   string
	Tally      []tally
	InFlight   []flight
	Changes    []readyChange
	Paths      []path
	Milestones []milestone
}

// tally is how many knots are in one state.
type tally struct {
	N     int
	Label string
}

// flight is a knot in flight, with who claims it and since when.
type flight struct {
	Knot knotLink
	Note string
}

// readyChange is a change ready for approval.
type readyChange struct {
	Handle  string
	Title   string
	Version string
	Note    string
	Knots   []knotLink
}

// path is one parent's children in the order their blocked_by edges set:
// each stage holds the children whose blockers among their siblings are
// all in earlier stages.
type path struct {
	Parent knotLink
	Stages [][]knotLink
}

// milestone is an outcome with no parent and the tree of knots under it,
// or a project's knots that have no parent and are no milestone.
type milestone struct {
	Knot    *knotLink
	Title   string
	Project string
	Count   string
	Cord    []string
	Tree    []node
}

// node is one knot of a milestone's tree, with the knots under it.
type node struct {
	Row      row
	Children []node
	Open     bool
}

// row is how a knot reads in a tree: its glyph, ID and title, and chips
// saying what more there is to know.
type row struct {
	knotLink
	Filter string
	Chips  []chip
}

type chip struct {
	Class string
	Text  string
}

// stateRank orders a tree's children: work in progress first, finished
// work last.
var stateRank = map[string]int{"in-progress": 0, "ready": 1, "blocked": 2, "active": 3, "done": 4, "abandoned": 5}

func rank(state string) int {
	if r, ok := stateRank[state]; ok {
		return r
	}
	return len(stateRank)
}

// plan serves the plan page: the generation's structure, and what is in
// flight as of the request.
func (q *request) plan() {
	p := *q.idx.plan
	for _, name := range q.idx.public {
		p.InFlight = append(p.InFlight, q.inFlight(q.idx.projects[name])...)
	}
	q.page(http.StatusOK, "plan", "Plan", p)
}

// planPage is the plan page's structure, without what is in flight.
func (idx *index) planPage() *planPage {
	var p planPage
	p.Projects = strings.Join(idx.public, " · ")
	counts := map[string]int{}
	for _, name := range idx.public {
		idx.checkBuild()
		proj := idx.projects[name]
		roots := idx.buildSet(proj.Roots)
		for i := range proj.Knots {
			idx.checkBuild()
			k := &proj.Knots[i]
			if (k.Type == "outcome" || k.Type == "bug") && !roots[k.ID] {
				counts[k.State]++
			}
		}
		p.Changes = append(p.Changes, idx.readyChanges(proj)...)
		p.Paths = append(p.Paths, idx.paths(proj)...)
		p.Milestones = append(p.Milestones, idx.milestones(proj)...)
	}
	for _, s := range []string{"done", "in-progress", "ready", "blocked"} {
		idx.checkBuild()
		p.Tally = append(p.Tally, tally{counts[s], stateLabel(s)})
	}
	return &p
}

// inFlight is the project's knots in flight, in the export's order: the
// claimed ones, oldest claim first, then those in progress that nobody
// claims. A claim's age is read from when it was made.
func (q *request) inFlight(p *export.Project) []flight {
	var out []flight
	for _, id := range p.InFlight {
		k, ok := q.idx.knots[p.Name][id]
		if !ok {
			continue
		}
		f := flight{Knot: q.idx.linkOf(p.Name, k), Note: "in progress, unclaimed"}
		if c := k.Claim; c != nil {
			f.Note = "claimed by " + c.By
			if at, err := parseTime(c.At); err == nil {
				age := q.now.Sub(at)
				f.Note += " " + ago(age)
				if age > knot.StaleClaim {
					f.Note += "; the claim is stale"
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// readyChanges are the project's changes ready for approval, each with
// its active version, whether an integration request approves it, and the
// integrator's last decision.
func (idx *index) readyChanges(p *export.Project) []readyChange {
	var out []readyChange
	for _, c := range p.Changes {
		idx.checkBuild()
		v := c.Version
		notes := []string{"not approved"}
		if v.Request != nil {
			notes = []string{"approved"}
		}
		if v.Outcome != nil {
			notes = append(notes, "integrator: "+v.Outcome.Decision)
		}
		if v.Landing == "uncertain" {
			notes = append(notes, "landing uncertain")
		}
		rc := readyChange{Handle: c.Handle, Title: v.Title, Version: p.Name + " v" + strconv.Itoa(v.N),
			Note: strings.Join(notes, " · ")}
		for _, ref := range c.Knots {
			idx.checkBuild()
			if k, ok := idx.knots[p.Name][ref.ID]; ok {
				rc.Knots = append(rc.Knots, idx.linkOf(p.Name, k))
			}
		}
		out = append(out, rc)
	}
	return out
}

// paths are the project's parents whose children wait on one another,
// each with its children in stages. A child's stage is one past the
// latest stage of the siblings in its blocked_by, done or not, so
// children with no order between them share a stage and can run in
// parallel. Abandoned children are left out.
func (idx *index) paths(p *export.Project) []path {
	knots := idx.knots[p.Name]
	var out []path
	for i := range p.Knots {
		idx.checkBuild()
		parent := &p.Knots[i]
		var kids []*export.Knot
		siblings := map[string]bool{}
		for _, id := range parent.Children {
			idx.checkBuild()
			if k, ok := knots[id]; ok && k.State != "abandoned" && !siblings[id] {
				kids = append(kids, k)
				siblings[id] = true
			}
		}
		// waits counts each child's sibling blockers not yet placed, and
		// next lists the siblings each child blocks.
		waits, next := map[string]int{}, map[string][]string{}
		for _, k := range kids {
			idx.checkBuild()
			for _, b := range k.BlockedBy {
				idx.checkBuild()
				if siblings[b] {
					waits[k.ID]++
					next[b] = append(next[b], k.ID)
				}
			}
		}
		if len(next) == 0 {
			continue
		}
		// A child is placed once its sibling blockers all are, each child
		// and each edge once.
		stage, placed := map[string]int{}, map[string]bool{}
		var ready []string
		for _, k := range kids {
			idx.checkBuild()
			if waits[k.ID] == 0 {
				ready, placed[k.ID] = append(ready, k.ID), true
			}
		}
		for len(ready) > 0 {
			idx.checkBuild()
			id := ready[0]
			ready = ready[1:]
			for _, c := range next[id] {
				idx.checkBuild()
				stage[c] = max(stage[c], stage[id]+1)
				if waits[c]--; waits[c] == 0 {
					ready, placed[c] = append(ready, c), true
				}
			}
		}
		// The graph holds blocked_by acyclic. A cycle it did not would
		// leave its knots, and those waiting on them, unplaced, and they
		// share a last stage.
		last := 0
		for id := range placed {
			idx.checkBuild()
			last = max(last, stage[id]+1)
		}
		pa := path{Parent: idx.linkOf(p.Name, parent)}
		for _, k := range kids {
			idx.checkBuild()
			s := stage[k.ID]
			if !placed[k.ID] {
				s = last
			}
			for len(pa.Stages) <= s {
				idx.checkBuild()
				pa.Stages = append(pa.Stages, nil)
			}
			pa.Stages[s] = append(pa.Stages[s], idx.linkOf(p.Name, k))
		}
		out = append(out, pa)
	}
	return out
}

// milestones are the project's roots, each with its tree, and then its
// knots that have no parent and are no root, as one more box. A knot is
// shown once in the project's trees, and counted once, however the
// graph's children lists name it.
func (idx *index) milestones(p *export.Project) []milestone {
	knots := idx.knots[p.Name]
	var out []milestone
	counted, shown := map[string]bool{}, map[string]bool{}
	for _, id := range p.Roots {
		idx.checkBuild()
		k, ok := knots[id]
		if !ok || shown[id] {
			continue
		}
		counted[id], shown[id] = true, true
		done, of := 0, 0
		var cord []string
		for _, d := range idx.descendants(p.Name, k, counted) {
			idx.checkBuild()
			if d.State == "abandoned" {
				continue
			}
			of++
			if d.State == "done" {
				done++
			}
			cord = append(cord, d.State)
		}
		// The cord reads finished work first, then the rest by state.
		sort.SliceStable(cord, func(i, j int) bool {
			idx.checkBuild()
			di, dj := cord[i] == "done", cord[j] == "done"
			if di != dj {
				return di
			}
			return rank(cord[i]) < rank(cord[j])
		})
		link := idx.linkOf(p.Name, k)
		out = append(out, milestone{Knot: &link, Title: k.Title, Project: p.Name,
			Count: strconv.Itoa(done) + " of " + strconv.Itoa(of) + " done", Cord: cord,
			Tree: idx.tree(p.Name, k.Children, 1, shown)})
	}
	roots := idx.buildSet(p.Roots)
	var loose []string
	for _, k := range p.Knots {
		idx.checkBuild()
		if k.Parent == "" && !roots[k.ID] {
			loose = append(loose, k.ID)
		}
	}
	if len(loose) > 0 {
		out = append(out, milestone{Title: "Unfiled", Project: p.Name, Count: count(len(loose), "knot"),
			Tree: idx.tree(p.Name, loose, 1, shown)})
	}
	return out
}

// descendants is every knot under k that counted does not hold, each
// added to counted as it is found. It walks the graph with a stack, so a
// chain of any depth takes no recursion, and counted guards against a
// parent cycle the graph would not hold.
func (idx *index) descendants(project string, k *export.Knot, counted map[string]bool) []*export.Knot {
	var out []*export.Knot
	stack := []*export.Knot{k}
	for len(stack) > 0 {
		idx.checkBuild()
		k := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, id := range k.Children {
			idx.checkBuild()
			if c, ok := idx.knots[project][id]; ok && !counted[id] {
				counted[id] = true
				out = append(out, c)
				stack = append(stack, c)
			}
		}
	}
	return out
}

// tree is the knots called ids and everything under them, each level
// ordered by state, at depth depth of a milestone's tree. shown holds the
// knots the project's trees show already, which are not shown again, so a
// tree takes one step for each knot and each child it lists. A knot at
// MaxPlanDepth is shown without the knots under it, and says they are on
// its page.
func (idx *index) tree(project string, ids []string, depth int, shown map[string]bool) []node {
	var kids []*export.Knot
	for _, id := range ids {
		idx.checkBuild()
		if k, ok := idx.knots[project][id]; ok && !shown[id] {
			shown[id] = true
			kids = append(kids, k)
		}
	}
	sort.SliceStable(kids, func(i, j int) bool { idx.checkBuild(); return rank(kids[i].State) < rank(kids[j].State) })
	var out []node
	for _, k := range kids {
		idx.checkBuild()
		n := node{Row: idx.row(project, k), Open: k.State != "done" && k.State != "abandoned"}
		if depth < MaxPlanDepth {
			n.Children = idx.tree(project, k.Children, depth+1, shown)
		} else if len(k.Children) > 0 {
			n.Row.Chips = append(n.Row.Chips, chip{"quiet", "more on its page"})
		}
		out = append(out, n)
	}
	return out
}

// row is a knot's line in a tree. Filter is the state the page's filters
// read: abandoned work is finished work, as done is.
func (idx *index) row(project string, k *export.Knot) row {
	r := row{knotLink: idx.linkOf(project, k), Filter: k.State}
	if k.State == "abandoned" {
		r.Filter = "done"
	}
	if k.Target != "" && k.Target != project {
		text, _ := idx.named(project, k.Target)
		r.Chips = append(r.Chips, chip{"quiet", "lands in " + text})
	}
	if k.Claim != nil {
		r.Chips = append(r.Chips, chip{"", "claimed by " + k.Claim.By})
	}
	if k.State == "blocked" {
		r.Chips = append(r.Chips, chip{"blocked", "waits on " + idx.waitsOn(k.WaitingOn, k.WaitingOnHidden)})
	}
	if k.Progress != nil && k.Progress.Of > 0 {
		r.Chips = append(r.Chips, chip{"quiet", strconv.Itoa(k.Progress.Done) + "/" + strconv.Itoa(k.Progress.Of)})
	}
	if k.CommentCount > 0 {
		r.Chips = append(r.Chips, chip{"quiet", count(k.CommentCount, "comment")})
	}
	return r
}

func set(list []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range list {
		m[s] = true
	}
	return m
}

func (idx *index) buildSet(list []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range list {
		idx.checkBuild()
		m[s] = true
	}
	return m
}
