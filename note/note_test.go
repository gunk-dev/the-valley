package note

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
)

const text = StatementType + "\nsubject.primary valley-tree-v1\nsubject.digest.valley-tree-v1 " +
	"1e1c4b3a55a0a3e46e0a8f5f5b0e0c47b0b1b2a0c9d8e7f6a5b4c3d2e1f00912\n"

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func verifier(name string, priv ed25519.PrivateKey) VerifierKey {
	return NewVerifierKey(name, priv.Public().(ed25519.PublicKey))
}

// A relayer can change a note's signature block and nothing else: the
// text is signed. So every way of adding to the block has to be refused
// by the one reading every reader shares, or a note one reader accepts is
// a note another refuses — and in a create-only namespace, the first
// reader's acceptance is permanent. The first case is the one the review
// that found this wrote down: a genuine note with an unknown signature
// line appended, whose signer name holds a tab.
func TestARelayerCannotAppendWhatAnotherReaderRefuses(t *testing.T) {
	signer := key(t)
	genuine, err := Sign([]byte(text), "signer/attestations", signer)
	if err != nil {
		t.Fatal(err)
	}
	known := []VerifierKey{verifier("signer/attestations", signer)}
	if _, _, err := Open(genuine, known); err != nil {
		t.Fatalf("the genuine note: %v", err)
	}

	for what, appended := range map[string]string{
		"a tab in the signer's name":       "— x\t AAAAAAA=\n",
		"a signature that is not Ed25519":  "— x AAAAAAA=\n",
		"base64 that does not decode":      "— x !!!!\n",
		"no signature at all":              "— x\n",
		"a line that is not a signature":   "a comment\n",
		"a second line by the same signer": strings.SplitAfterN(string(genuine), "\n\n", 2)[1],
	} {
		poisoned := append(append([]byte{}, genuine...), appended...)
		if _, _, err := Split(poisoned); err == nil {
			t.Errorf("%s: the note split", what)
		}
		if _, _, err := Open(poisoned, known); err == nil {
			t.Errorf("%s: the note opened", what)
		}
	}
}

// A well-formed signature by a key the reader does not hold is a party the
// reader cannot speak for, not a forgery: it is carried through as
// unknown, and the note opens on the signature the reader can check.
func TestAWellFormedSignatureByAnUnknownKeyIsCarriedThrough(t *testing.T) {
	signer, witness := key(t), key(t)
	n, err := Sign([]byte(text), "signer/attestations", signer)
	if err != nil {
		t.Fatal(err)
	}
	if n, err = AddSignature(n, "witness/attestations", witness); err != nil {
		t.Fatal(err)
	}
	_, checked, err := Open(n, []VerifierKey{verifier("signer/attestations", signer)})
	if err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 || !checked[0].Verified || checked[1].Known {
		t.Errorf("checked = %+v, want the signer verified and the witness unknown", checked)
	}

	// A signature by a key the reader does hold must verify, beside any
	// other that does.
	other := verifier("other/attestations", key(t))
	blob := make([]byte, 4+ed25519.SignatureSize)
	binary.BigEndian.PutUint32(blob, other.Hash)
	forged := append(append([]byte{}, n...), Mark+"other/attestations "+base64.StdEncoding.EncodeToString(blob)+"\n"...)
	if _, _, err := Open(forged, []VerifierKey{verifier("signer/attestations", signer), other}); err == nil {
		t.Error("a note carrying a signature that does not check out opened")
	}
}

func TestStatementSubject(t *testing.T) {
	s, err := StatementSubject([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if s.Scheme != "valley-tree-v1" || !strings.HasPrefix(s.Digest, "1e1c4b3a") {
		t.Errorf("subject = %+v", s)
	}
	for what, bad := range map[string]string{
		"another document":     "something/else\nsubject.primary x\nsubject.digest.x y\n",
		"no primary":           StatementType + "\nsubject.digest.x y\n",
		"a digest set twice":   text + "subject.digest.valley-tree-v1 00\n",
		"a line with no value": StatementType + "\nsubject.primary\n",
	} {
		if _, err := StatementSubject([]byte(bad)); err == nil {
			t.Errorf("%s: read a subject", what)
		}
	}
}
