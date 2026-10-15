package main

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"the-valley/change"
)

// A change record is a commit at refs/the-valley/changes/<target>/<change
// ID> whose tree holds one file, record.json (the change module). The
// record commit's parents tie it to what it records: a new change's record
// has the head it marks ready as its one parent, and each later record has
// the record it replaces as its first parent, then the head of the version
// it adds, if it adds one. So a record ref holds on to every head the
// change was ever ready at, as a topic branch holds on to its commits.
//
// The bounds on reading one, which the change module defines for every
// reader of a record. They are far above what the valley command writes,
// and they are checked before any object is read past them.
const (
	maxRecordCommit  = change.MaxRecordCommit  // bytes in a record commit
	maxRecordTree    = change.MaxRecordTree    // bytes in its tree
	maxRecordBlob    = change.MaxRecordBlob    // bytes in its record.json
	maxRecordUpdates = change.MaxRecordUpdates // change record updates in one push
	maxRecords       = change.MaxRecords       // change records a repository holds; a soft bound, below

	// recordTarget is the one target a change may name for now.
	recordTarget = "main"
)

// recordCommit is what a record commit says: its parents, in order, and
// its record.json, unread.
type recordCommit struct {
	parents []string
	record  []byte
}

func (gitRepository) record(id string) (recordCommit, error) {
	b := attestationBudget
	b.subject = "a change record"
	objects, err := openObjectsWithin(b)
	if err != nil {
		return recordCommit{}, err
	}
	defer objects.close()
	return readRecord(objects, id)
}

func readRecord(objects *objectStream, id string) (recordCommit, error) {
	hashSize := len(id) / 2
	body, err := objects.read(id, "commit", maxRecordCommit)
	if err != nil {
		return recordCommit{}, fmt.Errorf("it %v", err)
	}
	tree, parents, err := commitLinks(body, hashSize)
	if err != nil {
		return recordCommit{}, err
	}
	entries, err := objects.read(tree, "tree", maxRecordTree)
	if err != nil {
		return recordCommit{}, fmt.Errorf("its tree %v", err)
	}
	if len(entries) == 0 {
		return recordCommit{}, errors.New("its tree is empty, and a change record's tree holds record.json alone")
	}
	mode, name, blob, rest, err := treeEntry(entries, hashSize)
	switch {
	case err != nil:
		return recordCommit{}, fmt.Errorf("its tree %v", err)
	case len(rest) > 0:
		return recordCommit{}, errors.New("its tree holds more than one entry, and a change record's tree holds record.json alone")
	case name != "record.json" || mode != "100644":
		return recordCommit{}, fmt.Errorf("its tree holds %q with mode %s, and a change record's tree holds record.json alone, as a plain file", name, mode)
	}
	data, err := objects.read(blob, "blob", maxRecordBlob)
	if err != nil {
		return recordCommit{}, fmt.Errorf("its record.json %v", err)
	}
	return recordCommit{parents: parents, record: data}, nil
}

// commitLinks reads the tree and the parents out of a commit object, as
// git reads them: a tree line first, and then every parent line that
// follows it directly. A parent line anywhere later is one git ignores,
// and a record that holds one is refused rather than read two ways.
func commitLinks(body []byte, hashSize int) (tree string, parents []string, err error) {
	header, _, _ := bytes.Cut(body, []byte("\n\n"))
	lines := strings.Split(string(header), "\n")
	tree, ok := strings.CutPrefix(lines[0], "tree ")
	if !ok || !isHexID(tree, hashSize) {
		return "", nil, errors.New("it is a commit git could not have written: it does not open with its tree")
	}
	rest := lines[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "parent ") {
		parent := strings.TrimPrefix(rest[0], "parent ")
		if !isHexID(parent, hashSize) {
			return "", nil, errors.New("it is a commit git could not have written: a parent is not an object ID")
		}
		parents = append(parents, parent)
		rest = rest[1:]
	}
	for _, line := range rest {
		if strings.HasPrefix(line, "parent ") {
			return "", nil, errors.New("it names a parent after its other headers, which git does not read as a parent")
		}
	}
	return tree, parents, nil
}

func (gitRepository) kind(id string) (string, error) {
	out, err := run(nil, "cat-file", "-t", id)
	if err != nil {
		return "", errors.New("names an object this repository does not hold")
	}
	return strings.TrimSpace(string(out)), nil
}

