// Keeping a write between building it and pushing it.
//
// A record commit or a report commit is written before it is pushed, so a
// caller can keep the write somewhere durable first. After a crash, the
// caller restores it and compares origin's ref with it: at Commit, origin
// took the push; at Old, it did not, and the write may be pushed again.
// A prepared family's branch push is kept the same way, as each branch's
// old tip and prepared head, keyed by the branch's name.
//
// A kept write restores to the same write, or not at all. Its form names
// everything its push checks, and a version's mode is part of that: a
// restack append restored as a manual one would push without reading the
// branch again. MarshalJSON writes the form. UnmarshalJSON restores only
// a form MarshalJSON could have written, and refuses any other: one with
// a field missing, unknown or named twice, and one that does not agree
// with itself, such as a version whose record names another head.

package rebase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"the-valley/change"
)

// The schemas of the forms a write is kept in.
const (
	pendingVersionSchema  = "valley-pending-version/v1"
	pendingReportSchema   = "valley-pending-report/v1"
	pendingBranchesSchema = "valley-pending-branches/v1"
)

// The modes a version is built in. The mode decides what its push checks.
const (
	manualMode  = "manual"  // BuildVersion's: the push is leased on the record ref
	restackMode = "restack" // BuildRestack's: the push also needs origin's branch still at Head
)

// versionForm is the form a PendingVersion is kept in.
type versionForm struct {
	Schema  string         `json:"schema"`
	Mode    string         `json:"mode"`
	ID      string         `json:"change_id"`
	Handle  string         `json:"handle"`
	Ref     string         `json:"ref"`
	Old     string         `json:"old"`
	Commit  string         `json:"commit"`
	Version int            `json:"version"`
	Head    string         `json:"head"`
	Branch  string         `json:"branch"`
	Record  *change.Record `json:"record"`
}

// MarshalJSON writes p in the form it is kept in. It refuses a p that
// UnmarshalJSON would refuse, so what it writes always restores.
func (p PendingVersion) MarshalJSON() ([]byte, error) {
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("the pending version cannot be kept: %w", err)
	}
	mode := manualMode
	if p.restack {
		mode = restackMode
	}
	return json.Marshal(versionForm{
		Schema: pendingVersionSchema, Mode: mode, ID: p.ID, Handle: p.Handle, Ref: p.Ref, Old: p.Old,
		Commit: p.Commit, Version: p.Version, Head: p.Head, Branch: p.Branch, Record: p.Record,
	})
}

// UnmarshalJSON restores a PendingVersion that MarshalJSON wrote, in the
// mode it was built in.
func (p *PendingVersion) UnmarshalJSON(data []byte) error {
	var f versionForm
	if err := restore(data, &f); err != nil {
		return fmt.Errorf("the pending version does not restore: %w", err)
	}
	q := PendingVersion{
		ID: f.ID, Handle: f.Handle, Ref: f.Ref, Old: f.Old, Commit: f.Commit,
		Record: f.Record, Version: f.Version, Head: f.Head, Branch: f.Branch, restack: f.Mode == restackMode,
	}
	switch {
	case f.Schema != pendingVersionSchema:
		return fmt.Errorf("the pending version does not restore: its schema is %q, where %q is expected", f.Schema, pendingVersionSchema)
	case f.Mode != manualMode && f.Mode != restackMode:
		return fmt.Errorf("the pending version does not restore: its mode is %q, where %q or %q is expected", f.Mode, manualMode, restackMode)
	}
	if err := q.check(); err != nil {
		return fmt.Errorf("the pending version does not restore: %w", err)
	}
	*p = q
	return nil
}

