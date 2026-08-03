package main

// The envelope: a signed note.
//
// A note is the statement text, then a blank line, then one or more
// signature lines. Each signature line is an em-dash, a space, the
// signer's name, a space, and base64 of a four-byte key hash followed by
// the raw Ed25519 signature over the text:
//
//	valley-statement-v1
//	predicate.result passed
//	…
//
//	— laddie.gunk.dev/attestations WhFo+DGb1c/Fk6…
//	— witness.gunk.dev/attestations Ro+Yc3q9nSs8W…
//
// This is the format golang.org/x/mod/sumdb/note defines and transparency
// log checkpoints are published in.
//
// It is chosen for what it does to composition. Several parties attesting
// to one subject become sibling signature lines under one text, so a
// signature cannot be lifted onto a different statement: it sits under the
// text it covers, and moving it means moving the text with it. That
// property is structural here rather than a rule a verifier has to
// remember to apply. It is also the envelope a Phase 6 checkpoint arrives
// in, so the same envelope carries an attestation now and a log checkpoint
// later rather than changing at the boundary.
//
// A signature covers the text and nothing else — not the signer's name,
// not a namespace, and no git object (ida-51605e8). What separates an
// attestation from any other note a key signs is therefore the text's own
// first line, which is why statement text opens by naming its form.
//
// # On not taking the dependency
//
// golang.org/x/mod/sumdb/note is the reference implementation of this
// format, and this file is that format written out again. The trade was
// weighed as follows.
//
// The format is frozen. It is what sum.golang.org publishes and what every
// checkpoint verifier already reads, so it cannot drift out from under a
// second implementation the way a live API would. Writing it out is
// roughly a hundred lines over crypto/ed25519, all of it visible here.
//
// Against that, gunk-dev holds that the size of a dependency graph is
// itself the exposure. This program depends on nothing outside the
// standard library today: it builds with vendorHash = null, it fetches
// nothing, and the whole of what it runs is in this directory. One module
// would end that property for code this short.
//
// Interoperability is bought by pinning bytes rather than by sharing code.
// attest/conformance/ holds notes that must verify, and the unit tests
// re-derive the published key hash of sum.golang.org from its name and its
// public key — an answer the reference implementation computed, which this
// implementation has to match exactly or the vectors are worthless.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// algEd25519 is the algorithm identifier the note format prefixes a public
// key with. Ed25519 is the only algorithm this program signs or verifies
// with; a signature under any other is reported and left unchecked.
const algEd25519 = 1

// signatureMark opens a signature line. U+2014 EM DASH, then a space.
const signatureMark = "— "

// signer is a key this host signs with: a name, the private key, and the
// key hash the two together produce.
type signer struct {
	name string
	priv ed25519.PrivateKey
	hash uint32
}

// verifierKey is a key a verifier is willing to accept a signature from.
// It is written as one line — the name, the key hash in hex, and base64 of
// the algorithm byte and the public key, joined by plus signs:
//
//	laddie.gunk.dev/attestations+1f9c40b2+AbUmzL2t…
//
// This is the whole of what a verifier is given. There is no allowed
// signers file, no certificate and no directory lookup: `attest verify`
// takes a file of these lines, and a signature by a key not among them is
// a signature by nobody it accepts.
type verifierKey struct {
	name string
	hash uint32
	pub  ed25519.PublicKey
}

// keyHash is the note format's identifier for a name-and-key pair:
// SHA-256 over the name, a newline, the algorithm byte and the public key,
// truncated to its first four bytes.
//
// The name is inside the hash and outside the signature. So the same
// public key published under two names is two verifier keys and cannot be
// confused, while the signature itself commits only to the text — which is
// what makes the text's first line, rather than the signer's name, the
// thing that separates one kind of note from another.
func keyHash(name string, pub ed25519.PublicKey) uint32 {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte("\n"))
	h.Write([]byte{algEd25519})
	h.Write(pub)
	return binary.BigEndian.Uint32(h.Sum(nil))
}

