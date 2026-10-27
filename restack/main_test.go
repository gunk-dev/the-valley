package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/composed"
	"the-valley/rebase"
)

// hookEnv names the rendered pre-receive hook restack-unit wires origin
// with (nix/checks/restack.nix). Without it, the tests that need the hook
// are skipped.
const hookEnv = "VALLEY_RESTACK_TEST_HOOK"

var idA = strings.Repeat("k", 32)

// fixture is a bare origin and a clone of it to work in. The clone's git
// commits as an author of its own, named in its environment, whatever the
// test's process inherits.
type fixture struct {
	t            *testing.T
	origin, work string
	env          []string
}

func newFixture(t *testing.T, hook string) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, origin: filepath.Join(dir, "origin.git"), work: filepath.Join(dir, "work")}
	f.env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=A. Author", "GIT_AUTHOR_EMAIL=author@example.invalid",
		"GIT_COMMITTER_NAME=A. Author", "GIT_COMMITTER_EMAIL=author@example.invalid")
	f.run(dir, "init", "--quiet", "--bare", "--initial-branch=main", f.origin)
	if hook != "" {
		data, err := os.ReadFile(hook)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.origin, "hooks", "pre-receive"), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.run(dir, "clone", "--quiet", f.origin, f.work)
	f.git("checkout", "--quiet", "-b", "main")
	f.commit("base", "base.txt")
	f.git("push", "--quiet", "origin", "main")
	return f
}

func (f *fixture) run(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) git(args ...string) string { f.t.Helper(); return f.run(f.work, args...) }

func (f *fixture) commit(subject, file string) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.work, file), []byte(subject+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git("add", file)
	f.git("commit", "--quiet", "-m", subject)
	return f.git("rev-parse", "HEAD")
}

// as is the clone's git pushing as a principal.
func (f *fixture) as(principal string) rebase.Git {
	return rebase.Git{Dir: f.work, Env: append(append([]string(nil), f.env...), "VALLEY_PRINCIPAL="+principal)}
}

// readied pushes a branch off main with one commit, and readies it as a
// change owned by owner, under a title unlike the commit's subject.
// It returns the head.
func (f *fixture) readied(owner, branch, title string) string {
	f.t.Helper()
	f.git("checkout", "--quiet", "-b", branch, "main")
	head := f.commit("layer "+branch, branch+".txt")
	f.git("push", "--quiet", "origin", branch)
	r := &change.Record{
		Schema: change.Schema, ChangeID: idA, Target: rebase.Target, Owner: owner, State: change.Ready, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: title, Branch: branch, Uploader: owner}},
	}
	ctx := context.Background()
	g := f.as(owner)
	commit, err := rebase.WriteRecord(ctx, g, r, "test record", head)
	if err != nil {
		f.t.Fatal(err)
	}
	if w := rebase.PushRecord(ctx, g, idA, "", commit); w.Push != rebase.Pushed {
		f.t.Fatalf("the record of %s was not pushed: %+v", idA, w)
	}
	return head
}

// moveMain moves origin's main on by a commit of its own.
func (f *fixture) moveMain() {
	f.t.Helper()
	f.git("checkout", "--quiet", "main")
	f.commit("main moves on", "main.txt")
	f.git("push", "--quiet", "origin", "main")
}

