//go:build linux

package main

// The wake loop over a real directory and the real inotify watch. Only
// the pass is stood in for: it records why it started, and does what a
// case needs while it "reads the refs". What a real pass does is
// integrator-e2e's.

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type loopRun struct {
	t      *testing.T
	dir    string
	stop   chan struct{}
	done   chan error
	mu     sync.Mutex
	starts []passStart
	stderr strings.Builder
}

type passStart struct {
	reason passReason
	at     time.Time
	due    bool // whether due was there when the pass began
}

// startLoop runs a wake loop over dir. during, if set, runs inside pass n
// (from 0) and says what the pass returns.
func startLoop(t *testing.T, dir string, interval time.Duration, during func(n int) (bool, error)) *loopRun {
	t.Helper()
	r := &loopRun{t: t, dir: dir, stop: make(chan struct{}), done: make(chan error, 1)}
	l := &wakeLoop{
		dir:      dir,
		interval: interval,
		stderr:   lockedWriter{&r.mu, &r.stderr},
		watch:    watchDir,
		stop:     r.stop,
		pass: func(reason passReason) (bool, error) {
			_, err := os.Lstat(filepath.Join(dir, "due"))
			r.mu.Lock()
			n := len(r.starts)
			r.starts = append(r.starts, passStart{reason, time.Now(), err == nil})
			r.mu.Unlock()
			if during != nil {
				return during(n)
			}
			return false, nil
		},
	}
	go func() { r.done <- l.run() }()
	t.Cleanup(func() {
		select {
		case <-r.stop:
		default:
			close(r.stop)
		}
		<-r.done
	})
	return r
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (r *loopRun) passes() []passStart {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]passStart(nil), r.starts...)
}

func (r *loopRun) said() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stderr.String()
}

