package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"the-valley/approval"
	"the-valley/composed"
)

// snapshot is one commit of another repository: the instance repository
// a controller reads its floor and the instance identity from. A ref is
// resolved to it once, by the ref's exact name, and everything read
// afterwards is read at that commit, so a ref that moves meanwhile mixes
// no two versions into one reading.
//
// Every read is git plumbing over refs and objects: show-ref, rev-parse,
// cat-file and rev-list, and composed's ls-tree and cat-file for the
// floor. Each runs with optional locks off and with lazy fetching off, so
// in a partial clone an object the repository does not hold fails the
// read rather than being fetched into it. A snapshot creates no lock
// file, worktree, index or object there, and a repository the controller
// may only read can be read.
type snapshot struct {
	repo   string // the repository, as given
	ref    string // the full ref name resolved
	commit string // the commit it held
}

// takeSnapshot resolves ref, a full ref name, in repo to the commit it
// holds. A repository with no such ref, or one whose ref holds something
// other than a commit, is refused.
func takeSnapshot(repo, ref string) (snapshot, error) {
	if !strings.HasPrefix(ref, "refs/") {
		return snapshot{}, fmt.Errorf("%s is not a full ref name", ref)
	}
	// show-ref --exists exits 2 for a ref that is missing, and with
	// another status for a failure to read.
	_, err := readGit(repo, "show-ref", "--exists", "--end-of-options", ref)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 {
		return snapshot{}, fmt.Errorf("%s has no %s", repo, ref)
	} else if err != nil {
		return snapshot{}, err
	}
	oid, err := readGit(repo, "show-ref", "--verify", "--hash", "--end-of-options", ref)
	if err != nil {
		return snapshot{}, err
	}
	commit, err := readGit(repo, "rev-parse", "--verify", "--end-of-options", oid+"^{commit}")
	if err != nil {
		return snapshot{}, fmt.Errorf("%s in %s is not a commit: %w", ref, repo, err)
	}
	return snapshot{repo: repo, ref: ref, commit: commit}, nil
}

// read is the blob at path in the snapshot's commit.
func (s snapshot) read(path string) ([]byte, error) {
	kind, err := readGit(s.repo, "cat-file", "-t", s.commit+":"+path)
	if err != nil {
		return nil, fmt.Errorf("%s has no %s at %s: %w", s.repo, path, s.commit, err)
	}
	if kind != "blob" {
		return nil, fmt.Errorf("%s at %s in %s is a %s, not a file", path, s.commit, s.repo, kind)
	}
	out, err := gitIn(s.repo, readOnly, "--no-optional-locks", "cat-file", "blob", s.commit+":"+path)
	return []byte(out), err
}

// floor is the floor in dir as the snapshot's commit holds it. A Floor
// whose Ref is an object ID is read at that commit (composed.Floor), so
// the floor composed is the snapshot's whatever the ref holds by then.
func (s snapshot) floor(dir string) composed.Floor {
	return composed.Floor{Repo: s.repo, Ref: s.commit, Dir: dir, NoLazyFetch: true}
}

// instanceID is the instance identity (approval.InstanceID) of the
// snapshot's commit: its root commits and the repository's object format.
// A shallow repository is refused, since the roots it holds need not be
// its history's, and so is one missing a commit of that history, which
// rev-list cannot walk.
func (s snapshot) instanceID() (string, error) {
	shallow, err := readGit(s.repo, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return "", err
	}
	if shallow != "false" {
		return "", fmt.Errorf("the instance repository %s is shallow, so the root commits of its %s are not known", s.repo, s.ref)
	}
	format, err := readGit(s.repo, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	roots, err := readGit(s.repo, "rev-list", "--max-parents=0", "--end-of-options", s.commit)
	if err != nil {
		return "", fmt.Errorf("the history of %s in the instance repository does not read: %w", s.ref, err)
	}
	id, err := approval.InstanceID(format, strings.Fields(roots))
	if err != nil {
		return "", fmt.Errorf("the instance identity: %w", err)
	}
	return id, nil
}

// readOnly is the environment a snapshot's git runs in: lazy fetching
// off, so a partial clone's missing object fails the read instead of
// being fetched into the repository.
var readOnly = []string{"GIT_NO_LAZY_FETCH=1"}

// readGit runs a git command that only reads, and returns its output
// trimmed. Optional locks are off, so no command takes the lock some take
// to refresh an index, and lazy fetching is off.
func readGit(repo string, args ...string) (string, error) {
	out, err := gitIn(repo, readOnly, append([]string{"--no-optional-locks"}, args...)...)
	return strings.TrimSpace(out), err
}
