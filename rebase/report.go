package rebase

import (
	"context"
	"fmt"
	"strings"

	"the-valley/change"
)

// A change's restack report, read back: the latest report the host
// restacker wrote about the change, at RestackPrefix<change ID>
// (change/report.go), and whether it is current.
//
// A report is current only while every input it observed still holds on
// origin: main's tip, and for each layer its record commit and the head of
// its branch. A branch can move or disappear while its record and main
// stay put, so every layer's branch is compared, not only the record. For
// a layer the attempt published, the head compared is the one it moved the
// branch to, and the record compared is the one that recorded that head,
// when it added a version. A report that is not current is historical: it
// describes inputs that have moved since.

// Report is a change's latest restack report.
type Report struct {
	Ref     string                // the report's ref
	Commit  string                // the report commit the ref names
	Report  *change.RestackReport // what it says
	Current bool                  // whether every input it observed still holds
}

// ReadReport reads the report a report commit holds. The commit must have
// been fetched.
func ReadReport(ctx context.Context, g Git, commit string) (*change.RestackReport, error) {
	data, err := g.output(ctx, true, "cat-file", "blob", commit+":report.json")
	if err != nil {
		return nil, fmt.Errorf("the restack report %s holds no report.json", short(commit))
	}
	r, err := change.ParseRestackReport([]byte(data + "\n"))
	if err != nil {
		return nil, fmt.Errorf("the restack report %s does not read: %v", short(commit), err)
	}
	return r, nil
}

// Current reports whether every input r observed still holds in o.
func (o Origin) Current(r *change.RestackReport) bool {
	if r.Target != Target || o.Refs["refs/heads/"+Target] != strings.TrimPrefix(r.TargetTip, change.HeadPrefix) {
		return false
	}
	for _, l := range r.Layers {
		head, record := l.Head, l.Record
		if p := l.Published; p != nil {
			head = p.Head
			if p.Record != nil {
				record = *p.Record
			}
		}
		if o.Refs[change.Ref(Target, l.ChangeID)] != strings.TrimPrefix(record, change.HeadPrefix) ||
			o.Refs["refs/heads/"+l.Branch] != strings.TrimPrefix(head, change.HeadPrefix) {
			return false
		}
	}
	return true
}

// Report is the restack report origin holds for a change, read once per
// snapshot. found is false when origin holds none, or holds one that does
// not read or is about another change, which no reader shows: the hook
// refuses such a report, so only corrupt storage holds one.
func (s *Snapshot) Report(id string) (r Report, found bool) {
	if r, ok := s.reports[id]; ok {
		return r, r.Report != nil
	}
	r = Report{Ref: RestackPrefix + id, Commit: s.Refs[RestackPrefix+id]}
	if r.Commit != "" {
		if rep, err := ReadReport(s.ctx, s.g, r.Commit); err == nil && rep.ChangeID == id {
			r.Report, r.Current = rep, s.Origin.Current(rep)
		}
	}
	s.reports[id] = r
	return r, r.Report != nil
}
