package server

import (
	"context"

	"the-valley/web/export"
)

// projectForReader is what one reader's index is built from: a copy of
// each project of e that shown names, in e's order, with a copy of each of
// its knots, and the groups those projects belong to. It never changes e,
// which every reader's views share, and nothing changes the copies once
// they are made.
//
// A copied knot keeps its own fields, its State and its Ready as the host
// derived them, and what it says of the projects shown names. What it says
// of any other project's knots is taken out:
//
//   - WaitingOn keeps the references into projects shown names, and
//     WaitingOnHidden is set when the export set it or when a reference
//     was taken out of WaitingOn. It says neither which nor how many.
//   - ClearedByEnding, Dangling and Unresolved keep the references into
//     projects shown names alone.
//   - In each of those lists, a reference is a knot's ID in the knot's own
//     graph, or <project>:<id> (knot/validate.go), each whole (knotRef).
//     An entry of neither shape names no knot the reader may see, whatever
//     it opens with, and is taken out as a reference into a project shown
//     does not name is.
//   - An edge into a project shown does not name is hidden: it keeps its
//     field and reference, and loses its state, title, status and why,
//     whatever the export said of them. An edge into a project shown names
//     keeps what the export said.
//
// BlockedBy, Target, GraduatedInto and Changes are kept as they are, and
// every page names them through the index, which names nothing of a
// project it does not hold (reference, target, graduated, change and
// waitsOn). Nothing is inferred from BlockedBy: a blocker may be done.
//
// It checks ctx before each project, each knot, and each entry of a
// knot's lists and edges, so a cancelled build stops promptly.
func projectForReader(ctx context.Context, e *export.Export, shown map[string]bool) (*export.Export, error) {
	out := &export.Export{Schema: e.Schema, Collected: e.Collected, Projects: []export.Project{}}
	used := map[string]bool{}
	for i := range e.Projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := e.Projects[i]
		if !shown[p.Name] {
			continue
		}
		used[p.Group] = true
		p.Knots = make([]export.Knot, len(e.Projects[i].Knots))
		for j := range e.Projects[i].Knots {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			k, err := projectKnot(ctx, p.Name, e.Projects[i].Knots[j], shown)
			if err != nil {
				return nil, err
			}
			p.Knots[j] = k
		}
		out.Projects = append(out.Projects, p)
	}
	for _, g := range e.Groups {
		if used[g.ID] {
			out.Groups = append(out.Groups, g)
		}
	}
	return out, nil
}

// projectKnot is a copy of k, a knot of project's, with what it says of
// the knots of projects shown does not name taken out, as
// projectForReader says. It checks ctx before each entry of k's lists and
// edges.
func projectKnot(ctx context.Context, project string, k export.Knot, shown map[string]bool) (export.Knot, error) {
	hidden := func(ref string) bool {
		other, _, ok := knotRef(project, ref)
		return !ok || !shown[other]
	}
	waiting, err := keep(ctx, k.WaitingOn, hidden)
	if err != nil {
		return export.Knot{}, err
	}
	k.WaitingOnHidden = k.WaitingOnHidden || len(waiting) < len(k.WaitingOn)
	k.WaitingOn = waiting
	for _, list := range []*[]string{&k.ClearedByEnding, &k.Dangling, &k.Unresolved} {
		if *list, err = keep(ctx, *list, hidden); err != nil {
			return export.Knot{}, err
		}
	}
	edges := make([]export.Edge, 0, len(k.Edges))
	for _, edge := range k.Edges {
		if err := ctx.Err(); err != nil {
			return export.Knot{}, err
		}
		if !shown[edge.Project] {
			edge = export.Edge{Field: edge.Field, Ref: edge.Ref, Project: edge.Project, ID: edge.ID, State: export.EdgeHidden}
		}
		edges = append(edges, edge)
	}
	k.Edges = edges
	return k, nil
}

// keep is a new list of the entries of list that hidden does not report,
// never nil. It checks ctx before each entry.
func keep(ctx context.Context, list []string, hidden func(string) bool) ([]string, error) {
	kept := []string{}
	for _, s := range list {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !hidden(s) {
			kept = append(kept, s)
		}
	}
	return kept, nil
}
