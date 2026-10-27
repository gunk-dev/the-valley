package main

import (
	"fmt"
	"strings"

	"the-valley/change"
	"the-valley/rebase"
)

// Recovery reads origin and a journal, and decides each write the journal
// names, in the order a pass makes them: a family's branch push, then its
// versions, lower layers first, then its reports and its replacements.
// Each write is one of:
//
//   - done: origin holds it, or has moved on from it;
//   - finish: origin does not hold it, and everything the write was built
//     on still holds, so it is still valid to push;
//   - stop: it must not be pushed. A stop is final when no later pass could
//     push it either, and terminal when it is also a local failure whose
//     diagnostics the journal keeps.
//
// A branch push origin did not take is finished only while nothing it was
// prepared from has moved: main is at the tip it was rebased onto, every
// branch at the tip its lease names, and every record at the record
// commit read. Otherwise it stops, and the next pass prepares the family
// again. A version is finished only while its branch is at the prepared
// head, or the branch push is to be finished first, and its record is
// still the record commit read, so the change is still ready at the head
// the capture read. A version stops on its author's movement, a
// withdrawal, an unready, a submission, or a main that no longer descends
// from the tip the family was rebased onto. A deleted record is a
// terminal failure: the version cannot be added, and the hook takes no
// report about a change it does not record. A family whose writes stop
// does not stop another's.
//
// A related integration request that might still land holds the branch
// push and every version, which wait for a later pass: requests are read
// again, by name and by every version head the changes have had, the
// prepared heads included. A report is finished once what it says
// published is on origin, or will be once the writes before it are
// finished. A report whose claims no longer can be, because a version it
// names stopped, stops, and a report of what did happen is built in its
// place: a replacement. A replacement is finished while origin's report
// is still the one it was built on and it still says what did happen, as
// the family's versions are decided now. One that no longer does stops,
// and another is built in its place.
//
// In --report-only mode recovery decides and pushes nothing.

// What recovery decides about a write.
const (
	writeDone   = "done"
	writeFinish = "finish"
	writeStop   = "stop"
)

// Recovery is what recovery decided about a journal.
type Recovery struct {
	Journal   string     `json:"journal"`         // the journal's path
	Error     string     `json:"error,omitempty"` // why it was not read, so nothing in it is decided
	Settled   bool       `json:"settled"`         // every write is done or stopped for good
	Decisions []Decision `json:"decisions"`
}

// Decision is what recovery decided about one write.
type Decision struct {
	Family    int    `json:"family"`              // the family's place in the journal
	Operation string `json:"operation"`           // the operation that built the family
	Write     string `json:"write"`               // branches, version, report or replacement
	ID        string `json:"change_id,omitempty"` // the change a version or a report is about
	Commit    string `json:"commit,omitempty"`    // the record or report commit it pushes
	Action    string `json:"action"`              // done, finish or stop
	Final     bool   `json:"final"`               // a stop no later pass can finish
	Terminal  bool   `json:"terminal"`            // a final stop that is a local failure, kept with its diagnostics
	Why       string `json:"why"`
}

func (d Decision) settled() bool { return d.Action == writeDone || d.Action == writeStop && d.Final }

// reading is what recovery reads origin through.
type reading struct {
	s        *rebase.Snapshot
	requests []Request
	evidence string                                 // the evidence fingerprint origin shows now
	in       Incoming                               // against origin's tip now
	is       Ancestry                               // in the scratch repository
	have     func(oid string) bool                  // whether the scratch repository holds an object
	landed   func(id string, v change.Version) bool // whether a version has landed
}

// decide decides every write the journal names.
func decide(j *Journal, rd reading) *Recovery {
	rec := &Recovery{Decisions: []Decision{}, Settled: true}
	for i, f := range j.Families {
		ds := rd.family(i, f)
		for k := range ds {
			ds[k].Operation = f.Operation
		}
		rec.Decisions = append(rec.Decisions, ds...)
	}
	for _, d := range rec.Decisions {
		rec.Settled = rec.Settled && d.settled()
	}
	return rec
}

