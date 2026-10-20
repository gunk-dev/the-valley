package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/rebase"
)

// id is the nth of a run of change IDs, 32 letters from k to z.
func id(n int) string {
	var b strings.Builder
	for i := 0; i < 32; i++ {
		b.WriteByte(byte('k' + (n>>(i%8*2))%16))
	}
	return b.String()
}

// oversized is an observation of a conflicted family that holds more than
// a report may in every way: 70 recorded changes, 150 conflict paths with
// unusual names among them, 70 blocking requests, one of whose refs is
// too long and one of whose names is no branch name, and a refusal longer
// than a report holds, with control characters in it.
func oversized() Observation {
	o := Observation{Action: restack, Result: change.RestackConflict, Conflicts: []string{}, Requests: []RequestPlan{}}
	for i := 0; i < 70; i++ {
		o.Layers = append(o.Layers, ObservedLayer{
			Name: id(i)[:6], ID: id(i), Branch: fmt.Sprintf("stack/%02d", i), BranchTip: strings.Repeat("1", 40),
			Record: strings.Repeat("2", 40), Version: 1, Head: strings.Repeat("1", 40), State: "ready",
		})
	}
	o.Failing = id(40)
	o.Conflicts = append(o.Conflicts, "line\nbreak.txt", "bell\x07.txt", "latin1-\xe9.txt", strings.Repeat("long/", 220)+"x")
	for i := len(o.Conflicts); i < 150; i++ {
		o.Conflicts = append(o.Conflicts, fmt.Sprintf("src/file-%03d.go", i))
	}
	o.Requests = append(o.Requests,
		RequestPlan{Name: strings.Repeat("r", 300), Head: strings.Repeat("3", 40)},
		RequestPlan{Name: "no..branch", Head: strings.Repeat("3", 40)})
	for i := 0; i < 68; i++ {
		o.Requests = append(o.Requests, RequestPlan{Name: fmt.Sprintf("request-%02d", i), Head: strings.Repeat("3", 40)})
	}
	o.Refusal = strings.Repeat("the layer conflicts\nwith main\t", 150)
	return o
}

