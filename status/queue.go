package main

// The operator's queue: the ready changes that wait on an approval, in the
// order they would land, and the ready changes that move without one.
// valley pending prints it (pending.go), with each ready change's
// conflicts with the others (conflicts.go), and valley review with no
// change walks it (walk.go), without them.
//
// A ready change whose active version has not landed is moving when a
// current restack report says its restack stopped short of complete, when
// one of its required checks failed, or when it is approved: its active
// version has an integration request at its head, or an earlier version's
// request carries to it (addCarried, pending.go). Every other one waits on
// an approval. Waiting changes are stacked by ancestry among themselves
// (stack): a waiting change that another's head contains folds into that
// change's row, since approving the top lands every layer. A moving change
// is a row of its own whatever it is stacked on, since it moves on its own.
//
// Rows are in landing order. The check worker builds ready versions in
// the order it first saw them ready, then by project, then by change ID
// (worker.go), and the integrator lands an approved request once its
// evidence is complete. So a row's place is when its top's active version
// became ready, oldest first, then its project and change ID. A record
// carries no time, so when a version became ready is read as the commit
// time of the change's record: the last write to the record of a ready
// change is the one that readied its active version. The worker's own
// order is the scan in which its pass first saw each version, which only
// that pass holds, so versions readied between the same two scans may
// land in another order than their rows.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"the-valley/change"
)

// queue is a repository's rows: the stacks of changes that wait on an
// approval, each by its top, and the moving changes, each with its glyph
// and what it says. Neither is in landing order yet (byLanding). With
// measured, each entry is measured for its block (measure).
func (r listed) queue(view jsonRepo, h handleMap, l listing, measured bool) (waiting, moving []*entry) {
	states := r.states()
	var wait []*entry
	for _, c := range view.Changes {
		e := r.changeEntry("ready", c, h, l, states)
		v := c.Versions[c.Active-1]
		switch {
		case c.Restack != nil && c.Restack.Current && c.Restack.Report.Result != change.RestackComplete:
			name := func(id string) string { return h[handleKey{r.name, c.Target, id}] }
			e.glyph, e.colour = "◇", colourWaiting
			if c.Restack.Report.Result == change.RestackConflict {
				e.glyph, e.colour = "✗", colourRefused
			}
			e.says = "restack: " + readable(restackSays(c.Restack.Report, name))
		case v.Checks != nil && v.Checks.State == checkFailed:
			e.glyph, e.colour, e.says = "✗", colourRefused, failedNames(v.Checks)+" failed"
			if c.Approved != nil && *c.Approved {
				e.says = "approved · " + e.says
			}
		case c.Approved != nil && *c.Approved:
			e.glyph, e.colour, e.says = "◐", colourWorking, "approved"
			if _, n := rowChecks(v.Checks); n != "" {
				e.says += " · " + n
			}
		default:
			wait = append(wait, e)
			continue
		}
		moving = append(moving, e)
	}
	r.dated(append(append([]*entry{}, wait...), moving...))
	waiting = r.snap.stack(wait, measured)
	for _, e := range moving {
		if measured {
			r.snap.measure(e)
		}
	}
	return waiting, moving
}

// operatorQueue is the queue across every readable repository in repos,
// as valley pending and valley review's walk (walk.go) both read it: the
// rows that wait on an approval and the moving rows, each in landing
// order. A change's checks are read before it is classed, so a change
// whose check failed is moving rather than waiting. With --drafts and
// --checks, the drafts' checks are read with them, in the same batch.
// With p, each ready change is marked with its conflicts with the others
// (conflicts.go). each, when it is not nil, is given every repository's
// view as it is read.
func operatorQueue(repos []listed, h handleMap, l listing, c *costs, p *pairing, stderr io.Writer, each func(listed, jsonRepo)) (waiting, moving []*entry) {
	for _, r := range repos {
		if r.err != nil {
			continue
		}
		end := phase("changes")
		view := r.pendingView(h)
		r.addCarried(&view, stderr)
		end()
		end = phase("checks")
		r.addChecks(&view, l.drafts && l.checks, c, stderr)
		end()
		r.addConflicts(&view, p, stderr)
		wa, mo := r.queue(view, h, l, l.verbose)
		waiting, moving = append(waiting, wa...), append(moving, mo...)
		if each != nil {
			each(r, view)
		}
	}
	byLanding(waiting)
	byLanding(moving)
	return waiting, moving
}

