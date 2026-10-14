package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/change"
)

const changeID = "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"

var recordRef = change.Ref("main", changeID)

// records is a bare repository to push change records into, read the way
// the hook reads one: through gitRepository, with real git objects.
type records struct {
	t   *testing.T
	dir string
	p   policy
}

func newRecords(t *testing.T) *records {
	dir := t.TempDir()
	gitIn(t, dir, "", "init", "--quiet", "--bare", dir)
	t.Setenv("GIT_DIR", dir)
	return &records{t: t, dir: dir, p: policy{
		project: "project",
		grants:  grants{"operator": {grantRequest: true}},
		repo:    gitRepository{},
	}}
}

func (r *records) git(stdin string, args ...string) string {
	r.t.Helper()
	return gitIn(r.t, r.dir, stdin, args...)
}

// source is a commit standing for a head a change is marked ready at.
func (r *records) source(subject string) string {
	empty := r.git("", "mktree")
	return r.commit(empty, nil, subject)
}

// commit writes a commit object exactly as given, parents and all, which
// git's own porcelain would not always write.
func (r *records) commit(tree string, parents []string, message string) string {
	r.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "tree %s\n", tree)
	for _, p := range parents {
		fmt.Fprintf(&b, "parent %s\n", p)
	}
	b.WriteString("author a <a@localhost> 1700000000 +0000\ncommitter a <a@localhost> 1700000000 +0000\n\n")
	b.WriteString(message + "\n")
	return r.git(b.String(), "hash-object", "-t", "commit", "-w", "--literally", "--stdin")
}

func (r *records) blob(content string) string {
	return r.git(content, "hash-object", "-w", "--stdin")
}

// recordTree is a record commit's tree, holding the text as record.json.
func (r *records) recordTree(text string) string {
	return r.git(fmt.Sprintf("100644 blob %s\trecord.json\n", r.blob(text)), "mktree")
}

// write writes a record commit holding text, with the parents given.
func (r *records) write(text string, parents ...string) string {
	return r.commit(r.recordTree(text), parents, "a change record")
}

