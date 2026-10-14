package main

// What the change records say about a request.
//
// A change record (change/) lists the versions of one change and says
// whether the change is ready, a draft or withdrawn. Records grant
// nothing: no record ever makes a request land. The integrator reads them
// only to refuse. A request lands only when every record that knows its
// head says the change is ready, so unreadying or withdrawing a change
// stops a request already filed for it, though the change's owner cannot
// touch request refs.
//
// Two rules read records:
//
//   - A request named c-<change ID> is that change's. It is refused unless
//     the change's record exists, says ready, and lists the request's head
//     as one of its versions' heads. This reads the one record, fresh,
//     each time the rule is applied.
//   - A request of any name is refused when its head is a version head of
//     any record that is not ready. The integrator finds those records by
//     head, through an index it builds once per pass from every ref under
//     refs/the-valley/changes/.
//
// A third rule reads the request's commits rather than records. A request
// is refused when the commits from its merge base with the target to its
// head include a merge. The integrator lands a line of commits, and a
// merge could make other work look landed by ancestry.
//
// A request no record knows, and whose commits hold no merge, is judged as
// it always was.

import (
	"the-valley/change"
	"the-valley/integrator/verdict"

	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// The outcomes a request is refused with before the commit rule is
// applied. Like a rejection, a refusal leaves the request where it is. A
// refusal for a record's state stands only while the record says so: it
// is decided again on every pass, so a change readied again is judged on
// the next one.
const (
	refusedNotReady verdict.Outcome = "refused-not-ready"
	refusedMerge    verdict.Outcome = "refused-merge"
)

// recordLimit is the most records the index reads. The hook refuses to
// create a record past change.MaxRecords, and pushes that run at once can
// pass that bound only by the records each had in flight. A repository
// holding more than twice the bound is refused whole: the index could not
// say which heads are versions of changes that are not ready, so no
// request lands until records are deleted.
const recordLimit = 2 * change.MaxRecords

// indexed is one record the index read, and the ref it was read from.
type indexed struct {
	ref    string
	record *change.Record
}

// recordIndex is every record the served repository holds, by the heads
// its versions name. It is read once per pass.
type recordIndex struct {
	byHead map[string][]indexed

	// unreadable is each ref under the namespace that holds no record the
	// shared parser accepts, with why. A record that does not read names
	// no head the integrator can see, so it refuses nothing.
	unreadable []string
}

// readRecords builds the index from every ref under
// refs/the-valley/changes/.
func (in *integrator) readRecords() (*recordIndex, error) {
	out, err := git(in.repo, "for-each-ref", fmt.Sprintf("--count=%d", recordLimit+1),
		"--format=%(refname) %(objectname)", change.RefPrefix)
	if err != nil {
		return nil, err
	}
	var refs, commits []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if ref, oid, ok := strings.Cut(line, " "); ok {
			refs = append(refs, ref)
			commits = append(commits, oid)
		}
	}
	if len(refs) > recordLimit {
		return nil, fmt.Errorf("%s holds more than %d change records, so which heads are versions of changes that are not ready cannot be read; no request is judged until records are deleted", change.RefPrefix, recordLimit)
	}
	records, errs, err := in.records(refs, commits)
	if err != nil {
		return nil, err
	}
	idx := &recordIndex{byHead: map[string][]indexed{}}
	for i, ref := range refs {
		if errs[i] != nil {
			idx.unreadable = append(idx.unreadable, fmt.Sprintf("%s: %v", ref, errs[i]))
			continue
		}
		for _, v := range records[i].Versions {
			head := strings.TrimPrefix(v.Head, change.HeadPrefix)
			idx.byHead[head] = append(idx.byHead[head], indexed{ref: ref, record: records[i]})
		}
	}
	return idx, nil
}

// errNoRecord is a record ref that does not exist.
var errNoRecord = errors.New("no such record")

// readRecord reads the one record at ref, fresh.
func (in *integrator) readRecord(ref string) (*change.Record, error) {
	oid, ok := gitTry(in.repo, "rev-parse", "--verify", "--quiet", ref)
	if !ok {
		return nil, errNoRecord
	}
	records, errs, err := in.records([]string{ref}, []string{oid})
	if err != nil {
		return nil, err
	}
	return records[0], errs[0]
}

