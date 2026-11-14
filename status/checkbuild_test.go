package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/change"
)

const rigTree = "71ee"

// buildRig is a worktree whose origin is a bare repository, and the
// command's work in it, with the evidence reads and the worker stream the
// test's: each read takes the next of reads, and the stream plays follows.
type buildRig struct {
	t                    *testing.T
	origin, work, gitDir string
	base, head           string
	a                    *asking
	out, errs, states    bytes.Buffer
	reads                []fakeRead
	read                 int
	stream               *fakeStream
}

// fakeRead is one read of origin's evidence: the checks it covers, those
// whose evidence does not count, or an error.
type fakeRead struct {
	covered, mismatched []string
	err                 error
}

func newBuildRig(t *testing.T, nix ...string) *buildRig {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	r := &buildRig{t: t, origin: filepath.Join(dir, "origin.git"), work: filepath.Join(dir, "work")}
	r.git(dir, "init", "--quiet", "--bare", "--initial-branch=main", r.origin)
	r.git(dir, "clone", "--quiet", r.origin, r.work)
	r.git(r.work, "config", "user.name", "build-test")
	r.git(r.work, "config", "user.email", "build-test@localhost")
	r.git(r.work, "config", "valley.principal", "alice")
	r.git(r.work, "commit", "--quiet", "--allow-empty", "-m", "base")
	r.base = r.git(r.work, "rev-parse", "HEAD")
	r.git(r.work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	r.git(r.work, "commit", "--quiet", "--allow-empty", "-m", "draft")
	r.head = r.git(r.work, "rev-parse", "HEAD")
	r.gitDir = r.git(r.work, "rev-parse", "--absolute-git-dir")
	r.stream = &fakeStream{t: t}

	a := newAsking(git{env: gitEnvironment(os.Environ()), stderr: &r.errs, dir: r.work}, &r.out, &r.errs)
	a.states, a.readEvery = &r.states, time.Hour
	a.derive = func() error {
		a.head, a.short, a.project = r.head, r.head[:7], "app"
		a.required = len(nix)
		for _, name := range nix {
			a.nix = append(a.nix, nixCheck{name, name})
		}
		return nil
	}
	a.cover = func([]nixCheck) (evidence, error) {
		f := r.reads[min(r.read, len(r.reads)-1)]
		r.read++
		e := evidence{digest: rigTree, covered: map[string]string{}, failed: map[string]string{}}
		for _, name := range f.covered {
			e.covered[name] = "ref worker"
		}
		for _, name := range f.mismatched {
			e.failed[name] = "ref worker"
		}
		return e, f.err
	}
	a.stream = r.stream
	r.a = a
	return r
}

func (r *buildRig) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// requests is every check request origin holds, as "<ref> <commit>".
func (r *buildRig) requests() string {
	return r.git(r.origin, "for-each-ref", "--format=%(refname) %(objectname)", change.CheckRequestPrefix)
}

// run runs the work and checks its exit status.
func (r *buildRig) run(want int) {
	r.t.Helper()
	if got := r.a.run(); got != want {
		r.t.Errorf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", got, want, r.out.String(), r.errs.String())
	}
}

func (r *buildRig) says(out *bytes.Buffer, lines ...string) {
	r.t.Helper()
	for _, line := range lines {
		if !strings.Contains(out.String(), line) {
			r.t.Errorf("output lacks %q:\n%s", line, out.String())
		}
	}
}

func (r *buildRig) saysNot(out *bytes.Buffer, lines ...string) {
	r.t.Helper()
	for _, line := range lines {
		if strings.Contains(out.String(), line) {
			r.t.Errorf("output holds %q:\n%s", line, out.String())
		}
	}
}

// fakeStream plays each follow in turn: before, then its records, then
// its cursor or err. Past the last follow it waits for the follow's end.
type fakeStream struct {
	t       *testing.T
	endErr  error
	follows []fakeFollow
	ended   int
	tails   map[[2]string]string
}

type fakeFollow struct {
	before  func()
	records []workerRecord
	err     error
}

// end is the cursor before the filing, seq 1, and then the mark after
// it, seq 10.
func (s *fakeStream) end(context.Context) (string, error) {
	s.ended++
	if s.ended == 1 {
		return "worker=0123456789abcdef:1", s.endErr
	}
	return "worker=0123456789abcdef:10", s.endErr
}

func (s *fakeStream) follow(ctx context.Context, cursor string, seconds int, records chan<- workerRecord) (string, error) {
	if len(s.follows) == 0 {
		<-ctx.Done()
		return "", ctx.Err()
	}
	f := s.follows[0]
	s.follows = s.follows[1:]
	if f.before != nil {
		f.before()
	}
	for _, r := range f.records {
		select {
		case records <- r:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return cursor, f.err
}

func (s *fakeStream) logs(context.Context) (map[[2]string]string, error) { return s.tails, nil }

func record(typ, run, tree string, data map[string]any) workerRecord {
	return workerRecord{Type: typ, Run: run, Project: "app", Tree: tree, Data: data}
}

func treeBatchEnd(result string) workerRecord {
	return record("batch.end", "p1", rigTree, map[string]any{"attest_run": "attest-1", "result": result})
}

func published() workerRecord {
	return record("publish.end", "p1", rigTree, map[string]any{"result": "passed"})
}

func TestCheckBuildWaitFlag(t *testing.T) {
	for _, c := range []struct {
		args []string
		want time.Duration
		bad  bool
	}{
		{args: []string{"--build"}, want: 2 * time.Hour},
		{args: []string{"--build", "--wait", "30m"}, want: 30 * time.Minute},
		{args: []string{"--wait=45s", "--build"}, want: 45 * time.Second},
		{args: []string{"--build", "--wait", "0s"}, bad: true},
		{args: []string{"--build", "--names"}, bad: true},
	} {
		got, err := buildWait(c.args)
		if (err != nil) != c.bad || !c.bad && got != c.want {
			t.Errorf("%q: %v, %v", c.args, got, err)
		}
	}
}

// One complete first read passes without filing or reading the worker.
func TestCheckBuildPassesWithoutFiling(t *testing.T) {
	r := newBuildRig(t, "fast", "slow")
	r.reads = []fakeRead{{covered: []string{"fast", "slow"}}}
	r.run(0)
	r.says(&r.out, "✓ "+r.head[:7]+"  2 checks passed")
	if r.stream.ended != 0 || r.requests() != "" {
		t.Errorf("the stream was read, or a request filed: %q", r.requests())
	}
}

// Only the last read decides: reads that together covered every check do
// not pass when the last read fails. The request is deleted.
func TestCheckBuildLastReadDecides(t *testing.T) {
	r := newBuildRig(t, "fast", "slow")
	r.a.readEvery = time.Nanosecond
	r.reads = []fakeRead{{covered: []string{"slow"}}, {covered: []string{"fast"}}, {err: errors.New("origin did not answer")}}
	r.stream.follows = []fakeFollow{{}, {records: []workerRecord{published()}}}
	r.run(1)
	r.says(&r.out, "not established · the worker published for this tree · evidence unreadable")
	r.saysNot(&r.out, "passed")
	if r.read != 3 || r.requests() != "" {
		t.Errorf("%d reads; origin holds %q", r.read, r.requests())
	}
}

// Evidence that does not count for a check is no evidence for it, never
// a failure: the request is filed, and the check has no evidence.
func TestCheckBuildMismatchIsNoEvidence(t *testing.T) {
	r := newBuildRig(t, "fast", "slow")
	r.reads = []fakeRead{{covered: []string{"fast"}, mismatched: []string{"slow"}}}
	r.stream.follows = []fakeFollow{{records: []workerRecord{published()}}}
	r.run(1)
	r.says(&r.out, "◇ slow  no evidence on origin", "not established", "1 of 2 with evidence")
	r.saysNot(&r.out, "fail", "✗")
	r.says(&r.states, "filing ")
}

// A pass.end ends the wait only after its pass.start came after the
// filing: a pass that started before may not have read the request. A
// batch that passed is no wake-up either: its evidence is on origin only
// once it publishes.
func TestCheckBuildPassEndNeedsItsStart(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	waited := false
	r.stream.follows = []fakeFollow{
		{records: []workerRecord{{Seq: 11, Type: "pass.end", Run: "p0"}, treeBatchEnd("passed"),
			{Seq: 5, Type: "pass.start", Run: "p2"}, {Seq: 12, Type: "pass.end", Run: "p2"}}},
		{before: func() { waited = r.read == 1 }, records: []workerRecord{{Seq: 21, Type: "pass.start", Run: "p1"}, {Seq: 22, Type: "pass.end", Run: "p1"}}},
	}
	r.run(1)
	r.says(&r.out, "not established · a worker pass ended")
	if !waited {
		t.Error("the wait ended at a pass.end whose start it did not see")
	}
}

// Each wake-up ends the wait in the last read; with every check covered
// there, it passes.
func TestCheckBuildWakes(t *testing.T) {
	for _, c := range []struct {
		name   string
		follow fakeFollow
		endErr error
		last   []string
		status int
		says   string
	}{
		{name: "a publish for the tree ends", follow: fakeFollow{records: []workerRecord{published()}},
			last: []string{"fast"}, status: 0, says: "1 check passed"},
		{name: "a quick worker's batch fails before the mark", follow: fakeFollow{records: []workerRecord{
			func() workerRecord { r := treeBatchEnd("failed"); r.Seq = 5; return r }()}},
			status: 1, says: "not established · the worker's batch for this tree ended with failures"},
		{name: "a producer lost records", follow: fakeFollow{records: []workerRecord{{Type: "check.output", Data: map[string]any{"dropped_before": float64(2)}}}},
			status: 1, says: "not established · telemetry lost records"},
		{name: "the follow fails", follow: fakeFollow{err: errors.New("the host refused")}, status: 1, says: "not established · telemetry unreadable"},
		{name: "the stream cannot be read", endErr: errors.New("no observe grant"), status: 1, says: "not established · telemetry unreadable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newBuildRig(t, "fast")
			r.reads = []fakeRead{{}, {covered: c.last}}
			r.stream.endErr, r.stream.follows = c.endErr, []fakeFollow{c.follow}
			r.run(c.status)
			r.says(&r.out, c.says)
			if r.requests() != "" || r.read != 2 {
				t.Errorf("origin holds %q; %d reads, want the first and the last", r.requests(), r.read)
			}
		})
	}
}

// What the worker wrote for the tree is printed as its records, not this
// request's result: each check.result, a failed check's build output tied
// to it by attest run, untrusted, escaped and cut to its last lines, and
// for a failed check with no output the command that builds it here.
func TestCheckBuildReportsTheTreesRecords(t *testing.T) {
	r := newBuildRig(t, "fast", "slow", "lint")
	r.reads = []fakeRead{{}}
	result := func(check, res string, ms float64) workerRecord {
		rec := record("check.result", "attest-1", rigTree, map[string]any{"result": res, "check_ms": ms})
		rec.Check = check
		return rec
	}
	other := result("fast", "failed", 1)
	other.Tree = "0the4"
	r.stream.follows = []fakeFollow{{records: []workerRecord{
		record("batch.start", "p1", rigTree, map[string]any{"attest_run": "attest-1"}),
		other, result("fast", "passed", 1000), result("slow", "failed", 2000), result("lint", "failed", 3000),
		treeBatchEnd("failed"),
	}}}
	var tail strings.Builder
	for i := 1; i <= 70; i++ {
		fmt.Fprintf(&tail, "line %d\n", i)
	}
	tail.WriteString("said \x1b[31mred\n")
	r.stream.tails = map[[2]string]string{{"attest-1", "slow"}: tail.String(), {"attest-0", "lint"}: "another run's output"}
	r.run(1)
	r.says(&r.out, "not established · the worker's batch for this tree ended with failures",
		"the worker's recent records for this tree, not this request's result:",
		"    fast  passed · 1s", "    slow  failed · 2s",
		"slow · the worker's build output, untrusted, its last 60 lines:", "    | line 70", `said \x1b[31mred`,
		"run: nix build --no-link -L .#checks."+buildSystem()+".lint",
		"see: valley telemetry read --source worker --logs")
	r.saysNot(&r.out, "| line 11\n", "another run", "fast  failed", "\x1b[31m")
}

// A push that dies in transport leaves the request's existence unknown:
// the leased delete is tried, and printed when origin cannot confirm it.
func TestCheckBuildPushDiesInTransport(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	hook := "#!/bin/sh\nkill -9 $PPID\n"
	if err := os.WriteFile(filepath.Join(r.origin, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	r.run(1)
	r.says(&r.errs, "push did not finish", "is not established", "run: git push --force-with-lease="+r.a.ref+":"+r.head+" origin :"+r.a.ref)
	r.says(&r.out, "not established · the request's push did not finish")
	r.saysNot(&r.errs, "refused", "not filed")
}

// A push origin refuses files nothing, and leaves nothing to delete.
func TestCheckBuildRefused(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	hook := "#!/bin/sh\necho 'not this one' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(r.origin, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	r.run(1)
	r.says(&r.errs, "origin refused the check request: not this one")
	r.saysNot(&r.errs, "is not established")
}

// The worktree's token is read or made, and its earlier requests deleted,
// only under its lock: while another holds it, the command makes no token
// and deletes nothing.
func TestCheckBuildTokenAndRecoveryUnderTheLock(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	lock, err := os.OpenFile(filepath.Join(r.gitDir, requestLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	r.run(1)
	r.says(&r.errs, "another valley checks --build is running in this worktree")
	if _, err := os.Stat(filepath.Join(r.gitDir, worktreeTokenFile)); err == nil {
		t.Error("a token was made without the lock")
	}

	r = newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	token := "0123456789abcdef"
	os.WriteFile(filepath.Join(r.gitDir, worktreeTokenFile), []byte(token+"\n"), 0o600)
	earlier, _ := change.CheckRequestRef("alice", token, "00000000000000aa")
	r.git(r.work, "push", "--quiet", "origin", "HEAD:"+earlier)
	lock, err = os.OpenFile(filepath.Join(r.gitDir, requestLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	r.run(1)
	if got := r.requests(); got != earlier+" "+r.head {
		t.Errorf("origin holds %q, want the earlier request untouched", got)
	}
}

// With the lock, the worktree's earlier requests go, each leased, and
// another worktree's stays.
func TestCheckBuildRecoversEarlierRequests(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}, {covered: []string{"fast"}}}
	token := "0123456789abcdef"
	os.WriteFile(filepath.Join(r.gitDir, worktreeTokenFile), []byte(token+"\n"), 0o600)
	earlier, _ := change.CheckRequestRef("alice", token, "00000000000000aa")
	other, _ := change.CheckRequestRef("alice", "fedcba9876543210", "00000000000000bb")
	r.git(r.work, "push", "--quiet", "origin", "HEAD:"+earlier, "HEAD:"+other)
	r.stream.follows = []fakeFollow{{before: func() {
		if got := r.requests(); got != other+" "+r.head+"\n"+r.a.ref+" "+r.head && got != r.a.ref+" "+r.head+"\n"+other+" "+r.head {
			t.Errorf("while waiting, origin holds %q", got)
		}
	}, records: []workerRecord{published()}}}
	r.run(0)
	if got := r.requests(); got != other+" "+r.head {
		t.Errorf("origin holds %q, want only the other worktree's", got)
	}
}

// The deadline kills every program the command started, here the git
// listing coverageOf runs to read origin's evidence, hung in its
// transport, and the outcome is not established. The command is the
// real one, run by the watchdog as a child: it derives the checks under a
// floor, fetches main, and reads the evidence through coverageOf.
func TestCheckBuildDeadline(t *testing.T) {
	if _, err := exec.LookPath("cue"); err != nil {
		if os.Getenv("VALLEY_TEST_TOOLS") == "required" {
			t.Fatal("no cue on PATH, and VALLEY_TEST_TOOLS=required")
		}
		t.Skip("no cue to derive the checks with; the nix check provides one")
	}
	schema, err := filepath.Abs(filepath.Join("..", "schema", "verification.cue"))
	if err != nil {
		t.Fatal(err)
	}
	self, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_VERIFICATION_SCHEMA", schema)
	r := &buildRig{t: t, origin: filepath.Join(dir, "origin.git"), work: filepath.Join(dir, "work")}
	r.git(dir, "init", "--quiet", "--bare", "--initial-branch=main", r.origin)
	r.git(dir, "clone", "--quiet", r.origin, r.work)
	for _, kv := range [][2]string{{"user.name", "build-test"}, {"user.email", "build-test@localhost"}, {"valley.principal", "alice"}} {
		r.git(r.work, "config", kv[0], kv[1])
	}
	write := func(path, body string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(r.work, "policy", "instance", "floor.cue"), "package verification\nfloor: {\n"+
		"\tchecks: \"c-docs\": {runner: \"nix\", attribute: \"c-docs\"}\n\tunclassified: \"c-docs\": true\n}\n")
	write(filepath.Join(r.work, "docs", "a.md"), "base\n")
	r.git(r.work, "add", "-A")
	r.git(r.work, "commit", "--quiet", "-m", "base")
	r.git(r.work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	write(filepath.Join(r.work, "docs", "b.md"), "draft\n")
	r.git(r.work, "add", "-A")
	r.git(r.work, "commit", "--quiet", "-m", "draft")
	r.git(r.work, "remote", "set-url", "origin", "ssh://valley-host"+r.origin)

	// ssh and attest are this test binary (TestMain): ssh serves origin
	// for the first upload-pack, main's fetch, and hangs at the next, the
	// listing; attest digests any tree.
	bin, fake := filepath.Join(dir, "bin"), filepath.Join(dir, "fake")
	os.Mkdir(bin, 0o755)
	os.Mkdir(fake, 0o755)
	for _, name := range []string{"ssh", "attest"} {
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SSH_UPLOAD", fake)
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	t.Chdir(r.work)

	var out, errs bytes.Buffer
	began := time.Now()
	wait := 6 * time.Second
	status := watch(git{env: gitEnvironment(os.Environ()), stderr: &errs}, self, wait, &out, &errs, make(chan os.Signal))
	if status != 1 || !strings.Contains(out.String(), "not established · time limit") {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", status, out.String(), errs.String())
	}
	if took := time.Since(began); took > wait+cleanupBound {
		t.Errorf("the command took %s", took)
	}
	pid, err := os.ReadFile(filepath.Join(fake, "hung.pid"))
	if err != nil {
		t.Fatalf("the listing never reached origin: %v\nstderr:\n%s", err, errs.String())
	}
	if stat, err := os.ReadFile("/proc/" + strings.TrimSpace(string(pid)) + "/stat"); err == nil {
		if f := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:])); len(f) > 0 && f[0] != "Z" {
			t.Errorf("the hung listing outlived the command: %s", stat)
		}
	}
}

// fakeUpload is ssh for TestCheckBuildDeadline: it serves the repository
// an upload-pack names for the first one, and hangs at every later one.
func fakeUpload(dir string, args []string) int {
	path, ok := strings.CutPrefix(args[len(args)-1], "git-upload-pack ")
	if !ok {
		return 0
	}
	seen, _ := os.ReadFile(filepath.Join(dir, "uploads"))
	os.WriteFile(filepath.Join(dir, "uploads"), append(seen, '.'), 0o600)
	if len(seen) == 0 {
		gitPath, err := exec.LookPath("git")
		if err != nil {
			return 255
		}
		syscall.Exec(gitPath, []string{"git", "upload-pack", strings.Trim(path, "'")}, os.Environ())
		return 255
	}
	os.WriteFile(filepath.Join(dir, "hung.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	time.Sleep(time.Hour)
	return 255
}

// fakeAttest is attest for TestCheckBuildDeadline: every tree has one
// digest.
func fakeAttest() int {
	fmt.Println("valley-tree-v1:" + strings.Repeat("ab", 32))
	return 0
}

// No check required passes at once, with nothing filed.
func TestCheckBuildNoRequiredChecks(t *testing.T) {
	r := newBuildRig(t)
	r.run(0)
	r.says(&r.out, "no checks required")
	if r.stream.ended != 0 || r.requests() != "" {
		t.Errorf("the stream was read, or a request filed: %q", r.requests())
	}
}

// A telemetry record cannot speak for the child: one whose fields are not
// a required check, a known result and a run name is dropped, and no value
// holding a newline or space reaches the state pipe, so a crafted result
// cannot tell the parent the request is settled.
func TestCheckBuildStateCannotBeInjected(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.a.derive()
	r.a.tree = rigTree
	for _, c := range []struct{ check, result, run string }{
		{"fast", "failed\nsettled\n", "attest-1"},
		{"fast\nsettled", "failed", "attest-1"},
		{"lint", "failed", "attest-1"},
		{"fast", "failed", "attest-1\nsettled"},
	} {
		rec := record("check.result", c.run, rigTree, map[string]any{"result": c.result})
		rec.Check = c.check
		r.a.wake(rec, map[string]bool{}, map[string]bool{})
	}
	r.a.state("filing", "refs/x", "abc")
	r.a.state("result", "x\nsettled")
	raw := r.states.String()
	var s childState
	s.read(&r.states)
	if len(r.a.results) != 0 || s.settled || raw != "filing refs/x abc\n" {
		t.Errorf("results %v, state pipe %q", r.a.results, raw)
	}
}

// Names and results print as one escaped line each.
func TestCheckBuildReportEscapes(t *testing.T) {
	var out bytes.Buffer
	report(&out, []treeResult{{"attest-1", "fa\x1b[31mst\nsettled", "failed", 1000}}, nil, nil)
	if !strings.Contains(out.String(), `    fa\x1b[31mst\nsettled  failed · 1s`) {
		t.Errorf("%q", out.String())
	}
}

// Only a line rejecting the request's own ref is a rejection; a remote that
// failed to report status, or another ref's rejection, leaves it unknown.
func TestPushStatus(t *testing.T) {
	ref := "refs/the-valley/check-requests/alice/0123456789abcdef/0123456789abcdef"
	for _, c := range []struct{ out, want string }{
		{"To origin\n!\tabc:" + ref + "\t[remote rejected] (pre-receive hook declined)\nDone\n", "rejected"},
		{"!\tabc:" + ref + "\t[rejected] (stale info)\n", "rejected"},
		{"!\tabc:" + ref + "\t[remote failure] (remote failed to report status)\n", "unknown"},
		{"!\tabc:refs/heads/other\t[remote rejected] (no)\n", "unknown"},
		{"", "unknown"},
	} {
		if got := pushStatus(c.out, ref); got != c.want {
			t.Errorf("%q: %s, want %s", c.out, got, c.want)
		}
	}
}

// The parent exits 0 only when the child reported a pass: a child that
// exits 0 without one is no pass.
func TestCheckBuildFailsClosed(t *testing.T) {
	self, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true on PATH")
	}
	var out, errs bytes.Buffer
	if status := watch(git{env: os.Environ()}, self, time.Minute, &out, &errs, make(chan os.Signal)); status != 1 ||
		!strings.Contains(errs.String(), "ended without an outcome") {
		t.Errorf("exit %d\n%s%s", status, out.String(), errs.String())
	}
}

// A token that does not read is reported, never replaced.
func TestCheckBuildKeepsAMalformedToken(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	path := filepath.Join(r.gitDir, worktreeTokenFile)
	os.WriteFile(path, []byte("not a token\n"), 0o600)
	r.run(1)
	r.says(&r.errs, "holds no check request token")
	if data, _ := os.ReadFile(path); string(data) != "not a token\n" || r.requests() != "" {
		t.Errorf("the token is %q; origin holds %q", data, r.requests())
	}
}

// An earlier request whose delete the child started and did not end is the
// parent's to delete, leased; one the child cleared is not.
func TestCheckBuildParentDeletesAStaleTarget(t *testing.T) {
	r := newBuildRig(t, "fast")
	cut, _ := change.CheckRequestRef("alice", "0123456789abcdef", "00000000000000aa")
	done, _ := change.CheckRequestRef("alice", "0123456789abcdef", "00000000000000bb")
	r.git(r.work, "push", "--quiet", "origin", "HEAD:"+cut, "HEAD:"+done)
	var s childState
	s.read(strings.NewReader("stale " + cut + " " + r.head + "\nstale " + done + " " + r.head + "\ncleared " + done + "\n"))
	s.settle(git{env: gitEnvironment(os.Environ()), dir: r.work}, &r.errs, make(chan os.Signal))
	if got := r.requests(); got != done+" "+r.head {
		t.Errorf("origin holds %q, want only the cleared one", got)
	}
}

// A diagnostic from the host prints as one escaped line, so it cannot
// forge a verdict line of its own.
func TestCheckBuildDiagnosticsStayOnOneLine(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}}
	r.stream.endErr = errors.New("denied\n✓ HEAD  1 check passed")
	r.run(1)
	r.says(&r.errs, `denied\n✓ HEAD  1 check passed`)
	if strings.Contains(r.errs.String()+r.out.String(), "\n✓ HEAD") {
		t.Errorf("a diagnostic broke its line:\n%s%s", r.out.String(), r.errs.String())
	}
}

// A listed ref under the worktree's prefix that is no check request is
// skipped, and never reaches the state pipe, however long.
func TestCheckBuildSkipsAMalformedStaleRef(t *testing.T) {
	r := newBuildRig(t, "fast")
	r.reads = []fakeRead{{}, {covered: []string{"fast"}}}
	token := "0123456789abcdef"
	os.WriteFile(filepath.Join(r.gitDir, worktreeTokenFile), []byte(token+"\n"), 0o600)
	long := change.CheckRequestPrefix + "alice/" + token + "/" + strings.Repeat("a", 60000)
	if err := os.WriteFile(filepath.Join(r.origin, "packed-refs"), []byte(r.head+" "+long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.stream.follows = []fakeFollow{{records: []workerRecord{published()}}}
	r.run(0)
	r.says(&r.errs, "is no check request, so it is skipped")
	for _, line := range strings.Split(r.states.String(), "\n") {
		if len(line) > 1000 {
			t.Errorf("a %d-byte state line reached the pipe", len(line))
		}
	}
}