func (rd reading) family(i int, f JournalFamily) []Decision {
	var out []Decision
	stopped := func(d *Decision, final bool, why string, a ...any) {
		d.Action, d.Final, d.Why = writeStop, final, fmt.Sprintf(why, a...)
	}
	layers := map[string]ObservedLayer{}
	for _, l := range f.Observed.Layers {
		layers[l.ID] = l
	}

	// Whether main still descends from the tip the family was rebased onto.
	compatible, final, target := true, true, ""
	switch descends, err := rd.is(f.Tip, rd.s.Tip); {
	case rd.s.Tip == "":
		compatible, target = false, "origin has no "+rebase.Target
	case err != nil:
		compatible, final, target = false, false, fmt.Sprintf("whether %s at %s descends from %s cannot be told", rebase.Target, short(rd.s.Tip), short(f.Tip))
	case !descends:
		compatible, target = false, fmt.Sprintf("%s at %s no longer descends from %s, the tip the family was rebased onto", rebase.Target, short(rd.s.Tip), short(f.Tip))
	}
	held := rd.holding(f)
	var submitted []string
	for _, v := range f.Versions {
		if cv, ok := capturedVersion(v, layers[v.ID]); ok && rd.landed(v.ID, cv) {
			submitted = append(submitted, fmt.Sprintf("%s v%d has been submitted", v.Handle, cv.N))
		}
	}

	// The branch push.
	branches := Decision{Action: ""}
	if b := f.Branches; b != nil {
		d := Decision{Family: i, Write: "branches"}
		var moved, other, missing []string
		for _, u := range b.Updates {
			at := rd.s.Refs["refs/heads/"+u.Branch]
			switch {
			case at == u.New || at != "" && at != u.Old && rd.reaches(u.New, at):
				moved = append(moved, u.Branch)
			case at == u.Old:
			case at == "":
				other = append(other, "origin has no "+u.Branch)
			default:
				other = append(other, fmt.Sprintf("origin's %s is at %s, not %s", u.Branch, short(at), short(u.Old)))
			}
			if !rd.have(u.New) {
				missing = append(missing, short(u.New))
			}
		}
		recordMoved := ""
		for _, v := range f.Versions {
			if rd.s.Refs[v.Ref] != v.Old {
				recordMoved = rd.recordNow(v, layers[v.ID].Head)
				break
			}
		}
		switch {
		case len(moved) > 0:
			d.Action, d.Why = writeDone, "origin took the branch push: "+strings.Join(moved, ", ")+" at the prepared heads, or past them"
		case len(other) > 0:
			stopped(&d, true, "the branches were not pushed, and %s since the family was prepared", strings.Join(other, "; "))
		case len(missing) > 0:
			stopped(&d, true, "the scratch repository no longer holds the prepared head %s", strings.Join(missing, ", "))
		case !compatible:
			stopped(&d, final, "the branches were not pushed, and %s", target)
		case rd.s.Tip != f.Tip:
			stopped(&d, true, "%s moved from %s to %s before the branches were pushed, so the next pass prepares the family again", rebase.Target, short(f.Tip), short(rd.s.Tip))
		case recordMoved != "":
			stopped(&d, true, "the branches were not pushed, and %s", recordMoved)
		case len(submitted) > 0:
			stopped(&d, true, "the branches were not pushed, and %s", strings.Join(submitted, "; "))
		case len(held) > 0:
			stopped(&d, false, "the branches wait: %s", strings.Join(held, "; "))
		default:
			d.Action, d.Why = writeFinish, "origin has not moved since the family was prepared"
		}
		branches = d
		out = append(out, d)
	}

	// Each version, lower layers first.
	versions := map[string]Decision{}
	for _, v := range f.Versions {
		d := Decision{Family: i, Write: "version", ID: v.ID, Commit: v.Commit}
		at := rd.s.Refs[v.Ref]
		branch := rd.s.Refs["refs/heads/"+v.Branch]
		switch {
		case at == v.Commit || at != "" && at != v.Old && rd.reaches(v.Commit, at):
			d.Action, d.Why = writeDone, fmt.Sprintf("origin's record of %s holds v%d", v.Handle, v.Version)
		case at == "":
			stopped(&d, true, "origin no longer holds a record of %s, so v%d cannot be added", v.Handle, v.Version)
			d.Terminal = true
		case at != v.Old:
			stopped(&d, true, "%s, so v%d is not added", rd.recordNow(v, layers[v.ID].Head), v.Version)
		case !rd.have(v.Commit):
			stopped(&d, true, "the scratch repository no longer holds the record commit %s", short(v.Commit))
		case branches.Action == writeStop:
			stopped(&d, branches.Final, "its branch push stopped: %s", branches.Why)
		case !compatible:
			stopped(&d, final, "%s", target)
		case len(submitted) > 0:
			stopped(&d, true, "%s", strings.Join(submitted, "; "))
		case branches.Action == writeFinish:
			d.Action, d.Why = writeFinish, "once the branches are pushed: the record is ready at the head the capture read"
		case branch == "":
			stopped(&d, true, "origin no longer has %s, so v%d is not added", v.Branch, v.Version)
		case branch != v.Head:
			stopped(&d, true, "origin's %s is at %s, not the prepared head %s: its author moved it, so v%d is not added", v.Branch, short(branch), short(v.Head), v.Version)
		case len(held) > 0:
			stopped(&d, false, "paused: %s", strings.Join(held, "; "))
		default:
			d.Action, d.Why = writeFinish, fmt.Sprintf("origin's %s is at the prepared head, and the record is ready at the head the capture read", v.Branch)
		}
		versions[v.ID] = d
		out = append(out, d)
	}

	// Each report.
	for _, r := range f.Reports {
		p := r.Pending
		d := Decision{Family: i, Write: "report", ID: p.ID, Commit: p.Commit}
		at := rd.s.Refs[p.Ref]
		current, found := rd.s.Report(p.ID)
		// What the report says was published, against what became of it.
		// The journal was checked as it was read (loadJournal), so each
		// version the report names is one the family adds, decided above.
		claims, claimFinal, pending := "", false, false
		for _, l := range p.Report.Layers {
			if l.Published == nil {
				continue
			}
			switch v := versions[l.ChangeID]; {
			case branches.Action == writeStop:
				claims, claimFinal = "its branches were not pushed: "+branches.Why, branches.Final
			case l.Published.Record != nil && v.Action == writeStop:
				claims, claimFinal = fmt.Sprintf("%s's version was not added: %s", l.ChangeID, v.Why), v.Final
			case l.Published.Record != nil && v.Action == writeFinish || branches.Action == writeFinish:
				pending = true
			}
			if claims != "" {
				break
			}
		}
		switch {
		case at == p.Commit || at != "" && at != p.Old && rd.reaches(p.Commit, at):
			d.Action, d.Why = writeDone, "origin holds the report"
		case rd.s.Refs[change.Ref(rebase.Target, p.ID)] == "":
			stopped(&d, true, "origin no longer holds a record of %s, and the hook takes no report about a change it does not record. The report is kept here: %s, %s",
				p.ID, p.Report.Result, oneLine(p.Report))
			d.Terminal = true
		case found && sameReport(current.Report, p.Report):
			d.Action, d.Why = writeDone, "origin's report already says the same"
		case at != p.Old:
			stopped(&d, true, "origin's report moved to %s since this one was built on %s", short(at), orNone(p.Old))
		case !rd.have(p.Commit):
			stopped(&d, true, "the scratch repository no longer holds the report commit %s", short(p.Commit))
		case claims != "":
			stopped(&d, claimFinal, "the attempt did not end as this report says, since %s; a report of what did happen is built in its place", claims)
		case pending:
			d.Action, d.Why = writeFinish, "once the writes it reports are pushed"
		default:
			d.Action, d.Why = writeFinish, "origin's report is still the one it was built on"
		}
		out = append(out, d)
	}

	// Each replacement, against what did happen as the versions are
	// decided now.
	var happened Observation
	var published map[string]*change.RestackPublished
	if branches.Action == writeDone {
		happened, published = whatHappened(f, func(id string) Decision { return versions[id] })
	}
	for _, r := range f.Replacements {
		p := r.Pending
		d := Decision{Family: i, Write: "replacement", ID: p.ID, Commit: p.Commit}
		at := rd.s.Refs[p.Ref]
		current, found := rd.s.Report(p.ID)
		says := ""
		if branches.Action == writeDone {
			if want, _, err := bound(fullReport(f.Operation, f.Tip, p.ID, happened, published), happened.Digest()); err != nil {
				says = err.Error()
			} else if !sameReport(want, p.Report) {
				says = "it no longer says what did happen"
			}
		}
		switch {
		case at == p.Commit || at != "" && at != p.Old && rd.reaches(p.Commit, at):
			d.Action, d.Why = writeDone, "origin holds the report"
		case rd.s.Refs[change.Ref(rebase.Target, p.ID)] == "":
			stopped(&d, true, "origin no longer holds a record of %s, and the hook takes no report about a change it does not record. The report is kept here: %s, %s",
				p.ID, p.Report.Result, oneLine(p.Report))
			d.Terminal = true
		case found && sameReport(current.Report, p.Report):
			d.Action, d.Why = writeDone, "origin's report already says the same"
		case at != p.Old:
			stopped(&d, true, "origin's report moved to %s since this one was built on %s; a report of what did happen is built in its place", short(at), orNone(p.Old))
		case !rd.have(p.Commit):
			stopped(&d, true, "the scratch repository no longer holds the report commit %s; a report of what did happen is built in its place", short(p.Commit))
		case branches.Action != writeDone:
			stopped(&d, true, "origin no longer shows the family's branch push: %s", branches.Why)
		case says != "":
			stopped(&d, true, "%s; a report of what did happen is built in its place", says)
		default:
			d.Action, d.Why = writeFinish, "origin's report is still the one it was built on, and this one says what did happen"
		}
		out = append(out, d)
	}
	return out
}