// waitPasses waits until n passes have started, and fails after limit.
func (r *loopRun) waitPasses(n int, limit time.Duration) []passStart {
	r.t.Helper()
	deadline := time.Now().Add(limit)
	for {
		p := r.passes()
		if len(p) >= n {
			return p
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("%d passes after %s, not %d: %v\n%s", len(p), limit, n, p, r.said())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// still says the loop starts no pass beyond n over d.
func (r *loopRun) still(n int, d time.Duration) {
	r.t.Helper()
	time.Sleep(d)
	if p := r.passes(); len(p) != n {
		r.t.Fatalf("%d passes, not %d: %v\n%s", len(p), n, p, r.said())
	}
}

func markWake(t *testing.T, dir string) {
	f, err := os.OpenFile(filepath.Join(dir, "due"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o440)
	if errors.Is(err, fs.ErrExist) {
		return
	}
	if err != nil {
		t.Error(err)
		return
	}
	f.Close()
}

func reasons(p []passStart) []passReason {
	var out []passReason
	for _, s := range p {
		out = append(out, s.reason)
	}
	return out
}

func wakeDir(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "wake")
	if err := os.Mkdir(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A mark made before the start is removed before the first pass reads. A
// mark made during a pass, after the removal, starts exactly one more pass
// when it ends, and the notification it left starts nothing after that.
func TestAMarkDuringAPassStartsOneMoreAndNoOther(t *testing.T) {
	dir := wakeDir(t)
	markWake(t, dir)
	r := startLoop(t, dir, time.Hour, func(n int) (bool, error) {
		if n == 0 {
			markWake(t, dir)
		}
		return false, nil
	})
	p := r.waitPasses(2, 5*time.Second)
	if p[0].reason != reasonStartup || p[0].due {
		t.Fatalf("the first pass was %v, and due was there when it read: %v", p[0].reason, p[0].due)
	}
	if p[1].reason != reasonDue || p[1].due {
		t.Fatalf("the pass after a mark made during the first was %v, due there: %v", p[1].reason, p[1].due)
	}
	r.still(2, 300*time.Millisecond)

	// Idle, a mark starts a pass promptly.
	marked := time.Now()
	markWake(t, dir)
	p = r.waitPasses(3, 5*time.Second)
	if p[2].reason != reasonDue {
		t.Fatalf("a mark while idle started a %v pass", p[2].reason)
	}
	t.Logf("mark to pass start, idle: %s", p[2].at.Sub(marked))
	r.still(3, 300*time.Millisecond)
}

// A burst of marks while a pass runs is one more pass, and the last pass
// starts after the last mark.
func TestABurstOfMarksIsCoalesced(t *testing.T) {
	dir := wakeDir(t)
	r := startLoop(t, dir, time.Hour, func(int) (bool, error) {
		time.Sleep(100 * time.Millisecond)
		return false, nil
	})
	r.waitPasses(1, 5*time.Second)
	var last time.Time
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				markWake(t, dir)
				mu.Lock()
				last = time.Now()
				mu.Unlock()
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	time.Sleep(500 * time.Millisecond)
	p := r.passes()
	if len(p) < 2 || len(p) > 4 {
		t.Fatalf("200 marks over ~30ms started %d passes: %v", len(p)-1, reasons(p))
	}
	if !p[len(p)-1].at.After(last) {
		t.Fatalf("no pass started after the last mark")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the idle directory holds %v", entries)
	}
	t.Logf("200 marks: %d passes after the first", len(p)-1)
}

// A pass that lands is followed at once by another, and one that does not
// is followed by nothing until a mark or the timer.
func TestALandingIsFollowedAtOnce(t *testing.T) {
	dir := wakeDir(t)
	r := startLoop(t, dir, time.Hour, func(n int) (bool, error) { return n == 0, nil })
	p := r.waitPasses(2, 5*time.Second)
	if p[1].reason != reasonFollowUp {
		t.Fatalf("the pass after a landing was %v", p[1].reason)
	}
	if _, err := os.Lstat(filepath.Join(dir, "due")); err == nil {
		t.Fatalf("the follow-up was marked through the directory")
	}
	r.still(2, 300*time.Millisecond)
}

// The interval starts a pass when nothing else does.
func TestTheIntervalIsTheRecoveryPass(t *testing.T) {
	dir := wakeDir(t)
	r := startLoop(t, dir, 150*time.Millisecond, nil)
	p := r.waitPasses(3, 5*time.Second)
	if got := reasons(p[:3]); got[1] != reasonTimer || got[2] != reasonTimer {
		t.Fatalf("an idle loop passed for %v", got)
	}
}

// A due that cannot be removed starts the pass it is found at, and then
// nothing until the timer, however often it is marked.
func TestADueThatCannotBeRemovedDoesNotSpin(t *testing.T) {
	dir := wakeDir(t)
	r := startLoop(t, dir, 400*time.Millisecond, nil)
	r.waitPasses(1, 5*time.Second)
	// A due that is a directory with something in it cannot be removed.
	if err := os.MkdirAll(filepath.Join(dir, "due", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := r.waitPasses(2, 5*time.Second)
	if p[1].reason != reasonDue {
		t.Fatalf("a new due started a %v pass", p[1].reason)
	}
	time.Sleep(time.Second)
	p = r.passes()
	for _, s := range p[2:] {
		if s.reason != reasonTimer {
			t.Fatalf("a due that could not be removed started %v", reasons(p))
		}
	}
	if len(p) > 6 {
		t.Fatalf("%d passes in about 1.2s at a 400ms interval: %v", len(p), reasons(p))
	}
	if !strings.Contains(r.said(), "could not remove") {
		t.Fatalf("the failed removal was not said: %s", r.said())
	}
}

// A pass that failed is not followed by another before the hold, whatever
// is marked meanwhile.
func TestAFailedPassHoldsOff(t *testing.T) {
	dir := wakeDir(t)
	r := startLoop(t, dir, time.Hour, func(int) (bool, error) { return false, errors.New("the refs did not read") })
	r.waitPasses(1, 5*time.Second)
	for i := 0; i < 5; i++ {
		markWake(t, dir)
		time.Sleep(100 * time.Millisecond)
	}
	r.still(1, 0)
	if !strings.Contains(r.said(), "the refs did not read") {
		t.Fatalf("the failure was not said: %s", r.said())
	}
}

// A directory removed under the watch leaves the loop on its timer, and
// the loop watches again once the directory is back, with one pass for
// whatever was marked where nothing watched.
func TestARemovedDirectoryIsWatchedAgain(t *testing.T) {
	dir := wakeDir(t)
	r := startLoop(t, dir, time.Hour, nil)
	r.waitPasses(1, 5*time.Second)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if !strings.Contains(r.said(), "ended") {
		t.Fatalf("the ended watch was not said: %s", r.said())
	}
	if err := os.Mkdir(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	p := r.waitPasses(2, 2*watchRetry)
	if p[1].reason != reasonWatcher {
		t.Fatalf("the pass after the watch came back was %v", p[1].reason)
	}
	markWake(t, dir)
	p = r.waitPasses(3, 5*time.Second)
	if p[2].reason != reasonDue {
		t.Fatalf("a mark in the directory made again started a %v pass", p[2].reason)
	}
}

// A directory missing at the start is said, the start's pass runs, and
// the directory is watched once it exists.
func TestAMissingDirectoryIsWatchedOnceMade(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wake")
	r := startLoop(t, dir, time.Hour, nil)
	if p := r.waitPasses(1, 5*time.Second); p[0].reason != reasonStartup {
		t.Fatalf("the first pass was %v", p[0].reason)
	}
	if !strings.Contains(r.said(), "not watching") {
		t.Fatalf("the missing directory was not said: %s", r.said())
	}
	if err := os.Mkdir(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	markWake(t, dir)
	p := r.waitPasses(2, 2*watchRetry)
	if p[1].reason != reasonWatcher {
		t.Fatalf("the first pass once the directory was made was %v", p[1].reason)
	}
	r.still(2, 300*time.Millisecond)
}

// A file where the directory should be is no directory to watch.
func TestAFileIsNoWakeDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "wake")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := watchDir(file); err == nil {
		t.Fatalf("a file was watched as a wake directory")
	}
}

// A timer pass with nothing to say prints nothing; any other pass opens
// with its reason.
func TestATimerPassSaysNothingUnlessItHasSomethingToSay(t *testing.T) {
	var b strings.Builder
	newPassOutput(&b, reasonTimer)
	if b.String() != "" {
		t.Fatalf("an idle timer pass said %q", b.String())
	}
	io.WriteString(newPassOutput(&b, reasonTimer), "c1 -> refs/heads/main: land\n")
	newPassOutput(&b, reasonDue)
	if want := "pass: timer\nc1 -> refs/heads/main: land\npass: due\n"; b.String() != want {
		t.Fatalf("the passes said %q, not %q", b.String(), want)
	}
}
