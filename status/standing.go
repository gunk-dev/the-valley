// Where a change stands now, for review (review.go). Review shows a
// change as its record read when the review began, and the operator may
// read it for a long time. Meanwhile its owner may return it to draft,
// ready a new version, or withdraw it, another change may land and the
// host's restacker may report a conflict, or it may land. So review reads
// the change again with standing before it acts on an answer, and says
// plainly what changed rather than acting on a change that moved.

package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"the-valley/change"
)

// runStanding prints where a change stands now: one line of fields
// separated by tabs,
//
//	<state> <version> <head> <since> <landed> <restack>
//
// with the state as the record says it: ready, draft or withdrawn; the
// active version and its head as a full object ID; since, the time the
// record last changed, read from the record commit's committer date, in
// UTC, as hours and minutes on the current day and with the date before
// them otherwise; landed, v<n> when the change landed as version n
// (rebase.Snapshot.LandedAs), by its head on main or a verified landing
// record, and empty otherwise; and restack, "conflict" when the change's
// restack report is current and says rebasing it onto main conflicted,
// and empty otherwise. It fails when origin holds no record of the
// change.
func runStanding(g git, args []string, w io.Writer) error {
	if len(args) != 1 || !change.ValidID(args[0]) {
		return failure("usage: valley-go standing <change ID>")
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	s, err := standingOf(g, args[0])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.state, s.version, s.head, s.since, s.landed, s.conflict)
	return err
}

// standing is the line runStanding prints, one field each.
type standing struct {
	state, version, head, since, landed, conflict string
}

// standingOf reads where the change id stands now, as runStanding prints
// it.
func standingOf(g git, id string) (standing, error) {
	o, n, found, err := lookupRecorded(g, id)
	if err != nil {
		return standing{}, err
	}
	if !found {
		return standing{}, failure("origin holds no record of change " + id)
	}
	s, err := readRecorded(g, o, n, io.Discard)
	if err != nil {
		return standing{}, err
	}
	out, err := g.output(true, "show", "-s", "--format=%ct", n.commit)
	if err != nil {
		return standing{}, failure("could not read the record commit of change " + id)
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return standing{}, failure("the record commit of change " + id + " has no committer date")
	}
	landed, conflict := "", ""
	if s.recorded.landedAs != 0 {
		landed = fmt.Sprintf("v%d", s.recorded.landedAs)
	}
	if s.restack != nil && s.restack.Current && s.restack.Report.Result == change.RestackConflict {
		conflict = "conflict"
	}
	v := n.record.ActiveVersion()
	return standing{state: n.record.State, version: strconv.Itoa(v.N), head: headOf(v),
		since: clockTime(time.Unix(sec, 0), time.Now()), landed: landed, conflict: conflict}, nil
}

// clockTime is t in UTC as hours and minutes, with the date before them
// unless t falls on the same UTC day as now.
func clockTime(t, now time.Time) string {
	t, now = t.UTC(), now.UTC()
	if t.Format(time.DateOnly) == now.Format(time.DateOnly) {
		return t.Format("15:04Z")
	}
	return t.Format("2006-01-02 15:04Z")
}
