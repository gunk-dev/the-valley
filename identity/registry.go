package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The registry as the schema exports it (schema/identity.cue). The derived
// fields the schema uses to hold the floor — genesisGovernance,
// externalGovernance — are checked by `cue vet` and read by nobody here.
type registry struct {
	Boundaries map[string]boundary  `json:"boundaries"`
	Principals map[string]principal `json:"principals"`
	Genesis    string               `json:"genesis"`
}

type boundary struct {
	Kind string `json:"kind"`
}

type principal struct {
	Kind    string           `json:"kind"`
	Keys    []key            `json:"keys"`
	Grants  map[string]grant `json:"grants"`
	Expires string           `json:"expires"`
}

type key struct {
	Class  string `json:"class"`
	Bound  string `json:"bound"`
	Public string `json:"public"`
	Signs  string `json:"signs"`
}

type grant struct {
	Boundary string `json:"boundary"`
}

const (
	// boundaryGitPush is the boundary kind the authorized_keys artifact is
	// rendered from: sshd on a host serving the instance's repositories.
	boundaryGitPush = "git-push"

	// boundaryRegistry is the boundary kind that admits a change to the
	// registry document itself. A render leaving nobody there is refused.
	boundaryRegistry = "registry"

	// boundaryRequest is the boundary kind the grants artifact is rendered
	// from: the pre-receive hook's check of writes to the integration
	// request namespace (valleyhook/).
	boundaryRequest = "request"
)

// readRegistry takes the registry out of a repository's tip, vets it, and
// exports it.
//
// The tip is taken apart with ls-tree and show rather than checked out, the
// same way the integrator reads the floor: a worktree would write into a
// repository this program has no business writing, and reading is the whole
// of what it needs. cue is shelled out to for the same reason the rest of
// the machinery shells out to it — one pinned tool, one behaviour.
//
// Nothing is written outside the scratch directory, so every failure here
// leaves the artifacts as they were.
func readRegistry(repo, ref, dir, schema string) (registry, string, error) {
	var r registry

	commit, err := git(repo, "rev-parse", ref)
	if err != nil {
		return r, "", fmt.Errorf("%s has no %s: %w", repo, ref, err)
	}

	tree := ref + ":" + dir
	listing, err := git(repo, "ls-tree", "--name-only", tree)
	if err != nil {
		return r, "", fmt.Errorf("no registry: %s has no %s/ at %s: %w", repo, dir, ref, err)
	}

	scratch, err := os.MkdirTemp("", "valley-identity")
	if err != nil {
		return r, "", err
	}
	defer os.RemoveAll(scratch)

	// The registry is a CUE package: the directory is the enumeration, and
	// the documents directly in it are what unify.
	var files []string
	for _, name := range strings.Split(strings.TrimSpace(listing), "\n") {
		if !strings.HasSuffix(name, ".cue") {
			continue
		}
		body, err := git(repo, "show", tree+"/"+name)
		if err != nil {
			return r, "", fmt.Errorf("reading %s/%s from %s at %s: %w", dir, name, repo, ref, err)
		}
		path := filepath.Join(scratch, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return r, "", err
		}
		files = append(files, path)
	}
	if len(files) == 0 {
		return r, "", fmt.Errorf("no registry: %s holds no *.cue in %s/ at %s", repo, dir, ref)
	}

	if out, err := cue(append([]string{"vet", "-c", schema}, files...)); err != nil {
		return r, "", fmt.Errorf("the registry at %s does not satisfy %s:\n%s", commit, schema, out)
	}
	out, err := cue(append([]string{"export", schema}, files...))
	if err != nil {
		return r, "", fmt.Errorf("exporting the registry at %s:\n%s", commit, out)
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return r, "", fmt.Errorf("the exported registry is not the shape this program reads: %w", err)
	}
	return r, commit, nil
}

// git runs git over the instance repository with replacement refs off. A
// replacement ref makes one object stand in for another wherever git looks
// it up, so a refs/replace/<blob> pushed to the instance repository could
// swap in a registry that authorizes any key it liked without moving the
// ref this program reads. The repository is read as it is.
func git(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-replace-objects", "-C", repo}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", commandError(err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// gitEnvironment is the environment git runs in: the caller's, less every
// GIT_ variable, and with git's own configuration fixed the way sigverify
// fixes it. A GIT_DIR, GIT_OBJECT_DIRECTORY or GIT_CONFIG_PARAMETERS in the
// caller's environment could point git at other objects or change how it
// reads them, and system and user configuration are not read. What is not
// git's own is inherited.
func gitEnvironment(environ []string) []string {
	var env []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}

// cue returns combined output, because its diagnosis is the error message
// worth passing on.
func cue(args []string) (string, error) {
	out, err := exec.Command("cue", args...).CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// commandError carries what a failing command said, which is otherwise
// dropped by exec's "exit status 1".
func commandError(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}
