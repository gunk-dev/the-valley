// The knots a change does: every knot in its project's plan graph whose
// changes field lists valley:<project>/<change ID>. review shows them above
// a change's diff (runDoes), and changes, pending and status --json include
// them (list.go, pending.go, render.go).
//
// The plan graph in project storage holds the links from knots to changes.
// Each command reads a project's graph once, however many changes it
// inspects. When the graph is missing or unreadable, every change gets no
// knots, and -v warns on stderr.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"the-valley/change"
	"the-valley/knot"
)

// knotRef is a knot that links a change, as JSON listings and review use it.
type knotRef struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`

	active bool
}

// line formats a knot for review:
//
//	does oc-6335749 (open): Ready changes stay landable as main moves
//
// It strips newlines, tabs and control characters even though the graph
// schema forbids them, so a malformed knot cannot break runDoes's
// tab-separated output or write escape sequences to the terminal.
func (k knotRef) line() string {
	title := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, k.Title)
	return fmt.Sprintf("does %s (%s): %s", visible(k.ID), visible(k.Status), visible(title))
}

// doers maps each change ID in a project to the knots that link it.
type doers map[string][]knotRef

// readDoers reads the project's plan graph at refs/the-valley/knots on g's
// origin and indexes the knots by linked change ID. It keeps only links of
// the form valley:<project>/<change ID>, ignoring links to other projects
// or pull requests. When the graph is missing or unreadable, it returns an
// empty map and, if verbose, warns on stderr.
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
		// IDs() visits knots in ID order; a stable sort puts active knots
		// before terminal ones while keeping each group in ID order.
		sort.SliceStable(list, func(i, j int) bool { return list[i].active && !list[j].active })
	}
	return d
}

// of returns the knots that link change id, or an empty slice so JSON
// encodes [] instead of null.
func (d doers) of(id string) []knotRef {
	if list := d[id]; list != nil {
		return list
	}
	return []knotRef{}
}

// runDoes implements `valley-go does`, which `valley review` calls to find
// the knots each change in the review does. It reads this project's plan
// graph once and prints one tab-separated line per knot, in the order the
// change IDs are given:
//
//	<change ID>\tdoes <knot ID> (<status>): <title>
//
// Changes with no linked knots produce no lines. -v warns on stderr when
// the graph is missing or unreadable.
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
