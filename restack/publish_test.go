package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/rebase"
)

// hooked is a fixture whose origin runs the fixture host's rendered hook,
// which grants restack to the principal restacker and request to
// requester. Without the hook, the test is skipped.
func hooked(t *testing.T) *fixture {
	t.Helper()
	hook := os.Getenv(hookEnv)
	if hook == "" {
		t.Skip(hookEnv + " names no rendered hook; restack-unit supplies one")
	}
	withoutIdentity(t)
	return newFixture(t, hook)
}

// pass runs one write pass, as `valley-restacker once`, under a cue that
// composes every policy to the cap given, pushing as the restacker's
// principal. It returns what the pass printed with --json.
func (r *restacker) pass(cap int) *Plan {
	r.t.Helper()
	schema, floor := fakePolicyCap(r.t, cap)
	r.t.Setenv("VALLEY_PRINCIPAL", r.c.principal)
	var out, errOut bytes.Buffer
	if err := once(context.Background(), append(writeArgs(r.c, schema, floor), "--json"), &out, &errOut); err != nil {
		r.t.Fatalf("once: %v\n%s", err, errOut.String())
	}
	var p Plan
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		r.t.Fatalf("the pass does not read: %v\n%s", err, out.String())
	}
	return &p
}

// writeArgs is the arguments of a write pass with c's configuration.
func writeArgs(c config, schema, floor string) []string {
	return []string{"--repo", c.repo, "--state", c.state, "--principal", c.principal,
		"--commit-name", c.commitName, "--commit-email", c.commitEmail, "--schema", schema, "--instance", floor}
}

// pushAs pushes to origin as a principal, through the hook.
func (f *fixture) pushAs(principal string, args ...string) error {
	g := f.as(principal)
	cmd := exec.Command("git", append([]string{"push", "--quiet", "origin"}, args...)...)
	cmd.Dir, cmd.Env = g.Dir, g.Env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

// judge writes an outcome record on origin's side, as the integrator does:
// no push may write one.
func (f *fixture) judge(name, head, decision string) {
	f.t.Helper()
	line := fmt.Sprintf("%s %s %s %s\n", head, f.at("refs/heads/main"), change.EvidenceFingerprint(nil), decision)
	path := filepath.Join(f.t.TempDir(), "outcome")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		f.t.Fatal(err)
	}
	blob := f.onOrigin("hash-object", "-w", path)
	f.onOrigin("update-ref", rebase.OutcomePrefix+name, blob)
}

