package knot

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Graph is a whole graph, read: every knot, and every comment on each.
type Graph struct {
	Index    *Index
	Knots    map[string]*Knot
	Comments map[string][]Noted // by the ID of the knot they are on, oldest first

	// children and blocks are the edges read backwards, built on the
	// first view.
	children, blocks map[string][]string
}

// Noted is one comment as the graph holds it: what it says, where it is,
// when it was written, and the operation that wrote it.
type Noted struct {
	Comment
	Path string
	At   time.Time
	Op   string
}

// Load reads and validates a whole graph.
func Load(idx *Index, read Reader) (*Graph, error) {
	if err := Validate(idx, read); err != nil {
		return nil, err
	}
	g := &Graph{Index: idx, Knots: map[string]*Knot{}, Comments: map[string][]Noted{}}
	for id, f := range idx.Knots {
		data, err := read(f.OID, MaxKnotSize)
		if err != nil {
			return nil, err
		}
		if g.Knots[id], err = ParseKnot(f.Path, f.Slug, data); err != nil {
			return nil, err
		}
	}
	for path, f := range idx.Comments {
		data, err := read(f.OID, MaxCommentSize)
		if err != nil {
			return nil, err
		}
		c, err := ParseComment(path, data)
		if err != nil {
			return nil, err
		}
		at, err := time.Parse(CommentStamp, f.Stamp)
		if err != nil {
			return nil, fmt.Errorf("%s: its stamp %s is no time", path, f.Stamp)
		}
		g.Comments[f.ID] = append(g.Comments[f.ID], Noted{Comment: *c, Path: path, At: at, Op: f.Op})
	}
	for _, list := range g.Comments {
		sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	}
	return g, nil
}

// Empty is the graph a project holds before its first knot.
func Empty() *Graph {
	idx, _ := Layout(nil)
	return &Graph{Index: idx, Knots: map[string]*Knot{}, Comments: map[string][]Noted{}}
}

