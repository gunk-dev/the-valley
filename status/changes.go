// Changes and their records on origin.
//
// A change is named in one of three ways, and every verb that takes a
// change takes all three:
//
//   - its full change ID, 32 letters from k to z;
//   - its handle: the shortest prefix of its ID, four letters at least,
//     that no other change recorded on origin shares;
//   - its handle qualified by this repository's name, <repo>:<handle>,
//     the way changes and pending write the handle of a change ID that
//     several repositories record;
//   - its branch, the way changes were named before they had records.
//
// A name shaped like a handle is looked up among origin's records first.
// When it matches no record, it is a branch name. A name written as
// origin/<branch> is always a branch name. A handle that is also the name
// of a branch on origin is refused unless the change is on that branch,
// because the two could be different changes.
//
// The verbs that act on a change, and status, read a branch name further:
// the branch is the change whose active version is on it, when exactly one
// is (resolveRecorded). So how a change is named never decides whether its
// record is read. A change whose active version has landed, by the first
// two landing rules (inventory.go), is on no branch for this, so a branch
// name can be used again once its change has landed.

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"the-valley/change"
	"the-valley/rebase"
)

// recordTarget is the one target a change may name for now. The hook
// refuses a record for any other.
const recordTarget = rebase.Target

// origin is one ls-remote snapshot of the refs a verb reads.
type origin struct {
	refs map[string]string // ref → object ID
}

// listOrigin takes one snapshot of origin's refs matching the patterns, as
// the rebase engine reads origin.
func listOrigin(g git, patterns ...string) (origin, error) {
	o, err := rebase.ListOrigin(context.Background(), g.engine(), patterns...)
	return origin{refs: o.Refs}, err
}

// engine is the snapshot as the rebase engine holds one.
func (o origin) engine() rebase.Origin { return rebase.Origin{Refs: o.refs} }

// recordPatterns are the ls-remote patterns that list every record.
var recordPatterns = rebase.RecordPatterns

// changes is the ID of every change origin holds a record for, in order.
func (o origin) changes() []string { return o.engine().Changes() }

// handle is the change's handle among the changes origin holds, and this
// one, which may not be among them yet.
func (o origin) handle(id string) string { return o.engine().Handle(id) }

// fetch brings the objects named into this checkout's object store, as
// the rebase engine fetches them: by ID, without moving a ref.
func fetch(g git, oids ...string) error {
	return rebase.Fetch(context.Background(), g.engine(), oids...)
}