// recordOf is the change's record on origin.
func (f *fixture) recordOf(id string) *change.Record {
	f.t.Helper()
	data := f.onOrigin("cat-file", "blob", change.Ref(rebase.Target, id)+":record.json")
	r, err := change.Parse([]byte(data + "\n"))
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

// reportAt is the report a report commit on origin holds.
func (f *fixture) reportAt(commit string) *change.RestackReport {
	f.t.Helper()
	data := f.onOrigin("cat-file", "blob", commit+":report.json")
	r, err := change.ParseRestackReport([]byte(data + "\n"))
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

// reportOf is the change's latest report on origin, or nil for none.
func (f *fixture) reportOf(id string) *change.RestackReport {
	f.t.Helper()
	commit := f.at(rebase.RestackPrefix + id)
	if commit == "" {
		return nil
	}
	return f.reportAt(commit)
}

// restackedTo holds that the change's record gained exactly one version,
// v2, uploaded by the restacker at head, keeping the owner, the title and
// the branch, and that its latest report says complete with that version.
func (f *fixture) restackedTo(id, owner, title, branch, head string) {
	f.t.Helper()
	r := f.recordOf(id)
	v := r.ActiveVersion()
	if r.State != change.Ready || r.Seq != 2 || len(r.Versions) != 2 || v.N != 2 || rebase.HeadOf(v) != head ||
		v.Uploader != "restacker" || v.Title != title || v.Branch != branch || r.Owner != owner {
		f.t.Errorf("%s's record is not restacked to %s once: %+v", id[:4], short(head), r)
	}
	rep := f.reportOf(id)
	if rep == nil || rep.Result != change.RestackComplete {
		f.t.Fatalf("%s's report is %+v", id[:4], rep)
	}
	for _, l := range rep.Layers {
		if l.ChangeID == id && (l.Published == nil || l.Published.Version == nil || *l.Published.Version != 2 ||
			l.Published.Head != change.HeadPrefix+head) {
			f.t.Errorf("%s's report does not say v2 was published at %s: %+v", id[:4], short(head), l.Published)
		}
	}
}

// settledState holds that the pass left no journal and kept no object for
// one.
func (r *restacker) settledState() {
	r.t.Helper()
	if _, err := os.Stat(filepath.Join(r.c.state, journalFile)); err == nil {
		r.t.Error("the pass left a journal")
	}
	if kept := gitOutT(r.t, r.g, "for-each-ref", retainPrefix, "refs/valley-rebase/"); kept != "" {
		r.t.Errorf("the pass kept objects:\n%s", kept)
	}
}

// count is how many of the pass's actions are of the write given.
func count(p *Plan, write string) int {
	n := 0
	for _, a := range p.Actions {
		if a.Write == write {
			n++
		}
	}
	return n
}

// After a landing, one write pass rebases every ready change onto main
// through the rendered hook, as the restacker's principal: a stack of two
// owners' changes is one family, and each layer descends from the new
// layer below it, with only its own file changed. Each change gains one
// version, uploaded by the restacker, under its owner and title, and a
// report that says complete. A change that conflicts with the landing
// moves nothing and gets a report that says conflict, naming the path,
// and a draft is left alone. The pass lets go of its journal. A second
// pass pushes nothing: the restacked stack is current, and the conflict
// is not tried again while nothing it observed has moved.
func TestAPassRestacksReadyChangesAfterALanding(t *testing.T) {
	f := hooked(t)
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "a", "B's title")
	f.git("checkout", "--quiet", "-B", "c", "main")
	conflicted := f.commit("c's line", "shared.txt")
	f.git("push", "--quiet", "origin", "c")
	f.writeRecord(idC, &change.Record{
		Schema: change.Schema, ChangeID: idC, Target: rebase.Target, Owner: "carol", State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + conflicted, Title: "C's title", Branch: "c", Uploader: "carol"}},
	}, "", conflicted)
	draft := f.readyAs(idE, "alice", "e", "main", "E's title")
	f.setState(idE, change.Draft)
	f.git("checkout", "--quiet", "main")
	f.commit("main's line", "shared.txt")
	f.git("push", "--quiet", "origin", "main")
	tip := f.at("refs/heads/main")
	cRecord := f.at(change.Ref(rebase.Target, idC))

	r := newRestacker(t, f)
	p := r.pass(64)
	a, b := f.at("refs/heads/a"), f.at("refs/heads/b")
	if parent := f.onOrigin("rev-parse", a+"^"); parent != tip {
		t.Errorf("a is on %s, not main's tip %s", short(parent), short(tip))
	}
	if parent := f.onOrigin("rev-parse", b+"^"); parent != a {
		t.Errorf("b is on %s, not a's new head %s", short(parent), short(a))
	}
	for _, d := range [][3]string{{tip, a, "a.txt"}, {a, b, "b.txt"}} {
		if changed := f.onOrigin("diff", "--name-only", d[0], d[1]); changed != d[2] {
			t.Errorf("%s changes %q, not %s alone", short(d[1]), changed, d[2])
		}
	}
	f.restackedTo(idA, "alice", "A's title", "a", a)
	f.restackedTo(idB, "bob", "B's title", "b", b)

	if f.at("refs/heads/c") != conflicted || f.at(change.Ref(rebase.Target, idC)) != cRecord {
		t.Error("the conflicting change moved")
	}
	if rep := f.reportOf(idC); rep == nil || rep.Result != change.RestackConflict || rep.Failing == nil || *rep.Failing != idC ||
		len(rep.Conflicts) != 1 || rep.Conflicts[0] != "shared.txt" {
		t.Errorf("c's report is %+v", rep)
	}
	if f.at("refs/heads/e") != draft || f.recordOf(idE).Seq != 2 || f.reportOf(idE) != nil {
		t.Error("the draft was touched")
	}
	if count(p, "branches") != 1 || count(p, "version") != 2 || count(p, "report") != 3 {
		t.Errorf("the pass pushed %+v", p.Actions)
	}
	r.settledState()

	before := f.refs()
	p = r.pass(64)
	if len(p.Actions) != 0 || f.refs() != before {
		t.Errorf("a second pass pushed %+v", p.Actions)
	}
	for _, fp := range p.Families {
		if fp.Layers[0].ID == idC && fp.Writes != nil && len(fp.Writes.Reports) == 1 && !fp.Writes.Reports[0].Unchanged {
			t.Errorf("the second pass rebased the conflict again: %+v", fp.Writes)
		}
	}
	r.settledState()
}

