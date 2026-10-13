package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushTo runs preReceive over the lines as git would hand them to the
// hook, as who, against repo, under a policy that protects nothing.
func pushTo(t *testing.T, repo repository, who string, lines []string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	policyFile := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"refs": [], "writers": [], "grants": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := preReceive([]string{"--project", "project", "--policy", policyFile, "--principal", who},
		strings.NewReader(strings.Join(lines, "\n")+"\n"), &stderr, repo)
	return code, stderr.String()
}

// A push names as many refs as its sender likes. The hook keeps at most
// maxUpdates of them, so it asks git about at most that many.
func TestAPushIsBoundInTheRefsItUpdates(t *testing.T) {
	r := newRecords(t)
	h := r.source("one")
	topics := func(n int) []string {
		var lines []string
		for i := 0; i < n; i++ {
			lines = append(lines, fmt.Sprintf("%s %s refs/heads/topic/%04d", zeroID, h, i))
		}
		return lines
	}

	// One past the bound is refused before any ref is looked up: this
	// repository fails the test if one is.
	code, says := pushTo(t, unreadable{t}, "alice", topics(maxUpdates+1))
	if code == 0 || !strings.Contains(says, "it updates more than 1000 refs, the most one push may") {
		t.Errorf("1001 updates: exit %d, %q", code, says)
	}
	if code, says := pushTo(t, gitRepository{}, "alice", topics(maxUpdates)); code != 0 {
		t.Errorf("1000 updates: exit %d, %q", code, says)
	}

	// A symbolic ref among many is still found, in every form git reads
	// as one: written by git symbolic-ref, naming a ref that does not exist,
	// "ref:" followed by a tab or by nothing, and a filesystem symlink.
	// Each would have a push write the ref it names.
	r.git("", "update-ref", "refs/heads/real", h)
	r.git("", "symbolic-ref", "refs/heads/alias", "refs/heads/real")
	r.git("", "symbolic-ref", "refs/heads/dangling", "refs/heads/main")
	heads := filepath.Join(r.dir, "refs", "heads")
	for name, content := range map[string]string{
		"tabbed": "ref:\trefs/heads/real\n",
		"tight":  "ref:refs/heads/real\n",
	} {
		if err := os.WriteFile(filepath.Join(heads, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("refs/heads/real", filepath.Join(heads, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"alias", "dangling", "tabbed", "tight", "linked"} {
		ref := "refs/heads/" + alias
		lines := append(topics(maxUpdates-1), fmt.Sprintf("%s %s %s", zeroID, h, ref))
		code, says := pushTo(t, gitRepository{}, "alice", lines)
		want := "valley: alice may not write " + ref + " — it is a symbolic ref in this repository, and a push to it would write the ref it points at instead"
		if code == 0 || !strings.Contains(says, want) {
			t.Errorf("%s among %d updates: exit %d, %q", ref, len(lines), code, says)
		}
		if strings.Count(says, "\n") != 1 {
			t.Errorf("%s among %d updates: refused more than the symbolic ref: %q", ref, len(lines), says)
		}
	}
}
