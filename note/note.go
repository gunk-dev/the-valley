// Package note is the envelope a valley attestation travels in: a signed
// note, in the format golang.org/x/mod/sumdb/note defines, around statement
// text. It is the one reading of that envelope. attest writes notes with
// it, attest verify opens them with it, and the pre-receive hook checks
// pushed ones with it, so a note one of them accepts is a note all of them
// accept.
//
// A note is the text, a blank line, and one or more signature lines. Each
// signature line is an em-dash, a space, the signer's name, a space, and
// base64 of a four-byte key hash followed by the raw Ed25519 signature
// over the text:
//
//	the-valley/attestation/v1
//	subject.primary valley-tree-v1
//	…
//
//	— laddie.gunk.dev/attestations WhFo+DGb1c/Fk6…
//
// The reading is strict. A note that is not valid UTF-8, or holds an ASCII
// control character other than the newline, is refused. So is a signature
// line that is not exactly that shape, names a signer a verifier key could
// not name, or carries anything but an Ed25519 signature. A line by a
// signer the reader holds no key for is well formed and carried through as
// unknown: several parties can sign one text, and a reader need not hold
// every party's key. A note is accepted only when at least one signature
// verifies under a key the reader holds, and every signature by such a key
// does.
//
// The package depends on the standard library alone, like everything that
// uses it.
package note

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// AlgEd25519 is the algorithm byte a verifier key's public key is
	// prefixed with. Ed25519 is the only algorithm the valley signs or
	// verifies with.
	AlgEd25519 = 1

	// Mark opens a signature line: U+2014 EM DASH, then a space.
	Mark = "— "

	// StatementType is the first line of every statement's written form.
	// The signature covers the text and nothing else, so the text's own
	// first line is what says a note is an attestation.
	StatementType = "the-valley/attestation/v1"

	// signatureSize is a signature line's decoded length: the key hash
	// and the Ed25519 signature.
	signatureSize = 4 + ed25519.SignatureSize
)

// KeyHash is the note format's identifier for a name-and-key pair: SHA-256
// over the name, a newline, the algorithm byte and the public key,
// truncated to its first four bytes. The name is inside the hash and
// outside the signature, so the same key published under two names is two
// verifier keys.
func KeyHash(name string, pub ed25519.PublicKey) uint32 {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte("\n"))
	h.Write([]byte{AlgEd25519})
	h.Write(pub)
	return binary.BigEndian.Uint32(h.Sum(nil))
}

// CheckName holds a signer's name to what a signature line and a verifier
// key can carry: valid UTF-8, no space, because a space separates the name
// from the signature, no control character, and no plus, because a plus
// separates the fields of a verifier key.
func CheckName(name string) error {
	switch {
	case name == "":
		return errors.New("a signer name may not be empty")
	case !utf8.ValidString(name):
		return fmt.Errorf("signer name %q is not valid utf-8", name)
	case strings.ContainsFunc(name, unicode.IsSpace):
		return fmt.Errorf("signer name %q holds a space, which separates the name from the signature", name)
	case strings.ContainsFunc(name, unicode.IsControl):
		return fmt.Errorf("signer name %q holds a control character", name)
	case strings.Contains(name, "+"):
		return fmt.Errorf("signer name %q holds a plus, which separates the fields of a verifier key", name)
	}
	return nil
}

// CheckBytes holds a note, or the text inside one, to valid UTF-8 with no
// ASCII control character but the newline.
func CheckBytes(b []byte) error {
	if !utf8.Valid(b) {
		return errors.New("a note is valid utf-8, and this is not")
	}
	for _, c := range b {
		if c < 0x20 && c != '\n' || c == 0x7f {
			return fmt.Errorf("a note holds no ascii control character but the newline, and this holds %#02x", c)
		}
	}
	return nil
}

// CheckText holds text about to be signed, or read out of a note, to what
// the format allows: CheckBytes, and a closing newline.
func CheckText(text []byte) error {
	if len(text) == 0 || text[len(text)-1] != '\n' {
		return errors.New("a note's text must end with a newline")
	}
	return CheckBytes(text)
}

// VerifierKey is a key a reader accepts signatures from: a name, the key
// hash the name and key produce, and the public key.
type VerifierKey struct {
	Name string
	Hash uint32
	Pub  ed25519.PublicKey
}

// NewVerifierKey pairs a name with a public key.
func NewVerifierKey(name string, pub ed25519.PublicKey) VerifierKey {
	return VerifierKey{Name: name, Hash: KeyHash(name, pub), Pub: pub}
}