// A related request that might still land holds its family: the pass
// moves no branch and adds no version, and its report says blocked,
// naming the request. Once the integrator's outcome for the request is a
// current stale, the next pass restacks the change and leaves the
// request as it was.
func TestARequestHoldsItsFamilyUntilACurrentStaleReleasesIt(t *testing.T) {
	f := hooked(t)
	head := f.readyAs(idA, "alice", "a", "main", "A's title")
	request := rebase.RequestPrefix + change.RequestName(idA)
	if err := f.pushAs("requester", head+":"+request); err != nil {
		t.Fatal(err)
	}
	f.moveMain()
	record := f.at(change.Ref(rebase.Target, idA))

	r := newRestacker(t, f)
	r.pass(64)
	if f.at("refs/heads/a") != head || f.at(change.Ref(rebase.Target, idA)) != record {
		t.Fatal("the pass moved a change a request holds")
	}
	rep := f.reportOf(idA)
	if rep == nil || rep.Result != change.RestackBlocked || len(rep.Requests) != 1 || rep.Requests[0].Ref != request ||
		rep.Refusal == nil || !strings.Contains(*rep.Refusal, "might still land") {
		t.Fatalf("a's report is %+v", rep)
	}

	f.judge(change.RequestName(idA), head, "stale")
	r.pass(64)
	f.restackedTo(idA, "alice", "A's title", "a", f.at("refs/heads/a"))
	if f.at(request) != head {
		t.Error("the restacker moved the request")
	}
	r.settledState()
}

// A pass that ended after any step of its journal is finished by the
// next: each write already on origin is not pushed again, each change has
// exactly one new version, every report says complete, and the journal
// is let go.
func TestAPassFinishesAJournalLeftAtAnyStep(t *testing.T) {
	steps := []string{"kept", "branches pushed", "lower version added", "both versions added", "one report pushed"}
	for n, step := range steps {
		t.Run(step, func(t *testing.T) {
			f := hooked(t)
			f.readyAs(idA, "alice", "a", "main", "A's title")
			f.readyAs(idB, "bob", "b", "a", "B's title")
			f.moveMain()
			t.Setenv("VALLEY_PRINCIPAL", "restacker")
			r := newRestacker(t, f)
			fam := of(t, r.journal(), idA)
			ctx := context.Background()
			made := []func() bool{
				func() bool { push, _ := fam.Branches.Push(ctx, r.g); return push == rebase.Pushed },
				func() bool { return fam.Versions[0].Push(ctx, r.g).Err == nil },
				func() bool { return fam.Versions[1].Push(ctx, r.g).Err == nil },
				func() bool { return fam.Reports[0].Pending.Push(ctx, r.g).Push == rebase.Pushed },
			}
			for i := 0; i < n; i++ {
				if !made[i]() {
					t.Fatalf("step %d of the journal was not pushed", i+1)
				}
			}

			p := r.pass(64)
			f.restackedTo(idA, "alice", "A's title", "a", fam.Branches.Updates[0].New)
			f.restackedTo(idB, "bob", "B's title", "b", fam.Branches.Updates[1].New)
			if want := min(n, 1); count(p, "branches") != 1-want {
				t.Errorf("the pass pushed the branches %d times, after %s", count(p, "branches"), step)
			}
			if want := 2 - min(max(n-1, 0), 2); count(p, "version") != want {
				t.Errorf("the pass added %d versions, after %s, where %d were left", count(p, "version"), step, want)
			}
			if p.Recovery == nil || p.Pending != nil {
				t.Errorf("the journal was not found, or is still open: %+v", p.Pending)
			}
			r.settledState()
		})
	}
}

