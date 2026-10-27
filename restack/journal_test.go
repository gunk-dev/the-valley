package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/rebase"
)

var (
	idB = strings.Repeat("m", 32)
	idC = strings.Repeat("n", 32)
	idE = strings.Repeat("p", 32)
)

// readyAs pushes a branch off from with one commit, and readies it as the
// change id, owned by owner, under a title unlike the commit's subject. It
// returns the head.
func (f *fixture) readyAs(id, owner, branch, from, title string) string {
	f.t.Helper()
	f.git("checkout", "--quiet", "-B", branch, from)
	head := f.commit("layer "+branch, branch+".txt")
	f.git("push", "--quiet", "--force", "origin", branch)
	r := &change.Record{
		Schema: change.Schema, ChangeID: id, Target: rebase.Target, Owner: owner, State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: title, Branch: branch, Uploader: owner}},
	}
	f.writeRecord(id, r, "", head)
	return head
}

func (f *fixture) writeRecord(id string, r *change.Record, old string, parents ...string) {
	f.t.Helper()
	ctx := context.Background()
	g := f.as(r.Owner)
	commit, err := rebase.WriteRecord(ctx, g, r, "test record", parents...)
	if err != nil {
		f.t.Fatal(err)
	}
	if w := rebase.PushRecord(ctx, g, id, old, commit); w.Push != rebase.Pushed {
		f.t.Fatalf("the record of %s was not pushed: %+v", id, w)
	}
}

// setState writes the change's record again in another state.
func (f *fixture) setState(id, state string) {
	f.t.Helper()
	ref := change.Ref(rebase.Target, id)
	old := f.at(ref)
	data := f.run(f.origin, "cat-file", "blob", ref+":record.json")
	r, err := change.Parse([]byte(data + "\n"))
	if err != nil {
		f.t.Fatal(err)
	}
	r.State, r.Seq = state, r.Seq+1
	f.git("fetch", "--quiet", "origin", ref)
	f.writeRecord(id, r, old, old)
}

// plainBranch pushes a branch off from that no record is on.
func (f *fixture) plainBranch(branch, from string) {
	f.t.Helper()
	f.git("checkout", "--quiet", "-B", branch, from)
	f.commit("plain "+branch, branch+".txt")
	f.git("push", "--quiet", "origin", branch)
}

// ownerPushes adds a commit of the owner's own on origin's branch.
func (f *fixture) ownerPushes(branch string) {
	f.t.Helper()
	f.git("fetch", "--quiet", "origin")
	f.git("checkout", "--quiet", "-B", branch, "origin/"+branch)
	f.commit("the owner's own work on "+branch, branch+"-own.txt")
	f.git("push", "--quiet", "origin", branch)
}

// refs is every ref on origin, to tell that nothing moved.
func (f *fixture) refs() string {
	f.t.Helper()
	return f.run(f.origin, "for-each-ref", "--format=%(objectname) %(refname)")
}

// onOrigin runs git in origin itself, as a server-side change is made.
func (f *fixture) onOrigin(args ...string) string {
	f.t.Helper()
	return f.run(f.origin, args...)
}

// restacker is the restacker's side of a fixture: a configuration and the
// scratch repository a pass reads origin into.
type restacker struct {
	t *testing.T
	f *fixture
	c config
	g rebase.Git
}

