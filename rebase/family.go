// A family of layers, captured from one snapshot of origin.
//
// A layer is a change whose active version has not landed, in any state,
// or a branch on origin that no record is on, that main does not reach,
// and that no landed version's head reaches (Snapshot.Unrecorded).
// Each layer sits on its base: the nearest layer its head strictly
// contains, as valley pending nests its entries (Snapshot.Nest). A layer
// with no base sits on main. Linear history gives every layer at most one
// base, so the layers on a bottom layer form a tree: one chain, as in
// A → B → C, or a shared lower layer with several children, as in A → B
// and A → C. That tree is a family.
//
// Capture reads a family and every layer's state, and refuses nothing a
// caller could choose to hold instead: a draft, a withdrawn change, a
// plain branch, a change that may have landed. Check then refuses what the
// engine cannot rebase whole:
//
//   - a change layer is not ready, names no branch, or its branch is
//     missing on origin or not at its recorded head;
//   - a plain branch in the family is the branch of a change that is no
//     layer, as a change is once a rebase has moved its branch and could
//     not add the rebased head to its record;
//   - two layers are on one branch;
//   - the commits to be rebased under a top layer hold a merge, or a
//     version head of a change that is not ready or is no layer of that
//     top's chain. A rebase gives every commit a new ID, so such a commit
//     would ride into the family under an ID no version records. That is
//     the rule the integrator lands by (integrator/records.go), applied
//     before the rebase.
//
// Commits under the bottom that a lower change landed drop out of the
// rebase: the integrator landed them as one new commit, so their content
// is on main already though main does not reach them. They are the commits
// a landed version's head reaches.

package rebase

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"the-valley/change"
)

// State is what a layer is, for a caller deciding whether to move it.
type State string

const (
	Ready     State = "ready"     // a ready change
	Draft     State = "draft"     // a change its owner paused
	Withdrawn State = "withdrawn" // a change its owner withdrew
	Plain     State = "branch"    // a branch no record is on
	Submitted State = "submitted" // a change whose active version has landed
	Uncertain State = "uncertain" // a change whose active version may have landed, with no evidence either way
)

// Layer is one layer of a family as captured.
type Layer struct {
	Name   string // a change's handle, or a branch's name
	ID     string // a change's ID; "" for a plain branch
	Branch string // the branch a rebase pushes: the active version's, or the plain branch
	Tip    string // that branch's tip on origin as captured; "" when it is missing

	// A change's record as captured: its ref and commit, the record, and
	// the number of its active version.
	Ref, Commit string
	Record      *change.Record
	Version     int

	Head    string // the head rebased: the active version's, or the branch's tip
	Base    string // the commit its own commits start above; set by Check
	Landing string // a change's active version's landing evidence; "" for a plain branch
	When    int64  // its head's commit time

	Below *Layer   // the layer it sits on; nil for the bottom
	Above []*Layer // the layers that sit on it
}

// State is what the layer is: a plain branch, a change that has landed or
// may have, or else its record's state.
func (l *Layer) State() State {
	switch {
	case l.ID == "":
		return Plain
	case l.Landing == Landed || l.Landing == LandedRecorded:
		return Submitted
	case l.Landing == LandingUncertain:
		return Uncertain
	}
	return State(l.Record.State)
}

// Family is a tree of layers on one bottom layer, as captured from one
// snapshot.
type Family struct {
	Tip    string   // main on origin as captured: what the family is rebased onto
	Layers []*Layer // bottom first, every layer after the layer below it
	Start  *Layer   // the layer the capture started from

	// Cut is the head of each landed version under the bottom: the
	// commits they reach drop out of the rebase. Set by Check.
	Cut []string

	s       *Snapshot
	want    string
	checked bool
	err     error
}

// Bottom is the layer every other layer of the family sits on.
func (f *Family) Bottom() *Layer { return f.Layers[0] }

// Tops is every layer nothing sits on, in the family's order.
func (f *Family) Tops() []*Layer {
	var tops []*Layer
	for _, l := range f.Layers {
		if len(l.Above) == 0 {
			tops = append(tops, l)
		}
	}
	return tops
}

