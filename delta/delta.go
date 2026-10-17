// Package delta is valley-delta-v1: the change between two git trees, as
// whole files.
//
// The delta of a tree R against a tree T is the set of paths whose entries
// differ between T and R. Each path has a pre-image, its entry in T or
// absent, and a post-image, its entry in R or absent. An entry is a git
// mode and the SHA-256 of the content, as in valley-tree-v1: the mode is
// 100644, 100755 or 120000, and a symlink's content is its target. A
// rename is a deletion and an addition. Whole contents are hashed, so
// binary content, the executable bit, symlink targets, CRLF and final
// newlines are all bound, and nothing is positional.
//
// The encoding is exact. It is the header line, then one line per path,
// sorted bytewise, each path once:
//
//	valley-delta-v1\n
//	<len> <path> <pre> <post>\n
//
// <len> is the path's byte length in decimal, with no sign and no leading
// zero. <path> is the path's raw bytes as git stores them in the tree, so a
// reader takes exactly <len> bytes of it, spaces and newlines included.
// Each image is - for absent, or <mode> <sha256> with the hash in 64
// lowercase hex digits. Paths are never cleaned, case-folded or
// Unicode-normalised. The digest is the SHA-256 of the encoding.
//
// Parse reads an encoding strictly: it accepts exactly the encodings
// Between can produce, and Encode writes a parsed delta back byte for byte.
// The one exception is a symlink's target. Between refuses a target that
// holds a NUL or is too long, and a hash does not show either.
//
// The package depends on the standard library alone, and on git for
// Between.
package delta

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Header opens every encoding.
const Header = "valley-delta-v1\n"

// The modes an entry may have: a file, an executable file, and a symlink.
const (
	ModeFile    = "100644"
	ModeExec    = "100755"
	ModeSymlink = "120000"
)

// Image is a path's entry on one side of a delta: its mode and the SHA-256
// of its content. The zero Image is absent.
type Image struct {
	Mode string // ModeFile, ModeExec or ModeSymlink; "" for absent
	Sum  [32]byte
}

// Absent reports whether the path has no entry on this side.
func (i Image) Absent() bool { return i.Mode == "" }

// String is the image as the encoding writes it.
func (i Image) String() string {
	if i.Absent() {
		return "-"
	}
	return i.Mode + " " + hex.EncodeToString(i.Sum[:])
}

// Change is one path of a delta, with its two images. The images differ.
type Change struct {
	Path      string // the raw bytes git stores, which need not be UTF-8
	Pre, Post Image
}

// Delta is the changes between two trees, sorted bytewise by path, each
// path once.
type Delta []Change

