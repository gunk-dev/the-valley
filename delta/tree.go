package delta

// Between reads two trees straight from git's object store, with
// `git cat-file`, and nothing else: no checkout, no filters, no attributes,
// no diff drivers, no rename detection, and no replacement refs.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Repo is a git repository Between reads, and how git is run to read it.
type Repo struct {
	Git string   // the git program; "git" on PATH when empty
	Dir string   // the repository's directory; the current one when empty
	Env []string // git's environment; this process's when nil
}

// modeTree is a subtree's mode as git writes it in a tree.
const modeTree = "40000"

// Between is the delta from the tree pre to the tree post. Each is named
// by the full object ID of a tree, or of a commit, which stands for its
// tree.
//
// Both trees are read whole, and either is refused when a checkout on
// case-sensitive Linux could not hold it faithfully: an entry whose mode
// is not 100644, 100755, 120000 or a subtree's (a submodule among them);
// a name that is empty, . or .., .git in any case, holds a slash, or is
// longer than Linux's limit of 255 bytes; two entries with one name, as a
// file and a directory at one path are; a subtree with no entries, which
// a checkout drops; entries out of git's order; an entry whose object is
// not the type its mode says; and a symlink whose target is empty, holds
// a NUL, or is longer than Linux's limit of 4095 bytes. Every entry of
// both trees is checked, the ones the delta leaves out included.
func Between(r Repo, pre, post string) (Delta, error) {
	return between(r, pre, post, nil)
}

// Budget is what BetweenWithin may spend reading two trees. Every bound
// must be set.
//
// Bytes bounds the bytes the reading takes in: the content of every tree
// and blob it reads from git, and every path it builds on the way. Entries
// bounds the tree entries it visits: each entry once when its tree is read,
// and once more each time the comparison walks it, so a subtree that two
// directories name counts twice. Deadline is when the reading must be
// done. git is killed then, and the reading fails.
type Budget struct {
	Bytes    int64
	Entries  int64
	Deadline time.Time
}

// ErrBudget is wrapped by every error BetweenWithin returns for a bound of
// its budget, so a caller can tell trees too large to read within the
// budget from trees that are refused.
var ErrBudget = errors.New("the budget for reading the trees is spent")

// BetweenWithin is Between held to a budget. It reads what Between reads,
// refuses what Between refuses, and gives the same delta. It also stops at
// the first bound it would cross, and returns an error that wraps
// ErrBudget. An object's type and size are asked for before its content,
// so git never produces an object that would cross the budget. Between
// itself is unbounded.
func BetweenWithin(r Repo, pre, post string, b Budget) (Delta, error) {
	if b.Bytes <= 0 || b.Entries <= 0 || b.Deadline.IsZero() {
		return nil, errors.New("a budget bounds bytes, entries and time, and this one leaves one of them unset")
	}
	ctx, cancel := context.WithDeadline(context.Background(), b.Deadline)
	defer cancel()
	return between(r, pre, post, &limit{budget: b, ctx: ctx})
}

// between is Between, held to l when l is not nil.
func between(r Repo, pre, post string, l *limit) (Delta, error) {
	rd, err := r.open(l)
	if err != nil {
		return nil, err
	}
	defer rd.close()
	var trees [2]string
	for i, oid := range []string{pre, post} {
		if trees[i], err = rd.treeOf(oid); err != nil {
			return nil, err
		}
		if err := rd.check(trees[i], ""); err != nil {
			return nil, err
		}
	}
	if err := rd.checkLeaves(); err != nil {
		return nil, err
	}
	var d Delta
	if err := rd.diff(trees[0], trees[1], "", &d); err != nil {
		return nil, err
	}
	sort.Slice(d, func(i, j int) bool { return d[i].Path < d[j].Path })
	sorted()
	// Sorting asks nothing of git, so a deadline that passes while it runs
	// is caught only here.
	if err := l.late(); err != nil {
		return nil, err
	}
	return d, nil
}

// sorted is called once a delta is sorted, just before the reading checks
// its deadline for the last time. Tests make it slow, to pass the deadline
// there.
var sorted = func() {}

// limit is a reading's budget and what it has spent of it.
type limit struct {
	budget         Budget
	ctx            context.Context // ends at the budget's deadline
	bytes, entries int64
}

// spend counts bytes and entries against the budget, and refuses them
// once either would cross its bound or the deadline has passed. A nil
// limit spends nothing and refuses nothing.
func (l *limit) spend(bytes, entries int64) error {
	if l == nil {
		return nil
	}
	if err := l.late(); err != nil {
		return err
	}
	if l.bytes+bytes > l.budget.Bytes {
		return fmt.Errorf("%w: the trees take more than the %d bytes it allows", ErrBudget, l.budget.Bytes)
	}
	if l.entries+entries > l.budget.Entries {
		return fmt.Errorf("%w: the trees have more than the %d entries it allows", ErrBudget, l.budget.Entries)
	}
	l.bytes += bytes
	l.entries += entries
	return nil
}

