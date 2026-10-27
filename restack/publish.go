package main

import (
	"context"
	"fmt"
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
// next, lower layers first, and its reports and replacements last, each
// after another read. So a pass that ends anywhere leaves a journal the
// next pass finishes in the same way, and a push whose answer was lost is
// decided by what origin shows on the next read.
//
// A family whose branches moved gets reports that say what did happen.
// Its journaled reports say complete, which holds when every version was
// added. When a version was not, because it stopped or is paused, the
// pass builds a report in place of each that says partial, names each
// version origin holds, and says why each other version was not added.
// Such a report is a replacement. A family with a paused version is open,
// and its replacements are replaced again once it is not. A replacement
// is built from the journal's observation and origin as read, and kept in
// the journal before it is pushed, so a push that fails or whose answer
// is lost leaves it there for the next read to decide. A replacement that
// would say what origin's current report says is not built.
//
// What a read shows origin took of a family's writes, its branch push and
// each version, the journal keeps as accepted before anything more is
// pushed or built (confirm). Recovery decides those writes done from then
// on, so a replacement still to push stays in the journal even when an
// author rewrites or deletes every branch the family moved.
//
// Each family goes at its own pace. At each read, a family with a write
// still to finish pushes its next phase, and any other family whose
// branches moved has its replacements built. A write origin keeps
// refusing therefore holds back only its own family.
//
// Once a read finds nothing of a family left to push and no replacement
// of it to build, the pass lets go of the family if it is not open
// (retire). It does so when no family has anything left to push or build,
// or at the pass's last read, for the families that have nothing left
// then. A family is open
// while a write of it is still to finish, or while origin took its branch
// push and a version of it is neither added nor stopped for good, as a
// version paused by a request that might still land is. An open family
// stays in the journal, and the planner has each family that shares a
// change or a branch with it wait (waits). A later pass adds the families
// it builds to the same journal. A family whose branch push stopped, and
// so moved nothing, is left for the next plan. Once no family is left,
// the journal goes too.

// maxSteps bounds how many times one pass reads origin and pushes what is
// still to finish. A family takes three: its branches, its versions and its
// reports. A family with a replacement takes two more: one to build and
// keep it, and one to push it. A read that confirms the last push comes
// after these. A write still to finish after the last read is left to the
// next pass, and holds only its own family in the journal.
const maxSteps = 8

// Action is one write a pass tried to push, and what became of it: origin
// may have taken it or refused it, or the pass may not have pushed it.
type Action struct {
	Operation string `json:"operation"`
	Family    int    `json:"family"`              // the family's place in the journal
	Write     string `json:"write"`               // branches, version, report or replacement
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

// settle makes the journal's writes as recovery decides them, each family
// at its own pace. Once a read finds nothing left to push and no
// replacement to build, or at the last read, it lets go of each family
// that has nothing left then and is not open, and of the journal once
// none is left.
func settle(ctx context.Context, g rebase.Git, c config, j *Journal) (*settled, error) {
	out := &settled{actions: []Action{}}
	for step := 0; step < maxSteps; step++ {
		rd, err := readOrigin(ctx, g, c)
		if err != nil {
			return out, err
		}
		out.last = decide(j, rd)
		if confirm(j, out.last) {
			if err := keep(ctx, g, c.state, j); err != nil {
				return out, err
			}
		}
		busy := pushed(ctx, g, j, out.last, &out.actions)
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := replace(ctx, g, c, j, out.last, rd.s, busy); err != nil {
			return out, err
		}
		if len(busy) > 0 && step < maxSteps-1 {
			continue
		}
		if err := retire(ctx, g, c, j, out.last, busy); err != nil {
			return out, err
		}
		if out.closed = len(j.Families) == 0; !out.closed {
			out.last = decide(j, rd)
		}
		return out, nil
	}
	return out, nil
}

// confirm adds to each family's accepted writes what recovery decided
// origin took at its last read: the branch push, and each version, once
// the branch push is accepted. It reports whether it added anything,
// which the journal is then kept with before anything more is pushed or
// built.
func confirm(j *Journal, r *Recovery) bool {
	by := decisionsOf(r)
	added := false
	for i := range j.Families {
		f, ds := &j.Families[i], by[i]
		if f.Branches == nil {
			continue
		}
		if !f.Accepted.Branches && ds["branches "].Action == writeDone {
			f.Accepted.Branches, added = true, true
		}
		if !f.Accepted.Branches {
			continue
		}
		versions := []string{}
		for _, v := range f.Versions {
			if f.Accepted.has(v.ID) || ds["version "+v.ID].Action == writeDone {
				versions = append(versions, v.ID)
			}
		}
		if len(versions) != len(f.Accepted.Versions) {
			f.Accepted.Versions, added = versions, true
		}
	}
	return added
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
// to finish: its branch push, then its versions, then its reports and its
// replacements. It returns each family it tried a push of, by its place
// in the journal.
func pushed(ctx context.Context, g rebase.Git, j *Journal, r *Recovery, actions *[]Action) map[int]bool {
	tried := map[int]bool{}
	by := decisionsOf(r)
	for i, f := range j.Families {
		ds := by[i]
		record := func(a Action) {
			a.Operation, a.Family = f.Operation, i
			*actions = append(*actions, a)
			tried[i] = true
		}
		if ctx.Err() != nil {
			return tried
		}
		if f.Branches != nil && ds["branches "].Action == writeFinish {
			push, refused := f.Branches.Push(ctx, g)
			a := Action{Write: "branches", Result: push.String()}
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
			a := Action{Write: "version", ID: v.ID, Commit: v.Commit, Result: ap.Write.Push.String(), Why: ap.Write.Why}
			if ap.Err != nil && a.Why == "" {
				a.Why = ap.Err.Error()
			}
			record(a)
			versions = true
		}
		if versions {
			continue
		}
		for _, write := range []struct {
			name    string
			reports []JournalReport
		}{{"report", f.Reports}, {"replacement", f.Replacements}} {
			for _, rep := range write.reports {
				p := rep.Pending
				if ds[write.name+" "+p.ID].Action != writeFinish {
					continue
				}
				w := p.Push(ctx, g)
				record(Action{Write: write.name, ID: p.ID, Commit: p.Commit, Result: w.Push.String(), Why: w.Why})
			}
		}
	}
	return tried
}

// moved reports whether origin took a family's branch push.
func moved(f JournalFamily, ds map[string]Decision) bool {
	return f.Branches != nil && ds["branches "].Action == writeDone
}

// isOpen reports whether a family is open: a write of it still to finish,
// or its branches moved and a version neither added nor stopped for good.
func isOpen(f JournalFamily, ds map[string]Decision) bool {
	for _, d := range ds {
		if d.Action == writeFinish {
			return true
		}
	}
	if !moved(f, ds) {
		return false
	}
	for _, v := range f.Versions {
		if !ds["version "+v.ID].settled() {
			return true
		}
	}
	return false
}

// retire lets go of each family of the journal that is not open and not
// busy, and of the journal once no family is left. A busy family had a
// push tried or a replacement built at the read r was decided at, so what
// became of it is for the next read to tell. Every other family had
// nothing left to push and no replacement to build at that read, so each
// report of it, if its branches moved, is on origin, as that read showed.
func retire(ctx context.Context, g rebase.Git, c config, j *Journal, r *Recovery, busy map[int]bool) error {
	by := decisionsOf(r)
	kept := []JournalFamily{}
	for i, f := range j.Families {
		if busy[i] || isOpen(f, by[i]) {
			kept = append(kept, f)
		}
	}
	switch {
	case len(kept) == 0:
		j.Families = kept
		return forget(ctx, g, c.state)
	case len(kept) < len(j.Families):
		j.Families = kept
		return keep(ctx, g, c.state, j)
	}
	return nil
}

// whatHappened is the observation of a family whose branches moved, as
// what did happen to it: complete when every version was added, and
// partial, saying why each other version was not, otherwise. version is
// what recovery decided of each change's version. It returns too what
// was published of each change: its prepared head, with the version when
// it was added.
func whatHappened(f JournalFamily, version func(id string) Decision) (Observation, map[string]*change.RestackPublished) {
	heads := map[string]string{}
	for _, u := range f.Branches.Updates {
		heads[u.Branch] = u.New
	}
	o := f.Observed
	o.Result, o.Refusal = change.RestackComplete, ""
	published := map[string]*change.RestackPublished{}
	for _, l := range o.Layers {
		if l.ID != "" {
			published[l.ID] = &change.RestackPublished{Head: change.HeadPrefix + heads[l.Branch]}
		}
	}
	var why []string
	for _, v := range f.Versions {
		d := version(v.ID)
		if d.Action == writeDone {
			n, record := v.Version, change.HeadPrefix+v.Commit
			published[v.ID].Version, published[v.ID].Record = &n, &record
			continue
		}
		o.Result = change.RestackPartial
		why = append(why, fmt.Sprintf("%s v%d was not added: %s", v.Handle, v.Version, d.Why))
	}
	o.Refusal = strings.Join(why, "; ")
	return o, published
}

// replace builds, for each family whose branches moved and that is not
// busy, a report of what did happen for each of its changes whose report
// on origin says otherwise, in place of any replacement of that change
// the family held. A family busy at this read has a push whose result the
// next read tells. replace archives the observation a replacement is cut
// from when the replacement leaves something out of it, and keeps the
// journal with them before any is pushed. It marks each family it built a
// replacement for busy. A change origin no longer records gets none,
// since the hook takes no report about it, and so does a change no report
// of which fits, as when the family's writes were built.
func replace(ctx context.Context, g rebase.Git, c config, j *Journal, r *Recovery, s *rebase.Snapshot, busy map[int]bool) error {
	by := decisionsOf(r)
	built := false
	for i := range j.Families {
		f, ds := &j.Families[i], by[i]
		if busy[i] || !moved(*f, ds) {
			continue
		}
		o, published := whatHappened(*f, func(id string) Decision { return ds["version "+id] })
		digest := o.Digest()
		for _, l := range o.Layers {
			if l.ID == "" || s.Refs[change.Ref(rebase.Target, l.ID)] == "" {
				continue
			}
			rep, omitted, err := bound(fullReport(f.Operation, f.Tip, l.ID, o, published), digest)
			if err != nil {
				continue
			}
			if current, found := s.Report(l.ID); found && sameReport(current.Report, rep) {
				continue
			}
			if len(omitted) > 0 {
				if err := archive(c.state, o); err != nil {
					return err
				}
			}
			p, err := rebase.BuildReport(ctx, g, rep, rebase.ReportMessage(l.Name, rep), s.Refs[rebase.RestackPrefix+l.ID])
			if err != nil {
				return err
			}
			kept := JournalReport{Pending: p, Omitted: append([]string{}, omitted...)}
			replaced := false
			for k := range f.Replacements {
				if f.Replacements[k].Pending.ID == l.ID {
					f.Replacements[k], replaced = kept, true
				}
			}
			if !replaced {
				f.Replacements = append(f.Replacements, kept)
			}
			built, busy[i] = true, true
		}
	}
	if !built {
		return ctx.Err()
	}
	return keep(ctx, g, c.state, j)
}
