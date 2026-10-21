package derive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Diff returns every side of the diff from base to head in the repository
// at dir, sorted bytewise and each once. It reads it as `valley checks`
// does: `git diff --no-relative --name-status -M -z`, so renames are
// detected and both sides of one count, and every path is read whole
// whatever bytes it holds (Changed). Replacement refs are off, so no
// refs/replace/* can change which trees are compared.
//
// git runs in env, the caller's choice of environment; nil is this
// process's. A git that fails, and output that is cut short, are errors:
// read as no paths, either would be a change that requires nothing.
func Diff(ctx context.Context, dir string, env []string, base, head string) ([]string, error) {
	for _, rev := range []string{base, head} {
		if rev == "" || strings.HasPrefix(rev, "-") {
			return nil, fmt.Errorf("%q is not a commit to diff", rev)
		}
	}
	cmd := exec.CommandContext(ctx, "git", "--no-replace-objects", "diff",
		"--no-relative", "--name-status", "-M", "-z", base, head, "--")
	cmd.Dir, cmd.Env = dir, env
	cmd.WaitDelay = 5 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("could not diff %s..%s: %w: %s", base, head, err, strings.TrimSpace(errb.String()))
	}
	paths, err := Changed(out.Bytes())
	if err != nil {
		return nil, err
	}
	return sortedSet(paths), nil
}

// Changed reads every side of a diff from the output of `git diff -z
// --name-status`: an added, modified or deleted path, and both the old and
// the new path of a rename or a copy, in the order git gave them.
//
// Each field ends in NUL, so a path is read whole, newlines and bytes that
// are not UTF-8 included. Output that ends partway through a record is
// refused, a record cut inside its status too, and so is a record that is
// not one git writes: an unknown status, or an empty path.
func Changed(out []byte) ([]string, error) {
	var paths []string
	for len(out) > 0 {
		status, after, ok := bytes.Cut(out, []byte{0})
		if !ok {
			return nil, errTruncated
		}
		if !validStatus(status) {
			return nil, fmt.Errorf("the diff holds a record whose status is %q, which git does not write", status)
		}
		out = after
		sides := 1
		if status[0] == 'R' || status[0] == 'C' {
			sides = 2
		}
		for ; sides > 0; sides-- {
			path, after, ok := bytes.Cut(out, []byte{0})
			if !ok {
				return nil, errTruncated
			}
			if len(path) == 0 {
				return nil, errors.New("the diff holds a record with an empty path, which git does not write")
			}
			paths = append(paths, string(path))
			out = after
		}
	}
	return paths, nil
}

var errTruncated = errors.New("truncated diff output")

// validStatus reports whether s is a status --name-status writes: one
// letter, and for a rename or a copy the similarity after it.
func validStatus(s []byte) bool {
	if len(s) == 0 || !strings.ContainsRune("ACDMRTUXB", rune(s[0])) {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