// records counts the refs under refs/the-valley/changes/, past the bound
// by one at most: for-each-ref stops printing there.
//
// The bound it is checked against is soft. A pre-receive hook runs before
// git installs any ref, and nothing serializes the hooks of two pushes, so
// pushes running at once can each count the same records and each create
// more. The repository can then hold more than maxRecords, by at most the
// record updates those pushes had in flight: maxRecordUpdates each. Once
// it is over the bound, every later create is refused.
func (gitRepository) records() (int, error) {
	out, err := run(nil, "for-each-ref", fmt.Sprintf("--count=%d", maxRecords+1), "--format=x", change.RefPrefix)
	if err != nil {
		return 0, err
	}
	return bytes.Count(out, []byte("\n")), nil
}

// checkRecord decides a write to a change record ref, by the table of
// transitions below, and returns the verb and the reason that refuse it,
// or "" for the reason when the write is allowed. P is the new record
// commit's parents, old the ref's value before the push, and h the head a
// version names.
//
//	create       any named principal     P = [h]
//	new version  owner or request holder P = [old, h]
//	unready      owner or request holder P = [old]   ready → draft
//	re-ready     owner or request holder P = [old]   draft or withdrawn → ready
//	withdraw     owner or request holder P = [old]   ready or draft → withdrawn
//	delete       request holder          —
//
// A new version always leaves the change ready, from whatever state it
// was in. A record whose one parent is old changes the state and the seq
// alone. A withdrawn change comes back ready, at the same version or a new
// one, and never as a draft. Every other combination is refused.
func (p policy) checkRecord(principal string, u update) (verb, why string) {
	target, id, ok := change.ParseRef(u.ref)
	if !ok || target != recordTarget {
		return "write", fmt.Sprintf("a change record's ref is %s%s/<change ID>, with the change ID as 32 letters from k to z", change.RefPrefix, recordTarget)
	}
	if principal == "" {
		return "write", "a change record takes writes only from a named principal, and a key that names no principal is none"
	}
	switch {
	case isZero(u.new):
		if !p.grants.holds(principal, grantRequest) {
			return "delete", fmt.Sprintf("only a principal holding the %s grant may delete a change record, and %s holds none", grantRequest, principal)
		}
		return "delete", ""
	case isZero(u.old):
		if p.existingRecords+p.recordCreates > maxRecords {
			return "create", fmt.Sprintf("the repository holds %d change records, and this push would create %d more, past the %d it may hold", p.existingRecords, p.recordCreates, maxRecords)
		}
		return "create", p.checkCreate(principal, target, id, u.new)
	default:
		return "update", p.checkUpdate(principal, target, id, u.old, u.new)
	}
}

// checkCreate holds a new change's first record to the create row.
func (p policy) checkCreate(principal, target, id, commit string) string {
	rc, err := p.repo.record(commit)
	if err != nil {
		return err.Error()
	}
	if len(rc.parents) != 1 {
		return fmt.Sprintf("the record that creates a change has the head it marks ready as its one parent, and this one has %d parents", len(rc.parents))
	}
	h := rc.parents[0]
	if why := p.isCommit(h); why != "" {
		return why
	}
	r, err := change.Parse(rc.record)
	if err != nil {
		return "its record.json does not read: " + err.Error()
	}
	if why := sameChange(r, target, id); why != "" {
		return why
	}
	v := r.Versions[0]
	switch {
	case len(r.Versions) != 1:
		return fmt.Sprintf("a new change's record holds one version, and this one holds %d", len(r.Versions))
	case v.Head != change.HeadPrefix+h:
		return fmt.Sprintf("its version 1 names the head %s, and the record commit's parent is %s", v.Head, h)
	case v.Uploader != principal:
		return fmt.Sprintf("its version 1 names %s as its uploader, and %s is pushing it", v.Uploader, principal)
	case r.Owner != principal:
		return fmt.Sprintf("it names %s as the owner, and a new change is owned by the principal that pushes it, %s", r.Owner, principal)
	case r.Seq != 1:
		return fmt.Sprintf("its seq is %d, and a new change's is 1", r.Seq)
	case r.Active != 1:
		return fmt.Sprintf("its active is %d, and a new change's is 1", r.Active)
	case r.State != change.Ready:
		return fmt.Sprintf("its state is %s, and a new change is %s", r.State, change.Ready)
	}
	return ""
}