// late is the error for a reading whose deadline has passed, or nil.
func (l *limit) late() error {
	if l != nil && l.ctx.Err() != nil {
		return fmt.Errorf("%w: the trees could not be read by its deadline, %s", ErrBudget, l.budget.Deadline.Format(time.RFC3339Nano))
	}
	return nil
}

// failed is the error for a read from git that failed: the deadline's,
// when it has passed, since passing it is what kills git, and otherwise
// err.
func (l *limit) failed(err error) error {
	if late := l.late(); late != nil {
		return late
	}
	return err
}

// treeEntry is one entry of a tree object.
type treeEntry struct {
	mode, name, oid string
}

// reader holds one `git cat-file --batch` open, and what it has read.
type reader struct {
	repo   Repo
	limit  *limit // the budget the reading is held to; nil for none
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	hexLen int                    // an object ID's length in hex, once known
	trees  map[string][]treeEntry // tree → its entries, each tree checked
	leaves map[string]string      // blob → the mode the first entry naming it gave it
	links  map[string]string      // blob → the path of the first symlink whose target it is
	sums   map[string][32]byte    // blob → the SHA-256 of its content
}

// maxTarget is the longest symlink target Linux takes: PATH_MAX, 4096
// bytes, less the NUL that ends it.
const maxTarget = 4095

// command runs git in the repository. With a limit, git is killed when the
// limit's deadline passes.
func (r Repo) command(l *limit, args ...string) *exec.Cmd {
	program := r.Git
	if program == "" {
		program = "git"
	}
	args = append([]string{"--no-replace-objects"}, args...)
	var cmd *exec.Cmd
	if l == nil {
		cmd = exec.Command(program, args...)
	} else {
		cmd = exec.CommandContext(l.ctx, program, args...)
	}
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	return cmd
}

// open starts git cat-file. An unbounded reading asks for each object
// whole, with --batch. A bounded one asks for its type and size first,
// with --batch-command, and for its content only once they fit.
func (r Repo) open(l *limit) (*reader, error) {
	mode := "--batch"
	if l != nil {
		mode = "--batch-command"
	}
	cmd := r.command(l, "cat-file", mode)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, l.failed(fmt.Errorf("could not run git cat-file: %v", err))
	}
	return &reader{
		repo: r, limit: l, cmd: cmd, in: in, out: bufio.NewReader(out),
		trees: map[string][]treeEntry{}, leaves: map[string]string{}, links: map[string]string{},
		sums: map[string][32]byte{},
	}, nil
}

// close ends the reading. An unbounded one lets git finish. A bounded one
// kills it, since a reading stopped at a bound may have left an object
// unread that git would otherwise write out whole.
func (rd *reader) close() {
	rd.in.Close()
	if rd.limit != nil {
		rd.cmd.Process.Kill()
	} else {
		io.Copy(io.Discard, rd.out)
	}
	rd.cmd.Wait()
}

// validID reports whether oid is a full object ID in lowercase hex, of the
// repository's length once that is known.
func (rd *reader) validID(oid string) bool {
	if rd.hexLen != 0 && len(oid) != rd.hexLen {
		return false
	}
	return (len(oid) == 40 || len(oid) == 64) && lowerHex([]byte(oid))
}

// read reads one object whole, and its type. A bounded reading spends the
// object's size before asking for its content.
func (rd *reader) read(oid string) (kind string, data []byte, err error) {
	if !rd.validID(oid) {
		return "", nil, fmt.Errorf("%q is not a full object ID", oid)
	}
	if rd.limit != nil {
		kind, size, err := rd.ask("info", oid)
		if err != nil {
			return "", nil, err
		}
		if err := rd.limit.spend(size, 0); err != nil {
			return "", nil, err
		}
		again, confirmed, err := rd.ask("contents", oid)
		if err != nil {
			return "", nil, err
		}
		if again != kind || confirmed != size {
			return "", nil, fmt.Errorf("the object %s changed between being sized and being read", oid)
		}
		data, err := rd.content(oid, size)
		return kind, data, err
	}
	kind, size, err := rd.ask("", oid)
	if err != nil {
		return "", nil, err
	}
	data, err = rd.content(oid, size)
	return kind, data, err
}

