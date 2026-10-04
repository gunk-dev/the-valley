package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// An attestation ref is named for what it holds: the digest of the tree
// its notes are about, then the key hash of the signer it is filed under
// (design/contribute.md, step 5). attest writes the digest as 64 lowercase
// hex digits and the key hash as 8.
var attestationName = regexp.MustCompile(`^refs/the-valley/attestations/([0-9a-f]{64})/([0-9a-f]{8})$`)

// statementType is the first line of every statement's written form
// (attest/statement.go).
const statementType = "the-valley/attestation/v1"

// signatureMark opens a signature line in the note format: U+2014 EM DASH,
// then a space (attest/sign.go).
const signatureMark = "— "

// checkAttestation decides whether a push may create the attestation ref
// ref pointing at id. The namespace is create-only, so whoever creates a
// name first holds it for good, and a name that anyone could take would
// let one pusher keep another signer's evidence out. So a ref must be what
// its name says before it may exist:
//
//   - its name is exactly <digest>/<key hash>. A ref named for the digest
//     alone would stop git creating any ref beneath it.
//   - it points at a tree of notes, and every note is a statement about
//     the digest the name gives.
//   - every note carries a signature under the key hash the name gives.
//     When the host names the keys it accepts, that signature has to
//     verify under one of them, and every signature by a key it names has
//     to verify too.
//
// The pusher is not compared with the signer. Evidence is routinely
// relayed: the host's own key signs what `attest run` observes there, and
// the operator's key pushes it.
func (p policy) checkAttestation(ref, id string) error {
	parts := attestationName.FindStringSubmatch(ref)
	if parts == nil {
		return errors.New("an attestation ref is named refs/the-valley/attestations/<digest>/<key hash>, with the tree digest as 64 lowercase hex digits and the signer's key hash as 8")
	}
	digest, keyHash := parts[1], parts[2]

	notes, err := p.repo.notes(id)
	if err != nil {
		return err
	}
	for _, path := range sortedKeys(notes) {
		if err := p.checkNote(notes[path], digest, keyHash); err != nil {
			return fmt.Errorf("the note at %s %s", path, err)
		}
	}
	return nil
}

func (p policy) checkNote(note []byte, digest, keyHash string) error {
	text, lines, err := splitNote(note)
	if err != nil {
		return err
	}
	if about, err := subjectDigest(text); err != nil {
		return err
	} else if about != digest {
		return fmt.Errorf("is about the tree %s, not the one the ref is named for", about)
	}

	filed := false
	for _, line := range lines {
		name, encoded, ok := strings.Cut(strings.TrimPrefix(line, signatureMark), " ")
		blob, err := base64.StdEncoding.DecodeString(encoded)
		if !ok || err != nil || len(blob) < 5 {
			return fmt.Errorf("carries a signature line that does not decode: %q", line)
		}
		hash := binary.BigEndian.Uint32(blob[:4])
		claimed := fmt.Sprintf("%08x", hash) == keyHash

		if !p.verify {
			filed = filed || claimed
			continue
		}
		key, known := p.verifier(name, hash)
		if !known {
			continue
		}
		if len(blob[4:]) != ed25519.SignatureSize || !ed25519.Verify(key.pub, text, blob[4:]) {
			return fmt.Errorf("carries a signature by %s+%08x that does not check out", name, hash)
		}
		filed = filed || claimed
	}
	switch {
	case filed:
		return nil
	case !p.verify:
		return fmt.Errorf("carries no signature under the key hash %s the ref is named for", keyHash)
	default:
		return fmt.Errorf("carries no signature under the key hash %s by a key this host accepts", keyHash)
	}
}

func (p policy) verifier(name string, hash uint32) (verifierKey, bool) {
	for _, k := range p.verifiers {
		if k.name == name && k.hash == hash {
			return k, true
		}
	}
	return verifierKey{}, false
}

// splitNote separates a note's text from its signature lines, the way
// attest does: the last blank line is the separator, and every line after
// it is a signature line.
func splitNote(note []byte) (text []byte, sigs []string, err error) {
	if !utf8.Valid(note) {
		return nil, nil, errors.New("is not UTF-8")
	}
	split := bytes.LastIndex(note, []byte("\n\n"))
	if split < 0 {
		return nil, nil, errors.New("is not a note: no blank line separates the text from its signatures")
	}
	text, block := note[:split+1], note[split+2:]
	if len(block) == 0 || block[len(block)-1] != '\n' {
		return nil, nil, errors.New("is not a note: the signature block is empty or does not end with a newline")
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(block), "\n"), "\n") {
		if !strings.HasPrefix(line, signatureMark) {
			return nil, nil, fmt.Errorf("is not a note: %q is not a signature line", line)
		}
		sigs = append(sigs, line)
	}
	return text, sigs, nil
}

// subjectDigest reads the primary digest a statement's text names: the
// subject.primary line names the scheme, and the subject.digest.<scheme>
// line carries the digest (attest/text.go).
func subjectDigest(text []byte) (string, error) {
	lines := strings.Split(strings.TrimSuffix(string(text), "\n"), "\n")
	if lines[0] != statementType {
		return "", fmt.Errorf("is not a statement: its text opens with %q, not %q", lines[0], statementType)
	}
	fields := map[string]string{}
	for _, line := range lines[1:] {
		key, value, _ := strings.Cut(line, " ")
		fields[key] = value
	}
	scheme := fields["subject.primary"]
	if scheme == "" {
		return "", errors.New("names no primary subject digest")
	}
	digest := fields["subject.digest."+scheme]
	if digest == "" {
		return "", fmt.Errorf("names %s as its primary digest and carries no value for it", scheme)
	}
	return digest, nil
}

// verifierKey is a key whose signatures the host accepts, in the note
// format's verifier key form: name+hash+base64 of the algorithm byte and
// the public key (attest/sign.go).
type verifierKey struct {
	name string
	hash uint32
	pub  ed25519.PublicKey
}

const algEd25519 = 1

// readVerifiers adds the verifier keys in one file. A file that does not
// exist adds none: the compiled file is missing until the first
// compilation, and then no attestation verifies, which is the direction a
// missing key has to default in. A line that is not a verifier key, or
// whose hash is not the one its name and key produce, fails the read.
func readVerifiers(path string) ([]verifierKey, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var keys []verifierKey
	lines := bufio.NewScanner(f)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := parseVerifierKey(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		keys = append(keys, k)
	}
	return keys, lines.Err()
}

func parseVerifierKey(line string) (verifierKey, error) {
	// Two separators and no more: the base64 that ends the line may hold
	// a plus of its own.
	parts := strings.SplitN(line, "+", 3)
	if len(parts) != 3 {
		return verifierKey{}, fmt.Errorf("%q is not a verifier key: one is name+hash+base64", line)
	}
	raw, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(raw) != 1+ed25519.PublicKeySize || raw[0] != algEd25519 {
		return verifierKey{}, fmt.Errorf("%q is not an ed25519 verifier key", line)
	}
	k := verifierKey{name: parts[0], pub: raw[1:]}
	h := sha256.New()
	h.Write([]byte(k.name + "\n"))
	h.Write(raw)
	k.hash = binary.BigEndian.Uint32(h.Sum(nil))
	if fmt.Sprintf("%08x", k.hash) != parts[1] {
		return verifierKey{}, fmt.Errorf("%q carries hash %s, and its name and key produce %08x", line, parts[1], k.hash)
	}
	return k, nil
}
