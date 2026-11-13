package main

// Conflicts between ready changes: which of a project's ready changes
// conflict textually with each other, so the operator sees which approval
// costs another change a rebase. valley pending marks them under each
// row, and valley-pending/v1 and valley-changes/v1 carry them on each
// ready change that has not landed.
//
// The candidates are a project's ready changes that have not landed as
// any version (pendingChange). Two candidates are a pair unless one's
// head is an ancestor of the other's: the layers of one stack land
// together and are never paired. A pair is compared on T, main's tip on
// origin as this run read it, and only when T is an ancestor of both
// heads. It conflicts when git merge-tree --write-tree --merge-base=T A B
// leaves paths conflicted (carry, carried.go), and those are its paths.
// That is the merge the integrator makes of the second change once the
// first lands: it writes the first as one commit on T whose tree is the
// first's head's, then merges the second's head into the new tip from
// their merge base, which is T, since no head holds the landing commit
// (mergeDelta, integrator/git.go). The two heads' own merge base is not
// used: sibling heads can share an unlanded ancestor, and a merge from it
// reports a conflict the integrator never meets, where one head restores
// a line the ancestor changed and the other changes it again. One merge
// serves either landing order, since a three-way merge conflicts on the
// same paths whichever side is ours.
//
// A pair with a head T is not an ancestor of is awaiting restack: the
// restacker rebases each ready change onto main, and the pair is compared
// once both are. A merge that does not run or parse is not computed. At
// most pairBound merges run in one invocation, and the pairs past it are
// not computed either. A pair not computed is listed, with why, on both
// its changes, so a missing answer never reads as a clean one.
//
// A pair's answer depends only on T and the two heads, so it is
// remembered in the client cache (rebase.RememberPair): a remembered
// answer runs no merge and counts nothing against the bound. The merges
// run in a scratch git directory per project (carryGit), made at its
// first merge.

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"the-valley/change"
	"the-valley/rebase"
)

// pairBound is the most merges one invocation runs to compare pairs. Each
// is one git merge-tree, and a cold listing of n ready changes asks for up
// to n(n-1)/2 of them.
var pairBound = 64

// Why a pair was not compared, as conflicts_unknown gives it.
const (
	whyRestack = "awaiting restack"
	whyBound   = "past the bound"
	whyFailed  = "merge failed"
)

// jsonConflict is a conflict with another ready change: its change ID and
// handle, the first of the conflicted paths in sorted order that are
// UTF-8, and how many paths conflict in all.
type jsonConflict struct {
	ChangeID   string   `json:"change_id"`
	Handle     string   `json:"handle"`
	Paths      []string `json:"paths"`
	PathsTotal int      `json:"paths_total"`
}

// jsonConflictUnknown is another ready change a change was not compared
// with, and why: whyRestack, whyBound or whyFailed.
type jsonConflictUnknown struct {
	ChangeID string `json:"change_id"`
	Handle   string `json:"handle"`
	Why      string `json:"why"`
}

// pairing is what one invocation has left for comparing pairs: the merges
// it may still run, and the client cache directory.
type pairing struct {
	left  int
	cache string
}

func newPairing() *pairing { return &pairing{left: pairBound, cache: rebase.CacheDir()} }

