// valley-go stack: the layers of one change's stack, for valley review,
// which reviews a stack one layer at a time, bottom first.
//
// The layers are what valley pending lists under the change (pending.go):
// each change whose active version has not landed, and each branch on
// origin that no record is on and main does not reach, whose head the
// change's head strictly contains. A ready change, a draft and a branch
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
	"strconv"
	"strings"
	"unicode/utf8"
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
// read of origin.
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
	s, err := readSnapshot(g, verifierFromEnv())
	if err != nil {
		return err
	}
	if s.tip == "" {
		return failure("origin has no " + recordTarget + " to read a stack against")
	}
	layers, err := s.layers(r)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range layers {
		kind, base := "change", ""
		if e.id == "" {
			kind = "branch"
		}
		if !utf8.ValidString(e.ref) {
			return failure(fmt.Sprintf("the stack under %s holds the branch %s, whose name is not UTF-8", args[0], shellQuote(e.ref)))
		}
		if e.base != nil {
			base = e.base.head
		} else if base, err = s.g.output(true, "merge-base", s.tip, e.head); err != nil {
			return failure(fmt.Sprintf("%s and %s share no history", e.ref, recordTarget))
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", kind, e.ref, base, e.head, e.state, e.title)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// layers is the change r names and the layers under it, bottom first, each
// with its base. Only the change and the entries its head contains are
// stacked, so only they are measured.
func (s *snapshot) layers(r resolution) ([]*entry, error) {
	var top *entry
	if r.id != "" {
		for _, n := range s.all {
			if n.id == r.id {
				top = s.layerOf(n)
			}
		}
		if top == nil {
			return nil, failure("origin no longer holds a record of change " + r.handle)
		}
	} else {
		head := s.o.refs["refs/heads/"+r.branch]
		if head == "" {
			return nil, failure("no such branch on origin: " + r.branch)
		}
		top = &entry{section: "branches", ref: r.branch, head: head, state: "-"}
	}
	all := []*entry{top}
	for _, n := range s.all {
		if n.id != top.id && unlanded(s, n) && s.strictAncestor(headOf(n.record.ActiveVersion()), top.head) {
			all = append(all, s.layerOf(n))
		}
	}
	for _, b := range s.unrecorded() {
		if b.Branch != top.ref && s.strictAncestor(b.Head, top.head) {
			all = append(all, &entry{section: "branches", ref: b.Branch, head: b.Head, state: "-"})
		}
	}
	for _, e := range all {
		out, _ := s.g.output(true, "log", "-1", "--format=%ct", e.head)
		e.when, _ = strconv.ParseInt(out, 10, 64)
	}
	s.stack(all)
	var chain []*entry
	for e := top; e != nil; e = e.base {
		chain = append([]*entry{e}, chain...)
	}
	return chain, nil
}

// layerOf is a change as a layer: its handle, its active version's head
// and title, and its state. Its section is its state: what measure reads
// a section for is whether the entry is a branch.
func (s *snapshot) layerOf(n recorded) *entry {
	v := n.record.ActiveVersion()
	return &entry{
		section: n.record.State, ref: s.o.handle(n.id), id: n.id, state: n.record.State,
		head: headOf(v), title: readable(v.Title),
	}
}
