package rebase

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/change"
)

// world is a bare origin and a clone of it to work in, with git's
// configuration its own.
type world struct {
	t            *testing.T
	origin, work string
	env          []string
	g            Git
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, origin: filepath.Join(dir, "origin.git"), work: filepath.Join(dir, "work")}
	w.env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=rebase-test", "GIT_AUTHOR_EMAIL=rebase-test@localhost",
		"GIT_COMMITTER_NAME=rebase-test", "GIT_COMMITTER_EMAIL=rebase-test@localhost")
	w.run(dir, "init", "--quiet", "--bare", "--initial-branch=main", w.origin)
	w.run(dir, "clone", "--quiet", w.origin, w.work)
	w.g = Git{Dir: w.work, Env: w.env}
	w.git("checkout", "--quiet", "-b", "main")
	w.commit("base", "base.txt", "base\n")
	w.git("push", "--quiet", "origin", "main")
	return w
}

func (w *world) run(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, w.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (w *world) git(args ...string) string { w.t.Helper(); return w.run(w.work, args...) }

// commit writes one file on the current branch and commits it.
func (w *world) commit(subject, file, content string) string {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.work, file), []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
	w.git("add", file)
	w.git("commit", "--quiet", "-m", subject)
	return w.git("rev-parse", "HEAD")
}

// layer makes a branch on from with one commit of its own, pushes it, and
// returns its head.
func (w *world) layer(branch, from string) string {
	w.t.Helper()
	w.git("checkout", "--quiet", "-b", branch, from)
	head := w.commit("layer "+branch, branch+".txt", branch+"\n")
	w.git("push", "--quiet", "origin", branch)
	return head
}

// moveMain moves origin's main on by a commit of its own.
func (w *world) moveMain(file, content string) string {
	w.t.Helper()
	w.git("checkout", "--quiet", "main")
	head := w.commit("main moves on: "+file, file, content)
	w.git("push", "--quiet", "origin", "main")
	return head
}

// ready writes a ready record for a change at v1, on branch at its head on
// origin, through the engine's own write path.
func (w *world) ready(id, branch string) {
	w.t.Helper()
	head := w.at("refs/heads/" + branch)
	r := &change.Record{
		Schema: change.Schema, ChangeID: id, Target: Target, Owner: "author", State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: "layer " + branch, Branch: branch, Uploader: "author"}},
	}
	w.write(id, r, "", head)
}

// setState writes the change's record again in another state.
func (w *world) setState(id, state string) {
	w.t.Helper()
	old := w.at(change.Ref(Target, id))
	r := w.record(id)
	r.State, r.Seq = state, r.Seq+1
	w.write(id, r, old, old)
}

func (w *world) write(id string, r *change.Record, old string, parents ...string) {
	w.t.Helper()
	ctx := context.Background()
	commit, err := WriteRecord(ctx, w.g, r, "test record", parents...)
	if err != nil {
		w.t.Fatal(err)
	}
	if got := PushRecord(ctx, w.g, id, old, commit); got.Push != Pushed {
		w.t.Fatalf("the record of %s was not pushed: %+v", id, got)
	}
}

