// Publishing a prepared family, in two phases:
//
//  1. One atomic push moves every layer's branch, each leased on the tip
//     the capture read. Either every branch moves or none does.
//  2. Lower layers first, each change's prepared head is added to its
//     record as a new version, as `valley ready --if-active` adds one:
//     only while the change is still ready at the head the capture read.
//     Each change is appended on its own, and a failure for one does not
//     stop the others. The change may have been unreadied meanwhile, its
//     record may have moved, or the hook may refuse the write.
//
// Origin is read once more just before the push. Each change layer's
// record must still be the record commit the capture read, and each plain
// branch must still be a branch no record is on. That is the last check,
// and a window remains between it and the push. A change unreadied or
// withdrawn in that window, or a branch readied, has its branch moved all
// the same. git compares only the refs a push changes, and the record refs
// are not among them, so no client can make the push conditional on a
// record. Phase 2 then adds each version only while the change is still
// ready at the captured head, under a lease on the record, so a change
// unreadied in that window stays a draft at its old head, its result says
// so, and the integrator lands nothing for it.

package rebase

import (
	"context"
	"strings"

	"the-valley/change"
)

// Result is what became of one layer of a published family.
type Result struct {
	Layer    *Layer
	Head     string    // its prepared head
	Branch   Push      // what became of its branch in phase 1
	Appended *Appended // what became of its record in phase 2; nil for a plain branch, or until appended
}

// Unfinished reports whether the layer is a change whose branch may hold
// its prepared head with no version recording it.
func (r *Result) Unfinished() bool {
	moved := r.Branch == Pushed || r.Branch == Unknown
	return r.Layer.ID != "" && moved && (r.Appended == nil || r.Appended.Version == 0)
}

// Publication is what publishing a family did, layer by layer.
type Publication struct {
	Family  *Family
	Results []*Result    // as Family.Layers, bottom first
	Push    Push         // what became of the branch push, which every branch shares
	Refused []RefusedRef // each branch origin refused, in git's order, when it refused the push
}

// Result is the result for a layer of the family.
func (p *Publication) Result(l *Layer) *Result {
	for _, r := range p.Results {
		if r.Layer == l {
			return r
		}
	}
	return nil
}

// Publish is phase 1. It reads origin again, refusing a family whose
// records or plain branches have moved since the capture, and then pushes
// every layer's branch at once. A refusal, or a context already done,
// pushes nothing and returns an error. Otherwise the publication says
// whether the branches moved, did not, or may have.
func Publish(ctx context.Context, g Git, p *Prepared) (*Publication, error) {
	f := p.Family
	pub := &Publication{Family: f}
	for _, l := range f.Layers {
		pub.Results = append(pub.Results, &Result{Layer: l, Head: p.Heads[l]})
	}
	if err := ctx.Err(); err != nil {
		return pub, err
	}
	if err := revalidate(ctx, g, f); err != nil {
		return pub, err
	}
	args := []string{"--atomic"}
	for _, l := range f.Layers {
		args = append(args, "--force-with-lease=refs/heads/"+l.Branch+":"+l.Tip)
	}
	args = append(args, "origin")
	for _, l := range f.Layers {
		args = append(args, p.Heads[l]+":refs/heads/"+l.Branch)
	}
	pub.Push, pub.Refused = push(ctx, g, args...)
	for _, r := range pub.Results {
		r.Branch = pub.Push
	}
	if pub.Push == Unpushed {
		return pub, ctx.Err()
	}
	return pub, nil
}

