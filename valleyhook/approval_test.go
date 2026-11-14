package main

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/approval"
	"the-valley/change"
	"the-valley/note"
)

// TestMain lets the test binary stand in for the hook a real push runs:
// a repository whose hooks/pre-receive links to the binary runs
// preReceive with the arguments VALLEYHOOK_TEST_HOOK names.
func TestMain(m *testing.M) {
	if args := os.Getenv("VALLEYHOOK_TEST_HOOK"); args != "" {
		os.Exit(preReceive(strings.Split(args, "\n"), os.Stdin, os.Stderr, gitRepository{}))
	}
	os.Exit(m.Run())
}

var approvalKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))

// approvalNote is a signed approval of version of the change id landing
// on target, by principal.
func approvalNote(t *testing.T, id, target, principal string, version int) string {
	t.Helper()
	n, err := approval.Sign(approvalStatement(id, target, principal, version), approval.NewKey(approvalKey))
	if err != nil {
		t.Fatal(err)
	}
	return string(n)
}

func approvalStatement(id, target, principal string, version int) approval.Statement {
	return approval.Statement{
		Instance: strings.Repeat("5f", 32), Project: "project", Target: target, Change: id,
		Version: version, Head: change.HeadPrefix + strings.Repeat("2", 40),
		Record: change.HeadPrefix + strings.Repeat("3", 40), Principal: principal,
		Issued: time.Date(2026, 11, 6, 0, 43, 26, 0, time.UTC),
	}
}

