package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// carried is the approved version's head H₀ carried mechanically onto the
// shown version's base B₁: the tree git merge-tree --write-tree
// --merge-base=B₀ B₁ H₀ writes, the merge the integrator makes, and the
// paths it could not merge.
type carried struct {
	tree      string
	conflicts map[string][]string // each conflicted path → the conflict types merge-tree names it in, in its order
}

// paths are the conflicted paths, sorted.
func (c carried) paths() []string {
	var ps []string
	for p := range c.conflicts {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}

// carryGit is git run in a scratch git directory that shares g's object
// store and nothing else, so that what the carry merges and the diffs read
// from it depend on the objects alone, as the comment at the top of
// compare.go says. done removes the directory.
func carryGit(g git) (c git, done func(), err error) {
	done = func() {}
	objects, err := g.output(true, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil || objects == "" {
		return c, done, errors.New("the clone's object store is not found")
	}
	dir, err := os.MkdirTemp("", "valley-carry")
	if err != nil {
		return c, done, errors.New("no scratch git directory could be made")
	}
	done = func() { os.RemoveAll(dir) }
	var env []string
	for _, kv := range g.env {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.attributesFile", "GIT_CONFIG_VALUE_0=/dev/null")
	c = git{env: env, stderr: g.stderr, dir: dir}
	if _, err := c.output(true, "init", "--quiet", "--bare", "--template=", dir); err != nil {
		return c, done, errors.New("the scratch git directory could not be made")
	}
	c.env = append(c.env, "GIT_DIR="+dir, "GIT_OBJECT_DIRECTORY="+objects)
	return c, done, nil
}

// carry carries h0 from b0 onto b1 in cg, a carryGit. When b0 is b1, the
// carried tree is h0's and no merge runs.
func carry(cg git, b0, b1, h0 string) (carried, error) {
	if b0 == b1 {
		tree, err := cg.output(true, "--no-replace-objects", "rev-parse", "--verify", "--quiet", h0+"^{tree}")
		if err != nil {
			return carried{}, errors.New("its tree does not read")
		}
		return carried{tree: tree}, nil
	}
	out, err := cg.output(true, "--no-replace-objects", "merge-tree", "--write-tree", "-z", "--merge-base="+b0, b1, h0)
	var exit *exec.ExitError
	switch {
	case err == nil:
		tree, _, _ := strings.Cut(out, "\x00")
		if !isObjectID(tree) {
			return carried{}, errors.New("git merge-tree wrote no tree")
		}
		return carried{tree: tree}, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		c, err := parseConflicts(out)
		if err != nil {
			return carried{}, err
		}
		if len(c.conflicts) == 0 {
			return carried{}, errors.New("git merge-tree reported a conflict on no path")
		}
		return c, nil
	}
	return carried{}, fmt.Errorf("git merge-tree failed: %v", err)
}

// parseConflicts reads git merge-tree --write-tree -z's output for a
// merge with conflicts: the tree; each conflicted file entry, as
// "<mode> <object> <stage>\t<path>"; an empty field; then each message,
// as its path count, its paths, its type and its text. Every path a
// conflicted entry or a message of any type but Auto-merging names is
// conflicted, so a type this reading does not know shows more, never
// less.
func parseConflicts(out string) (carried, error) {
	bad := errors.New("git merge-tree's conflict report does not parse")
	fields := strings.Split(out, "\x00")
	if len(fields) < 2 || !isObjectID(fields[0]) || fields[len(fields)-1] != "" {
		return carried{}, bad
	}
	c := carried{tree: fields[0], conflicts: map[string][]string{}}
	fields = fields[1 : len(fields)-1]
	i := 0
	for ; i < len(fields) && fields[i] != ""; i++ {
		info, path, ok := strings.Cut(fields[i], "\t")
		if !ok || len(strings.Fields(info)) != 3 || path == "" {
			return carried{}, bad
		}
		if _, seen := c.conflicts[path]; !seen {
			c.conflicts[path] = nil
		}
	}
	if i == len(fields) {
		return carried{}, bad
	}
	for i++; i < len(fields); {
		n := 0
		if _, err := fmt.Sscanf(fields[i], "%d", &n); err != nil || fmt.Sprint(n) != fields[i] || n < 1 || i+n+2 >= len(fields) {
			return carried{}, bad
		}
		paths, kind := fields[i+1:i+1+n], fields[i+1+n]
		i += n + 3
		if kind == "Auto-merging" {
			continue
		}
		for _, p := range paths {
			if p == "" {
				return carried{}, bad
			}
			if !contains(c.conflicts[p], kind) {
				c.conflicts[p] = append(c.conflicts[p], kind)
			}
		}
	}
	return c, nil
}
