package main

// Main's state: whether the checks that follow a project's classes have
// passed on main since the landings that set them due.
//
// A landing that touches a class sets the checks that follow it due on
// main, without requiring them of the change (../derive). A follow check
// is due when a landing since the newest main commit whose tree holds a
// pass for it touched a class it follows. mainFollows finds that under the
// policy at main's tip, read without asking which requirements are
// mandatory (composed.ReadUnpinned), by walking main's first parent from
// the tip, at most followWalk commits. At each commit it reads the
// evidence origin holds for the commit's tree, from one listing of
// origin's attestation refs for the whole walk, judged as coverage judges
// it (coverage.go),
// and then derives the commit's own follows from its diff against its
// first parent, as valley checks does, and the change ID of each landing
// that sets a check due. A check whose pass the walk does
// not reach is due when a landing the walk read set it due, and when the
// walk stopped at its bound rather than at main's first commit, since a
// landing past the bound may have. The bound keeps a read of main's state
// to about two seconds however long main is, most of it a tree digest per
// commit; only follow checks of the nix runner are read.
//
// The check worker (worker.go) runs the due checks on main's tip, and
// valley pending shows the same walk, with the worker's telemetry, as one
// line (mainLines). Main is clear when no follow check is due, clearing
// when some are due or running and none failed, and muddy when a follow
// check's newest run on main failed since its last pass, in its build or
// its evaluation. The line names the landings the due checks cover as
// valley pending names a change: by handle, or by short commit ID for a
// main commit that carries no change ID. Nothing of this
// is stored: a pass is an attestation on a main tree, and a run and its
// result are the worker's telemetry (checkcost.go reads it).

