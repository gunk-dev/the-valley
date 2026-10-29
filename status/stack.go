// valley-go stack: the layers of one change's stack, for valley review,
// which reviews a stack one layer at a time, bottom first.
//
// The layers are what valley pending lists under the change (pending.go):
// each change whose active version has not landed, and each branch on
// origin that no record is on, that main does not reach and that no landed
// version's head reaches, whose head the change's head strictly contains. A ready change, a draft and a branch
// are layers, as in pending. A withdrawn change that has not landed is a
// layer too, where pending names it only in an includes line: the
// integrator refuses to land anything above it, and review warns of that.
//
// Each layer is on its base, the nearest layer its head contains, as
// pending reads a base. The layers printed are the chain of bases from the
// change down. Linear history makes every layer under the change part of
// that chain. A layer off the chain, as at a fork, is not printed, and its
// commits are in the diff of the first layer on the chain that contains
// them.

package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"the-valley/rebase"
)

// runStack prints the layers of a change's stack, bottom first, then the
// change itself, one line each, of fields separated by tabs:
//
//	<kind> <name> <base> <head> <state> <title>
//
// The kind is change or branch. The name is what review takes: a change's
// handle, or a branch's name. The base is the head of the layer below, or
// for the bottom layer its merge base with main. The head of a change is
// the head its active version records. The state is ready, draft or
// withdrawn, or - for a branch. The title is last: a change's is its
// record's, and a branch's is the subject of its first own commit, as
// pending reads it. No field holds a tab: git refuses control characters
// in a ref name, and a title's are made spaces. A change with nothing
// under it prints one line, its own.
//
// The change is named as resolve reads a name. Its head is read again
// from the snapshot the layers are read from, so every line is of one
// read of origin. The layers are the chain valley rebase moves, as the
// rebase engine captures it (rebase.Capture).
func runStack(g git, args []string, w io.Writer) error {
	if len(args) != 1 {
		return failure("usage: valley-go stack <change>")
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	r, err := resolveRecorded(g, args[0])
	if err != nil {
		return err
	}
	s, err := readSnapshot(g, newVerifier())
	if err != nil {
		return err
	}
	if s.tip == "" {
		return failure("origin has no " + recordTarget + " to read a stack against")
	}
	f, err := rebase.Capture(s.land, rebase.Selection{ID: r.id, Branch: r.branch})
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, l := range f.Layers {
		if !utf8.ValidString(l.Name) {
			return failure(fmt.Sprintf("the stack under %s holds the branch %s, whose name is not UTF-8", args[0], shellQuote(l.Name)))
		}
		var base string
		if l.Below != nil {
			base = l.Below.Head
		} else if base, err = s.g.output(true, "merge-base", s.tip, l.Head); err != nil {
			return failure(fmt.Sprintf("%s and %s share no history", l.Name, recordTarget))
		}
		kind, state, title := "branch", "-", ""
		if l.ID != "" {
			kind, state, title = "change", l.Record.State, readable(l.Record.ActiveVersion().Title)
		} else {
			not := []string{"^" + s.tip}
			if l.Below != nil {
				not = []string{"^" + l.Below.Head, "^" + s.tip}
			}
			title = s.branchTitle(l.Head, not)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", kind, l.Name, base, l.Head, state, title)
	}
	_, err = io.WriteString(w, b.String())
	return err
}
