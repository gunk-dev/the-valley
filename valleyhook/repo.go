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
	"time"
)

// gitProgram is the git this program runs. The package sets it at build
// time to an absolute store path, so nothing in a caller's PATH decides
// which git reads the repository.
var gitProgram = "git"

// repository is what a decision needs to know about the repository a push
// is landing in.
type repository interface {
	// symbolic reports which of refs are symbolic refs there now, all in
	// one read.
	symbolic(refs []string) (map[string]bool, error)
	// notes returns every blob under the tree id names, keyed by path,
	// or an error saying why id is not such a tree.
	notes(id string) (map[string][]byte, error)
	// record reads the change record commit id names (record.go): its
	// parents, and the record.json its tree holds, or an error saying why
	// id is not such a commit.
	record(id string) (recordCommit, error)
	// kind is the type of the object id names.
	kind(id string) (string, error)
	// records counts the change records the repository holds now, with
	// one for-each-ref.
	records() (int, error)
	// graph opens a reading of the plan graph's objects (knots.go),
	// within the graph's own bounds.
	graph() (graphReader, error)
}

// budget is what one reading of pushed objects may cost: how deep its
// trees nest, the root's counted as 1; the entries in any one tree and in
// the whole walk; the bytes in any one tree and in any one file; the bytes
// read in all; and, when it is not zero, how long the reading may take.
// The bounds exist because what is read is a pushed object, and git trees
// can share subtrees: a few dozen small trees, each naming the next many
// times over, describe more paths than any machine can walk. A walk
// counts every entry it visits, a shared subtree once per time it is
// named, and stops at the first bound it would cross, before reading or
// allocating anything past it. Each reader passes its own budget.
type budget struct {
	// subject is what the objects make up, as a refusal names it.
	subject                       string
	depth, entriesInTree, entries int
	treeSize, fileSize, bytes     int
	deadline                      time.Duration
}

// The bounds on reading an attestation ref. attest writes a tree holding
// one subtree per check, each holding one note, and the integrator writes a
// tree holding notes directly, so these are far above anything either
// writes.
const (
	maxDepth         = 3        // trees from the ref's tree down, inclusive
	maxEntriesInTree = 64       // entries in any one tree
	maxEntries       = 256      // entries visited in the whole walk
	maxTreeSize      = 16 << 10 // bytes in any one tree object
	maxNoteSize      = 64 << 10 // bytes in any one note
	maxBytes         = 1 << 20  // bytes read in the whole walk
)

var attestationBudget = budget{
	subject: "an attestation", depth: maxDepth, entriesInTree: maxEntriesInTree, entries: maxEntries,
	treeSize: maxTreeSize, fileSize: maxNoteSize, bytes: maxBytes,
}

// gitRepository reads the repository the hook runs in. receive-pack runs
// the hook inside the repository, with the pushed objects in a quarantine
// directory its environment names, so the objects a push brings are
// readable here before any ref points at them.
type gitRepository struct{}

// symbolic asks git, with git symbolic-ref, whether each of refs is a
// symbolic ref. It is git's own reading, so it agrees with git on every
// form git accepts: "ref:" followed by any white space, a filesystem
// symlink in the files backend, any other ref storage, and a dangling
// symbolic ref that names a ref which does not exist. for-each-ref skips a
// dangling one, and a push through it would create the ref it names,
// which could be a protected main a project does not have yet. That is one
// git call per ref, and preReceive bounds a push at maxUpdates refs.
func (gitRepository) symbolic(refs []string) (map[string]bool, error) {
	found := map[string]bool{}
	for _, ref := range refs {
		symbolic, err := symbolicRef(ref)
		if err != nil {
			return nil, fmt.Errorf("whether %s is a symbolic ref could not be read: %w", ref, err)
		}
		found[ref] = symbolic
	}
	return found, nil
}

func symbolicRef(ref string) (bool, error) {
	_, err := run(nil, "symbolic-ref", "--quiet", "--end-of-options", ref)
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
// path. Every tree is read one level at a time, and every object's type
// and size are asked for, and checked against the bounds, before its
// content is asked for at all. Any entry that is not a tree or a plain
// file, and any bound crossed, refuses the whole tree.
func (gitRepository) notes(id string) (map[string][]byte, error) {
	objects, err := openObjects()
	if err != nil {
		return nil, err
	}
	defer objects.close()
	return walkNotes(objects, id)
}

func walkNotes(objects *objectStream, id string) (map[string][]byte, error) {
	found := map[string][]byte{}
	w := walk{objects: objects, hashSize: len(id) / 2, visit: func(at, mode, child string) error {
		switch mode {
		case "40000":
			return nil
		case "100644":
			note, err := objects.read(child, "blob", maxNoteSize)
			if err != nil {
				return fmt.Errorf("%s %v", at, err)
			}
			found[at] = note
			return nil
		}
		return fmt.Errorf("%s has mode %s, and an attestation holds only notes, as plain files, and trees of them", at, mode)
	}}
	if err := w.tree(id, "", 1); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, errors.New("it points at a tree holding no note, and an attestation holds at least one")
	}
	return found, nil
}