// readRecord reads the record a record commit holds. The commit must have
// been fetched.
func readRecord(g git, commit string) (*change.Record, error) {
	return rebase.ReadRecord(context.Background(), g.engine(), commit)
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
	handle, qualified, err := handlePart(g, name)
	switch {
	case err != nil:
		return recorded{}, false, err
	case handle == "" || strings.HasPrefix(name, "origin/"):
		return recorded{}, false, nil
	}
	ids := o.changes()
	matches := change.Matching(handle, ids)
	switch {
	case len(matches) == 0 && qualified:
		return recorded{}, false, failure(fmt.Sprintf("no change on origin has the handle %s", name))
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

// handlePart is the handle a name gives, when it has a handle's shape: the
// name itself, or what follows the colon in a name written
// <repo>:<handle>. changes and pending qualify a handle that way when one
// change ID is recorded in several repositories they list. git refuses a
// colon in a ref name, so a qualified name is never a branch's. It must
// name this repository, and qualified reports that it was written so.
func handlePart(g git, name string) (handle string, qualified bool, err error) {
	if change.LooksLikeHandle(name) {
		return name, false, nil
	}
	repo, handle, ok := strings.Cut(name, ":")
	if !ok || !change.LooksLikeHandle(handle) {
		return "", false, nil
	}
	if here := project(g); repo != here {
		return "", false, failure(fmt.Sprintf("%s names a change in %s, and this repository is %s", name, repo, here))
	}
	return handle, true, nil
}

// resolution is what the name of a change stands for. A name that is a
// branch's sets branch alone. A name that is a change's sets every field
// from the change's record on origin: the active version, the head that
// version names, the branch it names, the change's state, and its owner.
type resolution struct {
	id, handle, head, branch, state, owner string
	version                                int
}

// resolveRecorded reads a change's name for the verbs that act on a
// change: review, approve and rebase, through runResolve. It is
// lookupRecorded's reading. A change whose active version names no branch
// resolves with an empty branch: approve files the head alone, and
// bin/valley refuses it for review and rebase, which act on a branch.
//
// A name no record is on is a branch, and is refused unless it is a valid
// branch name in UTF-8. The name comes from the command line, and
// runResolve prints it in a line of fields that bin/valley splits, so a
// name holding a separator could read as some other branch.
func resolveRecorded(g git, name string) (resolution, error) {
	o, n, found, err := lookupRecorded(g, name)
	switch {
	case err != nil:
		return resolution{}, err
	case !found:
		branch := strings.TrimPrefix(name, "origin/")
		if !change.ValidBranch(branch) || !utf8.ValidString(branch) {
			return resolution{}, failure(fmt.Sprintf("%q names no change on origin, and is not a branch name in UTF-8", name))
		}
		return resolution{branch: branch}, nil
	}
	return o.resolution(n), nil
}

// lookupRecorded reads a change's name and returns the record it names,
// if it names one, with the snapshot of origin it was read in. That
// snapshot lists inventoryPatterns. A name that is a change's ID or handle
// is that change, as lookup reads it. A branch name, written as <branch>
// or origin/<branch>, is read against the branch each record's active
// version names. No record on the branch leaves the name a branch, as it
// was before changes had records. One record makes the name that record's
// change, exactly as if its ID had been given. More than one is refused,
// since acting on the branch would act on any of them.
func lookupRecorded(g git, name string) (origin, recorded, bool, error) {
	branch := strings.TrimPrefix(name, "origin/")
	o, err := listOrigin(g, append([]string{"refs/heads/" + branch}, inventoryPatterns...)...)
	if err != nil {
		return origin{}, recorded{}, false, err
	}
	if !strings.HasPrefix(name, "origin/") {
		n, found, err := lookup(g, o, name)
		switch {
		case err != nil:
			return o, recorded{}, false, err
		case found:
			return o, n, true, nil
		}
	}
	found, err := unlandedOn(g, o, branch)
	if err != nil {
		return o, recorded{}, false, err
	}
	switch len(found) {
	case 0:
		return o, recorded{}, false, nil
	case 1:
		return o, found[0], true, nil
	}
	var ids []string
	for _, n := range found {
		ids = append(ids, n.id)
	}
	return o, recorded{}, false, failure(fmt.Sprintf("the changes %s all have their active version on %s; name one by its change ID",
		strings.Join(ids, ", "), branch))
}

// resolution is what a change's record says its name stands for: the
// active version, the head that version names, the branch it names, the
// change's state, and its owner.
func (o origin) resolution(n recorded) resolution {
	v := n.record.ActiveVersion()
	return resolution{
		id: n.id, handle: o.handle(n.id), version: v.N,
		head: strings.TrimPrefix(v.Head, change.HeadPrefix), branch: v.Branch, state: n.record.State,
		owner: n.record.Owner,
	}
}

// unlandedOn is every change origin holds a record of whose active
// version names branch, less each whose active version has landed, as
// the rebase engine reads it (rebase.UnlandedOn). o must list
// inventoryPatterns.
func unlandedOn(g git, o origin, branch string) ([]recorded, error) {
	found, err := rebase.UnlandedOn(context.Background(), g.engine(), o.engine(), branch, newVerifier())
	return fromEngine(found), err
}

// runResolve prints what a change's name stands for, for the verbs
// bin/valley runs itself, as resolveRecorded reads it: one line of fields
// separated by tabs. For a change it is
//
//	change <ID> <handle> <version> <head> <branch> <state> <owner>
//
// with the head as a full object ID, the state as the record says it:
// ready, draft or withdrawn, and the owner as the record names it. review
// names the owner of a draft it shows. For a branch it is
//
//	branch <branch>
//
// A version may name no branch, and then the branch field is empty. A
// branch name holds no tab, since git refuses control characters in a
// ref name, and an owner holds none, since the record's validation allows
// only a principal's name there. The record commit, which has the head as a parent, has been
// fetched, so the head is in this checkout's objects.
func runResolve(g git, args []string, w io.Writer) error {
	if len(args) != 1 {
		return failure("usage: valley-go resolve <change>")
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	r, err := resolveRecorded(g, args[0])
	if err != nil {
		return err
	}
	if r.id == "" {
		_, err = fmt.Fprintf(w, "branch\t%s\n", r.branch)
		return err
	}
	_, err = fmt.Fprintf(w, "change\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", r.id, r.handle, r.version, r.head, r.branch, r.state, r.owner)
	return err
}

// runVersions prints every version of a change, for bin/valley's approve,
// which approves exactly one head of a change and refuses a head no
// version records. One line each, in order, of fields separated by tabs:
//
//	<n> <head> <state> <title>
//
// with the head as a full object ID and the change's state as its record
// says it, on every line. The title is last, since it may hold spaces; it
// holds no tab, which is a control character. It fails when origin holds
// no record of the change.
func runVersions(g git, args []string, w io.Writer) error {
	if len(args) != 1 || !change.ValidID(args[0]) {
		return failure("usage: valley-go versions <change ID>")
	}
	id := args[0]
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	ref := change.Ref(recordTarget, id)
	o, err := listOrigin(g, ref)
	if err != nil {
		return err
	}
	commit := o.refs[ref]
	if commit == "" {
		return failure("origin holds no record of change " + id)
	}
	if err := fetch(g, commit); err != nil {
		return err
	}
	r, err := readRecord(g, commit)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, v := range r.Versions {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\n", v.N, headOf(v), r.State, v.Title)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// readAll reads every record origin holds, in the order of their IDs, as
// the rebase engine reads them (rebase.ReadAll). A record that does not
// read is kept apart, in unread, and is no change for any reader.
func readAll(g git, o origin) (all []recorded, unread []unreadRecord, err error) {
	read, bad, err := rebase.ReadAll(context.Background(), g.engine(), o.engine())
	for _, u := range bad {
		unread = append(unread, unreadRecord{ref: u.Ref, commit: u.Commit, why: u.Why})
	}
	return fromEngine(read), unread, err
}

// fromEngine is the engine's records as this program holds them.
func fromEngine(read []rebase.Recorded) []recorded {
	var all []recorded
	for _, n := range read {
		all = append(all, recorded{id: n.ID, commit: n.Commit, record: n.Record})
	}
	return all
}

// engine is the record as the rebase engine holds one.
func (n recorded) engine() rebase.Recorded {
	return rebase.Recorded{ID: n.id, Commit: n.commit, Record: n.record}
}

// unreadRecord is a record on origin that does not read.
type unreadRecord struct {
	ref, commit, why string
}

// mayName reports whether a record that does not read may name one of
// the commits from tip to head as a version head. A record names its
// version heads as parents of its record commits, so it can name only a
// commit its record commit reaches. That holds for some commit from tip to
// head exactly when a merge base of the record commit and head is not on
// tip. A record git cannot compare at all may name anything.
func mayName(g git, u unreadRecord, head, tip string) bool {
	out, err := g.output(true, "merge-base", "--all", u.commit, head)
	if err != nil {
		return exitCode(err) != 1
	}
	for _, base := range strings.Fields(out) {
		if !g.succeeds(true, "merge-base", "--is-ancestor", base, tip) {
			return true
		}
	}
	return false
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
// and returns its ID, as the rebase engine writes one.
func writeRecord(g git, r *change.Record, message string, parents ...string) (string, error) {
	return rebase.WriteRecord(context.Background(), g.engine(), r, message, parents...)
}

// pushRecord moves the change's record ref on origin from old to commit,
// leased on old: the push is refused if the ref has moved since it was
// read, and a record that did not exist must still not exist. The hook's
// refusal reaches stderr through git.
func pushRecord(g git, id, old, commit string) error {
	if w := rebase.PushRecord(context.Background(), g.engine(), id, old, commit); w.Push != rebase.Pushed {
		return failure("origin did not take the record of " + id)
	}
	return nil
}
