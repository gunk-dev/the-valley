// Package composed reads the verification policy as cue composes it: the
// floor, the project-type template the policy names, and the project's own
// layer at the target tip, under the packaged schema
// (schema/verification.cue). It does not compose anything itself. cue
// does, so no second copy of the composition can disagree with the one the
// deriver runs.
//
// The floor is read from the instance repository's tip, or from a
// materialized directory for a host that serves no instance repository.
// The project layer is read from the target tip. A layer that is not there
// defaults in the safe direction of its job: an absent project layer
// composes the floor alone, and an absent floor is refused.
package composed

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Floor is where the group's floor comes from: a repository's tip, read
// with ls-tree and show and never written, or else a directory on disk.
type Floor struct {
	Repo string // the repository carrying the floor; "" when Path is set
	Ref  string // the ref whose tip is authoritative
	Dir  string // the floor's directory in that tip's tree
	Path string // a materialized layer, when no repository carries one
}

// Floorless ends every refusal to find a floor. The floor is the minimum,
// and a missing minimum reads equally as a reader pointed at the wrong
// directory, a repository nobody seeded, or a floor somebody deleted. What
// is refused is the absent document; a floor that states it requires
// nothing composes like any other.
const Floorless = "a valley without a written floor is not a valley whose changes land"

// Open resolves the floor to a directory of *.cue on disk, and returns
// what removes it. A floor read from a repository is copied into a
// scratch directory whose name starts with prefix.
func (f Floor) Open(prefix string) (string, func(), error) {
	noop := func() {}
	if f.Repo == "" {
		// The materialized layer is held to the same minimum as the one a
		// repository carries: a directory with no documents in it is a floor
		// nobody wrote.
		if docs, err := filepath.Glob(filepath.Join(f.Path, "*.cue")); err != nil || len(docs) == 0 {
			return "", noop, fmt.Errorf("no instance policy layer: %s holds no *.cue: %s", f.Path, Floorless)
		}
		return f.Path, noop, nil
	}
	dir, found, done, err := readLayer(f.Repo, f.Ref, f.Dir, prefix)
	switch {
	case err != nil:
		return "", noop, err
	case found == absent:
		return "", noop, fmt.Errorf("no instance policy layer: %s has no %s/ at %s: %s", f.Repo, f.Dir, f.Ref, Floorless)
	case found == empty:
		return "", noop, fmt.Errorf("no instance policy layer: %s holds no *.cue in %s/ at %s: %s", f.Repo, f.Dir, f.Ref, Floorless)
	}
	return dir, done, nil
}

// Project resolves the project's own layer at a tree-ish of a repository
// to a directory of *.cue on disk, and returns what removes it, and a note
// when the layer is absent.
//
// A target that carries no layer of its own is composed under the floor
// alone. The project layer only ever adds, and the identity of "adds" is
// "adds nothing", so that is what absence means. Absence is answered with
// a document that adds nothing rather than with an argument left off,
// because cue is handed a directory of *.cue and has no spelling for "no
// layer".
func Project(repo, treeish, dir, prefix string) (string, string, func(), error) {
	path, found, done, err := readLayer(repo, treeish, dir, prefix)
	if err != nil {
		return "", "", func() {}, err
	}
	if found == present {
		return path, "", done, nil
	}
	done()
	path, done, err = AddsNothing(prefix)
	return path, fmt.Sprintf("no project layer at %s; the floor alone", dir), done, err
}

// AddsNothing writes a project layer that adds nothing into a scratch
// directory: a package clause and nothing else, so `project` stays what
// the schema leaves it.
func AddsNothing(prefix string) (string, func(), error) {
	dir, err := scratch(prefix)
	if err != nil {
		return "", func() {}, err
	}
	done := func() { _ = os.RemoveAll(dir) }
	if err := os.WriteFile(filepath.Join(dir, "adds-nothing.cue"), []byte("package verification\n"), 0o644); err != nil {
		done()
		return "", func() {}, err
	}
	return dir, done, nil
}

// What readLayer found at a directory of a tree.
const (
	present = iota // the directory, with *.cue in it
	absent         // no such directory
	empty          // the directory, with no *.cue in it
)

// readLayer copies the *.cue documents directly in one directory of a
// tree-ish out of a repository, into a scratch directory. The deriver and
// cue both read a layer as those documents, so they are what is taken.
// The scratch directory is removed unless the layer is present.
func readLayer(repo, treeish, dir, prefix string) (string, int, func(), error) {
	noop := func() {}
	tree := treeish + ":" + dir
	listing, err := git(repo, "ls-tree", "--name-only", "-z", tree)
	if err != nil {
		return "", absent, noop, nil
	}
	out, err := scratch(prefix)
	if err != nil {
		return "", absent, noop, err
	}
	done := func() { _ = os.RemoveAll(out) }
	found := 0
	for _, name := range strings.Split(listing, "\x00") {
		if !strings.HasSuffix(name, ".cue") {
			continue
		}
		body, err := git(repo, "cat-file", "blob", tree+"/"+name)
		if err != nil {
			done()
			return "", absent, noop, fmt.Errorf("reading %s/%s from %s at %s: %w", dir, name, repo, treeish, err)
		}
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o644); err != nil {
			done()
			return "", absent, noop, err
		}
		found++
	}
	if found == 0 {
		done()
		return "", empty, noop, nil
	}
	return out, present, done, nil
}

// Check is one check as the composed policy defines it: how it is run, and
// how long an observation of it stays good.
type Check struct {
	Runner    string `json:"runner"`
	Attribute string `json:"attribute"`
	Command   string `json:"command"`
	Validity  string `json:"validity"`
}

// Policy is the composed policy, as far as its readers read it.
type Policy struct {
	Checks map[string]Check

	// MaxAutomaticVersions caps how many versions the restacker adds to
	// one change over the change's life (restack.maxAutomaticVersions).
	MaxAutomaticVersions int

	// Digest is the SHA-256 of the whole export, so everything the policy
	// composes is in it: every check, every class's paths, requirements and
	// carry mode, and the restacker's cap.
	Digest string
}

// Compose runs `cue export` of the composed policy over the schema, the
// floor's documents and the project layer's documents, and reads it. The
// template is the one the policy's `type` names, which the schema selects
// as it composes.
func Compose(cue, schema, instance, project string) (*Policy, error) {
	args := []string{"export", "--out", "json", "-e", "policy", schema}
	for _, dir := range []string{instance, project} {
		docs, err := filepath.Glob(filepath.Join(dir, "*.cue"))
		if err != nil || len(docs) == 0 {
			return nil, fmt.Errorf("no policy layer at %s", dir)
		}
		args = append(args, docs...)
	}
	out, err := exec.Command(cue, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("composing the policy: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	var read struct {
		Checks  map[string]Check `json:"checks"`
		Restack struct {
			MaxAutomaticVersions int `json:"maxAutomaticVersions"`
		} `json:"restack"`
	}
	if err := json.Unmarshal(out, &read); err != nil {
		return nil, fmt.Errorf("reading the composed policy: %w", err)
	}
	sum := sha256.Sum256(out)
	return &Policy{
		Checks:               read.Checks,
		MaxAutomaticVersions: read.Restack.MaxAutomaticVersions,
		Digest:               hex.EncodeToString(sum[:]),
	}, nil
}

func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// scratch makes one throwaway directory under whatever TMPDIR names. The
// error names the operation, the path, and where the path came from.
func scratch(prefix string) (string, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", fmt.Errorf("creating a scratch directory under %s, which TMPDIR names: %w", os.TempDir(), err)
	}
	return dir, nil
}
