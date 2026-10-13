// Changes and their records on origin.
//
// A change is named in one of three ways, and every verb that takes a
// change takes all three:
//
//   - its full change ID, 32 letters from k to z;
//   - its handle: the shortest prefix of its ID, four letters at least,
//     that no other change recorded on origin shares;
//   - its branch, the way changes were named before they had records.
//
// A name shaped like a handle is looked up among origin's records first.
// When it matches no record, it is a branch name. A name written as
// origin/<branch> is always a branch name. A handle that is also the name
// of a branch on origin is refused unless the change is on that branch,
// because the two could be different changes.

package main

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"the-valley/change"
)

// recordTarget is the one target a change may name for now. The hook
// refuses a record for any other.
const recordTarget = "main"

// origin is one ls-remote snapshot of the refs a verb reads.
type origin struct {
	refs map[string]string // ref → object ID
}

// listOrigin takes one snapshot of origin's refs matching the patterns.
// ls-remote matches a pattern against the tail of a ref name, so every ref
// is compared whole afterwards.
func listOrigin(g git, patterns ...string) (origin, error) {
	out, err := g.output(false, append([]string{"ls-remote", "--refs", "origin"}, patterns...)...)
	if err != nil {
		return origin{}, failure("could not read origin")
	}
	o := origin{refs: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		oid, ref, ok := strings.Cut(line, "\t")
		if ok && ref != "" {
			o.refs[ref] = oid
		}
	}
	return o, nil
}

// recordPatterns are the ls-remote patterns that list every record.
var recordPatterns = []string{change.RefPrefix + recordTarget + "/*"}