// ask sends one request for oid, with the command given or none, and reads
// the header git answers it with: the object's type and size.
func (rd *reader) ask(command, oid string) (kind string, size int64, err error) {
	if err := rd.limit.late(); err != nil {
		return "", 0, err
	}
	request := oid + "\n"
	if command != "" {
		request = command + " " + request
	}
	if _, err := io.WriteString(rd.in, request); err != nil {
		return "", 0, rd.limit.failed(fmt.Errorf("could not read the object %s: %v", oid, err))
	}
	header, err := rd.out.ReadString('\n')
	if err != nil {
		return "", 0, rd.limit.failed(fmt.Errorf("could not read the object %s: %v", oid, err))
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != oid {
		return "", 0, fmt.Errorf("the object %s is not in the repository", oid)
	}
	size, err = strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("git gave the size of %s as %q", oid, fields[2])
	}
	return fields[1], size, nil
}

// content reads the size bytes of an object git is writing, and the
// newline that closes them.
func (rd *reader) content(oid string, size int64) ([]byte, error) {
	data := make([]byte, size+1)
	if _, err := io.ReadFull(rd.out, data); err != nil || data[size] != '\n' {
		return nil, rd.limit.failed(fmt.Errorf("could not read the object %s whole", oid))
	}
	rd.hexLen = len(oid)
	return data[:size], nil
}

// treeOf is the tree an object ID names: the tree itself, or a commit's.
func (rd *reader) treeOf(oid string) (string, error) {
	kind, data, err := rd.read(oid)
	if err != nil {
		return "", err
	}
	switch kind {
	case "tree":
		return oid, nil
	case "commit":
		line, _, _ := strings.Cut(string(data), "\n")
		tree, ok := strings.CutPrefix(line, "tree ")
		if !ok || !rd.validID(tree) {
			return "", fmt.Errorf("the commit %s does not open with its tree", oid)
		}
		return tree, nil
	}
	return "", fmt.Errorf("%s is a %s, not a tree or a commit", oid, kind)
}

// check reads a tree and every subtree under it, and refuses one that a
// checkout could not hold faithfully, as Between describes. Each tree is
// read and checked once. Its leaves are kept, for checkLeaves. A tree met
// again is still refused as an empty directory, since it may have been
// read first as the empty root, which is no directory.
func (rd *reader) check(oid, at string) error {
	entries, done := rd.trees[oid]
	if !done {
		kind, data, err := rd.read(oid)
		if err != nil {
			return err
		}
		if kind != "tree" {
			return fmt.Errorf("%s is a directory whose object %s is a %s", described(at), oid, kind)
		}
		if entries, err = rd.parseTree(data, at); err != nil {
			return err
		}
	}
	if len(entries) == 0 && at != "" {
		return fmt.Errorf("%s is an empty directory, which a checkout does not hold", described(at))
	}
	if done {
		return nil
	}
	rd.trees[oid] = entries
	for _, e := range entries {
		path := join(at, e.name)
		if err := rd.limit.spend(int64(len(path)), 0); err != nil {
			return err
		}
		if e.mode == modeTree {
			if err := rd.check(e.oid, path); err != nil {
				return err
			}
			continue
		}
		if _, seen := rd.leaves[e.oid]; !seen {
			rd.leaves[e.oid] = e.mode
		}
		if _, seen := rd.links[e.oid]; e.mode == ModeSymlink && !seen {
			rd.links[e.oid] = path
		}
	}
	return nil
}

// parseTree reads a tree object's entries: "<mode> <name>\0<raw ID>" each.
func (rd *reader) parseTree(data []byte, at string) ([]treeEntry, error) {
	size := rd.hexLen / 2
	var entries []treeEntry
	names := map[string]bool{}
	prev := ""
	for len(data) > 0 {
		sp := bytes.IndexByte(data, ' ')
		nul := bytes.IndexByte(data, 0)
		if sp <= 0 || nul < sp || nul+1+size > len(data) {
			return nil, fmt.Errorf("the tree at %s is malformed", described(at))
		}
		e := treeEntry{mode: string(data[:sp]), name: string(data[sp+1 : nul])}
		e.oid = hex.EncodeToString(data[nul+1 : nul+1+size])
		data = data[nul+1+size:]
		path := join(at, e.name)
		if err := rd.limit.spend(int64(len(path)), 1); err != nil {
			return nil, err
		}
		if err := checkName(e.name); err != nil {
			return nil, fmt.Errorf("the tree at %s %w", described(at), err)
		}
		switch e.mode {
		case ModeFile, ModeExec, ModeSymlink, modeTree:
		case "160000":
			return nil, fmt.Errorf("%s is a submodule, which valley-delta-v1 does not support", described(path))
		default:
			return nil, fmt.Errorf("%s has the mode %q, which valley-delta-v1 does not support", described(path), e.mode)
		}
		if names[e.name] {
			return nil, fmt.Errorf("%s is named twice in its tree, as a file and a directory are when they share a path", described(path))
		}
		names[e.name] = true
		// git orders a tree's entries by name, a subtree's read with a
		// slash after it.
		key := e.name
		if e.mode == modeTree {
			key += "/"
		}
		if key <= prev {
			return nil, fmt.Errorf("the tree at %s holds %q out of git's order", described(at), e.name)
		}
		prev = key
		entries = append(entries, e)
	}
	return entries, nil
}