// revalidate reads origin once more, just before the push: each change
// layer's record must still be the record commit the capture read, and
// each plain branch must still be a branch no record is on.
func revalidate(ctx context.Context, g Git, f *Family) error {
	o, err := ListOrigin(ctx, g, InventoryPatterns...)
	if err != nil {
		return &Refusal{Kind: OriginUnread}
	}
	for _, l := range f.Layers {
		if l.ID != "" {
			if now := o.Refs[l.Ref]; now != l.Commit {
				return recordMoved(ctx, g, l, now)
			}
			continue
		}
		found, err := UnlandedOn(ctx, g, o, l.Branch, f.s.verify)
		if err != nil {
			return &Refusal{Kind: BranchUnread, Layer: l}
		}
		if len(found) > 0 {
			r := &Refusal{Kind: BecameChange, Layer: l}
			r.Change, r.Handle = &found[0], o.Handle(found[0].ID)
			return r
		}
	}
	return nil
}

// recordMoved is the refusal for a change layer whose record is no longer
// the record commit the capture read.
func recordMoved(ctx context.Context, g Git, l *Layer, now string) error {
	if now == "" {
		return &Refusal{Kind: RecordGone, Layer: l}
	}
	if err := Fetch(ctx, g, now); err != nil {
		return &Refusal{Kind: RecordUnread, Layer: l}
	}
	rec, err := ReadRecord(ctx, g, now)
	if err != nil {
		return &Refusal{Kind: RecordUnread, Layer: l}
	}
	r := &Refusal{Layer: l, Change: &Recorded{ID: l.ID, Commit: now, Record: rec}, Handle: l.Name}
	switch v := rec.ActiveVersion(); {
	case rec.State != change.Ready:
		r.Kind = RecordNotReady
	case HeadOf(v) != l.Head:
		r.Kind = RecordNewHead
	default:
		r.Kind = RecordChanged
	}
	return r
}

// UnlandedOn is every change origin holds a record of whose active
// version names branch, less each whose active version has landed. o must
// list InventoryPatterns. Landing is read only for a change on the
// branch, so a name no record is on costs no more than reading the
// records.
//
// A landed change is left out wherever the branch has moved since. The
// branch then holds new work, and the name is free for it. A landing read
// wrongly could make a draft's branch read as a plain branch, but nothing
// lands from that: the integrator refuses any request whose head is a
// version of a change that is not ready, whatever the request is named.
func UnlandedOn(ctx context.Context, g Git, o Origin, branch string, v Verifier) ([]Recorded, error) {
	all, _, err := ReadAll(ctx, g, o)
	if err != nil {
		return nil, err
	}
	var on []Recorded
	for _, n := range all {
		if n.Record.ActiveVersion().Branch == branch {
			on = append(on, n)
		}
	}
	if len(on) == 0 {
		return nil, nil
	}
	var want []string
	for ref, oid := range o.Refs {
		if ref == "refs/heads/"+Target || strings.HasPrefix(ref, OutcomePrefix) {
			want = append(want, oid)
		}
	}
	if err := Fetch(ctx, g, want...); err != nil {
		return nil, err
	}
	s := NewSnapshot(ctx, g, o, all, v)
	var found []Recorded
	for _, n := range on {
		if !s.IsLanded(n.ID, n.Record.ActiveVersion()) {
			found = append(found, n)
		}
	}
	return found, nil
}

// Append is phase 2 for one layer: a change whose branch moved gains its
// prepared head as a new version, while it is still ready at the head the
// capture read, with the principal as its uploader. It does nothing for a
// plain branch, or for a layer whose branch did not move. A caller that
// appends layer by layer appends lower layers first.
func (p *Publication) Append(ctx context.Context, g Git, principal string, r *Result) {
	l := r.Layer
	if l.ID == "" || r.Branch != Pushed {
		return
	}
	ap := AppendVersion(ctx, g, Append{ID: l.ID, Branch: l.Branch, Head: r.Head, IfActive: l.Head, Principal: principal})
	r.Appended = &ap
}

// AppendAll is phase 2 for every layer, lower layers first. A context done
// part way leaves the remaining layers unappended.
func (p *Publication) AppendAll(ctx context.Context, g Git, principal string) {
	for _, r := range p.Results {
		if ctx.Err() != nil {
			return
		}
		p.Append(ctx, g, principal, r)
	}
}
