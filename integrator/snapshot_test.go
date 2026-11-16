package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/approval"
)

// floorCommit is a commit whose tree holds one floor document,
// policy/instance/floor.cue, saying text, on parents.
func (s *scratchRepo) floorCommit(text string, parents ...string) string {
	s.t.Helper()
	blob := s.input(text, "hash-object", "-w", "--stdin")
	instance := s.input("100644 blob "+blob+"\tfloor.cue\n", "mktree")
	policy := s.input("040000 tree "+instance+"\tinstance\n", "mktree")
	tree := s.input("040000 tree "+policy+"\tpolicy\n", "mktree")
	args := []string{"commit-tree", tree, "-m", "a floor"}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return s.git(args...)
}

// repoFiles is every path under dir with its mode, modification time,
// size and, for a file, the SHA-256 of its content, which any write under
// dir would change.
func repoFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = fmt.Sprint(info.Mode(), info.ModTime(), info.Size())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] += fmt.Sprintf(" %x", sha256.Sum256(content))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// repoFilesUnchanged fails the test when dir's files are not before.
func repoFilesUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := repoFiles(t, dir)
	for path, was := range before {
		if after[path] != was {
			t.Errorf("%s changed: %q, then %q", path, was, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s was written", path)
		}
	}
}

// readOnlyTree takes write away from every file and directory under dir,
// and gives it back when the test ends.
func readOnlyTree(t *testing.T, dir string) {
	t.Helper()
	modes := map[string]fs.FileMode{}
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		modes[path] = info.Mode().Perm()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for path, mode := range modes {
		if err := os.Chmod(path, mode&^0o222); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for path, mode := range modes {
			os.Chmod(path, mode)
		}
	})
}

// A snapshot is the commit its ref held when it was taken. The floor, a
// document and the instance identity are read at that commit after the
// ref has moved to a commit holding another floor and another root. The
// reads write nothing: they succeed in a repository nobody may write,
// and leave every file in it as it was.
func TestASnapshotReadsTheCommitItResolved(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "refs/heads/main")
	first := s.floorCommit("package verification\n// first\n", base)
	s.git("update-ref", "refs/heads/main", first)
	snap, err := takeSnapshot(s.repo, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if snap.commit != first {
		t.Fatalf("the snapshot is of %s, not main's %s", snap.commit, first)
	}

	other := s.commit("an unrelated root")
	moved := s.floorCommit("package verification\n// moved\n", first, other)
	s.git("update-ref", "refs/heads/main", moved)

	// Nobody may write the repository from here on, the test included.
	if os.Geteuid() == 0 {
		t.Log("running as root, which writes whatever the modes say; the listing alone shows no write")
	}
	readOnlyTree(t, s.repo)
	before := repoFiles(t, s.repo)

	if got, err := snap.read("policy/instance/floor.cue"); err != nil || string(got) != "package verification\n// first\n" {
		t.Errorf("the snapshot's floor document reads %q, %v", got, err)
	}
	dir, done, err := snap.floor("policy/instance").Open(t.Context(), "valley-snapshot-test")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "floor.cue")); err != nil || !strings.Contains(string(got), "first") {
		t.Errorf("the snapshot's floor opens as %q, %v", got, err)
	}
	done()
	want, err := approval.InstanceID("sha1", []string{base})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := snap.instanceID(); err != nil || got != want {
		t.Errorf("the snapshot's identity is %q, %v, not its commit's %q", got, err, want)
	}
	again, err := takeSnapshot(s.repo, "refs/heads/main")
	if err != nil || again.commit != moved {
		t.Errorf("a new snapshot is of %q, %v, not main's %s", again.commit, err, moved)
	}
	repoFilesUnchanged(t, s.repo, before)
}

// A snapshot is taken of a full ref name holding a commit. A short name, a
// ref the repository does not hold and a ref holding a tree are refused,
// and so is a path the commit does not hold as a file.
func TestASnapshotIsOfACommit(t *testing.T) {
	s := newScratchRepo(t)
	tree := s.git("rev-parse", "refs/heads/main^{tree}")
	s.git("update-ref", "refs/the-valley/signers", tree)
	for _, ref := range []string{"main", "refs/heads/absent", "refs/the-valley/signers"} {
		if snap, err := takeSnapshot(s.repo, ref); err == nil {
			t.Errorf("%s was taken as a snapshot of %s", ref, snap.commit)
		}
	}
	snap, err := takeSnapshot(s.repo, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	s.git("update-ref", "refs/heads/main", s.floorCommit("package verification\n"))
	if snap, err = takeSnapshot(s.repo, "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"absent.cue", "policy/instance", "policy"} {
		if got, err := snap.read(path); err == nil {
			t.Errorf("%s read as %q", path, got)
		}
	}
}

// In a partial clone, a snapshot reads only what the repository holds. A
// blob the clone left to its promisor remote fails the read, of the
// document and of the floor alike, and nothing is fetched into the
// repository: every file in it is as it was. The commit and its history
// are held, so the instance identity reads. A git read with lazy fetching
// on does fetch the blob, which shows the clone would have.
func TestASnapshotFetchesNothingFromAPromisorRemote(t *testing.T) {
	src := newScratchRepo(t)
	base := src.git("rev-parse", "refs/heads/main")
	src.git("update-ref", "refs/heads/main", src.floorCommit("package verification\n// promised\n", base))
	src.git("config", "uploadpack.allowFilter", "true")
	part := filepath.Join(t.TempDir(), "part.git")
	if _, err := git(t.TempDir(), "clone", "--quiet", "--bare", "--filter=blob:none", "file://"+src.repo, part); err != nil {
		t.Fatal(err)
	}
	if promisor, err := gitLine(part, "config", "remote.origin.promisor"); err != nil || promisor != "true" {
		t.Fatalf("the clone has no promisor remote (%q, %v), so this test proves nothing", promisor, err)
	}
	blob := src.git("rev-parse", "refs/heads/main:policy/instance/floor.cue")

	before := repoFiles(t, part)
	snap, err := takeSnapshot(part, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := snap.read("policy/instance/floor.cue"); err == nil {
		t.Errorf("a blob the clone does not hold read as %q", got)
	}
	if dir, done, err := snap.floor("policy/instance").Open(t.Context(), "valley-snapshot-test"); err == nil {
		done()
		t.Errorf("a floor the clone does not hold opened at %s", dir)
	}
	want, err := approval.InstanceID("sha1", []string{base})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := snap.instanceID(); err != nil || got != want {
		t.Errorf("the partial clone's identity is %q, %v, not %q", got, err, want)
	}
	repoFilesUnchanged(t, part, before)

	if _, err := git(part, "cat-file", "blob", blob); err != nil {
		t.Fatalf("git with lazy fetching on did not fetch the blob either, so this test proves nothing: %v", err)
	}
}
