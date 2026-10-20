package review

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"the-valley/change"
	"the-valley/delta"
)

// Repo is a git repository reviews are read from and written to, and how
// git is run in it: the program, the directory, and the environment. It
// is the repository valley-delta-v1 reads, so one value names both.
type Repo = delta.Repo

// hashSize is the bytes of an object ID. A review names commits as a
// change record does, by SHA-1, so it is read from a SHA-1 repository.
const hashSize = 20

// command runs git in the repository with replacement refs off, killed
// when ctx ends.
func command(ctx context.Context, r Repo, args ...string) *exec.Cmd {
	program := r.Git
	if program == "" {
		program = "git"
	}
	cmd := exec.CommandContext(ctx, program, append([]string{"--no-replace-objects"}, args...)...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env
	return cmd
}

// git runs git in the repository and returns its standard output. A git
// killed at the deadline is reported as late.
func git(ctx context.Context, r Repo, stdin []byte, args ...string) ([]byte, error) {
	cmd := command(ctx, r, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, late(ctx)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return out, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

// late is the error for a reading whose deadline has passed. It wraps
// delta.ErrBudget, the way a delta's reading past its deadline does.
func late(ctx context.Context) error {
	until, _ := ctx.Deadline()
	return fmt.Errorf("%w: the review could not be read by its deadline, %s", delta.ErrBudget, until.Format(time.RFC3339Nano))
}

// objects reads objects one at a time out of one
// `git cat-file --batch-command`. It asks for an object's type and size
// first, and for its content only once they pass every bound, so git never
// produces an object a reading would refuse.
type objects struct {
	ctx   context.Context
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   *bufio.Reader
	bytes int64 // the bytes the stream may read in all
	spent int64
}

// openObjects starts git cat-file, to read at most bytes in all before
// ctx ends.
func openObjects(ctx context.Context, r Repo, bytes int64) (*objects, error) {
	cmd := command(ctx, r, "cat-file", "--batch-command")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, late(ctx)
		}
		return nil, fmt.Errorf("could not run git cat-file: %v", err)
	}
	return &objects{ctx: ctx, cmd: cmd, in: in, out: bufio.NewReader(out), bytes: bytes}, nil
}

// close kills git rather than asking it to finish, since a reading that
// stopped at a bound has nothing more to ask.
func (o *objects) close() {
	o.in.Close()
	o.cmd.Process.Kill()
	o.cmd.Wait()
}

// failed is the error for a read or write of the stream that failed: the
// deadline's, when it has passed, since that is what kills git.
func (o *objects) failed(err error) error {
	if o.ctx.Err() != nil {
		return late(o.ctx)
	}
	return err
}

// ask sends one command and reads git's answer: the object's ID, type and
// size, or that it is missing.
func (o *objects) ask(command, name string) (id, kind string, size int64, err error) {
	if o.ctx.Err() != nil {
		return "", "", 0, late(o.ctx)
	}
	if _, err := fmt.Fprintf(o.in, "%s %s\n", command, name); err != nil {
		return "", "", 0, o.failed(fmt.Errorf("could not be asked for: %v", err))
	}
	header, err := o.out.ReadString('\n')
	if err != nil {
		return "", "", 0, o.failed(fmt.Errorf("could not be read: %v", err))
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return "", "", 0, errors.New("is no object in the repository")
	}
	if len(fields) != 3 {
		return "", "", 0, fmt.Errorf("could not be read: git answered %q", strings.TrimSpace(header))
	}
	size, err = strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return "", "", 0, fmt.Errorf("could not be read: git answered %q", strings.TrimSpace(header))
	}
	return fields[0], fields[1], size, nil
}

// info is the ID, type and size of the object name names, which may be an
// object ID or <commit>:<path>.
func (o *objects) info(name string) (id, kind string, size int64, err error) {
	return o.ask("info", name)
}