func encode(t *testing.T, rec change.Record) string {
	t.Helper()
	out, err := change.Encode(&rec)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// first is the record that creates the change at head h, pushed by owner.
func first(owner, h string) change.Record {
	return change.Record{
		Schema: change.Schema, ChangeID: changeID, Target: "main", Owner: owner,
		State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{
			{N: 1, Head: change.HeadPrefix + h, Title: "the first", Branch: "topic", Uploader: owner},
		},
	}
}

// next is rec with a new version at head h, uploaded by who.
func next(rec change.Record, who, h string) change.Record {
	rec.Versions = append(append([]change.Version(nil), rec.Versions...), change.Version{
		N: len(rec.Versions) + 1, Head: change.HeadPrefix + h, Title: "the next", Branch: "topic", Uploader: who,
	})
	rec.Active = len(rec.Versions)
	rec.State = change.Ready
	rec.Seq++
	return rec
}

// withdrawn is rec, withdrawn.
func withdrawn(rec change.Record) change.Record {
	return moved(rec, change.Withdrawn)
}

// moved is rec moved to the state given and nothing more, as a record
// whose one parent is the record it replaces moves it.
func moved(rec change.Record, state string) change.Record {
	rec.State = state
	rec.Seq++
	return rec
}

// expect decides one write and holds the answer to what is wanted: "" for
// an allowed write, and otherwise a phrase the refusal has to hold.
func (r *records) expect(what, who, ref, old, new, says string) {
	r.t.Helper()
	got := r.p.decide(who, update{old: old, new: new, ref: ref})
	switch {
	case says == "" && got != "":
		r.t.Errorf("%s: refused: %s", what, got)
	case says != "" && !strings.Contains(got, says):
		r.t.Errorf("%s: got %q, want a refusal saying %q", what, got, says)
	}
}

const zeroID = "0000000000000000000000000000000000000000"

func TestCreatingAChangeRecord(t *testing.T) {
	r := newRecords(t)
	h := r.source("the head")
	h2 := r.source("another head")
	good := encode(t, first("alice", h))
	create := func(what, who, text string, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, zeroID, r.write(text, parents...), says)
	}
	mutate := func(f func(*change.Record)) string {
		rec := first("alice", h)
		f(&rec)
		return encode(t, rec)
	}

	create("the create row", "alice", good, "", h)

	// Who.
	create("an untagged key", "", good, "a key that names no principal", h)

	// Parent structure.
	create("no parent", "alice", good, "has 0 parents")
	create("an extra parent", "alice", good, "has 2 parents", h, h2)
	tree := r.git("", "mktree")
	create("a tree as the head", "alice",
		strings.Replace(good, h, tree, 1), "is a tree, and a head is a commit", tree)
	create("a head the repository lacks", "alice",
		strings.Replace(good, h, strings.Repeat("9", 40), 1), "does not hold", strings.Repeat("9", 40))

	// Record content.
	create("a head other than the parent", "alice", good, "the record commit's parent is "+h2, h2)
	create("another uploader", "alice", mutate(func(c *change.Record) { c.Versions[0].Uploader = "bob" }),
		"names bob as its uploader", h)
	create("another owner", "alice", mutate(func(c *change.Record) { c.Owner = "bob" }),
		"names bob as the owner", h)
	create("a seq other than 1", "alice", mutate(func(c *change.Record) { c.Seq = 2 }), "seq is 2", h)
	create("a withdrawn change", "alice", mutate(func(c *change.Record) { c.State = change.Withdrawn }),
		"state is withdrawn", h)
	create("two versions", "alice", encode(t, next(first("alice", h2), "alice", h)), "holds 2", h)
	create("another target", "alice", mutate(func(c *change.Record) { c.Target = "release" }),
		"names the target release", h)
	create("another change", "alice", mutate(func(c *change.Record) { c.ChangeID = strings.Repeat("k", 32) }),
		"names the change kkkk", h)
	r.expect("a ref for another target", "alice", change.Ref("release", changeID), zeroID, r.write(good, h),
		"refs/the-valley/changes/main/<change ID>")
	r.expect("a ref that names no change", "alice", change.RefPrefix+"main/topic", zeroID, r.write(good, h),
		"32 letters from k to z")

	// Parsing.
	create("a duplicate key", "alice", strings.Replace(good, `"seq": 1,`, `"seq": 1, "seq": 1,`, 1),
		`names the key "seq" twice`, h)
	create("a duplicate key in a version", "alice", strings.Replace(good, `"n": 1,`, `"n": 1, "n": 1,`, 1),
		`names the key "n" twice`, h)
	create("an unknown field", "alice", strings.Replace(good, `"seq": 1,`, `"seq": 1, "extra": 1,`, 1),
		`"extra"`, h)
	create("trailing data", "alice", good+"{}", "more after the value", h)
	create("bytes that are not UTF-8", "alice", strings.Replace(good, "the first", "the \xff", 1),
		"not valid UTF-8", h)

	// Tree shape.
	shaped := func(what, entries, says string) {
		t.Helper()
		commit := r.commit(r.git(entries, "mktree"), []string{h}, "a change record")
		r.expect(what, "alice", recordRef, zeroID, commit, says)
	}
	blob := r.blob(good)
	shaped("an empty tree", "", "its tree is empty")
	shaped("a second entry", fmt.Sprintf("100644 blob %s\trecord.json\n100644 blob %s\tz\n", blob, blob),
		"more than one entry")
	shaped("another name", fmt.Sprintf("100644 blob %s\trecord.jsonx\n", blob), `"record.jsonx"`)
	shaped("an executable", fmt.Sprintf("100755 blob %s\trecord.json\n", blob), "mode 100755")
	shaped("a subtree", fmt.Sprintf("040000 tree %s\trecord.json\n", tree), "mode 40000")
	r.expect("a tree as the record", "alice", recordRef, zeroID, r.recordTree(good),
		"points at a tree, where a change record holds a commit")

	// Bounds. Each object past its bound is refused on its type and size
	// alone, before its content is asked for.
	big := r.commit(r.recordTree(good), []string{h}, strings.Repeat("x", maxRecordCommit))
	r.expect("an oversized commit", "alice", recordRef, zeroID, big, "more than the 4096")
	long := r.commit(r.git(fmt.Sprintf("100644 blob %s\t%s\n", blob, strings.Repeat("r", maxRecordTree)), "mktree"),
		[]string{h}, "a change record")
	r.expect("an oversized tree", "alice", recordRef, zeroID, long, "more than the 1024")
	create("an oversized record.json", "alice", good+strings.Repeat(" ", maxRecordBlob), "more than the 65536", h)
	objects, err := openObjects()
	if err != nil {
		t.Fatal(err)
	}
	objects.subject = "a change record"
	_, err = readRecord(objects, big)
	objects.close()
	if err == nil || strings.Join(objects.requests, "; ") != "info "+big {
		t.Errorf("an oversized commit: requests = %v, error = %v; want its info alone", objects.requests, err)
	}

	// A record commit naming a parent after its other headers names one
	// git does not read, so it is refused rather than read two ways.
	sneaky := r.git(fmt.Sprintf("tree %s\nparent %s\nauthor a <a@localhost> 1700000000 +0000\nparent %s\n\nx\n",
		r.recordTree(good), h, h2), "hash-object", "-t", "commit", "-w", "--literally", "--stdin")
	r.expect("a parent among the later headers", "alice", recordRef, zeroID, sneaky, "does not read as a parent")
}

