// Package composed reads the verification policy as cue composes it: the
// floor, the project-type template the policy names, and the project's own
// layer at the target tip, under the packaged schema
// (schema/verification.cue). It does not compose anything itself. cue
// does, so no second copy of the composition can disagree with the one the
// deriver runs.
//
// Compose reads the check definitions the integrator reads, the
// restacker's cap, whether an approval must be signed with a hardware key,
// and the policy's digest. Read reads the whole policy the deriver
// (../derive) derives required checks from: every class, the unclassified
// set, and whether each requirement is mandatory.
//
// The floor is read from the instance repository's tip, or from a
// materialized directory for a host that serves no instance repository.
// The project layer is read from the target tip. A layer that is not there
// defaults in the safe direction of its job: an absent project layer
// composes the floor alone, and an absent floor is refused.
//
// Every git and cue it runs runs under the caller's context, in a process
// group of its own. A context that is done kills the whole group, and the
// wait for the output is bounded, so a caller with a deadline is never
// held past it.
package composed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
func (f Floor) Open(ctx context.Context, prefix string) (string, func(), error) {
	noop := func() {}
	if f.Repo == "" {
		// The materialized layer is held to the same minimum as the one a
		// repository carries: a directory with no documents in it is a floor
		// nobody wrote.
		if _, err := layer(f.Path); err != nil {
			return "", noop, fmt.Errorf("no instance policy layer: %s holds no *.cue: %s", f.Path, Floorless)
		}
		return f.Path, noop, nil
	}
	dir, found, done, err := readLayer(ctx, f.Repo, f.Ref, f.Dir, prefix)
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
func Project(ctx context.Context, repo, treeish, dir, prefix string) (string, string, func(), error) {
	path, found, done, err := readLayer(ctx, repo, treeish, dir, prefix)
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
//
// The listing is NUL-separated, so every name is read as it is. A
// newline-separated listing quotes a name with non-ASCII characters in it,
// such as naïve.cue, and the quoted name does not end in .cue, so that
// document would be skipped. A materialized floor's glob finds it, and so
// does this.
func readLayer(ctx context.Context, repo, treeish, dir, prefix string) (string, int, func(), error) {
	noop := func() {}
	tree := treeish + ":" + dir
	listing, err := git(ctx, repo, "ls-tree", "--name-only", "-z", tree)
	if err != nil {
		if ctx.Err() != nil {
			return "", absent, noop, fmt.Errorf("reading %s from %s at %s: %w", dir, repo, treeish, ctx.Err())
		}
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
		body, err := git(ctx, repo, "show", tree+"/"+name)
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

// Check is one check as the composed policy defines it: how it is run, how
// long an observation of it stays good, and what running it costs.
type Check struct {
	Runner    string `json:"runner"`    // nix or command
	Attribute string `json:"attribute"` // the flake check attribute, for the nix runner
	Command   string `json:"command"`   // the command line, for the command runner
	Validity  string `json:"validity"`  // how long an observation stays good; "" for no window
	Cost      string `json:"cost"`      // quick or slow
}

// Policy is the composed policy, as far as its readers read it.
type Policy struct {
	Checks map[string]Check

	// MaxAutomaticVersions caps how many versions the restacker adds to
	// one change over the change's life (restack.maxAutomaticVersions).
	// It is kept as cue wrote it. The schema admits any whole number of at
	// least 1, and a Go int holds only some of them, so decoding into an
	// int would refuse policies the schema admits. The restacker converts
	// it where it counts.
	MaxAutomaticVersions json.Number

	// HardwareApproval says whether an approval of a change must be signed
	// with a key the identity registry enrolled as a hardware key
	// (approval.hardware). It selects which keys may sign an approval. It
	// does not decide whether a change needs one. A policy that leaves the
	// field out does not require a hardware key.
	HardwareApproval bool

	// Digest is the SHA-256 of the whole export, so everything the policy
	// composes is in it: every check, every class's paths, requirements and
	// carry mode, the restacker's cap, and the hardware requirement.
	Digest string
}

// Compose runs `cue export` of the composed policy over the schema, the
// floor's documents and the project layer's documents, and reads it. The
// template is the one the policy's `type` names, which the schema selects
// as it composes.
func Compose(ctx context.Context, cue, schema, instance, project string) (*Policy, error) {
	docs, err := documents(schema, instance, project)
	if err != nil {
		return nil, err
	}
	pol, _, err := compose(ctx, cue, docs)
	return pol, err
}

// compose exports `policy` over docs and reads the policy from it. It
// returns the export too, for a reader that reads more of it.
func compose(ctx context.Context, cue string, docs []string) (*Policy, []byte, error) {
	out, err := export(ctx, cue, docs, "policy")
	if err != nil {
		return nil, nil, fmt.Errorf("composing the policy: %w", err)
	}
	var read struct {
		Checks  map[string]Check `json:"checks"`
		Restack struct {
			MaxAutomaticVersions json.Number `json:"maxAutomaticVersions"`
		} `json:"restack"`
		Approval struct {
			Hardware bool `json:"hardware"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(out, &read); err != nil {
		return nil, nil, fmt.Errorf("reading the composed policy: %w", err)
	}
	sum := sha256.Sum256(out)
	return &Policy{
		Checks:               read.Checks,
		MaxAutomaticVersions: read.Restack.MaxAutomaticVersions,
		HardwareApproval:     read.Approval.Hardware,
		Digest:               hex.EncodeToString(sum[:]),
	}, out, nil
}

// documents is what a policy is composed from: the schema, then the
// floor's documents and the project layer's (layer).
func documents(schema, instance, project string) ([]string, error) {
	docs := []string{schema}
	for _, dir := range []string{instance, project} {
		found, err := layer(dir)
		if err != nil {
			return nil, err
		}
		docs = append(docs, found...)
	}
	return docs, nil
}

// layer is the documents of one policy layer: the entries directly in dir
// that bin/valley's `"$dir"/*.cue` names, in its order.
//
// dir is a name and never a pattern, as it is quoted there, so a directory
// named policy[1] is that directory and not policy1. Its entries are
// listed, and an entry is a document when its name ends in .cue and does
// not begin with a dot, because bash's glob leaves a dotfile out. They are
// sorted bytewise, as bash sorts them in the C locale bin/valley derives
// in. The first must be a regular file, as bin/valley's `[ -f ]` asks, or
// the layer holds no documents.
func layer(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("no policy layer at %s: %w", dir, err)
	}
	var docs []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".cue") && !strings.HasPrefix(name, ".") {
			docs = append(docs, filepath.Join(dir, name))
		}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no policy layer at %s", dir)
	}
	if info, err := os.Stat(docs[0]); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("no policy layer at %s: %s is not a file", dir, docs[0])
	}
	return docs, nil
}

// export runs `cue export` of one expression over docs and returns what
// cue printed. A failure returns what cue said, so the caller can say what
// it was doing.
//
// Its output and its diagnostics are read as one stream, and
// Policy.Digest is of those bytes. Reading them apart would change every
// digest with no change to any policy.
func export(ctx context.Context, cue string, docs []string, expr string) ([]byte, error) {
	args := append([]string{"export", "--out", "json", "-e", expr}, docs...)
	cmd := command(ctx, cue, args...)
	var combined bytes.Buffer
	cmd.Stdout, cmd.Stderr = &combined, &combined
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, strings.TrimSpace(combined.String()))
	}
	return combined.Bytes(), nil
}

// waitDelay is how long a wait for a program's output may go on after it
// has exited, or after a done context has killed it. Something it started
// that left its process group may still hold the output open.
const waitDelay = 5 * time.Second

// command is a program run under ctx in a process group of its own, which
// a done context kills whole, with the wait for its output bounded.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = waitDelay
	ownGroup(cmd)
	return cmd
}

func git(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := command(ctx, "git", args...)
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
