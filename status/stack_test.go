package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stack lists a change's layers bottom first, each on the one below it,
// the bottom on its merge base with main: a branch, a draft and a ready
// change under a ready change. A branch beside the stack and a change
// above it are not layers. A change with nothing under it is one line.
func TestStackListsLayersBottomFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
	origin, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "--quiet", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	git("config", "user.name", "stack-test")
	git("config", "user.email", "stack-test@localhost")
	git("config", "valley.principal", "klaus-env")
	git("checkout", "--quiet", "-b", "main")
	git("commit", "--quiet", "--allow-empty", "-m", "base")
	git("push", "--quiet", "origin", "main")
	git("commit", "--quiet", "--allow-empty", "-m", "main moves on")
	git("push", "--quiet", "origin", "main")
	base := git("rev-parse", "main~1")

	heads := map[string]string{}
	from := "main~1"
	for _, b := range []string{"one", "two", "three", "four", "five"} {
		git("checkout", "--quiet", "-b", b, from)
		git("commit", "--quiet", "--allow-empty", "-m", "layer "+b)
		git("push", "--quiet", "origin", b)
		heads[b], from = git("rev-parse", "HEAD"), b
	}
	git("checkout", "--quiet", "-b", "beside", "one")
	git("commit", "--quiet", "--allow-empty", "-m", "beside the stack")
	git("push", "--quiet", "origin", "beside")

	t.Chdir(work)
	run1 := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("valley-go %s: %s", strings.Join(args, " "), stderr.String())
		}
		return stdout.String()
	}
	run1("ready", "--change", strings.Repeat("q", 32), "two")
	run1("ready", "--change", strings.Repeat("r", 32), "three")
	run1("unready", strings.Repeat("r", 32))
	run1("ready", "--change", strings.Repeat("s", 32), "four")
	run1("ready", "--change", strings.Repeat("t", 32), "five")

	want := strings.Join([]string{
		"branch\tone\t" + base + "\t" + heads["one"] + "\t-\tlayer one",
		"change\tqqqq\t" + heads["one"] + "\t" + heads["two"] + "\tready\tlayer two",
		"change\trrrr\t" + heads["two"] + "\t" + heads["three"] + "\tdraft\tlayer three",
		"change\tssss\t" + heads["three"] + "\t" + heads["four"] + "\tready\tlayer four",
		"",
	}, "\n")
	if got := run1("stack", "ssss"); got != want {
		t.Errorf("stack ssss:\n%s\nwant:\n%s", got, want)
	}
	// Named by its branch, the change is the same stack.
	if got := run1("stack", "four"); got != want {
		t.Errorf("stack four:\n%s\nwant:\n%s", got, want)
	}
	if got, want := run1("stack", "one"), "branch\tone\t"+base+"\t"+heads["one"]+"\t-\tlayer one\n"; got != want {
		t.Errorf("stack one: %q, want %q", got, want)
	}
}