func TestAddingAVersion(t *testing.T) {
	r := newRecords(t)
	h1, h2, h3 := r.source("one"), r.source("two"), r.source("three")
	v1 := first("alice", h1)
	old := r.write(encode(t, v1), h1)
	update := func(what, who, text, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, old, r.write(text, parents...), says)
	}
	mutate := func(f func(*change.Record)) string {
		rec := next(v1, "alice", h2)
		f(&rec)
		return encode(t, rec)
	}
	good := encode(t, next(v1, "alice", h2))

	update("the new version row", "alice", good, "", old, h2)
	update("the new version row, by the operator", "operator", encode(t, next(v1, "operator", h2)), "", old, h2)

	// Who.
	update("a principal that is neither owner nor operator", "mallory", encode(t, next(v1, "mallory", h2)),
		"only the change's owner, alice, or a principal holding the request grant", old, h2)
	update("an untagged key", "", good, "a key that names no principal", old, h2)

	// Parent structure.
	update("the parents in the wrong order", "alice", good, "has the record it replaces as its first parent", h2, old)
	update("an extra parent", "alice", good, "has the record it replaces as its first parent", old, h2, h3)
	update("a first parent that is not the old record", "alice", good, "as its first parent", h1, h2)
	tree := r.git("", "mktree")
	update("a tree as the head", "alice", strings.Replace(good, h2, tree, 1), "is a tree, and a head is a commit", old, tree)

	// Record content.
	update("a changed historical entry", "alice", mutate(func(c *change.Record) { c.Versions[0].Title = "rewritten" }),
		"changes version 1", old, h2)
	update("a historical entry with another uploader", "alice",
		mutate(func(c *change.Record) { c.Versions[0].Uploader = "bob" }), "changes version 1", old, h2)
	update("a skipped n", "alice", strings.Replace(good, `"n": 2`, `"n": 3`, 1), "no gap", old, h2)
	update("seq not incremented", "alice", mutate(func(c *change.Record) { c.Seq = 1 }), "seq is 1", old, h2)
	update("seq incremented twice", "alice", mutate(func(c *change.Record) { c.Seq = 3 }), "seq is 3", old, h2)
	update("the owner changed", "alice", mutate(func(c *change.Record) { c.Owner = "bob" }),
		"changes the owner from alice to bob", old, h2)
	update("the target changed", "alice", mutate(func(c *change.Record) { c.Target = "release" }),
		"names the target release", old, h2)
	update("the change ID changed", "alice", mutate(func(c *change.Record) { c.ChangeID = strings.Repeat("k", 32) }),
		"names the change kkkk", old, h2)
	update("the active head again", "alice", encode(t, next(v1, "alice", h1)), "needs a new head", old, h1)
	update("a head other than the second parent", "alice", good, "second parent is "+h3, old, h3)
	update("another uploader", "alice", encode(t, next(v1, "bob", h2)), "names bob as its uploader", old, h2)
	update("active left on the old version", "alice", mutate(func(c *change.Record) { c.Active = 1 }),
		"active is 1", old, h2)
	update("a new version that withdraws", "alice", mutate(func(c *change.Record) { c.State = change.Withdrawn }),
		"state is withdrawn", old, h2)
	update("two new versions", "alice", encode(t, next(next(v1, "alice", h3), "alice", h2)), "holds 3", old, h2)
	update("no new version", "alice", encode(t, withdrawn(withdrawn(v1))), "adds one version", old, h2)
}