// at is a ref on origin, or "" for none.
func (f *fixture) at(ref string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir, cmd.Env = f.origin, f.env
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// withoutIdentity leaves the test's process with no identity variable,
// as a service may run.
func withoutIdentity(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE", "EMAIL"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

// restacked reads origin into the restacker's scratch repository, rebases
// the change's family there, and pushes its branch. It returns the
// family and the prepared head of its bottom layer.
func restacked(t *testing.T, g rebase.Git) (*rebase.Family, string) {
	t.Helper()
	ctx := context.Background()
	s, err := rebase.ReadSnapshot(ctx, g, rebase.Verifier{})
	if err != nil {
		t.Fatal(err)
	}
	fam, err := rebase.Capture(s, rebase.Selection{ID: idA})
	if err != nil {
		t.Fatal(err)
	}
	p, err := rebase.Prepare(ctx, g, fam)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer p.Release(ctx, g)
	pub, err := rebase.Publish(ctx, g, p)
	if err != nil || pub.Push != rebase.Pushed {
		t.Fatalf("publish: %v %v %+v", err, pub.Push, pub.Refused)
	}
	return fam, p.Heads[fam.Bottom()]
}

func logged(t *testing.T, g rebase.Git, format, commit string) string {
	t.Helper()
	out, err := gitOut(context.Background(), g, "log", "-1", "--format="+format, commit)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// With no identity variable in its environment, the restacker commits
// under the identity its flags give: it is the committer of each commit
// it rebases, whose author stays the change's, and the author and the
// committer of each record commit. An identity in the environment is not
// inherited.
func TestScratchCommitsAsTheController(t *testing.T) {
	withoutIdentity(t)
	f := newFixture(t, "")
	f.readied("author", "a", "A title")
	f.moveMain()
	c := config{repo: f.origin, state: t.TempDir(), principal: "restacker",
		commitName: "The Restacker", commitEmail: "restacker@example.invalid"}
	ctx := context.Background()
	g, err := scratchRepo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	fam, head := restacked(t, g)
	const restacker = "The Restacker <restacker@example.invalid>"
	if got := logged(t, g, "%an <%ae>|%cn <%ce>", head); got != "A. Author <author@example.invalid>|"+restacker {
		t.Errorf("the rebased commit is authored and committed as %q", got)
	}
	p, err := rebase.BuildRestack(ctx, g, rebase.RestackAppend{ID: idA, Head: head, IfActive: fam.Bottom().Head, Principal: "restacker"})
	if err != nil {
		t.Fatal(err)
	}
	if got := logged(t, g, "%an <%ae>|%cn <%ce>", p.Commit); got != restacker+"|"+restacker {
		t.Errorf("the record commit is authored and committed as %q", got)
	}

	t.Setenv("GIT_AUTHOR_NAME", "Inherited")
	t.Setenv("GIT_COMMITTER_EMAIL", "inherited@example.invalid")
	t.Setenv("EMAIL", "inherited@example.invalid")
	g, err = scratchRepo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := gitOut(ctx, g, "mktree")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := gitOut(ctx, g, "commit-tree", strings.TrimSpace(tree), "-m", "inherited")
	if err != nil {
		t.Fatal(err)
	}
	if got := logged(t, g, "%an <%ae>|%cn <%ce>", strings.TrimSpace(commit)); got != restacker+"|"+restacker {
		t.Errorf("with an identity in the environment, a commit is authored and committed as %q", got)
	}
}

// Configuration the environment injects reaches no git the restacker
// runs. An author and a committer given in GIT_CONFIG_COUNT entries and in
// GIT_CONFIG_PARAMETERS would beat the scratch repository's user.name and
// user.email. They are not inherited: the restacker is the committer of
// each commit it rebases, whose author stays the change's, and the author
// and the committer of each record commit.
func TestInheritedConfigurationDoesNotCommit(t *testing.T) {
	withoutIdentity(t)
	f := newFixture(t, "")
	f.readied("author", "a", "A title")
	f.moveMain()
	for name, value := range map[string]string{
		"GIT_CONFIG_COUNT": "2",
		"GIT_CONFIG_KEY_0": "author.name", "GIT_CONFIG_VALUE_0": "Inherited",
		"GIT_CONFIG_KEY_1": "author.email", "GIT_CONFIG_VALUE_1": "inherited@example.invalid",
		"GIT_CONFIG_PARAMETERS": "'committer.name'='Inherited' 'committer.email'='inherited@example.invalid'",
	} {
		t.Setenv(name, value)
	}
	c := config{repo: f.origin, state: t.TempDir(), principal: "restacker",
		commitName: "The Restacker", commitEmail: "restacker@example.invalid"}
	ctx := context.Background()
	g, err := scratchRepo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	fam, head := restacked(t, g)
	const restacker = "The Restacker <restacker@example.invalid>"
	if got := logged(t, g, "%an <%ae>|%cn <%ce>", head); got != "A. Author <author@example.invalid>|"+restacker {
		t.Errorf("the rebased commit is authored and committed as %q", got)
	}
	p, err := rebase.BuildRestack(ctx, g, rebase.RestackAppend{ID: idA, Head: head, IfActive: fam.Bottom().Head, Principal: "restacker"})
	if err != nil {
		t.Fatal(err)
	}
	if got := logged(t, g, "%an <%ae>|%cn <%ce>", p.Commit); got != restacker+"|"+restacker {
		t.Errorf("the record commit is authored and committed as %q", got)
	}
}

// A pass locks its state directory. A second lock fails at once, saying
// another pass holds it, and succeeds once the first is let go.
func TestAPassLocksItsState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	release, err := lockState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockState(dir); err == nil || !strings.Contains(err.Error(), "another pass is using the state directory") {
		t.Fatalf("a second lock while one is held: %v", err)
	}
	release()
	again, err := lockState(dir)
	if err != nil {
		t.Fatalf("a lock once the first is let go: %v", err)
	}
	again()
}

// A state directory belongs to the repository, target and principal the
// first pass used it for, and a pass for any other is refused.
func TestStateBelongsToOneRepositoryTargetAndPrincipal(t *testing.T) {
	dir := t.TempDir()
	first := binding{Repository: "ssh://valley/project.git", Target: "main", Principal: "restacker"}
	if err := bindState(dir, first); err != nil {
		t.Fatal(err)
	}
	if err := bindState(dir, first); err != nil {
		t.Fatalf("the same binding again: %v", err)
	}
	for _, other := range []binding{
		{Repository: "ssh://valley/other.git", Target: "main", Principal: "restacker"},
		{Repository: "ssh://valley/project.git", Target: "main", Principal: "requester"},
		{Repository: "ssh://valley/project.git", Target: "next", Principal: "restacker"},
	} {
		if err := bindState(dir, other); err == nil || !strings.Contains(err.Error(), "give each its own --state") {
			t.Errorf("a pass for %+v: %v", other, err)
		}
	}
}

// blocker is a script that stands for a program that does not return.
// The first time it runs, it says it has started, then keeps its output
// open for a minute and beats while it does: it adds a line to a file
// every tenth of a second.
type blocker struct {
	script, started, beats string
}

func newBlocker(t *testing.T, script string) *blocker {
	t.Helper()
	dir := t.TempDir()
	b := &blocker{script: script, started: filepath.Join(dir, "started"), beats: filepath.Join(dir, "beats")}
	body := fmt.Sprintf("#!/bin/sh\n[ -e '%s' ] && exit 0\n: > '%s'\n"+
		"i=0\nwhile [ $i -lt 600 ]; do echo >> '%s'; sleep 0.1; i=$((i+1)); done\n",
		b.started, b.started, b.beats)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return b
}

func (b *blocker) wait(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(b.started); err == nil {
			return
		}
	}
	t.Fatal("the blocker never started")
}

func (b *blocker) beating() bool {
	size := func() int64 {
		fi, err := os.Stat(b.beats)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	time.Sleep(200 * time.Millisecond)
	before := size()
	time.Sleep(time.Second)
	return size() != before
}

// bounded runs read under a context with a two-second deadline, and holds
// that it fails, returns soon after the deadline, and leaves nothing of
// the blocker it ran into running.
func bounded(t *testing.T, b *blocker, read func(ctx context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := read(ctx); err == nil {
		t.Fatal("a read that never returned succeeded")
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("the read took %v, with a deadline of 2s", took)
	}
	b.wait(t)
	if b.beating() {
		t.Error("what the read started outlived the deadline")
	}
}

// Composing the policy ends at the pass's deadline, with the cue that
// never returned killed.
func TestPolicyCompositionEndsAtTheDeadline(t *testing.T) {
	f := newFixture(t, "")
	floor := t.TempDir()
	if err := os.WriteFile(filepath.Join(floor, "floor.cue"), []byte("package verification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := newBlocker(t, filepath.Join(t.TempDir(), "cue"))
	c := config{cue: b.script, schema: filepath.Join(floor, "floor.cue"), floor: composed.Floor{Path: floor}, projectPolicy: "policy/project"}
	g := rebase.Git{Dir: f.work, Env: f.env}
	tip := f.git("rev-parse", "HEAD")
	bounded(t, b, func(ctx context.Context) error {
		_, err := policyCap(ctx, g, c, tip)
		return err
	})
}

// Reading the floor from the instance repository ends at the pass's
// deadline, with the ssh that never answered killed: the scratch
// repository's git reaches origin unattended.
func TestInstanceReadEndsAtTheDeadline(t *testing.T) {
	b := newBlocker(t, filepath.Join(t.TempDir(), "ssh"))
	t.Setenv("GIT_SSH_COMMAND", b.script)
	c := config{repo: "ssh://valley/project.git", state: t.TempDir(), principal: "restacker",
		commitName: "The Restacker", commitEmail: "restacker@example.invalid",
		instanceRepo: "ssh://valley/instance.git", floor: composed.Floor{Ref: "refs/heads/main", Dir: "policy/instance"}}
	g, err := scratchRepo(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	bounded(t, b, func(ctx context.Context) error {
		_, err := policyCap(ctx, g, c, strings.Repeat("0", 40))
		return err
	})
}

// The floor is read at --instance-ref by that exact name. ls-remote
// matches its pattern against the tail of each name, so for an instance
// repository with no refs/heads/main it lists
// refs/heads/refs/heads/main, a branch a pusher can make. That branch is
// no floor, and the pass finds none.
func TestTheFloorIsReadAtItsRefByItsExactName(t *testing.T) {
	f := newFixture(t, "")
	tip := f.git("rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(f.work, "policy", "instance"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.commit("package verification", filepath.Join("policy", "instance", "floor.cue"))
	instance := filepath.Join(t.TempDir(), "instance.git")
	f.run(f.work, "init", "--quiet", "--bare", instance)
	f.git("push", "--quiet", instance, "HEAD:refs/heads/refs/heads/main")
	c := config{repo: f.origin, state: t.TempDir(), principal: "restacker",
		commitName: "The Restacker", commitEmail: "restacker@example.invalid",
		instanceRepo: instance, floor: composed.Floor{Ref: "refs/heads/main", Dir: "policy/instance"}}
	g, err := scratchRepo(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = policyCap(context.Background(), g, c, tip)
	if err == nil || !strings.Contains(err.Error(), "has no refs/heads/main: "+composed.Floorless) {
		t.Fatalf("with no refs/heads/main, the floor read returned %v", err)
	}
}

// Through the host's rendered hook, the restacker principal, which holds
// the restack grant and not the request grant, adds a restack version to
// a change another principal owns: the version keeps the active title
// though its head's subject differs. The hook refuses a version it adds
// under any other title. It also takes a restack report from the
// restacker, built and then pushed, and refuses one from the owner, who
// holds no restack grant.
func TestRenderedHookTakesTheRestackAppend(t *testing.T) {
	hook := os.Getenv(hookEnv)
	if hook == "" {
		t.Skip(hookEnv + " names no rendered hook; restack-unit supplies one")
	}
	withoutIdentity(t)
	f := newFixture(t, hook)
	was := f.readied("alice", "a", "A title unlike its commit's subject")
	f.moveMain()
	t.Setenv("VALLEY_PRINCIPAL", "restacker")
	c := config{repo: f.origin, state: t.TempDir(), principal: "restacker",
		commitName: "The Restacker", commitEmail: "restacker@example.invalid"}
	ctx := context.Background()
	g, err := scratchRepo(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	g.Stderr = &stderr
	_, head := restacked(t, g)
	ref := change.Ref(rebase.Target, idA)
	old := f.at(ref)

	before, err := rebase.ReadRecord(ctx, g, old)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := rebase.NextRecord("kkkk", before, change.Version{
		Head: change.HeadPrefix + head, Title: "A forged title", Branch: "a", Uploader: "restacker",
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := rebase.WriteRecord(ctx, g, forged, "forged", old, head)
	if err != nil {
		t.Fatal(err)
	}
	if w := rebase.PushRecord(ctx, g, idA, old, commit); w.Push != rebase.Refused || f.at(ref) != old {
		t.Fatalf("a restack version under a forged title: %+v", w)
	}
	if !strings.Contains(stderr.String(), "under the title of the active one") {
		t.Errorf("the hook's refusal does not name the title:\n%s", stderr.String())
	}

	p, err := rebase.BuildRestack(ctx, g, rebase.RestackAppend{ID: idA, Head: head, IfActive: was, Principal: "restacker"})
	if err != nil {
		t.Fatal(err)
	}
	if ap := p.Push(ctx, g); ap.Err != nil || ap.Version != 2 || f.at(ref) != p.Commit {
		t.Fatalf("the restack append: %+v\n%s", ap, stderr.String())
	}
	if err := rebase.Fetch(ctx, g, f.at(ref)); err != nil {
		t.Fatal(err)
	}
	r, err := rebase.ReadRecord(ctx, g, f.at(ref))
	if err != nil {
		t.Fatal(err)
	}
	if v := r.ActiveVersion(); v.Title != "A title unlike its commit's subject" || v.Uploader != "restacker" || v.Branch != "a" || r.Owner != "alice" {
		t.Errorf("the record on origin is %+v", r)
	}

	read, tip, record := change.HeadPrefix+was, change.HeadPrefix+head, change.HeadPrefix+p.Commit
	report := &change.RestackReport{
		Schema: change.RestackSchema, ChangeID: idA, Target: rebase.Target,
		TargetTip: change.HeadPrefix + f.at("refs/heads/main"), Operation: "test-1", Result: change.RestackComplete,
		Layers: []change.RestackLayer{{
			ChangeID: idA, Record: change.HeadPrefix + old, Version: 1, Head: change.HeadPrefix + was,
			Branch: "a", BranchTip: &read, Published: &change.RestackPublished{Head: tip, Version: &p.Version, Record: &record},
		}},
	}
	alice := f.as("alice")
	alice.Stderr = &stderr
	byAlice, err := rebase.WriteReport(ctx, alice, report, rebase.ReportMessage("kkkk", report), "")
	if err != nil {
		t.Fatal(err)
	}
	if w := rebase.PushReport(ctx, alice, idA, "", byAlice); w.Push != rebase.Refused {
		t.Errorf("a report from a principal with no restack grant: %+v", w)
	}
	byRestacker, err := rebase.WriteReport(ctx, g, report, rebase.ReportMessage("kkkk", report), "")
	if err != nil {
		t.Fatal(err)
	}
	if w := rebase.PushReport(ctx, g, idA, "", byRestacker); w.Push != rebase.Pushed || f.at(rebase.RestackPrefix+idA) != byRestacker {
		t.Errorf("a report from the restacker: %+v\n%s", w, stderr.String())
	}
}
