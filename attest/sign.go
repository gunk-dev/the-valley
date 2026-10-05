package main

// The envelope: a signed note.
//
// A note is the statement text, then a blank line, then one or more
// signature lines. Each signature line is an em-dash, a space, the
// signer's name, a space, and base64 of a four-byte key hash followed by
// the raw Ed25519 signature over the text:
//
//	the-valley/attestation/v1
//	subject.primary valley-tree-v1
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
// first line, which is the statement type.
//
// # One reading of the envelope
//
// The envelope is read and written by the note module (../note), not by
// this program alone. attest signs with it and attest verify opens with
// it, and so does the pre-receive hook a valley host checks pushed
// attestations with (valleyhook/). One reading means a note the hook
// accepts into the create-only namespace is a note every verifier opens,
// and a note this program writes is one the hook accepts.
//
// golang.org/x/mod/sumdb/note is the reference implementation of the
// format, and the note module is that format written out again. The
// format is frozen — it is what sum.golang.org publishes and every
// checkpoint verifier reads — and gunk-dev holds that the size of a
// dependency graph is itself the exposure, so the valley's programs depend
// on nothing outside the standard library and their own repository.
// Interoperability is bought by pinning bytes rather than by sharing code:
// attest/conformance/ holds notes that must verify, and the unit tests
// re-derive the published key hash of sum.golang.org from its name and its
// public key.

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"

	"the-valley/note"
)

// algEd25519 is the algorithm identifier the note format prefixes a public
// key with. Ed25519 is the only algorithm this program signs or verifies
// with.
const algEd25519 = note.AlgEd25519

// signer is a key this host signs with: a name, the private key, and the
// key hash the two together produce.
type signer struct {
	name string
	priv ed25519.PrivateKey
	hash uint32
}

// verifierKey is a key a verifier is willing to accept a signature from.
// It is written as one line — the name, the key hash in hex, and base64 of
// the algorithm byte and the public key, joined by plus signs.
//
// This is the whole of what a verifier is given. There is no allowed
// signers file, no certificate and no directory lookup: `attest verify`
// takes a file of these lines, and a signature by a key not among them is
// a signature by nobody it accepts.
type verifierKey = note.VerifierKey

func keyHash(name string, pub ed25519.PublicKey) uint32 { return note.KeyHash(name, pub) }

func checkSignerName(name string) error { return note.CheckName(name) }

func checkNoteText(text []byte) error { return note.CheckText(text) }

func (s signer) verifierKey() verifierKey {
	return note.NewVerifierKey(s.name, s.priv.Public().(ed25519.PublicKey))
}

func parseVerifierKey(line string) (verifierKey, error) { return note.ParseVerifierKey(line) }

// readVerifierKeys reads a known-keys file: one verifier key per line,
// with blank lines and # comments ignored.
func readVerifierKeys(path string) ([]verifierKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys, err := note.ParseVerifierKeys(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s lists no verifier keys", path)
	}
	return keys, nil
}

// signNote writes a note: the text, a blank line, and one signature line.
func signNote(text []byte, s signer) ([]byte, error) {
	return note.Sign(text, s.name, s.priv)
}

// addSignature adds a signature line to a note that already carries one.
// This is how several parties attest to one subject: the second signer
// signs the same text, and its signature lands beside the first, under the
// text both of them cover.
func addSignature(n []byte, s signer) ([]byte, error) {
	return note.AddSignature(n, s.name, s.priv)
}

// noteSignature is one signature line as read back: who it says signed,
// under which key hash, and what a verifier made of it.
type noteSignature struct {
	name     string
	hash     uint32
	known    bool
	verified bool
}

// openNote checks a note against the keys a verifier holds, and returns
// the text and what became of every signature line over it. The reading
// and the rules are the note module's: a signature by a key the verifier
// holds must check out, at least one must, and a line that is not a
// well-formed Ed25519 signature line refuses the note.
func openNote(n []byte, known []verifierKey) ([]byte, []noteSignature, error) {
	text, checked, err := note.Open(n, known)
	if err != nil {
		return nil, nil, err
	}
	var found []noteSignature
	for _, c := range checked {
		found = append(found, noteSignature{name: c.Name, hash: c.Hash, known: c.Known, verified: c.Verified})
	}
	return text, found, nil
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
