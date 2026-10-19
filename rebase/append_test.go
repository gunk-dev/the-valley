package rebase

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"the-valley/change"
)

// moveBranches prepares a family and pushes its branches, appending no
// version, and returns each layer's prepared head by change ID.
func (w *world) moveBranches(f *Family) map[string]string {
	w.t.Helper()
	ctx := context.Background()
	p, err := Prepare(ctx, w.g, f)
	if err != nil {
		w.t.Fatalf("prepare: %v", err)
	}
	defer p.Release(ctx, w.g)
	pub, err := Publish(ctx, w.g, p)
	if err != nil || pub.Push != Pushed {
		w.t.Fatalf("publish: %v %v", err, pub.Push)
	}
	heads := map[string]string{}
	for l, h := range p.Heads {
		heads[l.ID] = h
	}
	return heads
}

// ownerPushes has the owner add a commit on top of origin's branch.
func (w *world) ownerPushes(branch string) string {
	w.t.Helper()
	w.git("fetch", "--quiet", "origin")
	w.git("checkout", "--quiet", "-B", branch, "origin/"+branch)
	file := branch + "-own.txt"
	before, _ := os.ReadFile(filepath.Join(w.work, file))
	head := w.commit("the owner's own work on "+branch, file, string(before)+"own\n")
	w.git("push", "--quiet", "origin", branch)
	return head
}

