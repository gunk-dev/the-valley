package knot

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// OpTrailer is the trailer every graph commit carries: the ID of the
// operation it records. The valley command picks the ID once for an
// operation and keeps it across every retry, so after a push whose outcome
// it cannot tell, it can look on the ref for the operation before trying
// it again.
const OpTrailer = "Valley-Op"

// Commit is what a graph commit says: its tree, its parents in order, the
// name its committer line gives, and the operation its trailer names.
type Commit struct {
	Tree      string
	Parents   []string
	Committer string
	Op        string
}

// ParseCommit reads a commit object as git stores it, with object IDs of
// hashSize bytes. The tree line comes first and the parent lines directly
// after it, as git reads them; a parent line anywhere later is one git
// ignores, and a commit that holds one is refused rather than read two
// ways. The message's last line is the Valley-Op trailer, and no other
// line names it.
func ParseCommit(body []byte, hashSize int) (Commit, error) {
	header, message, ok := bytes.Cut(body, []byte("\n\n"))
	if !ok {
		return Commit{}, errors.New("it is a commit with no message, and a graph commit's message carries its Valley-Op trailer")
	}
	lines := strings.Split(string(header), "\n")
	var c Commit
	tree, found := strings.CutPrefix(lines[0], "tree ")
	if !found || !isHex(tree, hashSize) {
		return Commit{}, errors.New("it is a commit git could not have written: it does not open with its tree")
	}
	c.Tree = tree
	rest := lines[1:]
	for len(rest) > 0 && strings.HasPrefix(rest[0], "parent ") {
		parent := strings.TrimPrefix(rest[0], "parent ")
		if !isHex(parent, hashSize) {
			return Commit{}, errors.New("it is a commit git could not have written: a parent is not an object ID")
		}
		c.Parents = append(c.Parents, parent)
		rest = rest[1:]
	}
	committers := 0
	for _, line := range rest {
		switch {
		case strings.HasPrefix(line, "parent "):
			return Commit{}, errors.New("it names a parent after its other headers, which git does not read as a parent")
		case strings.HasPrefix(line, "committer "):
			committers++
			who, _, ok := strings.Cut(strings.TrimPrefix(line, "committer "), " <")
			if !ok {
				return Commit{}, errors.New("its committer line names no email, as git writes one")
			}
			c.Committer = who
		}
	}
	if committers != 1 {
		return Commit{}, fmt.Errorf("it has %d committer lines, and a commit has one", committers)
	}
	op, err := OpOf(string(message))
	if err != nil {
		return Commit{}, err
	}
	c.Op = op
	return c, nil
}

// OpOf is the operation a graph commit's message names in its trailer:
// the message's last line is "Valley-Op: <16 hex digits>", and no other
// line opens with Valley-Op.
func OpOf(message string) (string, error) {
	lines := strings.Split(strings.TrimRight(message, "\n"), "\n")
	last := lines[len(lines)-1]
	op, ok := strings.CutPrefix(last, OpTrailer+": ")
	if !ok || !IsOpID(op) {
		return "", fmt.Errorf("its message does not end with a %s: <16 hex digits> trailer, which every graph commit carries", OpTrailer)
	}
	for _, line := range lines[:len(lines)-1] {
		if strings.HasPrefix(line, OpTrailer) {
			return "", fmt.Errorf("its message names %s on more than its last line", OpTrailer)
		}
	}
	return op, nil
}

// Message is the commit message of one operation: a subject, and the
// trailer naming the operation.
func Message(subject, op string) string {
	return subject + "\n\n" + OpTrailer + ": " + op + "\n"
}

func isHex(s string, size int) bool {
	if len(s) != 2*size {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
