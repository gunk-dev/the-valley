package server

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"the-valley/knot"
	"the-valley/webui/export"
)

// index is an export's projects and knots by name, built once per
// generation.
type index struct {
	projects map[string]*export.Project
	knots    map[string]map[string]*export.Knot
	// public are the public projects' names, in the export's order.
	public []string
}

func newIndex(e *export.Export) *index {
	idx := &index{projects: map[string]*export.Project{}, knots: map[string]map[string]*export.Knot{}}
	for i := range e.Projects {
		p := &e.Projects[i]
		idx.projects[p.Name] = p
		if p.Visibility == export.Public {
			idx.public = append(idx.public, p.Name)
		}
		byID := map[string]*export.Knot{}
		for j := range p.Knots {
			byID[p.Knots[j].ID] = &p.Knots[j]
		}
		idx.knots[p.Name] = byID
	}
	return idx
}

func knotURL(project, id string) string {
	return "/knots/" + url.PathEscape(project) + "/" + url.PathEscape(id)
}

// knotLink is a knot as a page links to it.
type knotLink struct {
	Project string
	ID      string
	Title   string
	State   string
	URL     string
}

func linkOf(project string, k *export.Knot) knotLink {
	return knotLink{Project: project, ID: k.ID, Title: k.Title, State: k.State, URL: knotURL(project, k.ID)}
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

func (q *request) plan() {
	var p planPage
	p.Projects = strings.Join(q.idx.public, " · ")
	counts := map[string]int{}
	for _, name := range q.idx.public {
		proj := q.idx.projects[name]
		roots := set(proj.Roots)
		for i := range proj.Knots {
			k := &proj.Knots[i]
			if (k.Type == "outcome" || k.Type == "bug") && !roots[k.ID] {
				counts[k.State]++
			}
		}
		p.InFlight = append(p.InFlight, q.inFlight(proj)...)
		p.Changes = append(p.Changes, q.readyChanges(proj)...)
		p.Paths = append(p.Paths, q.paths(proj)...)
		p.Milestones = append(p.Milestones, q.milestones(proj)...)
	}
	for _, s := range []string{"done", "in-progress", "ready", "blocked"} {
		p.Tally = append(p.Tally, tally{counts[s], stateLabel(s)})
	}
	q.page(http.StatusOK, "plan", "Plan", p)
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
		f := flight{Knot: linkOf(p.Name, k), Note: "in progress, unclaimed"}
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
func (q *request) readyChanges(p *export.Project) []readyChange {
	var out []readyChange
	for _, c := range p.Changes {
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
			if k, ok := q.idx.knots[p.Name][ref.ID]; ok {
				rc.Knots = append(rc.Knots, linkOf(p.Name, k))
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
func (q *request) paths(p *export.Project) []path {
	knots := q.idx.knots[p.Name]
	var out []path
	for i := range p.Knots {
		parent := &p.Knots[i]
		var kids []*export.Knot
		siblings := map[string]bool{}
		for _, id := range parent.Children {
			if k, ok := knots[id]; ok && k.State != "abandoned" {
				kids = append(kids, k)
				siblings[id] = true
			}
		}
		ordered := false
		for _, k := range kids {
			for _, b := range k.BlockedBy {
				ordered = ordered || siblings[b]
			}
		}
		if !ordered {
			continue
		}
		stage := map[string]int{}
		// Each pass places the children whose sibling blockers are all
		// placed. The graph holds blocked_by acyclic; a cycle it did not
		// would end the passes, and its knots share a last stage.
		for placed := 0; placed < len(kids); {
			progress := false
			for _, k := range kids {
				if _, done := stage[k.ID]; done {
					continue
				}
				at, ready := 0, true
				for _, b := range k.BlockedBy {
					if !siblings[b] {
						continue
					}
					s, ok := stage[b]
					if !ok {
						ready = false
						break
					}
					at = max(at, s+1)
				}
				if ready {
					stage[k.ID] = at
					placed++
					progress = true
				}
			}
			if !progress {
				last := 0
				for _, s := range stage {
					last = max(last, s+1)
				}
				for _, k := range kids {
					if _, done := stage[k.ID]; !done {
						stage[k.ID] = last
						placed++
					}
				}
			}
		}
		pa := path{Parent: linkOf(p.Name, parent)}
		for _, k := range kids {
			s := stage[k.ID]
			for len(pa.Stages) <= s {
				pa.Stages = append(pa.Stages, nil)
			}
			pa.Stages[s] = append(pa.Stages[s], linkOf(p.Name, k))
		}
		out = append(out, pa)
	}
	return out
}

// milestones are the project's roots, each with its tree, and then its
// knots that have no parent and are no root, as one more box.
func (q *request) milestones(p *export.Project) []milestone {
	knots := q.idx.knots[p.Name]
	var out []milestone
	for _, id := range p.Roots {
		k, ok := knots[id]
		if !ok {
			continue
		}
		var desc []*export.Knot
		q.descendants(p.Name, k, &desc, map[string]bool{k.ID: true})
		done, of := 0, 0
		var cord []string
		for _, d := range desc {
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
			di, dj := cord[i] == "done", cord[j] == "done"
			if di != dj {
				return di
			}
			return rank(cord[i]) < rank(cord[j])
		})
		link := linkOf(p.Name, k)
		out = append(out, milestone{Knot: &link, Title: k.Title, Project: p.Name,
			Count: strconv.Itoa(done) + " of " + strconv.Itoa(of) + " done", Cord: cord,
			Tree: q.tree(p.Name, k.Children, map[string]bool{k.ID: true})})
	}
	roots := set(p.Roots)
	var loose []string
	for _, k := range p.Knots {
		if k.Parent == "" && !roots[k.ID] {
			loose = append(loose, k.ID)
		}
	}
	if len(loose) > 0 {
		out = append(out, milestone{Title: "Unfiled", Project: p.Name, Count: count(len(loose), "knot"),
			Tree: q.tree(p.Name, loose, map[string]bool{})})
	}
	return out
}

// descendants appends every knot under k, depth first. seen guards
// against a parent cycle the graph would not hold.
func (q *request) descendants(project string, k *export.Knot, into *[]*export.Knot, seen map[string]bool) {
	for _, id := range k.Children {
		c, ok := q.idx.knots[project][id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		*into = append(*into, c)
		q.descendants(project, c, into, seen)
	}
}

// tree is the knots called ids and everything under them, each level
// ordered by state.
func (q *request) tree(project string, ids []string, seen map[string]bool) []node {
	var kids []*export.Knot
	for _, id := range ids {
		if k, ok := q.idx.knots[project][id]; ok && !seen[id] {
			kids = append(kids, k)
		}
	}
	sort.SliceStable(kids, func(i, j int) bool { return rank(kids[i].State) < rank(kids[j].State) })
	var out []node
	for _, k := range kids {
		inner := map[string]bool{k.ID: true}
		for id := range seen {
			inner[id] = true
		}
		n := node{Row: q.row(project, k), Open: k.State != "done" && k.State != "abandoned"}
		n.Children = q.tree(project, k.Children, inner)
		out = append(out, n)
	}
	return out
}

// row is a knot's line in a tree. Filter is the state the page's filters
// read: abandoned work is finished work, as done is.
func (q *request) row(project string, k *export.Knot) row {
	r := row{knotLink: linkOf(project, k), Filter: k.State}
	if k.State == "abandoned" {
		r.Filter = "done"
	}
	if k.Target != "" && k.Target != project {
		text, _ := q.idx.named(project, k.Target)
		r.Chips = append(r.Chips, chip{"quiet", "lands in " + text})
	}
	if k.Claim != nil {
		r.Chips = append(r.Chips, chip{"", "claimed by " + k.Claim.By})
	}
	if k.State == "blocked" {
		if len(k.WaitingOn) > 0 {
			r.Chips = append(r.Chips, chip{"blocked", "waits on " + q.idx.waitsOn(k.WaitingOn)})
		} else {
			r.Chips = append(r.Chips, chip{"blocked", "waits on " + unpublishedKnot})
		}
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
