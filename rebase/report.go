package rebase

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"the-valley/change"
)

// A change's restack report: written as a report commit and pushed to
// RestackPrefix<change ID> (change/report.go) in two steps, and read back
// as the latest report the host restacker wrote about the change, and
// whether it is current.
//
// A report is current only while every ref it observed still holds on
// origin what the report says it held:
//
//   - main, at the report's target_tip;
//   - each layer's record ref, at the record commit it read;
//   - each layer's branch, at its branch_tip, or still absent when
//     branch_tip is null. A layer that names no branch has none to compare;
//   - each blocking request, at the tip it read.
//
// For a layer the attempt published, the branch is compared with the head
// the attempt moved it to, and the record with the one that recorded that
// head, when it added a version.
//
// Each comparison is with what the report recorded of the ref itself,
// never with the version's head: a branch can be missing, or somewhere
// other than the head its version names. Absent stays absent, so a report
// that observed a missing branch is historical once the branch exists. A
// report that is not current is historical: it describes refs that have
// moved since.

// WriteReport writes a report commit holding r, and returns its ID. Its
// tree holds report.json alone, the one shape the hook accepts. Its one
// parent is the report it replaces, the commit origin's report ref names,
// or it has none when old is "" and the change has no report yet. Nothing
// is pushed, so a caller can keep the commit's ID and old before
// PushReport, and compare origin's report ref with them after a crash.
// BuildReport does the same, and returns a write the caller can keep
// whole.
func WriteReport(ctx context.Context, g Git, r *change.RestackReport, message, old string) (string, error) {
	data, err := change.EncodeRestackReport(r)
	if err != nil {
		return "", err
	}
	if len(data) > change.MaxReportBlob {
		return "", fmt.Errorf("the restack report would be %d bytes, more than the %d a report may be", len(data), change.MaxReportBlob)
	}
	var parents []string
	if old != "" {
		parents = append(parents, old)
	}
	commit, err := writeOneFile(ctx, g, "restack report", "report.json", data, message, parents...)
	if err != nil {
		return "", err
	}
	size, err := g.output(ctx, true, "cat-file", "-s", commit)
	if n, perr := strconv.Atoi(size); err != nil || perr != nil || n > change.MaxReportCommit {
		return "", fmt.Errorf("the restack report's commit is %s bytes, and a report commit may be at most %d", size, change.MaxReportCommit)
	}
	return commit, nil
}

// ReportMessage is the message of a report commit.
func ReportMessage(handle string, r *change.RestackReport) string {
	return fmt.Sprintf("valley restack: %s %s\n\nChange %s, operation %s, on %s at %s.",
		handle, r.Result, r.ChangeID, r.Operation, r.Target, strings.TrimPrefix(r.TargetTip, change.HeadPrefix))
}

// PushReport moves the change's report ref on origin from old to commit,
// leased on old, as PushRecord moves a record ref.
func PushReport(ctx context.Context, g Git, id, old, commit string) RecordWrite {
	return pushLeased(ctx, g, RestackPrefix+id, old, commit)
}

// PendingReport is a report written into a report commit and not yet
// pushed. A caller keeps it whole before it calls Push, in the form
// MarshalJSON writes (pending.go), as it keeps a PendingVersion.
type PendingReport struct {
	ID     string                // the change the report is about
	Ref    string                // its report ref
	Old    string                // the report commit the push is leased on; "" when the change had none
	Commit string                // the report commit written
	Report *change.RestackReport // the report Commit holds
}

// BuildReport writes the report commit holding r, replacing old, as
// WriteReport does, and pushes nothing.
func BuildReport(ctx context.Context, g Git, r *change.RestackReport, message, old string) (*PendingReport, error) {
	commit, err := WriteReport(ctx, g, r, message, old)
	if err != nil {
		return nil, err
	}
	return &PendingReport{ID: r.ChangeID, Ref: RestackPrefix + r.ChangeID, Old: old, Commit: commit, Report: r}, nil
}

// Push moves the change's report ref on origin from Old to Commit, leased
// on Old, as PushReport does.
func (p *PendingReport) Push(ctx context.Context, g Git) RecordWrite {
	return PushReport(ctx, g, p.ID, p.Old, p.Commit)
}

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

// Current reports whether every ref r observed still holds in o what r
// says it held, or left it holding.
func (o Origin) Current(r *change.RestackReport) bool {
	if r.Target != Target || !o.holds("refs/heads/"+Target, &r.TargetTip) {
		return false
	}
	for _, q := range r.Requests {
		if !o.holds(q.Ref, &q.Tip) {
			return false
		}
	}
	for _, l := range r.Layers {
		record, tip := l.Record, l.BranchTip
		if p := l.Published; p != nil {
			tip = &p.Head
			if p.Record != nil {
				record = *p.Record
			}
		}
		if !o.holds(change.Ref(Target, l.ChangeID), &record) {
			return false
		}
		if l.Branch != "" && !o.holds("refs/heads/"+l.Branch, tip) {
			return false
		}
	}
	return true
}

// holds reports whether ref is at the head want names in o, or absent from
// o when want is nil.
func (o Origin) holds(ref string, want *string) bool {
	oid, present := o.Refs[ref]
	if want == nil {
		return !present
	}
	return present && oid == strings.TrimPrefix(*want, change.HeadPrefix)
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
