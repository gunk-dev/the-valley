package main

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// The configuration a step borrows goes after the entries the caller's
// environment gives git already, which are kept, and the count covers
// both.
func TestWithConfigAppendsToTheCallersEntries(t *testing.T) {
	env := []string{"HOME=/home/operator", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.pager", "GIT_CONFIG_VALUE_0=cat"}
	got := withConfig(env, [][2]string{{"valley.principal", "alice"}, {"user.name", "alice"}})
	want := []string{"HOME=/home/operator", "GIT_CONFIG_KEY_0=core.pager", "GIT_CONFIG_VALUE_0=cat",
		"GIT_CONFIG_KEY_1=valley.principal", "GIT_CONFIG_VALUE_1=alice",
		"GIT_CONFIG_KEY_2=user.name", "GIT_CONFIG_VALUE_2=alice", "GIT_CONFIG_COUNT=3"}
	if !slices.Equal(got, want) {
		t.Fatalf("withConfig gave\n%q\nwant\n%q", got, want)
	}
	if got := withConfig(env, nil); !slices.Equal(got, env) {
		t.Fatalf("withConfig with nothing to borrow changed the environment: %q", got)
	}
}

// walkHost is a host of scratch repositories a walk reads through
// discovery: the instance repository valley, whose main carries
// policy/instance and whose projects ref lists app, site and valley, and
// app and site beside it, whose mains name it at policy/valley. The walk
// runs from a checkout of app, as alice, and reads site and valley through
// cache clones.
type walkHost struct {
	t                *testing.T
	dir, tmp, server string
	instance         string // the instance repository's URL, as app names it
	work             map[string]string
}

func newWalkHost(t *testing.T) *walkHost {
	dir := t.TempDir()
	h := &walkHost{t: t, dir: dir, tmp: filepath.Join(dir, "tmp"), server: filepath.Join(dir, "srv"), work: map[string]string{}}
	for k, v := range map[string]string{"HOME": dir, "XDG_CONFIG_HOME": filepath.Join(dir, "config"),
		"XDG_STATE_HOME": filepath.Join(dir, "state"), "XDG_CACHE_HOME": filepath.Join(dir, "cache"),
		"GIT_CONFIG_NOSYSTEM": "1", "VALLEY_KNOWN_SIGNERS": "", "VALLEY_INTEGRATOR_SIGNER": "", "TMPDIR": h.tmp} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(h.tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	h.instance = h.url("valley")
	h.project("valley", "policy/instance/readme", "the instance\n")
	h.project("app", "policy/valley", h.instance+"\n")
	h.project("site", "policy/valley", h.instance+"\n")
	return h
}

// url is the URL of the project name on the host.
func (h *walkHost) url(name string) string { return filepath.Join(h.server, name+".git") }

// project makes the project name on the host, with a checkout whose main
// holds one file, and lists it in the instance's projects ref.
func (h *walkHost) project(name, file, content string) {
	origin, work := h.url(name), filepath.Join(h.dir, name)
	h.git(h.dir, "init", "--quiet", "--bare", "--initial-branch=main", origin)
	h.git(h.dir, "clone", "--quiet", origin, work)
	h.work[name] = work
	for _, kv := range [][2]string{{"user.name", "alice"}, {"user.email", "alice@example.org"}, {"valley.principal", "alice"}} {
		h.git(work, "config", kv[0], kv[1])
	}
	h.git(work, "checkout", "--quiet", "-b", "main")
	h.write(name, file, content)
	h.git(work, "add", ".")
	h.git(work, "commit", "--quiet", "-m", "base")
	h.git(work, "push", "--quiet", "origin", "main")
	listed := h.git(h.url("valley"), "for-each-ref", "--format=%(objectname)", projectsRef)
	names := ""
	if listed != "" {
		names = h.git(h.url("valley"), "cat-file", "blob", listed) + "\n"
	}
	blob := h.gitIn(h.url("valley"), names+name+"\n", "hash-object", "-w", "--stdin")
	h.git(h.url("valley"), "update-ref", projectsRef, blob)
}

// write writes a file in a project's checkout.
func (h *walkHost) write(name, file, content string) {
	path := filepath.Join(h.work[name], file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// ready readies a change in the project name: a branch with one commit,
// pushed, then readied as the change id.
func (h *walkHost) ready(name, id, branch string) {
	work := h.work[name]
	h.git(work, "checkout", "--quiet", "-b", branch, "main")
	h.write(name, branch+".txt", branch+"\n")
	h.git(work, "add", ".")
	h.git(work, "commit", "--quiet", "-m", branch)
	h.git(work, "push", "--quiet", "origin", branch)
	h.git(work, "checkout", "--quiet", "main")
	h.t.Chdir(work)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ready", "--change", id, branch}, &stdout, &stderr, os.Exit); code != 0 {
		h.t.Fatalf("valley ready in %s: %s", name, stderr.String())
	}
}

// point has the project name's main name the valley at url.
func (h *walkHost) point(name, url string) {
	h.write(name, "policy/valley", url+"\n")
	h.git(h.work[name], "commit", "--quiet", "-am", "another valley")
	h.git(h.work[name], "push", "--quiet", "origin", "main")
}

func (h *walkHost) git(dir string, args ...string) string {
	h.t.Helper()
	return h.gitIn(dir, "", args...)
}

func (h *walkHost) gitIn(dir, stdin string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ran is how a run of valley ended: its status and output.
type ran struct {
	status         int
	stdout, stderr string
}

// run runs valley with args in app's checkout, its answers read from
// stdin. h.t.Chdir must have been called from the test's goroutine.
func (h *walkHost) run(stdin *os.File, args ...string) ran { return <-h.start(stdin, args...) }

// start runs valley as run does, in a goroutine, and says how it ended. A
// signal the review takes ends it through the end path, which would end
// the process: it fails the test, says how it ended and leaves its
// goroutine, so a test that waits is not left waiting.
func (h *walkHost) start(stdin *os.File, args ...string) <-chan ran {
	ended := make(chan ran, 2)
	go func() {
		old := os.Stdin
		os.Stdin = stdin
		defer func() { os.Stdin = old }()
		var stdout, stderr bytes.Buffer
		status := run(args, &stdout, &stderr, func(status int) {
			h.t.Errorf("a signal the review took ended it with %d", status)
			ended <- ran{status, stdout.String(), stderr.String()}
			runtime.Goexit()
		})
		ended <- ran{status, stdout.String(), stderr.String()}
	}()
	return ended
}

// fromApp has the test run valley from app's checkout. The walk and
// valley review <change> catch SIGINT and SIGTERM until the process
// exits, so the test gives them back once it has ended.
func (h *walkHost) fromApp() {
	h.t.Chdir(h.work["app"])
	h.t.Cleanup(func() { signal.Reset(os.Interrupt, syscall.SIGTERM) })
}

// leftBehind fails the test when the run left anything in its TMPDIR.
func (h *walkHost) leftBehind() {
	h.t.Helper()
	if left, _ := os.ReadDir(h.tmp); len(left) > 0 {
		h.t.Errorf("the run left %s in its TMPDIR", left[0].Name())
	}
}

// answers is a file holding the answers given.
func (h *walkHost) answers(text string) *os.File {
	path := filepath.Join(h.dir, "answers")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		h.t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { f.Close() })
	return f
}

// heldChecks makes the checks read every review starts hold through
// SIGTERM, which a review's cleanup sends it, until the test releases it
// (fakeCheckline). It returns once the read is running, and the review has
// shown its page; it is given the answers that end the review.
type heldChecks struct {
	t   *testing.T
	dir string
}

func holdChecks(t *testing.T) heldChecks {
	dir := t.TempDir()
	for _, f := range []string{"ready", "termed", "release"} {
		if err := syscall.Mkfifo(filepath.Join(dir, f), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VALLEY_TEST_HELD_CHECKS", dir)
	return heldChecks{t, dir}
}

// await returns once the held read says it has reached the stage named.
func (c heldChecks) await(stage string) {
	if _, err := os.ReadFile(filepath.Join(c.dir, stage)); err != nil {
		c.t.Fatal(err)
	}
}

// release lets the held read end.
func (c heldChecks) release() {
	if err := os.WriteFile(filepath.Join(c.dir, "release"), nil, 0o600); err != nil {
		c.t.Fatal(err)
	}
}

// fakeCheckline is the review's checks read when the test binary is run as
// checkline: it reads no checks and prints no line. Under
// VALLEY_TEST_HELD_CHECKS, the first read says it is running on the fifo
// ready there, and once sent SIGTERM says so on termed and waits for
// release, which holds the review's cleanup open. Any later read ends at
// once, so a walk that goes on does not wait on it.
func fakeCheckline() int {
	dir := os.Getenv("VALLEY_TEST_HELD_CHECKS")
	if dir == "" {
		return 0
	}
	if f, err := os.OpenFile(filepath.Join(dir, "claimed"), os.O_CREATE|os.O_EXCL, 0o600); err != nil {
		return 0
	} else {
		f.Close()
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM)
	_ = os.WriteFile(filepath.Join(dir, "ready"), nil, 0o600)
	<-c
	_ = os.WriteFile(filepath.Join(dir, "termed"), nil, 0o600)
	_, _ = os.ReadFile(filepath.Join(dir, "release"))
	return 0
}

// signalSelf sends sig to the test's process, and returns once every
// channel notified of it has been sent it: os/signal sends to all of them
// under one lock, which Stop takes.
func signalSelf(t *testing.T, sig syscall.Signal) {
	seen := make(chan os.Signal, 1)
	signal.Notify(seen, sig)
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
	<-seen
	signal.Stop(seen)
}

// interruptCleanup runs valley with args from app's checkout, answers s
// once the first review's checks read is running, and sends SIGTERM once
// that review has stopped listening and its cleanup waits on the read.
// The input then ends, so a review that goes on ends rather than waits.
func (h *walkHost) interruptCleanup(args ...string) ran {
	held := holdChecks(h.t)
	h.fromApp()
	in, out, err := os.Pipe()
	if err != nil {
		h.t.Fatal(err)
	}
	defer in.Close()
	ended := h.start(in, args...)
	held.await("ready")
	if _, err := out.Write([]byte("s\n")); err != nil {
		h.t.Fatal(err)
	}
	held.await("termed")
	signalSelf(h.t, syscall.SIGTERM)
	held.release()
	out.Close()
	return <-ended
}

// A signal that arrives once a step's review has stopped listening, while
// it cleans up, ends the walk before the next step: the step ends as it
// would, the summary so far is printed, nothing is left in TMPDIR, and the
// walk exits 130.
func TestWalkEndsOnASignalWhileAStepCleansUp(t *testing.T) {
	h := newWalkHost(t)
	h.ready("app", strings.Repeat("m", 32), "a-one")
	h.ready("app", strings.Repeat("n", 32), "a-two")
	r := h.interruptCleanup("review", "--repo", "app")
	if r.status != interruptedStatus {
		t.Fatalf("the walk exited %d, not 130\n%s%s", r.status, r.stdout, r.stderr)
	}
	for _, want := range []string{"walked (1 of 2)\n", "  ◇ app mmmm v1  skipped\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the walk did not print %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "[2/2]") || strings.Contains(r.stdout, "needs you (") && strings.Contains(r.stdout, "still needs you") {
		t.Errorf("the walk went on after the signal:\n%s", r.stdout)
	}
	h.leftBehind()
}

// valley review <change> given a signal while it cleans up exits 130.
func TestReviewEndsOnASignalWhileItCleansUp(t *testing.T) {
	h := newWalkHost(t)
	h.ready("app", strings.Repeat("m", 32), "a-one")
	r := h.interruptCleanup("review", strings.Repeat("m", 32))
	if r.status != interruptedStatus {
		t.Fatalf("the review exited %d, not 130\n%s%s", r.status, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "skipped a-one; nothing done") {
		t.Errorf("the review did not end as it would:\n%s", r.stdout)
	}
	h.leftBehind()
}

// A step in a project whose main names another valley is not reviewed with
// the walk's signer source: it ends in error, saying which valley each
// names, and the walk goes on. Given what to trust, the walk reviews it.
func TestWalkRefusesAStepWhoseProjectNamesAnotherValley(t *testing.T) {
	h := newWalkHost(t)
	other := filepath.Join(h.dir, "other.git")
	h.git(h.dir, "init", "--quiet", "--bare", other)
	h.publish(other, "other-host/attestations")
	h.point("site", other)
	h.ready("site", strings.Repeat("s", 32), "s-one")
	h.ready("app", strings.Repeat("m", 32), "a-one")
	h.fromApp()
	r := h.run(h.answers("s\n"), "review")
	if r.status != 1 {
		t.Fatalf("the walk exited %d, not 1\n%s%s", r.status, r.stdout, r.stderr)
	}
	why := "its main names the valley " + other + ", not this checkout's " + h.instance +
		" · review it from a checkout of its own valley"
	if !strings.Contains(r.stderr, "valley: site: "+why+"\n") {
		t.Errorf("the refused step did not say %q:\n%s", why, r.stderr)
	}
	for _, want := range []string{"walked (2 of 2)\n", "  ✗ site ssss v1  error · its main names the valley ", "  ◇ app mmmm v1  skipped\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the walk did not print %q:\n%s", want, r.stdout)
		}
	}
	if strings.Contains(r.stdout, "s-one; nothing done") {
		t.Errorf("the refused step was reviewed:\n%s", r.stdout)
	}
	r = h.run(h.answers("s\ns\n"), "walk", "--integrator-name", "operator")
	if r.status != 0 || !strings.Contains(r.stdout, "  ◇ site ssss v1  skipped\n") {
		t.Errorf("the walk given an integrator name did not review site (%d):\n%s%s", r.status, r.stdout, r.stderr)
	}
	h.leftBehind()
}

// The instance repository names itself by its origin, which its cache
// clone spells as the listing does, and another project names it at
// policy/valley as written there. A checkout that spells it otherwise
// still walks both: each reads the same publication.
func TestWalkReviewsTheInstanceAndProjectsNamingItAnotherWay(t *testing.T) {
	h := newWalkHost(t)
	h.publish(h.url("valley"), "valley-host/attestations")
	h.point("app", h.instance+"/")
	h.ready("valley", strings.Repeat("v", 32), "v-one")
	h.ready("site", strings.Repeat("s", 32), "s-one")
	h.fromApp()
	r := h.run(h.answers("s\ns\n"), "review")
	if r.status != 0 {
		t.Fatalf("the walk exited %d, not 0\n%s%s", r.status, r.stdout, r.stderr)
	}
	for _, want := range []string{"  ◇ valley vvvv v1  skipped\n", "  ◇ site ssss v1  skipped\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the walk did not print %q:\n%s%s", want, r.stdout, r.stderr)
		}
	}
	h.leftBehind()
}

// publish has the bare repository at dir publish signers under name.
func (h *walkHost) publish(dir, name string) {
	keys := h.gitIn(dir, name+" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKf0k8m5mJ8T0pQ1o4n3wWvZ0f0r6C5g1Y8p0rJpY1sQ\n", "hash-object", "-w", "--stdin")
	named := h.gitIn(dir, name+"\n", "hash-object", "-w", "--stdin")
	tree := h.gitIn(dir, "100644 blob "+named+"\t"+signersName+"\n100644 blob "+keys+"\t"+signersKeys+"\n", "mktree")
	h.git(dir, "update-ref", signersRef, tree)
}