// String is the verifier key's line: name+hash+base64 of the algorithm
// byte and the public key.
func (v VerifierKey) String() string {
	return fmt.Sprintf("%s+%08x+%s", v.Name, v.Hash,
		base64.StdEncoding.EncodeToString(append([]byte{AlgEd25519}, v.Pub...)))
}

// ParseVerifierKey reads one verifier key line. The key hash it carries is
// checked against the one its name and public key produce, so a line whose
// hash was edited is refused rather than quietly matching nothing.
func ParseVerifierKey(line string) (VerifierKey, error) {
	// Two separators and no more: the base64 that ends the line may hold
	// a plus of its own.
	parts := strings.SplitN(strings.TrimSpace(line), "+", 3)
	if len(parts) != 3 {
		return VerifierKey{}, fmt.Errorf("%q is not a verifier key: one is name+hash+base64", line)
	}
	name, written, encoded := parts[0], parts[1], parts[2]
	if err := CheckName(name); err != nil {
		return VerifierKey{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return VerifierKey{}, fmt.Errorf("verifier key %s: %w", name, err)
	}
	if len(raw) != 1+ed25519.PublicKeySize || raw[0] != AlgEd25519 {
		return VerifierKey{}, fmt.Errorf("verifier key %s is not an ed25519 key", name)
	}
	v := NewVerifierKey(name, raw[1:])
	if fmt.Sprintf("%08x", v.Hash) != written {
		return VerifierKey{}, fmt.Errorf("verifier key %s carries hash %s, but its name and key produce %08x", name, written, v.Hash)
	}
	return v, nil
}

// ParseVerifierKeys reads a known-keys file's contents: one verifier key
// per line, with blank lines and # comments skipped. A line that is not a
// verifier key fails the whole read, naming the line.
func ParseVerifierKeys(data []byte) ([]VerifierKey, error) {
	var keys []VerifierKey
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := ParseVerifierKey(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		keys = append(keys, v)
	}
	return keys, nil
}

// Signature is one signature line as read: who it says signed, under which
// key hash, the signature itself, and the line as written.
type Signature struct {
	Name string
	Hash uint32
	Sig  []byte
	Line string
}

// Split separates a note into its text and its signature lines, and holds
// both to the format. The last blank line is the separator, so text holding
// a blank line of its own does not move the split. Every line after it must
// be a signature line, every signature line must carry an Ed25519
// signature, and no signer may sign twice under one key hash.
func Split(note []byte) (text []byte, sigs []Signature, err error) {
	if err := CheckBytes(note); err != nil {
		return nil, nil, err
	}
	split := bytes.LastIndex(note, []byte("\n\n"))
	if split < 0 {
		return nil, nil, errors.New("this is not a note: no blank line separates the text from its signatures")
	}
	text, block := note[:split+1], note[split+2:]
	if err := CheckText(text); err != nil {
		return nil, nil, err
	}
	if len(block) == 0 || block[len(block)-1] != '\n' {
		return nil, nil, errors.New("this is not a note: the signature block is empty or does not end with a newline")
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(block), "\n"), "\n") {
		sig, err := parseSignature(line)
		if err != nil {
			return nil, nil, err
		}
		id := fmt.Sprintf("%s+%08x", sig.Name, sig.Hash)
		if seen[id] {
			return nil, nil, fmt.Errorf("the note carries two signatures by %s", id)
		}
		seen[id] = true
		sigs = append(sigs, sig)
	}
	return text, sigs, nil
}

func parseSignature(line string) (Signature, error) {
	rest, ok := strings.CutPrefix(line, Mark)
	if !ok {
		return Signature{}, fmt.Errorf("%q is not a signature line: one opens with an em-dash and a space", line)
	}
	name, encoded, ok := strings.Cut(rest, " ")
	if !ok {
		return Signature{}, fmt.Errorf("the signature line %q carries no signature", line)
	}
	if err := CheckName(name); err != nil {
		return Signature{}, fmt.Errorf("the signature line %q: %w", line, err)
	}
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Signature{}, fmt.Errorf("the signature line for %q does not decode", name)
	}
	if len(blob) != signatureSize {
		return Signature{}, fmt.Errorf("the signature line for %q is not an Ed25519 signature: it holds %d bytes, not %d", name, len(blob), signatureSize)
	}
	return Signature{Name: name, Hash: binary.BigEndian.Uint32(blob[:4]), Sig: blob[4:], Line: line}, nil
}