// FastForwards reports whether the family already fast-forwards from
// main: whether its bottom's head contains main's tip, so a rebase would
// change nothing.
func (f *Family) FastForwards() bool {
	return f.s.g.succeeds(f.s.ctx, true, "merge-base", "--is-ancestor", f.Tip, f.Bottom().Head)
}

// Selection is where a capture starts, and how much of the family it
// takes.
type Selection struct {
	ID     string // the change the capture starts from, or
	Branch string // the plain branch it starts from

	// Whole takes every layer connected to the start: the layers under it,
	// the layers on them, and so on. Otherwise the capture takes the start
	// and the chain of layers under it, which is what valley rebase moves.
	Whole bool

	// Head, when set, is the head the caller showed as the start's: Check
	// refuses the family when the start is at another head by now.
	Head string
}

// Capture reads the family a selection names from the snapshot. The only
// refusals are a start that origin no longer holds.
func Capture(s *Snapshot, sel Selection) (*Family, error) {
	var start *Layer
	if sel.ID != "" {
		for _, n := range s.Changes {
			if n.ID == sel.ID {
				start = s.changeLayer(n)
			}
		}
		if start == nil {
			return nil, &Refusal{Kind: Gone, Name: s.Handle(sel.ID)}
		}
	} else {
		head := s.Refs["refs/heads/"+sel.Branch]
		if head == "" {
			return nil, &Refusal{Kind: StartMissing, Name: sel.Branch}
		}
		start = &Layer{Name: sel.Branch, Branch: sel.Branch, Head: head}
	}
	// A chain is the start and what its head contains, so only they are
	// measured. A whole family is found among every layer there is.
	contains := func(head string) bool { return sel.Whole || s.StrictAncestor(head, start.Head) }
	all := []*Layer{start}
	for _, n := range s.Changes {
		if n.ID != start.ID && s.Unlanded(n) && contains(HeadOf(n.Record.ActiveVersion())) {
			all = append(all, s.changeLayer(n))
		}
	}
	for _, b := range s.Unrecorded() {
		if b.Branch != start.Name && contains(b.Head) {
			all = append(all, &Layer{Name: b.Branch, Branch: b.Branch, Head: b.Head})
		}
	}
	nodes := make([]Node, len(all))
	for i, l := range all {
		out, _ := s.g.output(s.ctx, true, "log", "-1", "--format=%ct", l.Head)
		l.When, _ = strconv.ParseInt(out, 10, 64)
		if l.Branch != "" {
			l.Tip = s.Refs["refs/heads/"+l.Branch]
		}
		nodes[i] = Node{Name: l.Name, Head: l.Head, When: l.When}
	}
	nest := s.Nest(nodes)
	var keep []*Layer
	if sel.Whole {
		keep = connected(all, nest.Base)
	} else {
		for i := 0; i >= 0; i = nest.Base[i] {
			keep = append([]*Layer{all[i]}, keep...)
		}
	}
	index := map[*Layer]int{}
	for i, l := range all {
		index[l] = i
	}
	member := map[*Layer]bool{}
	for _, l := range keep {
		member[l] = true
	}
	for _, l := range keep {
		if b := nest.Base[index[l]]; b >= 0 && member[all[b]] {
			l.Below = all[b]
			l.Below.Above = append(l.Below.Above, l)
		}
	}
	f := &Family{Tip: s.Tip, Start: start, s: s, want: sel.Head}
	f.Layers = order(keep)
	return f, nil
}

// changeLayer is a change as a layer: its handle, its record, its active
// version's head and branch, and how far that version has landed.
func (s *Snapshot) changeLayer(n Recorded) *Layer {
	v := n.Record.ActiveVersion()
	landing, _ := s.Landing(n.ID, v)
	return &Layer{
		Name: s.Handle(n.ID), ID: n.ID, Branch: v.Branch,
		Ref: change.Ref(Target, n.ID), Commit: n.Commit, Record: n.Record, Version: v.N,
		Head: HeadOf(v), Landing: landing,
	}
}

