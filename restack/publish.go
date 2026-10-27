package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"the-valley/change"
	"the-valley/rebase"
)

// Publishing. A write pass makes the writes its journal names the way
// recovery would make them after a crash: it reads origin, decides every
// write (recover.go), and pushes the ones still to finish, one phase of
// each family at a time. A family's branch push comes first. Origin is
// read again, so its versions are decided against the branches as origin
// holds them and against the requests filed meanwhile. Its versions come
// next, lower layers first, and its reports last, each after another
// read. So a pass that ends anywhere leaves a journal the next pass
// finishes in the same way, and a push whose answer was lost is decided
// by what origin shows on the next read.
//
// A pass lets go of the journal once no family in it is open. A family is
// open while its branch push is still to finish, or while origin took its
// branch push and a version of it is neither added nor stopped for good,
// as a version paused by a request that might still land is. Until then
// the pass starts no new operation, since the journal holds one operation
// at a time. A family whose branch push stopped, and so moved nothing, is
// left for the next plan.
//
// A family whose branches moved gets reports that say what did happen.
// complete is every version added. partial is a version not added: one
// that stopped, or one that is paused, which the report says while the
// family is open and replaces once it is not. Such a report is built from
// the journal's observation and origin as read, and is not journaled: a
// pass that ends before pushing it builds it again from the same journal.
// A report that says what origin's current report says is not pushed.

// maxSteps bounds how many times one pass reads origin and pushes what is
// still to finish. A family takes three: its branches, its versions and its
// reports. A write still to finish after the last is left to the next pass.
const maxSteps = 6

// Action is one write a pass pushed, and what became of it.
type Action struct {
	Operation string `json:"operation"`
	Family    int    `json:"family"`              // the family's place in the journal
	Write     string `json:"write"`               // branches, version or report
	ID        string `json:"change_id,omitempty"` // the change a version or a report is about
	Commit    string `json:"commit,omitempty"`    // the record or report commit pushed
	Result    string `json:"result"`              // pushed, refused, unknown or unpushed
	Why       string `json:"why,omitempty"`       // why origin did not take it, when it did not
}

// settled is what settling a journal did.
type settled struct {
	actions []Action
	last    *Recovery // what recovery decided of the journal at the last read
	closed  bool      // the journal was let go
}

// settle makes the journal's writes as recovery decides them, and lets go
// of the journal once no family in it is open.
func settle(ctx context.Context, g rebase.Git, c config, j *Journal) (*settled, error) {
	out := &settled{actions: []Action{}}
	for step := 0; step < maxSteps; step++ {
		rd, err := readOrigin(ctx, g, c)
		if err != nil {
			return out, err
		}
		out.last = decide(j, rd)
		if pushed(ctx, g, j, out.last, &out.actions) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := reportWhatHappened(ctx, g, j, out.last, rd.s, &out.actions); err != nil {
			return out, err
		}
		if open(j, out.last) {
			return out, nil
		}
		if err := release(ctx, g, j.Operation); err != nil {
			return out, err
		}
		if err := forget(c.state); err != nil {
			return out, err
		}
		out.closed = true
		return out, nil
	}
	return out, nil
}

// readOrigin reads one snapshot of origin, and what recovery reads it
// through.
func readOrigin(ctx context.Context, g rebase.Git, c config) (reading, error) {
	s, err := rebase.ReadSnapshot(ctx, g, c.verify)
	if err != nil {
		return reading{}, err
	}
	requests, evidence := requestsOf(ctx, g, s)
	return reading{
		s: s, requests: requests, evidence: evidence, in: incoming(ctx, g, s.Tip), is: ancestry(ctx, g),
		have:   func(oid string) bool { return run(ctx, g, "cat-file", "-e", oid) == nil },
		landed: s.IsLanded,
	}, nil
}

// decisionsOf is each family's decisions, by write and change ID.
func decisionsOf(r *Recovery) map[int]map[string]Decision {
	by := map[int]map[string]Decision{}
	for _, d := range r.Decisions {
		if by[d.Family] == nil {
			by[d.Family] = map[string]Decision{}
		}
		by[d.Family][d.Write+" "+d.ID] = d
	}
	return by
}