func newRestacker(t *testing.T, f *fixture) *restacker {
	t.Helper()
	c := config{repo: f.origin, target: rebase.Target, state: t.TempDir(), principal: "restacker",
		commitName: "The Restacker", commitEmail: "restacker@example.invalid"}
	g, err := scratchRepo(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return &restacker{t: t, f: f, c: c, g: g}
}

// plan reads origin and plans it under a cap of 64, deciding the journal
// in the state directory if there is one.
func (r *restacker) plan() (*Plan, *rebase.Snapshot) {
	r.t.Helper()
	ctx := context.Background()
	s, err := rebase.ReadSnapshot(ctx, r.g, r.c.verify)
	if err != nil {
		r.t.Fatal(err)
	}
	p, err := plan(ctx, r.g, r.c, s, 64)
	if err != nil {
		r.t.Fatal(err)
	}
	return p, s
}

// journal plans origin, builds the pass's writes and keeps them, as a pass
// does before its first push.
func (r *restacker) journal() *Journal {
	r.t.Helper()
	ctx := context.Background()
	p, s := r.plan()
	out, err := build(ctx, r.g, s, p, r.c.binding(), newOperation(time.Now()))
	if err != nil {
		r.t.Fatal(err)
	}
	defer out.releasePrepared(ctx, r.g)
	if err := keep(ctx, r.g, r.c.state, out.journal); err != nil {
		r.t.Fatal(err)
	}
	return out.journal
}

// recovery is what recovery decides of the journal now.
func (r *restacker) recovery() *Recovery {
	r.t.Helper()
	p, _ := r.plan()
	if p.Recovery == nil {
		r.t.Fatal("no journal was decided")
	}
	if p.Recovery.Error != "" {
		r.t.Fatalf("the journal was not read: %s", p.Recovery.Error)
	}
	return p.Recovery
}

// of is the family of the journal whose bottom layer is the change id.
func of(t *testing.T, j *Journal, id string) JournalFamily {
	t.Helper()
	for _, f := range j.Families {
		if f.Observed.Layers[0].ID == id {
			return f
		}
	}
	t.Fatalf("the journal has no family on %s", id)
	return JournalFamily{}
}

// decision is what recovery decided of one write.
func decision(t *testing.T, r *Recovery, write, id string) Decision {
	t.Helper()
	for _, d := range r.Decisions {
		if d.Write == write && d.ID == id {
			return d
		}
	}
	t.Fatalf("recovery decided nothing of %s %s: %+v", write, id, r.Decisions)
	return Decision{}
}

// expect holds a decision to its action, its finality, and a word of its
// reason.
func expect(t *testing.T, d Decision, action string, final bool, why string) {
	t.Helper()
	if d.Action != action || d.Final != final || !strings.Contains(d.Why, why) {
		t.Errorf("%s %s: %s (final %v): %s; want %s (final %v) saying %q", d.Write, d.ID, d.Action, d.Final, d.Why, action, final, why)
	}
}

func branchesOf(t *testing.T, r *Recovery, family JournalFamily, j *Journal) Decision {
	t.Helper()
	for i, f := range j.Families {
		if reflect.DeepEqual(f.Observed, family.Observed) {
			for _, d := range r.Decisions {
				if d.Family == i && d.Write == "branches" {
					return d
				}
			}
		}
	}
	t.Fatal("recovery decided nothing of the family's branches")
	return Decision{}
}

// Every write a pass would make is built and kept before any push: the
// journal holds each family's branch push, each version, lower layers
// first, with its active title and leased on the record read, and each
// recorded change's report. Origin is untouched, the journal reads back
// as kept, and every object a write would push is kept reachable under
// the operation's refs. A family a plain branch holds gets a blocked
// report and no other write.
func TestTheJournalKeepsEveryWriteBeforeAnyPush(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "main", "B's title")
	f.readyAs(idC, "bob", "c", "b", "C's title")
	f.readyAs(idE, "alice", "e", "main", "E's title")
	f.plainBranch("e-plain", "e")
	f.moveMain()
	before := f.refs()
	r := newRestacker(t, f)
	j := r.journal()
	if after := f.refs(); after != before {
		t.Fatalf("building the writes moved something on origin:\n%s\nnow:\n%s", before, after)
	}

	kept, err := loadJournal(r.c.state, r.c.binding())
	if err != nil || !reflect.DeepEqual(kept, j) {
		t.Fatalf("the journal reads back as %+v, %v", kept, err)
	}
	a := of(t, j, idA)
	if a.Branches == nil || len(a.Branches.Updates) != 1 || a.Branches.Updates[0].Old != f.at("refs/heads/a") || a.Branches.Tip != f.at("refs/heads/main") {
		t.Errorf("a's branch push is %+v", a.Branches)
	}
	if len(a.Versions) != 1 || a.Versions[0].Old != f.at(change.Ref(rebase.Target, idA)) || !a.Versions[0].Restack() ||
		a.Versions[0].Record.ActiveVersion().Title != "A's title" || a.Versions[0].Head != a.Branches.Updates[0].New {
		t.Errorf("a's version is %+v", a.Versions)
	}
	if len(a.Reports) != 1 || a.Reports[0].Pending.Report.Result != change.RestackComplete || a.Reports[0].Pending.Old != "" {
		t.Errorf("a's report is %+v", a.Reports)
	}
	bc := of(t, j, idB)
	if len(bc.Branches.Updates) != 2 || len(bc.Versions) != 2 || bc.Versions[0].ID != idB || bc.Versions[1].ID != idC || len(bc.Reports) != 2 {
		t.Errorf("b and c's writes are %+v", bc)
	}
	for _, rep := range bc.Reports {
		if l := rep.Pending.Report.Layers; len(l) != 2 || l[1].Published == nil || *l[1].Published.Record != change.HeadPrefix+bc.Versions[1].Commit {
			t.Errorf("%s's report does not say what the family published: %+v", rep.Pending.ID, l)
		}
	}
	e := of(t, j, idE)
	if e.Branches != nil || len(e.Versions) != 0 || len(e.Reports) != 1 || e.Reports[0].Pending.Report.Result != change.RestackBlocked ||
		!strings.Contains(*e.Reports[0].Pending.Report.Refusal, "e-plain is a plain branch") {
		t.Errorf("e's writes are %+v", e)
	}

	var want []string
	for ref, oid := range j.retained() {
		if !strings.HasPrefix(ref, retainPrefix+j.Families[0].Operation+"/") {
			t.Errorf("%s is not kept under the operation's refs", ref)
		}
		want = append(want, ref+" "+oid)
	}
	got := strings.Split(gitOutT(t, r.g, "for-each-ref", "--format=%(refname) %(objectname)", retainPrefix), "\n")
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the scratch repository keeps %v, and the journal's writes push %v", got, want)
	}
	if left := gitOutT(t, r.g, "for-each-ref", "refs/valley-rebase/"); left != "" {
		t.Errorf("the prepared heads' own refs were not let go:\n%s", left)
	}
}