// checkSignerName holds a name to what a signature line can carry: no
// space, because a space separates the name from the base64 that follows,
// and no plus, because a plus separates the fields of a verifier key.
func checkSignerName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a signer name may not be empty")
	case !utf8.ValidString(name):
		return fmt.Errorf("signer name %q is not valid utf-8", name)
	case strings.ContainsFunc(name, unicode.IsSpace):
		return fmt.Errorf("signer name %q holds a space, which separates the name from the signature", name)
	case strings.Contains(name, "+"):
		return fmt.Errorf("signer name %q holds a plus, which separates the fields of a verifier key", name)
	}
	return nil
}

func (v verifierKey) String() string {
	return fmt.Sprintf("%s+%08x+%s", v.name, v.hash,
		base64.StdEncoding.EncodeToString(append([]byte{algEd25519}, v.pub...)))
}

func (s signer) verifierKey() verifierKey {
	return verifierKey{name: s.name, hash: s.hash, pub: s.priv.Public().(ed25519.PublicKey)}
}

// parseVerifierKey reads one verifier key line. The key hash it carries is
// checked against the one its name and public key produce, so a line whose
// hash was edited is refused here rather than quietly matching nothing at
// verification time.
func parseVerifierKey(line string) (verifierKey, error) {
	malformed := fmt.Errorf("%q is not a verifier key: one is name+hash+base64", line)
	name, rest, found := strings.Cut(strings.TrimSpace(line), "+")
	if !found {
		return verifierKey{}, malformed
	}
	written, encoded, found := strings.Cut(rest, "+")
	if !found {
		return verifierKey{}, malformed
	}
	if err := checkSignerName(name); err != nil {
		return verifierKey{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return verifierKey{}, fmt.Errorf("verifier key %s: %w", name, err)
	}
	if len(raw) != 1+ed25519.PublicKeySize || raw[0] != algEd25519 {
		return verifierKey{}, fmt.Errorf("verifier key %s is not an ed25519 key", name)
	}
	v := verifierKey{name: name, hash: keyHash(name, raw[1:]), pub: raw[1:]}
	if fmt.Sprintf("%08x", v.hash) != written {
		return verifierKey{}, fmt.Errorf("verifier key %s carries hash %s, but its name and key produce %08x", name, written, v.hash)
	}
	return v, nil
}

// readVerifierKeys reads a known-keys file: one verifier key per line,
// with blank lines and # comments ignored.
func readVerifierKeys(path string) ([]verifierKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys []verifierKey
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		v, err := parseVerifierKey(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		keys = append(keys, v)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s lists no verifier keys", path)
	}
	return keys, nil
}

// signNote writes a note: the text, a blank line, and one signature line.
func signNote(text []byte, s signer) ([]byte, error) {
	if err := checkNoteText(text); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(text)
	b.WriteByte('\n')
	b.WriteString(signatureLine(text, s))
	return b.Bytes(), nil
}

// addSignature adds a signature line to a note that already carries one.
// This is how several parties attest to one subject: the second signer
// signs the same text, and its signature lands beside the first, under the
// text both of them cover.
func addSignature(note []byte, s signer) ([]byte, error) {
	text, sigs, err := splitNote(note)
	if err != nil {
		return nil, err
	}
	line := signatureLine(text, s)
	for _, existing := range sigs {
		if existing == strings.TrimSuffix(line, "\n") {
			return note, nil
		}
	}
	return append(append([]byte{}, note...), line...), nil
}

func signatureLine(text []byte, s signer) string {
	var hb [4]byte
	binary.BigEndian.PutUint32(hb[:], s.hash)
	blob := append(hb[:], ed25519.Sign(s.priv, text)...)
	return signatureMark + s.name + " " + base64.StdEncoding.EncodeToString(blob) + "\n"
}

// noteSignature is one signature line as read back: who it says signed,
// under which key hash, and what a verifier made of it.
type noteSignature struct {
	name     string
	hash     uint32
	known    bool
	verified bool
}

// splitNote separates a note's text from its signature lines. The blank
// line before the signature block is the separator, and the last one wins,
// so text holding a blank line of its own does not move the split.
func splitNote(note []byte) (text []byte, sigs []string, err error) {
	if err := checkNoteBytes(note); err != nil {
		return nil, nil, err
	}
	split := bytes.LastIndex(note, []byte("\n\n"))
	if split < 0 {
		return nil, nil, fmt.Errorf("this is not a note: no blank line separates the text from its signatures")
	}
	text, block := note[:split+1], note[split+2:]
	if len(block) == 0 || block[len(block)-1] != '\n' {
		return nil, nil, fmt.Errorf("this is not a note: the signature block is empty or does not end with a newline")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(block), "\n"), "\n") {
		if !strings.HasPrefix(line, signatureMark) {
			return nil, nil, fmt.Errorf("%q is not a signature line: one opens with an em-dash and a space", line)
		}
		sigs = append(sigs, line)
	}
	return text, sigs, nil
}

