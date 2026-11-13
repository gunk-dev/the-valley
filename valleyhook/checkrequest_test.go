package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/change"
)

// requestRef is the ref of a check request filed by who, its attempt
// numbered n.
func requestRef(who string, n int) string {
	return fmt.Sprintf("%s%s/0123456789abcdef/%016x", change.CheckRequestPrefix, who, n)
}

// A named principal files, moves and withdraws check requests under its
// own name and no other's, and a holder of the approve grant withdraws
// anyone's. A request points at a commit the repository holds, under a
// name of exactly the documented shape, and the symbolic-ref, lookalike
// and protection rules every ref meets still apply to it.
func TestAPrincipalFilesCheckRequestsUnderItsOwnName(t *testing.T) {
	r := newRecords(t)
	head, other := r.source("one"), r.source("two")
	tree := r.git("", "mktree")
	blob := r.blob("not a commit\n")
	mine, theirs := requestRef("alice", 1), requestRef("bob", 1)

	r.expect("a create under its own name", "alice", mine, zeroID, head, "")
	r.expect("a move under its own name", "alice", mine, head, other, "")
	r.expect("a delete under its own name", "alice", mine, head, zeroID, "")
	r.expect("a create under another's name", "alice", theirs, zeroID, head, "files check requests under its own name, refs/the-valley/check-requests/alice/")
	r.expect("a move under another's name", "alice", theirs, head, other, "under its own name")
	r.expect("a delete under another's name", "alice", theirs, head, zeroID, "deleted by the principal it is filed under or by a holder of the approve grant")
	r.expect("an approve holder's delete", "operator", theirs, head, zeroID, "")
	r.expect("an approve holder's create under another's name", "operator", theirs, zeroID, head, "under its own name")
	r.expect("an approve holder's move under another's name", "operator", theirs, head, other, "under its own name")
	r.expect("an untagged key", "", mine, zeroID, head, "a key that names no principal")
	r.expect("a tree", "alice", mine, zeroID, tree, "is a tree, and a check request points at a commit")
	r.expect("a blob", "alice", mine, head, blob, "is a blob, and a check request points at a commit")
	r.expect("an object the repository lacks", "alice", mine, zeroID, strings.Repeat("1", 40), "does not hold")

	const worktree, attempt = "0123456789abcdef", "fedcba9876543210"
	for _, ref := range []string{
		change.CheckRequestPrefix + "alice/" + worktree,
		change.CheckRequestPrefix + "alice/" + worktree + "/" + attempt + "/x",
		change.CheckRequestPrefix + "alice/" + worktree[:15] + "/" + attempt,
		change.CheckRequestPrefix + "alice/" + worktree + "/" + attempt + "0",
		change.CheckRequestPrefix + "alice/" + worktree + "/" + strings.ToUpper(attempt),
		change.CheckRequestPrefix + "alice/" + worktree + "/" + attempt[:15] + "g",
		change.CheckRequestPrefix + "-alice/" + worktree + "/" + attempt,
		change.CheckRequestPrefix + worktree + "/" + attempt,
		change.CheckRequestPrefix + "alice//" + attempt,
	} {
		r.expect("the malformed "+ref, "alice", ref, zeroID, head, "a check request's ref is refs/the-valley/check-requests/<principal>/<worktree>/<attempt>")
		r.expect("a delete of the malformed "+ref, "operator", ref, head, zeroID, "a check request's ref is")
	}

	r.p.symrefs = map[string]bool{mine: true}
	r.expect("a symbolic ref", "alice", mine, zeroID, head, "symbolic ref")
	r.p.symrefs = nil
	r.expect("a lookalike", "alice", "refs/"+mine, zeroID, head, "named refs/ followed by a full ref name")
	r.p.push = pushPolicy{Refs: []string{change.CheckRequestPrefix + "*"}, Writers: []string{"operator"}}
	r.expect("a protected request", "alice", mine, zeroID, head, "a protected ref of project")
}

// A principal holds at most maxCheckRequests requests in a repository,
// counted with the ones the push creates. Moves and deletes create none,
// and another principal's requests count for that principal alone.
func TestAPrincipalHoldsABoundedNumberOfCheckRequests(t *testing.T) {
	r := newRecords(t)
	head, other := r.source("one"), r.source("two")
	policyFile := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"refs": [], "writers": [], "grants": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	push := func(who string, lines []string) (int, string) {
		var stderr bytes.Buffer
		code := preReceive([]string{"--project", "project", "--policy", policyFile, "--principal", who},
			strings.NewReader(strings.Join(lines, "\n")+"\n"), &stderr, gitRepository{})
		return code, stderr.String()
	}
	creates := func(who string, from, n int) []string {
		var lines []string
		for i := from; i < from+n; i++ {
			lines = append(lines, zeroID+" "+head+" "+requestRef(who, i))
		}
		return lines
	}
	install := func(who string, from, n int) {
		var refs strings.Builder
		for i := from; i < from+n; i++ {
			fmt.Fprintf(&refs, "create %s %s\n", requestRef(who, i), head)
		}
		r.git(refs.String(), "update-ref", "--stdin")
	}

	code, says := push("alice", creates("alice", 0, maxCheckRequests+1))
	if code == 0 || !strings.Contains(says, "alice holds 0 check requests in project, and this push would create 33 more, past the 32 one principal may hold") {
		t.Errorf("33 creates in one push: exit %d, %q", code, says)
	}
	if code, says := push("alice", creates("alice", 0, maxCheckRequests)); code != 0 {
		t.Errorf("32 creates in one push: exit %d, %q", code, says)
	}

	install("alice", 0, 30)
	install("bob", 0, 40)
	code, says = push("alice", creates("alice", 30, 3))
	if code == 0 || !strings.Contains(says, "alice holds 30 check requests in project, and this push would create 3 more") {
		t.Errorf("three past 30: exit %d, %q", code, says)
	}
	if code, says := push("alice", creates("alice", 30, 2)); code != 0 {
		t.Errorf("two past 30, beside another principal's 40: exit %d, %q", code, says)
	}

	install("alice", 30, 2)
	moves := []string{head + " " + other + " " + requestRef("alice", 0), head + " " + zeroID + " " + requestRef("alice", 1)}
	if code, says := push("alice", moves); code != 0 {
		t.Errorf("a move and a delete at the bound: exit %d, %q", code, says)
	}
	code, says = push("alice", creates("alice", 32, 1))
	if code == 0 || !strings.Contains(says, "alice holds 32 check requests") {
		t.Errorf("a create at the bound: exit %d, %q", code, says)
	}
}
