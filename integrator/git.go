package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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

func addWorktree(repo, rev string) (*worktree, error) {
	dir, err := os.MkdirTemp("", "valley-integrator-wt")
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
		return "", out, nil
	}
	lines := strings.SplitN(out, "\n", 2)
	tree = strings.TrimSpace(lines[0])
	if len(tree) < 40 {
		return "", "", fmt.Errorf("merge-tree wrote no tree: %s", out)
	}
	return tree, "", nil
}

// diffPaths lists every side of a diff — an added, modified or deleted
// path, and both the old and the new path of a rename. Only used to answer
// "did this range change anything at all"; the policy's own matching is the
// deriver's, never this program's.
func diffPaths(repo, from, to string) ([]string, error) {
	out, err := git(repo, "diff", "--no-relative", "--name-only", "-M", "-z", from, to)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
