package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A git tree can name the same subtree any number of times, so a handful
// of small objects can describe more paths than any walk could visit: the
// deep tree below is nine trees and one blob, and describes 64^9 paths.
// Each case is a tree like that pushed as an attestation, and each has to
// be refused at a bound, quickly, rather than walked.
func TestAWalkStopsAtItsBoundsBeforeReadingPastThem(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "", "init", "--quiet", "--bare", dir)
	t.Setenv("GIT_DIR", dir)

	note := gitIn(t, dir, "a note\n", "hash-object", "-w", "--stdin")
	leaf := mktree(t, dir, 1, "100644 blob "+note, "statement.note")
	sharing := func(child string, levels, width int) string {
		id := child
		for i := 0; i < levels; i++ {
			id = mktree(t, dir, width, "040000 tree "+id, "t")
		}
		return id
	}
	big := gitIn(t, dir, strings.Repeat("x", maxNoteSize+1), "hash-object", "-w", "--stdin")

	for _, c := range []struct {
		what, tree, says string
	}{
		{"one subtree named 64 times over, nine levels deep", sharing(leaf, 9, 64), "deeper than"},
		{"one subtree named 64 times over, within the depth", sharing(leaf, 2, 64), "entries in all"},
		{"a tree wider than any attestation's", mktree(t, dir, maxEntriesInTree+1, "100644 blob "+note, "n"), "more than 64 entries"},
		{"a note larger than any statement", mktree(t, dir, 1, "100644 blob "+big, "statement.note"), "more than the 65536"},
		{"an executable where a note goes", mktree(t, dir, 1, "100755 blob "+note, "statement.note"), "mode 100755"},
	} {
		start := time.Now()
		_, err := gitRepository{}.notes(c.tree)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: error = %v, want one saying %q", c.what, err, c.says)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%s: refused only after %s", c.what, elapsed)
		}
	}

	// And what attest writes is read whole.
	attested := mktree(t, dir, 1, "040000 tree "+leaf, "prose-format")
	notes, err := gitRepository{}.notes(attested)
	if err != nil || string(notes["prose-format/statement.note"]) != "a note\n" {
		t.Errorf("an attestation's tree: notes = %q, error = %v", notes, err)
	}
}

// mktree writes a tree of n entries, each "<entry>\t<name><index>".
func mktree(t *testing.T, dir string, n int, entry, name string) string {
	t.Helper()
	var lines []string
	for i := 0; i < n; i++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf("%03d", i)
		}
		lines = append(lines, entry+"\t"+name+suffix)
	}
	return gitIn(t, dir, strings.Join(lines, "\n")+"\n", "mktree")
}

func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