func gitOutT(t *testing.T, g rebase.Git, args ...string) string {
	t.Helper()
	out, err := gitOut(context.Background(), g, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// Recovery follows each write as the pass would make it. Before any push,
// every write is still valid to finish. Once a family's branches are
// pushed, they are done, and its versions are still to finish; once a
// version and a report are pushed, they are done too. An author who
// pushes past a prepared head stops that change's version for good, and
// with it each report that says the version was added, while the version
// below it is still finished.
func TestRecoveryFollowsEachWrite(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "main", "B's title")
	f.readyAs(idC, "bob", "c", "b", "C's title")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	a, bc := of(t, j, idA), of(t, j, idB)
	ctx := context.Background()

	rec := r.recovery()
	for _, d := range rec.Decisions {
		if d.Action != writeFinish {
			t.Errorf("before any push, %s %s is %s: %s", d.Write, d.ID, d.Action, d.Why)
		}
	}
	if rec.Settled || len(rec.Decisions) != 8 {
		t.Errorf("recovery decided %d writes, settled %v", len(rec.Decisions), rec.Settled)
	}

	if push, _ := a.Branches.Push(ctx, r.g); push != rebase.Pushed {
		t.Fatal("a's branches were not pushed")
	}
	rec = r.recovery()
	expect(t, branchesOf(t, rec, a, j), writeDone, false, "origin took the branch push")
	expect(t, decision(t, rec, "version", idA), writeFinish, false, "at the prepared head")
	expect(t, decision(t, rec, "report", idA), writeFinish, false, "once the writes it reports are pushed")
	if ap := a.Versions[0].Push(ctx, r.g); ap.Err != nil {
		t.Fatal(ap.Err)
	}
	if w := a.Reports[0].Pending.Push(ctx, r.g); w.Push != rebase.Pushed {
		t.Fatalf("a's report: %+v", w)
	}
	rec = r.recovery()
	expect(t, decision(t, rec, "version", idA), writeDone, false, "holds v2")
	expect(t, decision(t, rec, "report", idA), writeDone, false, "origin holds the report")

	if push, _ := bc.Branches.Push(ctx, r.g); push != rebase.Pushed {
		t.Fatal("b and c's branches were not pushed")
	}
	f.ownerPushes("c")
	rec = r.recovery()
	expect(t, branchesOf(t, rec, bc, j), writeDone, false, "or past them")
	expect(t, decision(t, rec, "version", idB), writeFinish, false, "at the prepared head")
	expect(t, decision(t, rec, "version", idC), writeStop, true, "its author moved it")
	expect(t, decision(t, rec, "report", idB), writeStop, true, "did not end as this report says")
	expect(t, decision(t, rec, "report", idC), writeStop, true, "did not end as this report says")
	if rec.Settled {
		t.Error("recovery is settled with b's version still to finish")
	}
}

// A version stops for good on a withdrawal, an unready, a record moved
// on by its author, and a deleted record, which is a terminal failure
// that keeps its diagnostics: the report it cannot push is described. A
// family whose writes stopped does not stop another's.
func TestRecoveryStopsForGood(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "main", "B's title")
	f.readyAs(idC, "bob", "c", "b", "C's title")
	f.readyAs(idE, "alice", "e", "main", "E's title")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	ctx := context.Background()
	bc := of(t, j, idB)
	if push, _ := bc.Branches.Push(ctx, r.g); push != rebase.Pushed {
		t.Fatal("b and c's branches were not pushed")
	}
	f.setState(idB, change.Draft)
	f.onOrigin("update-ref", "-d", change.Ref(rebase.Target, idC))
	f.setState(idE, change.Withdrawn)

	rec := r.recovery()
	expect(t, decision(t, rec, "version", idB), writeStop, true, "is draft, not ready")
	c := decision(t, rec, "version", idC)
	expect(t, c, writeStop, true, "no longer holds a record")
	cr := decision(t, rec, "report", idC)
	expect(t, cr, writeStop, true, "the hook takes no report about a change it does not record")
	if !c.Terminal || !cr.Terminal || !strings.Contains(cr.Why, "complete, on main at") {
		t.Errorf("a deleted record is not a terminal failure with its diagnostics: %+v %+v", c, cr)
	}
	expect(t, branchesOf(t, rec, of(t, j, idE), j), writeStop, true, "was withdrawn")
	expect(t, decision(t, rec, "version", idE), writeStop, true, "was withdrawn")
	expect(t, decision(t, rec, "report", idE), writeStop, true, "branches were not pushed")
	for _, d := range rec.Decisions {
		if d.ID == idA && d.Action != writeFinish {
			t.Errorf("an unrelated family's %s is %s: %s", d.Write, d.Action, d.Why)
		}
	}
	expect(t, branchesOf(t, rec, of(t, j, idA), j), writeFinish, false, "has not moved")
}

