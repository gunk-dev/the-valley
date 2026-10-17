package main

import (
	"fmt"
	"strings"
	"testing"

	"the-valley/change"
)

var reportRef = change.RestackRef("main", changeID)

// restackers is a repository where restacker holds the restack grant and
// both holds it beside the request grant.
func restackers(t *testing.T) *records {
	r := newRecords(t)
	r.p.grants["restacker"] = map[string]bool{grantRestack: true}
	r.p.grants["both"] = map[string]bool{grantRestack: true, grantRequest: true}
	return r
}

// restacked is rec with a new version at head h uploaded by who, on the
// active version's branch and under its title, as the restack row has it.
func restacked(rec change.Record, who, h string) change.Record {
	was := rec.ActiveVersion()
	rec = next(rec, who, h)
	rec.Versions[len(rec.Versions)-1].Title = was.Title
	rec.Versions[len(rec.Versions)-1].Branch = was.Branch
	return rec
}

func TestRestackingAVersion(t *testing.T) {
	r := restackers(t)
	h1, h2, h3 := r.source("one"), r.source("two"), r.source("three")
	v1 := first("alice", h1)
	ready := r.write(encode(t, v1), h1)
	update := func(what, who, old, text, says string, parents ...string) {
		t.Helper()
		r.expect(what, who, recordRef, old, r.write(text, parents...), says)
	}
	mutate := func(f func(*change.Record)) string {
		rec := restacked(v1, "restacker", h2)
		f(&rec)
		return encode(t, rec)
	}
	good := encode(t, restacked(v1, "restacker", h2))

	update("the restack row", "restacker", ready, good, "", ready, h2)

	// Who. A principal without the grant is refused as before, and the
	// grant opens nothing to a key that names no principal.
	update("a principal without the grant", "mallory", ready, encode(t, restacked(v1, "mallory", h2)),
		"only the change's owner, alice, or a principal holding the request grant", ready, h2)
	update("an untagged key", "", ready, good, "a key that names no principal", ready, h2)

	// The state before and after.
	for _, state := range []string{change.Draft, change.Withdrawn} {
		paused := moved(v1, state)
		old := r.write(encode(t, paused), ready)
		update("a version on a change that is "+state, "restacker", old,
			encode(t, restacked(paused, "restacker", h2)), "the change is "+state+", and the restack grant adds a version only to a change that is ready", old, h2)
	}
	for _, state := range []string{change.Draft, change.Withdrawn} {
		update("a move to "+state, "restacker", ready, encode(t, moved(v1, state)),
			"the restack grant changes no state", ready)
	}
	update("a new version that leaves the change a draft", "restacker", ready,
		mutate(func(c *change.Record) { c.State = change.Draft }), "state is draft", ready, h2)

	// What every new version is held to holds here too.
	update("two new versions", "restacker", ready,
		encode(t, restacked(restacked(v1, "restacker", h3), "restacker", h2)), "holds 3", ready, h2)
	update("a changed earlier version", "restacker", ready,
		mutate(func(c *change.Record) { c.Versions[0].Title = "rewritten" }), "changes version 1", ready, h2)
	update("the owner changed", "restacker", ready, mutate(func(c *change.Record) { c.Owner = "restacker" }),
		"changes the owner from alice to restacker", ready, h2)
	update("the target changed", "restacker", ready, mutate(func(c *change.Record) { c.Target = "release" }),
		"names the target release", ready, h2)
	update("a forged uploader", "restacker", ready, encode(t, restacked(v1, "alice", h2)),
		"names alice as its uploader, and restacker is pushing it", ready, h2)
	update("seq incremented twice", "restacker", ready, mutate(func(c *change.Record) { c.Seq = 3 }),
		"seq is 3", ready, h2)
	update("active left on the old version", "restacker", ready, mutate(func(c *change.Record) { c.Active = 1 }),
		"active is 1", ready, h2)
	update("the active head again", "restacker", ready, encode(t, restacked(v1, "restacker", h1)),
		"needs a new head", ready, h1)
	update("a head other than the second parent", "restacker", ready, good, "second parent is "+h3, ready, h3)

	// What the restack row holds a version to beyond those.
	update("another branch", "restacker", ready, mutate(func(c *change.Record) { c.Versions[1].Branch = "elsewhere" }),
		`names the branch "elsewhere"`, ready, h2)
	update("no branch", "restacker", ready, mutate(func(c *change.Record) { c.Versions[1].Branch = "" }),
		`names the branch ""`, ready, h2)
	update("another title", "restacker", ready, mutate(func(c *change.Record) { c.Versions[1].Title = "retitled" }),
		`is titled "retitled"`, ready, h2)

	// Deleting is the request grant's alone.
	r.expect("a delete", "restacker", recordRef, ready, zeroID, "only a principal holding the request grant may delete")

	// A holder that owns the change, or holds the request grant beside
	// it, writes by those rows, unrestricted by this one.
	own := first("restacker", h1)
	mine := r.write(encode(t, own), h1)
	r.expect("its own record, created", "restacker", recordRef, zeroID, mine, "")
	update("its own record, unreadied", "restacker", mine, encode(t, moved(own, change.Draft)), "", mine)
	update("its own record, retitled", "restacker", mine, encode(t, next(own, "restacker", h2)), "", mine, h2)
	update("another's record, unreadied with the request grant", "both", ready, encode(t, moved(v1, change.Draft)), "", ready)
	r.expect("another's record, deleted with the request grant", "both", recordRef, ready, zeroID, "")
}

