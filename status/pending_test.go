package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pending fits its text to 80 columns when stdout is no terminal: a long
// title is cut short with …, and a branch or a handle is printed whole,
// even where the line it is on cannot fit. Nothing it prints is an escape
// code.
func TestPendingFitsEightyColumnsAndKeepsNamesWhole(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VALLEY_KNOWN_SIGNERS", "")
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
	sh("config", "user.name", "a committer whose name is so long that the line it is on has to cut it short")
	sh("config", "user.email", "committer@localhost")
	sh("config", "valley.principal", "klaus-env")
	sh("checkout", "--quiet", "-b", "main")
	sh("commit", "--quiet", "--allow-empty", "-m", "base")
	sh("push", "--quiet", "origin", "main")

	long := "a-branch-whose-name-runs-on-well-past-the-eightieth-column-of-any-terminal-it-is-shown-in"
	title := "a subject that says far more than one line has room for, " + strings.Repeat("and then some more ", 4)
	sh("checkout", "--quiet", "-b", long, "main")
	sh("commit", "--quiet", "--allow-empty", "-m", title+"on the long branch")
	sh("push", "--quiet", "origin", long)
	sh("checkout", "--quiet", "-b", "short", "main")
	sh("commit", "--quiet", "--allow-empty", "-m", title)
	sh("push", "--quiet", "origin", "short")

	t.Chdir(work)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"ready", "--change", strings.Repeat("q", 32), "short"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ready: %s", stderr.String())
	}
	stdout.Reset()
	if code := run([]string{"pending"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pending: %s", stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("pending printed an escape code to no terminal:\n%s", out)
	}
	if !strings.Contains(out, "\n"+long+"\n") {
		t.Errorf("the long branch is not whole, on a line of its own:\n%s", out)
	}
	if !strings.Contains(out, "\nqqqq v1  ") {
		t.Errorf("the change is not named by its whole handle and version:\n%s", out)
	}
	cut := 0
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.Contains(line, long) {
			continue
		}
		if columns(line) > 80 {
			t.Errorf("a line is %d columns wide: %q", columns(line), line)
		}
		if strings.HasPrefix(line, "  a subject that says") {
			if columns(line) != 80 || !strings.HasSuffix(line, "…") {
				t.Errorf("a long title is not cut to 80 columns with …: %q", line)
			}
			cut++
		}
		if strings.HasPrefix(line, "qqqq v1") && !strings.HasSuffix(line, "…") {
			t.Errorf("the author is not cut short to keep the line within 80 columns: %q", line)
		}
	}
	if cut != 2 {
		t.Errorf("%d titles were cut short, where the change's and the branch's should be:\n%s", cut, out)
	}
}

// fit cuts by columns, not bytes: a wide character takes two, and a
// combining mark none.
func TestFitCountsColumns(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"one word too many", 12, "one word to…"},
		{"漢字漢字漢字", 7, "漢字漢…"},
		{"café au lait", 5, "café au lait"[:6] + "…"},
	} {
		if got := fit(c.s, c.n); got != c.want || columns(got) > c.n {
			t.Errorf("fit(%q, %d) = %q, %d columns; want %q", c.s, c.n, got, columns(got), c.want)
		}
	}
}