// An owner who pushes to a branch the restacker has moved stops that
// change's version for good: the pass adds the version below it, adds
// none to the moved change, and reports partial for both, with each
// version origin holds. The moved change's record stays as its owner left
// it, and the plan that follows in the same pass holds it, since its
// branch is not at its version's head.
func TestAnOwnerWhoMovesARestackedBranchStopsItsVersion(t *testing.T) {
	f := hooked(t)
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.readyAs(idB, "bob", "b", "a", "B's title")
	f.moveMain()
	t.Setenv("VALLEY_PRINCIPAL", "restacker")
	r := newRestacker(t, f)
	fam := of(t, r.journal(), idA)
	if push, _ := fam.Branches.Push(context.Background(), r.g); push != rebase.Pushed {
		t.Fatal("the branches were not pushed")
	}
	f.ownerPushes("b")
	bRecord := f.at(change.Ref(rebase.Target, idB))

	p := r.pass(64)
	for _, fp := range p.Families {
		if fp.Layers[0].ID == idB && fp.Action != hold {
			t.Errorf("once the journal is let go, the plan does not hold b, whose branch is not at its version's head: %+v", fp)
		}
	}
	if got := f.recordOf(idA); got.Seq != 2 || rebase.HeadOf(got.ActiveVersion()) != fam.Branches.Updates[0].New {
		t.Errorf("a's version was not added: %+v", got)
	}
	if f.at(change.Ref(rebase.Target, idB)) != bRecord {
		t.Error("a version was added to the change its owner moved")
	}
	// The latest report or one before it, since a later plan in the same
	// pass may report on b again, says partial.
	partial := false
	for commit := f.at(rebase.RestackPrefix + idB); commit != ""; commit = f.parentOf(commit) {
		rep := f.reportAt(commit)
		if rep.Result != change.RestackPartial {
			continue
		}
		partial = true
		for _, l := range rep.Layers {
			switch {
			case l.ChangeID == idA && (l.Published == nil || l.Published.Version == nil || *l.Published.Version != 2):
				t.Errorf("the partial report does not say a's v2 was added: %+v", l.Published)
			case l.ChangeID == idB && (l.Published == nil || l.Published.Version != nil):
				t.Errorf("the partial report says b's version was added: %+v", l.Published)
			}
		}
		if rep.Refusal == nil || !strings.Contains(*rep.Refusal, "its author moved it") {
			t.Errorf("the partial report does not say why: %v", rep.Refusal)
		}
	}
	if !partial {
		t.Error("no report of b says partial")
	}
	r.settledState()
}