func TestWithdrawingAndComingBack(t *testing.T) {
	r := newRecords(t)
	h1, h2 := r.source("one"), r.source("two")
	v1 := first("alice", h1)
	ready := r.write(encode(t, v1), h1)
	withdraw := func(what, who, text, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, ready, r.write(text, parents...), says)
	}
	mutate := func(f func(*change.Record)) string {
		rec := withdrawn(v1)
		f(&rec)
		return encode(t, rec)
	}
	good := encode(t, withdrawn(v1))

	withdraw("the withdraw row", "alice", good, "", ready)
	withdraw("the withdraw row, by the operator", "operator", good, "", ready)

	withdraw("a principal that is neither owner nor operator", "mallory", good, "only the change's owner", ready)
	withdraw("an untagged key", "", good, "a key that names no principal", ready)
	withdraw("an extra parent", "alice", good, "first parent", ready, h1, h2)
	withdraw("the old record as a second parent", "alice", good, "a new version adds one version", ready, ready)
	withdraw("no parent", "alice", good, "first parent")
	withdraw("ready to ready without a new version", "alice", mutate(func(c *change.Record) { c.State = change.Ready }),
		"the change is ready already", ready)
	withdraw("seq not incremented", "alice", mutate(func(c *change.Record) { c.Seq = 1 }), "seq is 1", ready)
	withdraw("the owner changed", "alice", mutate(func(c *change.Record) { c.Owner = "bob" }), "changes the owner", ready)
	withdraw("a version changed", "alice", mutate(func(c *change.Record) { c.Versions[0].Title = "x" }),
		"changes the versions", ready)
	withdraw("the target changed", "alice", mutate(func(c *change.Record) { c.Target = "release" }),
		"names the target release", ready)

	// From withdrawn, the change comes back ready: at the version it was
	// withdrawn at, or with a new one. It never comes back as a draft.
	gone := withdrawn(v1)
	old := r.write(encode(t, gone), ready)
	again := func(what, who, text, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, old, r.write(text, parents...), says)
	}
	again("the re-ready row, at a new version", "alice", encode(t, next(gone, "alice", h2)), "", old, h2)
	again("the re-ready row, at a new version, by the operator", "operator", encode(t, next(gone, "operator", h2)), "", old, h2)
	again("the re-ready row, at the same version", "alice", encode(t, moved(gone, change.Ready)), "", old)
	again("the re-ready row, at the same version, by the operator", "operator", encode(t, moved(gone, change.Ready)), "", old)
	again("a new version at the same head", "alice", encode(t, next(gone, "alice", h1)), "needs a new head", old, h1)
	again("withdrawn twice", "alice", encode(t, withdrawn(gone)), "withdrawn already", old)
	again("withdrawn to draft", "alice", encode(t, moved(gone, change.Draft)), "never as a draft", old)
	again("withdrawn to draft by the operator", "operator", encode(t, moved(gone, change.Draft)), "never as a draft", old)
	again("a new version left withdrawn", "alice",
		encode(t, func() change.Record { c := next(gone, "alice", h2); c.State = change.Withdrawn; return c }()),
		"state is withdrawn", old, h2)
	again("a new version left a draft", "alice",
		encode(t, func() change.Record { c := next(gone, "alice", h2); c.State = change.Draft; return c }()),
		"state is draft", old, h2)
	again("re-ready by another principal", "mallory", encode(t, next(gone, "mallory", h2)), "only the change's owner", old, h2)
	again("re-ready at the same version by another principal", "mallory", encode(t, moved(gone, change.Ready)),
		"only the change's owner", old)
	again("re-ready at the same version with seq not incremented", "alice",
		encode(t, func() change.Record { c := moved(gone, change.Ready); c.Seq--; return c }()), "seq is 2", old)
}