// SignatureLine is the signature line a key adds under text, with its
// closing newline.
func SignatureLine(text []byte, name string, priv ed25519.PrivateKey) string {
	var hb [4]byte
	binary.BigEndian.PutUint32(hb[:], KeyHash(name, priv.Public().(ed25519.PublicKey)))
	blob := append(hb[:], ed25519.Sign(priv, text)...)
	return Mark + name + " " + base64.StdEncoding.EncodeToString(blob) + "\n"
}

// Sign writes a note: the text, a blank line, and one signature line. What
// it writes is held to what Split reads, so a note this package signs is
// one it opens.
func Sign(text []byte, name string, priv ed25519.PrivateKey) ([]byte, error) {
	if err := CheckText(text); err != nil {
		return nil, err
	}
	if err := CheckName(name); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(text)
	b.WriteByte('\n')
	b.WriteString(SignatureLine(text, name, priv))
	return b.Bytes(), nil
}

// AddSignature adds a signature line to a note that already carries one:
// a second party signing the same text, beside the first. Signing again
// with a key that already signed leaves the note as it was.
func AddSignature(note []byte, name string, priv ed25519.PrivateKey) ([]byte, error) {
	if err := CheckName(name); err != nil {
		return nil, err
	}
	text, sigs, err := Split(note)
	if err != nil {
		return nil, err
	}
	line := SignatureLine(text, name, priv)
	hash := KeyHash(name, priv.Public().(ed25519.PublicKey))
	for _, s := range sigs {
		if s.Name == name && s.Hash == hash {
			return note, nil
		}
	}
	return append(append([]byte{}, note...), line...), nil
}

// Checked is a signature line and what a reader made of it: whether the
// reader holds the key it names, and whether it verified.
type Checked struct {
	Signature
	Known    bool
	Verified bool
}

// Open reads a note and checks its signatures against the keys a reader
// holds. A signature by a key the reader holds must verify, or the whole
// note is refused: one good signature standing beside a forgery is not a
// note anybody should read past. At least one signature must verify.
func Open(note []byte, known []VerifierKey) (text []byte, checked []Checked, err error) {
	text, sigs, err := Split(note)
	if err != nil {
		return nil, nil, err
	}
	verified := 0
	for _, s := range sigs {
		c := Checked{Signature: s}
		for _, k := range known {
			if k.Name != s.Name || k.Hash != s.Hash {
				continue
			}
			c.Known = true
			if !ed25519.Verify(k.Pub, text, s.Sig) {
				return nil, nil, fmt.Errorf("the signature by %s+%08x does not check out over this text", s.Name, s.Hash)
			}
			c.Verified = true
			verified++
			break
		}
		checked = append(checked, c)
	}
	if verified == 0 {
		return nil, nil, errors.New("no signature on this note is by a key the verifier holds")
	}
	return text, checked, nil
}

// Subject is the primary member of a statement's subject digest set: the
// scheme the statement names as primary, and the digest under it.
type Subject struct {
	Scheme string
	Digest string
}

// StatementSubject reads the subject a statement's text names. The text
// must open with the statement type, and every line after it must be a
// key, one space, and a value, with no key twice. The primary digest is
// the subject.digest.<scheme> line for the scheme subject.primary names.
//
// This is the part of a statement every reader needs before anything else
// — which tree it is about. attest reads the rest of the text, and vets it
// against the attestation schema.
func StatementSubject(text []byte) (Subject, error) {
	if err := CheckText(text); err != nil {
		return Subject{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if lines[0] != StatementType {
		return Subject{}, fmt.Errorf("is not a statement: its text opens with %q, not %q", lines[0], StatementType)
	}
	fields := map[string]string{}
	for i, line := range lines[1:] {
		key, value, ok := strings.Cut(line, " ")
		if !ok || key == "" || value == "" {
			return Subject{}, fmt.Errorf("line %d is %q; a line is a key, one space, and a value", i+2, line)
		}
		if _, twice := fields[key]; twice {
			return Subject{}, fmt.Errorf("line %d sets %s a second time", i+2, key)
		}
		fields[key] = value
	}
	scheme := fields["subject.primary"]
	if scheme == "" {
		return Subject{}, errors.New("names no primary subject digest")
	}
	digest := fields["subject.digest."+scheme]
	if digest == "" {
		return Subject{}, fmt.Errorf("names %s as its primary digest and carries no value for it", scheme)
	}
	return Subject{Scheme: scheme, Digest: digest}, nil
}