// Only a holder of the approve grant, under either of its names, writes an
// approval: owning the change, the restack grant and a writer list confer
// nothing. An approval ref is named for a change and main, and points at
// one blob holding an approval note about that change and target, held to
// approval.MaxNote bytes before it is read. Whether its key may approve
// anything is not the hook's to judge.
func TestAnApprovalTakesTheApproveGrantAndHoldsOneNote(t *testing.T) {
	r := newRecords(t)
	r.p.grants["approver"] = map[string]bool{grantApprove: true}
	r.p.grants["restacker"] = map[string]bool{grantRestack: true}
	ref := approval.Ref("main", changeID)
	first := r.blob(approvalNote(t, changeID, "main", "operator", 1))
	second := r.blob(approvalNote(t, changeID, "main", "approver", 2))
	if ref != "refs/the-valley/approvals/main/c-"+changeID {
		t.Fatalf("the approval ref is %s", ref)
	}

	r.expect("a create by a holder of request", "operator", ref, zeroID, first, "")
	r.expect("a create by a holder of approve", "approver", ref, zeroID, second, "")
	r.expect("a replacement", "operator", ref, first, second, "")
	r.expect("a delete", "approver", ref, first, zeroID, "")
	for _, who := range []string{"alice", "restacker"} {
		says := "refs/the-valley/approvals/ takes writes only from a principal holding the approve grant (also accepted as request), and " + who + " holds none"
		r.expect("a create by "+who, who, ref, zeroID, first, says)
		r.expect("a replacement by "+who, who, ref, first, second, says)
		r.expect("a delete by "+who, who, ref, first, zeroID, says)
	}
	r.expect("an untagged key", "", ref, zeroID, first, "a key that names no principal holds no grant")
	r.p.push = pushPolicy{Writers: []string{"alice"}, Grants: map[string]refGrant{"all": {Refs: []string{"refs/*"}, Writers: []string{"alice"}}}}
	r.expect("a declared writer opened every ref", "alice", ref, zeroID, first, "holds none")
	r.p.push = pushPolicy{Refs: []string{approval.RefPrefix + "*"}, Writers: []string{"approver"}}
	r.expect("a protected approval", "operator", ref, zeroID, first, "a protected ref of project")
	r.expect("a protected approval by its writer", "approver", ref, zeroID, first, "")
	r.p.push = pushPolicy{}

	for _, bad := range []string{
		approval.RefPrefix + "main/" + changeID,
		approval.RefPrefix + "main/c-" + changeID[:31],
		approval.RefPrefix + "main/c-" + strings.ToUpper(changeID),
		approval.RefPrefix + "main/c-" + changeID + "/x",
		approval.RefPrefix + "c-" + changeID,
		approval.RefPrefix + "release/c-" + changeID,
		approval.RefPrefix + "main",
	} {
		r.expect("the malformed "+bad, "operator", bad, zeroID, first, "an approval's ref is refs/the-valley/approvals/main/c-<change ID>")
		r.expect("a delete of the malformed "+bad, "operator", bad, first, zeroID, "an approval's ref is")
	}
	r.p.symrefs = map[string]bool{ref: true}
	r.expect("a symbolic ref", "operator", ref, zeroID, first, "symbolic ref")
	r.p.symrefs = nil
	r.expect("a lookalike", "operator", "refs/"+ref, zeroID, first, "named refs/ followed by a full ref name")

	signedAs := func(text []byte, name string) string {
		n, err := note.Sign(text, name, approvalKey)
		if err != nil {
			t.Fatal(err)
		}
		return string(n)
	}
	text, err := approvalStatement(changeID, "main", "operator", 1).Encode()
	if err != nil {
		t.Fatal(err)
	}
	twice, err := note.AddSignature([]byte(approvalNote(t, changeID, "main", "operator", 1)), "approver", approvalKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ what, object, says string }{
		{"a tree", r.git("", "mktree"), "it points at a tree, where an approval holds a blob"},
		{"a commit", r.source("one"), "it points at a commit, where an approval holds a blob"},
		{"an object the push did not bring", strings.Repeat("1", 40), "it names an object the push did not bring"},
		{"an oversized blob", r.blob(strings.Repeat("x", approval.MaxNote+1)), "it is a blob of 2049 bytes, more than the 2048 an approval's may be"},
		{"text", r.blob("approved\n"), "it is not an approval note: this is not a note"},
		{"another kind of statement", r.blob(signedAs([]byte("the-valley/attestation/v1\n"), "operator")), "it is not an approval note: the signed text is not an approval"},
		{"a note signed twice", r.blob(string(twice)), "an approval note carries one signature, and this carries 2"},
		{"a signature under another name", r.blob(signedAs(text, "approver")), "signed with a key of approver, and names operator as its approver"},
		{"another change's approval", r.blob(approvalNote(t, strings.Repeat("k", 32), "main", "operator", 1)), "it approves the change " + strings.Repeat("k", 32) + ", and its ref names " + changeID},
		{"an approval for another target", r.blob(approvalNote(t, changeID, "release", "operator", 1)), "it approves landing on release, and its ref names main"},
	} {
		r.expect(c.what, "operator", ref, zeroID, c.object, c.says)
		r.expect(c.what+" as a replacement", "operator", ref, first, c.object, c.says)
	}
}

