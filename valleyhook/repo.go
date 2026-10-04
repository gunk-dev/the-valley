package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// gitProgram is the git this program runs. The package sets it at build
// time to an absolute store path, so nothing in a caller's PATH decides
// which git reads the repository.
var gitProgram = "git"

// repository is what a decision needs to know about the repository a push
// is landing in.
type repository interface {
	// symbolic reports whether ref is a symbolic ref there now.
	symbolic(ref string) (bool, error)
	// notes returns every blob under the tree id names, keyed by path,
	// or an error saying why id is not such a tree.
	notes(id string) (map[string][]byte, error)
}

// The bounds on what an attestation ref may hold. A statement is a few
// hundred bytes and a ref holds one note per check, so these are far above
// anything attest writes, and they keep a hostile push from making the
// hook read without limit.
const (
	maxNotes    = 256
	maxNoteSize = 64 << 10
)

// gitRepository reads the repository the hook runs in. receive-pack runs
// the hook inside the repository, with the pushed objects in a quarantine
// directory its environment names, so the objects a push brings are
// readable here before any ref points at them.
type gitRepository struct{}

func (gitRepository) symbolic(ref string) (bool, error) {
	_, err := run(nil, "symbolic-ref", "--quiet", ref)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// Not a symbolic ref, or no ref at all.
		return false, nil
	default:
		return false, err
	}
}

func (gitRepository) notes(id string) (map[string][]byte, error) {
	kind, err := run(nil, "cat-file", "-t", id)
	if err != nil {
		return nil, err
	}
	if kind := strings.TrimSpace(string(kind)); kind != "tree" {
		return nil, fmt.Errorf("it points at a %s, and an attestation ref points at a tree of notes", kind)
	}
	listing, err := run(nil, "ls-tree", "-r", "-z", "--full-tree", id)
	if err != nil {
		return nil, err
	}
	entries := strings.Split(strings.TrimSuffix(string(listing), "\x00"), "\x00")
	if len(entries) == 1 && entries[0] == "" {
		return nil, errors.New("it points at an empty tree, and an attestation holds at least one note")
	}
	if len(entries) > maxNotes {
		return nil, fmt.Errorf("its tree holds %d entries, more than the %d an attestation may", len(entries), maxNotes)
	}
	var paths, oids []string
	for _, entry := range entries {
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[0] != "100644" || fields[1] != "blob" {
			return nil, fmt.Errorf("its tree holds %q, and an attestation holds only notes, as plain files", entry)
		}
		paths = append(paths, path)
		oids = append(oids, fields[2])
	}

	// Every size is checked before any note is read, and then one
	// cat-file serves them all.
	ids := []byte(strings.Join(oids, "\n") + "\n")
	sizes, err := run(ids, "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Fields(string(sizes)) {
		if size, err := strconv.Atoi(line); err != nil || size > maxNoteSize {
			return nil, fmt.Errorf("%s holds %s bytes, more than the %d a note may", paths[i], line, maxNoteSize)
		}
	}
	out, err := run(ids, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	found := map[string][]byte{}
	for i := range oids {
		header, rest, ok := bytes.Cut(out, []byte("\n"))
		fields := strings.Fields(string(header))
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			return nil, fmt.Errorf("reading %s: git answered %q", paths[i], header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size+1 > len(rest) {
			return nil, fmt.Errorf("reading %s: git answered %q", paths[i], header)
		}
		found[paths[i]] = rest[:size]
		out = rest[size+1:]
	}
	return found, nil
}

// run runs git in the repository and returns its stdout.
//
// Replacement refs are off: a replacement could make the hook read some
// other object than the one pushed. The rest of git's environment is the
// one receive-pack gave the hook, which the client has no way to set, less
// any GIT_ variable but the four that say where this repository and the
// pushed objects are. System and user configuration are not read.
func run(stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.Command(gitProgram, append([]string{"--no-replace-objects"}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// kept are the GIT_ variables receive-pack sets for a hook and that the
// hook's own reads need: the repository, and the quarantine the pushed
// objects sit in until the push is accepted.
var kept = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_QUARANTINE_PATH":              true,
}

func gitEnvironment(inherited []string) []string {
	var env []string
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && !kept[name] {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
