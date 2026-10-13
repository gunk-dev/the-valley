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
// maxUpdates of them, and asks git about them once, not once per ref.
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

	// A symbolic ref among many is still found, whether the ref it names
	// exists or not. A dangling one would create the ref it names.
	r.git("", "update-ref", "refs/heads/real", h)
	r.git("", "symbolic-ref", "refs/heads/alias", "refs/heads/real")
	r.git("", "symbolic-ref", "refs/heads/dangling", "refs/heads/main")
	for _, alias := range []string{"refs/heads/alias", "refs/heads/dangling"} {
		lines := append(topics(maxUpdates-1), fmt.Sprintf("%s %s %s", zeroID, h, alias))
		code, says := pushTo(t, gitRepository{}, "alice", lines)
		want := "valley: alice may not write " + alias + " — it is a symbolic ref in this repository, and a push to it would write the ref it points at instead"
		if code == 0 || !strings.Contains(says, want) {
			t.Errorf("%s among %d updates: exit %d, %q", alias, len(lines), code, says)
		}
		if strings.Count(says, "\n") != 1 {
			t.Errorf("%s among %d updates: refused more than the symbolic ref: %q", alias, len(lines), says)
		}
	}

	// Names git would refuse as refnames are never read as paths.
	symrefs, err := gitRepository{}.symbolic([]string{"refs/heads/../../HEAD", "refs/heads/real", "refs/heads/alias"})
	if err != nil || symrefs["refs/heads/../../HEAD"] || symrefs["refs/heads/real"] || !symrefs["refs/heads/alias"] {
		t.Errorf("symbolic = %v, %v", symrefs, err)
	}
}
