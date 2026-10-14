package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// resolve prints a name no record is on as a branch only when it is a
// valid branch name in UTF-8. bin/valley splits the line resolve prints
// on its separators, so a name holding one, or a tab, would read as some
// other branch. Such a name is refused, and nothing is printed.
func TestResolveRefusesANameThatIsNoBranch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	origin, work := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	sh := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("git", "init", "--quiet", "--bare", "--initial-branch=main", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "clone", "--quiet", origin, work).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	sh("config", "user.name", "valley-go-test")
	sh("config", "user.email", "valley-go-test@localhost")
	sh("checkout", "--quiet", "-b", "main")
	sh("commit", "--quiet", "--allow-empty", "-m", "base")
	sh("push", "--quiet", "origin", "main")
	t.Chdir(work)

	for _, name := range []string{"main\x1fjunk", "main\tjunk", "origin/main\x1fjunk", "topic/\xff", "a..b", "with space"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"resolve", name}, &stdout, &stderr); code == 0 {
			t.Errorf("resolve %q was accepted: %q", name, stdout.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("resolve %q printed %q", name, stdout.String())
		}
		if !strings.Contains(stderr.String(), "is not a branch name in UTF-8") {
			t.Errorf("the refusal of %q did not say why: %q", name, stderr.String())
		}
	}
	for name, want := range map[string]string{"main": "branch\tmain\n", "origin/main": "branch\tmain\n"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"resolve", name}, &stdout, &stderr); code != 0 {
			t.Fatalf("resolve %q: %s", name, stderr.String())
		}
		if stdout.String() != want {
			t.Errorf("resolve %q printed %q, where %q is expected", name, stdout.String(), want)
		}
	}
}