// An approval is published beside the change's integration request in one
// atomic push, each ref leased: a create against no ref, a replacement or a
// delete against the value read. The push goes through the real hook, run
// by receive-pack. A stale lease on either ref, or an approval the hook
// refuses, leaves both refs as they were.
func TestAnApprovalIsPublishedWithItsRequestAtomicallyAndLeased(t *testing.T) {
	hook, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	origin, client := filepath.Join(dir, "origin.git"), filepath.Join(dir, "client")
	gitIn(t, dir, "", "init", "--quiet", "--bare", origin)
	if err := os.Symlink(hook, filepath.Join(origin, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	policyFile, grantsFile := filepath.Join(dir, "policy.json"), filepath.Join(dir, "grants")
	if err := os.WriteFile(policyFile, []byte(`{"refs": [], "writers": [], "grants": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grantsFile, []byte("approve operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "", "init", "--quiet", client)
	commit := func(subject string) string {
		tree := gitIn(t, client, "", "mktree")
		return gitIn(t, client, "", "-c", "user.name=t", "-c", "user.email=t@localhost", "commit-tree", tree, "-m", subject)
	}
	blob := func(content string) string { return gitIn(t, client, content, "hash-object", "-w", "--stdin") }
	push := func(args ...string) (bool, string) {
		cmd := exec.Command("git", append([]string{"push", "--atomic", "--porcelain", origin}, args...)...)
		cmd.Dir = client
		cmd.Env = append(os.Environ(), "VALLEYHOOK_TEST_HOOK="+strings.Join([]string{
			"--project", "project", "--policy", policyFile, "--grants", grantsFile, "--principal", "operator",
		}, "\n"))
		out, err := cmd.CombinedOutput()
		return err == nil, string(out)
	}
	at := func(ref string) string {
		cmd := exec.Command("git", "for-each-ref", "--format=%(objectname)", ref)
		cmd.Dir = origin
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}

	req := requestPrefix + "main/" + change.RequestName(changeID)
	ref := approval.Ref("main", changeID)
	h1, h2 := commit("one"), commit("two")
	a1 := blob(approvalNote(t, changeID, "main", "operator", 1))
	a2 := blob(approvalNote(t, changeID, "main", "operator", 2))
	elsewhere := blob(approvalNote(t, strings.Repeat("k", 32), "main", "operator", 2))

	if ok, out := push("--force-with-lease="+req+":", "--force-with-lease="+ref+":", h1+":"+req, a1+":"+ref); !ok {
		t.Fatalf("filing the request and its approval: %s", out)
	}
	if at(req) != h1 || at(ref) != a1 {
		t.Fatalf("after filing: the request is at %q and the approval at %q", at(req), at(ref))
	}

	for _, c := range []struct {
		what string
		args []string
	}{
		{"an approval leased against no ref", []string{"--force-with-lease=" + req + ":" + h1, "--force-with-lease=" + ref + ":", h2 + ":" + req, a2 + ":" + ref}},
		{"an approval leased against another value", []string{"--force-with-lease=" + req + ":" + h1, "--force-with-lease=" + ref + ":" + a2, h2 + ":" + req, a2 + ":" + ref}},
		{"a request leased against another value", []string{"--force-with-lease=" + req + ":" + h2, "--force-with-lease=" + ref + ":" + a1, h2 + ":" + req, a2 + ":" + ref}},
		{"a delete leased against another value", []string{"--force-with-lease=" + req + ":" + h1, "--force-with-lease=" + ref + ":" + a2, ":" + req, ":" + ref}},
		{"an approval the hook refuses", []string{"--force-with-lease=" + req + ":" + h1, "--force-with-lease=" + ref + ":" + a1, h2 + ":" + req, elsewhere + ":" + ref}},
	} {
		if ok, out := push(c.args...); ok {
			t.Errorf("%s was pushed: %s", c.what, out)
		}
		if at(req) != h1 || at(ref) != a1 {
			t.Errorf("after %s: the request is at %q and the approval at %q", c.what, at(req), at(ref))
		}
	}

	if ok, out := push("--force-with-lease="+req+":"+h1, "--force-with-lease="+ref+":"+a1, h2+":"+req, a2+":"+ref); !ok {
		t.Fatalf("replacing the request and its approval: %s", out)
	}
	if at(req) != h2 || at(ref) != a2 {
		t.Fatalf("after replacing: the request is at %q and the approval at %q", at(req), at(ref))
	}
	if ok, out := push("--force-with-lease="+req+":"+h2, "--force-with-lease="+ref+":"+a2, ":"+req, ":"+ref); !ok {
		t.Fatalf("withdrawing the request and its approval: %s", out)
	}
	if at(req) != "" || at(ref) != "" {
		t.Fatalf("after withdrawing: the request is at %q and the approval at %q", at(req), at(ref))
	}
}