func TestUnreadyingAndReadyingAgain(t *testing.T) {
	r := newRecords(t)
	h1, h2, h3 := r.source("one"), r.source("two"), r.source("three")
	v2 := next(first("alice", h1), "alice", h2)
	ready := r.write(encode(t, v2), r.write(encode(t, first("alice", h1)), h1), h2)
	unready := func(what, who, text, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, ready, r.write(text, parents...), says)
	}
	mutate := func(rec change.Record, f func(*change.Record)) string {
		f(&rec)
		return encode(t, rec)
	}
	good := encode(t, moved(v2, change.Draft))

	// Ready to draft changes the state and the seq, and nothing else.
	unready("the unready row", "alice", good, "", ready)
	unready("the unready row, by the operator", "operator", good, "", ready)
	unready("unready by another principal", "mallory", good, "only the change's owner", ready)
	unready("unready by an untagged key", "", good, "a key that names no principal", ready)
	unready("unready to an earlier version", "alice", mutate(moved(v2, change.Draft), func(c *change.Record) { c.Active = 1 }),
		"changes active from 2 to 1", ready)
	unready("unready with seq not incremented", "alice", mutate(moved(v2, change.Draft), func(c *change.Record) { c.Seq-- }),
		"seq is 2", ready)
	unready("unready with a version changed", "alice",
		mutate(moved(v2, change.Draft), func(c *change.Record) {
			c.Versions = append([]change.Version(nil), c.Versions...)
			c.Versions[1].Title = "x"
		}),
		"changes the versions", ready)
	unready("unready with the owner changed", "alice", mutate(moved(v2, change.Draft), func(c *change.Record) { c.Owner = "bob" }),
		"changes the owner", ready)
	unready("unready with a new version", "alice", encode(t, func() change.Record { c := next(v2, "alice", h3); c.State = change.Draft; return c }()),
		"state is draft", ready, h3)

	// From draft, the change is readied again at the same version or a new
	// one, or withdrawn.
	draft := moved(v2, change.Draft)
	old := r.write(good, ready)
	again := func(what, who, text, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, old, r.write(text, parents...), says)
	}
	again("the re-ready row, at the same version", "alice", encode(t, moved(draft, change.Ready)), "", old)
	again("the re-ready row, at the same version, by the operator", "operator", encode(t, moved(draft, change.Ready)), "", old)
	again("the re-ready row, at a new version", "alice", encode(t, next(draft, "alice", h3)), "", old, h3)
	again("the re-ready row, at a new version, by the operator", "operator", encode(t, next(draft, "operator", h3)), "", old, h3)
	again("the withdraw row, from a draft", "alice", encode(t, withdrawn(draft)), "", old)
	again("the withdraw row, from a draft, by the operator", "operator", encode(t, withdrawn(draft)), "", old)
	again("re-ready by another principal", "mallory", encode(t, moved(draft, change.Ready)), "only the change's owner", old)
	again("a new version by another principal", "mallory", encode(t, next(draft, "mallory", h3)), "only the change's owner", old, h3)
	again("withdraw by another principal", "mallory", encode(t, withdrawn(draft)), "only the change's owner", old)
	again("draft to draft", "alice", encode(t, moved(draft, change.Draft)), "the change is draft already", old)
	again("a new version at the active head", "alice", encode(t, next(draft, "alice", h2)), "needs a new head", old, h2)
	again("a new version left a draft", "alice",
		encode(t, func() change.Record { c := next(draft, "alice", h3); c.State = change.Draft; return c }()),
		"state is draft", old, h3)
	again("re-ready at another version", "alice", mutate(moved(draft, change.Ready), func(c *change.Record) { c.Active = 1 }),
		"changes active from 2 to 1", old)
	again("re-ready with seq incremented twice", "alice", mutate(moved(draft, change.Ready), func(c *change.Record) { c.Seq++ }),
		"seq is 5", old)
	again("re-ready with a version changed", "alice",
		mutate(moved(draft, change.Ready), func(c *change.Record) {
			c.Versions = append([]change.Version(nil), c.Versions...)
			c.Versions[0].Head = change.HeadPrefix + h3
		}),
		"changes the versions", old)
}

func TestOnlyTheOperatorDeletesARecord(t *testing.T) {
	r := newRecords(t)
	h := r.source("one")
	old := r.write(encode(t, first("alice", h)), h)
	r.expect("the delete row", "operator", recordRef, old, zeroID, "")
	r.expect("a delete by the owner", "alice", recordRef, old, zeroID, "only a principal holding the request grant may delete")
	r.expect("a delete by an untagged key", "", recordRef, old, zeroID, "a key that names no principal")
}