// report is a well-formed report about the change, of a conflict
// observed at the head h.
func report(t *testing.T, h string) change.RestackReport {
	t.Helper()
	failing, tip := changeID, change.HeadPrefix+h
	return change.RestackReport{
		Schema: change.RestackSchema, ChangeID: changeID, Target: "main",
		TargetTip: change.HeadPrefix + h, Operation: "op-1", Result: change.RestackConflict,
		Layers: []change.RestackLayer{{
			ChangeID: changeID, Record: change.HeadPrefix + h, Version: 1, Head: change.HeadPrefix + h,
			Branch: "topic", BranchTip: &tip,
		}},
		Failing: &failing, Conflicts: []string{"a.go"},
	}
}

func encodeReport(t *testing.T, rep change.RestackReport) string {
	t.Helper()
	out, err := change.EncodeRestackReport(&rep)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// writeReport writes a report commit holding text as report.json.
func (r *records) writeReport(text string, parents ...string) string {
	tree := r.git(fmt.Sprintf("100644 blob %s\treport.json\n", r.blob(text)), "mktree")
	return r.commit(tree, parents, "a restack report")
}

func TestWritingARestackReport(t *testing.T) {
	r := restackers(t)
	h := r.source("one")
	good := encodeReport(t, report(t, h))
	expect := func(what, who, old, new, says string) {
		t.Helper()
		r.expect(what, who, reportRef, old, new, says)
	}

	// About a change the repository records.
	expect("a report about a change no record names", "restacker", zeroID, r.writeReport(good),
		"holds no record of the change "+changeID)
	r.git("", "update-ref", recordRef, r.write(encode(t, first("alice", h)), h))

	first := r.writeReport(good)
	expect("the first report", "restacker", zeroID, first, "")
	r.git("", "update-ref", reportRef, first)
	second := r.writeReport(good, first)
	expect("a report extending it", "restacker", first, second, "")

	// Who.
	for _, who := range []string{"alice", "operator", ""} {
		expect("a report by "+who, who, first, second, "refs/the-valley/restack/ takes writes only from a principal holding the restack grant")
	}

	// History.
	expect("a delete", "restacker", first, zeroID, "no push deletes one")
	expect("a first report with a parent", "restacker", zeroID, second, "has no parent, and this one has 1")
	expect("a report that starts over", "restacker", first, r.writeReport(good), "as its one parent")
	expect("a report on another parent", "restacker", first, r.writeReport(good, h), "as its one parent")
	expect("a report with two parents", "restacker", first, r.writeReport(good, first, h), "as its one parent")

	// The ref.
	other := change.RestackRef("release", changeID)
	r.expect("another target", "restacker", other, zeroID, r.writeReport(good), "refs/the-valley/restack/main/<change ID>")
	r.expect("no change ID", "restacker", change.RestackPrefix+"main/topic", zeroID, r.writeReport(good), "32 letters from k to z")
	r.expect("a ref that only begins with the namespace", "restacker", "refs/the-valley/restacked/main/"+changeID, zeroID,
		r.writeReport(good), "written only by the machinery")

	// The report.
	rep := report(t, h)
	rep.ChangeID, rep.Layers[0].ChangeID, *rep.Failing = strings.Repeat("k", 32), strings.Repeat("k", 32), strings.Repeat("k", 32)
	expect("a report about another change", "restacker", first, r.writeReport(encodeReport(t, rep), first),
		"about the change kkkk")
	rep = report(t, h)
	rep.Target = "release"
	expect("a report naming another target", "restacker", first, r.writeReport(encodeReport(t, rep), first),
		"names the target release")
	expect("an unknown field", "restacker", first,
		r.writeReport(strings.Replace(good, `"result"`, `"extra": 1, "result"`, 1), first), `"extra"`)
	expect("a report that is no JSON", "restacker", first, r.writeReport("conflict", first), "does not read")

	// The commit's shape.
	blob := r.blob(good)
	shaped := func(what, entries, says string) {
		t.Helper()
		expect(what, "restacker", first, r.commit(r.git(entries, "mktree"), []string{first}, "a restack report"), says)
	}
	shaped("a second file", fmt.Sprintf("100644 blob %s\treport.json\n100644 blob %s\tz\n", blob, blob),
		"a restack report's tree holds report.json alone")
	shaped("another name", fmt.Sprintf("100644 blob %s\trecord.json\n", blob), `"record.json"`)
	shaped("an executable", fmt.Sprintf("100755 blob %s\treport.json\n", blob), "mode 100755")

	// Bounds, each refused on the object's type and size alone.
	expect("an oversized report.json", "restacker", first, r.writeReport(good+strings.Repeat(" ", change.MaxReportBlob), first),
		"more than the 65536")
	big := r.commit(r.git(fmt.Sprintf("100644 blob %s\treport.json\n", blob), "mktree"), []string{first},
		strings.Repeat("x", change.MaxReportCommit))
	expect("an oversized commit", "restacker", first, big, "more than the 4096")
	objects, err := openObjectsWithin(budget{subject: "a restack report", depth: 1, entriesInTree: 1, entries: 1,
		treeSize: change.MaxReportTree, fileSize: change.MaxReportBlob, bytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	_, err = readFileCommit(objects, big, reportLayout)
	objects.close()
	if err == nil || strings.Join(objects.requests, "; ") != "info "+big {
		t.Errorf("an oversized commit: requests = %v, error = %v; want its info alone", objects.requests, err)
	}
}

// A push may update at most maxReportUpdates reports, and one that names
// more is refused before any is read.
func TestAPushIsBoundInTheReportsItWrites(t *testing.T) {
	var lines []string
	for i := 0; i <= maxReportUpdates; i++ {
		id := strings.Repeat("k", 30) + string(rune('k'+i/16)) + string(rune('k'+i%16))
		lines = append(lines, zeroID+" "+strings.Repeat("1", 40)+" "+change.RestackRef("main", id))
	}
	code, says := pushTo(t, unreadable{t}, "restacker", lines)
	if code == 0 || !strings.Contains(says, "it updates 65 restack reports, and a push may update at most 64") {
		t.Errorf("a push of %d reports: exit %d, %q", len(lines), code, says)
	}
}
