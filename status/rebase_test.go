package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A SIGTERM while a hook blocks the rebase ends it: rebase exits 130 at
// once, though the post-checkout hook worktree add runs holds the stderr
// the verb was given. The hook is killed with the git that ran it, and the
// rebase leaves no worktree, no ref and no temporary directory, and moves
// nothing on origin.
func TestRebaseSignalledWhileAHookBlocks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	origin, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(dir, "init", "--quiet", "--bare", "--initial-branch=main", origin)
	git(dir, "clone", "--quiet", origin, work)
	git(work, "config", "user.name", "rebase-test")
	git(work, "config", "user.email", "rebase-test@localhost")
	git(work, "config", "valley.principal", "rebaser")
	git(work, "checkout", "--quiet", "-b", "main")
	git(work, "commit", "--quiet", "--allow-empty", "-m", "base")
	git(work, "push", "--quiet", "origin", "main")
	git(work, "checkout", "--quiet", "-b", "one")
	git(work, "commit", "--quiet", "--allow-empty", "-m", "layer one")
	git(work, "push", "--quiet", "origin", "one")
	t.Chdir(work)
	id := strings.Repeat("q", 32)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ready", "--change", id, "one"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ready: %s", stderr.String())
	}
	git(work, "checkout", "--quiet", "main")
	git(work, "commit", "--quiet", "--allow-empty", "-m", "main moves on")
	git(work, "push", "--quiet", "origin", "main")
	before := git(origin, "for-each-ref")

	// The hook says it has started, and then beats for a minute: it adds
	// a line to a file every tenth of a second.
	started, beats := filepath.Join(dir, "started"), filepath.Join(dir, "beats")
	hook := fmt.Sprintf("#!/bin/sh\n[ -e '%s' ] && exit 0\n: > '%s'\n"+
		"i=0\nwhile [ $i -lt 600 ]; do echo >> '%s'; sleep 0.1; i=$((i+1)); done\n", started, started, beats)
	if err := os.WriteFile(filepath.Join(work, ".git", "hooks", "post-checkout"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)

	stdout.Reset()
	stderr.Reset()
	exited := make(chan int, 1)
	go func() { exited <- run([]string{"rebase", id}, &stdout, &stderr) }()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hook never started")
		}
	}
	self, _ := os.FindProcess(os.Getpid())
	if err := self.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		if code != 130 {
			t.Fatalf("rebase exited %d, not 130:\n%s%s", code, stdout.String(), stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("rebase did not end on SIGTERM")
	}

	size := func() int64 {
		fi, err := os.Stat(beats)
		if err != nil {
			return 0
		}
		return fi.Size()
	}
	time.Sleep(200 * time.Millisecond)
	was := size()
	time.Sleep(time.Second)
	if size() != was {
		t.Error("the hook outlived the rebase")
	}
	if kept := git(work, "for-each-ref", "refs/valley-rebase/"); kept != "" {
		t.Errorf("the rebase left a ref behind: %q", kept)
	}
	if out := git(work, "worktree", "list", "--porcelain"); strings.Count(out, "worktree ") != 1 {
		t.Errorf("the rebase left a worktree registered:\n%s", out)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the rebase left its worktree's directory: %s", left[0].Name())
	}
	if after := git(origin, "for-each-ref"); after != before {
		t.Errorf("an interrupted rebase moved something:\n%s\nnow:\n%s", before, after)
	}
}

// The manifest lines up the layers' names, says for each change whether a
// version was added, and gives the command that finishes each change whose
// branch may hold its rebased head with no version recording it: one
// pushed and left without a version, or one whose push may have reached
// origin. A plain branch is never unfinished, and neither is a change
// whose branch did not move.
func TestManifest(t *testing.T) {
	const (
		o1, n1 = "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "1111111bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		o2, n2 = "2222222aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		o3, n3 = "3333333aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "3333333bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		id2    = "qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
		id3    = "pppppppppppppppppppppppppppppppp"
	)
	stack := func(pushed string, added2, added3 int) []*layer {
		return []*layer{
			{name: "topic/one", branch: "topic/one", head: o1, rebased: n1, pushed: pushed},
			{name: "qqqq", id: id2, branch: "two", head: o2, rebased: n2, pushed: pushed, added: added2},
			{name: "pppp", id: id3, branch: "my branch", head: o3, rebased: n3, pushed: pushed, added: added3},
		}
	}
	for _, c := range []struct {
		name   string
		layers []*layer
		want   string
	}{
		{"finished", stack(pushDone, 2, 4), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one pushed
  qqqq       2222222 -> 2222222  origin/two pushed, v2 added
  pppp       3333333 -> 3333333  origin/my branch pushed, v4 added
`},
		{"one change left without a version", stack(pushDone, 0, 4), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one pushed
  qqqq       2222222 -> 2222222  origin/two pushed, no version added
  pppp       3333333 -> 3333333  origin/my branch pushed, v4 added
to finish qqqq: valley ready --head ` + n2 + ` --change ` + id2 + ` two
`},
		{"refused", stack(pushNot, 0, 0), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one not pushed
  qqqq       2222222 -> 2222222  origin/two not pushed, no version added
  pppp       3333333 -> 3333333  origin/my branch not pushed, no version added
`},
		{"not known", stack(pushUnknown, 0, 0), `the stack under pppp, bottom first:
  topic/one  1111111 -> 1111111  origin/topic/one may have moved
  qqqq       2222222 -> 2222222  origin/two may have moved, no version added
  pppp       3333333 -> 3333333  origin/my branch may have moved, no version added
to finish qqqq: valley ready --head ` + n2 + ` --change ` + id2 + ` two
to finish pppp: valley ready --head ` + n3 + ` --change ` + id3 + ` my\ branch
`},
	} {
		if got := manifest("pppp", c.layers); got != c.want {
			t.Errorf("%s:\n%s\nwant:\n%s", c.name, got, c.want)
		}
	}
}
