package sigverify

import (
	"bytes"
	"errors"
	"fmt"
)

// GitNamespace is the namespace git signs and verifies objects in.
const GitNamespace = "git"

// signatureHeaders are the lines that open a signature in a git object, for
// every signature format git knows (gpg-interface.c). git splits an object
// at the last line that opens with any of them, whatever format it is
// configured to use, so this does too.
var signatureHeaders = [][]byte{
	[]byte("-----BEGIN PGP SIGNATURE-----"),
	[]byte("-----BEGIN PGP MESSAGE-----"),
	[]byte("-----BEGIN SIGNED MESSAGE-----"),
	[]byte(armorBegin),
}

// SplitTag separates a raw git tag object into the payload its signature
// covers and the signature, the way git's parse_signature does. The
// signature starts at the last line that opens with a signature header and
// runs to the end of the object. The payload is everything before it, less
// any gpgsig or gpgsig-sha256 header, which carries a signature made for
// the repository's other hash algorithm. signature is nil when the object
// is unsigned.
func SplitTag(object []byte) (payload, signature []byte) {
	match := len(object)
	for start := 0; start < len(object); {
		line := object[start:]
		for _, h := range signatureHeaders {
			if bytes.HasPrefix(line, h) {
				match = start
			}
		}
		next := bytes.IndexByte(line, '\n')
		if next < 0 {
			break
		}
		start += next + 1
	}
	if match == len(object) {
		return object, nil
	}
	return removeSignatureHeaders(object[:match]), object[match:]
}

// removeSignatureHeaders drops gpgsig and gpgsig-sha256 headers, and
// their space-indented continuation lines, from the header block of a git
// object, as git's remove_signature does.
func removeSignatureHeaders(object []byte) []byte {
	var out []byte
	inSignature := false
	for start := 0; start < len(object); {
		end := bytes.IndexByte(object[start:], '\n')
		if end < 0 {
			end = len(object)
		} else {
			end += start + 1
		}
		line := object[start:end]
		switch {
		case inSignature && bytes.HasPrefix(line, []byte(" ")):
		case bytes.HasPrefix(line, []byte("gpgsig ")) || bytes.HasPrefix(line, []byte("gpgsig-sha256 ")):
			inSignature = true
		case bytes.Equal(line, []byte("\n")):
			// The header block ends here, and the message is kept whole.
			return append(out, object[start:]...)
		default:
			inSignature = false
			out = append(out, line...)
		}
		start = end
	}
	return out
}

// Tag is the header of a verified git tag object.
type Tag struct {
	Name   string // the name the tag was signed with
	Commit string // the id of the commit it points at
}

// VerifyTag verifies the SSH signature on a raw git tag object, as
// `git verify-tag` would, and then checks what the signature covers:
//
//   - The tag was signed with the name it is being verified under. A
//     signature covers the tag's name, but a ref can point at any tag
//     object. Without this check, a signed tag for an old release could be
//     republished under a new release's name.
//   - The tag points at a commit. A signed tag may point at another tag, a
//     tree or a blob, and a caller that applies "the verified commit"
//     must get a commit.
//
// The header is read only from the signed payload, and only after the
// signature verifies. The Tag is returned only on success: nothing about
// a refused tag's target is reported, because nothing about it is
// authenticated.
//
// VerifyTag checks the type the tag declares. That the object exists in a
// repository, and has that type, is the caller's to check, with git
// replacement refs disabled.
//
// The namespace is always "git", whatever opts says. The verify time is
// opts.Time, or now, and never the tagger date. git verifies at the tagger
// date, but the signer chooses that date, so a key past its valid-before
// could backdate a tag.
func VerifyTag(signers *AllowedSigners, object []byte, name string, opts Options) (*Result, *Tag, error) {
	if name == "" {
		return nil, nil, errors.New("sigverify: no tag name given")
	}
	payload, signature := SplitTag(object)
	if signature == nil {
		return nil, nil, &Refusal{Reason: Unsigned, Detail: "the tag object carries no signature"}
	}
	opts.Namespace = GitNamespace
	result, err := Verify(signers, payload, signature, opts)
	if err != nil {
		return nil, nil, err
	}
	refuse := func(reason Reason, format string, args ...any) (*Result, *Tag, error) {
		return nil, nil, &Refusal{Reason: reason, Detail: fmt.Sprintf(format, args...), Signer: &result.Signer}
	}
	header, err := parseTagHeader(payload)
	if err != nil {
		return refuse(Malformed, "the signed tag object %s", err)
	}
	if header.name != name {
		return refuse(TagNameMismatch, "the tag was signed as %q and is being verified as %q", header.name, name)
	}
	if header.objectType != "commit" {
		return refuse(TargetNotCommit, "the tag points at a %s, not a commit", header.objectType)
	}
	return result, &Tag{Name: header.name, Commit: header.object}, nil
}

type tagHeader struct {
	object, objectType, name string
}

// parseTagHeader reads the object, type and tag lines that open a tag
// object, in the order git writes and requires them.
func parseTagHeader(payload []byte) (*tagHeader, error) {
	header, _, _ := bytes.Cut(payload, []byte("\n\n"))
	lines := bytes.Split(header, []byte("\n"))
	field := func(i int, name string) (string, error) {
		if i >= len(lines) || !bytes.HasPrefix(lines[i], []byte(name+" ")) {
			return "", fmt.Errorf("has no %q header where git puts one", name)
		}
		return string(lines[i][len(name)+1:]), nil
	}
	h := &tagHeader{}
	var err error
	if h.object, err = field(0, "object"); err != nil {
		return nil, err
	}
	if !isObjectID(h.object) {
		return nil, errors.New("points at something that is not a lowercase hex object id")
	}
	if h.objectType, err = field(1, "type"); err != nil {
		return nil, err
	}
	if h.name, err = field(2, "tag"); err != nil {
		return nil, err
	}
	return h, nil
}

// isObjectID reports whether id is a SHA-1 or SHA-256 object id in the
// lowercase hex git writes.
func isObjectID(id string) bool {
	if len(id) != 40 && len(id) != 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
