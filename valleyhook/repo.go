package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

// The bounds on reading an attestation ref. attest writes a tree holding
// one subtree per check, each holding one note, and the integrator writes a
// tree holding notes directly, so these are far above anything either
// writes. They exist because the tree is a pushed object, and git trees can
// share subtrees: a few dozen small trees, each naming the next many times
// over, describe more paths than any machine can walk. The walk below
// counts every entry it visits, a shared subtree once per time it is
// named, and stops at the first bound it would cross, before reading or
// allocating anything past it.
const (
	maxDepth         = 3        // trees from the ref's tree down, inclusive
	maxEntriesInTree = 64       // entries in any one tree
	maxEntries       = 256      // entries visited in the whole walk
	maxTreeSize      = 16 << 10 // bytes in any one tree object
	maxNoteSize      = 64 << 10 // bytes in any one note
	maxBytes         = 1 << 20  // bytes read in the whole walk
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

// notes walks the tree id names and returns every note in it, keyed by
// path. Every tree is read one level at a time and every object's type and
// size are read before its content, so a bound is enforced before the
// bytes it bounds are read. Any entry that is not a tree or a plain file,
// and any bound crossed, refuses the whole tree.
func (gitRepository) notes(id string) (map[string][]byte, error) {
	objects, err := openObjects()
	if err != nil {
		return nil, err
	}
	defer objects.close()

	w := walk{objects: objects, hashSize: len(id) / 2, found: map[string][]byte{}}
	if err := w.tree(id, "", 1); err != nil {
		return nil, err
	}
	if len(w.found) == 0 {
		return nil, errors.New("it points at a tree holding no note, and an attestation holds at least one")
	}
	return w.found, nil
}

// walk is one bounded walk of an attestation's tree.
type walk struct {
	objects  *objectStream
	hashSize int
	entries  int
	found    map[string][]byte
}

func (w *walk) tree(id, path string, depth int) error {
	body, err := w.objects.read(id, "tree", maxTreeSize)
	if err != nil {
		if path == "" {
			return fmt.Errorf("it %v", err)
		}
		return fmt.Errorf("%s %v", path, err)
	}
	inTree := 0
	for len(body) > 0 {
		inTree++
		w.entries++
		switch {
		case inTree > maxEntriesInTree:
			return fmt.Errorf("a tree in it holds more than %d entries, the most an attestation's tree may", maxEntriesInTree)
		case w.entries > maxEntries:
			return fmt.Errorf("its trees name more than %d entries in all, the most an attestation may", maxEntries)
		}
		mode, name, child, rest, err := treeEntry(body, w.hashSize)
		if err != nil {
			return err
		}
		body = rest
		at := name
		if path != "" {
			at = path + "/" + name
		}
		switch mode {
		case "40000":
			if depth+1 > maxDepth {
				return fmt.Errorf("%s nests trees deeper than the %d an attestation may", at, maxDepth)
			}
			if err := w.tree(child, at, depth+1); err != nil {
				return err
			}
		case "100644":
			note, err := w.objects.read(child, "blob", maxNoteSize)
			if err != nil {
				return fmt.Errorf("%s %v", at, err)
			}
			w.found[at] = note
		default:
			return fmt.Errorf("%s has mode %s, and an attestation holds only notes, as plain files, and trees of them", at, mode)
		}
	}
	return nil
}

// treeEntry reads one entry of a tree object as git stores it: the mode in
// octal, a space, the name, a NUL, and the raw object id.
func treeEntry(body []byte, hashSize int) (mode, name, id string, rest []byte, err error) {
	space := bytes.IndexByte(body, ' ')
	nul := bytes.IndexByte(body, 0)
	if space < 0 || nul < space || len(body) < nul+1+hashSize {
		return "", "", "", nil, errors.New("it holds a tree git could not have written")
	}
	return string(body[:space]), string(body[space+1 : nul]),
		hex.EncodeToString(body[nul+1 : nul+1+hashSize]), body[nul+1+hashSize:], nil
}

// objectStream reads objects one at a time out of a single
// `git cat-file --batch`, which answers each request with the object's id,
// type and size on one line before its content.
type objectStream struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   *bufio.Reader
	spent int
}

func openObjects() (*objectStream, error) {
	cmd := exec.Command(gitProgram, "--no-replace-objects", "cat-file", "--batch")
	cmd.Env = gitEnvironment(os.Environ())
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &objectStream{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 4096)}, nil
}

// read returns the content of object id, which must be of type kind and at
// most max bytes, and must fit what is left of the walk's byte budget.
// Both are checked against the size git reports before the content is
// read, so an object past a bound is never read at all.
func (s *objectStream) read(id, kind string, max int) ([]byte, error) {
	if _, err := fmt.Fprintln(s.in, id); err != nil {
		return nil, err
	}
	header, err := s.out.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("could not be read: %v", err)
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return nil, errors.New("names an object the push did not bring")
	}
	if len(fields) != 3 {
		return nil, fmt.Errorf("could not be read: git answered %q", strings.TrimSpace(header))
	}
	if fields[1] != kind {
		return nil, fmt.Errorf("points at a %s, where an attestation holds a %s", fields[1], kind)
	}
	size, err := strconv.Atoi(fields[2])
	switch {
	case err != nil || size < 0:
		return nil, fmt.Errorf("could not be read: git answered %q", strings.TrimSpace(header))
	case size > max:
		return nil, fmt.Errorf("is a %s of %d bytes, more than the %d an attestation's may be", kind, size, max)
	case s.spent+size > maxBytes:
		return nil, fmt.Errorf("would take the walk past the %d bytes an attestation may hold", maxBytes)
	}
	s.spent += size
	body := make([]byte, size+1) // the content and git's closing newline
	if _, err := io.ReadFull(s.out, body); err != nil {
		return nil, fmt.Errorf("could not be read: %v", err)
	}
	return body[:size], nil
}

// close ends the stream. A walk that stopped at a bound leaves an object
// unread, so the process is killed rather than drained.
func (s *objectStream) close() {
	_ = s.in.Close()
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
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