// connected is the layers joined to the first of all through the bases
// given: the first's bottom, and every layer on that bottom.
func connected(all []*Layer, base []int) []*Layer {
	root := func(i int) int {
		for base[i] >= 0 {
			i = base[i]
		}
		return i
	}
	r := root(0)
	var keep []*Layer
	for i := range all {
		if root(i) == r {
			keep = append(keep, all[i])
		}
	}
	return keep
}

// order puts a family bottom first, then breadth first, each layer's
// children in the order of their names.
func order(layers []*Layer) []*Layer {
	var bottom *Layer
	for _, l := range layers {
		if l.Below == nil {
			bottom = l
		}
	}
	out := []*Layer{bottom}
	for i := 0; i < len(out); i++ {
		above := out[i].Above
		sort.SliceStable(above, func(a, b int) bool { return above[a].Name < above[b].Name })
		out = append(out, above...)
	}
	return out
}

// Check refuses a family the engine cannot rebase whole, before anything
// is rebased, and finds where each layer's own commits start: for the
// bottom, the parent of its oldest commit; for every other layer, the
// head of the layer below. It returns a *Refusal, or Refusals when every
// layer's branch was read and several were refused. It checks once, and
// gives the same answer when asked again.
func (f *Family) Check() error {
	if !f.checked {
		f.checked, f.err = true, f.check()
	}
	return f.err
}

func (f *Family) check() error {
	var tips Refusals // the branches not at their changes' recorded heads
	in := map[string]bool{}
	for _, l := range f.Layers {
		if l.ID != "" {
			in[l.ID] = true
		}
	}
	for _, l := range f.Layers {
		switch {
		case l.ID != "" && l.Branch == "":
			return &Refusal{Kind: NoBranch, Layer: l}
		case l.Tip == "":
			return &Refusal{Kind: BranchMissing, Layer: l}
		case l.ID != "" && l.Record.State != change.Ready:
			return &Refusal{Kind: NotReady, Layer: l}
		case l == f.Start && f.want != "" && l.Head != f.want:
			return &Refusal{Kind: HeadMoved, Layer: l, Commit: f.want}
		case l.ID != "" && l.Tip != l.Head:
			tips = append(tips, &Refusal{Kind: TipMoved, Layer: l})
		}
		if l.ID != "" {
			continue
		}
		// A plain branch that is a change's branch holds a head no version
		// of that change records, as after a rebase that moved the branch
		// and could not add the version.
		for _, n := range f.s.Changes {
			v := n.Record.ActiveVersion()
			if in[n.ID] || v.Branch != l.Branch || !f.s.Unlanded(n) {
				continue
			}
			tips = append(tips, f.s.about(&Refusal{Kind: BranchOfChange, Layer: l, Version: v}, n))
		}
	}
	// Two layers on one branch leave a change's branch off its recorded
	// head as well, so they are refused first, for what they are.
	on := map[string]*Layer{}
	for _, l := range f.Layers {
		if o := on[l.Branch]; o != nil {
			return &Refusal{Kind: SharedBranch, Layer: l, Other: o}
		}
		on[l.Branch] = l
	}
	if len(tips) > 0 {
		return tips
	}
	return f.commits()
}

// commits reads the commits the rebase moves, top by top: those each
// top's head reaches, less those main reaches and those a landed
// version's head reaches under the bottom. It refuses a merge among them,
// and a version head of a change that is not ready or is not on that
// top's chain. It sets each layer's base.
func (f *Family) commits() error {
	s, bottom := f.s, f.Bottom()
	not := []string{"^" + f.Tip}
	for _, h := range s.LandedUnder(bottom.Head) {
		not, f.Cut = append(not, "^"+h), append(f.Cut, h)
	}
	for _, top := range f.Tops() {
		chain := map[string]bool{}
		for l := top; l != nil; l = l.Below {
			if l.ID != "" {
				chain[l.ID] = true
			}
		}
		out, err := s.g.output(s.ctx, true, append([]string{"rev-list", "--parents", top.Head}, not...)...)
		if err != nil {
			return &Refusal{Kind: StackUnread, Layer: top}
		}
		moved := map[string]bool{}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 2 {
				return &Refusal{Kind: Merge, Layer: top, Commit: fields[0]}
			}
			if len(fields) > 0 {
				moved[fields[0]] = true
			}
		}
		for _, n := range s.Changes {
			for _, v := range n.Record.Versions {
				c := HeadOf(v)
				if !moved[c] || (chain[n.ID] && n.Record.State == change.Ready) {
					continue
				}
				return s.about(&Refusal{Kind: ForeignHead, Layer: top, Version: v, Commit: c}, n)
			}
		}
	}
	out, err := s.g.output(s.ctx, true, append([]string{"rev-list", "--parents", "--reverse", bottom.Head}, not...)...)
	if err != nil {
		return &Refusal{Kind: LayerUnread, Layer: bottom}
	}
	bottom.Base = bottom.Head
	if oldest, _, _ := strings.Cut(out, "\n"); oldest != "" {
		fields := strings.Fields(oldest)
		if len(fields) < 2 {
			return &Refusal{Kind: NoHistory, Layer: bottom}
		}
		bottom.Base = fields[1]
	}
	for _, l := range f.Layers[1:] {
		l.Base = l.Below.Head
	}
	return nil
}

