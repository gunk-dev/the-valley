// Main's history, read once and kept in memory.
//
// A listing asks whether one commit is an ancestor of another many times
// over: every change against every other, in valley changes and valley
// pending. Most of the commits asked about are on main. A snapshot reads
// main's history once, each commit with its parents, and answers those
// questions from it, without starting git for each one.

package rebase

import (
	"context"
	"errors"
	"strings"
)

// history is every commit main reaches, each with its parents, as
// `git rev-list --parents <main>` lists them.
type history struct {
	index   map[string]int // commit → its place in parents
	parents [][]int        // each commit's parents, by their places
	// below is, for each commit walked from, every commit it reaches,
	// itself included, as a set of places: bit i is the commit at place i.
	below map[int][]uint64
}

// readHistory reads every commit tip reaches, with its parents. A parent
// the listing does not also list is an error: the history read is then
// not the history git walks.
func readHistory(ctx context.Context, g Git, tip string) (*history, error) {
	out, err := g.output(ctx, true, "rev-list", "--parents", tip)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(out, "\n")
	h := &history{index: make(map[string]int, len(lines)), parents: make([][]int, len(lines)), below: map[int][]uint64{}}
	for i, line := range lines {
		commit, _, _ := strings.Cut(line, " ")
		if commit == "" {
			return nil, errors.New("git listed main's history in a form this does not read")
		}
		h.index[commit] = i
	}
	for i, line := range lines {
		for _, p := range strings.Fields(line)[1:] {
			j, ok := h.index[p]
			if !ok {
				return nil, errors.New("git listed a parent outside main's history")
			}
			h.parents[i] = append(h.parents[i], j)
		}
	}
	return h, nil
}

// has reports whether main reaches a commit.
func (h *history) has(commit string) bool {
	_, ok := h.index[commit]
	return ok
}

// reaches reports whether from is to or has it as an ancestor, both
// commits main reaches. Every commit from reaches is found once, and
// kept.
func (h *history) reaches(from, to string) bool {
	i, j := h.index[from], h.index[to]
	set, walked := h.below[i]
	if !walked {
		set = make([]uint64, (len(h.parents)+63)/64)
		set[i/64] |= 1 << (i % 64)
		for stack := []int{i}; len(stack) > 0; {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, p := range h.parents[k] {
				if set[p/64]&(1<<(p%64)) == 0 {
					set[p/64] |= 1 << (p % 64)
					stack = append(stack, p)
				}
			}
		}
		h.below[i] = set
	}
	return set[j/64]&(1<<(j%64)) != 0
}