// A report of an observation that holds more than the format allows is a
// fallback that the format takes: the change's own layer and the failing
// one, the conflict paths and requests that fit, up to the format's
// count, and a refusal cut to fit, free of control characters, ending
// with what it leaves out and the digest of the whole observation.
func TestAReportFallsBackWithinEveryBound(t *testing.T) {
	o := oversized()
	self := id(3)
	r, omitted, err := bound(fullReport("op-1", strings.Repeat("4", 40), self, o, nil), o.Digest())
	if err != nil {
		t.Fatal(err)
	}
	data, err := change.EncodeRestackReport(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := change.ParseRestackReport(data); err != nil || len(data) > change.MaxReportBlob {
		t.Fatalf("the fallback is %d bytes and reads as %v", len(data), err)
	}
	if len(r.Layers) != 2 || r.Layers[0].ChangeID != self || r.Layers[1].ChangeID != o.Failing {
		t.Errorf("the fallback keeps the layers %+v", r.Layers)
	}
	if len(r.Conflicts) != change.MaxConflictPaths || r.Conflicts[0] != "src/file-004.go" {
		t.Errorf("the fallback keeps %d conflict paths, first %q", len(r.Conflicts), r.Conflicts[0])
	}
	if len(r.Requests) != 64 || r.Requests[0].Ref != rebase.RequestPrefix+"request-00" {
		t.Errorf("the fallback keeps %d requests, first %+v", len(r.Requests), r.Requests[0])
	}
	if r.Refusal == nil || len(*r.Refusal) > change.MaxRefusal || !strings.Contains(*r.Refusal, "digest "+o.Digest()) ||
		!strings.HasPrefix(*r.Refusal, "the layer conflicts with main the layer") {
		t.Errorf("the fallback's refusal is %q", *r.Refusal)
	}
	for _, want := range []string{"68 of the family's 70 recorded changes", "50 of its 150 conflict paths", "6 of its 70 blocking requests", "the end of its refusal"} {
		if !strings.Contains(strings.Join(omitted, "; "), want) {
			t.Errorf("the fallback does not say it leaves out %s: %v", want, omitted)
		}
	}
}

// A report whose every field fits on its own, and that holds more than 64
// KiB in all, sheds its conflict paths, then its requests, then the
// layers it does not need, until it fits.
func TestAReportTooLargeInAllSheds(t *testing.T) {
	o := Observation{Action: restack, Result: change.RestackConflict, Requests: []RequestPlan{}}
	for i := 0; i < 64; i++ {
		o.Layers = append(o.Layers, ObservedLayer{
			Name: id(i)[:6], ID: id(i), Branch: fmt.Sprintf("%02d/", i) + strings.Repeat("b", 900), BranchTip: strings.Repeat("1", 40),
			Record: strings.Repeat("2", 40), Version: 1, Head: strings.Repeat("1", 40), State: "ready",
		})
	}
	o.Failing = id(0)
	for i := 0; i < 100; i++ {
		o.Conflicts = append(o.Conflicts, fmt.Sprintf("%03d/", i)+strings.Repeat("c", 1000))
	}
	full := fullReport("op-1", strings.Repeat("4", 40), id(9), o, nil)
	if err := full.Check(); err != nil {
		t.Fatalf("each field should fit on its own: %v", err)
	}
	r, omitted, err := bound(full, o.Digest())
	if err != nil {
		t.Fatal(err)
	}
	data, err := change.EncodeRestackReport(r)
	if err != nil || len(data) > change.MaxReportBlob {
		t.Fatalf("the fallback is %d bytes: %v", len(data), err)
	}
	if len(r.Conflicts) != 0 || len(r.Layers) != 2 || !strings.Contains(strings.Join(omitted, "; "), "the 100 conflict paths left") {
		t.Errorf("the fallback keeps %d paths and %d layers, leaving out %v", len(r.Conflicts), len(r.Layers), omitted)
	}
}

// Two reports are the same when their observations and result are,
// whatever operation wrote each. Two fallbacks that show the same are told
// apart by the digest of what they leave out.
func TestReportsAreTheSameByWhatTheySay(t *testing.T) {
	o := oversized()
	tip := strings.Repeat("4", 40)
	a, _, err := bound(fullReport("op-1", tip, id(3), o, nil), o.Digest())
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := bound(fullReport("op-2", tip, id(3), o, nil), o.Digest())
	if !sameReport(a, b) {
		t.Error("the same observation written by two operations is not the same report")
	}
	o.Conflicts[149] = "src/another.go"
	c, _, _ := bound(fullReport("op-2", tip, id(3), o, nil), o.Digest())
	if sameReport(a, c) {
		t.Error("two fallbacks of different observations are the same report")
	}
	if strings.Join(a.Conflicts, " ") != strings.Join(c.Conflicts, " ") {
		t.Fatal("the test's fallbacks should show the same conflict paths")
	}
}

// A conflict over more paths than a report holds keeps them all in the
// journal, and its report, which the format takes, names what fits and
// says what it leaves out.
func TestAConflictKeepsEveryPathInTheJournal(t *testing.T) {
	f := newFixture(t, "")
	f.git("checkout", "--quiet", "-b", "a", "main")
	names := []string{"line\nbreak.txt", "bell\x07.txt"}
	for i := 0; i < 120; i++ {
		names = append(names, fmt.Sprintf("many/file-%03d.txt", i))
	}
	write := func(content string) {
		for _, n := range names {
			path := filepath.Join(f.work, n)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		f.git("add", "-A")
	}
	write("a\n")
	f.git("commit", "--quiet", "-m", "layer a")
	f.git("push", "--quiet", "origin", "a")
	head := f.git("rev-parse", "HEAD")
	f.writeRecord(idA, &change.Record{
		Schema: change.Schema, ChangeID: idA, Target: rebase.Target, Owner: "alice", State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: "A", Branch: "a", Uploader: "alice"}},
	}, "", head)
	f.git("checkout", "--quiet", "main")
	write("main\n")
	f.git("commit", "--quiet", "-m", "main has its own")
	f.git("push", "--quiet", "origin", "main")

	r := newRestacker(t, f)
	j := r.journal()
	a := of(t, j, idA)
	if a.Observed.Result != change.RestackConflict || len(a.Observed.Conflicts) != len(names) || a.Branches != nil {
		t.Fatalf("the journal keeps %d conflict paths, of %d, as %s", len(a.Observed.Conflicts), len(names), a.Observed.Result)
	}
	rep := a.Reports[0]
	if len(rep.Pending.Report.Conflicts) != change.MaxConflictPaths || *rep.Pending.Report.Failing != idA ||
		!strings.Contains(strings.Join(rep.Omitted, " "), "22 of its 122 conflict paths") {
		t.Errorf("the report names %d paths and leaves out %v", len(rep.Pending.Report.Conflicts), rep.Omitted)
	}
	if !strings.Contains(*rep.Pending.Report.Refusal, a.Observed.Digest()) {
		t.Errorf("the report's refusal names no digest: %q", *rep.Pending.Report.Refusal)
	}
}

// A report that says what the change's current report on origin says is
// not built again, under a new operation: a held family whose report has
// been pushed has nothing left to write.
func TestAnUnchangedReportIsNotBuiltAgain(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idE, "alice", "e", "main", "E's title")
	f.plainBranch("e-plain", "e")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	if w := of(t, j, idE).Reports[0].Pending.Push(context.Background(), r.g); w.Push != rebase.Pushed {
		t.Fatalf("e's report: %+v", w)
	}

	p, s := r.plan()
	out, err := build(context.Background(), r.g, s, p, r.c.binding(), newOperation(time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.journal.Families) != 0 {
		t.Errorf("an unchanged report was built again: %+v", out.journal.Families)
	}
	if w := p.Families[0].Writes; w == nil || len(w.Reports) != 1 || !w.Reports[0].Unchanged {
		t.Errorf("the plan's writes are %+v", w)
	}
}
