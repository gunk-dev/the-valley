package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"the-valley/change"
)

// The handle cache is what completions/_valley reads to offer a change's
// handle. It lives in the current repository, at valley/handles under the
// common git directory, and holds one line for each change that is neither
// landed nor withdrawn, tab-separated: handle, state (ready or draft),
// branch, title. Ready changes come first, then drafts, each in handle
// order. The branch is empty when the active version has none or the name
// holds a tab or a control character.
//
// Completion never fetches, so the cache is as old as the last
// valley pending or valley changes that read origin. A stale line is
// harmless: the verb resolves the name against origin anyway.

// handleLines are the cache's lines for one repository.
func (l listed) handleLines(h handleMap) []string {
	type line struct{ state, handle, text string }
	var lines []line
	for _, n := range l.snap.all {
		st := n.record.State
		if (st != change.Ready && st != change.Draft) || l.snap.isLanded(n.id, n.record.ActiveVersion()) {
			continue
		}
		v := n.record.ActiveVersion()
		handle := h[handleKey{l.name, n.record.Target, n.id}]
		branch := v.Branch
		if !printable(branch) || strings.Contains(branch, "\t") {
			branch = ""
		}
		text := fmt.Sprintf("%s\t%s\t%s\t%s", handle, st, branch, readable(v.Title))
		lines = append(lines, line{st, handle, text})
	}
	sort.Slice(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		if a.state != b.state {
			return a.state == change.Ready
		}
		return a.handle < b.handle
	})
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = ln.text
	}
	return out
}

// writeHandleCache writes the cache for the repository g is in, when
// repos lists it and read it. It never fails the verb: a cache that
// cannot be written is left as it was.
func writeHandleCache(g git, repos []listed, h handleMap) {
	here := project(g)
	for _, r := range repos {
		if r.name != here || r.err != nil || r.snap == nil {
			continue
		}
		dir, err := g.output(true, "rev-parse", "--git-common-dir")
		if err != nil || dir == "" {
			return
		}
		if !filepath.IsAbs(dir) && g.dir != "" {
			dir = filepath.Join(g.dir, dir)
		}
		dir = filepath.Join(dir, "valley")
		if os.MkdirAll(dir, 0o755) != nil {
			return
		}
		body := strings.Join(r.handleLines(h), "\n")
		if body != "" {
			body += "\n"
		}
		tmp, err := os.CreateTemp(dir, "handles.*")
		if err != nil {
			return
		}
		_, werr := tmp.WriteString(body)
		cerr := tmp.Close()
		if werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(dir, "handles")) != nil {
			os.Remove(tmp.Name())
		}
		return
	}
}
