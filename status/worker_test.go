package main

import (
	"cmp"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/telemetry"
)

// A pass prunes exactly the roots no batch has used for longer than the
// age it keeps outputs for, and nothing in the directory that is not a
// root symlink.
func TestPruneRootsRemovesExactlyTheRootsOlderThanTheAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	keep := 7 * 24 * time.Hour
	used := map[string]time.Time{
		"aaaa-stale":     now.Add(-keep - time.Hour),
		"bbbb-dangling":  now.Add(-30 * 24 * time.Hour),
		"cccc-recent":    now.Add(-keep + time.Hour),
		"dddd-today":     now,
		"eeee-not-alink": now.Add(-30 * 24 * time.Hour),
	}
	for name, at := range used {
		path := filepath.Join(dir, name)
		if name == "eeee-not-alink" {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink("/nix/store/"+name, path); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("touch", "-h", "-t", at.Local().Format("200601021504.05"), path).CombinedOutput(); err != nil {
			t.Fatalf("touch %s: %v: %s", path, err, out)
		}
	}

	if err := pruneRoots(dir, keep, now); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	sort.Strings(left)
	want := []string{"cccc-recent", "dddd-today", "eeee-not-alink"}
	if !slices.Equal(left, want) {
		t.Errorf("pruning left %v, want %v", left, want)
	}

	if err := pruneRoots(filepath.Join(dir, "missing"), keep, now); err != nil {
		t.Errorf("pruning a roots directory that does not exist yet: %v", err)
	}
}

// testJob is a job in a project, for a change's version first seen in a
// scan, whose batch would store ref.
func testJob(project, id string, seen int, ref, key string) *job {
	return &job{r: listed{source: source{name: project}}, c: jsonChange{ChangeID: id, Handle: id},
		v: jsonVersion{N: 1, Head: "0123456789"}, seen: seen, ref: ref, key: key}
}

// A scan's jobs start in the order their versions were first seen ready,
// then by project, then by change ID, and one job holds each destination:
// a later job for the same project and ref is the same tree, and is
// dropped. The same ref in another project is another destination.
func TestOrderIsFirstSeenThenProjectThenChangeAndOneJobPerDestination(t *testing.T) {
	w := &worker{said: map[string]map[string]bool{}, stdout: io.Discard}
	a := testJob("app", "m", 1, "refs/one", "")
	b := testJob("app", "l", 1, "refs/two", "")
	c := testJob("site", "a", 1, "refs/one", "")
	d := testJob("app", "b", 2, "refs/two", "")
	e := testJob("app", "c", 3, "refs/one", "")
	f := testJob("app", "d", 2, "refs/three", "")
	got := w.order([]*job{e, d, c, a, f, b})
	want := []*job{b, a, c, f}
	if !slices.Equal(got, want) {
		var names []string
		for _, j := range got {
			names = append(names, j.r.name+"/"+j.c.ChangeID)
		}
		t.Errorf("the queue is %v, want app/l app/m site/a app/d", names)
	}
}

// A scan that read a project whole forgets the failures it did not meet,
// except the key of a batch still running there, whose failure is
// recorded once it ends. Other projects' failures are kept.
func TestPruneKeepsTheKeysOfRunningBatches(t *testing.T) {
	record := &failureRecord{Projects: map[string]map[string]backoff{
		"app":  {"build one a": {Failures: 1}, "build two b": {Failures: 1}, "push 0a1b": {Failures: 2}},
		"site": {"build one a": {Failures: 1}},
	}}
	running := testJob("app", "x", 1, "refs/one", "build one a")
	w := &worker{failures: record, running: map[string]*batch{running.dest(): {job: running}}}
	w.prune("app", map[string]bool{"push 0a1b": true}, false)
	var got []string
	for key := range record.Projects["app"] {
		got = append(got, key)
	}
	sort.Strings(got)
	if !slices.Equal(got, []string{"build one a", "push 0a1b"}) {
		t.Errorf("app keeps %v, want the running batch's key and the push the scan met", got)
	}
	if len(record.Projects["site"]) != 1 {
		t.Errorf("pruning app changed site's failures: %v", record.Projects["site"])
	}
}

// A pass reads neither the policy nor main where an earlier pass found no
// follow run to start on the same tip, until main moves or the follow
// run's wait is over. The record outlives the pass that wrote it.
func TestQuietMainHoldsUntilMainMovesOrTheWaitEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.json")
	failures := &failureRecord{Projects: map[string]map[string]backoff{}}
	failures.failed("app", "build t1 e2e", clock(), time.Hour)
	failures.failed("app", "build t1 old", clock().Add(-2*time.Hour), time.Hour)
	q := readQuiet(path, io.Discard)
	for _, m := range []struct{ project, tip, wait string }{
		{"app", "c1", ""}, {"site", "c1", "build t1 e2e"}, {"web", "c1", "build t1 old"},
	} {
		if err := q.set(m.project, quietMain{Tip: m.tip, Wait: m.wait}); err != nil {
			t.Fatal(err)
		}
	}
	failures.Projects["site"], failures.Projects["web"] = failures.Projects["app"], failures.Projects["app"]
	q = readQuiet(path, io.Discard)
	for _, tc := range []struct {
		project, tip string
		want         bool
	}{
		{"app", "c1", true}, {"app", "c2", false}, {"site", "c1", true}, {"web", "c1", false}, {"other", "c1", false},
	} {
		if got := q.holds(tc.project, tc.tip, failures); got != tc.want {
			t.Errorf("%s at %s: quiet %v, want %v", tc.project, tc.tip, got, tc.want)
		}
	}
}