// Kind is what a refusal is about.
type Kind int

const (
	// Capture: the start is gone from origin.
	Gone         Kind = iota + 1 // the start's record
	StartMissing                 // the start's branch

	// Check.
	NoBranch       // a change layer records no branch
	BranchMissing  // a layer's branch is missing on origin
	NotReady       // a change layer is not ready
	HeadMoved      // the start is no longer at Selection.Head, which Commit holds
	TipMoved       // a change layer's branch is not at its recorded head
	BranchOfChange // a plain branch is the branch of Change, which is no layer
	SharedBranch   // Layer and Other are on one branch
	StackUnread    // the commits under a top could not be read
	LayerUnread    // the bottom's commits could not be read
	Merge          // the commits under a top hold the merge Commit
	ForeignHead    // the commit Commit under a top is Version of Change, which is not ready or not on that top's chain
	NoHistory      // the bottom shares no history with main

	// Prepare: nothing is pushed.
	NoWorktree // no directory could be made for the rebase
	NoCheckout // the bottom could not be checked out
	Conflict   // a layer conflicts with what it is rebased onto
	NoNewHead  // a layer's rebased head could not be read
	EmptyLayer // a layer adds nothing once rebased

	// Publish, read again just before the push: nothing is pushed.
	OriginUnread   // origin could not be read again
	RecordGone     // a change layer's record is gone
	RecordUnread   // a change layer's moved record could not be read
	RecordNotReady // a change layer's record, now Change, is not ready
	RecordNewHead  // a change layer's record, now Change, is at another head
	RecordChanged  // a change layer's record, now Change, changed otherwise
	BranchUnread   // a plain branch could not be read against origin's records
	BecameChange   // a plain branch is now the branch of Change
)

// Refusal is why a family cannot be rebased whole. The step that returns
// one has changed nothing on origin.
type Refusal struct {
	Kind  Kind
	Name  string // Gone and StartMissing: the start's handle or branch
	Layer *Layer // the layer it is about
	Other *Layer // SharedBranch: the layer below on the same branch

	// The change it is about, other than the layer itself, and the
	// version concerned.
	Change  *Recorded
	Version change.Version

	Handle string // the handle of Change

	Commit string   // HeadMoved, Merge, ForeignHead: the commit concerned
	Paths  []string // Conflict: every path that conflicted, in git's order
	Below  string   // Conflict and EmptyLayer: what the layer was rebased onto, by name
}

