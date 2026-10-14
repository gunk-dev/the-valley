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
//   - A request of any name is refused when any commit it would bring to
//     the target, its head or one below it, is a version head of a record
//     that is not ready. The integrator finds those records by head,
//     through an index it builds from every ref under
//     refs/the-valley/changes/: once per pass, and again just before it
//     moves the target.
//
// A withdrawn change's requests are voided. Each pass deletes every
// request named for a withdrawn change, and every request whose head is
// one of its version heads, so readying the change again needs a fresh
// approval.
//
// None of this is atomic with landing. A record written after the
// integrator's last read and before it moves the target is not seen, so
// an approved head its author has just paused can still land. What lands
// is always a head the operator approved: the request names it exactly.
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

	// voidedWithdrawn is the outcome of a request deleted because its
	// change was withdrawn.
	voidedWithdrawn verdict.Outcome = "voided-withdrawn"
)

// recordLimit is the most records the index reads. The hook refuses to
// create a record past change.MaxRecords, and pushes that run at once can
// pass that bound only by the records each had in flight. A repository
// holding more than twice the bound lands nothing: the index could not say
// which heads are versions of changes that are not ready, so no request in
// it lands until records are deleted. Each repository has its own
// controller, so the others are not held up.
const recordLimit = 2 * change.MaxRecords

// indexed is one record the index read, and the ref it was read from.
type indexed struct {
	ref    string
	record *change.Record
}

// recordIndex is every record the served repository holds, by the heads
// its versions name and by ref.
type recordIndex struct {
	byHead map[string][]indexed
	byRef  map[string]*change.Record

	// unreadable is each ref under the namespace that holds no record the
	// shared parser accepts. A record that does not read names no head the
	// integrator can see, so it refuses nothing, and a head it names can
	// land. The hook refuses such a record, so only corrupt storage holds
	// one, and the pass reports it wherever it may matter.
	unreadable []unreadable
}

