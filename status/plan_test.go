package main

import (
	"slices"
	"testing"
	"time"

	"the-valley/knot"
)

// A root's progress counts every descendant, at any depth. An abandoned
// knot counts for nothing, and the knots under it count as any other. A
// level lists its knots in the order they were created, then by title,
// then by ID, so knots of one date and one title keep one order. valley-plan
// holds the rest of the view to a golden file.
func TestPlanCountsEveryDescendantAndOrdersByCreatedTitleID(t *testing.T) {
	g := &knot.Graph{Knots: map[string]*knot.Knot{}, Comments: map[string][]knot.Noted{}}
	add := func(typ, id, status, title, created, parent string) {
		g.Knots[id] = &knot.Knot{Type: typ, ID: id, Status: status, Title: title, Created: created,
			Parent: parent, Slug: "s"}
	}
	add("outcome", "oc-0000001", "open", "Milestone", "2026-10-02", "")
	add("outcome", "oc-0000009", "open", "Zz root", "2026-10-01", "")
	add("outcome", "oc-0000002", "done", "Zeta", "2026-10-01", "oc-0000001")
	add("bug", "bd-0000005", "closed", "Beta", "2026-10-02", "oc-0000002")
	add("outcome", "oc-0000003", "abandoned", "Alpha", "2026-10-02", "oc-0000001")
	add("bug", "bd-0000004", "open", "Left under an abandoned outcome", "2026-10-02", "oc-0000003")
	add("outcome", "oc-0000008", "open", "Beta", "2026-10-02", "oc-0000001")
	add("outcome", "oc-0000006", "open", "Beta", "2026-10-02", "oc-0000001")
	add("idea", "ida-0000007", "raw", "Beta", "2026-10-02", "oc-0000001")

	v := newPlanView(remote{}, knotState{graph: g}, time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), nil)

	if want := []string{"oc-0000009", "oc-0000001"}; !slices.Equal(v.roots, want) {
		t.Errorf("roots are %v, want %v", v.roots, want)
	}
	want := []string{"oc-0000002", "oc-0000003", "ida-0000007", "oc-0000006", "oc-0000008"}
	if got := v.children["oc-0000001"]; !slices.Equal(got, want) {
		t.Errorf("the milestone's children are %v, want %v", got, want)
	}
	want = []string{"oc-0000009", "oc-0000001", "oc-0000002", "bd-0000005", "oc-0000003", "bd-0000004",
		"ida-0000007", "oc-0000006", "oc-0000008"}
	if !slices.Equal(v.order, want) {
		t.Errorf("the tree lists %v, want %v", v.order, want)
	}
	for id, want := range map[string]knot.Progress{
		"oc-0000001": {Done: 2, Of: 6}, // oc-2 and bd-5 done; bd-4, ida-7, oc-6, oc-8 not; oc-3 abandoned
		"oc-0000002": {Done: 1, Of: 1},
		"oc-0000003": {Done: 0, Of: 1},
	} {
		if got := v.knots[id].Progress; got == nil || *got != want {
			t.Errorf("%s's progress is %v, want %v", id, got, want)
		}
	}
	if p := v.knots["oc-0000009"].Progress; p != nil {
		t.Errorf("a root with no descendants has the progress %v, want none", *p)
	}
}
