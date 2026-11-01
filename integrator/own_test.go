//go:build linux

package main

// Which controller serves a repository, over real directories, flock and
// inotify, with the competing controller a second process. The hooks
// ownLocked and watchOpened stop a start at the boundaries where a
// directory can be replaced under it.

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHelperHoldsARepository is not a test. Run as a second process, with
// VALLEY_TEST_HOLD naming a repository, it takes the repository's lock the
// way a controller does, says "held" or "refused", and holds the lock
// until its standard input closes.
func TestHelperHoldsARepository(t *testing.T) {
	repo := os.Getenv("VALLEY_TEST_HOLD")
	if repo == "" {
		t.Skip("run by the tests below as a second controller")
	}
	r, err := ownRepo(repo)
	if err != nil {
		os.Stdout.WriteString("refused: " + err.Error() + "\n")
		os.Exit(3)
	}
	os.Stdout.WriteString("held\n")
	io.Copy(io.Discard, os.Stdin)
	r.Close()
	os.Exit(0)
}

// otherController is a second process taking repo's lock.
type otherController struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	said  string
}

// startOther starts a second controller over repo and waits for what it
// says about the lock.
func startOther(t *testing.T, repo string) *otherController {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsARepository$")
	cmd.Env = append(os.Environ(), "VALLEY_TEST_HOLD="+repo)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	o := &otherController{cmd: cmd, stdin: stdin}
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	o.said = strings.TrimSpace(line)
	t.Cleanup(o.stop)
	return o
}

func (o *otherController) stop() {
	o.stdin.Close()
	o.cmd.Wait()
}