// reaches reports whether a commit is in the history of another: whether
// a ref moved on from a write origin took.
func (rd reading) reaches(commit, at string) bool {
	in, err := rd.is(commit, at)
	return err == nil && in
}

// holding is why the requests origin holds now might still land one of
// the family's heads: each related request whose outcome does not release
// it, as the planner judges them. A family's heads are every version head
// its changes have had, and the prepared heads.
func (rd reading) holding(f JournalFamily) []string {
	if len(f.Versions) == 0 {
		return nil
	}
	m := Members{Names: map[string]bool{}}
	seen := map[string]bool{}
	for _, v := range f.Versions {
		m.Names[change.RequestName(v.ID)] = true
		for _, version := range v.Record.Versions {
			if version.Branch != "" {
				m.Names[rebase.ChangeName(version.Branch)] = true
			}
			if h := rebase.HeadOf(version); !seen[h] {
				seen[h] = true
				m.Heads = append(m.Heads, h)
			}
		}
	}
	var held []string
	for _, r := range rd.requests {
		by, err := Relate(r, m, rd.in)
		if err != nil {
			held = append(held, fmt.Sprintf("request %s: whether it is related cannot be determined: %v", r.Name, err))
			continue
		}
		if by == nil {
			continue
		}
		o, found := rd.s.OutcomeOf(r.Name)
		if _, releases, why := Currency(o, found, r.Head, rd.s.Tip, rd.evidence); !releases {
			held = append(held, fmt.Sprintf("request %s might still land: %s", r.Name, why))
		}
	}
	return held
}