// byLanding orders rows as they would land: by when the top's active
// version became ready, oldest first, then by project and change ID.
func byLanding(rows []*entry) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.since != b.since {
			return a.since < b.since
		}
		if a.repo != b.repo {
			return a.repo < b.repo
		}
		return a.id < b.id
	})
}

// checkCount is a version's checks as a row says them: passed out of
// required when every nix check passed, and otherwise how many are
// pending or failed. A check of another runner is not counted, since the
// host worker does not run it. It is "" when the checks were not read.
func checkCount(c *jsonVersionChecks) string {
	if c == nil {
		return ""
	}
	count := map[string]int{}
	for _, q := range c.Required {
		count[q.State]++
	}
	built := len(c.Required) - count[checkOther]
	switch {
	case built == 0:
		return "no checks required"
	case count[checkFailed] > 0:
		return counted(count[checkFailed], "check") + " failed"
	case count[checkPending] > 0:
		return counted(count[checkPending], "check") + " pending"
	}
	return fmt.Sprintf("checks %d/%d", count[checkPassed], built)
}

// failedNames names the checks that failed, up to three.
func failedNames(c *jsonVersionChecks) string {
	var names []string
	for _, q := range c.Required {
		if q.State == checkFailed {
			names = append(names, q.Name)
		}
	}
	return firstFew(names)
}

// states is the state of every change a repository records, by its
// handle key.
func (r listed) states() map[handleKey]string {
	states := map[handleKey]string{}
	for _, n := range r.snap.all {
		states[handleKey{r.name, n.record.Target, n.id}] = n.record.State
	}
	return states
}

// changeEntry is a recorded change as an entry of section.
func (r listed) changeEntry(section string, c jsonChange, h handleMap, l listing, states map[handleKey]string) *entry {
	v := c.Versions[c.Active-1]
	e := &entry{section: section, repo: r.name, id: c.ChangeID, version: v.N, record: c.Record,
		name: fmt.Sprintf("%s v%d", c.Handle, v.N), ref: c.Handle, head: v.Head, title: readable(v.Title)}
	if len(c.Knots) > 0 {
		e.knot = readable(c.Knots[0].ID)
	}
	if section == "drafts" && v.Branch != "" {
		e.name += " on " + branchName(v.Branch)
	}
	for _, id := range c.Includes {
		k := handleKey{r.name, c.Target, id}
		if st := states[k]; st != change.Ready && st != change.Draft {
			e.includes = append(e.includes, fmt.Sprintf("includes %s (%s)", h[k], st))
		}
	}
	if v.Landing == uncertain {
		e.notes = append(e.notes, "uncertain: it may have landed without its evidence being stored")
	}
	if c.Restack != nil && c.Restack.Current {
		name := func(id string) string { return h[handleKey{r.name, c.Target, id}] }
		e.notes = append(e.notes, "restack: "+readable(restackSays(c.Restack.Report, name)))
	}
	if l.checks {
		e.requires = r.snap.checks(v.Head)
	}
	if v.Checks != nil {
		e.state, e.checks, e.see = v.Checks, checksLine(v.Checks), workerSee(v.Checks)
	}
	e.conflicts = conflictLines(c, l.checks || l.verbose)
	if c.ConflictsUnknown != nil {
		e.unknown = *c.ConflictsUnknown
	}
	return e
}

// dated gives each entry its head's commit time, age and author, and a
// change's entry the commit time of its record.
func (r listed) dated(all []*entry) {
	for _, e := range all {
		out, _ := r.g.output(true, "log", "-1", "--format=%ct%x00%cr%x00%an", e.head)
		if fields := strings.SplitN(out, "\x00", 3); len(fields) == 3 {
			e.when = parseUnix(fields[0])
			e.age, e.author = fields[1], readable(fields[2])
		}
		if e.record != "" {
			out, _ := r.g.output(true, "log", "-1", "--format=%ct", e.record)
			e.since = parseUnix(out)
		}
	}
}