func repoDir(t *testing.T) string {
	repo := filepath.Join(t.TempDir(), "app.git")
	if err := os.Mkdir(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	return repo
}

// serveRecorded starts a controller over repo whose sweep and loop record
// that they ran.
func serveRecorded(repo string) (err error, swept, looped bool) {
	err = serve(repo, func(string) { swept = true }, func(func() error) error { looped = true; return nil })
	return err, swept, looped
}

// A second process over a repository a controller serves is refused, and a
// controller started over a repository a second process holds neither
// sweeps nor passes.
func TestOneControllerServesARepository(t *testing.T) {
	repo := repoDir(t)
	held, err := ownRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	if o := startOther(t, repo); !strings.HasPrefix(o.said, "refused: ") || !strings.Contains(o.said, "another controller holds its lock") {
		t.Fatalf("a second process over a served repository said %q", o.said)
	}
	held.Close()

	o := startOther(t, repo)
	if o.said != "held" {
		t.Fatalf("the second process said %q over a repository nobody served", o.said)
	}
	err, swept, looped := serveRecorded(repo)
	if !errors.Is(err, errLockHeld) || swept || looped {
		t.Fatalf("a controller refused the repository returned %v, swept %v, looped %v", err, swept, looped)
	}
}

// A repository replaced between its open and the check is opened again,
// and the lock that is kept is on the directory the path names.
func TestARepositoryReplacedAsItIsLockedIsLockedAgain(t *testing.T) {
	repo := repoDir(t)
	moved := repo + ".moved"
	once := sync.Once{}
	ownLocked = func() {
		once.Do(func() {
			if err := os.Rename(repo, moved); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(repo, 0o750); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(func() { ownLocked = nil })
	held, err := ownRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ownLocked = nil
	if o := startOther(t, repo); !strings.HasPrefix(o.said, "refused: ") {
		t.Fatalf("the directory at the path was not the one locked: a second process said %q", o.said)
	}
	if o := startOther(t, moved); o.said != "held" {
		t.Fatalf("the directory moved away is still locked: a second process said %q", o.said)
	}
}

// The first lock is on a directory another takes the place of, and by the
// next try a second process holds the new one. The controller is refused,
// sweeps nothing, passes nothing, and holds nothing.
func TestAStartWhoseSecondTryFindsAnOwnerNeitherSweepsNorPasses(t *testing.T) {
	repo := repoDir(t)
	var other *otherController
	ownLocked = func() {
		if other != nil {
			return
		}
		if err := os.Rename(repo, repo+".moved"); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(repo, 0o750); err != nil {
			t.Error(err)
		}
		other = startOther(t, repo)
	}
	t.Cleanup(func() { ownLocked = nil })
	err, swept, looped := serveRecorded(repo)
	ownLocked = nil
	if other == nil || other.said != "held" {
		t.Fatalf("the second process did not take the new directory: %+v", other)
	}
	if !errors.Is(err, errLockHeld) || swept || looped {
		t.Fatalf("the start returned %v, swept %v, looped %v", err, swept, looped)
	}
	if o := startOther(t, repo+".moved"); o.said != "held" {
		t.Fatalf("the refused start kept the first directory locked: %q", o.said)
	}
}

// A path that names another directory at every try is no repository to
// serve, and the refusal is bounded.
func TestARepositoryReplacedEveryTimeIsNotServed(t *testing.T) {
	repo := repoDir(t)
	n := 0
	ownLocked = func() {
		n++
		os.Rename(repo, repo+".moved"+string(rune('0'+n)))
		os.Mkdir(repo, 0o750)
	}
	t.Cleanup(func() { ownLocked = nil })
	err, swept, looped := serveRecorded(repo)
	if err == nil || swept || looped || n != ownTries {
		t.Fatalf("the start returned %v after %d tries, swept %v, looped %v", err, n, swept, looped)
	}
}

// A repository whose path comes to name another directory while the
// controller runs stops the loop before its next pass.
func TestAControllerWhoseRepositoryIsReplacedStops(t *testing.T) {
	repo := repoDir(t)
	dir := wakeDir(t)
	passes := 0
	err := serve(repo, func(string) {}, func(owned func() error) error {
		l := &wakeLoop{dir: dir, interval: time.Hour, stderr: io.Discard, watch: watchDir, owned: owned,
			pass: func(passReason) (bool, error) {
				passes++
				os.Rename(repo, repo+".moved")
				os.Mkdir(repo, 0o750)
				markWake(t, dir)
				return false, nil
			}}
		return l.run()
	})
	if err == nil || !strings.Contains(err.Error(), "no longer the directory this controller locked") || passes != 1 {
		t.Fatalf("a controller whose repository was replaced returned %v after %d passes", err, passes)
	}
}

// failingWatch is a watch that ends at once, for a reason other than the
// directory leaving, as an inotify read error would end one.
type failingWatch struct{ end chan error }

func (f failingWatch) events() <-chan struct{} { return nil }
func (f failingWatch) failed() <-chan error    { return f.end }
func (f failingWatch) removeDue() error        { return nil }
func (f failingWatch) hasDue() bool            { return false }
func (f failingWatch) close()                  {}

// A watch that fails leaves the controller passing on its interval, still
// serving the repository: a second process is refused it meanwhile.
func TestAFailedWatchKeepsTheRepository(t *testing.T) {
	repo := repoDir(t)
	dir := wakeDir(t)
	stop := make(chan struct{})
	done := make(chan error, 1)
	var stderr strings.Builder
	var mu sync.Mutex
	go func() {
		done <- serve(repo, func(string) {}, func(owned func() error) error {
			l := &wakeLoop{dir: dir, interval: time.Hour, stderr: lockedWriter{&mu, &stderr}, stop: stop, owned: owned,
				watch: func(string) (watcher, error) {
					end := make(chan error, 1)
					end <- errors.New("reading its events: input/output error")
					return failingWatch{end}, nil
				},
				pass: func(passReason) (bool, error) { return false, nil }}
			return l.run()
		})
	}()
	time.Sleep(300 * time.Millisecond)
	if o := startOther(t, repo); !strings.HasPrefix(o.said, "refused: ") {
		t.Fatalf("while its watch had failed, a second process could take the repository: %q", o.said)
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(stderr.String(), "input/output error") {
		t.Fatalf("the failed watch was not said: %s", stderr.String())
	}
}

// A wake directory replaced between its open and its watch is not watched:
// the path names another directory. The next try watches the directory at
// the path, and a due left in the one moved away is not consumed.
func TestAWakeDirectoryReplacedAsItIsWatchedIsWatchedAgain(t *testing.T) {
	dir := wakeDir(t)
	moved := dir + ".moved"
	once := sync.Once{}
	watchOpened = func() {
		once.Do(func() {
			if err := os.Rename(dir, moved); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(dir, 0o770); err != nil {
				t.Error(err)
			}
			markWake(t, moved)
		})
	}
	t.Cleanup(func() { watchOpened = nil })
	r := startLoop(t, dir, time.Hour, nil)
	if p := r.waitPasses(1, 5*time.Second); p[0].reason != reasonStartup {
		t.Fatalf("the first pass was %v", p[0].reason)
	}
	if !strings.Contains(r.said(), "another directory took its place") {
		t.Fatalf("the replacement was not said: %s", r.said())
	}
	if p := r.waitPasses(2, 2*watchRetry); p[1].reason != reasonWatcher {
		t.Fatalf("the pass once the directory at the path was watched was %v", p[1].reason)
	}
	markWake(t, dir)
	if p := r.waitPasses(3, 5*time.Second); p[2].reason != reasonDue {
		t.Fatalf("a mark in the directory at the path started a %v pass", p[2].reason)
	}
	if _, err := os.Lstat(filepath.Join(moved, "due")); err != nil {
		t.Fatalf("the due in the directory moved away was consumed: %v", err)
	}
}

// A second controller given the same wake directory consumes none of its
// marks, and passes at its interval. The first goes on being woken.
func TestASecondConsumerOfAWakeDirectoryConsumesNothing(t *testing.T) {
	dir := wakeDir(t)
	first := startLoop(t, dir, time.Hour, nil)
	first.waitPasses(1, 5*time.Second)
	second := startLoop(t, dir, time.Hour, nil)
	second.waitPasses(1, 5*time.Second)
	if !strings.Contains(second.said(), "another controller consumes its marks") {
		t.Fatalf("the second consumer said: %s", second.said())
	}
	markWake(t, dir)
	if p := first.waitPasses(2, 5*time.Second); p[1].reason != reasonDue {
		t.Fatalf("the first consumer passed for %v", p[1].reason)
	}
	second.still(1, 300*time.Millisecond)
}