// parentOf is a commit's first parent on origin, or "" for none.
func (f *fixture) parentOf(commit string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", commit+"^")
	cmd.Dir, cmd.Env = f.origin, f.env
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// A request filed between a family's branch push and its versions pauses
// the versions: the pass reports partial, adds none, and keeps the
// journal open, so it starts no new operation. Once the request's outcome
// is a current stale, the next pass adds the version, reports complete,
// and lets go of the journal.
func TestARequestBetweenPhasesPausesTheVersions(t *testing.T) {
	f := hooked(t)
	old := f.readyAs(idA, "alice", "a", "main", "A's title")
	f.moveMain()
	t.Setenv("VALLEY_PRINCIPAL", "restacker")
	r := newRestacker(t, f)
	fam := of(t, r.journal(), idA)
	if push, _ := fam.Branches.Push(context.Background(), r.g); push != rebase.Pushed {
		t.Fatal("the branches were not pushed")
	}
	if err := f.pushAs("requester", old+":"+rebase.RequestPrefix+"zz-old"); err != nil {
		t.Fatal(err)
	}
	record := f.at(change.Ref(rebase.Target, idA))

	p := r.pass(64)
	if f.at(change.Ref(rebase.Target, idA)) != record || p.Pending == nil {
		t.Fatalf("the paused version was added, or the journal let go: %+v", p.Pending)
	}
	if _, err := os.Stat(filepath.Join(r.c.state, journalFile)); err != nil {
		t.Errorf("the open journal is gone: %v", err)
	}
	rep := f.reportOf(idA)
	if rep == nil || rep.Result != change.RestackPartial || rep.Refusal == nil || !strings.Contains(*rep.Refusal, "zz-old might still land") {
		t.Fatalf("a's report is %+v", rep)
	}
	if p = r.pass(64); len(p.Actions) != 0 || p.Pending == nil {
		t.Errorf("a pass with the request still open pushed %+v", p.Actions)
	}

	f.judge("zz-old", old, "stale")
	p = r.pass(64)
	if p.Pending != nil {
		t.Errorf("the journal is still open: %+v", p.Pending)
	}
	f.restackedTo(idA, "alice", "A's title", "a", fam.Branches.Updates[0].New)
	r.settledState()
}

// A report of what did happen is kept in the journal before it is pushed,
// and stays there until a read of origin shows it. Here an owner moves a
// restacked branch, so the version above stops and the family's reports
// say partial. When origin refuses the push of one of them, the pass
// leaves the journal open with that report in it, and the next pass
// pushes it. When origin takes the push and its answer is lost, the next
// read finds it there, and it is not pushed again.
func TestAReportOfWhatHappenedOutlivesAFailedPush(t *testing.T) {
	for _, failure := range []string{"refused", "answer lost"} {
		t.Run(failure, func(t *testing.T) {
			f := hooked(t)
			f.readyAs(idA, "alice", "a", "main", "A's title")
			f.readyAs(idB, "bob", "b", "a", "B's title")
			f.moveMain()
			t.Setenv("VALLEY_PRINCIPAL", "restacker")
			r := newRestacker(t, f)
			fam := of(t, r.journal(), idA)
			if push, _ := fam.Branches.Push(context.Background(), r.g); push != rebase.Pushed {
				t.Fatal("the branches were not pushed")
			}
			f.ownerPushes("b")
			ref := rebase.RestackPrefix + idB
			lift := func() {}
			if failure == "refused" {
				lift = f.refuseOnOrigin(ref)
			} else {
				r.loseAnswers(ref)
			}

			p := r.pass(64)
			var pushes []string
			for _, a := range p.Actions {
				if a.Write == "replacement" && a.ID == idB {
					pushes = append(pushes, a.Result)
				}
			}
			if failure == "answer lost" {
				if strings.Join(pushes, " ") != "unknown" {
					t.Errorf("b's report of what happened was pushed as %v, where once, its answer lost, is expected", pushes)
				}
				if !partialOf(f, idB) {
					t.Error("no report of b on origin says partial")
				}
				r.settledState()
				return
			}
			if len(pushes) == 0 || strings.Trim(strings.Join(pushes, " "), "refused ") != "" {
				t.Errorf("b's report of what happened was pushed as %v", pushes)
			}
			if f.reportOf(idB) != nil {
				t.Fatal("origin holds a report of b it refused")
			}
			j, err := loadJournal(r.c.state, r.c.binding())
			if err != nil || j == nil || p.Pending == nil {
				t.Fatalf("the pass let go of the journal that holds b's report: %v", err)
			}
			kept := false
			for _, rep := range of(t, j, idA).Replacements {
				kept = kept || rep.Pending.ID == idB && rep.Pending.Report.Result == change.RestackPartial
			}
			if !kept {
				t.Fatalf("the journal does not hold b's report of what happened: %+v", of(t, j, idA).Replacements)
			}

			lift()
			r.pass(64)
			if !partialOf(f, idB) {
				t.Error("no report of b on origin says partial once origin takes it")
			}
			r.settledState()
		})
	}
}

// partialOf reports whether the change's latest report on origin, or one
// before it, says partial: a's version added, the change's own not, since
// its author moved it. A later plan in the same pass may report on the
// change again.
func partialOf(f *fixture, id string) bool {
	f.t.Helper()
	for commit := f.at(rebase.RestackPrefix + id); commit != ""; commit = f.parentOf(commit) {
		rep := f.reportAt(commit)
		if rep.Result != change.RestackPartial || rep.Refusal == nil || !strings.Contains(*rep.Refusal, "its author moved it") {
			continue
		}
		for _, l := range rep.Layers {
			if l.Published == nil || (l.ChangeID == idA) != (l.Published.Version != nil) {
				f.t.Errorf("the partial report of %s does not say what was published: %+v", id[:4], l)
			}
		}
		return true
	}
	return false
}

// refuseOnOrigin has origin refuse every push that moves the ref, ahead
// of its own hook, until the function it returns is called.
func (f *fixture) refuseOnOrigin(ref string) (lift func()) {
	f.t.Helper()
	hook := filepath.Join(f.origin, "hooks", "pre-receive")
	own := hook + ".own"
	if err := os.Rename(hook, own); err != nil {
		f.t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nlines=$(cat)\n"+
		"if printf '%%s\\n' \"$lines\" | grep -q ' %s$'; then echo 'origin refuses %s for the test' >&2; exit 1; fi\n"+
		"printf '%%s\\n' \"$lines\" | exec '%s'\n", ref, ref, own)
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return func() {
		if err := os.Rename(own, hook); err != nil {
			f.t.Fatal(err)
		}
	}
}

// dropEnv names the ref whose pushes lossyReceivePack loses the answer to.
const dropEnv = "RESTACK_TEST_DROP"

// loseAnswers has origin's answer lost to every push of the restacker's
// that moves the ref: origin takes the push, and the pass hears nothing.
func (r *restacker) loseAnswers(ref string) {
	r.t.Helper()
	self, err := os.Executable()
	if err != nil {
		r.t.Fatal(err)
	}
	path := filepath.Join(r.t.TempDir(), "lossy-receive-pack")
	script := fmt.Sprintf("#!/bin/sh\n%s=drop-acks %s='%s' exec '%s' \"$@\"\n", roleEnv, dropEnv, ref, self)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		r.t.Fatal(err)
	}
	gitOutT(r.t, r.g, "config", "remote.origin.receivepack", path)
}