// records reads the record each ref points at, under the bounds the hook
// admits a record under (change.MaxRecordCommit and the rest), each
// checked before anything past it is read. The record must be the one
// its ref names: its change ID and its target are the ref's. errs holds
// why each record that does not read does not; err is for git itself
// failing.
func (in *integrator) records(refs, commits []string) (records []*change.Record, errs []error, err error) {
	records = make([]*change.Record, len(refs))
	errs = make([]error, len(refs))
	if len(refs) == 0 {
		return records, errs, nil
	}
	// First the sizes and types: the commit, its tree, and its
	// record.json, one line each.
	var query bytes.Buffer
	for _, c := range commits {
		fmt.Fprintf(&query, "%s\n%s^{tree}\n%s:record.json\n", c, c, c)
	}
	checked, err := gitInput(in.repo, query.Bytes(), "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.TrimRight(string(checked), "\n"), "\n")
	if len(lines) != 3*len(refs) {
		return nil, nil, fmt.Errorf("git cat-file answered %d lines for %d records", len(lines), len(refs))
	}
	var blobs []string
	var wanted []int
	for i := range refs {
		blob, why := withinBounds(lines[3*i : 3*i+3])
		if why != "" {
			errs[i] = errors.New(why)
			continue
		}
		blobs = append(blobs, blob)
		wanted = append(wanted, i)
	}
	// Then the contents, of the record.json blobs within bounds alone.
	contents, err := in.blobs(blobs)
	if err != nil {
		return nil, nil, err
	}
	for k, i := range wanted {
		target, id, ok := change.ParseRef(refs[i])
		if !ok {
			errs[i] = fmt.Errorf("not a record ref; one is %s<target>/<change ID>", change.RefPrefix)
			continue
		}
		r, err := change.Parse(contents[k])
		switch {
		case err != nil:
			errs[i] = fmt.Errorf("its record.json does not read: %v", err)
		case r.ChangeID != id || r.Target != target:
			errs[i] = fmt.Errorf("it records change %s for %s, and its ref names change %s for %s", r.ChangeID, r.Target, id, target)
		default:
			records[i] = r
		}
	}
	return records, errs, nil
}

// withinBounds reads cat-file's three lines about one record — its commit,
// its tree and its record.json — and returns the blob, or why the record
// is not one to read.
func withinBounds(lines []string) (blob, why string) {
	for k, want := range []struct {
		what, kind string
		max        int
	}{
		{"the record", "commit", change.MaxRecordCommit},
		{"its tree", "tree", change.MaxRecordTree},
		{"its record.json", "blob", change.MaxRecordBlob},
	} {
		fields := strings.Fields(lines[k])
		if len(fields) != 3 {
			return "", fmt.Sprintf("%s is missing", want.what)
		}
		size, err := strconv.Atoi(fields[2])
		switch {
		case fields[1] != want.kind:
			return "", fmt.Sprintf("%s is a %s, where a %s is expected", want.what, fields[1], want.kind)
		case err != nil || size > want.max:
			return "", fmt.Sprintf("%s is %s bytes, more than the %d a record may hold", want.what, fields[2], want.max)
		}
		blob = fields[0]
	}
	return blob, ""
}

// blobs reads each blob's contents with one cat-file.
func (in *integrator) blobs(oids []string) ([][]byte, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	out, err := gitInput(in.repo, []byte(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	rd := bufio.NewReader(bytes.NewReader(out))
	contents := make([][]byte, len(oids))
	for i := range oids {
		header, err := rd.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git cat-file --batch answered %q", strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch answered %q", strings.TrimSpace(header))
		}
		contents[i] = make([]byte, size)
		if _, err := io.ReadFull(rd, contents[i]); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %w", err)
		}
		if _, err := rd.Discard(1); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: %w", err)
		}
	}
	return contents, nil
}

// recordRefusal applies the two rules that read records, and returns the
// refusal and why, or "" when neither refuses. reread reads every record
// the index found for the head again, as well as the change's own; the
// pass does that just before it lands, since the checks that come between
// can take minutes.
func (in *integrator) recordRefusal(r request, idx *recordIndex, reread bool) (verdict.Outcome, string, error) {
	target := strings.TrimPrefix(r.target, "refs/heads/")
	if id, ok := change.RequestID(r.change); ok {
		ref := change.Ref(target, id)
		rec, err := in.readRecord(ref)
		switch {
		case errors.Is(err, errNoRecord):
			return refusedNotReady, fmt.Sprintf("the request is named for change %s, and %s holds no record of it", id, ref), nil
		case err != nil:
			return refusedNotReady, fmt.Sprintf("the request is named for change %s, and its record %s does not read: %v", id, ref, err), nil
		case rec.State != change.Ready:
			return refusedNotReady, fmt.Sprintf("change %s is %s, not ready", id, rec.State), nil
		}
		if _, found := versionAt(rec, r.head); !found {
			return refusedNotReady, fmt.Sprintf("its head %s is no version of change %s", verdict.Short(r.head), id), nil
		}
	}
	for _, n := range idx.byHead[r.head] {
		rec := n.record
		if reread {
			var err error
			if rec, err = in.readRecord(n.ref); errors.Is(err, errNoRecord) {
				continue
			} else if err != nil {
				return refusedNotReady, fmt.Sprintf("the record %s, which named its head, does not read now: %v", n.ref, err), nil
			}
		}
		v, found := versionAt(rec, r.head)
		if found && rec.State != change.Ready {
			return refusedNotReady, fmt.Sprintf("its head %s is v%d of change %s, which is %s, not ready", verdict.Short(r.head), v.N, rec.ChangeID, rec.State), nil
		}
	}
	return "", "", nil
}

// versionAt is the version of a record whose head is oid.
func versionAt(r *change.Record, oid string) (change.Version, bool) {
	for _, v := range r.Versions {
		if v.Head == change.HeadPrefix+oid {
			return v, true
		}
	}
	return change.Version{}, false
}

// mergeRefusal refuses a request whose commits from its merge base with
// the tip to its head include a merge. Those are the commits the head
// would bring to the target: tip..head.
func (in *integrator) mergeRefusal(r request, tip string) (verdict.Outcome, string, error) {
	merge, err := gitLine(in.repo, "rev-list", "--min-parents=2", "--max-count=1", r.head, "^"+tip)
	if err != nil {
		return "", "", fmt.Errorf("cannot read the commits from %s to %s: %w", r.target, verdict.Short(r.head), err)
	}
	if merge != "" {
		return refusedMerge, fmt.Sprintf("the commits from its merge base with %s to its head include the merge %s, and a change is a line of commits", r.target, verdict.Short(merge)), nil
	}
	return "", "", nil
}
