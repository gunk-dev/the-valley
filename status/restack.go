// A change's restack report, as status, changes and pending show it: the
// latest report the host restacker wrote about the change, and whether it
// is current (../rebase/report.go). A report is current only while main's
// tip, and every layer's record and branch head, are what it observed, or
// what it published. Otherwise it is historical.

package main

import (
	"fmt"
	"strings"

	"the-valley/change"
	"the-valley/rebase"
)

// jsonRestack is a change's restack report as valley-status/v1,
// valley-changes/v1 and valley-pending/v1 write it: its ref, the report
// commit, whether it is current, and the report as it reads.
type jsonRestack struct {
	Ref     string                `json:"ref"`
	Commit  string                `json:"commit"`
	Current bool                  `json:"current"`
	Report  *change.RestackReport `json:"report"`
}

// restackView is the JSON of a report, or nil for none.
func restackView(r rebase.Report, found bool) *jsonRestack {
	if !found {
		return nil
	}
	return &jsonRestack{Ref: r.Ref, Commit: r.Commit, Current: r.Current, Report: r.Report}
}

// report is the restack report origin holds for a change.
func (s *snapshot) report(id string) (rebase.Report, bool) { return s.land.Report(id) }

// restackSays is what a report says came of the attempt, as a phrase:
// what happened, onto which main, and where it stopped. name gives a
// change's handle, or "" for a change it has none for, which is named by
// its ID.
func restackSays(r *change.RestackReport, name func(id string) string) string {
	onto := "onto main at " + short(strings.TrimPrefix(r.TargetTip, change.HeadPrefix))
	switch r.Result {
	case change.RestackConflict:
		failing := name(*r.Failing)
		if failing == "" {
			failing = *r.Failing
		}
		says := fmt.Sprintf("a conflict rebasing %s %s", failing, onto)
		if len(r.Conflicts) > 0 {
			says += ", in " + firstFew(r.Conflicts)
		}
		return says
	case change.RestackBlocked:
		says := "held before rebasing " + onto
		if len(r.Requests) > 0 {
			var names []string
			for _, ref := range r.Requests {
				names = append(names, strings.TrimPrefix(ref, rebase.RequestPrefix))
			}
			says += " by the request"
			if len(names) > 1 {
				says += "s"
			}
			says += " " + firstFew(names)
		}
		if r.Refusal != nil {
			says += ": " + *r.Refusal
		}
		return says
	case change.RestackPartial:
		return "rebased " + onto + ", and not every version was added"
	case change.RestackComplete:
		return "rebased " + onto
	default:
		return "given up rebasing " + onto + ": its inputs moved while it ran"
	}
}

// firstFew is up to three names, and how many more there are.
func firstFew(names []string) string {
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:3], ", "), len(names)-3)
}
