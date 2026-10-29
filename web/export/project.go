package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The schemas of the valley's own JSON the projection reads.
const (
	planSchema    = "valley-plan/v1"
	pendingSchema = "valley-pending/v1"
	detailSchema  = "valley-knot/v1"
)

// ErrInconsistent is the error for inputs read at different graph tips: a
// knot's detail read after the plan saw a later graph. The publisher reads
// the inputs again rather than publish them.
var ErrInconsistent = errors.New("the inputs were read at different graph tips")

// Inputs are what the projection reads: the valley's own JSON, as its
// commands print it, and the publisher's configuration.
type Inputs struct {
	// Plan is `valley plan --all --json`.
	Plan []byte
	// Pending is `valley pending --all --json`.
	Pending []byte
	// Details are `valley knot show --project P <id> --json`, one for
	// every knot of every public project.
	Details [][]byte
	// Identities are the publication identities of the projects to
	// export, by project. A project it does not name is not exported.
	Identities map[string]string
	// SourceTips are each public project's main.
	SourceTips map[string]string
	// Principals is the display mapping principal names pass through.
	Principals Principals
	// Groups are the instance's groups, in identifier order, and
	// Placements where the instance places each project on the web, by
	// project, as the instance declares them. Every public project has a
	// group. The export carries the groups of its public projects alone.
	Groups     []Group
	Placements map[string]Placement
}

// Principals maps a principal's name to what an export shows in its place,
// such as operator or agent. A name it does not map is shown as Unmapped.
type Principals map[string]string

// Unmapped is what an export shows for a principal the display mapping
// does not name.
const Unmapped = "contributor"

// Show is what an export shows for the principal name: its mapping, or
// Unmapped. The empty name, for a field that names nobody, stays empty.
func (p Principals) Show(name string) string {
	if name == "" {
		return ""
	}
	if shown, ok := p[name]; ok {
		return shown
	}
	return Unmapped
}