// unreadable is a ref under the namespace whose record does not read.
type unreadable struct {
	ref, commit, why string
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
	idx := &recordIndex{byHead: map[string][]indexed{}, byRef: map[string]*change.Record{}}
	for i, ref := range refs {
		if errs[i] != nil {
			idx.unreadable = append(idx.unreadable, unreadable{ref: ref, commit: commits[i], why: errs[i].Error()})
			continue
		}
		idx.byRef[ref] = records[i]
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
// refusal and why, or "" when neither refuses. The request's own record,
// for a request named c-<change ID>, is read fresh; every other record is
// read from idx.
func (in *integrator) recordRefusal(r request, idx *recordIndex, tip string) (verdict.Outcome, string, error) {
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
	commits, err := in.introduced(r, tip)
	if err != nil {
		return "", "", err
	}
	for _, c := range commits {
		for _, n := range idx.byHead[c] {
			v, found := versionAt(n.record, c)
			if !found || n.record.State == change.Ready {
				continue
			}
			what := "the commit " + verdict.Short(c) + " it brings"
			if c == r.head {
				what = "its head " + verdict.Short(c)
			}
			return refusedNotReady, fmt.Sprintf("%s is v%d of change %s, which is %s, not ready", what, v.N, n.record.ChangeID, n.record.State), nil
		}
	}
	return "", "", nil
}

// introduced is every commit a request would bring to the target, newest
// first: those reachable from its head and not from the tip, and the head
// itself whatever the tip holds.
func (in *integrator) introduced(r request, tip string) ([]string, error) {
	out, err := gitLine(in.repo, "rev-list", r.head, "^"+tip)
	if err != nil {
		return nil, fmt.Errorf("cannot read the commits from %s to %s: %w", r.target, verdict.Short(r.head), err)
	}
	commits := strings.Fields(out)
	if len(commits) == 0 || commits[0] != r.head {
		commits = append([]string{r.head}, commits...)
	}
	return commits, nil
}

// finalRefusal applies the record rules once more, immediately before the
// target moves, with every record read again into a fresh index. A record
// that came to name one of the request's commits since the pass began is
// seen here. Only a record written after this read and before the target
// moves is missed.
func (in *integrator) finalRefusal(r request, tip string) (verdict.Outcome, string, error) {
	idx, err := in.readRecords()
	if err != nil {
		return "", "", err
	}
	return in.recordRefusal(r, idx, tip)
}

// uncheckable is each record that does not read and may name one of the
// commits the request brings. A record names its version heads as parents
// of its record commits, so it can name only a commit its record commit
// reaches. That is true of some commit the request brings exactly when a
// merge base of the record commit and the head is not on the tip. A record
// git cannot compare at all may name anything.
func (in *integrator) uncheckable(r request, idx *recordIndex, tip string) []unreadable {
	var found []unreadable
	for _, u := range idx.unreadable {
		out, ok := gitTry(in.repo, "merge-base", "--all", u.commit, r.head)
		may := !ok && out != ""
		for _, base := range strings.Fields(out) {
			if !ok {
				break
			}
			if _, onTip := gitTry(in.repo, "merge-base", "--is-ancestor", base, tip); !onTip {
				may = true
			}
		}
		if may {
			found = append(found, u)
		}
	}
	return found
}

// voidWithdrawn deletes the requests a withdrawn change voids, and returns
// the requests left to judge. A request is voided when it is named for a
// withdrawn change, or when its head is a version head of one. Before each
// deletion the record is read again, and the request is deleted only if
// the change is still withdrawn. The deletion is leased on the head the
// pass found the request at, so an approval filed since stands. A
// withdrawal followed by a ready within one pass is not seen, and leaves
// the request in place.
func (in *integrator) voidWithdrawn(requests []request, idx *recordIndex) []request {
	var kept []request
	for _, r := range requests {
		if ref := withdrawnFor(r, idx); ref == "" || !in.void(r, ref) {
			kept = append(kept, r)
		}
	}
	return kept
}

// withdrawnFor is the ref of a withdrawn change's record that voids the
// request, as idx read it, or "".
func withdrawnFor(r request, idx *recordIndex) string {
	if id, ok := change.RequestID(r.change); ok {
		ref := change.Ref(strings.TrimPrefix(r.target, "refs/heads/"), id)
		if rec := idx.byRef[ref]; rec != nil && rec.State == change.Withdrawn {
			return ref
		}
	}
	for _, n := range idx.byHead[r.head] {
		if n.record.State == change.Withdrawn {
			return n.ref
		}
	}
	return ""
}

// void deletes one request a withdrawn change voids, if the change is
// still withdrawn and the request still at the head the pass found, and
// records the outcome voided-withdrawn. It reports whether it deleted it.
func (in *integrator) void(r request, ref string) bool {
	rec, err := in.readRecord(ref)
	if err != nil || rec.State != change.Withdrawn {
		return false
	}
	if id, named := change.RequestID(r.change); !(named && id == rec.ChangeID) {
		if _, found := versionAt(rec, r.head); !found {
			return false
		}
	}
	tip, err := gitLine(in.repo, "rev-parse", "--verify", "--quiet", r.target)
	if err != nil {
		return false
	}
	evidence, err := in.evidenceState()
	if err != nil {
		return false
	}
	if _, err := git(in.repo, "update-ref", "-d", r.ref, r.head); err != nil {
		return false
	}
	fmt.Fprintf(in.out, "%s -> %s @ %s: %s\n", r.change, r.target, verdict.Short(tip), voidedWithdrawn)
	fmt.Fprintf(in.out, "  change %s is withdrawn, which voids its requests; the request is deleted, and the change needs a fresh approval once it is ready again\n", rec.ChangeID)
	if err := in.writeMemo(r, fmt.Sprintf("%s %s %s %s", r.head, tip, evidence, voidedWithdrawn)); err != nil {
		fmt.Fprintf(in.out, "  the outcome was not recorded: %v\n", err)
	}
	return true
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