// addConflicts gives each ready change in view that has not landed its
// conflicts with the others and the pairs not computed, each an empty
// list when there are none. A merge that fails says so on stderr.
func (l listed) addConflicts(view *jsonRepo, p *pairing, stderr io.Writer) {
	if l.err != nil || p == nil {
		return
	}
	var cands []int
	for i, c := range view.Changes {
		if c.State == change.Ready && c.LandedVersion == nil {
			cands = append(cands, i)
			view.Changes[i].Conflicts = &[]jsonConflict{}
			view.Changes[i].ConflictsUnknown = &[]jsonConflictUnknown{}
		}
	}
	if len(cands) < 2 {
		return
	}
	defer phase("conflicts")()
	s := l.snap
	heads, onTip := map[int]string{}, map[int]bool{}
	for _, i := range cands {
		c := view.Changes[i]
		h := c.Versions[c.Active-1].Head
		heads[i], onTip[i] = h, s.tip != "" && (h == s.tip || s.strictAncestor(s.tip, h))
	}
	var cg git
	done := func() {}
	defer func() { done() }()
	for x, i := range cands {
		for _, j := range cands[x+1:] {
			a, b := heads[i], heads[j]
			if a == b || s.strictAncestor(a, b) || s.strictAncestor(b, a) {
				continue
			}
			pair, why := rebase.Pair{}, ""
			remembered := false
			if onTip[i] && onTip[j] {
				pair, remembered = rebase.RememberedPair(p.cache, s.tip, a, b)
			}
			switch {
			case remembered:
			case !onTip[i] || !onTip[j]:
				why = whyRestack
			case p.left == 0:
				why = whyBound
			default:
				p.left--
				var err error
				if cg.dir == "" {
					if cg, done, err = carryGit(s.g); err != nil {
						done()
						cg, done = git{}, func() {}
					}
				}
				var c carried
				if err == nil {
					c, err = carry(cg, s.tip, a, b)
				}
				if err != nil {
					why = whyFailed
					fmt.Fprintf(stderr, "valley: %s: whether %s and %s conflict did not read: %v\n",
						l.name, view.Changes[i].Handle, view.Changes[j].Handle, err)
					break
				}
				paths := c.paths()
				pair = rebase.RememberPair(p.cache, s.tip, a, b, rebase.Pair{Total: len(paths), Paths: paths})
			}
			markPair(view, i, j, pair, why)
			markPair(view, j, i, pair, why)
		}
	}
}

// markPair puts on change i of view what comparing it with change j found.
func markPair(view *jsonRepo, i, j int, pair rebase.Pair, why string) {
	other := view.Changes[j]
	c := &view.Changes[i]
	switch {
	case why != "":
		*c.ConflictsUnknown = append(*c.ConflictsUnknown, jsonConflictUnknown{other.ChangeID, other.Handle, why})
	case pair.Total > 0:
		paths := []string{}
		for _, p := range pair.Paths {
			if utf8.ValidString(p) {
				paths = append(paths, p)
			}
		}
		*c.Conflicts = append(*c.Conflicts, jsonConflict{other.ChangeID, other.Handle, paths, pair.Total})
	}
}

// conflictLines are the lines under a row for a change's conflicts: one
// naming the changes it conflicts with, or, with full, one for each such
// change naming the conflicted paths.
func conflictLines(c jsonChange, full bool) []string {
	var lines, names []string
	if c.Conflicts != nil {
		for _, x := range *c.Conflicts {
			names = append(names, x.Handle)
			paths := []string{}
			for _, p := range x.Paths {
				paths = append(paths, readable(p))
			}
			say := strings.Join(paths, ", ")
			if len(paths) > 3 || x.PathsTotal > len(paths) {
				say = fmt.Sprintf("%s and %d more", strings.Join(paths[:min(3, len(paths))], ", "), x.PathsTotal-min(3, len(paths)))
			}
			if len(paths) == 0 {
				say = counted(x.PathsTotal, "path")
			}
			lines = append(lines, "conflicts with "+x.Handle+": "+say)
		}
	}
	if full || len(names) == 0 {
		return lines
	}
	return []string{"conflicts with " + strings.Join(names, ", ")}
}

// uncompared is the line saying how many pairs among the listed changes
// were not compared, and why, or "" when every pair was.
func uncompared(all []*entry) string {
	type pairKey struct{ repo, a, b string }
	whys := map[pairKey]string{}
	for _, e := range all {
		for _, x := range e.unknown {
			k := pairKey{e.repo, min(e.id, x.ChangeID), max(e.id, x.ChangeID)}
			whys[k] = x.Why
		}
	}
	if len(whys) == 0 {
		return ""
	}
	count := map[string]int{}
	for _, why := range whys {
		count[why]++
	}
	var parts []string
	for _, why := range []string{whyRestack, whyBound, whyFailed} {
		if n := count[why]; n > 0 {
			if why == whyBound {
				why = fmt.Sprintf("past the bound of %d merges", pairBound)
			}
			parts = append(parts, fmt.Sprintf("%d %s", n, why))
		}
	}
	if len(parts) == 1 {
		parts[0] = strings.TrimLeft(parts[0], "0123456789 ")
	}
	noun := "pairs"
	if len(whys) == 1 {
		noun = "pair"
	}
	return fmt.Sprintf("%d %s not compared: %s", len(whys), noun, strings.Join(parts, ", "))
}