// recordNow says how the change's record moved from the record commit a
// version was built on. captured is the head the capture read the change
// at.
func (rd reading) recordNow(v *rebase.PendingVersion, captured string) string {
	at := rd.s.Refs[v.Ref]
	if at == "" {
		return "origin no longer holds a record of " + v.Handle
	}
	for _, n := range rd.s.Changes {
		if n.ID != v.ID {
			continue
		}
		r, active := n.Record, n.Record.ActiveVersion()
		switch {
		case r.State == change.Withdrawn:
			return v.Handle + " was withdrawn"
		case r.State != change.Ready:
			return fmt.Sprintf("%s is %s, not ready", v.Handle, r.State)
		case rebase.HeadOf(active) != captured:
			return fmt.Sprintf("%s is at v%d, %s: its author moved it", v.Handle, active.N, short(rebase.HeadOf(active)))
		}
		return fmt.Sprintf("the record of %s moved to %s since it was read, so the lease is gone", v.Handle, short(at))
	}
	return fmt.Sprintf("the record of %s at %s does not read", v.Handle, short(at))
}

// capturedVersion is the version a family's layer was at when the capture
// read it, from the record the version was built on.
func capturedVersion(v *rebase.PendingVersion, l ObservedLayer) (change.Version, bool) {
	for _, version := range v.Record.Versions {
		if version.N == l.Version {
			return version, true
		}
	}
	return change.Version{}, false
}

// oneLine is a report as one line, for a diagnostic.
func oneLine(r *change.RestackReport) string {
	var layers []string
	for _, l := range r.Layers {
		s := fmt.Sprintf("%s v%d at %s", l.ChangeID, l.Version, short(strings.TrimPrefix(l.Head, change.HeadPrefix)))
		if l.Published != nil {
			s += " published at " + short(strings.TrimPrefix(l.Published.Head, change.HeadPrefix))
		}
		layers = append(layers, s)
	}
	return fmt.Sprintf("on %s at %s, operation %s: %s", r.Target, short(strings.TrimPrefix(r.TargetTip, change.HeadPrefix)), r.Operation, strings.Join(layers, "; "))
}

func orNone(oid string) string {
	if oid == "" {
		return "no report"
	}
	return short(oid)
}
