package main

// Every ref the integrator reads by name is read by that exact name. git
// rev-parse, and every git command given a ref name, goes on to try other
// names for a ref the repository lacks: refs/tags/<name>, then
// refs/heads/<name>. A pusher can make such a branch or tag. Each test
// here leaves a ref missing, makes the ref git would take for it, and
// shows the integrator treats the ref as missing.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/change"
	"the-valley/composed"
)

// A request named for a change with no record is refused, whatever a
// branch named like its record says.
func TestAMissingRecordIsNoRecord(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	head := s.commit("head", base)
	ref := change.Ref("main", readyID)
	s.recordText("refs/heads/"+ref, recordJSON(t, readyID, change.Ready, head), head)

	in := &integrator{repo: s.repo}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	r := request{ref: requestPrefix + "/main/" + change.RequestName(readyID), change: change.RequestName(readyID), target: "refs/heads/main", head: head}
	outcome, why, err := in.recordRefusal(r, idx, base)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != refusedNotReady || !strings.Contains(why, ref+" holds no record of it") {
		t.Fatalf("with refs/heads/%s ready and %s missing, the request was refused %q (%s)", ref, ref, outcome, why)
	}
}

// A request whose target is missing is not judged, and a withdrawn change
// does not void it, whatever a branch named like the target holds.
func TestAMissingTargetIsNoTarget(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	head := s.commit("head", base)
	s.git("update-ref", "refs/heads/refs/heads/gone", base)
	data, err := change.Encode(&change.Record{
		Schema: change.Schema, ChangeID: draftID, Target: "gone", Owner: "owner", State: change.Withdrawn, Seq: 2, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: "a version", Branch: "topic", Uploader: "owner"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.recordText(change.Ref("gone", draftID), string(data), head)
	name := change.RequestName(draftID)
	ref := requestPrefix + "/gone/" + name
	s.git("update-ref", ref, head)

	in := &integrator{repo: s.repo, out: devNull(t)}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	r := request{ref: ref, change: name, target: "refs/heads/gone", head: head}
	if kept := in.voidWithdrawn([]request{r}, idx); len(kept) != 1 {
		t.Errorf("with refs/heads/gone missing, the withdrawn change voided its request")
	}
	if _, there := gitTry(s.repo, "rev-parse", "--verify", "--quiet", ref); !there {
		t.Errorf("with refs/heads/gone missing, the request was deleted")
	}
	if err := in.handle(r, idx); err == nil || err.Error() != "no target refs/heads/gone to integrate onto" {
		t.Errorf("with refs/heads/gone missing, the request was judged: %v", err)
	}
}

// A request with no memo has none, whatever a tag named like its memo
// holds. A tag may hold a blob, as a memo does; a branch may not.
func TestAMissingMemoIsNoMemo(t *testing.T) {
	s := newScratchRepo(t)
	r := request{ref: requestPrefix + "/main/topic", change: "topic", target: "refs/heads/main", head: s.git("rev-parse", "HEAD")}
	in := &integrator{repo: s.repo}
	planted := s.input("planted land\n", "hash-object", "-w", "--stdin")
	s.git("update-ref", "refs/tags/"+in.memoRef(r), planted)
	if memo, err := in.readMemo(r); err != nil || memo != "" {
		t.Fatalf("with %s missing, the memo read %q (%v)", in.memoRef(r), memo, err)
	}
	if err := in.writeMemo(r, "written"); err != nil {
		t.Fatal(err)
	}
	if memo, err := in.readMemo(r); err != nil || memo != "written" {
		t.Fatalf("the memo written reads %q (%v)", memo, err)
	}
}

// The floor is read at its ref by that exact name. An instance repository
// with no refs/heads/main has no floor, whatever a branch named
// refs/heads/refs/heads/main carries.
func TestAMissingFloorRefIsNoFloor(t *testing.T) {
	s := newScratchRepo(t)
	dir := filepath.Join(s.repo, "policy", "instance")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "floor.cue"), []byte("package verification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.git("add", "policy")
	s.git("commit", "--quiet", "-m", "the floor")
	floor := composed.Floor{Repo: s.repo, Ref: "refs/heads/main", Dir: "policy/instance"}
	read, done, err := floor.Open(context.Background(), "valley-integrator-floor")
	done()
	if err != nil || read == "" {
		t.Fatalf("the floor at main did not read: %v", err)
	}

	s.git("update-ref", "refs/heads/refs/heads/main", "refs/heads/main")
	s.git("update-ref", "-d", "refs/heads/main")
	_, done, err = floor.Open(context.Background(), "valley-integrator-floor")
	done()
	if err == nil || !strings.Contains(err.Error(), "has no refs/heads/main: "+composed.Floorless) {
		t.Fatalf("with refs/heads/main missing, the floor read returned %v", err)
	}
}

// The signers publication is written when there is none yet, whatever a
// branch named refs/heads/refs/the-valley/signers holds. Read through
// that branch, the publication's compare-and-swap would expect a ref that
// is not there, and no publication could ever be written.
func TestAMissingPublicationIsWritten(t *testing.T) {
	s := newScratchRepo(t)
	s.git("update-ref", "refs/heads/"+signersRef, s.git("rev-parse", "HEAD"))
	known := filepath.Join(t.TempDir(), "known-signers")
	if err := os.WriteFile(known, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devNull(t), devNull(t)
	err := publishSigners([]string{"--instance-repo", s.repo, "--known-signers", known, "--name", "valley-host/attestations"})
	os.Stdout, os.Stderr = stdout, stderr
	if err != nil {
		t.Fatalf("with %s missing, the publication failed: %v", signersRef, err)
	}
	if _, found, err := resolveRef(s.repo, signersRef); err != nil || !found {
		t.Fatalf("no publication at %s (%v)", signersRef, err)
	}
}

// Evidence is read through the objects its refs held when they were
// listed. A ref deleted after the listing is read as it was listed, never
// through a branch named like it.
func TestEvidenceDeletedAfterListingIsReadAsListed(t *testing.T) {
	s := newScratchRepo(t)
	head := s.git("rev-parse", "HEAD")
	subject := strings.Repeat("ab", 32)
	ref := attestationPrefix + "/" + subject + "/" + strings.Repeat("cd", 32)
	noteTree := func(note string) string {
		blob := s.input(note, "hash-object", "-w", "--stdin")
		dir := s.input("100644 blob "+blob+"\t"+statementNote+"\n", "mktree")
		return s.input("040000 tree "+dir+"\ttree-ok\n", "mktree")
	}
	listed := "predicate.check.name tree-ok\npredicate.result pass\n\nthe listed signature\n"
	planted := "predicate.check.name tree-ok\npredicate.result pass\n\nthe planted signature\n"
	s.git("update-ref", ref, noteTree(listed))
	plantedCommit := s.git("commit-tree", noteTree(planted), "-m", "planted")

	// attest is not under test: every note reads as signed by nobody the
	// verifier holds, and collectEvidence returns it as read.
	attest := filepath.Join(t.TempDir(), "attest")
	if err := os.WriteFile(attest, []byte("#!/bin/sh\necho 'attest: "+unknownSigner+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := &integrator{repo: s.repo, attest: attest, evidenceListed: func() {
		s.git("update-ref", "-d", ref)
		s.git("update-ref", "refs/heads/"+ref, plantedCommit)
	}}
	_, notes, err := in.collectEvidence(&worktree{repo: s.repo, dir: s.repo}, head, subject)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(notes["tree-ok"]); got != listed {
		t.Fatalf("with %s deleted after it was listed, and refs/heads/%s planted, the note read is %q", ref, ref, got)
	}
}

// A request that is gone when the integrator consumes it did not move,
// whatever a branch named like it holds, and the landing says it was not
// consumed.
func TestAGoneRequestDidNotMove(t *testing.T) {
	s := newScratchRepo(t)
	head := s.git("rev-parse", "HEAD")
	other := s.commit("other", head)
	ref := requestPrefix + "/main/topic"
	s.git("update-ref", "refs/heads/"+ref, other)

	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	in := &integrator{repo: s.repo, out: out}
	why := in.consume(request{ref: ref, change: "topic", target: "refs/heads/main", head: head})
	said, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(why, "the request ref "+ref+" was not consumed") || strings.Contains(string(said), "moved") {
		t.Fatalf("with %s gone and refs/heads/%s planted, consuming it said %q and returned %q", ref, ref, said, why)
	}
}
