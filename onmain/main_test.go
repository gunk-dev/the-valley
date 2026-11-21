package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_AUTHOR_NAME":     "valley-check",
		"GIT_AUTHOR_EMAIL":    "valley-check@localhost",
		"GIT_COMMITTER_NAME":  "valley-check",
		"GIT_COMMITTER_EMAIL": "valley-check@localhost",
	} {
		os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

const limit = 10 * time.Second

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// bare makes an empty bare repository, with the ref storage given.
func bare(t *testing.T, format string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app.git")
	run(t, ".", "init", "--quiet", "--bare", "--ref-format="+format, dir)
	return dir
}

// commit makes a commit in repo that no ref names yet.
func commit(t *testing.T, repo, message string) string {
	t.Helper()
	tree := run(t, repo, "mktree")
	return run(t, repo, "commit-tree", "-m", message, tree)
}

// land moves main to a new commit, as the integrator's update-ref does.
func land(t *testing.T, repo, message string) string {
	t.Helper()
	c := commit(t, repo, message)
	run(t, repo, "update-ref", "refs/heads/main", c)
	return c
}

// harness runs a bridge and stands in for its bound unit: each request
// the bridge makes is handed to the test, which consumes it as the unit's
// onmain environment would.
type harness struct {
	t       *testing.T
	repo    string
	file    string // the commit file
	request string
	reqs    chan string
	done    chan error
	stop    chan struct{}

	mu       sync.Mutex
	log      strings.Builder
	requests int
	idleAt   int // requests when the bridge last began waiting
}

func (h *harness) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.log.Write(p)
}

func (h *harness) said() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.log.String()
}

func startBridge(t *testing.T, repo string, watch func(string) (watcher, error)) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		t:       t,
		repo:    repo,
		file:    filepath.Join(dir, "commit"),
		request: filepath.Join(dir, "request"),
		reqs:    make(chan string),
		done:    make(chan error, 1),
		stop:    make(chan struct{}),
		idleAt:  -1,
	}
	b := &bridge{
		repo:    repo,
		commit:  h.file,
		request: h.request,
		log:     h,
		watch:   watch,
		stop:    h.stop,
		requested: func(tip string) {
			h.mu.Lock()
			h.requests++
			h.mu.Unlock()
			h.reqs <- tip
		},
		waiting: func() {
			h.mu.Lock()
			h.idleAt = h.requests
			h.mu.Unlock()
		},
	}
	go func() { h.done <- b.run() }()
	t.Cleanup(func() {
		close(h.stop)
		deadline := time.After(limit)
		for {
			select {
			case <-h.reqs:
			case err := <-h.done:
				if err != nil {
					t.Errorf("the bridge ended with %v", err)
				}
				return
			case <-deadline:
				t.Errorf("the bridge did not stop")
				return
			}
		}
	})
	return h
}

// requested waits for the bridge to request a run at want, and consumes
// the request as the unit's onmain environment does: the environment
// file it writes must name want, and no request may be left.
func (h *harness) requested(want string) {
	h.t.Helper()
	select {
	case tip := <-h.reqs:
		if tip != want {
			h.t.Fatalf("the bridge requested %s, want %s", tip, want)
		}
	case err := <-h.done:
		h.t.Fatalf("the bridge ended with %v; it said:\n%s", err, h.said())
	case <-time.After(limit):
		h.t.Fatalf("no run was requested; the bridge said:\n%s", h.said())
	}
	if got := environmentCommit(h.t, h.repo, h.file, h.request); got != want {
		h.t.Fatalf("the environment names %s, want %s", got, want)
	}
	if _, err := os.Stat(h.request); err == nil {
		h.t.Fatal("the request was left after the environment was written")
	}
}

