// Keeping a write between building it and pushing it.
//
// A record commit or a report commit is written before it is pushed, so a
// caller can keep the write somewhere durable first. After a crash, the
// caller restores it and compares origin's ref with it: at Commit, origin
// took the push; at Old, it did not, and the write may be pushed again.
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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"the-valley/change"
)

// The schemas of the forms a write is kept in.
const (
	pendingVersionSchema = "valley-pending-version/v1"
	pendingReportSchema  = "valley-pending-report/v1"
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