// The time spans the host module passes as --interval read as systemd
// reads a timer's, and the ones systemd refuses are refused.
func TestTimeSpanReadsSystemdSpans(t *testing.T) {
	month, year := 2629800*time.Second, 31557600*time.Second
	for in, want := range map[string]time.Duration{
		"1m": time.Minute, "15s": 15 * time.Second, "5min": 5 * time.Minute, "1h 30min": 90 * time.Minute,
		"1m30s": 90 * time.Second, "90": 90 * time.Second, "2 days": 48 * time.Hour, "1.5h": 90 * time.Minute,
		"0": 0, " 2months": 2 * month, "1y 1M": year + month, "1 year": year, "3weeks": 21 * 24 * time.Hour,
		"5ms": 5 * time.Millisecond, "7\u00b5s": 7 * time.Microsecond, "7\u03bcs": 7 * time.Microsecond,
		".5s": 500 * time.Millisecond, "1.0000005s": time.Second, "5 3": 8 * time.Second,
		"infinity": math.MaxInt64, " infinity ": math.MaxInt64,
		// Longer than a time.Duration holds, but not than systemd reads.
		"300y": math.MaxInt64, "9223372036854775807us": math.MaxInt64,
	} {
		if got, err := timeSpan(in); err != nil || got != want {
			t.Errorf("timeSpan(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", " ", "m", "5 fortnights", "-1m", "1m x", "5mins", "12.34.56", "3.s", "1.", "+5s", "infinity 1s", "1 infinity",
		// As many microseconds as systemd's infinity, or more.
		"18446744073709551615us", "9223372036854775808", "584543y", "500000y 500000y",
	} {
		if got, err := timeSpan(in); err == nil {
			t.Errorf("timeSpan(%q) = %v, want an error", in, got)
		}
	}
}

// A read of origin that fails while two batches run stops the pass from
// starting anything more: the job queued behind them never starts, and no
// read follows. The loop still takes both batches' ends, which come after
// the failed read began, and records each one's failure and wait, with its
// attest run. The pass then fails with the read's error, and leaves its
// claim, including the mark it claimed for that read, unacknowledged.
func TestAFailedReadStopsAdmissionAndDrainsTheRunningBatches(t *testing.T) {
	state, wakeDir, telDir := t.TempDir(), t.TempDir(), t.TempDir()
	mark := func() {
		if err := os.WriteFile(filepath.Join(wakeDir, "due"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mark()
	claim := claimWake(wakeDir, io.Discard)
	defer claim.close()
	// The emitter drops a record it cannot store within its deadline, and
	// every record emitted while that store is still under way, so on a
	// loaded machine the records below would be missing. Each record is
	// waited for instead.
	tel := openWorkerTelemetry(telDir)
	tel.tel.SetDeadline(time.Minute)
	w := &worker{batches: 2, interval: time.Millisecond, wake: claim, stdout: io.Discard, stderr: io.Discard,
		seen: map[string]int{}, running: map[string]*batch{}, said: map[string]map[string]bool{},
		tel: tel, failures: readFailures(filepath.Join(state, "failures.json"), io.Discard)}
	jobs := []*job{testJob("app", "a", 1, "refs/a", "build a"), testJob("app", "b", 1, "refs/b", "build b"),
		testJob("app", "c", 1, "refs/c", "build c")}
	var launched []*batch
	reads := 0
	w.read = func(git, string) (projectRead, error) {
		reads++
		if reads == 1 {
			mark() // a ready push while the pass reads
			return projectRead{jobs: jobs}, nil
		}
		// Both batches end while this read is under way, and fail.
		for _, b := range launched {
			go func() { w.done <- completion{b: b, err: errors.New("exit status 1"), ended: time.Now()} }()
		}
		return projectRead{}, failure("app: origin did not answer; the pass stops here")
	}
	w.recheck = func(*job) (bool, error) { return true, nil }
	w.launch = func(b *batch) { launched = append(launched, b) }

	err := w.pass(git{}, []string{"app"})
	if err == nil || err.Error() != "app: origin did not answer; the pass stops here" {
		t.Fatalf("the pass returned %v, want the read's error", err)
	}
	if reads != 2 {
		t.Errorf("the pass read %d times, want 2: none after the read that failed", reads)
	}
	if len(launched) != 2 || launched[0].job != jobs[0] || launched[1].job != jobs[1] {
		t.Errorf("the pass started %d batches, want a's and b's alone", len(launched))
	}
	if len(w.running) != 0 {
		t.Errorf("the pass ended with %d batches running", len(w.running))
	}
	record := readFailures(filepath.Join(state, "failures.json"), io.Discard).Projects["app"]
	for _, key := range []string{"build a", "build b"} {
		if record[key].Failures != 1 {
			t.Errorf("failures.json holds %q as %+v, want one failure", key, record[key])
		}
	}
	if _, err := os.Stat(filepath.Join(wakeDir, "claimed")); err != nil {
		t.Errorf("the claim was acknowledged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wakeDir, "due")); err == nil {
		t.Error("the mark made during the first read was not claimed before the second")
	}
	recs, _, err := (&telemetry.Stream{Dir: w.tel.stream()}).Read(0, 1000, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range launched {
		for _, typ := range []string{"batch.start", "batch.end", "backoff"} {
			n := 0
			for _, r := range recs {
				if r.Type == typ && r.Data["attest_run"] == b.run {
					n++
				}
			}
			if n != 1 {
				t.Errorf("the stream holds %d %s records naming attest run %s, want 1", n, typ, b.run)
			}
		}
	}
}

// The worker reads both layers at main's tip as approve does, and refuses
// a layer that cannot be read rather than reading it as absent. An
// unreadable floor is not sent to the owning valley that policy/valley
// names, though that valley's floor reads, and an unreadable project layer
// is not derived as the floor alone.
func TestPolicyLayersRefuseALayerThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	commit := func(repo string, files map[string]string) string {
		t.Helper()
		run := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
			cmd.Dir = repo
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
			return strings.TrimSpace(string(out))
		}
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		run("init", "--quiet", "--initial-branch=main")
		for path, body := range files {
			full := filepath.Join(repo, path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "--all")
		run("commit", "--quiet", "-m", "layers")
		return run("rev-parse", "HEAD")
	}
	valley := filepath.Join(dir, "valley")
	commit(valley, map[string]string{"policy/instance/floor.cue": "package verification // the valley's\n"})

	for _, lost := range []string{"", instanceLayer, projectLayer, projectLayer + "/policy.cue"} {
		t.Run("lost "+lost, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "app")
			tip := commit(repo, map[string]string{
				instanceLayer + "/floor.cue": "package verification // its own\n",
				projectLayer + "/policy.cue": "package verification // the project's\n",
				valleyPointer:                valley + "\n",
			})
			if lost != "" {
				out, err := exec.Command("git", "-C", repo, "rev-parse", tip+":"+lost).Output()
				if err != nil {
					t.Fatal(err)
				}
				oid := strings.TrimSpace(string(out))
				if err := os.Remove(filepath.Join(repo, ".git", "objects", oid[:2], oid[2:])); err != nil {
					t.Fatal(err)
				}
			}
			instance, project, done, err := policyLayers(git{env: os.Environ(), stderr: io.Discard, dir: repo}, tip)
			defer done()
			if lost == "" {
				if err != nil {
					t.Fatal(err)
				}
				for path, want := range map[string]string{
					filepath.Join(instance, "floor.cue"): "package verification // its own\n",
					filepath.Join(project, "policy.cue"): "package verification // the project's\n",
				} {
					if body, err := os.ReadFile(path); err != nil || string(body) != want {
						t.Errorf("%s reads %q (%v), not %q", path, body, err, want)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("an unreadable %s resolved the layers to %s and %s", lost, instance, project)
			}
			if !strings.Contains(err.Error(), "reading "+lost) {
				t.Errorf("the refusal %q does not say that %s could not be read", err, lost)
			}
		})
	}
}

// world is what one project holds in a scheduler test: its ready
// changes, the head on main a follow run is due on, or "", origin's refs
// under the check-request prefix, the tree of each head, the checks each
// head requires, given, when not the one check "check", the checks whose notes do
// not count, and the error reading a head's evidence ends in, if any. The
// evidence itself is on a scratch origin. The test changes the world
// while the loop runs.
type world struct {
	mu        sync.Mutex
	ready     []testVersion
	follow    string
	refs      map[string]string
	trees     map[string]string
	asks      map[string][]string
	uncounted map[string]bool
	broken    map[string]error
}

// testVersion is a ready change's active version.
type testVersion struct {
	change string
	n      int
	head   string
}

// checks are the nix checks a head requires.
func (wd *world) checks(head string) []nixCheck {
	names, ok := wd.asks[head]
	if !ok {
		names = []string{"check"}
	}
	var checks []nixCheck
	for _, name := range names {
		checks = append(checks, nixCheck{name, name})
	}
	return checks
}

// scheduler runs a worker's loop over the worlds of its projects, with no
// attest, in a clone of a scratch origin that the state directory keeps
// across passes. Each batch the loop starts is sent on launched, and each
// it stops on stops; a batch ends when the test hands the loop its
// completion, having stored or added its notes as attest would. reads
// gets the scan's number after each read of the last project. A request
// batch started while a batch of a higher class ran is kept in wrong.
type scheduler struct {
	t        *testing.T
	w        *worker
	g        git    // the worker's clone
	origin   string // the scratch origin's directory
	worlds   map[string]*world
	last     string
	launched chan *batch
	stops    chan *batch
	reads    chan int
	ended    chan error
	wrong    []string
	assessed []string // the requests assessed, by ref, in order
	during   func()   // run once, at the start of the next read of requests
}

// newScheduler starts a pass over the worlds, running batches at once
// and reading every project again every interval while batches run, or
// every millisecond when interval is 0. The origin refuses a push while
// it holds a file named refuse, counts every push it is offered in
// attempts, and marks the pass's wake directory after each push it takes,
// as a host's hook does.
func newScheduler(t *testing.T, batches int, interval time.Duration, state string, worlds map[string]*world) *scheduler {
	t.Helper()
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	origin, clone := filepath.Join(state, "origin.git"), filepath.Join(state, "clone")
	s := &scheduler{t: t, g: git{env: env, stderr: io.Discard, dir: clone}, origin: origin, worlds: worlds,
		launched: make(chan *batch, 100), stops: make(chan *batch, 100), reads: make(chan int, 100), ended: make(chan error, 1)}
	wakeDir := filepath.Join(state, "wake")
	if _, err := os.Stat(clone); err != nil {
		s.run(git{env: env, dir: state}, "init", "--quiet", "--bare", origin)
		s.run(git{env: env, dir: state}, "init", "--quiet", clone)
		s.run(s.g, "remote", "add", "origin", origin)
		for name, hook := range map[string]string{
			"pre-receive":  "#!/bin/sh\necho attempt >> attempts\ntest ! -e refuse\n",
			"post-receive": "#!/bin/sh\n: > '" + filepath.Join(wakeDir, "due") + "'\n",
		} {
			if err := os.WriteFile(filepath.Join(origin, "hooks", name), []byte(hook), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(wakeDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	claim := claimWake(wakeDir, io.Discard)
	projects := slices.Sorted(maps2keys(worlds))
	s.last = projects[len(projects)-1]
	s.w = &worker{batches: batches, interval: cmp.Or(interval, time.Millisecond), stdout: io.Discard, stderr: io.Discard,
		seen: map[string]int{}, running: map[string]*batch{}, said: map[string]map[string]bool{},
		failures: readFailures(filepath.Join(state, "failures.json"), io.Discard),
		quiet:    readQuiet(filepath.Join(state, "main.json"), io.Discard), keyhash: "deadbeef", wake: claim}
	s.w.read = func(_ git, name string) (projectRead, error) {
		return s.read(listed{source: source{name: name, g: s.g}})
	}
	s.w.cover = s.cover
	s.w.recheck = func(j *job) (bool, error) {
		needed, _, err := s.w.assess(j, nil)
		return needed, err
	}
	s.w.launch = func(b *batch) {
		if b.job.class() == classRequest && s.w.runningAbove(classRequest) {
			s.wrong = append(s.wrong, b.job.version()+" started beside a batch of a higher class")
		}
		b.stop = func() { s.stops <- b }
		s.launched <- b
	}
	go func() {
		err := s.w.loop(git{}, projects)
		claim.close()
		s.ended <- err
	}()
	return s
}

// maps2keys yields a map's keys.
func maps2keys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// run runs git, and fails the test when it fails.
func (s *scheduler) run(g git, args ...string) string {
	out, err := g.output(true, args...)
	if err != nil {
		s.t.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// onOrigin is the object origin's ref names, or "".
func (s *scheduler) onOrigin(ref string) string {
	out, err := git{env: s.g.env, dir: s.origin}.output(true, "rev-parse", "--quiet", "--verify", ref)
	if err != nil {
		return ""
	}
	return out
}

// noted is the checks the notes tree object holds a note for.
func (s *scheduler) noted(object string) []string {
	return strings.Fields(s.run(s.g, "ls-tree", "--name-only", object))
}

// cover reads a job's evidence as coverage does: this worker's ref for
// the head's tree on origin, fetched here, and the checks it holds a
// note for that counts.
func (s *scheduler) cover(j *job) (evidence, error) {
	wd := s.worlds[j.r.name]
	wd.mu.Lock()
	defer wd.mu.Unlock()
	if j.req != "" {
		s.assessed = append(s.assessed, j.req)
	}
	if err := wd.broken[j.v.Head]; err != nil {
		return evidence{}, err
	}
	e := evidence{digest: wd.trees[j.v.Head], listed: map[string]string{}, covered: map[string]string{}}
	ref := attestationRefs + e.digest + "/" + s.w.keyhash
	if object := s.onOrigin(ref); object != "" {
		e.listed[ref] = object
		s.run(s.g, "fetch", "--quiet", "--no-write-fetch-head", "origin", ref)
		for _, name := range s.noted(object) {
			if !wd.uncounted[name] {
				e.covered[name] = ref
			}
		}
	}
	return e, nil
}

// read is project over a world: the ready changes, then the follow run,
// each read only as project reads it, then the stops and the pruning of a
// whole read, and the reader of the check requests, which runs during
// before it assesses any.
func (s *scheduler) read(r listed) (projectRead, error) {
	wd, w := s.worlds[r.name], s.w
	wd.mu.Lock()
	refs, follow := map[string]string{}, wd.follow
	for ref, head := range wd.refs {
		refs[ref] = head
	}
	ready := slices.Clone(wd.ready)
	asks := map[string][]nixCheck{}
	for _, v := range ready {
		asks[v.head] = wd.checks(v.head)
	}
	for _, head := range refs {
		asks[head] = wd.checks(head)
	}
	asks[follow] = wd.checks(follow)
	wd.mu.Unlock()
	if r.name == s.last {
		defer func() { s.reads <- w.scans }()
	}
	listed, pending := w.listRequests(r, refs)
	met := map[string]bool{}
	whole := true
	var jobs []*job
	var changes []jsonChange
	for _, v := range ready {
		c := jsonChange{ChangeID: v.change, Handle: v.change, Active: 1, Versions: []jsonVersion{{N: v.n, Head: v.head}}}
		changes = append(changes, c)
		j := &job{r: r, c: c, v: c.Versions[0], asked: asks[v.head]}
		w.ready[j.version()] = true
		if _, ok := w.seen[j.version()]; !ok {
			w.seen[j.version()] = w.scans
		}
		j.seen = w.seen[j.version()]
		needed, read, err := w.assess(j, met)
		if err != nil {
			return projectRead{}, err
		}
		whole = whole && read
		if needed {
			jobs = append(jobs, j)
		}
	}
	if len(jobs) == 0 && !w.gating() && follow != "" {
		j, read, err := w.followRun(&job{r: r, v: jsonVersion{Head: follow}, main: true, seen: w.scans, asked: asks[follow]}, met)
		if err != nil {
			return projectRead{}, err
		}
		whole = whole && read
		if j != nil {
			jobs = append(jobs, j)
		}
	}
	if whole {
		w.prune(r.name, met, len(listed) > 0)
		w.stopSuperseded(r.name, changes, listed)
	}
	pr := projectRead{jobs: jobs}
	if len(pending) > 0 {
		pr.done = func() {}
		pr.requests = func() ([]*job, error) {
			wd.mu.Lock()
			f := s.during
			s.during = nil
			wd.mu.Unlock()
			if f != nil {
				f()
			}
			var jobs []*job
			for _, q := range pending {
				j := &job{r: r, v: jsonVersion{Head: q.head}, req: q.ref, seen: w.seen[checkRequestName(r.name, q.ref, q.head)], asked: asks[q.head]}
				needed, _, err := w.assess(j, met)
				if err != nil {
					return nil, err
				}
				if needed {
					jobs = append(jobs, j)
				}
			}
			if whole {
				w.prune(r.name, met, false)
			}
			return jobs, nil
		}
	}
	return pr, nil
}

// alter changes a world while the loop runs.
func (s *scheduler) alter(project string, f func(wd *world)) {
	wd := s.worlds[project]
	wd.mu.Lock()
	defer wd.mu.Unlock()
	f(wd)
}

// reread waits for two reads of every project to end, the second of which
// began after the call.
func (s *scheduler) reread() {
	s.t.Helper()
	for range 2 {
		select {
		case <-s.reads:
		case err := <-s.ended:
			s.t.Fatalf("the pass ended (%v) before it read every project again", err)
		case <-time.After(10 * time.Second):
			s.t.Fatal("the pass did not read every project again")
		}
	}
}

// started waits for the next batch to start, and requires it to be for
// want: a change's ID, main, or a request's ref.
func (s *scheduler) started(want string) *batch {
	s.t.Helper()
	select {
	case b := <-s.launched:
		got := cmp.Or(b.job.c.ChangeID, b.job.req)
		if b.job.main {
			got = "main"
		}
		if got != want {
			s.t.Fatalf("the batch for %s started, want %s", got, want)
		}
		return b
	case err := <-s.ended:
		s.t.Fatalf("the pass ended (%v) before %s started", err, want)
	case <-time.After(10 * time.Second):
		s.t.Fatalf("no batch started, want %s", want)
	}
	return nil
}

// stopped waits for the loop to stop b.
func (s *scheduler) stopped(b *batch) {
	s.t.Helper()
	select {
	case got := <-s.stops:
		if got != b {
			s.t.Fatalf("the loop stopped %s, want %s", got.job.version(), b.job.version())
		}
	case <-time.After(10 * time.Second):
		s.t.Fatalf("the loop did not stop %s", b.job.version())
	}
}

// idle requires that no batch started or was stopped since the test last
// looked, after two more reads.
func (s *scheduler) idle(what string) {
	s.t.Helper()
	s.reread()
	select {
	case b := <-s.launched:
		s.t.Fatalf("%s: the batch for %s started", what, b.job.version())
	case b := <-s.stops:
		s.t.Fatalf("%s: the batch for %s was stopped", what, b.job.version())
	default:
	}
}

// end hands the loop b's completion, failed with err, or else passed
// having done what attest does: store a ref of one note for each check it
// built, or for a batch that adds its notes, add them to origin's ref.
func (s *scheduler) end(b *batch, err error) {
	s.t.Helper()
	c := completion{b: b, err: err, ended: time.Now()}
	if err == nil {
		j := b.job
		var entries []string
		for _, q := range j.missing {
			blob := strings.TrimSpace(s.input("note "+j.digest+" "+q.name+"\n", "hash-object", "-w", "--stdin"))
			sub := strings.TrimSpace(s.input("100644 blob "+blob+"\tstatement.note\n", "mktree"))
			entries = append(entries, "040000 tree "+sub+"\t"+q.name)
		}
		if j.main || j.additive {
			if held := s.onOrigin(j.ref); held != "" {
				s.run(s.g, "fetch", "--quiet", "--no-write-fetch-head", "origin", j.ref)
				entries = append(entries, strings.Split(strings.TrimSpace(s.run(s.g, "ls-tree", held)), "\n")...)
			}
		}
		tree := strings.TrimSpace(s.input(strings.Join(entries, "\n")+"\n", "mktree"))
		s.run(s.g, "update-ref", j.ref, tree)
		if j.main || j.additive {
			s.run(s.g, "push", "--quiet", "--force", "origin", tree+":"+j.ref)
		} else {
			c.stored = tree
		}
		if j.main {
			s.alter(j.r.name, func(wd *world) { wd.follow = "" })
		}
	}
	s.w.done <- c
}

// input runs git with stdin, and fails the test when it fails.
func (s *scheduler) input(stdin string, args ...string) string {
	out, err := s.g.input(true, strings.NewReader(stdin), args...)
	if err != nil {
		s.t.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// finish waits for the pass to end, and requires that it started and
// stopped nothing the test did not take, and kept the class order.
func (s *scheduler) finish() {
	s.t.Helper()
	select {
	case err := <-s.ended:
		if err != nil {
			s.t.Fatalf("the pass returned %v", err)
		}
	case <-time.After(10 * time.Second):
		s.t.Fatal("the pass did not end")
	}
	select {
	case b := <-s.launched:
		s.t.Errorf("the batch for %s started, and the test did not take it", b.job.version())
	case b := <-s.stops:
		s.t.Errorf("the batch for %s was stopped, and the test did not take it", b.job.version())
	default:
	}
	for _, w := range s.wrong {
		s.t.Error(w)
	}
}

// testRequest is the ref of a check request in a scheduler test.
func testRequest(attempt string) string {
	return change.CheckRequestPrefix + "alice/0123456789abcdef/" + attempt
}

// newWorld is a world whose heads hold the trees given, head to tree.
func newWorld(trees ...string) *world {
	wd := &world{refs: map[string]string{}, trees: map[string]string{}, asks: map[string][]string{},
		uncounted: map[string]bool{}, broken: map[string]error{}}
	for i := 0; i+1 < len(trees); i += 2 {
		wd.trees[trees[i]] = trees[i+1]
	}
	return wd
}

const (
	changeK = "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"
	changeL = "llllllllllllllllllllllllllllllll"
)

// Ready changes come first, then follow runs, then check requests,
// whichever projects they are in. A request starts only once no ready
// change or follow run waits or runs, whatever slots are free. A request
// batch that runs goes on to its end beside a follow run or ready work
// that arrives meanwhile, and a request that arrives then waits for the
// ready work to end.
func TestARequestStartsAfterReadyChangesAndFollowRuns(t *testing.T) {
	r1, r2 := testRequest("1111111111111111"), testRequest("2222222222222222")
	app := newWorld("h2", "t2", "h3", "t3", "h4", "t4", "h5", "t5", "h6", "t6")
	app.follow, app.refs[r1] = "h2", "h3"
	site := newWorld("h1", "t1")
	site.ready = []testVersion{{changeK, 1, "h1"}}
	s := newScheduler(t, 2, 0, t.TempDir(), map[string]*world{"app": app, "site": site})
	ready := s.started(changeK)
	s.idle("beside a ready batch, with a slot free")
	s.end(ready, nil)
	follow := s.started("main")
	s.idle("beside a follow run, with a slot free")
	s.end(follow, nil)
	request := s.started(r1)
	s.alter("app", func(wd *world) { wd.follow = "h6" })
	s.end(s.started("main"), nil)

	s.alter("app", func(wd *world) {
		wd.ready = append(wd.ready, testVersion{changeL, 1, "h4"})
		wd.refs[r2] = "h5"
	})
	later := s.started(changeL)
	s.idle("a request batch beside a ready one that started after it")
	s.end(request, nil)
	s.idle("a slot freed while a ready batch runs and a request waits")
	s.end(later, nil)
	s.end(s.started(r2), nil)
	s.finish()
}

// A slot a request batch frees goes to the next request only after a read
// of every project: a ready change readied meanwhile takes it.
func TestAFreedSlotWaitsForAReadBeforeTheNextRequest(t *testing.T) {
	r1, r2 := testRequest("1111111111111111"), testRequest("2222222222222222")
	app := newWorld("h1", "t1", "h2", "t2", "h3", "t3")
	app.refs[r1], app.refs[r2] = "h1", "h2"
	s := newScheduler(t, 1, time.Hour, t.TempDir(), map[string]*world{"app": app})
	first := s.started(r1)
	s.alter("app", func(wd *world) { wd.ready = []testVersion{{changeK, 1, "h3"}} })
	s.end(first, nil)
	s.end(s.started(changeK), nil)
	s.end(s.started(r2), nil)
	s.finish()
}

// A request whose batch failed is not built again, in this pass or the
// next, until a listing finds its ref moved or gone. A rerun is a new
// attempt, which builds at once though it names the same commit.
func TestAFailedRequestIsNotBuiltAgainUntilItsRefMoves(t *testing.T) {
	state := t.TempDir()
	first, rerun := testRequest("1111111111111111"), testRequest("2222222222222222")
	app := newWorld("h1", "t1", "h2", "t2")
	app.refs[first] = "h1"
	worlds := map[string]*world{"app": app}
	s := newScheduler(t, 1, 0, state, worlds)
	s.end(s.started(first), errors.New("exit status 1"))
	s.finish()
	if got := readFailures(filepath.Join(state, "failures.json"), io.Discard).Requests["app"]; len(got) != 1 || got[first] != "h1" {
		t.Fatalf("failures.json marks %v, want the failed request at its commit", got)
	}
	if _, waiting := s.w.failures.Projects["app"]; waiting {
		t.Errorf("the request's failure waits as a ready change's would: %v", s.w.failures.Projects)
	}

	s = newScheduler(t, 1, 0, state, worlds) // a pass that starts afresh, as after a restart
	s.finish()

	app.refs[rerun] = "h1"
	s = newScheduler(t, 1, 0, state, worlds)
	s.end(s.started(rerun), nil)
	s.finish()

	app.refs[first] = "h2"
	s = newScheduler(t, 1, 0, state, worlds)
	s.end(s.started(first), nil)
	s.finish()
	if got := readFailures(filepath.Join(state, "failures.json"), io.Discard).Requests; len(got) != 0 {
		t.Errorf("failures.json still marks %v once the ref moved", got)
	}
}

// A request batch is stopped once a whole read finds its ref moved or
// gone, and the request at its new commit is built. A stopped batch
// records no failure. A batch whose ref went runs on while a ready
// change's active version holds its tree, and another request with the
// same tree does not keep it: that request may need checks the batch
// does not build, and the worker's ref for the tree, once published,
// takes no more.
func TestARequestThatMovesOrGoesLosesItsBatch(t *testing.T) {
	state := t.TempDir()
	moved, kept, rerun := testRequest("1111111111111111"), testRequest("2222222222222222"), testRequest("3333333333333333")
	app := newWorld("h1", "t1", "h2", "t2", "h3", "t3", "h4", "t3")
	app.refs[moved] = "h1"
	worlds := map[string]*world{"app": app}
	s := newScheduler(t, 2, 0, state, worlds)
	b := s.started(moved)
	s.alter("app", func(wd *world) { wd.refs[moved] = "h2" })
	s.stopped(b)
	s.end(b, errors.New("signal: killed"))
	if again := s.started(moved); again.job.v.Head != "h2" {
		t.Errorf("the request started again at %s, want h2", again.job.v.Head)
	} else {
		s.end(again, nil)
	}
	s.finish()
	if len(s.w.failures.Requests) != 0 || len(s.w.failures.Projects) != 0 {
		t.Errorf("the stopped batch recorded a failure: %v %v", s.w.failures.Requests, s.w.failures.Projects)
	}

	app.refs = map[string]string{kept: "h3"}
	s = newScheduler(t, 2, 0, state, worlds)
	b = s.started(kept)
	s.alter("app", func(wd *world) {
		delete(wd.refs, kept)
		wd.ready = []testVersion{{changeK, 1, "h4"}}
	})
	s.idle("a request gone whose tree a ready version holds")
	s.alter("app", func(wd *world) {
		wd.ready = nil
		wd.refs[rerun] = "h3"
	})
	s.stopped(b)
	s.end(b, errors.New("signal: killed"))
	s.end(s.started(rerun), nil)
	s.finish()
}

// A check request whose commit's evidence cannot be read, as when attest
// cannot digest a tree holding a submodule, is marked and said once, and
// the scan goes on to build the rest; so does a ready change's, which is
// said. Neither is read again in the next pass. Only a failed read of
// origin ends a scan.
func TestAnUnreadableHeadEndsNoScan(t *testing.T) {
	state := t.TempDir()
	r1, r2 := testRequest("1111111111111111"), testRequest("2222222222222222")
	app := newWorld("h1", "t1", "h2", "t2")
	app.refs[r1], app.refs[r2] = "h1", "h2"
	app.broken = map[string]error{"h1": failure("could not digest the tree of h1")}
	site := newWorld("h3", "t3", "h4", "t4")
	site.ready = []testVersion{{changeK, 1, "h3"}, {changeL, 1, "h4"}}
	site.broken = map[string]error{"h3": failure("could not digest the tree of h3")}
	worlds := map[string]*world{"app": app, "site": site}
	s := newScheduler(t, 1, 0, state, worlds)
	s.end(s.started(changeL), nil)
	s.end(s.started(r2), nil)
	s.finish()
	if got := readFailures(filepath.Join(state, "failures.json"), io.Discard).Requests["app"]; len(got) != 1 || got[r1] != "h1" {
		t.Errorf("failures.json marks %v, want the unreadable request at its commit", got)
	}

	s = newScheduler(t, 1, 0, state, worlds)
	s.finish()
	if slices.Contains(s.assessed, r1) {
		t.Error("the marked request was read again")
	}

	site.broken["h4"] = originFailure{failure("could not list the evidence origin holds for this tree")}
	s = newScheduler(t, 1, 0, state, worlds)
	select {
	case err := <-s.ended:
		if err == nil || !strings.Contains(err.Error(), "the pass stops here") {
			t.Errorf("a failed read of origin ended the pass with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a failed read of origin did not end the pass")
	}
}

// A scan reads check requests only once it has read every project's ready
// changes and follow run: an earlier project's requests are not assessed
// while a later project's ready change waits or builds.
func TestRequestsWaitForEveryProjectsReadyWork(t *testing.T) {
	r1 := testRequest("1111111111111111")
	app := newWorld("h1", "t1")
	app.refs[r1] = "h1"
	site := newWorld("h2", "t2")
	site.ready = []testVersion{{changeK, 1, "h2"}}
	s := newScheduler(t, 2, 0, t.TempDir(), map[string]*world{"app": app, "site": site})
	ready := s.started(changeK)
	s.idle("a request beside a ready batch, with a slot free")
	app.mu.Lock()
	assessed := slices.Clone(s.assessed)
	app.mu.Unlock()
	if len(assessed) != 0 {
		t.Errorf("requests %v were assessed while a ready change waited or built", assessed)
	}
	s.end(ready, nil)
	s.end(s.started(r1), nil)
	s.finish()
}

// A request whose batch ends in failure while a scan reads the requests
// is not built again from that scan: its marker is read again when it is
// assessed.
func TestAMarkerWrittenDuringAReadHoldsTheRequest(t *testing.T) {
	r1 := testRequest("1111111111111111")
	app := newWorld("h1", "t1")
	app.refs[r1] = "h1"
	s := newScheduler(t, 1, 0, t.TempDir(), map[string]*world{"app": app})
	b := s.started(r1)
	app.mu.Lock()
	s.during = func() {
		go func() { s.w.done <- completion{b: b, err: errors.New("exit status 1"), ended: time.Now()} }()
		for s.w.running[b.job.dest()] != nil {
			if err := s.w.reap(); err != nil {
				t.Error(err)
				return
			}
		}
	}
	app.mu.Unlock()
	s.finish()
	if got := s.w.failures.Requests["app"][r1]; got != "h1" {
		t.Errorf("the failed request is marked at %q, want h1", got)
	}
}

// notesOn are the checks origin holds this worker's notes for at a tree.
func (s *scheduler) notesOn(tree string) []string {
	object := s.onOrigin(attestationRefs + tree + "/" + s.w.keyhash)
	if object == "" {
		return nil
	}
	s.run(s.g, "fetch", "--quiet", "--no-write-fetch-head", "origin", object)
	return s.noted(object)
}

// built requires a batch to build exactly the checks named, and says
// whether it adds them to the ref origin holds.
func built(t *testing.T, b *batch, additive bool, names ...string) {
	t.Helper()
	if got := b.job.names(); !slices.Equal(got, names) || b.job.additive != additive {
		t.Errorf("the batch for %s builds %v (additive %v), want %v (additive %v)", b.job.version(), got, b.job.additive, names, additive)
	}
}

// When origin holds this worker's evidence for a tree and it lacks a
// check a head there requires, the worker builds that check alone and
// adds its note to the ref: for a ready change waiting on a request's
// batch for its tree, and for a ready change whose policy came to require
// another check after its evidence was published. Nothing is built twice.
func TestPublishedEvidenceIsExtended(t *testing.T) {
	asked := testRequest("1111111111111111")
	app := newWorld("h1", "t1", "h2", "t1")
	app.refs[asked] = "h1"
	app.asks["h1"], app.asks["h2"] = []string{"x"}, []string{"x", "y"}
	s := newScheduler(t, 2, 0, t.TempDir(), map[string]*world{"app": app})
	request := s.started(asked)
	s.alter("app", func(wd *world) { wd.ready = []testVersion{{changeK, 1, "h2"}} })
	s.idle("a ready change whose tree a request's batch builds")
	s.end(request, nil)
	added := s.started(changeK)
	built(t, request, false, "x")
	built(t, added, true, "y")
	s.end(added, nil)
	s.finish()
	if got := s.notesOn("t1"); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("origin holds notes for %v, want x and y", got)
	}

	gained := newWorld("h3", "t3")
	gained.ready = []testVersion{{changeL, 1, "h3"}}
	gained.asks["h3"] = []string{"x"}
	s = newScheduler(t, 1, 0, t.TempDir(), map[string]*world{"app": gained})
	first := s.started(changeL)
	s.alter("app", func(wd *world) { wd.asks["h3"] = []string{"x", "y"} })
	s.end(first, nil)
	second := s.started(changeL)
	built(t, first, false, "x")
	built(t, second, true, "y")
	s.end(second, nil)
	s.finish()
	if got := s.notesOn("t3"); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("origin holds notes for %v, want x and y", got)
	}
}

// A ref a request's batch stored before the request went is published as
// it stands for a ready change with the same tree, and then extended with
// the check that change also requires.
func TestAStoredRefIsPublishedThenExtended(t *testing.T) {
	state := t.TempDir()
	asked := testRequest("1111111111111111")
	app := newWorld("h1", "t1", "h2", "t1")
	app.refs[asked] = "h1"
	app.asks["h1"], app.asks["h2"] = []string{"x"}, []string{"x", "y"}
	worlds := map[string]*world{"app": app}
	s := newScheduler(t, 1, 0, state, worlds)
	request := s.started(asked)
	sub := strings.TrimSpace(s.input("100644 blob "+strings.TrimSpace(s.input("x\n", "hash-object", "-w", "--stdin"))+"\tstatement.note\n", "mktree"))
	s.run(s.g, "update-ref", request.job.ref, strings.TrimSpace(s.input("040000 tree "+sub+"\tx\n", "mktree")))
	s.alter("app", func(wd *world) { delete(wd.refs, asked) })
	s.stopped(request)
	s.end(request, errors.New("signal: killed"))
	s.finish()
	if got := s.notesOn("t1"); got != nil {
		t.Fatalf("origin holds %v before any change needs the tree", got)
	}

	app.ready = []testVersion{{changeK, 1, "h2"}}
	s = newScheduler(t, 1, 0, state, worlds)
	added := s.started(changeK)
	built(t, added, true, "y")
	s.end(added, nil)
	s.finish()
	if got := s.notesOn("t1"); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("origin holds notes for %v, want x and y", got)
	}
}

// A note this worker's ref on origin holds for a check that does not
// count, as one for another attribute, cannot be replaced by another note
// of the same signer, so the check is not built again: a ready change
// waits for approve to build it, and a request is done with.
func TestANoteThatDoesNotCountIsNotBuiltAgain(t *testing.T) {
	state := t.TempDir()
	asked := testRequest("1111111111111111")
	app := newWorld("h1", "t1", "h2", "t1")
	app.ready = []testVersion{{changeK, 1, "h1"}}
	app.asks["h1"], app.asks["h2"] = []string{"x"}, []string{"x"}
	worlds := map[string]*world{"app": app}
	s := newScheduler(t, 1, 0, state, worlds)
	s.end(s.started(changeK), nil)
	s.finish()

	app.uncounted["x"] = true
	app.refs[asked] = "h2"
	s = newScheduler(t, 1, 0, state, worlds)
	s.finish()
	if got := s.w.failures.Requests["app"][asked]; got != "h2" {
		t.Errorf("the request is marked at %q, want h2", got)
	}
}

// A head whose evidence does not read is not read: its project's records
// are not pruned, so its own batch's failure still waits once it reads
// again; its batches are not stopped for want of it; and a main tip whose
// evidence does not read is not recorded as quiet.
func TestAnUnreadHeadKeepsItsProjectsRecordsAndBatches(t *testing.T) {
	state := t.TempDir()
	r := readFailures(filepath.Join(state, "failures.json"), io.Discard)
	r.failed("app", "build t1 check", time.Now(), time.Hour)
	if err := r.save(); err != nil {
		t.Fatal(err)
	}
	app := newWorld("h1", "t1", "h2", "t1", "h3", "t3")
	app.ready = []testVersion{{changeK, 1, "h2"}}
	app.broken["h2"] = failure("could not digest the tree of h2")
	worlds := map[string]*world{"app": app}
	s := newScheduler(t, 1, 0, state, worlds)
	s.finish()
	delete(app.broken, "h2")
	s = newScheduler(t, 1, 0, state, worlds)
	s.finish()

	app.ready = []testVersion{{changeL, 1, "h1"}}
	s = newScheduler(t, 1, 0, t.TempDir(), worlds)
	b := s.started(changeL)
	s.alter("app", func(wd *world) {
		wd.ready = []testVersion{{changeL, 2, "h2"}}
		wd.broken["h2"] = failure("could not digest the tree of h2")
	})
	s.idle("a superseded batch whose tree the new version, unread, holds")
	s.end(b, nil)
	s.finish()

	app.ready, app.follow = nil, "h3"
	app.broken["h3"] = failure("could not digest the tree of h3")
	s = newScheduler(t, 1, 0, state, worlds)
	s.finish()
	if m, ok := readQuiet(filepath.Join(state, "main.json"), io.Discard).Projects["app"]; ok {
		t.Errorf("a main tip whose evidence did not read is recorded quiet: %+v", m)
	}
}

// A scan that finds no higher-class work empties the queue before it
// reads requests, so a request batch that ends while they are read frees
// its slot to nothing an earlier scan queued, such as a version since
// superseded.
func TestTheRequestPhaseStartsNothingAnEarlierScanQueued(t *testing.T) {
	asked := testRequest("1111111111111111")
	app := newWorld("h1", "t1", "h2", "t2", "h3", "t3")
	app.refs[asked] = "h1"
	s := newScheduler(t, 1, 0, t.TempDir(), map[string]*world{"app": app})
	b := s.started(asked)
	s.alter("app", func(wd *world) { wd.ready = []testVersion{{changeK, 1, "h2"}} })
	s.idle("a ready change waiting for the slot a request batch holds")
	s.alter("app", func(wd *world) {
		wd.ready = []testVersion{{changeK, 2, "h3"}}
		wd.asks["h3"] = []string{}
		s.during = func() {
			go func() { s.w.done <- completion{b: b, err: errors.New("exit status 1"), ended: time.Now()} }()
			for s.w.running[b.job.dest()] != nil {
				if err := s.w.reap(); err != nil {
					t.Error(err)
					return
				}
			}
		}
	})
	s.finish()
}

// A request's failed push waits out its backoff: the scans and passes
// after it do not push the stored ref again before its wait is over.
func TestARequestsFailedPushWaitsOutItsBackoff(t *testing.T) {
	state := t.TempDir()
	asked := testRequest("1111111111111111")
	app := newWorld("h1", "t1")
	app.refs[asked] = "h1"
	worlds := map[string]*world{"app": app}
	s := newScheduler(t, 1, 0, state, worlds)
	if err := os.WriteFile(filepath.Join(s.origin, "refuse"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.end(s.started(asked), nil)
	s.finish()
	s = newScheduler(t, 1, 0, state, worlds)
	s.finish()
	attempts, err := os.ReadFile(filepath.Join(s.origin, "attempts"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(attempts), "attempt\n"); n != 1 {
		t.Errorf("origin was offered %d pushes, want 1", n)
	}
}
