// The knots a change does: every knot in its project's plan graph whose
// changes lists the change, as valley:<project>/<change ID>. review shows
// them above a change's diff (runDoes), and changes, pending and status
// list them (list.go, pending.go, render.go).
//
// The change record holds no knot field. The plan lives in project
// storage, and the record holds the change's workflow, so the link is read
// from the graph's side alone. The graph is read once per command, however
// many changes it lists. A graph that is missing or does not read links no
// change. That is never an error: with -v, the command says so on stderr.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"the-valley/change"
	"the-valley/knot"
)

// knotRef is a knot as a listing names it.
type knotRef struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`

	active bool
}

// line is how review shows a knot the change does:
//
//	does oc-6335749 (open): Ready changes stay landable as main moves
//
// The graph holds no control character, and a title holds no newline or
// tab. The title is made visible all the same, so the line is one line
// whatever the graph holds.
func (k knotRef) line() string {
	title := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, k.Title)
	return fmt.Sprintf("does %s (%s): %s", visible(k.ID), visible(k.Status), visible(title))
}

// doers is one read of a project's graph: the knots that link each change,
// by change ID.
type doers map[string][]knotRef

// readDoers reads the project's graph at refs/the-valley/knots on the
// origin of g, and finds the knots that link each change in the project.
// A link names the project and the full change ID, and links to any other
// project or to a pull request are passed over. A missing graph, or one
// that does not read, gives no knots, and says why on stderr when verbose.
func readDoers(g git, project string, verbose bool, stderr io.Writer) doers {
	if !verbose {
		g.stderr = io.Discard
	}
	s, err := remote{g: g, name: project}.read()
	switch {
	case err != nil:
		if verbose {
			fmt.Fprintf(stderr, "valley: %s's plan graph could not be read, so no change shows a knot: %v\n", project, err)
		}
		return doers{}
	case s.tip == "":
		if verbose {
			fmt.Fprintf(stderr, "valley: %s has no plan graph at %s, so no change shows a knot\n", project, knot.Ref)
		}
		return doers{}
	}
	prefix := "valley:" + project + "/"
	d := doers{}
	for _, id := range s.graph.IDs() {
		k := s.graph.Knots[id]
		ref := knotRef{ID: k.ID, Type: k.Type, Status: k.Status, Title: k.Title, active: k.Active()}
		seen := map[string]bool{}
		for _, link := range k.Changes {
			cid, ok := strings.CutPrefix(link, prefix)
			if !ok || !change.ValidID(cid) || seen[cid] {
				continue
			}
			seen[cid] = true
			d[cid] = append(d[cid], ref)
		}
	}
	for _, list := range d {
		// IDs() gives each list in ID order. The stable sort puts active
		// knots ahead of the ones that have ended, so the first knot is
		// one the work still serves.
		sort.SliceStable(list, func(i, j int) bool { return list[i].active && !list[j].active })
	}
	return d
}

// of is the knots that link the change: active knots first, each group in
// ID order. It is never nil, so JSON writes an empty list.
func (d doers) of(id string) []knotRef {
	if list := d[id]; list != nil {
		return list
	}
	return []knotRef{}
}

// runDoes prints the knots each change named does, for review, which
// shows them after the line that names the change and before its diff.
// The graph is that of this repository's project, read once. One line per
// knot, in the order the changes are given, each change's knots in the
// order of:
//
//	<change ID>\tdoes <knot ID> (<status>): <title>
//
// A change no knot links prints nothing. -v says why on stderr when the
// graph is missing or does not read.
func runDoes(g git, args []string, stdout, stderr io.Writer) error {
	verbose := false
	var ids []string
	for _, a := range args {
		switch {
		case a == "-v":
			verbose = true
		case change.ValidID(a):
			ids = append(ids, a)
		default:
			return failure("usage: valley-go does [-v] <change ID>...")
		}
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	d := readDoers(g, project(g), verbose, stderr)
	var b strings.Builder
	for _, id := range ids {
		for _, k := range d.of(id) {
			fmt.Fprintf(&b, "%s\t%s\n", id, k.line())
		}
	}
	_, err := io.WriteString(stdout, b.String())
	return err
}