// changes is the ID of every change origin holds a record for, in order.
func (o origin) changes() []string {
	var ids []string
	for ref := range o.refs {
		if target, id, ok := change.ParseRef(ref); ok && target == recordTarget {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// handle is the change's handle among the changes origin holds, and this
// one, which may not be among them yet.
func (o origin) handle(id string) string {
	return change.Handle(id, append(o.changes(), id))
}

// fetch brings the objects named into this checkout's object store,
// without writing FETCH_HEAD, moving a ref or fetching tags.
func fetch(g git, oids ...string) error {
	var stdin bytes.Buffer
	for _, oid := range oids {
		if oid != "" {
			fmt.Fprintln(&stdin, oid)
		}
	}
	if stdin.Len() == 0 {
		return nil
	}
	_, err := g.input(false, &stdin, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--stdin", "origin")
	if err != nil {
		return failure("could not fetch from origin")
	}
	return nil
}

// readRecord reads the record a record commit holds. The commit must have
// been fetched.
func readRecord(g git, commit string) (*change.Record, error) {
	data, err := g.output(true, "cat-file", "blob", commit+":record.json")
	if err != nil {
		return nil, failure(fmt.Sprintf("the change record %s holds no record.json", short(commit)))
	}
	r, err := change.Parse([]byte(data + "\n"))
	if err != nil {
		return nil, failure(fmt.Sprintf("the change record %s does not read: %v", short(commit), err))
	}
	return r, nil
}

// recorded is a change that a name resolved to: its ID, and its record on
// origin.
type recorded struct {
	id     string
	commit string // the record commit origin's ref points at
	record *change.Record
}

// lookup resolves a name to a change origin holds a record for. found is
// false when the name is a branch name rather than a change's: it is
// written as origin/<branch>, it has no handle's shape, or it matches no
// record. A name that matches more than one record is refused.
func lookup(g git, o origin, name string) (n recorded, found bool, err error) {
	if strings.HasPrefix(name, "origin/") || !change.LooksLikeHandle(name) {
		return recorded{}, false, nil
	}
	ids := o.changes()
	matches := change.Matching(name, ids)
	switch {
	case len(matches) == 0:
		return recorded{}, false, nil
	case len(matches) > 1:
		var handles []string
		for _, id := range matches {
			handles = append(handles, change.Handle(id, ids))
		}
		return recorded{}, false, failure(fmt.Sprintf("%s could name any of the changes %s; give more of its ID",
			name, strings.Join(handles, ", ")))
	}
	n = recorded{id: matches[0], commit: o.refs[change.Ref(recordTarget, matches[0])]}
	if err := fetch(g, n.commit); err != nil {
		return recorded{}, false, err
	}
	if n.record, err = readRecord(g, n.commit); err != nil {
		return recorded{}, false, err
	}
	if branch := n.record.ActiveVersion().Branch; o.refs["refs/heads/"+name] != "" && branch != name {
		return recorded{}, false, failure(fmt.Sprintf("%s is a branch on origin and the handle of the change %s; name the branch as origin/%s, or the change by its full ID",
			name, n.id, name))
	}
	return n, true, nil
}

// resolution is what the name of a change stands for. A name that is a
// branch's sets branch alone. A name that is a change's sets every field
// from the change's record on origin: the active version, the head that
// version names, and the branch it names.
type resolution struct {
	id, handle, head, branch string
	version                  int
}

// resolve reads a change's name, as every verb that takes a change does.
// A name with no handle's shape is a branch name without origin being
// read. A change whose active version names no branch is refused, since
// every verb that resolves a name acts on a branch too.
func resolve(g git, name string) (resolution, error) {
	if strings.HasPrefix(name, "origin/") || !change.LooksLikeHandle(name) {
		return resolution{branch: strings.TrimPrefix(name, "origin/")}, nil
	}
	o, err := listOrigin(g, append([]string{"refs/heads/" + name}, recordPatterns...)...)
	if err != nil {
		return resolution{}, err
	}
	n, found, err := lookup(g, o, name)
	switch {
	case err != nil:
		return resolution{}, err
	case !found:
		return resolution{branch: name}, nil
	}
	v := n.record.ActiveVersion()
	r := resolution{
		id: n.id, handle: o.handle(n.id), version: v.N,
		head: strings.TrimPrefix(v.Head, change.HeadPrefix), branch: v.Branch,
	}
	if r.branch == "" {
		return resolution{}, failure(fmt.Sprintf("the change %s names no branch at v%d, so there is no branch to act on", r.handle, v.N))
	}
	return r, nil
}

// resolveBranch is the branch a change is on: the branch its active
// version names, when the name is a change's, and otherwise the name
// itself, as a branch name.
func resolveBranch(g git, name string) (string, error) {
	r, err := resolve(g, name)
	return r.branch, err
}

// runResolve prints what a change's name stands for, for the verbs
// bin/valley runs itself, as one line of fields separated by tabs. For a
// change it is
//
//	change <ID> <handle> <version> <head> <branch>
//
// with the head as a full object ID. For a branch it is
//
//	branch <branch>
//
// A branch name holds no tab, since git refuses control characters in a
// ref name. The record commit, which has the head as a parent, has been
// fetched, so the head is in this checkout's objects.
func runResolve(g git, args []string, w io.Writer) error {
	if len(args) != 1 {
		return failure("usage: valley-go resolve <change>")
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	r, err := resolve(g, args[0])
	if err != nil {
		return err
	}
	if r.id == "" {
		_, err = fmt.Fprintf(w, "branch\t%s\n", r.branch)
		return err
	}
	_, err = fmt.Fprintf(w, "change\t%s\t%s\t%d\t%s\t%s\n", r.id, r.handle, r.version, r.head, r.branch)
	return err
}

// project is the name a change's handle is printed under: origin's
// repository, as its URL names it, without .git.
func project(g git) string {
	url, err := g.output(true, "remote", "get-url", "origin")
	if err != nil {
		return "origin"
	}
	url = strings.TrimRight(url, "/")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}
	if name := strings.TrimSuffix(url, ".git"); name != "" {
		return name
	}
	return "origin"
}

// writeRecord writes a record commit holding r, with the parents given,
// and returns its ID. Its tree holds record.json alone, as a plain file,
// which is the only shape the hook accepts.
func writeRecord(g git, r *change.Record, message string, parents ...string) (string, error) {
	data, err := change.Encode(r)
	if err != nil {
		return "", err
	}
	if len(data) > maxRecordBlob {
		return "", failure(fmt.Sprintf("the change's record would be %d bytes, more than the %d a record may be", len(data), maxRecordBlob))
	}
	blob, err := g.input(false, bytes.NewReader(data), "hash-object", "-w", "--stdin")
	if err != nil {
		return "", failure("could not write the record")
	}
	tree, err := g.input(false, strings.NewReader("100644 blob "+blob+"\trecord.json\n"), "mktree")
	if err != nil {
		return "", failure("could not write the record's tree")
	}
	args := []string{"commit-tree", tree}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	commit, err := g.input(false, strings.NewReader(message+"\n"), append(args, "-F", "-")...)
	if err != nil {
		return "", failure("could not write the record commit")
	}
	return commit, nil
}

// maxRecordBlob is the most bytes a record.json may hold, as the hook
// bounds it.
const maxRecordBlob = 64 << 10

// pushRecord moves the change's record ref on origin from old to commit,
// leased on old: the push is refused if the ref has moved since it was
// read, and a record that did not exist must still not exist. The hook's
// refusal reaches stderr through git.
func pushRecord(g git, id, old, commit string) error {
	ref := change.Ref(recordTarget, id)
	_, err := g.output(false, "push", "--quiet", "--force-with-lease="+ref+":"+old, "origin", commit+":"+ref)
	if err != nil {
		return failure("origin did not take the record of " + id)
	}
	return nil
}