// environmentCommit runs onmain environment, and returns the commit the
// environment file then names.
func environmentCommit(t *testing.T, repo, commitFile, request string) string {
	t.Helper()
	out := filepath.Join(filepath.Dir(commitFile), "environment")
	if err := writeEnvironment(repo, commitFile, request, out); err != nil {
		t.Fatalf("writing the environment: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	commit, ok := strings.CutPrefix(string(data), "VALLEY_REPOSITORY="+repo+"\nVALLEY_COMMIT=")
	if !ok {
		t.Fatalf("the environment file reads %q", data)
	}
	return strings.TrimSuffix(commit, "\n")
}

// idle waits until the bridge waits for a change after its last request,
// so what the test moves next can reach it only through the watch, and
// checks it left no request the test has not consumed.
func (h *harness) idle() {
	h.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		h.mu.Lock()
		idle := h.idleAt == h.requests
		h.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the bridge did not wait; it said:\n%s", h.said())
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := os.Stat(h.request); err == nil {
		h.t.Fatal("a run was requested that the test did not see")
	}
}

func TestAStartRequestsTheTipAndALooseRefMoveTheNext(t *testing.T) {
	repo := bare(t, "files")
	a := land(t, repo, "a")
	h := startBridge(t, repo, watchRefs)
	h.requested(a)
	h.idle()
	b := land(t, repo, "b")
	h.requested(b)
	h.idle()
}

func TestARepositoryWithNoMainRequestsItsFirst(t *testing.T) {
	repo := bare(t, "files")
	h := startBridge(t, repo, watchRefs)
	h.idle()
	a := land(t, repo, "a")
	h.requested(a)
}

// main packed, with no loose ref, moves when packed-refs is replaced; and
// an update after git pack-refs writes main's loose ref again.
func TestAPackedRefsRewriteRequestsTheTip(t *testing.T) {
	repo := bare(t, "files")
	a := land(t, repo, "a")
	run(t, repo, "pack-refs", "--all")
	if _, err := os.Stat(filepath.Join(repo, "refs", "heads", "main")); err == nil {
		t.Fatal("pack-refs left main's loose ref")
	}
	h := startBridge(t, repo, watchRefs)
	h.requested(a)
	h.idle()

	// Replaced by a rename, as git replaces it.
	b := commit(t, repo, "b")
	next := filepath.Join(repo, "packed-refs.next")
	if err := os.WriteFile(next, []byte("# pack-refs with: peeled fully-peeled sorted \n"+b+" refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, filepath.Join(repo, "packed-refs")); err != nil {
		t.Fatal(err)
	}
	h.requested(b)
	h.idle()

	c := land(t, repo, "c")
	h.requested(c)
}

func TestAnUnchangedTipIsNotRequestedAgain(t *testing.T) {
	repo := bare(t, "files")
	a := land(t, repo, "a")
	h := startBridge(t, repo, watchRefs)
	h.requested(a)
	h.idle()

	run(t, repo, "pack-refs", "--all")
	run(t, repo, "update-ref", "refs/heads/main", a)
	run(t, repo, "update-ref", "refs/heads/topic", commit(t, repo, "topic"))
	run(t, repo, "pack-refs", "--all")

	b := land(t, repo, "b")
	h.requested(b)
}

// pathUnit starts a unit as systemd does for a path unit with PathExists=
// on the request file: whenever the file exists and the unit is not
// running, and so again each time the unit stops while it exists. The
// unit's first command is onmain environment, and command stands for the
// rest, given the run's number and the commit its environment names; how
// a run ends does not change what the path unit does. pathUnit returns
// each run's commit.
func pathUnit(t *testing.T, repo, commitFile, request string, command func(run int, commit string)) []string {
	t.Helper()
	var runs []string
	for len(runs) < 10 {
		if _, err := os.Stat(request); err != nil {
			return runs
		}
		c := environmentCommit(t, repo, commitFile, request)
		runs = append(runs, c)
		command(len(runs)-1, c)
	}
	t.Fatalf("the unit was started again and again: %v", runs)
	return nil
}

// A commit the bridge requests while the unit runs for an older one, at
// any moment of that run, is run: by the same run, or by one more after
// it. Each case has the bridge request b while the unit's run for a is
// under way, at the moment it names.
func TestACommitRequestedDuringARunIsRun(t *testing.T) {
	a := strings.Repeat("a", 40)
	b := strings.Repeat("b", 40)
	c := strings.Repeat("c", 40)
	for _, tc := range []struct {
		name string
		step string // the step of onmain environment b is requested after
		want []string
	}{
		// b is in the commit file when this run reads it, and its request
		// was made after the removal, so b runs twice.
		{"after the request is removed", "removed", []string{b, b}},
		{"after the commit is read", "read", []string{a, b}},
		{"while the command runs", "", []string{a, b}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			repo, commitFile, request := "/srv/git/app.git", filepath.Join(dir, "commit"), filepath.Join(dir, "request")
			bridgeRequests := func(c string) {
				if err := requestRun(commitFile, request, c); err != nil {
					t.Fatal(err)
				}
			}
			bridgeRequests(a)
			afterStep = func(step string) {
				if step == tc.step {
					afterStep = nil
					bridgeRequests(b)
				}
			}
			t.Cleanup(func() { afterStep = nil })
			runs := pathUnit(t, repo, commitFile, request, func(run int, _ string) {
				if run == 0 && tc.step == "" {
					bridgeRequests(b)
				}
			})
			if !slices.Equal(runs, tc.want) {
				t.Errorf("the unit ran for %v, want %v", runs, tc.want)
			}
		})
	}

	// Moves made during a run are one run at the newest, and a run with no
	// request after it, failed or not, is the last.
	t.Run("several moves during a run", func(t *testing.T) {
		dir := t.TempDir()
		repo, commitFile, request := "/srv/git/app.git", filepath.Join(dir, "commit"), filepath.Join(dir, "request")
		if err := requestRun(commitFile, request, a); err != nil {
			t.Fatal(err)
		}
		runs := pathUnit(t, repo, commitFile, request, func(run int, _ string) {
			if run == 0 {
				for _, m := range []string{b, c} {
					if err := requestRun(commitFile, request, m); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
		if want := []string{a, c}; !slices.Equal(runs, want) {
			t.Errorf("the unit ran for %v, want %v", runs, want)
		}
	})
}

func TestReftableIsRefused(t *testing.T) {
	repo := bare(t, "reftable")
	land(t, repo, "a")
	dir := t.TempDir()
	b := &bridge{
		repo:    repo,
		commit:  filepath.Join(dir, "commit"),
		request: filepath.Join(dir, "request"),
		log:     os.Stderr,
		watch:   watchRefs,
	}
	err := b.run()
	if err == nil || !strings.Contains(err.Error(), "keeps its refs in reftable storage") {
		t.Fatalf("got %v, want reftable refused", err)
	}
	if _, err := os.Stat(b.request); err == nil {
		t.Error("a run was requested")
	}
}

func TestTheEnvironmentIsWrittenFromOneCommit(t *testing.T) {
	dir := t.TempDir()
	commit := strings.Repeat("0123456789abcdef", 4)
	for _, c := range []string{commit[:40], commit} {
		if err := os.WriteFile(filepath.Join(dir, "commit"), []byte(c+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "environment")
		if err := writeEnvironment("/srv/git/app.git", filepath.Join(dir, "commit"), filepath.Join(dir, "request"), out); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if want := "VALLEY_REPOSITORY=/srv/git/app.git\nVALLEY_COMMIT=" + c + "\n"; string(data) != want {
			t.Errorf("got %q, want %q", data, want)
		}
	}
}

// The commit file is the bridge's user's to write, and the environment
// file is read by systemd for a unit that may run as root, so nothing in
// the commit file but one commit ID reaches it. A refused file still
// consumes the request, so the refusal does not start the unit again.
func TestAnythingButOneCommitIsRefused(t *testing.T) {
	commit := strings.Repeat("ab", 20)
	for name, content := range map[string]string{
		"an assignment after it": commit + "\nLD_PRELOAD=/tmp/x.so\n",
		"an assignment alone":    "VALLEY_COMMIT=" + commit + "\n",
		"no newline":             commit,
		"capitals":               strings.ToUpper(commit) + "\n",
		"a short ID":             commit[:39] + "\n",
		"empty":                  "",
		"a longer file":          commit + "\n" + strings.Repeat("x", 1<<20),
		"a carriage return":      commit + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "commit"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			refuse(t, dir, "/srv/git/app.git")
		})
	}
	t.Run("a symbolic link", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "target"), []byte(commit+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target", filepath.Join(dir, "commit")); err != nil {
			t.Fatal(err)
		}
		refuse(t, dir, "/srv/git/app.git")
	})
	t.Run("a FIFO", func(t *testing.T) {
		dir := t.TempDir()
		if err := syscall.Mkfifo(filepath.Join(dir, "commit"), 0o644); err != nil {
			t.Fatal(err)
		}
		refuse(t, dir, "/srv/git/app.git")
	})
	t.Run("no commit file", func(t *testing.T) {
		refuse(t, t.TempDir(), "/srv/git/app.git")
	})
	t.Run("a repository the file cannot carry", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "commit"), []byte(commit+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		refuse(t, dir, "/srv/git/my app.git")
	})
}

// refuse checks the commit file in dir is refused for repo, no
// environment file written, and the request consumed.
func refuse(t *testing.T, dir, repo string) {
	t.Helper()
	out, request := filepath.Join(dir, "environment"), filepath.Join(dir, "request")
	if err := os.WriteFile(request, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeEnvironment(repo, filepath.Join(dir, "commit"), request, out)
	if err == nil {
		t.Fatal("it was accepted")
	}
	if strings.Contains(err.Error(), "LD_PRELOAD") || strings.Contains(err.Error(), "xxxx") {
		t.Errorf("the refusal prints what the file holds: %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an environment file was written")
	}
	if _, err := os.Stat(request); err == nil {
		t.Error("the request was left, to start the unit again")
	}
}

// fakeWatch is a watch the test fails by hand.
type fakeWatch struct {
	note   chan struct{}
	end    chan error
	closed chan struct{}
}

func newFake() *fakeWatch {
	return &fakeWatch{note: make(chan struct{}, 1), end: make(chan error, 1), closed: make(chan struct{})}
}

func (f *fakeWatch) events() <-chan struct{} { return f.note }
func (f *fakeWatch) failed() <-chan error    { return f.end }
func (f *fakeWatch) close()                  { close(f.closed) }

// A move made while no watch is held is requested once the watch is back,
// though no event reports it; a watch that cannot be set up again ends
// the bridge.
func TestALostWatchIsSetUpAgainAndMainReadAgain(t *testing.T) {
	repo := bare(t, "files")
	a := land(t, repo, "a")
	b := commit(t, repo, "b")
	first, second := newFake(), newFake()
	watches := 0
	h := startBridge(t, repo, func(string) (watcher, error) {
		watches++
		switch watches {
		case 1:
			return first, nil
		case 2:
			if out, err := exec.Command("git", "-C", repo, "update-ref", "refs/heads/main", b).CombinedOutput(); err != nil {
				return nil, fmt.Errorf("moving main: %v: %s", err, out)
			}
			return second, nil
		}
		return nil, errors.New("no watch")
	})
	h.requested(a)
	h.idle()

	first.end <- errDropped
	h.requested(b)
	select {
	case <-first.closed:
	default:
		t.Error("the lost watch was not closed")
	}

	h.idle()
	second.end <- errDropped
	select {
	case err := <-h.done:
		if err == nil || !strings.Contains(err.Error(), "no watch") {
			t.Errorf("the bridge ended with %v", err)
		}
		h.done <- nil
	case <-time.After(limit):
		t.Fatal("the bridge went on without a watch")
	}
}

// A watched directory moved away, or refs or refs/heads renamed, fails the
// watch: it no longer sees main.
func TestAWatchFailsWhenItsDirectoriesGo(t *testing.T) {
	for _, moved := range []string{".", "refs", "refs/heads"} {
		t.Run(moved, func(t *testing.T) {
			repo := bare(t, "files")
			w, err := watchRefs(repo)
			if err != nil {
				t.Fatal(err)
			}
			defer w.close()
			from := filepath.Join(repo, moved)
			if err := os.Rename(from, filepath.Clean(from)+".moved"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-w.failed():
			case <-time.After(limit):
				t.Fatal("the watch did not fail")
			}
		})
	}
}

// A repository whose refs are all packed may have no refs/heads. The
// bridge reads main from packed-refs, and follows main into refs/heads
// once git creates it.
func TestAMissingRefsHeadsIsWatchedForAtStart(t *testing.T) {
	repo := bare(t, "files")
	a := land(t, repo, "a")
	run(t, repo, "pack-refs", "--all")
	if err := os.RemoveAll(filepath.Join(repo, "refs", "heads")); err != nil {
		t.Fatal(err)
	}
	h := startBridge(t, repo, watchRefs)
	h.requested(a)
	h.idle()

	b := land(t, repo, "b")
	h.requested(b)
	h.idle()
	c := land(t, repo, "c")
	h.requested(c)
}

// refs/heads going while the bridge watches it, as when the directory is
// replaced, leaves the bridge watching for it, and following main once it
// is back.
func TestARefsHeadsGoneWhileWatchedIsWatchedFor(t *testing.T) {
	repo := bare(t, "files")
	a := land(t, repo, "a")
	heads := filepath.Join(repo, "refs", "heads")
	// The second watch is the one set up after refs/heads went.
	rearmed := make(chan bool)
	watches := 0
	h := startBridge(t, repo, func(gitDir string) (watcher, error) {
		watches++
		w, err := watchRefs(gitDir)
		if watches == 2 {
			_, absent := os.Stat(heads)
			rearmed <- absent != nil
		}
		return w, err
	})
	h.requested(a)
	h.idle()

	run(t, repo, "pack-refs", "--all")
	if err := os.Rename(heads, heads+".old"); err != nil {
		t.Fatal(err)
	}
	select {
	case absent := <-rearmed:
		if !absent {
			t.Fatal("refs/heads was back before the watch was set up again")
		}
	case <-time.After(limit):
		t.Fatalf("the watch was not set up again; the bridge said:\n%s", h.said())
	}
	b := land(t, repo, "b")
	h.requested(b)
	h.idle()
	c := land(t, repo, "c")
	h.requested(c)
}