// A branch push not yet made is finished only while main is where the
// family was rebased onto: once main moves, it stops, and the next pass
// prepares the family again. Once the branches are pushed, main moving on
// does not stop the versions. A main that no longer descends from the tip
// stops every write left.
func TestRecoveryAndTheTarget(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "main", "B's title")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	ctx := context.Background()
	a := of(t, j, idA)
	if push, _ := a.Branches.Push(ctx, r.g); push != rebase.Pushed {
		t.Fatal("a's branches were not pushed")
	}
	tip := f.at("refs/heads/main")
	f.git("checkout", "--quiet", "main")
	f.commit("main moves on again", "again.txt")
	f.git("push", "--quiet", "origin", "main")

	rec := r.recovery()
	expect(t, decision(t, rec, "version", idA), writeFinish, false, "at the prepared head")
	expect(t, branchesOf(t, rec, of(t, j, idB), j), writeStop, true, "before the branches were pushed")
	expect(t, decision(t, rec, "version", idB), writeStop, true, "its branch push stopped")

	f.onOrigin("update-ref", "refs/heads/main", tip+"~1")
	rec = r.recovery()
	expect(t, decision(t, rec, "version", idA), writeStop, true, "no longer descends")
	expect(t, decision(t, rec, "report", idA), writeStop, true, "did not end as this report says")
}

// A related request that might still land holds the branch push, and
// pauses the versions once the branches are pushed. Neither stop is
// final. A request is related by any version head, the prepared heads
// included, whatever its name.
func TestARequestHoldsRecovery(t *testing.T) {
	f := newFixture(t, "")
	was := f.readyAs(idA, "alice", "a", "main", "A's title")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	a := of(t, j, idA)
	request := rebase.RequestPrefix + "zz-any-name"

	f.onOrigin("update-ref", request, was)
	rec := r.recovery()
	expect(t, branchesOf(t, rec, a, j), writeStop, false, "request zz-any-name might still land")
	f.onOrigin("update-ref", "-d", request)
	if push, _ := a.Branches.Push(context.Background(), r.g); push != rebase.Pushed {
		t.Fatal("a's branches were not pushed")
	}
	f.onOrigin("update-ref", request, a.Versions[0].Head)
	rec = r.recovery()
	expect(t, decision(t, rec, "version", idA), writeStop, false, "paused: request zz-any-name might still land")
	f.onOrigin("update-ref", "-d", request)
	rec = r.recovery()
	expect(t, decision(t, rec, "version", idA), writeFinish, false, "at the prepared head")
}