// check refuses a pending version that does not agree with itself. Its
// record must be the change's, ready at the version it names as both its
// active and its last, at its head and on its branch. A version is only
// ever added to a record that exists, so Old names a commit.
func (p *PendingVersion) check() error {
	switch {
	case !change.ValidID(p.ID):
		return fmt.Errorf("%q is not a change ID", p.ID)
	case len(p.Handle) < change.MinHandle || !strings.HasPrefix(p.ID, p.Handle):
		return fmt.Errorf("%q is not a handle of %s", p.Handle, p.ID)
	case p.Ref != change.Ref(Target, p.ID):
		return fmt.Errorf("its ref %q is not the record ref of %s", p.Ref, p.ID)
	case !IsObjectID(p.Old) || !IsObjectID(p.Commit) || !IsObjectID(p.Head):
		return errors.New("the record it read, the record commit and the head are not each a full object ID")
	case !change.ValidBranch(p.Branch):
		return fmt.Errorf("%q is not a branch name", p.Branch)
	case p.Record == nil:
		return errors.New("it holds no record")
	}
	if err := p.Record.Check(); err != nil {
		return fmt.Errorf("its record does not read: %w", err)
	}
	r, v := p.Record, p.Record.ActiveVersion()
	if r.ChangeID != p.ID || r.Target != Target || r.State != change.Ready || r.Active != len(r.Versions) ||
		v.N != p.Version || HeadOf(v) != p.Head || v.Branch != p.Branch {
		return fmt.Errorf("its record does not hold %s ready at v%d, its last version, at %s on %s", p.ID, p.Version, short(p.Head), p.Branch)
	}
	return nil
}

// reportForm is the form a PendingReport is kept in.
type reportForm struct {
	Schema string                `json:"schema"`
	ID     string                `json:"change_id"`
	Ref    string                `json:"ref"`
	Old    string                `json:"old"`
	Commit string                `json:"commit"`
	Report *change.RestackReport `json:"report"`
}

// MarshalJSON writes p in the form it is kept in. It refuses a p that
// UnmarshalJSON would refuse, so what it writes always restores.
func (p PendingReport) MarshalJSON() ([]byte, error) {
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("the pending report cannot be kept: %w", err)
	}
	return json.Marshal(reportForm{Schema: pendingReportSchema, ID: p.ID, Ref: p.Ref, Old: p.Old, Commit: p.Commit, Report: p.Report})
}

// UnmarshalJSON restores a PendingReport that MarshalJSON wrote.
func (p *PendingReport) UnmarshalJSON(data []byte) error {
	var f reportForm
	if err := restore(data, &f); err != nil {
		return fmt.Errorf("the pending report does not restore: %w", err)
	}
	if f.Schema != pendingReportSchema {
		return fmt.Errorf("the pending report does not restore: its schema is %q, where %q is expected", f.Schema, pendingReportSchema)
	}
	q := PendingReport{ID: f.ID, Ref: f.Ref, Old: f.Old, Commit: f.Commit, Report: f.Report}
	if err := q.check(); err != nil {
		return fmt.Errorf("the pending report does not restore: %w", err)
	}
	*p = q
	return nil
}

// check refuses a pending report that does not agree with itself: its
// report must read, and be about the change whose report ref it names.
// Old is "" for a change that had no report.
func (p *PendingReport) check() error {
	switch {
	case !change.ValidID(p.ID):
		return fmt.Errorf("%q is not a change ID", p.ID)
	case p.Ref != RestackPrefix+p.ID:
		return fmt.Errorf("its ref %q is not the report ref of %s", p.Ref, p.ID)
	case p.Old != "" && !IsObjectID(p.Old) || !IsObjectID(p.Commit):
		return errors.New("the report it replaces and the report commit are not each a full object ID")
	case p.Report == nil:
		return errors.New("it holds no report")
	}
	if err := p.Report.Check(); err != nil {
		return fmt.Errorf("its report does not read: %w", err)
	}
	if p.Report.ChangeID != p.ID {
		return fmt.Errorf("its report is about %s, not %s", p.Report.ChangeID, p.ID)
	}
	return nil
}

// PendingBranches is a prepared family's branch push, kept: the target tip
// the family was rebased onto, and each branch the one atomic push moves,
// from the tip the capture read, which the push is leased on, to its
// prepared head. It names branches, never a layer in memory, so it
// restores whole after a crash. Origin holding every New then means the
// push was taken, and holding every Old that it was not.
type PendingBranches struct {
	Tip     string         // the target tip the family was rebased onto
	Updates []BranchUpdate // bottom first, one for each layer
}

// BranchUpdate is one branch a family's push moves.
type BranchUpdate struct {
	Branch string // the branch
	Old    string // its tip as captured, which the push is leased on
	New    string // the prepared head the push moves it to
}