// A restack append writes the record commit and pushes nothing: origin's
// record stays where it was, and the commit, with the record it holds, is
// in the repository for the caller to keep before the push. The version
// keeps the active version's title, though the commit's subject differs,
// and names its branch. The push then moves the record to that commit.
func TestRestackAppendKeepsTheActiveTitle(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.readyTitled(idA, "a", "A title unlike its commit's subject")
	w.moveMain("main.txt", "main\n")
	f := w.capture(Selection{ID: idA})
	was := f.Bottom().Head
	head := w.moveBranches(f)[idA]
	ref := change.Ref(Target, idA)
	old := w.at(ref)
	ctx := context.Background()

	p, err := BuildRestack(ctx, w.g, RestackAppend{ID: idA, Head: head, IfActive: was, Principal: "restacker"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Old != old || p.Ref != ref || p.Commit == "" || p.Version != 2 || w.at(ref) != old {
		t.Fatalf("the build pushed, or does not say what it wrote: %+v; origin's record is at %s", p, w.at(ref))
	}
	if parents := w.git("rev-list", "--parents", "-n", "1", p.Commit); parents != p.Commit+" "+old+" "+head {
		t.Errorf("the record commit's parents are %q", parents)
	}
	r, err := ReadRecord(ctx, w.g, p.Commit)
	if err != nil {
		t.Fatal(err)
	}
	v := r.ActiveVersion()
	if v.N != 2 || HeadOf(v) != head || v.Title != "A title unlike its commit's subject" || v.Branch != "a" || v.Uploader != "restacker" {
		t.Errorf("the new version is %+v", v)
	}
	ap := p.Push(ctx, w.g)
	if ap.Err != nil || ap.Version != 2 || ap.Write.Push != Pushed || w.at(ref) != p.Commit {
		t.Fatalf("the push: %+v; origin's record is at %s", ap, w.at(ref))
	}
}

// A manual append keeps titling a version by its head's subject, and
// takes a head anywhere on the branch.
func TestManualAppendTitlesBySubject(t *testing.T) {
	w := newWorld(t)
	w.layer("b", "main")
	w.readyTitled(idB, "b", "B's own title")
	was := w.at("refs/heads/b")
	head := w.ownerPushes("b")
	w.ownerPushes("b")

	ap := AppendVersion(context.Background(), w.g, Append{ID: idB, Branch: "b", Head: head, IfActive: was, Principal: "author"})
	if ap.Err != nil || ap.Version != 2 {
		t.Fatalf("append: %+v", ap)
	}
	if v := w.record(idB).ActiveVersion(); v.Title != "the owner's own work on b" || HeadOf(v) != head {
		t.Errorf("the new version is %+v", v)
	}
}

// A restack append records only the head the restack moved the branch to.
// A branch the owner has pushed past that head is refused when the record
// is built, and again when it is pushed, with nothing moved either time.
func TestRestackAppendNeedsTheBranchAtItsHead(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "main")
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.moveMain("main.txt", "main\n")
	ctx := context.Background()

	fa := w.capture(Selection{ID: idA})
	wasA := fa.Bottom().Head
	headA := w.moveBranches(fa)[idA]
	w.ownerPushes("a")
	before := w.refs()
	_, err := BuildRestack(ctx, w.g, RestackAppend{ID: idA, Head: headA, IfActive: wasA, Principal: "restacker"})
	if err == nil || !strings.Contains(err.Error(), "not "+short(headA)) {
		t.Fatalf("a restack append to a branch past its head: %v", err)
	}
	if after := w.refs(); after != before {
		t.Errorf("a refused restack append moved something")
	}

	fb := w.capture(Selection{ID: idB})
	wasB := fb.Bottom().Head
	headB := w.moveBranches(fb)[idB]
	p, err := BuildRestack(ctx, w.g, RestackAppend{ID: idB, Head: headB, IfActive: wasB, Principal: "restacker"})
	if err != nil {
		t.Fatal(err)
	}
	w.ownerPushes("b")
	before = w.refs()
	ap := p.Push(ctx, w.g)
	if ap.Err == nil || ap.Version != 0 || ap.Write.Push != Unpushed || ap.Write.Commit != p.Commit || ap.Write.Old != p.Old {
		t.Fatalf("a restack push to a branch moved since the build: %+v", ap)
	}
	if after := w.refs(); after != before {
		t.Errorf("a refused restack push moved something")
	}
}

// A restack append is kept and restored as recovery will keep and restore
// it, and the restored write is the one kept, in the same mode. Once the
// owner pushes past the head it records, the restored write's push is
// refused, as the kept one's would be, and nothing moves. Restored while
// the branch is still at that head, it pushes.
func TestRestoredRestackStillNeedsTheBranchAtItsHead(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "main")
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.moveMain("main.txt", "main\n")
	ctx := context.Background()
	keep := func(id string) []byte {
		t.Helper()
		f := w.capture(Selection{ID: id})
		p, err := BuildRestack(ctx, w.g, RestackAppend{ID: id, Head: w.moveBranches(f)[id], IfActive: f.Bottom().Head, Principal: "restacker"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var q PendingVersion
		if err := json.Unmarshal(data, &q); err != nil || !reflect.DeepEqual(&q, p) {
			t.Fatalf("%s restores as %+v, %v; it was kept as %+v", data, q, err, *p)
		}
		return data
	}
	restored := func(data []byte) *PendingVersion {
		t.Helper()
		var p PendingVersion
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		return &p
	}

	keptA, keptB := keep(idA), keep(idB)
	w.ownerPushes("a")
	before := w.refs()
	ap := restored(keptA).Push(ctx, w.g)
	if ap.Err == nil || ap.Version != 0 || ap.Write.Push != Unpushed {
		t.Fatalf("a restored restack push to a branch the owner moved: %+v", ap)
	}
	if after := w.refs(); after != before {
		t.Errorf("a refused restored restack push moved something")
	}
	p := restored(keptB)
	if ap := p.Push(ctx, w.g); ap.Err != nil || ap.Version != 2 || w.at(change.Ref(Target, idB)) != p.Commit {
		t.Fatalf("a restored restack push to a branch still at its head: %+v", ap)
	}
}

// Only the form a write is kept in restores. A form missing anything the
// push checks, holding something else, or not agreeing with itself is
// refused, and so is a field named twice, which would otherwise let a
// second mode overrule the first.
func TestRestoreRefusesAMalformedWrite(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.ready(idA, "a")
	w.moveMain("main.txt", "main\n")
	ctx := context.Background()
	f := w.capture(Selection{ID: idA})
	p, err := BuildRestack(ctx, w.g, RestackAppend{ID: idA, Head: w.moveBranches(f)[idA], IfActive: f.Bottom().Head, Principal: "restacker"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	kept := string(data)
	field := func(name, value string) string { return `"` + name + `":"` + value + `"` }
	for what, form := range map[string]string{
		"no mode":              strings.Replace(kept, field("mode", "restack")+",", "", 1),
		"an unknown mode":      strings.Replace(kept, field("mode", "restack"), field("mode", "rebase"), 1),
		"the mode twice":       strings.Replace(kept, field("mode", "restack"), field("mode", "restack")+","+field("mode", "manual"), 1),
		"an unknown field":     strings.Replace(kept, `{"schema"`, `{"lease":"none","schema"`, 1),
		"no record read":       strings.Replace(kept, field("old", p.Old), field("old", ""), 1),
		"no branch":            strings.Replace(kept, field("branch", "a"), field("branch", ""), 1),
		"a head it is not at":  strings.Replace(kept, field("head", p.Head), field("head", p.Old), 1),
		"another change's ref": strings.Replace(kept, field("ref", p.Ref), field("ref", change.Ref(Target, idB)), 1),
		"a report's schema":    strings.Replace(kept, pendingVersionSchema, pendingReportSchema, 1),
		"a second form":        kept + " " + kept,
	} {
		if form == kept {
			t.Fatalf("%s: the edit changed nothing", what)
		}
		var q PendingVersion
		if err := json.Unmarshal([]byte(form), &q); err == nil {
			t.Errorf("%s restored: %+v", what, q)
		}
	}
	var r PendingReport
	if err := json.Unmarshal(data, &r); err == nil {
		t.Errorf("a pending version restored as a report: %+v", r)
	}
}

// A report kept and restored is the report written, and its push moves
// the report ref to it. A kept report about another change than the one
// whose ref it names is refused.
func TestRestoredReportPushes(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.ready(idA, "a")
	ctx := context.Background()
	tip := change.HeadPrefix + w.at("refs/heads/a")
	r := &change.RestackReport{
		Schema: change.RestackSchema, ChangeID: idA, Target: Target,
		TargetTip: change.HeadPrefix + w.at("refs/heads/main"), Operation: "test-1", Result: change.RestackBlocked,
		Layers: []change.RestackLayer{{
			ChangeID: idA, Record: change.HeadPrefix + w.at(change.Ref(Target, idA)), Version: 1,
			Head: tip, Branch: "a", BranchTip: &tip,
		}},
	}
	p, err := BuildReport(ctx, w.g, r, ReportMessage("kkkk", r), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q PendingReport
	if err := json.Unmarshal(data, &q); err != nil || !reflect.DeepEqual(&q, p) {
		t.Fatalf("%s restores as %+v, %v", data, q, err)
	}
	if wr := q.Push(ctx, w.g); wr.Push != Pushed || w.at(RestackPrefix+idA) != p.Commit {
		t.Fatalf("the restored report's push: %+v", wr)
	}
	other := strings.Replace(string(data), `"change_id":"`+idA+`","ref":"`+RestackPrefix+idA+`"`,
		`"change_id":"`+idB+`","ref":"`+RestackPrefix+idB+`"`, 1)
	if err := json.Unmarshal([]byte(other), &q); err == nil || !strings.Contains(err.Error(), "is about "+idA) {
		t.Errorf("a report kept under another change's ref: %v", err)
	}
}

// A report commit is written and then pushed: written, it holds the report
// and is not on origin; pushed, origin's report ref names it. The next
// report has the one it replaces as its parent, and a push leased on a
// report that has moved is refused.
func TestReportIsWrittenThenPushed(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.ready(idA, "a")
	ctx := context.Background()
	tip := change.HeadPrefix + w.at("refs/heads/a")
	r := &change.RestackReport{
		Schema: change.RestackSchema, ChangeID: idA, Target: Target,
		TargetTip: change.HeadPrefix + w.at("refs/heads/main"), Operation: "test-1", Result: change.RestackBlocked,
		Layers: []change.RestackLayer{{
			ChangeID: idA, Record: change.HeadPrefix + w.at(change.Ref(Target, idA)), Version: 1,
			Head: tip, Branch: "a", BranchTip: &tip,
		}},
	}
	ref := RestackPrefix + idA

	first, err := WriteReport(ctx, w.g, r, ReportMessage("kkkk", r), "")
	if err != nil {
		t.Fatal(err)
	}
	if w.at(ref) != "" {
		t.Fatal("writing a report pushed it")
	}
	if parents := w.git("rev-list", "--parents", "-n", "1", first); parents != first {
		t.Errorf("a change's first report has parents: %q", parents)
	}
	if got, err := ReadReport(ctx, w.g, first); err != nil || got.Operation != "test-1" || got.Result != change.RestackBlocked {
		t.Fatalf("the report commit reads back as %+v, %v", got, err)
	}
	if wr := PushReport(ctx, w.g, idA, "", first); wr.Push != Pushed || w.at(ref) != first {
		t.Fatalf("the first push: %+v", wr)
	}

	r.Operation = "test-2"
	second, err := WriteReport(ctx, w.g, r, ReportMessage("kkkk", r), first)
	if err != nil {
		t.Fatal(err)
	}
	if parents := w.git("rev-list", "--parents", "-n", "1", second); parents != second+" "+first {
		t.Errorf("a report's parents are %q", parents)
	}
	if wr := PushReport(ctx, w.g, idA, "", second); wr.Push != Refused || w.at(ref) != first {
		t.Errorf("a push leased on no report, where there is one: %+v", wr)
	}
	if wr := PushReport(ctx, w.g, idA, first, second); wr.Push != Pushed || w.at(ref) != second {
		t.Errorf("the second push: %+v", wr)
	}
}

// Release runs under a context that is already done, and for no longer
// than its bound: a hook that never returns while the kept refs are
// deleted is killed with everything it started.
func TestReleaseEndsWithinItsBound(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.ready(idA, "a")
	w.moveMain("main.txt", "main\n")
	p, err := Prepare(context.Background(), w.g, w.capture(Selection{ID: idA}))
	if err != nil {
		t.Fatal(err)
	}
	guard := "[ \"$1\" = prepared ] || exit 0\ncase $(cat) in *' " + keepPrefix + "'*) ;; *) exit 0 ;; esac"
	b := newBlocker(t, filepath.Join(w.work, ".git", "hooks", "reference-transaction"), guard)
	defer func(was time.Duration) { cleanupBound = was }(cleanupBound)
	cleanupBound = 2 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- p.Release(ctx, w.g) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a release whose refs were never deleted says it released them")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("release did not return")
	}
	if took := time.Since(start); took > cleanupBound+waitDelay+5*time.Second {
		t.Errorf("release took %v, with a bound of %v", took, cleanupBound)
	}
	b.wait(t)
	if b.beating() {
		t.Error("the hook outlived the bound")
	}
}

// An unattended command that reaches origin is killed whole when its
// context's deadline passes: git and the upload-pack it started for a
// fetch that never answers.
func TestUnattendedFetchEndsAtTheDeadline(t *testing.T) {
	w := newWorld(t)
	b := newBlocker(t, filepath.Join(t.TempDir(), "upload-pack"), "")
	w.git("config", "remote.origin.uploadpack", b.script)
	g := w.g
	g.Unattended = true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, err := ListOrigin(ctx, g, "refs/heads/*")
	if err == nil {
		t.Fatal("a fetch that never answered read origin")
	}
	if took := time.Since(start); took > 2*time.Second+waitDelay+5*time.Second {
		t.Errorf("the read took %v, with a deadline of 2s", took)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Errorf("the read ended before its deadline: %v", err)
	}
	b.wait(t)
	if b.beating() {
		t.Error("the upload-pack git started outlived the deadline")
	}
}
