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