// lossyReceivePack is git receive-pack less its answer to a push that
// moves the ref named. It runs the real receive-pack, which takes the
// push, and passes its report on without the status of any ref when one
// of them is that ref, as when origin's answer is lost. The side-band
// capabilities are taken out of its advertisement, so the report comes as
// plain pkt-lines. The rebase engine's tests drop every answer the same
// way.
func lossyReceivePack(ref string, args []string) int {
	cmd := exec.Command("git", append([]string{"receive-pack"}, args...)...)
	cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return 1
	}
	r := bufio.NewReader(out)
	advertised := false
	var status [][]byte
	for {
		p, err := readPkt(r)
		if err != nil {
			break
		}
		switch {
		case !advertised && p == nil:
			advertised = true
		case !advertised:
			if refs, caps, ok := bytes.Cut(p, []byte{0}); ok {
				var kept []string
				for _, c := range strings.Fields(string(caps)) {
					if !strings.HasPrefix(c, "side-band") {
						kept = append(kept, c)
					}
				}
				p = []byte(string(refs) + "\x00" + strings.Join(kept, " ") + "\n")
			}
		case bytes.HasPrefix(p, []byte("ok ")) || bytes.HasPrefix(p, []byte("ng ")) || bytes.HasPrefix(p, []byte("option ")):
			status = append(status, p)
			continue
		case p == nil:
			lost := false
			for _, line := range status {
				if fields := strings.Fields(string(line)); len(fields) > 1 && fields[1] == ref {
					lost = true
				}
			}
			for _, line := range status {
				if !lost {
					writePkt(line)
				}
			}
			status = nil
		}
		writePkt(p)
	}
	if cmd.Wait() != nil {
		return 1
	}
	return 0
}