// Nothing is inferred from a journal that is lost, another's, or not in
// its form. A lost journal leaves the change whose branch the pass moved
// held by the planner, its branch not at its recorded head. A journal
// another principal wrote, or one with a field it does not have, is
// refused, and nothing in it is decided.
func TestNothingIsInferredFromALostOrForeignJournal(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	path := filepath.Join(r.c.state, journalFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for what, form := range map[string]string{
		"another principal's": strings.Replace(string(data), `"principal": "restacker"`, `"principal": "requester"`, 1),
		"an unknown field":    strings.Replace(string(data), `"schema"`, `"pushed": true, "schema"`, 1),
		"a field twice":       strings.Replace(string(data), `"target": "main",`, `"target": "main", "target": "main",`, 1),
		"another version":     strings.Replace(string(data), journalSchema, "valley-restack-journal/v2", 1),
		"cut short":           string(data[:len(data)/2]),
	} {
		if form == string(data) {
			t.Fatalf("%s: the edit changed nothing", what)
		}
		if err := os.WriteFile(path, []byte(form), 0o600); err != nil {
			t.Fatal(err)
		}
		p, _ := r.plan()
		if p.Recovery == nil || p.Recovery.Error == "" || len(p.Recovery.Decisions) != 0 {
			t.Errorf("%s journal was decided: %+v", what, p.Recovery)
		}
	}

	if push, _ := of(t, j, idA).Branches.Push(context.Background(), r.g); push != rebase.Pushed {
		t.Fatal("a's branches were not pushed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	p, _ := r.plan()
	if p.Recovery != nil {
		t.Errorf("a lost journal was decided: %+v", p.Recovery)
	}
	if len(p.Families) != 1 || p.Families[0].Action != hold || !strings.Contains(strings.Join(p.Families[0].Reasons, "\n"), "records the head") {
		t.Errorf("the change whose branch moved with no journal is planned as %+v", p.Families)
	}
}

// --report-only decides a journal holding a branch push, versions and
// reports still to make, and pushes none of them: no ref on origin moves,
// and the journal is left as it was. The decisions are in the plan.
func TestReportOnlyRecoveryPushesNothing(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "main", "B's title")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	if push, _ := of(t, j, idB).Branches.Push(context.Background(), r.g); push != rebase.Pushed {
		t.Fatal("b's branches were not pushed")
	}
	path := filepath.Join(r.c.state, journalFile)
	journal, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := f.refs()

	var out, errOut bytes.Buffer
	if err := once(context.Background(), reportOnly(t, r.c, "--json"), &out, &errOut); err != nil {
		t.Fatalf("once: %v\n%s", err, errOut.String())
	}
	if after := f.refs(); after != before {
		t.Errorf("a --report-only pass moved something on origin:\n%s\nnow:\n%s", before, after)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, journal) {
		t.Error("a --report-only pass changed the journal")
	}
	var p Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Recovery == nil || len(p.Recovery.Decisions) == 0 || p.Recovery.Decisions[0].Operation != j.Families[0].Operation {
		t.Fatalf("the plan holds no recovery of the journal: %s", out.String())
	}
	finish := map[string]int{}
	for _, d := range p.Recovery.Decisions {
		if d.Action == writeFinish {
			finish[d.Write]++
		}
	}
	if finish["branches"] != 1 || finish["version"] != 2 || finish["report"] != 2 {
		t.Errorf("recovery would finish %v, and pushed none", finish)
	}
}

// reportOnly is the arguments of a --report-only pass with c's
// configuration and a stand-in for cue (fakePolicy).
func reportOnly(t *testing.T, c config, more ...string) []string {
	t.Helper()
	schema, floor := fakePolicy(t)
	return append([]string{"--report-only", "--repo", c.repo, "--state", c.state, "--principal", c.principal,
		"--commit-name", c.commitName, "--commit-email", c.commitEmail, "--schema", schema, "--instance", floor}, more...)
}

// fakePolicy puts a stand-in for cue on the path, which composes every
// policy to a cap of 64, and returns a schema and a floor for it. It keeps
// the process's environment as it was, since a pass controls its own.
func fakePolicy(t *testing.T) (schema, floor string) {
	t.Helper()
	return fakePolicyCap(t, 64)
}

// fakePolicyCap is fakePolicy with the cap given.
func fakePolicyCap(t *testing.T, cap int) (schema, floor string) {
	t.Helper()
	keepEnvironment(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	cue := fmt.Sprintf("#!/bin/sh\necho '{\"checks\": {}, \"restack\": {\"maxAutomaticVersions\": %d}}'\n", cap)
	if err := os.WriteFile(filepath.Join(bin, "cue"), []byte(cue), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	floor = filepath.Join(dir, "floor")
	schema = filepath.Join(dir, "schema.cue")
	if err := os.MkdirAll(floor, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{schema, filepath.Join(floor, "floor.cue")} {
		if err := os.WriteFile(path, []byte("package verification\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return schema, floor
}

// keepEnvironment puts the process's environment back as it was once the
// test ends.
func keepEnvironment(t *testing.T) {
	t.Helper()
	was := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range was {
			name, value, _ := strings.Cut(kv, "=")
			os.Setenv(name, value)
		}
	})
}

// A journal in its form whose writes do not agree with each other is
// refused, and nothing in it is decided: a version that is null, a family
// that would restack with one change's version left out and its reports
// kept, a report that says a version was published other than as the
// family adds it, and a report whose result is not the family's. Planning
// with any of them is an error of recovery, never a crash.
func TestAJournalWhoseWritesDisagreeIsRefused(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idB, "bob", "b", "main", "B's title")
	f.readyAs(idC, "bob", "c", "b", "C's title")
	f.readyAs(idE, "alice", "e", "main", "E's title")
	f.plainBranch("e-plain", "e")
	f.moveMain()
	r := newRestacker(t, f)
	r.journal()
	path := filepath.Join(r.c.state, journalFile)
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	family := func(j *Journal, id string) *JournalFamily {
		for i := range j.Families {
			if j.Families[i].Observed.Layers[0].ID == id {
				return &j.Families[i]
			}
		}
		t.Fatalf("the journal has no family on %s", id)
		return nil
	}
	other := change.HeadPrefix + strings.Repeat("0", 40)

	for _, c := range []struct {
		what, refused string
		edit          func(j *Journal)
	}{
		{"a null version", "holds an empty version", func(j *Journal) { family(j, idB).Versions[1] = nil }},
		{"a change's version left out", "adds a version to 1 of them", func(j *Journal) {
			bc := family(j, idB)
			bc.Versions = bc.Versions[:1]
		}},
		{"a report naming another record", "says " + idC + " was published as other than v2", func(j *Journal) {
			family(j, idB).Reports[0].Pending.Report.Layers[1].Published.Record = &other
		}},
		{"a report of another result", "says blocked, and the family's result is conflict", func(j *Journal) {
			family(j, idE).Observed.Result = change.RestackConflict
		}},
	} {
		var j Journal
		if err := strictly(kept, &j); err != nil {
			t.Fatal(err)
		}
		c.edit(&j)
		data, err := json.MarshalIndent(&j, "", "  ")
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		p, _ := r.plan()
		if p.Recovery == nil || !strings.Contains(p.Recovery.Error, c.refused) || len(p.Recovery.Decisions) != 0 {
			t.Errorf("a journal with %s was decided: %+v", c.what, p.Recovery)
		}
	}
}

// A journal that would not read back as written is not kept: nothing is
// kept for it in the scratch repository, and the journal before it stays.
// JSON holds only UTF-8, so a reason in Latin-1 is such a journal.
func TestAJournalThatWouldNotReadBackIsNotKept(t *testing.T) {
	f := newFixture(t, "")
	f.readyAs(idE, "alice", "e", "main", "E's title")
	f.plainBranch("e-plain", "e")
	f.moveMain()
	r := newRestacker(t, f)
	j := r.journal()
	path := filepath.Join(r.c.state, journalFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := letGo(ctx, r.g, nil); err != nil {
		t.Fatal(err)
	}
	j.Families[0].Observed.Reasons = append(j.Families[0].Observed.Reasons, "a reason in Latin-1: caf\xe9")
	if err := keep(ctx, r.g, r.c.state, j); err == nil || !strings.Contains(err.Error(), "would not read back") {
		t.Fatalf("a journal that would not read back was kept: %v", err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, before) {
		t.Error("the journal before it was replaced")
	}
	if left := gitOutT(t, r.g, "for-each-ref", retainPrefix); left != "" {
		t.Errorf("objects were kept for a journal that was not:\n%s", left)
	}
}
