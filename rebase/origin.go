package rebase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"the-valley/change"
)

// Target is the one target a change may name for now. The hook refuses a
// record for any other.
const Target = "main"

// The namespaces of origin's integration requests, the integrator's
// outcome records, and the attestations the evidence is kept under.
const (
	RequestPrefix   = "refs/the-valley/integration-requests/" + Target + "/"
	OutcomePrefix   = "refs/the-valley/integration-outcomes/" + Target + "/"
	AttestationRefs = "refs/the-valley/attestations/"
)

// RecordPatterns are the ls-remote patterns that list every record.
var RecordPatterns = []string{change.RefPrefix + Target + "/*"}

// InventoryPatterns are the ls-remote patterns of every ref a snapshot
// reads: every branch, every record, every integration request and
// outcome, and every attestation ref.
var InventoryPatterns = []string{
	"refs/heads/*",
	change.RefPrefix + Target + "/*",
	RequestPrefix + "*",
	OutcomePrefix + "*",
	AttestationRefs + "*",
}

// Origin is one ls-remote snapshot of the refs a reader reads.
type Origin struct {
	Refs map[string]string // ref → object ID
}

// ListOrigin takes one snapshot of origin's refs matching the patterns.
// ls-remote matches a pattern against the tail of a ref name, so every ref
// is compared whole afterwards.
func ListOrigin(ctx context.Context, g Git, patterns ...string) (Origin, error) {
	out, err := g.output(ctx, false, append([]string{"ls-remote", "--refs", "origin"}, patterns...)...)
	if err != nil {
		return Origin{}, errors.New("could not read origin")
	}
	o := Origin{Refs: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		oid, ref, ok := strings.Cut(line, "\t")
		if ok && ref != "" {
			o.Refs[ref] = oid
		}
	}
	return o, nil
}

// Changes is the ID of every change origin holds a record for, in order.
func (o Origin) Changes() []string {
	var ids []string
	for ref := range o.Refs {
		if target, id, ok := change.ParseRef(ref); ok && target == Target {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Handle is the change's handle among the changes origin holds, and this
// one, which may not be among them yet.
func (o Origin) Handle(id string) string {
	return change.Handle(id, append(o.Changes(), id))
}

// Fetch brings the objects named into this checkout's object store,
// without writing FETCH_HEAD, moving a ref or fetching tags. An object
// already here is not asked for: a fetch brings an object with everything
// it reaches, so one that is here has what it reaches here too.
func Fetch(ctx context.Context, g Git, oids ...string) error {
	var query bytes.Buffer
	for _, oid := range oids {
		if oid != "" {
			fmt.Fprintln(&query, oid)
		}
	}
	if query.Len() == 0 {
		return nil
	}
	present, _ := g.input(ctx, true, &query, "cat-file", "--batch-check=%(objectname)")
	here := map[string]bool{}
	for _, line := range strings.Split(present, "\n") {
		here[line] = true
	}
	var stdin bytes.Buffer
	for _, oid := range oids {
		if oid != "" && !here[oid] {
			fmt.Fprintln(&stdin, oid)
		}
	}
	if stdin.Len() == 0 {
		return nil
	}
	if _, err := g.input(ctx, false, &stdin, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--stdin", "origin"); err != nil {
		return errors.New("could not fetch from origin")
	}
	return nil
}

// Recorded is a change origin holds a record of: its ID, the record commit
// origin's ref points at, and the record that commit holds.
type Recorded struct {
	ID     string
	Commit string
	Record *change.Record
}

// ReadRecord reads the record a record commit holds. The commit must have
// been fetched.
func ReadRecord(ctx context.Context, g Git, commit string) (*change.Record, error) {
	data, err := g.output(ctx, true, "cat-file", "blob", commit+":record.json")
	if err != nil {
		return nil, fmt.Errorf("the change record %s holds no record.json", short(commit))
	}
	r, err := change.Parse([]byte(data + "\n"))
	if err != nil {
		return nil, fmt.Errorf("the change record %s does not read: %v", short(commit), err)
	}
	return r, nil
}

// UnreadRecord is a record on origin that does not read.
type UnreadRecord struct {
	Ref, Commit, Why string
}

// ReadAll reads every record origin holds, in the order of their IDs. A
// record that does not read is kept apart, in unread, and is no change for
// any reader: the integrator treats it the same way. The hook refuses such
// a record, so only corrupt storage holds one. err is for origin that
// could not be read.
func ReadAll(ctx context.Context, g Git, o Origin) (all []Recorded, unread []UnreadRecord, err error) {
	ids := o.Changes()
	var commits []string
	for _, id := range ids {
		commits = append(commits, o.Refs[change.Ref(Target, id)])
	}
	if err := Fetch(ctx, g, commits...); err != nil {
		return nil, nil, err
	}
	for i, id := range ids {
		r, err := ReadRecord(ctx, g, commits[i])
		if err != nil {
			unread = append(unread, UnreadRecord{Ref: change.Ref(Target, id), Commit: commits[i], Why: err.Error()})
			continue
		}
		all = append(all, Recorded{ID: id, Commit: commits[i], Record: r})
	}
	return all, unread, nil
}

// HeadOf is a version's head as an object ID.
func HeadOf(v change.Version) string {
	return strings.TrimPrefix(v.Head, change.HeadPrefix)
}

// ChangeName is the change segment of a request ref. The integrator reads
// a request as <prefix>/<target>/<change> and refuses a change segment
// holding a slash (integrator/refs.go), so a branch name with one is
// written with dashes instead. bin/valley's change_name is the same
// mapping, and the request approve files is named by it.
func ChangeName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// RequestNames are the names a request for a version is filed under: its
// branch, the way approve names one, and c-<change ID>.
func RequestNames(id string, v change.Version) []string {
	var names []string
	if v.Branch != "" {
		names = append(names, ChangeName(v.Branch))
	}
	return append(names, change.RequestName(id))
}