import (
	"cmp"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// followWalk is how many of main's commits a walk reads at most.
const followWalk = 20

// followCheck is one nix check that follows a class of the policy at
// main's tip, as the walk found it.
type followCheck struct {
	nixCheck
	due    bool
	covers []string // the landings since its newest pass that set it due, newest first
	passed string   // the main commit whose tree holds its newest pass, or "" when the walk reached none
}

// mainWalk is what mainFollows read of main.
type mainWalk struct {
	checks []followCheck     // every nix follow check the policy declares, by name
	walked []string          // the commits read, newest first
	trees  map[string]string // the newest commit read at each tree digest
	ids    map[string]string // the change ID each landing in a check's covers carries
	tip    string            // the tip's tree digest
}

// mainFollows walks main from tip under the two policy layers named. It
// returns no checks when the policy declares no nix follow check.
func mainFollows(g git, tip, instance, project string, stderr io.Writer) (mainWalk, error) {
	schema, err := schemaFile(g, "")
	if err != nil {
		return mainWalk{}, err
	}
	model, err := policyModel(schema, instance, project, false)
	if err != nil {
		return mainWalk{}, err
	}
	declared := map[string]bool{}
	for _, c := range model.Classes {
		for _, name := range c.Follows {
			declared[name] = true
		}
	}
	var names []string
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	w := mainWalk{trees: map[string]string{}, ids: map[string]string{}}
	open := map[string]int{} // each check whose pass the walk has not reached, by name, to its index
	for _, name := range names {
		if q := model.Checks[name]; q.Runner == "nix" {
			open[name] = len(w.checks)
			w.checks = append(w.checks, followCheck{nixCheck: nixCheck{name, q.Attribute}})
		}
	}
	if len(w.checks) == 0 {
		return w, nil
	}
	out, err := g.output(true, "rev-list", "--first-parent", "--max-count="+strconv.Itoa(followWalk), tip)
	if err != nil {
		return mainWalk{}, failure("could not walk main from " + tip)
	}
	// One listing of origin's attestation refs, and one read of the host's
	// signers, serve every tree the walk judges.
	o, err := listOrigin(g, attestationRefs+"*")
	if err != nil {
		return mainWalk{}, failure("could not list the evidence origin holds")
	}
	var keys hostKeys
	defer keys.done()
	rooted := false // whether the walk reached main's first commit
	for _, commit := range strings.Fields(out) {
		var asked []nixCheck
		for _, q := range w.checks {
			if _, ok := open[q.name]; ok {
				asked = append(asked, q.nixCheck)
			}
		}
		e, err := coverageIn(g, commit, o.refs, asked, &keys, stderr)
		if err != nil {
			return mainWalk{}, err
		}
		if len(w.walked) == 0 {
			w.tip = e.digest
		}
		w.walked = append(w.walked, commit)
		if _, ok := w.trees[e.digest]; !ok {
			w.trees[e.digest] = commit // the newest commit at a tree a revert brought back
		}
		for name, i := range open {
			if _, ok := e.covered[name]; ok {
				w.checks[i].passed = commit
				delete(open, name)
			}
		}
		parent, err := g.output(true, "rev-parse", "--verify", "--quiet", commit+"^1")
		rooted = err != nil // a root commit lands nothing
		if len(open) == 0 || rooted {
			break
		}
		doc, err := deriveUnder(g, model, parent, commit)
		if err != nil {
			return mainWalk{}, err
		}
		for _, f := range doc.Follows {
			if i, ok := open[f.Name]; ok {
				w.checks[i].due = true
				w.checks[i].covers = append(w.checks[i].covers, commit)
				if _, read := w.ids[commit]; !read {
					// A commit whose IDs do not read is named by its
					// commit ID, as one that carries none is.
					header, trailer, _ := headIDs(g, commit)
					w.ids[commit] = cmp.Or(header, trailer)
				}
			}
		}
	}
	if !rooted && len(w.walked) == followWalk {
		for _, i := range open {
			w.checks[i].due = true
		}
	}
	return w, nil
}

// landings are the landings the due checks cover, newest first, each
// named once as valley pending names a change: by the handle h gives the
// change ID its commit carries, and otherwise by its short commit ID.
func (w mainWalk) landings(h handleMap, project string) []string {
	index := map[string]int{}
	for i, c := range w.walked {
		index[c] = i
	}
	var covers []string
	for _, q := range w.checks {
		covers = append(covers, q.covers...)
	}
	sort.SliceStable(covers, func(a, b int) bool { return index[covers[a]] < index[covers[b]] })
	var named []string
	for i, c := range covers {
		if i > 0 && c == covers[i-1] {
			continue
		}
		name := h[handleKey{project, recordTarget, w.ids[c]}]
		if name == "" {
			name = short(c)
		}
		named = append(named, name)
	}
	return named
}

// mainRun is one batch the worker ran on a main tip, as its telemetry
// records it: its start, or its end.
type mainRun struct {
	project, tree, run string
	checks             []string
	ended              bool
}

// mainLines is a project's main state as valley pending's first lines,
// the first led by lead: the state, and a see: line when a check failed.
// h names the landings covered, and builds and runs are the worker's
// telemetry, newest first.
func (t terminal) mainLines(w mainWalk, lead, project string, h handleMap, builds []checkBuild, runs []mainRun) []string {
	index := map[string]int{}
	for i, c := range w.walked {
		index[c] = i
	}
	ended := map[string]bool{}
	for _, r := range runs {
		if r.ended {
			ended[r.run] = true
		}
	}
	running := map[string]bool{}
	for _, r := range runs {
		if !r.ended && !ended[r.run] && r.project == project && r.tree == w.tip {
			for _, c := range r.checks {
				running[c] = true
			}
		}
	}
	var failed, due []string
	var failedAt string
	newest := ""
	for _, q := range w.checks {
		if q.passed != "" && (newest == "" || index[q.passed] < index[newest]) {
			newest = q.passed
		}
		for _, b := range builds {
			if b.project != project || b.change != "" || b.check != q.name {
				continue
			}
			at, ok := w.trees[b.tree]
			if b.result != "passed" && ok && (q.passed == "" || index[at] < index[q.passed]) {
				failed = append(failed, q.name)
				if failedAt == "" || index[at] < index[failedAt] {
					failedAt = at
				}
			}
			break // the check's newest run on main
		}
		if q.due {
			state := " due"
			if running[q.name] {
				state = " running"
			}
			due = append(due, q.name+state)
		}
	}
	shown := w.landings(h, project)
	switch {
	case len(failed) > 0:
		line := t.coverage(lead+t.tint(colourRefused, "✗")+" muddy · "+strings.Join(failed, ", ")+" failed at "+short(failedAt), shown)
		return []string{line, "  see: valley telemetry read --source worker"}
	case len(due) > 0:
		return []string{t.coverage(lead+t.tint(colourWorking, "◐")+" clearing · "+strings.Join(due, ", "), shown)}
	case newest != "":
		return []string{lead + t.tint(colourDone, "✓") + " clear · checks passed at " + short(newest)}
	}
	return []string{lead + t.tint(colourDone, "✓") + " clear"}
}

// coverage is line with the landings it covers, as many as fit the
// terminal, and how many more there are.
func (t terminal) coverage(line string, covers []string) string {
	if len(covers) == 0 {
		return line
	}
	for n := len(covers); n > 0; n-- {
		s := line + " · covers " + strings.Join(covers[:n], ", ")
		if n < len(covers) {
			s += fmt.Sprintf(" and %d more", len(covers)-n)
		}
		if n == 1 || columns(s) <= t.width {
			return s
		}
	}
	return line
}
