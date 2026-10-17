package delta

// Between reads two trees straight from git's object store, with
// `git cat-file`, and nothing else: no checkout, no filters, no attributes,
// no diff drivers, no rename detection, and no replacement refs.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
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
// a name that is empty, . or .., .git in any case, or holds a slash; two
// entries with one name, as a file and a directory at one path are; a
// subtree with no entries, which a checkout drops; entries out of git's
// order; and an entry whose object is not the type its mode says.
func Between(r Repo, pre, post string) (Delta, error) {
	rd, err := r.open()
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
	return d, nil
}

// treeEntry is one entry of a tree object.
type treeEntry struct {
	mode, name, oid string
}

// reader holds one `git cat-file --batch` open, and what it has read.
type reader struct {
	repo   Repo
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	hexLen int                    // an object ID's length in hex, once known
	trees  map[string][]treeEntry // tree → its entries, each tree checked
	leaves map[string]string      // blob → the mode an entry gave it
	sums   map[string][32]byte    // blob → the SHA-256 of its content
}

func (r Repo) command(args ...string) *exec.Cmd {
	program := r.Git
	if program == "" {
		program = "git"
	}
	cmd := exec.Command(program, append([]string{"--no-replace-objects"}, args...)...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	return cmd
}

func (r Repo) open() (*reader, error) {
	cmd := r.command("cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not run git cat-file: %v", err)
	}
	return &reader{
		repo: r, cmd: cmd, in: in, out: bufio.NewReader(out),
		trees: map[string][]treeEntry{}, leaves: map[string]string{}, sums: map[string][32]byte{},
	}, nil
}

func (rd *reader) close() {
	rd.in.Close()
	io.Copy(io.Discard, rd.out)
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

// read reads one object whole, and its type.
func (rd *reader) read(oid string) (kind string, data []byte, err error) {
	if !rd.validID(oid) {
		return "", nil, fmt.Errorf("%q is not a full object ID", oid)
	}
	if _, err := io.WriteString(rd.in, oid+"\n"); err != nil {
		return "", nil, fmt.Errorf("could not read the object %s: %v", oid, err)
	}
	header, err := rd.out.ReadString('\n')
	if err != nil {
		return "", nil, fmt.Errorf("could not read the object %s: %v", oid, err)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != oid {
		return "", nil, fmt.Errorf("the object %s is not in the repository", oid)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return "", nil, fmt.Errorf("git gave the size of %s as %q", oid, fields[2])
	}
	data = make([]byte, size+1)
	if _, err := io.ReadFull(rd.out, data); err != nil || data[size] != '\n' {
		return "", nil, fmt.Errorf("could not read the object %s whole", oid)
	}
	rd.hexLen = len(oid)
	return fields[1], data[:size], nil
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
// read and checked once. Its leaves are kept, for checkLeaves.
func (rd *reader) check(oid, at string) error {
	if _, done := rd.trees[oid]; done {
		return nil
	}
	kind, data, err := rd.read(oid)
	if err != nil {
		return err
	}
	if kind != "tree" {
		return fmt.Errorf("%s is a directory whose object %s is a %s", described(at), oid, kind)
	}
	entries, err := rd.parseTree(data, at)
	if err != nil {
		return err
	}
	if len(entries) == 0 && at != "" {
		return fmt.Errorf("%s is an empty directory, which a checkout does not hold", described(at))
	}
	rd.trees[oid] = entries
	for _, e := range entries {
		path := join(at, e.name)
		if e.mode == modeTree {
			if err := rd.check(e.oid, path); err != nil {
				return err
			}
			continue
		}
		if _, seen := rd.leaves[e.oid]; !seen {
			rd.leaves[e.oid] = e.mode
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
// --batch-check`.
func (rd *reader) checkLeaves() error {
	if len(rd.leaves) == 0 {
		return nil
	}
	oids := make([]string, 0, len(rd.leaves))
	for oid := range rd.leaves {
		oids = append(oids, oid)
	}
	sort.Strings(oids)
	cmd := rd.repo.command("cat-file", "--batch-check=%(objectname) %(objecttype)")
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("could not read the trees' entries: %v", err)
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