func (r *Refusal) Error() string {
	l := r.Layer
	switch r.Kind {
	case Gone:
		return "origin no longer holds a record of change " + r.Name
	case StartMissing:
		return "no such branch on origin: " + r.Name
	case NoBranch:
		return fmt.Sprintf("change %s v%d records no branch, so the rebase has no branch to push it to; nothing rebased", l.Name, l.Version)
	case BranchMissing:
		return fmt.Sprintf("%s is on the branch %s, and origin has no such branch; nothing rebased", l.Name, l.Branch)
	case NotReady:
		return fmt.Sprintf("change %s v%d is %s, not ready; nothing rebased", l.Name, l.Version, l.Record.State)
	case HeadMoved:
		return fmt.Sprintf("%s is at %s, and the head given was %s; nothing rebased", l.Name, l.Head, r.Commit)
	case TipMoved:
		return fmt.Sprintf("change %s v%d records the head %s, and origin/%s is at %s; nothing rebased", l.Name, l.Version, l.Head, l.Branch, l.Tip)
	case BranchOfChange:
		return fmt.Sprintf("change %s v%d records the head %s, and origin/%s is at %s; nothing rebased", r.Handle, r.Version.N, HeadOf(r.Version), l.Branch, l.Tip)
	case SharedBranch:
		return fmt.Sprintf("%s and %s are both on the branch %s; nothing rebased", r.Other.Name, l.Name, l.Branch)
	case StackUnread:
		return fmt.Sprintf("could not read the commits under %s; nothing rebased", l.Name)
	case LayerUnread:
		return fmt.Sprintf("could not read the commits of %s; nothing rebased", l.Name)
	case Merge:
		return fmt.Sprintf("the commits under %s include the merge %s, and a rebase moves a line of commits; nothing rebased", l.Name, short(r.Commit))
	case ForeignHead:
		h, why := r.Handle, "which is no layer of this stack"
		if r.Change.Record.State != change.Ready {
			why = fmt.Sprintf("which is %s, not ready", r.Change.Record.State)
		}
		return fmt.Sprintf("the commit %s under %s is v%d of change %s, %s; nothing rebased. A rebase gives each commit a new ID, which no version of %s would record",
			short(r.Commit), l.Name, r.Version.N, h, why, h)
	case NoHistory:
		return fmt.Sprintf("%s and %s share no history; nothing rebased", l.Name, Target)
	case NoWorktree:
		return "could not make a directory for the rebase; nothing changed"
	case NoCheckout:
		return fmt.Sprintf("could not check out origin/%s for the rebase; nothing changed", l.Branch)
	case Conflict:
		return fmt.Sprintf("%s conflicts with %s; nothing changed", l.Name, r.Below)
	case NoNewHead:
		return fmt.Sprintf("could not read the rebased head of %s; nothing pushed", l.Branch)
	case EmptyLayer:
		return fmt.Sprintf("%s adds nothing to %s once rebased, as when its commits have landed already; nothing pushed", l.Name, r.Below)
	case OriginUnread:
		return "could not read origin again before the push; nothing pushed"
	case RecordGone:
		return fmt.Sprintf("origin no longer holds a record of change %s; nothing pushed", l.Name)
	case RecordUnread:
		return fmt.Sprintf("could not read the record of change %s again; nothing pushed", l.Name)
	case RecordNotReady:
		v := r.Change.Record.ActiveVersion()
		return fmt.Sprintf("change %s v%d is %s, not ready; nothing pushed", l.Name, v.N, r.Change.Record.State)
	case RecordNewHead:
		v := r.Change.Record.ActiveVersion()
		return fmt.Sprintf("change %s is at v%d, %s, and the rebase began at v%d, %s; nothing pushed", l.Name, v.N, HeadOf(v), l.Version, l.Head)
	case RecordChanged:
		return fmt.Sprintf("the record of change %s changed while it was rebased; nothing pushed", l.Name)
	case BranchUnread:
		return fmt.Sprintf("could not read origin/%s again against origin's records; nothing pushed", l.Branch)
	case BecameChange:
		return fmt.Sprintf("origin/%s is now change %s; nothing pushed", l.Branch, r.Handle)
	}
	return fmt.Sprintf("refusal %d", r.Kind)
}

// about is a refusal about the change n, other than its layer.
func (s *Snapshot) about(r *Refusal, n Recorded) *Refusal {
	r.Change, r.Handle = &Recorded{ID: n.ID, Commit: n.Commit, Record: n.Record}, s.Handle(n.ID)
	return r
}

// Refusals are several refusals found together: Check reads every layer's
// branch before it refuses those not at their changes' heads.
type Refusals []*Refusal

func (rs Refusals) Error() string {
	var words []string
	for _, r := range rs {
		words = append(words, r.Error())
	}
	return strings.Join(words, "\n")
}