// Branches is the prepared family's branch push, kept.
func (p *Prepared) Branches() *PendingBranches {
	b := &PendingBranches{Tip: p.Family.Tip}
	for _, l := range p.Family.Layers {
		b.Updates = append(b.Updates, BranchUpdate{Branch: l.Branch, Old: l.Tip, New: p.Heads[l]})
	}
	return b
}

// Push moves every branch at once, each leased on its Old: either every
// branch moves or none does. It does not read origin first; Publish does.
func (b *PendingBranches) Push(ctx context.Context, g Git) (Push, []RefusedRef) {
	args := []string{"--atomic"}
	for _, u := range b.Updates {
		args = append(args, "--force-with-lease=refs/heads/"+u.Branch+":"+u.Old)
	}
	args = append(args, "origin")
	for _, u := range b.Updates {
		args = append(args, u.New+":refs/heads/"+u.Branch)
	}
	return push(ctx, g, args...)
}

// branchesForm is the form a PendingBranches is kept in.
type branchesForm struct {
	Schema  string       `json:"schema"`
	Tip     string       `json:"tip"`
	Updates []updateForm `json:"updates"`
}

type updateForm struct {
	Branch string `json:"branch"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

// MarshalJSON writes b in the form it is kept in. It refuses a b that
// UnmarshalJSON would refuse, so what it writes always restores.
func (b PendingBranches) MarshalJSON() ([]byte, error) {
	if err := b.check(); err != nil {
		return nil, fmt.Errorf("the pending branch push cannot be kept: %w", err)
	}
	f := branchesForm{Schema: pendingBranchesSchema, Tip: b.Tip, Updates: []updateForm{}}
	for _, u := range b.Updates {
		f.Updates = append(f.Updates, updateForm(u))
	}
	return json.Marshal(f)
}

// UnmarshalJSON restores a PendingBranches that MarshalJSON wrote.
func (b *PendingBranches) UnmarshalJSON(data []byte) error {
	var f branchesForm
	if err := restore(data, &f); err != nil {
		return fmt.Errorf("the pending branch push does not restore: %w", err)
	}
	if f.Schema != pendingBranchesSchema {
		return fmt.Errorf("the pending branch push does not restore: its schema is %q, where %q is expected", f.Schema, pendingBranchesSchema)
	}
	q := PendingBranches{Tip: f.Tip}
	for _, u := range f.Updates {
		q.Updates = append(q.Updates, BranchUpdate(u))
	}
	if err := q.check(); err != nil {
		return fmt.Errorf("the pending branch push does not restore: %w", err)
	}
	*b = q
	return nil
}

// check refuses a branch push that does not agree with itself: it moves at
// least one branch, no branch twice, and each from one full object ID to
// another.
func (b *PendingBranches) check() error {
	if !IsObjectID(b.Tip) {
		return fmt.Errorf("its tip %q is not a full object ID", b.Tip)
	}
	if len(b.Updates) == 0 {
		return errors.New("it moves no branch")
	}
	seen := map[string]bool{}
	for _, u := range b.Updates {
		switch {
		case !change.ValidBranch(u.Branch):
			return fmt.Errorf("%q is not a branch name", u.Branch)
		case seen[u.Branch]:
			return fmt.Errorf("it moves %s twice", u.Branch)
		case !IsObjectID(u.Old) || !IsObjectID(u.New):
			return fmt.Errorf("the old tip and the new head of %s are not each a full object ID", u.Branch)
		case u.Old == u.New:
			return fmt.Errorf("it moves %s to the tip it is at", u.Branch)
		}
		seen[u.Branch] = true
	}
	return nil
}

// restore reads data into form, and takes only what json.Marshal writes
// of form. Space between tokens is all that may differ, so a form kept
// inside an indented file still restores. Anything else is refused: a
// field missing, unknown or named twice, a value written another way, or
// anything after the form.
func restore(data []byte, form any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(form); err != nil {
		return err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return err
	}
	again, err := json.Marshal(form)
	if err != nil {
		return err
	}
	if !bytes.Equal(compact.Bytes(), again) {
		return errors.New("it is not in the form it is kept in: a field is missing or named twice, or a value is written another way")
	}
	return nil
}