// openNote checks a note against the keys a verifier holds. It returns the
// text and what became of every signature line over it.
//
// A signature line naming a key the verifier holds must check out, or the
// whole note is refused: one good signature standing beside a forgery is
// not a note anybody should read past. A line naming a key the verifier
// does not hold is neither an error nor evidence — it is reported as
// unknown and carried through.
func openNote(note []byte, known []verifierKey) ([]byte, []noteSignature, error) {
	text, lines, err := splitNote(note)
	if err != nil {
		return nil, nil, err
	}
	if err := checkNoteText(text); err != nil {
		return nil, nil, err
	}
	var found []noteSignature
	verified := 0
	for _, line := range lines {
		name, encoded, ok := strings.Cut(strings.TrimPrefix(line, signatureMark), " ")
		if !ok {
			return nil, nil, fmt.Errorf("the signature line for %q carries no signature", name)
		}
		blob, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(blob) < 5 {
			return nil, nil, fmt.Errorf("the signature line for %q does not decode", name)
		}
		sig := noteSignature{name: name, hash: binary.BigEndian.Uint32(blob[:4])}
		for _, k := range known {
			if k.name != name || k.hash != sig.hash {
				continue
			}
			sig.known = true
			if len(blob[4:]) != ed25519.SignatureSize || !ed25519.Verify(k.pub, text, blob[4:]) {
				return nil, nil, fmt.Errorf("the signature by %s+%08x does not check out over this text", name, sig.hash)
			}
			sig.verified = true
			verified++
			break
		}
		found = append(found, sig)
	}
	if verified == 0 {
		return nil, nil, fmt.Errorf("no signature on this note is by a key the verifier holds")
	}
	return text, found, nil
}

// checkNoteText holds a note's text to what the format allows: valid
// UTF-8, no ASCII control character but the newline, and a closing
// newline. Statement text satisfies all of it by construction; a caller
// signing something else is held to it here.
func checkNoteText(text []byte) error {
	if len(text) == 0 || text[len(text)-1] != '\n' {
		return fmt.Errorf("a note's text must end with a newline")
	}
	return checkNoteBytes(text)
}

func checkNoteBytes(b []byte) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("a note is valid utf-8, and this is not")
	}
	for _, c := range b {
		if c < 0x20 && c != '\n' || c == 0x7f {
			return fmt.Errorf("a note holds no ascii control character but the newline, and this holds %#02x", c)
		}
	}
	return nil
}