// The bounds about a push as a whole are checked by preReceive, before
// any record is read.
func TestAPushIsBoundInTheRecordsItWrites(t *testing.T) {
	r := newRecords(t)
	h := r.source("one")
	dir := t.TempDir()
	policyFile := filepath.Join(dir, "policy.json")
	grantsFile := filepath.Join(dir, "grants")
	if err := os.WriteFile(policyFile, []byte(`{"refs": [], "writers": [], "grants": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grantsFile, []byte("request operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	push := func(repo repository, who string, lines []string) (int, string) {
		var stderr bytes.Buffer
		code := preReceive([]string{"--project", "project", "--policy", policyFile, "--grants", grantsFile,
			"--principal", who}, strings.NewReader(strings.Join(lines, "\n")+"\n"), &stderr, repo)
		return code, stderr.String()
	}
	// ids makes n distinct change IDs, from an offset.
	ids := func(from, n int) []string {
		var out []string
		for i := from; i < from+n; i++ {
			id := []byte(strings.Repeat("k", 32))
			for j, x := 31, i; x > 0; j, x = j-1, x/16 {
				id[j] = byte('z' - x%16)
			}
			out = append(out, string(id))
		}
		return out
	}

	// 33 record updates are refused before any is read, or any ref looked
	// up: this repository fails the test if one is.
	var many []string
	for _, id := range ids(0, maxRecordUpdates+1) {
		many = append(many, zeroID+" "+h+" "+change.Ref("main", id))
	}
	code, says := push(unreadable{t}, "alice", many)
	if code == 0 || !strings.Contains(says, "it updates 33 change records, and a push may update at most 32") {
		t.Errorf("33 record updates: exit %d, %q", code, says)
	}

	// 32 are within the bound, and each is then read and decided.
	var creates []string
	for _, id := range ids(0, maxRecordUpdates) {
		rec := first("alice", h)
		rec.ChangeID = id
		creates = append(creates, zeroID+" "+r.write(encode(t, rec), h)+" "+change.Ref("main", id))
	}
	if code, says := push(gitRepository{}, "alice", creates); code != 0 {
		t.Errorf("32 record creates: exit %d, %q", code, says)
	}

	// Those were decided and never written. With 1999 records in place,
	// one more fits and two more do not. Once the repository is full, a
	// delete still goes through.
	var refs strings.Builder
	for _, id := range ids(maxRecordUpdates, maxRecords-1) {
		fmt.Fprintf(&refs, "create %s %s\n", change.Ref("main", id), h)
	}
	r.git(refs.String(), "update-ref", "--stdin")
	line := func(id string) string {
		rec := first("alice", h)
		rec.ChangeID = id
		return zeroID + " " + r.write(encode(t, rec), h) + " " + change.Ref("main", id)
	}
	more := ids(maxRecordUpdates+maxRecords, 2)
	code, says = push(gitRepository{}, "alice", []string{line(more[0]), line(more[1])})
	if code == 0 || !strings.Contains(says, "holds 1999 change records, and this push would create 2 more, past the 2000") {
		t.Errorf("two records past the bound: exit %d, %q", code, says)
	}
	if code, says := push(gitRepository{}, "alice", []string{line(more[0])}); code != 0 {
		t.Errorf("the last record within the bound: exit %d, %q", code, says)
	}
	r.git("", "update-ref", change.Ref("main", more[0]), h)
	code, says = push(gitRepository{}, "alice", []string{line(more[1])})
	if code == 0 || !strings.Contains(says, "holds 2000 change records") {
		t.Errorf("a record past a full repository: exit %d, %q", code, says)
	}
	if code, says := push(gitRepository{}, "operator", []string{h + " " + zeroID + " " + change.Ref("main", more[0])}); code != 0 {
		t.Errorf("a delete from a full repository: exit %d, %q", code, says)
	}
}

// unreadable is a repository whose objects must not be read.
type unreadable struct{ t *testing.T }

func (u unreadable) symbolic([]string) (map[string]bool, error) {
	u.t.Error("the refs were read")
	return nil, errors.New("unread")
}
func (u unreadable) notes(string) (map[string][]byte, error) {
	u.t.Error("an object was read")
	return nil, errors.New("unread")
}
func (u unreadable) record(string) (recordCommit, error) {
	u.t.Error("a record was read")
	return recordCommit{}, errors.New("unread")
}
func (u unreadable) kind(string) (string, error) {
	u.t.Error("an object was read")
	return "", errors.New("unread")
}
func (u unreadable) records() (int, error) {
	u.t.Error("the records were counted")
	return 0, errors.New("unread")
}