// Encode is the delta's valley-delta-v1 encoding.
func (d Delta) Encode() []byte {
	var b bytes.Buffer
	b.WriteString(Header)
	for _, c := range d {
		b.WriteString(strconv.Itoa(len(c.Path)))
		b.WriteByte(' ')
		b.WriteString(c.Path)
		b.WriteByte(' ')
		b.WriteString(c.Pre.String())
		b.WriteByte(' ')
		b.WriteString(c.Post.String())
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// Digest is the SHA-256 of the delta's encoding, in lowercase hex.
func (d Delta) Digest() string {
	sum := sha256.Sum256(d.Encode())
	return hex.EncodeToString(sum[:])
}

// maxLenDigits bounds the digits of a length, so that reading one can
// never overflow. No path git can store is near ten digits long.
const maxLenDigits = 10

// Parse reads a valley-delta-v1 encoding, strictly. It refuses anything
// Encode would not write for some pair of trees Between accepts: another
// header, a length with a sign or a leading zero, or one that does not
// end at a space; a path that is empty, out of order or given twice, or
// that no tree could hold; an image whose mode is not one of the three,
// or whose hash is not 64 lowercase hex digits; a symlink whose hash is
// that of empty content, since its target would be empty; a line whose
// images are equal; and a missing final newline or anything after it.
func Parse(data []byte) (Delta, error) {
	if !bytes.HasPrefix(data, []byte(Header)) {
		return nil, fmt.Errorf("it does not open with the header %q", strings.TrimSuffix(Header, "\n"))
	}
	rest := data[len(Header):]
	var d Delta
	for k := 1; len(rest) > 0; k++ {
		c, after, err := parseLine(rest)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", k, err)
		}
		if n := len(d); n > 0 {
			switch prev := d[n-1].Path; {
			case c.Path == prev:
				return nil, fmt.Errorf("entry %d: the path %q is given twice", k, c.Path)
			case c.Path < prev:
				return nil, fmt.Errorf("entry %d: the path %q is out of order after %q", k, c.Path, prev)
			}
		}
		d = append(d, c)
		rest = after
	}
	if err := d.checkSides(); err != nil {
		return nil, err
	}
	return d, nil
}

// parseLine reads one line of an encoding, and returns what follows it.
func parseLine(b []byte) (Change, []byte, error) {
	digits := 0
	for digits < len(b) && b[digits] >= '0' && b[digits] <= '9' {
		digits++
	}
	switch {
	case digits == 0:
		return Change{}, nil, fmt.Errorf("it does not open with a length")
	case digits > maxLenDigits:
		return Change{}, nil, fmt.Errorf("its length has more than %d digits", maxLenDigits)
	case b[0] == '0' && digits > 1:
		return Change{}, nil, fmt.Errorf("its length %s has a leading zero", b[:digits])
	case digits == len(b) || b[digits] != ' ':
		return Change{}, nil, fmt.Errorf("its length is not followed by a space")
	}
	n, err := strconv.Atoi(string(b[:digits]))
	if err != nil {
		return Change{}, nil, fmt.Errorf("its length %s does not read: %v", b[:digits], err)
	}
	b = b[digits+1:]
	if n > len(b) {
		return Change{}, nil, fmt.Errorf("its length %d runs past the end", n)
	}
	c := Change{Path: string(b[:n])}
	if err := checkPath(c.Path); err != nil {
		return Change{}, nil, err
	}
	b = b[n:]
	if len(b) == 0 || b[0] != ' ' {
		return Change{}, nil, fmt.Errorf("the path %q is not followed by a space", c.Path)
	}
	if c.Pre, b, err = parseImage(b[1:]); err != nil {
		return Change{}, nil, fmt.Errorf("the pre-image of %q: %w", c.Path, err)
	}
	if len(b) == 0 || b[0] != ' ' {
		return Change{}, nil, fmt.Errorf("the pre-image of %q is not followed by a space", c.Path)
	}
	if c.Post, b, err = parseImage(b[1:]); err != nil {
		return Change{}, nil, fmt.Errorf("the post-image of %q: %w", c.Path, err)
	}
	if len(b) == 0 || b[0] != '\n' {
		return Change{}, nil, fmt.Errorf("the line of %q does not end in a newline after its post-image", c.Path)
	}
	if c.Pre == c.Post {
		return Change{}, nil, fmt.Errorf("the path %q has the same pre-image and post-image, so it is no change", c.Path)
	}
	return c, b[1:], nil
}

// parseImage reads one image, and returns what follows it.
func parseImage(b []byte) (Image, []byte, error) {
	if len(b) > 0 && b[0] == '-' {
		return Image{}, b[1:], nil
	}
	mode := 0
	for mode < len(b) && mode <= len(ModeFile) && b[mode] != ' ' {
		mode++
	}
	if err := checkMode(string(b[:mode])); err != nil {
		return Image{}, nil, err
	}
	if mode == len(b) {
		return Image{}, nil, fmt.Errorf("its mode is not followed by a space")
	}
	i := Image{Mode: string(b[:mode])}
	b = b[mode+1:]
	const hexLen = 2 * sha256.Size
	if len(b) < hexLen || !lowerHex(b[:hexLen]) || (len(b) > hexLen && isHexDigit(b[hexLen])) {
		return Image{}, nil, fmt.Errorf("its hash is not 64 lowercase hex digits")
	}
	hex.Decode(i.Sum[:], b[:hexLen])
	if i.Mode == ModeSymlink && i.Sum == emptySum {
		return Image{}, nil, fmt.Errorf("it is a symlink with the hash of empty content, and no symlink's target is empty")
	}
	return i, b[hexLen:], nil
}

// emptySum is the SHA-256 of empty content.
var emptySum = sha256.Sum256(nil)

// checkMode refuses a mode no image can have, naming what it is when git
// would write it: a directory or a submodule is no entry of a delta.
func checkMode(mode string) error {
	switch mode {
	case ModeFile, ModeExec, ModeSymlink:
		return nil
	case "40000", "040000":
		return fmt.Errorf("its mode %s is a directory's, which is no entry", mode)
	case "160000":
		return fmt.Errorf("its mode %s is a submodule's, which valley-delta-v1 does not support", mode)
	case "":
		return fmt.Errorf("it has no mode")
	}
	return fmt.Errorf("its mode %q is not %s, %s or %s", mode, ModeFile, ModeExec, ModeSymlink)
}

func lowerHex(b []byte) bool {
	for _, c := range b {
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

// checkPath refuses a path that no tree a checkout can hold on
// case-sensitive Linux could give: an empty path, an empty component (a
// leading, trailing or doubled slash), a component that is . or .., or
// .git in any case, which git keeps for itself, a component longer than
// maxName, and a NUL, which a tree entry's name cannot hold.
func checkPath(p string) error {
	if p == "" {
		return fmt.Errorf("a path is empty")
	}
	if strings.IndexByte(p, 0) >= 0 {
		return fmt.Errorf("the path %q holds a NUL", p)
	}
	for _, name := range strings.Split(p, "/") {
		if err := checkName(name); err != nil {
			return fmt.Errorf("the path %q %w", p, err)
		}
	}
	return nil
}

// maxName is the longest name a file may have on Linux: NAME_MAX, 255
// bytes.
const maxName = 255

// checkName refuses one component of a path, as checkPath describes.
func checkName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("has an empty component")
	case name == "." || name == "..":
		return fmt.Errorf("has the component %s", name)
	case strings.EqualFold(name, ".git"):
		return fmt.Errorf("has the component %s, which git keeps for itself", name)
	case strings.IndexByte(name, '/') >= 0:
		return fmt.Errorf("has a component holding a slash")
	case len(name) > maxName:
		return fmt.Errorf("has a component of %d bytes, longer than Linux's limit of %d", len(name), maxName)
	}
	return nil
}

// checkSides refuses a delta that names, on one side, a path that is an
// entry and a directory at once: an image at a and another at a/b. No
// tree holds both, so no pair of trees gives that delta.
func (d Delta) checkSides() error {
	pre, post := map[string]bool{}, map[string]bool{}
	for _, c := range d {
		if !c.Pre.Absent() {
			pre[c.Path] = true
		}
		if !c.Post.Absent() {
			post[c.Path] = true
		}
	}
	for _, c := range d {
		for i := strings.IndexByte(c.Path, '/'); i >= 0; i = nextSlash(c.Path, i) {
			dir := c.Path[:i]
			if !c.Pre.Absent() && pre[dir] {
				return fmt.Errorf("the pre-images have an entry at %q and another under it at %q", dir, c.Path)
			}
			if !c.Post.Absent() && post[dir] {
				return fmt.Errorf("the post-images have an entry at %q and another under it at %q", dir, c.Path)
			}
		}
	}
	return nil
}

// nextSlash is the index of the next slash in p after i, or -1.
func nextSlash(p string, i int) int {
	j := strings.IndexByte(p[i+1:], '/')
	if j < 0 {
		return -1
	}
	return i + 1 + j
}