// checkLeaves refuses an entry whose object is not a blob, or is missing,
// for every leaf of the trees checked, with one `git cat-file
// --batch-check`. Then it reads every blob a symlink names, and refuses a
// target a checkout could not make (checkTarget).
func (rd *reader) checkLeaves() error {
	if len(rd.leaves) == 0 {
		return nil
	}
	oids := make([]string, 0, len(rd.leaves))
	for oid := range rd.leaves {
		oids = append(oids, oid)
	}
	sort.Strings(oids)
	cmd := rd.repo.command(rd.limit, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return rd.limit.failed(fmt.Errorf("could not read the trees' entries: %v", err))
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(oids) {
		return errors.New("could not read the trees' entries")
	}
	for i, line := range lines {
		if line != oids[i]+" blob" {
			return fmt.Errorf("an entry with the mode %s names the object %s, which is no blob in the repository", rd.leaves[oids[i]], oids[i])
		}
	}
	for _, oid := range oids {
		path, ok := rd.links[oid]
		if !ok {
			continue
		}
		_, target, err := rd.read(oid)
		if err != nil {
			return err
		}
		if err := checkTarget(target); err != nil {
			return fmt.Errorf("the symlink %s %w", described(path), err)
		}
		rd.sums[oid] = sha256.Sum256(target)
	}
	return nil
}

// checkTarget refuses a symlink's target that symlink(2) on Linux would
// not make: an empty one, one holding a NUL, which would end it early,
// and one longer than maxTarget.
func checkTarget(target []byte) error {
	switch {
	case len(target) == 0:
		return fmt.Errorf("has an empty target, which a checkout cannot make")
	case bytes.IndexByte(target, 0) >= 0:
		return fmt.Errorf("has a target holding a NUL, which a checkout cannot make")
	case len(target) > maxTarget:
		return fmt.Errorf("has a target of %d bytes, longer than Linux's limit of %d", len(target), maxTarget)
	}
	return nil
}

// diff adds to d every path under the directory at whose entries differ
// between the trees pre and post. Either tree may be "", for none. Equal
// trees hold equal entries, so they are not read again.
func (rd *reader) diff(pre, post, at string, d *Delta) error {
	if pre == post {
		return nil
	}
	byName := map[string][2]*treeEntry{}
	for side, tree := range []string{pre, post} {
		if tree == "" {
			continue
		}
		for i := range rd.trees[tree] {
			e := &rd.trees[tree][i]
			pair := byName[e.name]
			pair[side] = e
			byName[e.name] = pair
		}
	}
	for name, pair := range byName {
		path := join(at, name)
		if err := rd.limit.spend(int64(len(path)), 1); err != nil {
			return err
		}
		var sub [2]string
		var leaf [2]*treeEntry
		for side, e := range pair {
			switch {
			case e == nil:
			case e.mode == modeTree:
				sub[side] = e.oid
			default:
				leaf[side] = e
			}
		}
		if sub[0] != "" || sub[1] != "" {
			if err := rd.diff(sub[0], sub[1], path, d); err != nil {
				return err
			}
		}
		if leaf[0] == nil && leaf[1] == nil {
			continue
		}
		if leaf[0] != nil && leaf[1] != nil && *leaf[0] == *leaf[1] {
			continue
		}
		c := Change{Path: path}
		var err error
		if c.Pre, err = rd.image(leaf[0]); err != nil {
			return err
		}
		if c.Post, err = rd.image(leaf[1]); err != nil {
			return err
		}
		if c.Pre != c.Post {
			*d = append(*d, c)
		}
	}
	return nil
}

// image is an entry's image: its mode and the SHA-256 of its blob.
func (rd *reader) image(e *treeEntry) (Image, error) {
	if e == nil {
		return Image{}, nil
	}
	sum, ok := rd.sums[e.oid]
	if !ok {
		kind, data, err := rd.read(e.oid)
		if err != nil {
			return Image{}, err
		}
		if kind != "blob" {
			return Image{}, fmt.Errorf("the object %s is a %s, not a blob", e.oid, kind)
		}
		sum = sha256.Sum256(data)
		rd.sums[e.oid] = sum
	}
	return Image{Mode: e.mode, Sum: sum}, nil
}

func join(at, name string) string {
	if at == "" {
		return name
	}
	return at + "/" + name
}

// described names a path in an error: the root, or the path quoted.
func described(path string) string {
	if path == "" {
		return "the root"
	}
	return strconv.Quote(path)
}