// checkUpdate holds an update of a change's record to the rows after
// create. Which row applies is read from the record commit's parents, and
// the record has to say what that row says.
func (p policy) checkUpdate(principal, target, id, old, commit string) string {
	prev, err := p.repo.record(old)
	if err != nil {
		return "the record it replaces " + err.Error()
	}
	before, err := change.Parse(prev.record)
	if err != nil {
		return "the record it replaces does not read: " + err.Error()
	}
	if principal != before.Owner && !p.grants.holds(principal, grantRequest) {
		return fmt.Sprintf("only the change's owner, %s, or a principal holding the %s grant may update its record", before.Owner, grantRequest)
	}
	rc, err := p.repo.record(commit)
	if err != nil {
		return err.Error()
	}
	after, err := change.Parse(rc.record)
	if err != nil {
		return "its record.json does not read: " + err.Error()
	}
	if why := sameChange(after, target, id); why != "" {
		return why
	}
	switch {
	case len(rc.parents) == 1 && rc.parents[0] == old:
		return checkStateChange(before, after)
	case len(rc.parents) == 2 && rc.parents[0] == old:
		h := rc.parents[1]
		if why := p.isCommit(h); why != "" {
			return why
		}
		return checkNewVersion(principal, before, after, h)
	default:
		return "a record that updates a change has the record it replaces as its first parent, and no other parent but the head of the version it adds"
	}
}

// checkStateChange holds a record whose one parent is the record it
// replaces to the unready, re-ready and withdraw rows: the state changes,
// by one of those rows, and the seq goes up by one. Nothing else changes,
// and so the active version stays.
func checkStateChange(before, after *change.Record) string {
	switch {
	case after.State == before.State:
		return fmt.Sprintf("the change is %s already, and a record whose one parent is the record it replaces changes the state", before.State)
	case before.State == change.Withdrawn && after.State == change.Draft:
		return "the change is withdrawn, and a withdrawn change comes back ready, never as a draft"
	case after.Seq != before.Seq+1:
		return fmt.Sprintf("its seq is %d, and an update's is the seq before it plus one, %d", after.Seq, before.Seq+1)
	case after.Owner != before.Owner:
		return fmt.Sprintf("it changes the owner from %s to %s, and no update does", before.Owner, after.Owner)
	case after.Active != before.Active:
		return fmt.Sprintf("it changes active from %d to %d, and a change of state keeps the active version", before.Active, after.Active)
	case !reflect.DeepEqual(after.Versions, before.Versions):
		return "it changes the versions, and a change of state changes only the state and the seq"
	}
	return ""
}

// checkNewVersion holds a new version to adding exactly one version, at
// the head h, and to the fields that follow from it.
func checkNewVersion(principal string, before, after *change.Record, h string) string {
	if len(after.Versions) != len(before.Versions)+1 {
		return fmt.Sprintf("a new version adds one version to the %d before it, and this record holds %d", len(before.Versions), len(after.Versions))
	}
	for i, v := range before.Versions {
		if after.Versions[i] != v {
			return fmt.Sprintf("it changes version %d, and the versions before a new one stay exactly as they were", v.N)
		}
	}
	v := after.Last()
	switch {
	case v.Head != change.HeadPrefix+h:
		return fmt.Sprintf("its new version names the head %s, and the record commit's second parent is %s", v.Head, h)
	case v.Head == before.ActiveVersion().Head:
		return fmt.Sprintf("its new version's head is the active version's, %s, and a new version needs a new head", v.Head)
	case v.Uploader != principal:
		return fmt.Sprintf("its new version names %s as its uploader, and %s is pushing it", v.Uploader, principal)
	case after.Active != v.N:
		return fmt.Sprintf("its active is %d, and a new version becomes the active one, %d", after.Active, v.N)
	case after.State != change.Ready:
		return fmt.Sprintf("its state is %s, and a new version makes the change %s", after.State, change.Ready)
	case after.Seq != before.Seq+1:
		return fmt.Sprintf("its seq is %d, and an update's is the seq before it plus one, %d", after.Seq, before.Seq+1)
	case after.Owner != before.Owner:
		return fmt.Sprintf("it changes the owner from %s to %s, and no update does", before.Owner, after.Owner)
	}
	return ""
}

// sameChange holds a record to naming the change and the target its ref
// names. Neither ever changes, so an update keeps them too.
func sameChange(r *change.Record, target, id string) string {
	switch {
	case r.ChangeID != id:
		return fmt.Sprintf("it names the change %s, and its ref names %s", r.ChangeID, id)
	case r.Target != target:
		return fmt.Sprintf("it names the target %s, and its ref names %s", r.Target, target)
	}
	return ""
}

// isCommit refuses a head that is not a commit.
func (p policy) isCommit(h string) string {
	kind, err := p.repo.kind(h)
	switch {
	case err != nil:
		return fmt.Sprintf("its head %s %v", h, err)
	case kind != "commit":
		return fmt.Sprintf("its head %s is a %s, and a head is a commit", h, kind)
	}
	return ""
}

func isHexID(s string, size int) bool {
	if len(s) != 2*size {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