// at is a ref on origin, or "" for none.
func (w *world) at(ref string) string {
	w.t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir, cmd.Env = w.origin, w.env
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func (w *world) record(id string) *change.Record {
	w.t.Helper()
	data := w.run(w.origin, "cat-file", "blob", change.Ref(Target, id)+":record.json")
	r, err := change.Parse([]byte(data + "\n"))
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

// refs is every branch and record on origin, to tell that nothing moved.
func (w *world) refs() string {
	w.t.Helper()
	return w.run(w.origin, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads/", change.RefPrefix)
}

// capture reads a snapshot and the family a selection names.
func (w *world) capture(sel Selection) *Family {
	w.t.Helper()
	s, err := ReadSnapshot(context.Background(), w.g, Verifier{Attest: "attest"})
	if err != nil {
		w.t.Fatal(err)
	}
	f, err := Capture(s, sel)
	if err != nil {
		w.t.Fatal(err)
	}
	return f
}

// publish prepares, pushes and appends a family as the principal rebaser.
func (w *world) publish(f *Family) *Publication {
	w.t.Helper()
	ctx := context.Background()
	p, err := Prepare(ctx, w.g, f)
	if err != nil {
		w.t.Fatalf("prepare: %v", err)
	}
	defer p.Release(ctx, w.g)
	pub, err := Publish(ctx, w.g, p)
	if err != nil {
		w.t.Fatalf("publish: %v", err)
	}
	pub.AppendAll(ctx, w.g, "rebaser")
	return pub
}

// sameDelta holds that a layer's diff from its base is what it was.
func (w *world) sameDelta(oldBase, oldHead, newBase, newHead string) {
	w.t.Helper()
	if a, b := w.run(w.origin, "diff", oldBase, oldHead), w.run(w.origin, "diff", newBase, newHead); a != b {
		w.t.Errorf("the diff of %s changed in the rebase:\n%s\nnow:\n%s", oldHead, a, b)
	}
}

func (w *world) ancestor(a, b string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir, cmd.Env = w.origin, w.env
	return cmd.Run() == nil
}

func names(layers []*Layer) string {
	var n []string
	for _, l := range layers {
		n = append(n, l.Name)
	}
	return strings.Join(n, " ")
}

var (
	idA = strings.Repeat("k", 32)
	idB = strings.Repeat("m", 32)
	idC = strings.Repeat("n", 32)
)

// A chain A → B → C moves whole onto main: each layer onto the rebased
// layer below, each keeping its own diff, one push for every branch, and
// each change's rebased head added as v2 with the rebaser as its uploader.
func TestChainMovesWhole(t *testing.T) {
	w := newWorld(t)
	base := w.at("refs/heads/main")
	a := w.layer("a", "main")
	b := w.layer("b", "a")
	c := w.layer("c", "b")
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.ready(idC, "c")
	main := w.moveMain("main.txt", "main\n")

	f := w.capture(Selection{ID: idC})
	if got := names(f.Layers); got != "kkkk mmmm nnnn" {
		t.Fatalf("the chain under c is %q", got)
	}
	pub := w.publish(f)
	if pub.Push != Pushed {
		t.Fatalf("the branches were not pushed: %v %v", pub.Push, pub.Refused)
	}
	na, nb, nc := w.at("refs/heads/a"), w.at("refs/heads/b"), w.at("refs/heads/c")
	if !w.ancestor(main, na) || !w.ancestor(na, nb) || !w.ancestor(nb, nc) {
		t.Fatal("the rebased chain is not main, a, b, c")
	}
	w.sameDelta(base, a, main, na)
	w.sameDelta(a, b, na, nb)
	w.sameDelta(b, c, nb, nc)
	for id, head := range map[string]string{idA: na, idB: nb, idC: nc} {
		r := w.record(id)
		v := r.ActiveVersion()
		if r.State != change.Ready || v.N != 2 || HeadOf(v) != head || v.Uploader != "rebaser" {
			t.Errorf("change %s: %+v", id[:4], r)
		}
	}
	for _, res := range pub.Results {
		if res.Branch != Pushed || res.Appended == nil || res.Appended.Version != 2 || res.Unfinished() {
			t.Errorf("%s: %+v", res.Layer.Name, res)
		}
	}
}

// A shared lower layer, A → B and A → C, is prepared once: A gains one
// version, both children are rebased onto its new head, and one push moves
// all three branches. The family is the same from either child.
func TestSharedLowerLayerIsRebasedOnce(t *testing.T) {
	w := newWorld(t)
	a := w.layer("a", "main")
	b := w.layer("b", "a")
	c := w.layer("c", "a")
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.ready(idC, "c")
	main := w.moveMain("main.txt", "main\n")

	if got := names(w.capture(Selection{ID: idC, Whole: true}).Layers); got != "kkkk mmmm nnnn" {
		t.Fatalf("the family of c is %q", got)
	}
	f := w.capture(Selection{ID: idB, Whole: true})
	if got := names(f.Layers); got != "kkkk mmmm nnnn" {
		t.Fatalf("the family of b is %q", got)
	}
	if len(f.Tops()) != 2 || f.Bottom().ID != idA {
		t.Fatalf("the family's tops are %q", names(f.Tops()))
	}
	pub := w.publish(f)
	if pub.Push != Pushed {
		t.Fatalf("the branches were not pushed: %v %v", pub.Push, pub.Refused)
	}
	na, nb, nc := w.at("refs/heads/a"), w.at("refs/heads/b"), w.at("refs/heads/c")
	if w.run(w.origin, "rev-parse", nb+"^") != na || w.run(w.origin, "rev-parse", nc+"^") != na {
		t.Fatal("b and c are not both on the new head of a")
	}
	if !w.ancestor(main, na) {
		t.Fatal("a is not on main")
	}
	w.sameDelta(a, b, na, nb)
	w.sameDelta(a, c, na, nc)
	if r := w.record(idA); len(r.Versions) != 2 || HeadOf(r.ActiveVersion()) != na {
		t.Errorf("a gained other than one version: %+v", r)
	}
}

// A conflict in one child holds the whole family: no branch moves, and no
// record gains a version, the siblings' included. The refusal names the
// layer and the path.
func TestConflictMovesNothing(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "a")
	w.layer("c", "a")
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.ready(idC, "c")
	w.moveMain("c.txt", "main has its own\n")
	before := w.refs()

	f := w.capture(Selection{ID: idA, Whole: true})
	_, err := Prepare(context.Background(), w.g, f)
	var r *Refusal
	if !errors.As(err, &r) || r.Kind != Conflict || r.Layer.ID != idC || r.Below != "kkkk as rebased" {
		t.Fatalf("prepare: %v", err)
	}
	if strings.Join(r.Paths, " ") != "c.txt" {
		t.Errorf("the conflict names %q", r.Paths)
	}
	if after := w.refs(); after != before {
		t.Errorf("a conflict moved something:\n%s\nnow:\n%s", before, after)
	}
	if out := w.git("worktree", "list", "--porcelain"); strings.Count(out, "worktree ") != 1 {
		t.Errorf("the rebase left a worktree:\n%s", out)
	}
}

// A layer whose commits are on main already rebases to nothing, and is
// refused rather than retired.
func TestEmptyLayerIsRefused(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.ready(idA, "a")
	w.moveMain("a.txt", "a\n")
	before := w.refs()

	_, err := Prepare(context.Background(), w.g, w.capture(Selection{ID: idA}))
	var r *Refusal
	if !errors.As(err, &r) || r.Kind != EmptyLayer || r.Layer.ID != idA || r.Below != Target {
		t.Fatalf("prepare: %v", err)
	}
	if after := w.refs(); after != before {
		t.Errorf("an empty layer moved something")
	}
}

// A branch moved on origin between the capture and the push loses its
// lease: the atomic push moves no branch, says which was refused, and no
// version is appended.
func TestMovedBranchLosesItsLease(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "a")
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.moveMain("main.txt", "main\n")
	ctx := context.Background()

	p, err := Prepare(ctx, w.g, w.capture(Selection{ID: idB}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release(ctx, w.g)
	elsewhere := w.layer("elsewhere", "b")
	w.git("push", "--quiet", "--force", "origin", elsewhere+":refs/heads/b")
	before := w.refs()

	pub, err := Publish(ctx, w.g, p)
	if err != nil {
		t.Fatal(err)
	}
	pub.AppendAll(ctx, w.g, "rebaser")
	stale := false
	for _, ref := range pub.Refused {
		stale = stale || ref.Ref == "refs/heads/b" && strings.Contains(ref.Why, "stale info")
	}
	if pub.Push != Refused || !stale {
		t.Fatalf("the push was %v, refusing %+v", pub.Push, pub.Refused)
	}
	if after := w.refs(); after != before {
		t.Errorf("a refused push moved something:\n%s\nnow:\n%s", before, after)
	}
	for _, res := range pub.Results {
		if res.Appended != nil || res.Unfinished() {
			t.Errorf("%s: %+v", res.Layer.Name, res.Appended)
		}
	}
}

// After the branches move, each append stands alone. A change unreadied
// meanwhile gains no version and writes no record; one whose record push
// the hook refuses keeps the record commit it tried; one whose push
// response is lost is unknown, though origin took it; and the rest are
// appended.
func TestAppendsAfterThePushStandAlone(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "a")
	w.layer("c", "b")
	w.layer("d", "c")
	idD := strings.Repeat("p", 32)
	w.ready(idA, "a")
	w.ready(idB, "b")
	w.ready(idC, "c")
	w.ready(idD, "d")
	w.moveMain("main.txt", "main\n")
	ctx := context.Background()

	p, err := Prepare(ctx, w.g, w.capture(Selection{ID: idD}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release(ctx, w.g)
	pub, err := Publish(ctx, w.g, p)
	if err != nil || pub.Push != Pushed {
		t.Fatalf("publish: %v %v", err, pub.Push)
	}
	w.setState(idB, change.Draft)
	hook := filepath.Join(w.origin, "hooks", "pre-receive")
	refuse := "#!/bin/sh\nwhile read old new ref; do [ \"$ref\" = " + change.Ref(Target, idC) + " ] && exit 1; done\nexit 0\n"
	if err := os.WriteFile(hook, []byte(refuse), 0o755); err != nil {
		t.Fatal(err)
	}
	// A receive-pack that takes the push and then fails, as when the
	// connection drops before the client reads the result.
	lossy := filepath.Join(t.TempDir(), "lossy-receive-pack")
	if err := os.WriteFile(lossy, []byte("#!/bin/sh\ngit-receive-pack \"$@\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, res := range pub.Results {
		if res.Layer.ID == idD {
			w.git("config", "remote.origin.receivepack", lossy)
		}
		pub.Append(ctx, w.g, "rebaser", res)
	}
	w.git("config", "--unset", "remote.origin.receivepack")
	byID := map[string]*Appended{}
	for _, res := range pub.Results {
		byID[res.Layer.ID] = res.Appended
	}
	if ap := byID[idA]; ap.Err != nil || ap.Version != 2 || ap.Write.Push != Pushed {
		t.Errorf("a: %+v", ap)
	}
	if ap := byID[idB]; ap.Err == nil || ap.Version != 0 || ap.Write.Commit != "" || ap.Write.Push != Unpushed {
		t.Errorf("b, unreadied: %+v", ap)
	}
	if ap := byID[idC]; ap.Err == nil || ap.Write.Push != Refused || ap.Write.Commit == "" || ap.Write.Why == "" ||
		w.at(change.Ref(Target, idC)) != ap.Write.Old {
		t.Errorf("c, refused by the hook: %+v", ap)
	}
	if ap := byID[idD]; ap.Err == nil || ap.Write.Push != Unknown || w.at(change.Ref(Target, idD)) != ap.Write.Commit {
		t.Errorf("d, its response lost: %+v", ap)
	}
	if r := w.record(idB); r.State != change.Draft || len(r.Versions) != 1 {
		t.Errorf("b gained a version while a draft: %+v", r)
	}
	var unfinished []string
	for _, res := range pub.Results {
		if res.Unfinished() {
			unfinished = append(unfinished, res.Layer.Name)
		}
	}
	if got := strings.Join(unfinished, " "); got != "mmmm nnnn pppp" {
		t.Errorf("the unfinished layers are %q", got)
	}
}

// A cancelled context publishes nothing: Prepare under one leaves no
// worktree and no ref behind, and Publish under one pushes nothing, the
// prepared heads kept until they are released.
func TestCancelledBeforePublishPublishesNothing(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.ready(idA, "a")
	w.moveMain("main.txt", "main\n")
	before := w.refs()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Prepare(cancelled, w.g, w.capture(Selection{ID: idA})); !errors.Is(err, context.Canceled) {
		t.Fatalf("prepare under a cancelled context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p, err := Prepare(ctx, w.g, w.capture(Selection{ID: idA}))
	if err != nil {
		t.Fatal(err)
	}
	if kept := w.git("for-each-ref", "--format=%(objectname)", keepPrefix); kept != p.Heads[p.Family.Bottom()] {
		t.Errorf("the prepared head is not kept: %q", kept)
	}
	cancel()
	pub, err := Publish(ctx, w.g, p)
	if !errors.Is(err, context.Canceled) || pub.Push != Unpushed {
		t.Fatalf("publish under a cancelled context: %v, %v", err, pub.Push)
	}
	if after := w.refs(); after != before {
		t.Errorf("a cancelled rebase moved something")
	}
	if err := p.Release(ctx, w.g); err != nil {
		t.Fatal(err)
	}
	if kept := w.git("for-each-ref", keepPrefix); kept != "" || strings.Count(w.git("worktree", "list", "--porcelain"), "worktree ") != 1 {
		t.Errorf("the rebase left a ref or a worktree behind: %q", kept)
	}
}

// Capture reports each layer's state and refuses nothing a caller could
// hold instead: a draft and a plain branch are layers, which Check then
// refuses as the manual rebase does.
func TestCaptureReportsStatesForTheCallerToHold(t *testing.T) {
	w := newWorld(t)
	w.layer("a", "main")
	w.layer("b", "a")
	w.layer("c", "b")
	w.ready(idB, "b")
	w.ready(idC, "c")
	w.setState(idB, change.Draft)
	w.moveMain("main.txt", "main\n")

	f := w.capture(Selection{ID: idC})
	var states []string
	for _, l := range f.Layers {
		states = append(states, string(l.State()))
	}
	if got := strings.Join(states, " "); got != "branch draft ready" {
		t.Fatalf("the states are %q", got)
	}
	var r *Refusal
	if err := f.Check(); !errors.As(err, &r) || r.Kind != NotReady || r.Layer.ID != idB {
		t.Fatalf("check: %v", err)
	}
}