// read returns the content of the object id, which must be of type kind
// and at most max bytes, and must fit what is left of the stream's bytes.
func (o *objects) read(id, kind string, max int64) ([]byte, error) {
	_, got, size, err := o.ask("info", id)
	switch {
	case err != nil:
		return nil, err
	case got != kind:
		return nil, fmt.Errorf("is a %s, where a %s is expected", got, kind)
	case size > max:
		return nil, fmt.Errorf("is a %s of %d bytes, more than the %d it may be", kind, size, max)
	case o.spent+size > o.bytes:
		return nil, fmt.Errorf("%w: reading it would take more than the %d bytes the reading may", delta.ErrBudget, o.bytes)
	}
	_, again, confirmed, err := o.ask("contents", id)
	if err != nil {
		return nil, err
	}
	if again != kind || confirmed != size {
		return nil, fmt.Errorf("changed between being sized and being read: git answered %s %d", again, confirmed)
	}
	o.spent += size
	body := make([]byte, size+1) // the content and git's closing newline
	if _, err := io.ReadFull(o.out, body); err != nil || body[size] != '\n' {
		return nil, o.failed(errors.New("could not be read whole"))
	}
	return body[:size], nil
}

// oneFile is the layout of a commit whose tree holds one plain file and
// nothing else, and the bounds on its objects: a review commit's, and a
// change record commit's.
type oneFile struct {
	subject            string // what such a commit is, as a refusal names it
	name               string // the one file its tree holds
	commit, tree, blob int64  // the most bytes each of its objects may hold
}

// readOneFile reads the commit id as a commit of the layout given: its
// parents, in order, and its one file. Each object is held to its bound
// before its content is read.
func (o *objects) readOneFile(id string, l oneFile) (parents []string, file []byte, err error) {
	if !validObjectID(id) {
		return nil, nil, fmt.Errorf("%q is not a full SHA-1 object ID", id)
	}
	body, err := o.read(id, "commit", l.commit)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s %w", l.subject, id, err)
	}
	tree, parents, err := commitLinks(body)
	if err != nil {
		return nil, nil, fmt.Errorf("%s %s %w", l.subject, id, err)
	}
	entries, err := o.read(tree, "tree", l.tree)
	if err != nil {
		return nil, nil, fmt.Errorf("the tree of %s %s %w", l.subject, id, err)
	}
	want := "100644 " + l.name + "\x00"
	if len(entries) != len(want)+hashSize || string(entries[:len(want)]) != want {
		return nil, nil, fmt.Errorf("the tree of %s %s holds something other than %s alone, as a plain file", l.subject, id, l.name)
	}
	blob := hex.EncodeToString(entries[len(want):])
	if file, err = o.read(blob, "blob", l.blob); err != nil {
		return nil, nil, fmt.Errorf("the %s of %s %s %w", l.name, l.subject, id, err)
	}
	return parents, file, nil
}

// commitLinks reads the tree and the parents out of a commit object, as
// git reads them: a tree line first, and then every parent line that
// follows it directly. A parent line anywhere later is one git ignores,
// and a commit that holds one is refused rather than read two ways.
func commitLinks(body []byte) (tree string, parents []string, err error) {
	header, _, _ := bytes.Cut(body, []byte("\n\n"))
	lines := strings.Split(string(header), "\n")
	tree, ok := strings.CutPrefix(lines[0], "tree ")
	if !ok || !validObjectID(tree) {
		return "", nil, errors.New("is a commit git could not have written: it does not open with its tree")
	}
	rest := lines[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "parent ") {
		parent := strings.TrimPrefix(rest[0], "parent ")
		if !validObjectID(parent) {
			return "", nil, errors.New("is a commit git could not have written: a parent is not an object ID")
		}
		parents = append(parents, parent)
		rest = rest[1:]
	}
	for _, line := range rest {
		if strings.HasPrefix(line, "parent ") {
			return "", nil, errors.New("names a parent after its other headers, which git does not read as a parent")
		}
	}
	return tree, parents, nil
}

// validObjectID reports whether s is a full SHA-1 object ID in lowercase
// hex.
func validObjectID(s string) bool {
	return len(s) == 2*hashSize && lowerHex(s)
}

// none reports whether id names no commit: empty, or git's all-zero ID.
func none(id string) bool {
	return strings.Trim(id, "0") == ""
}

// objectOf is the object ID a git-sha1:<hex> name stands for.
func objectOf(name string) string {
	return strings.TrimPrefix(name, change.HeadPrefix)
}