// readPkt reads one pkt-line, nil for a flush.
func readPkt(r *bufio.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return nil, err
	}
	n, err := strconv.ParseUint(string(size[:]), 16, 16)
	if err != nil || n < 4 {
		return nil, err
	}
	p := make([]byte, n-4)
	_, err = io.ReadFull(r, p)
	return p, err
}

// writePkt writes one pkt-line to stdout, nil as a flush.
func writePkt(p []byte) {
	if p == nil {
		io.WriteString(os.Stdout, "0000")
		return
	}
	fmt.Fprintf(os.Stdout, "%04x%s", len(p)+4, p)
}

// The project's cap on automatic versions holds: with a cap of 1, the
// first landing's restack adds the change's one automatic version, and
// after a second landing the pass holds the change and reports blocked,
// saying the cap is why.
func TestAPassHonoursTheCap(t *testing.T) {
	f := hooked(t)
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.moveMain()
	r := newRestacker(t, f)
	r.pass(1)
	f.restackedTo(idA, "alice", "A's title", "a", f.at("refs/heads/a"))
	head, record := f.at("refs/heads/a"), f.at(change.Ref(rebase.Target, idA))

	f.git("checkout", "--quiet", "main")
	f.commit("main moves again", "again.txt")
	f.git("push", "--quiet", "origin", "main")
	r.pass(1)
	if f.at("refs/heads/a") != head || f.at(change.Ref(rebase.Target, idA)) != record {
		t.Fatal("the pass restacked a change over the cap")
	}
	if rep := f.reportOf(idA); rep == nil || rep.Result != change.RestackBlocked || rep.Refusal == nil ||
		!strings.Contains(*rep.Refusal, "over the cap of 1") {
		t.Errorf("a's report is %+v", rep)
	}
	r.settledState()
}

// run reconciles at once and then on its interval, without being told
// anything has moved: after a landing it restacks a ready change, and a
// change readied later, off an older main, is restacked with no landing
// between. Stopped, it returns.
func TestRunConverges(t *testing.T) {
	f := hooked(t)
	base := f.at("refs/heads/main")
	f.readyAs(idA, "alice", "a", "main", "A's title")
	f.moveMain()
	r := newRestacker(t, f)
	schema, floor := fakePolicyCap(t, 64)
	t.Setenv("VALLEY_PRINCIPAL", r.c.principal)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var out, errOut syncBuffer
	go func() {
		done <- loop(ctx, append(writeArgs(r.c, schema, floor), "--interval", "200ms"), &out, &errOut)
	}()
	// A pass prints what it pushed once it ends.
	restacked := func(id string) {
		t.Helper()
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if strings.Contains(out.String(), "report "+id+": pushed") {
				return
			}
		}
		cancel()
		t.Fatalf("run did not restack %s:\n%s\n%s", id[:4], out.String(), errOut.String())
	}
	restacked(idA)
	f.readyAs(idB, "bob", "b", base, "B's title")
	restacked(idB)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run ended with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not end once stopped")
	}
	f.restackedTo(idA, "alice", "A's title", "a", f.at("refs/heads/a"))
	f.restackedTo(idB, "bob", "B's title", "b", f.at("refs/heads/b"))
}

// A pass's wait after failures doubles from the interval, up to the
// bound.
func TestBackoffIsBounded(t *testing.T) {
	for failures, want := range []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, maxBackoff, maxBackoff} {
		if got := backoff(15*time.Second, failures); got != want {
			t.Errorf("after %d failures, run waits %v, not %v", failures, got, want)
		}
	}
}

// syncBuffer is a buffer one goroutine writes while another reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
