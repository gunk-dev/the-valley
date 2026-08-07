package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// git runs a git command in repo and returns its stdout. Stderr is folded
// into the error, because a git failure that does not say what git said is
// a failure the reader has to reproduce by hand.
func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func gitLine(repo string, args ...string) (string, error) {
	out, err := git(repo, args...)
	return strings.TrimSpace(out), err
}

// gitTry runs a git command that is allowed to fail, and reports both what
// it wrote and whether it succeeded. Used where the failure is the answer:
// a merge that conflicts, a compare-and-swap that lost.
func gitTry(repo string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err == nil
}

// worktree is a throwaway checkout attached to the served repository. The
// integrator needs one to read the policy at the target tip and another to
// evaluate check closures over the tree that would land; the served repo is
// bare, and both of those want a tree on disk.
type worktree struct {
	repo string
	dir  string
}

// scratchPrefix names every directory addWorktree creates. It is what tells
// a worktree of the integrator's own making from one somebody else attached
// to the same repository by hand, which is the difference between a leak to
// clean up and a checkout to leave alone.
const scratchPrefix = "valley-integrator-wt"

func addWorktree(repo, rev string) (*worktree, error) {
	dir, err := os.MkdirTemp("", scratchPrefix)
	if err != nil {
		return nil, err
	}
	// git worktree add refuses an existing directory; the mkdtemp is only
	// to reserve a name nothing else will take.
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if _, err := git(repo, "worktree", "add", "--quiet", "--detach", dir, rev); err != nil {
		return nil, err
	}
	return &worktree{repo: repo, dir: dir}, nil
}

func (w *worktree) remove() {
	if w == nil {
		return
	}
	_, _ = gitTry(w.repo, "worktree", "remove", "--force", w.dir)
	_ = os.RemoveAll(w.dir)
}

// sweepScratch removes the scratch worktrees a previous run left attached to
// the repository. Every tick removes its own worktree on the way out, on the
// failure path as much as the success one, so a leak needs a run that died
// between the add and the remove — a crash, a kill, a restart. The checkout
// and its administrative entry both survive that, and the scratch root is
// now a state directory that persists across restarts, so leaks would
// accumulate there without something to sweep them.
//
// A leak is found through the repository rather than the filesystem: a
// worktree is registered in the repository it is attached to, and a
// controller serves exactly one repository, so this cannot reach another
// controller's scratch even though they share a scratch root. Called once
// before the loop starts, when this process holds no worktree of its own, so
// everything carrying the prefix is a leak.
func sweepScratch(repo string) {
	out, ok := gitTry(repo, "worktree", "list", "--porcelain")
	if !ok {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		dir, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found || !strings.HasPrefix(filepath.Base(dir), scratchPrefix) {
			continue
		}
		_, _ = gitTry(repo, "worktree", "remove", "--force", dir)
		_ = os.RemoveAll(dir)
	}
	// An entry whose checkout went away by some other route is still an
	// entry, and only prune clears those.
	_, _ = gitTry(repo, "worktree", "prune")
}

// mergeDelta applies a change's delta onto a tip without a working tree:
// a three-way merge of tip and head against the base the delta was authored
// against. It writes the resulting tree into the object store and returns
// its id, or the conflict.
//
// This is the whole of the first part of the commit rule. It is deliberately
// not `git rebase`: the serialized section at the commit point is a tree
// computation and a ref write, and a rebase would want a checkout, a
// sequencer state, and a cleanup path for every way it can be interrupted.
func mergeDelta(repo, base, tip, head string) (tree string, conflict string, err error) {
	out, ok := gitTry(repo, "merge-tree", "--write-tree", "--merge-base="+base, tip, head)
	if !ok {
		return "", conflictMessage(out), nil
	}
	lines := strings.SplitN(out, "\n", 2)
	tree = strings.TrimSpace(lines[0])
	if len(tree) < 40 {
		return "", "", fmt.Errorf("merge-tree wrote no tree: %s", out)
	}
	return tree, "", nil
}

// conflictMessage picks the sentence out of a conflicted merge-tree's
// report. That report opens with the tree oid and the conflicted entries
// and ends with the informational messages, which are the part naming what
// could not be merged.
func conflictMessage(out string) string {
	var last string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CONFLICT") {
			return line
		}
		if line != "" {
			last = line
		}
	}
	return last
}
