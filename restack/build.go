package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"the-valley/change"
	"the-valley/rebase"
)

// Building a pass's writes. For each family the planner would restack,
// the family is prepared in a throwaway worktree, and its writes are built
// in the scratch repository: the one atomic branch push, each change's
// next version, lower layers first, and each recorded change's report. A
// family the planner holds gets only its reports, which say blocked. A
// family whose rebase conflicts, or that the engine refuses to prepare,
// gets reports that say conflict or blocked, and moves nothing. Each
// version is built from the record the capture read, so every write is
// built before any push, and all of them can be kept in the journal first
// (keep).
//
// A report that says the same as the change's current report on origin,
// apart from the operation that wrote it, is not built again (sameReport).
//
// Building pushes nothing. What it built names its operation, the
// attempt's local name, which every report it built carries.

// Writes is what a pass would push for one family, as the plan shows it.
type Writes struct {
	Result   string         `json:"result"` // what its reports say: complete, conflict or blocked
	Branches []BranchWrite  `json:"branches"`
	Versions []VersionWrite `json:"versions"`
	Reports  []ReportWrite  `json:"reports"`
}

// BranchWrite is one branch the family's atomic push would move.
type BranchWrite struct {
	Branch string `json:"branch"`
	Old    string `json:"old"` // its tip as captured, which the push is leased on
	New    string `json:"new"` // its prepared head
}

// VersionWrite is one version a pass would add.
type VersionWrite struct {
	ID      string `json:"change_id"`
	Version int    `json:"version"`
	Head    string `json:"head"`
	Record  string `json:"record"` // the record commit built
	Lease   string `json:"lease"`  // the record commit it replaces, which the push is leased on
}

// ReportWrite is one change's report.
type ReportWrite struct {
	ID        string   `json:"change_id"`
	Result    string   `json:"result"`
	Commit    string   `json:"commit,omitempty"` // the report commit built; "" when none was
	Lease     string   `json:"lease,omitempty"`  // the report it replaces; "" for a change with none
	Omitted   []string `json:"omitted"`          // what the report leaves out of the observation
	Unchanged bool     `json:"unchanged"`        // the current report says the same, so none was built
	Error     string   `json:"error,omitempty"`  // why no report could be built
}

// built is what building a pass's writes made: the journal of them, and
// the prepared families whose heads it keeps reachable until released.
type built struct {
	journal  *Journal
	prepared []*rebase.Prepared
}

// releasePrepared lets go of the refs that keep the prepared heads.
func (b *built) releasePrepared(ctx context.Context, g rebase.Git) {
	for _, p := range b.prepared {
		p.Release(ctx, g)
	}
	b.prepared = nil
}

// newOperation names one attempt: when it started, to the second, and a
// random suffix, in the shape a report's operation takes.
func newOperation(now time.Time) string {
	var token [4]byte
	rand.Read(token[:])
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(token[:])
}

// build prepares and builds the writes of every family the plan would
// restack or holds, and sets each such family's Writes. A conflict or a
// refusal is a family's result; an error is a pass that cannot go on, as
// when its context is done.
func build(ctx context.Context, g rebase.Git, s *rebase.Snapshot, p *Plan, b binding, op string) (*built, error) {
	out := &built{journal: &Journal{
		Schema: journalSchema, Repository: b.Repository, Target: b.Target, Principal: b.Principal,
		Operation: op, Tip: p.Tip, Evidence: p.Evidence, Families: []JournalFamily{},
	}}
	for i := range p.Families {
		fp := &p.Families[i]
		if fp.Action != restack && fp.Action != hold {
			continue
		}
		jf, w, err := out.family(ctx, g, s, p.families[i], *fp, b.Principal, op)
		if err != nil {
			out.releasePrepared(ctx, g)
			return nil, err
		}
		fp.Writes = w
		if jf.Branches != nil || len(jf.Reports) > 0 {
			out.journal.Families = append(out.journal.Families, *jf)
		}
	}
	return out, nil
}

// family builds one family's writes.
func (out *built) family(ctx context.Context, g rebase.Git, s *rebase.Snapshot, f *rebase.Family, fp FamilyPlan, principal, op string) (*JournalFamily, *Writes, error) {
	o := observe(f, fp)
	jf := &JournalFamily{Versions: []*rebase.PendingVersion{}, Reports: []JournalReport{}}
	published := map[string]*change.RestackPublished{}
	switch fp.Action {
	case hold:
		o.Result, o.Refusal = change.RestackBlocked, strings.Join(fp.Reasons, "; ")
	case restack:
		prep, err := rebase.Prepare(ctx, g, f)
		var one *rebase.Refusal
		var many rebase.Refusals
		switch {
		case err == nil:
			versions, err := prep.BuildAppends(ctx, g, principal)
			if err != nil {
				prep.Release(ctx, g)
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				o.Result, o.Refusal = change.RestackBlocked, err.Error()
				break
			}
			out.prepared = append(out.prepared, prep)
			o.Result, jf.Branches, jf.Versions = change.RestackComplete, prep.Branches(), versions
			for _, v := range versions {
				n, record := v.Version, change.HeadPrefix+v.Commit
				published[v.ID] = &change.RestackPublished{Head: change.HeadPrefix + v.Head, Version: &n, Record: &record}
			}
		case errors.As(err, &one) && one.Kind == rebase.Conflict:
			o.Result, o.Failing, o.Refusal = change.RestackConflict, one.Layer.ID, one.Error()
			o.Conflicts = append([]string{}, one.Paths...)
		case errors.As(err, &one) || errors.As(err, &many):
			o.Result, o.Refusal = change.RestackBlocked, err.Error()
			if one != nil && one.Layer != nil {
				o.Failing = one.Layer.ID
			}
		default:
			return nil, nil, err
		}
	}
	jf.Observed = o

	w := &Writes{Result: o.Result, Branches: []BranchWrite{}, Versions: []VersionWrite{}, Reports: []ReportWrite{}}
	if jf.Branches != nil {
		for _, u := range jf.Branches.Updates {
			w.Branches = append(w.Branches, BranchWrite(u))
		}
	}
	for _, v := range jf.Versions {
		w.Versions = append(w.Versions, VersionWrite{ID: v.ID, Version: v.Version, Head: v.Head, Record: v.Commit, Lease: v.Old})
	}
	digest := o.Digest()
	for _, l := range f.Layers {
		if l.ID == "" {
			continue
		}
		rw := ReportWrite{ID: l.ID, Result: o.Result, Omitted: []string{}}
		r, omitted, err := bound(fullReport(op, s.Tip, l.ID, o, published), digest)
		if err != nil {
			rw.Error = err.Error()
			w.Reports = append(w.Reports, rw)
			continue
		}
		rw.Omitted = append(rw.Omitted, omitted...)
		if current, found := s.Report(l.ID); found && sameReport(current.Report, r) {
			rw.Unchanged = true
			w.Reports = append(w.Reports, rw)
			continue
		}
		old := s.Refs[rebase.RestackPrefix+l.ID]
		pending, err := rebase.BuildReport(ctx, g, r, rebase.ReportMessage(l.Name, r), old)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			rw.Error = err.Error()
			w.Reports = append(w.Reports, rw)
			continue
		}
		rw.Commit, rw.Lease = pending.Commit, old
		jf.Reports = append(jf.Reports, JournalReport{Pending: pending, Omitted: rw.Omitted})
		w.Reports = append(w.Reports, rw)
	}
	return jf, w, nil
}