// loadSigner reads an unencrypted OpenSSH Ed25519 private key and pairs it
// with the name it signs under. /etc/ssh/ssh_host_ed25519_key is the
// natural production identity — the host already has it, and it already
// says which host this is — but the key is a parameter and provisioning
// one is deployment.
func loadSigner(path, name string) (signer, error) {
	if path == "" {
		return signer{}, fmt.Errorf("no signing key: pass --key. The host signs; an unsigned statement is not an attestation")
	}
	if err := checkSignerName(name); err != nil {
		return signer{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return signer{}, fmt.Errorf("signing key: %w", err)
	}
	priv, err := parseOpenSSHEd25519(raw)
	if err != nil {
		return signer{}, fmt.Errorf("%s: %w", path, err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	return signer{name: name, priv: priv, hash: keyHash(name, pub)}, nil
}

// parseOpenSSHEd25519 reads the private key format ssh-keygen writes. The
// container is
//
//	"openssh-key-v1\0"
//	string  cipher, string kdf, string kdf options
//	uint32  number of keys, string public key
//	string  the private section, which for one unencrypted key is
//	        uint32 check, uint32 check, string "ssh-ed25519",
//	        string public key, string seed and public key, string comment
//
// Only the unencrypted single-key case is read. A key behind a passphrase
// is refused rather than prompted for: attest runs unattended, and a tool
// that asked a host for a passphrase it does not have would fail later and
// less clearly.
func parseOpenSSHEd25519(raw []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		return nil, fmt.Errorf("not an openssh private key; attest signs with the ed25519 key `ssh-keygen -t ed25519` writes")
	}
	body := sshBytes{rest: block.Bytes}
	magic := []byte("openssh-key-v1\x00")
	if !bytes.HasPrefix(body.rest, magic) {
		return nil, fmt.Errorf("not an openssh private key: the container is unrecognised")
	}
	body.rest = body.rest[len(magic):]

	cipher := body.str()
	kdf := body.str()
	body.str() // the kdf options
	count := body.u32()
	body.str() // the public key, which the private section repeats
	section := sshBytes{rest: body.str()}
	if body.err != nil {
		return nil, body.err
	}
	if string(cipher) != "none" || string(kdf) != "none" {
		return nil, fmt.Errorf("the key is encrypted with %s; attest runs unattended and cannot ask for a passphrase", cipher)
	}
	if count != 1 {
		return nil, fmt.Errorf("the file holds %d keys; attest signs with one", count)
	}

	if a, b := section.u32(), section.u32(); a != b {
		return nil, fmt.Errorf("the key is encrypted or damaged: its check words differ")
	}
	if kind := section.str(); string(kind) != "ssh-ed25519" {
		return nil, fmt.Errorf("the key is %s; attest signs with ed25519, which is what a note carries", kind)
	}
	pub := section.str()
	priv := section.str()
	if section.err != nil {
		return nil, section.err
	}
	if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("the key is not the size an ed25519 key is")
	}
	key := ed25519.PrivateKey(priv)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), pub) {
		return nil, fmt.Errorf("the key's two halves do not agree: its private half does not produce its public one")
	}
	return key, nil
}

// sshBytes reads the ssh wire encoding, where a string is a four-byte
// big-endian length and that many bytes. The first error is kept and every
// later read is a no-op, so a caller checks once at the end.
type sshBytes struct {
	rest []byte
	err  error
}

func (s *sshBytes) u32() uint32 {
	if s.err != nil {
		return 0
	}
	if len(s.rest) < 4 {
		s.err = fmt.Errorf("the key ends in the middle of a field")
		return 0
	}
	v := binary.BigEndian.Uint32(s.rest)
	s.rest = s.rest[4:]
	return v
}

func (s *sshBytes) str() []byte {
	n := s.u32()
	if s.err != nil {
		return nil
	}
	if uint64(n) > uint64(len(s.rest)) {
		s.err = fmt.Errorf("the key claims a %d-byte field with %d bytes left", n, len(s.rest))
		return nil
	}
	v := s.rest[:n]
	s.rest = s.rest[n:]
	return v
}