// pushed pushes, for each family, the first phase that has a write still
// to finish: its branch push, then its versions, then its reports. It
// reports whether it tried any push.
func pushed(ctx context.Context, g rebase.Git, j *Journal, r *Recovery, actions *[]Action) bool {
	tried := false
	by := decisionsOf(r)
	record := func(a Action) {
		a.Operation = j.Operation
		*actions = append(*actions, a)
		tried = true
	}
	for i, f := range j.Families {
		ds := by[i]
		if ctx.Err() != nil {
			return tried
		}
		if f.Branches != nil && ds["branches "].Action == writeFinish {
			push, refused := f.Branches.Push(ctx, g)
			a := Action{Family: i, Write: "branches", Result: push.String()}
			var why []string
			for _, ref := range refused {
				why = append(why, ref.Ref+": "+ref.Why)
			}
			a.Why = strings.Join(why, "; ")
			record(a)
			continue
		}
		versions := false
		for _, v := range f.Versions {
			if ds["version "+v.ID].Action != writeFinish {
				continue
			}
			ap := v.Push(ctx, g)
			a := Action{Family: i, Write: "version", ID: v.ID, Commit: v.Commit, Result: ap.Write.Push.String(), Why: ap.Write.Why}
			if ap.Err != nil && a.Why == "" {
				a.Why = ap.Err.Error()
			}
			record(a)
			versions = true
		}
		if versions {
			continue
		}
		for _, rep := range f.Reports {
			p := rep.Pending
			if ds["report "+p.ID].Action != writeFinish {
				continue
			}
			w := p.Push(ctx, g)
			record(Action{Family: i, Write: "report", ID: p.ID, Commit: p.Commit, Result: w.Push.String(), Why: w.Why})
		}
	}
	return tried
}

// moved reports whether origin took a family's branch push.
func moved(f JournalFamily, ds map[string]Decision) bool {
	return f.Branches != nil && ds["branches "].Action == writeDone
}

// open reports whether any family of the journal is open: its branch push
// still to finish, or its branches moved and a version neither added nor
// stopped for good.
func open(j *Journal, r *Recovery) bool {
	by := decisionsOf(r)
	for i, f := range j.Families {
		ds := by[i]
		if f.Branches != nil && ds["branches "].Action == writeFinish {
			return true
		}
		if !moved(f, ds) {
			continue
		}
		for _, v := range f.Versions {
			if !ds["version "+v.ID].settled() {
				return true
			}
		}
	}
	return false
}

// reportWhatHappened pushes, for each family whose branches moved and
// whose journaled reports are not all on origin, a report for each of its
// changes that says what did happen. It builds each from the family's
// observation: published at its prepared head, with the version when
// origin holds it. A change origin no longer records gets none, since the
// hook takes no report about it.
func reportWhatHappened(ctx context.Context, g rebase.Git, j *Journal, r *Recovery, s *rebase.Snapshot, actions *[]Action) error {
	by := decisionsOf(r)
	for i, f := range j.Families {
		ds := by[i]
		if !moved(f, ds) {
			continue
		}
		done := true
		for _, rep := range f.Reports {
			done = done && ds["report "+rep.Pending.ID].Action == writeDone
		}
		if done {
			continue
		}
		heads := map[string]string{}
		for _, u := range f.Branches.Updates {
			heads[u.Branch] = u.New
		}
		o := f.Observed
		o.Result, o.Refusal = change.RestackComplete, ""
		published := map[string]*change.RestackPublished{}
		var why []string
		for _, l := range o.Layers {
			if l.ID != "" {
				published[l.ID] = &change.RestackPublished{Head: change.HeadPrefix + heads[l.Branch]}
			}
		}
		for _, v := range f.Versions {
			d := ds["version "+v.ID]
			if d.Action == writeDone {
				n, record := v.Version, change.HeadPrefix+v.Commit
				published[v.ID].Version, published[v.ID].Record = &n, &record
				continue
			}
			o.Result = change.RestackPartial
			why = append(why, fmt.Sprintf("%s v%d was not added: %s", v.Handle, v.Version, d.Why))
		}
		o.Refusal = strings.Join(why, "; ")
		digest := o.Digest()
		for _, l := range o.Layers {
			if l.ID == "" || s.Refs[change.Ref(rebase.Target, l.ID)] == "" {
				continue
			}
			rep, _, err := bound(fullReport(j.Operation, j.Tip, l.ID, o, published), digest)
			if err != nil {
				return err
			}
			if current, found := s.Report(l.ID); found && sameReport(current.Report, rep) {
				continue
			}
			old := s.Refs[rebase.RestackPrefix+l.ID]
			p, err := rebase.BuildReport(ctx, g, rep, rebase.ReportMessage(l.Name, rep), old)
			if err != nil {
				return err
			}
			w := p.Push(ctx, g)
			*actions = append(*actions, Action{Operation: j.Operation, Family: i, Write: "report", ID: l.ID, Commit: p.Commit, Result: w.Push.String(), Why: w.Why})
		}
	}
	return ctx.Err()
}

// forget removes the journal from the state directory, durably: the
// directory is synced after.
func forget(dir string) error {
	if err := os.Remove(filepath.Join(dir, journalFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