// check holds every shown name to 1 to 64 bytes of UTF-8 with no control
// character.
func (p Principals) check() error {
	for name, shown := range p {
		bad := len(shown) == 0 || len(shown) > 64 || !utf8.ValidString(shown) ||
			containsControl(shown)
		if bad {
			return fmt.Errorf("the display mapping shows %q as %q; a shown name is 1 to 64 bytes of text, with no control character", name, shown)
		}
	}
	return nil
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// The valley's JSON, as far as the projection reads it. A field not named
// here is never read, so a field the valley adds is not exported until the
// projection names it.
type (
	planDoc struct {
		Schema   string        `json:"schema"`
		Now      string        `json:"now"`
		Projects []planProject `json:"projects"`
	}
	planProject struct {
		Project       string      `json:"project"`
		Error         *string     `json:"error"`
		Tip           string      `json:"tip"`
		Roots         []string    `json:"roots"`
		Knots         []planKnot  `json:"knots"`
		InFlight      []string    `json:"in_flight"`
		OpenResiduals []Residuals `json:"open_residuals"`
	}
	planKnot struct {
		ID              string    `json:"id"`
		Type            string    `json:"type"`
		Status          string    `json:"status"`
		Active          bool      `json:"active"`
		Title           string    `json:"title"`
		Created         string    `json:"created"`
		CreatedBy       string    `json:"created_by"`
		Source          string    `json:"source"`
		Parent          string    `json:"parent"`
		Owner           string    `json:"owner"`
		BlockedBy       []string  `json:"blocked_by"`
		Changes         []string  `json:"changes"`
		Target          string    `json:"target"`
		GraduatedInto   string    `json:"graduated_into"`
		CommentCount    int       `json:"comment_count"`
		State           string    `json:"state"`
		Ready           bool      `json:"ready"`
		WaitingOn       []string  `json:"waiting_on"`
		ClearedByEnding []string  `json:"cleared_by_ending"`
		Dangling        []string  `json:"dangling"`
		Unresolved      []string  `json:"unresolved"`
		Children        []string  `json:"children"`
		Blocks          []string  `json:"blocks"`
		Progress        *Progress `json:"progress"`
		Claim           *Claim    `json:"claim"`
		Edges           []Edge    `json:"edges"`
	}
	detailDoc struct {
		Schema  string `json:"schema"`
		Project string `json:"project"`
		Tip     string `json:"tip"`
		Knot    struct {
			ID       string    `json:"id"`
			Body     *string   `json:"body"`
			Comments []Comment `json:"comments"`
		} `json:"knot"`
	}
	pendingDoc struct {
		Schema string        `json:"schema"`
		Repos  []pendingRepo `json:"repos"`
	}
	pendingRepo struct {
		Repo    string          `json:"repo"`
		Error   *string         `json:"error"`
		Changes []pendingChange `json:"changes"`
	}
	pendingChange struct {
		ChangeID string           `json:"change_id"`
		Handle   string           `json:"handle"`
		Target   string           `json:"target"`
		Owner    string           `json:"owner"`
		State    string           `json:"state"`
		Active   int              `json:"active"`
		Versions []pendingVersion `json:"versions"`
		Includes []string         `json:"includes"`
		Knots    []KnotRef        `json:"knots"`
	}
	pendingVersion struct {
		N        int    `json:"n"`
		Head     string `json:"head"`
		Title    string `json:"title"`
		Uploader string `json:"uploader"`
		Landing  string `json:"landing"`
		Request  *struct {
			Commit string `json:"commit"`
		} `json:"request"`
		Outcome *Outcome `json:"outcome"`
	}
)

// detailKey names a knot's detail: IDs are unique within a project, not
// across projects.
type detailKey struct{ project, id string }

// Build projects the valley's JSON onto the public export. It is a pure
// function of its inputs. It exports each project whose visibility is
// public, and everything of it an anonymous reader may see: every knot,
// with its body and comments, and the changes ready for approval, each
// with its active version alone. It leaves out what is not the reader's:
// the plan's operator sections, drafts, branches, older versions,
// repository URLs, error text and the file each knot is stored in. A
// reference to a project that is not exported keeps only the reference,
// and every principal name passes through the display mapping.
//
// A public project whose plan or pending listing could not be read, whose
// knot lacks its detail, or whose detail was read at another graph tip
// fails the projection, so a partial export is never published. The last
// of these is ErrInconsistent.
func Build(in Inputs) (*Export, error) {
	if err := in.Principals.check(); err != nil {
		return nil, err
	}
	if err := CheckNames(context.Background(), in.Groups, Sorted(in.Placements)); err != nil {
		return nil, err
	}
	var plan planDoc
	if err := decodeInput("the plan", in.Plan, planSchema, &plan, &plan.Schema); err != nil {
		return nil, err
	}
	var pending pendingDoc
	if err := decodeInput("the pending listing", in.Pending, pendingSchema, &pending, &pending.Schema); err != nil {
		return nil, err
	}
	details := map[detailKey]detailDoc{}
	for _, raw := range in.Details {
		var d detailDoc
		if err := decodeInput("a knot's detail", raw, detailSchema, &d, &d.Schema); err != nil {
			return nil, err
		}
		key := detailKey{d.Project, d.Knot.ID}
		if _, twice := details[key]; twice {
			return nil, fmt.Errorf("the details hold knot %s:%s twice", d.Project, d.Knot.ID)
		}
		details[key] = d
	}

	exported := map[string]bool{}
	for name, id := range in.Identities {
		if !ValidIdentity(id) {
			return nil, fmt.Errorf("project %s has identity %q, which is not 32 lowercase hex digits", name, id)
		}
		exported[name] = true
	}
	planned := map[string]planProject{}
	for _, p := range plan.Projects {
		planned[p.Project] = p
	}
	repos := map[string]pendingRepo{}
	for _, r := range pending.Repos {
		repos[r.Repo] = r
	}

	e := &Export{Schema: Schema, Collected: plan.Now, Projects: []Project{}}
	var names []string
	for name := range exported {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p, ok := planned[name]
		if !ok {
			return nil, fmt.Errorf("exported project %s is not in the plan", name)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("the plan could not read exported project %s", name)
		}
		r, ok := repos[name]
		if !ok {
			return nil, fmt.Errorf("exported project %s is not in the pending listing", name)
		}
		if r.Error != nil {
			return nil, fmt.Errorf("the pending listing could not read exported project %s", name)
		}
		out, err := project(name, p, r, details, exported, in)
		if err != nil {
			return nil, err
		}
		e.Projects = append(e.Projects, out)
	}
	groups, err := exportedGroups(e.Projects, in.Groups)
	if err != nil {
		return nil, err
	}
	e.Groups = groups
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return e, nil
}

func project(name string, p planProject, r pendingRepo, details map[detailKey]detailDoc,
	exported map[string]bool, in Inputs) (Project, error) {
	shown := in.Principals.Show
	tip := in.SourceTips[name]
	if !ValidObjectID(tip) {
		return Project{}, fmt.Errorf("exported project %s has no source tip, or one that is not an object ID", name)
	}
	placed := in.Placements[name]
	if placed.Group == "" {
		return Project{}, fmt.Errorf("exported project %s belongs to no group, so it has no web URL", name)
	}
	out := Project{
		Name: name, Identity: in.Identities[name],
		Group: placed.Group, WebName: placed.Name, FormerNames: placed.FormerNames, Aliases: placed.Aliases,
		GraphTip: p.Tip, SourceTip: tip,
		Roots: nonNil(p.Roots), Knots: []Knot{}, OpenResiduals: []Residuals{}, Changes: []Change{},
	}
	for _, g := range p.OpenResiduals {
		out.OpenResiduals = append(out.OpenResiduals, Residuals{Parent: g.Parent, Bugs: nonNil(g.Bugs)})
	}

	// A reference to another project's knot is hidden when that project
	// is not exported. Each list of the view a hidden reference appears in
	// says something of the knot's status, so it is dropped from them all.
	// A knot that waits on a hidden knot says only that it does.
	hidden := func(ref string) bool {
		project, _, qualified := strings.Cut(ref, ":")
		return qualified && !exported[project]
	}
	var inProgress []string
	inFlight := map[string]bool{}
	for _, id := range p.InFlight {
		inFlight[id] = true
	}
	for _, k := range p.Knots {
		d, ok := details[detailKey{name, k.ID}]
		if !ok {
			return Project{}, fmt.Errorf("knot %s:%s has no detail", name, k.ID)
		}
		if d.Tip != p.Tip {
			return Project{}, fmt.Errorf("%w: the plan read %s at %s, and knot %s at %s", ErrInconsistent, name, p.Tip, k.ID, d.Tip)
		}
		if d.Knot.Body == nil {
			return Project{}, fmt.Errorf("the detail of knot %s:%s holds no body", name, k.ID)
		}
		if len(d.Knot.Comments) != k.CommentCount {
			return Project{}, fmt.Errorf("%w: the plan counts %d comments on %s:%s, and its detail holds %d", ErrInconsistent, k.CommentCount, name, k.ID, len(d.Knot.Comments))
		}
		x := Knot{
			ID: k.ID, Type: k.Type, Status: k.Status, Active: k.Active, Title: k.Title,
			Created: k.Created, CreatedBy: shown(k.CreatedBy), Source: k.Source, Parent: k.Parent,
			Owner: shown(k.Owner), BlockedBy: nonNil(k.BlockedBy), Changes: nonNil(k.Changes),
			Target: k.Target, GraduatedInto: k.GraduatedInto,
			State: k.State, Ready: k.Ready,
			WaitingOn: drop(k.WaitingOn, hidden), WaitingOnHidden: slices.ContainsFunc(k.WaitingOn, hidden),
			ClearedByEnding: drop(k.ClearedByEnding, hidden), Dangling: drop(k.Dangling, hidden),
			Unresolved: drop(k.Unresolved, hidden), Children: nonNil(k.Children), Blocks: nonNil(k.Blocks),
			Progress: k.Progress, Edges: []Edge{}, Body: *d.Knot.Body, Comments: []Comment{},
			CommentCount: k.CommentCount,
		}
		if k.Claim != nil {
			c := *k.Claim
			c.By = shown(c.By)
			x.Claim = &c
		}
		for _, edge := range k.Edges {
			if !exported[edge.Project] {
				edge = Edge{Field: edge.Field, Ref: edge.Ref, Project: edge.Project, ID: edge.ID, State: EdgeHidden}
			}
			x.Edges = append(x.Edges, edge)
		}
		for _, c := range d.Knot.Comments {
			c.Author, c.EditedBy = shown(c.Author), shown(c.EditedBy)
			x.Comments = append(x.Comments, c)
		}
		if k.Active && k.Status == "in-progress" && !inFlight[k.ID] {
			inProgress = append(inProgress, k.ID)
		}
		out.Knots = append(out.Knots, x)
	}
	out.InFlight = append(nonNil(p.InFlight), inProgress...)

	// includes may name a draft, which the export does not carry.
	listed := map[string]bool{}
	for _, c := range r.Changes {
		listed[c.ChangeID] = true
	}
	for _, c := range r.Changes {
		if c.State != "ready" {
			return Project{}, fmt.Errorf("the pending listing holds change %s of %s, which is %s, among the ready changes", c.ChangeID, name, c.State)
		}
		var active *pendingVersion
		for i := range c.Versions {
			if c.Versions[i].N == c.Active {
				active = &c.Versions[i]
			}
		}
		if active == nil {
			return Project{}, fmt.Errorf("change %s of %s holds no version %d, its active one", c.ChangeID, name, c.Active)
		}
		v := Version{N: active.N, Head: active.Head, Title: active.Title, Uploader: shown(active.Uploader),
			Landing: active.Landing, Outcome: active.Outcome}
		if active.Request != nil {
			v.Request = &Request{Commit: active.Request.Commit}
		}
		out.Changes = append(out.Changes, Change{
			ChangeID: c.ChangeID, Handle: c.Handle, Target: c.Target, Owner: shown(c.Owner), Version: v,
			Includes: drop(c.Includes, func(id string) bool { return !listed[id] }), Knots: nonNilRefs(c.Knots),
		})
	}
	return out, nil
}

// exportedGroups are the groups the exported projects belong to, in
// identifier order, as groups are: a group no public project belongs to
// is not exported.
func exportedGroups(projects []Project, groups []Group) ([]Group, error) {
	used := map[string]bool{}
	for _, p := range projects {
		used[p.Group] = true
	}
	var out []Group
	for _, g := range groups {
		if used[g.ID] {
			out = append(out, g)
			delete(used, g.ID)
		}
	}
	for _, p := range projects {
		if used[p.Group] {
			return nil, fmt.Errorf("exported project %s belongs to the group %q, which is not declared", p.Name, p.Group)
		}
	}
	return out, nil
}

// decodeInput reads one of the valley's documents and checks its schema.
func decodeInput(what string, raw []byte, schema string, into any, got *string) error {
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if *got != schema {
		return fmt.Errorf("%s has schema %q, not %q", what, *got, schema)
	}
	return nil
}

// drop is list without the entries hidden reports, never nil.
func drop(list []string, hidden func(string) bool) []string {
	kept := []string{}
	for _, s := range list {
		if !hidden(s) {
			kept = append(kept, s)
		}
	}
	return kept
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func nonNilRefs(list []KnotRef) []KnotRef {
	if list == nil {
		return []KnotRef{}
	}
	return list
}
