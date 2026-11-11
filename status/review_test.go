package main

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// A review's outcome on real repositories: a plain branch, topic, on a
// scratch origin, reviewed off a terminal with its answers read from a
// file or a pipe. [a]pprove's attempt is given at its Go boundary, since
// attempting needs the attest helper.
type reviewRepo struct {
	t              *testing.T
	dir, work, tmp string
	head           string // topic's head
	stdout         *watched
	stderr         bytes.Buffer
	ended          chan stepOutcome // what the step's end path was given
	endStatus      int
}

func newReviewRepo(t *testing.T) *reviewRepo {
	dir := t.TempDir()
	r := &reviewRepo{t: t, dir: dir, work: filepath.Join(dir, "work"), tmp: filepath.Join(dir, "tmp"),
		stdout: &watched{}, ended: make(chan stepOutcome, 1)}
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	t.Setenv("VALLEY_INTEGRATOR_SIGNER", "")
	t.Setenv("TMPDIR", r.tmp)
	if err := os.Mkdir(r.tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(dir, "origin.git")
	for _, args := range [][]string{{"init", "--quiet", "--bare", "--initial-branch=main", origin}, {"clone", "--quiet", origin, r.work}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}
	}
	r.git("config", "user.name", "review-test")
	r.git("config", "user.email", "review-test@localhost")
	r.git("checkout", "--quiet", "-b", "main")
	r.git("commit", "--quiet", "--allow-empty", "-m", "base")
	r.git("push", "--quiet", "origin", "main")
	r.git("checkout", "--quiet", "-b", "topic")
	if err := os.WriteFile(filepath.Join(r.work, "readme.md"), []byte("a readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git("add", "readme.md")
	r.git("commit", "--quiet", "-m", "a readme")
	r.git("push", "--quiet", "origin", "topic")
	r.git("checkout", "--quiet", "main")
	r.head = r.git("rev-parse", "topic")
	return r
}

func (r *reviewRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.work
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// review is a review of topic, from stdin, with attempt as [a]pprove's
// attempt, and the test's catch of SIGINT and SIGTERM as the caller's.
// The step's end path keeps what it was given and leaves the goroutine it
// was called on, as it would leave the process.
func (r *reviewRepo) review(stdin *os.File, attempt func(*approval)) *review {
	g := git{env: gitEnvironment(os.Environ()), stderr: &r.stderr, dir: r.work}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	r.t.Cleanup(func() { signal.Stop(interrupts) })
	rv := newReview(reviewStep{g: g, stdin: stdin, stdout: r.stdout, stderr: &r.stderr,
		signers: &signerSource{g: g, stderr: &r.stderr}, interrupts: interrupts,
		end: func(status int, o stepOutcome) {
			r.endStatus = status
			r.ended <- o
			runtime.Goexit()
		}})
	rv.attempt = attempt
	return rv
}

// answers is a file holding the answers given.
func (r *reviewRepo) answers(text string) *os.File {
	r.t.Helper()
	path := filepath.Join(r.dir, "answers")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { f.Close() })
	return f
}

// leftBehind fails the test when the review left anything in its TMPDIR.
func (r *reviewRepo) leftBehind() {
	r.t.Helper()
	if left, _ := os.ReadDir(r.tmp); len(left) > 0 {
		r.t.Errorf("the review left %s in its TMPDIR", left[0].Name())
	}
}

// watched is a review's stdout, which says when it first shows a text.
type watched struct {
	mu   sync.Mutex
	b    bytes.Buffer
	want string
	seen chan struct{}
}

func (w *watched) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.b.Write(p)
	if w.seen != nil && strings.Contains(w.b.String(), w.want) {
		close(w.seen)
		w.seen = nil
	}
	return n, err
}

// shows is closed once the output shows text.
func (w *watched) shows(text string) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.want, w.seen = text, make(chan struct{})
	return w.seen
}

func (w *watched) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// An approval of a plain branch ends the review approved, with the head
// review showed and bound the attempt to, and no record's ID, handle or
// version.
func TestReviewApprovedAtTheHeadShown(t *testing.T) {
	r := newReviewRepo(t)
	o := r.review(r.answers("a\n"), func(a *approval) {
		if a.bound != r.head || a.branch != "topic" || a.id != "" {
			t.Errorf("the attempt was bound to %q on %q, change %q, not to topic at %s", a.bound, a.branch, a.id, r.head)
		}
		a.ended(nil)
	}).review("topic")
	if want := (stepOutcome{kind: "approved", head: r.head}); o != want {
		t.Fatalf("the review ended %+v, not %+v\n%s%s", o, want, r.stdout, r.stderr.String())
	}
	r.leftBehind()
}

// An attempt whose checks failed, and one whose request's state could not
// be established after the push, are refusals: the prompt comes back, and
// a review then skipped ends refused, with status 0.
func TestReviewRefusedAfterTheAttempt(t *testing.T) {
	for name, fail := range map[string]func(*approval){
		"checks failed": func(a *approval) { a.failedAt = a.bound; a.ended(errRefused) },
		"unknown":       func(a *approval) { a.ended(errUnknown) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newReviewRepo(t)
			o := r.review(r.answers("a\ns\n"), fail).review("topic")
			if o != (stepOutcome{kind: "refused"}) {
				t.Fatalf("the review ended %+v, not refused\n%s%s", o, r.stdout, r.stderr.String())
			}
			if !strings.Contains(r.stdout.String(), "skipped topic; nothing done") {
				t.Fatalf("the prompt did not come back to be skipped:\n%s", r.stdout)
			}
			r.leftBehind()
		})
	}
}

// An attempt a signal interrupts ends the review through the step's end
// path, interrupted with status 130, once the review has cleaned up.
func TestReviewInterruptedDuringTheAttempt(t *testing.T) {
	r := newReviewRepo(t)
	rv := r.review(r.answers("a\n"), func(a *approval) { a.interrupt() })
	go rv.review("topic")
	if o := <-r.ended; o.kind != "interrupted" || r.endStatus != interruptedStatus {
		t.Fatalf("the review ended %+v with %d, not interrupted with 130", o, r.endStatus)
	}
	r.leftBehind()
}

// SIGTERM at the prompt ends the review through the step's end path,
// interrupted with status 130, and an input that ends then is not read as
// another ending.
func TestReviewInterruptedAtThePrompt(t *testing.T) {
	r := newReviewRepo(t)
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	prompt := r.stdout.shows("[s]kip")
	rv := r.review(in, func(*approval) { t.Error("the review attempted an approval") })
	reviewed := make(chan struct{})
	go func() {
		defer close(reviewed)
		rv.review("topic")
	}()
	<-prompt
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if o := <-r.ended; o.kind != "interrupted" || r.endStatus != interruptedStatus {
		t.Fatalf("the review ended %+v with %d, not interrupted with 130", o, r.endStatus)
	}
	out.Close()
	<-reviewed
	r.leftBehind()
}