// walk is one bounded walk of a tree, within its stream's budget. It
// hands visit every entry it finds, trees included, and walks each tree
// after visit has seen it.
type walk struct {
	objects  *objectStream
	hashSize int
	entries  int
	visit    func(at, mode, id string) error
}

func (w *walk) tree(id, path string, depth int) error {
	b := w.objects.budget
	body, err := w.objects.read(id, "tree", b.treeSize)
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
		case inTree > b.entriesInTree:
			return fmt.Errorf("a tree in it holds more than %d entries, the most %s's tree may", b.entriesInTree, b.subject)
		case w.entries > b.entries:
			return fmt.Errorf("its trees name more than %d entries in all, the most %s may", b.entries, b.subject)
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
		if mode == "40000" && depth+1 > b.depth {
			return fmt.Errorf("%s nests trees deeper than the %d %s may", at, b.depth, b.subject)
		}
		if err := w.visit(at, mode, child); err != nil {
			return err
		}
		if mode == "40000" {
			if err := w.tree(child, at, depth+1); err != nil {
				return err
			}
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
// `git cat-file --batch-command`. It asks for an object's type and size
// first (`info`), and asks for its content (`contents`) only once those
// have passed every bound, so git is never asked to produce an object the
// walk would refuse. requests records what was asked, in order.
type objectStream struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	out      *bufio.Reader
	spent    int
	requests []string

	// budget is what the reading may cost, and names what the objects
	// make up in a refusal. until is when its deadline passes, if it has
	// one.
	budget budget
	until  time.Time
}

// openObjects opens a stream within an attestation's budget.
func openObjects() (*objectStream, error) {
	return openObjectsWithin(attestationBudget)
}

func openObjectsWithin(b budget) (*objectStream, error) {
	cmd := exec.Command(gitProgram, "--no-replace-objects", "cat-file", "--batch-command")
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
	s := &objectStream{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 4096), budget: b}
	if b.deadline > 0 {
		s.until = time.Now().Add(b.deadline)
	}
	return s, nil
}

// ask sends one command and reads the header line git answers it with:
// the object's id, type and size.
func (s *objectStream) ask(command, id string) (kind string, size int, err error) {
	if !s.until.IsZero() && time.Now().After(s.until) {
		return "", 0, fmt.Errorf("could not be read within the %s the hook spends reading %s", s.budget.deadline, s.budget.subject)
	}
	s.requests = append(s.requests, command+" "+id)
	if _, err := fmt.Fprintf(s.in, "%s %s\n", command, id); err != nil {
		return "", 0, err
	}
	header, err := s.out.ReadString('\n')
	if err != nil {
		return "", 0, fmt.Errorf("could not be read: %v", err)
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return "", 0, errors.New("names an object the push did not bring")
	}
	if len(fields) != 3 {
		return "", 0, fmt.Errorf("could not be read: git answered %q", strings.TrimSpace(header))
	}
	size, err = strconv.Atoi(fields[2])
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("could not be read: git answered %q", strings.TrimSpace(header))
	}
	return fields[1], size, nil
}

// read returns the content of object id, which must be of type kind and at
// most max bytes, and must fit what is left of the walk's byte budget. The
// type and size are asked for and checked first; the content is asked for
// only once they pass, so an object past a bound is never produced.
func (s *objectStream) read(id, kind string, max int) ([]byte, error) {
	got, size, err := s.ask("info", id)
	switch {
	case err != nil:
		return nil, err
	case got != kind:
		return nil, fmt.Errorf("points at a %s, where %s holds a %s", got, s.budget.subject, kind)
	case size > max:
		return nil, fmt.Errorf("is a %s of %d bytes, more than the %d %s's may be", kind, size, max, s.budget.subject)
	case s.spent+size > s.budget.bytes:
		return nil, fmt.Errorf("would take the walk past the %d bytes %s may hold", s.budget.bytes, s.budget.subject)
	}
	again, confirmed, err := s.ask("contents", id)
	if err != nil {
		return nil, err
	}
	if again != kind || confirmed != size {
		return nil, fmt.Errorf("changed between being sized and being read: git answered %s %d", again, confirmed)
	}
	s.spent += size
	body := make([]byte, size+1) // the content and git's closing newline
	if _, err := io.ReadFull(s.out, body); err != nil {
		return nil, fmt.Errorf("could not be read: %v", err)
	}
	return body[:size], nil
}

// close ends the stream. The process is killed rather than asked to
// finish, since a walk that stopped at a bound has nothing more to ask.
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
