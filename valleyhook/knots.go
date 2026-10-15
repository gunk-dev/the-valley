package main

import (
	"fmt"
	"strings"

	"the-valley/knot"
)

// The plan graph is a project's plan as data: knots at one ref,
// refs/the-valley/knots, with its own root history and one commit per
// operation (the knot module). Any named principal may write it, and no
// review stands between a write and the ref, so the hook is what holds
// every write to the graph's rules.
//
// The bounds on reading it follow from the graph's limits (knot/
// contract.go): the widest tree is one knot's comments, the walk visits
// every knot and every comment, and the hook stops reading a push's graph
// after knot.HookDeadline.
var graphBudget = budget{
	subject: "the plan graph", depth: knot.MaxDepth, entriesInTree: knot.MaxTreeEntries, entries: knot.MaxEntries,
	treeSize: knot.MaxTreeSize, fileSize: knot.MaxKnotSize, bytes: knot.MaxReadBytes, deadline: knot.HookDeadline,
}

// graphReader reads the objects of one push's graph, within graphBudget.
type graphReader interface {
	// commit reads the graph commit id names.
	commit(id string) (knot.Commit, error)
	// tree lists every tree and file below the tree id names.
	tree(id string) ([]knot.Entry, error)
	// blob reads the file id names, at most max bytes.
	blob(id string, max int) ([]byte, error)
	close()
}

type streamGraph struct{ objects *objectStream }

func (gitRepository) graph() (graphReader, error) {
	objects, err := openObjectsWithin(graphBudget)
	if err != nil {
		return nil, err
	}
	return streamGraph{objects}, nil
}

func (g streamGraph) close() { g.objects.close() }

func (g streamGraph) commit(id string) (knot.Commit, error) {
	body, err := g.objects.read(id, "commit", knot.MaxCommitSize)
	if err != nil {
		return knot.Commit{}, fmt.Errorf("%s %v", id, err)
	}
	c, err := knot.ParseCommit(body, len(id)/2)
	if err != nil {
		return knot.Commit{}, fmt.Errorf("%s: %v", id, err)
	}
	return c, nil
}

// tree walks the tree within the budget, and lists what it holds for the
// knot module to judge: the walk refuses only what would cross a bound,
// and the module refuses every entry that is not the graph's layout.
func (g streamGraph) tree(id string) ([]knot.Entry, error) {
	var entries []knot.Entry
	w := walk{objects: g.objects, hashSize: len(id) / 2, visit: func(at, mode, child string) error {
		entries = append(entries, knot.Entry{Path: at, Mode: mode, OID: child})
		return nil
	}}
	if err := w.tree(id, "", 1); err != nil {
		return nil, err
	}
	return entries, nil
}

func (g streamGraph) blob(id string, max int) ([]byte, error) {
	return g.objects.read(id, "blob", max)
}

// checkGraph decides a write to refs/the-valley/knots, and returns the
// verb and the reason that refuse it, or "" for the reason when the write
// is allowed.
//
//   - Any named principal may write it. A key that names none may not.
//   - The push that creates the ref brings one commit with no parent. An
//     update brings one commit whose only parent is the tip it replaces,
//     so a push is one operation, and the ref never rewinds. No push
//     deletes the ref: its history is the record of every write, and a
//     graph that must be recovered is recovered on the host.
//   - The commit names the pushing principal as its committer, and carries
//     a Valley-Op trailer.
//   - Its tree is exactly the graph's layout, and every knot and comment
//     the commit adds or changes holds to the module's rules, against the
//     graph before it (knot.Push): no knot or comment is deleted, no
//     comment changes, a knot keeps its id, type, created and created_by,
//     a new knot's created_by and a new comment's author name the pusher,
//     every edge resolves, and no edge forms a cycle.
func (p policy) checkGraph(principal string, u update) (verb, why string) {
	switch {
	case principal == "":
		return "write", "the plan graph takes writes only from a named principal, and a key that names no principal is none"
	case isZero(u.new):
		return "delete", "the plan graph is never deleted: its history is the record of every write to it, and a graph that must be recovered is recovered on the host"
	}
	verb = "update"
	if isZero(u.old) {
		verb = "create"
	}
	g, err := p.repo.graph()
	if err != nil {
		return verb, "the graph could not be read: " + err.Error()
	}
	defer g.close()
	c, err := g.commit(u.new)
	if err != nil {
		return verb, "its new tip " + err.Error()
	}
	switch {
	case verb == "create" && len(c.Parents) != 0:
		return verb, fmt.Sprintf("the commit that creates the graph has no parent, and this one has %d", len(c.Parents))
	case verb == "update" && (len(c.Parents) != 1 || c.Parents[0] != u.old):
		return verb, fmt.Sprintf("an update of the graph is one commit whose only parent is the tip it replaces, %s, and this one's parents are %s", u.old, parentList(c.Parents))
	case c.Committer != principal:
		return verb, fmt.Sprintf("its commit names %s as its committer, and %s is pushing it: every commit on the graph is committed by the principal that pushes it", c.Committer, principal)
	}
	entries, err := g.tree(c.Tree)
	if err != nil {
		return verb, "its tree: " + err.Error()
	}
	after, err := knot.Layout(entries)
	if err != nil {
		return verb, err.Error()
	}
	var before *knot.Index
	if verb == "update" {
		old, err := g.commit(u.old)
		if err != nil {
			return verb, "the tip it replaces, " + err.Error()
		}
		entries, err := g.tree(old.Tree)
		if err != nil {
			return verb, "the tip it replaces: its tree: " + err.Error()
		}
		if before, err = knot.Layout(entries); err != nil {
			return verb, "the tip it replaces: " + err.Error()
		}
	}
	if err := (knot.Push{Old: before, New: after, Read: g.blob, Pusher: principal}).Check(); err != nil {
		return verb, err.Error()
	}
	return verb, ""
}

func parentList(parents []string) string {
	if len(parents) == 0 {
		return "none"
	}
	return strings.Join(parents, ", ")
}