// IDs are every knot's ID, sorted.
func (g *Graph) IDs() []string {
	ids := make([]string, 0, len(g.Knots))
	for id := range g.Knots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Foreign resolves a reference to another project's knot. found is false
// when that project's graph holds no such knot; err is for a graph that
// could not be read.
type Foreign func(ref string) (k *Knot, found bool, err error)

// View is what the graph says of one knot beyond its own fields: whether
// it is ready to be worked on, what holds it up, and what it holds up.
// None of it is stored; it is derived from the graph each time.
type View struct {
	// Ready is an active knot, unclaimed, every blocker of which is
	// terminal.
	Ready bool `json:"ready"`
	// WaitingOn are the blockers that are not terminal, or that could not
	// be resolved.
	WaitingOn []string `json:"waiting_on"`
	// ClearedByEnding are blockers that cleared by being abandoned,
	// discarded or superseded rather than finished: they no longer block,
	// as the archive's rule says, but a reader should know.
	ClearedByEnding []string `json:"cleared_by_ending"`
	// Dangling are references to another project's knots that its graph
	// does not hold; Unresolved are those whose graph could not be read.
	Dangling   []string `json:"dangling"`
	Unresolved []string `json:"unresolved"`
	// Children are the knots whose parent this is, and Blocks the knots
	// whose blocked_by names it.
	Children []string `json:"children"`
	Blocks   []string `json:"blocks"`
	// Progress is set for a knot with children.
	Progress *Progress `json:"progress,omitempty"`
	// Claim is set for a claimed knot.
	Claim *Claim `json:"claim,omitempty"`
}

// Progress is a parent's children that are finished, out of its children:
// terminal children that are not abandoned, out of the children that are
// not abandoned.
type Progress struct {
	Done int `json:"done"`
	Of   int `json:"of"`
}

// Claim is a knot's claim, with its age.
type Claim struct {
	By         string `json:"by"`
	At         string `json:"at"`
	AgeSeconds int64  `json:"age_seconds"`
	// Stale is a claim older than StaleClaim: the run that made it may
	// have ended without releasing it.
	Stale bool `json:"stale"`
}

// endings are the terminal statuses that end a knot without finishing it.
var endings = []string{"abandoned", "discarded", "superseded"}

// View derives the view of the knot with id at the time now. foreign
// resolves references to other projects' knots; nil leaves them all
// unresolved.
func (g *Graph) View(id string, now time.Time, foreign Foreign) View {
	k := g.Knots[id]
	v := View{WaitingOn: []string{}, ClearedByEnding: []string{}, Dangling: []string{}, Unresolved: []string{},
		Children: []string{}, Blocks: []string{}}
	for _, ref := range k.BlockedBy {
		var b *Knot
		if IsLocalID(ref) {
			b = g.Knots[ref]
		} else if foreign == nil {
			v.Unresolved = append(v.Unresolved, ref)
		} else if found, ok, err := foreign(ref); err != nil {
			v.Unresolved = append(v.Unresolved, ref)
		} else if !ok {
			v.Dangling = append(v.Dangling, ref)
		} else {
			b = found
		}
		switch {
		case b == nil:
			v.WaitingOn = append(v.WaitingOn, ref)
		case b.Active():
			v.WaitingOn = append(v.WaitingOn, ref)
		case slices.Contains(endings, b.Status):
			v.ClearedByEnding = append(v.ClearedByEnding, ref)
		}
	}
	if g.children == nil {
		g.children, g.blocks = map[string][]string{}, map[string][]string{}
		for _, other := range g.IDs() {
			o := g.Knots[other]
			if o.Parent != "" {
				g.children[o.Parent] = append(g.children[o.Parent], other)
			}
			for _, ref := range o.BlockedBy {
				g.blocks[ref] = append(g.blocks[ref], other)
			}
		}
	}
	var progress Progress
	for _, child := range g.children[id] {
		v.Children = append(v.Children, child)
		if o := g.Knots[child]; o.Status != "abandoned" {
			progress.Of++
			if !o.Active() {
				progress.Done++
			}
		}
	}
	v.Blocks = append(v.Blocks, g.blocks[id]...)
	if len(v.Children) > 0 {
		v.Progress = &progress
	}
	if k.ClaimedBy != "" {
		c := &Claim{By: k.ClaimedBy, At: k.ClaimedAt}
		if at, err := time.Parse(time.RFC3339, k.ClaimedAt); err == nil {
			age := now.Sub(at)
			c.AgeSeconds = int64(age / time.Second)
			c.Stale = age > StaleClaim
		}
		v.Claim = c
	}
	v.Ready = k.Active() && k.ClaimedBy == "" && len(v.WaitingOn) == 0
	return v
}

// Schema is the model as `valley knot schema` prints it: the types and
// their statuses, the fields, the edges and the limits, so that a reader
// of the graph never has to read the CUE schema.
type Schema struct {
	Schema string  `json:"schema"`
	Ref    string  `json:"ref"`
	Types  []Type  `json:"types"`
	Fields []Field `json:"fields"`
	Edges  []Edge  `json:"edges"`
	Limits Limits  `json:"limits"`
}

// Edge is one field that names other knots.
type Edge struct {
	Field   string `json:"field"`
	From    string `json:"from"`
	To      string `json:"to"`
	Meaning string `json:"meaning"`
}

// Limits are the limits a graph is held to.
type Limits struct {
	Knots        int `json:"knots"`
	Comments     int `json:"comments"`
	KnotBytes    int `json:"knot_bytes"`
	CommentBytes int `json:"comment_bytes"`
	ChangedPaths int `json:"changed_paths_per_commit"`
	TitleBytes   int `json:"title_bytes"`
	ListItems    int `json:"list_items"`
}

// TheSchema is the model.
func TheSchema() Schema {
	return Schema{
		Schema: "valley-knot-schema/v1",
		Ref:    Ref,
		Types:  Types,
		Fields: Fields,
		Edges: []Edge{
			{Field: "parent", From: "any knot", To: "an outcome in this graph",
				Meaning: "the knot belongs to the outcome; parent edges form no cycle"},
			{Field: "blocked_by", From: "outcome, bug", To: "any knot in this graph, or <project>:<id> in another",
				Meaning: "the knot waits on each until it is terminal; blocked_by edges form no cycle"},
			{Field: "graduated_into", From: "a graduated idea", To: "a decision in this graph",
				Meaning: "where the idea's thinking moved"},
		},
		Limits: Limits{Knots: MaxKnots, Comments: MaxComments, KnotBytes: MaxKnotSize,
			CommentBytes: MaxCommentSize, ChangedPaths: MaxChangedPaths, TitleBytes: MaxTitle, ListItems: MaxListItems},
	}
}

// Describe is the schema as text, for a terminal.
func Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "knots live at %s, one file each; comments at comments/<id>/.\n\ntypes:\n", Ref)
	for _, t := range Types {
		fmt.Fprintf(&b, "  %-9s %s-<7 hex>  in %s/\n", t.Name, t.Prefix, t.Directory)
		fmt.Fprintf(&b, "            active: %s   terminal: %s\n", strings.Join(t.Active, ", "), strings.Join(t.Terminal, ", "))
	}
	b.WriteString("\nfields:\n")
	for _, f := range Fields {
		fmt.Fprintf(&b, "  %-15s on %s; %s\n", f.Name, strings.Join(f.On, ", "), f.Meaning)
	}
	b.WriteString("\nedges:\n")
	for _, e := range TheSchema().Edges {
		fmt.Fprintf(&b, "  %-15s %s → %s: %s\n", e.Field, e.From, e.To, e.Meaning)
	}
	fmt.Fprintf(&b, "\nlimits: %d knots, %d comments, %d bytes a knot, %d a comment, %d files a commit\n",
		MaxKnots, MaxComments, MaxKnotSize, MaxCommentSize, MaxChangedPaths)
	return b.String()
}
