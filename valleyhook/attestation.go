package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"

	"the-valley/note"
)

// An attestation ref is named for what it holds: the digest of the tree
// its notes are about, then the key hash of the signer it is filed under
// (archive/design/contribute.md, step 5). attest writes the digest as 64 lowercase
// hex digits and the key hash as 8.
var attestationName = regexp.MustCompile(`^refs/the-valley/attestations/([0-9a-f]{64})/([0-9a-f]{8})$`)

// checkAttestation decides whether a push may create the attestation ref
// ref pointing at id. The namespace is create-only, so whoever creates a
// name first holds it for good, and a name that anyone could take would
// let one pusher keep another signer's evidence out. So a ref must be what
// its name says before it may exist:
//
//   - its name is exactly <digest>/<key hash>. A ref named for the digest
//     alone would stop git creating any ref beneath it.
//   - it points at a tree of notes, read within fixed bounds (repo.go).
//   - every note opens under the keys this host accepts evidence from,
//     the way any verifier opens it (the note module): the envelope is
//     well formed, every signature by an accepted key verifies, and at
//     least one does.
//   - every note is a statement about the digest the name gives, and
//     carries a verified signature under the key hash the name gives.
//
// A host that names no keys accepts no attestation: a note nobody here
// could verify is not one this namespace can hold.
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
	if len(p.verifiers) == 0 {
		return errors.New("this host names no keys it accepts evidence from, so no attestation can be checked here")
	}

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

func (p policy) checkNote(n []byte, digest, keyHash string) error {
	text, checked, err := note.Open(n, p.verifiers)
	if err != nil {
		return fmt.Errorf("does not open: %v", err)
	}
	subject, err := note.StatementSubject(text)
	if err != nil {
		return err
	}
	if subject.Digest != digest {
		return fmt.Errorf("is about the tree %s, not the one the ref is named for", subject.Digest)
	}
	for _, c := range checked {
		if c.Verified && fmt.Sprintf("%08x", c.Hash) == keyHash {
			return nil
		}
	}
	return fmt.Errorf("carries no signature under the key hash %s by a key this host accepts", keyHash)
}

// readVerifiers reads the verifier keys in one file. A file that does not
// exist names none: the compiled file is missing until the first
// compilation, and then no attestation is accepted, which is the
// direction a missing key has to default in. A line that is not a
// verifier key fails the read.
func readVerifiers(path string) ([]note.VerifierKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	keys, err := note.ParseVerifierKeys(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return keys, nil
}
