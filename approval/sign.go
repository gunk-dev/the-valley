package approval

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"

	"the-valley/note"
)

// A Signer makes the raw Ed25519 signature over a statement's text with
// one key: a software key (Key) or a YubiKey's PIV slot (PIVSession).
type Signer interface {
	Public() ed25519.PublicKey
	Sign(text []byte) ([]byte, error)
}

// Sign signs a statement as its principal and seals it in a note: the
// complete statement text, a blank line, and one signature line named by
// the principal. The note is opened under the signer's own key before it
// is returned, so a signer that signed something else, or with another
// key, returns an error and no note.
func Sign(s Statement, signer Signer) ([]byte, error) {
	text, err := s.Encode()
	if err != nil {
		return nil, err
	}
	pub := signer.Public()
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("the signer holds no ed25519 public key")
	}
	sig, err := signer.Sign(text)
	if err != nil {
		return nil, err
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("the signer returned %d bytes, not an ed25519 signature", len(sig))
	}
	var hash [4]byte
	binary.BigEndian.PutUint32(hash[:], note.KeyHash(s.Principal, pub))
	var b bytes.Buffer
	b.Write(text)
	b.WriteString("\n" + note.Mark + s.Principal + " ")
	b.WriteString(base64.StdEncoding.EncodeToString(append(hash[:], sig...)))
	b.WriteString("\n")
	if _, _, err := note.Open(b.Bytes(), []note.VerifierKey{note.NewVerifierKey(s.Principal, pub)}); err != nil {
		return nil, fmt.Errorf("the signature does not verify under the signer's own key: %w", err)
	}
	return b.Bytes(), nil
}

// Verify opens an approval note under the approval keys a reader holds,
// and returns the statement it carries. The note carries one signature,
// by a key among those keys, over a statement in its written form that
// names the key's principal as its approver.
//
// Which keys a reader holds decides which approvals count: the keys of
// approval-signers, or of approval-signers-hardware when only hardware
// keys may approve.
func Verify(n []byte, authority []note.VerifierKey) (Statement, error) {
	if len(n) > MaxNote {
		return Statement{}, fmt.Errorf("the approval note is %d bytes; one holds at most %d", len(n), MaxNote)
	}
	_, sigs, err := note.Split(n)
	if err != nil {
		return Statement{}, err
	}
	if len(sigs) != 1 {
		return Statement{}, fmt.Errorf("an approval note carries one signature, and this carries %d", len(sigs))
	}
	text, checked, err := note.Open(n, authority)
	if err != nil {
		return Statement{}, err
	}
	s, err := Parse(text)
	if err != nil {
		return Statement{}, fmt.Errorf("the signed text %w", err)
	}
	if checked[0].Name != s.Principal {
		return Statement{}, fmt.Errorf("the approval is signed with a key of %s, and names %s as its approver", checked[0].Name, s.Principal)
	}
	return s, nil
}

// Key is a software approval key.
type Key struct {
	priv ed25519.PrivateKey
}

// NewKey is the software key with this private half.
func NewKey(priv ed25519.PrivateKey) Key { return Key{priv: priv} }

func (k Key) Public() ed25519.PublicKey { return k.priv.Public().(ed25519.PublicKey) }

func (k Key) Sign(text []byte) ([]byte, error) { return ed25519.Sign(k.priv, text), nil }

// ParseKey reads the unencrypted OpenSSH Ed25519 private key that
// `ssh-keygen -t ed25519 -N ”` writes, as attest/sign.go reads a host's.
// A key behind a passphrase is refused.
func ParseKey(raw []byte) (Key, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		return Key{}, errors.New("not an openssh private key")
	}
	body := sshBytes{rest: block.Bytes}
	magic := []byte("openssh-key-v1\x00")
	if !bytes.HasPrefix(body.rest, magic) {
		return Key{}, errors.New("not an openssh private key: the container is unrecognised")
	}
	body.rest = body.rest[len(magic):]
	cipher, kdf := body.str(), body.str()
	body.str() // the kdf options
	count := body.u32()
	body.str() // the public key, which the private section repeats
	section := sshBytes{rest: body.str()}
	if body.err != nil {
		return Key{}, body.err
	}
	if string(cipher) != "none" || string(kdf) != "none" {
		return Key{}, fmt.Errorf("the key is encrypted with %s; only an unencrypted key is read", cipher)
	}
	if count != 1 {
		return Key{}, fmt.Errorf("the file holds %d keys, not one", count)
	}
	if a, b := section.u32(), section.u32(); a != b {
		return Key{}, errors.New("the key is encrypted or damaged: its check words differ")
	}
	if kind := section.str(); string(kind) != "ssh-ed25519" {
		return Key{}, fmt.Errorf("the key is %s, not ssh-ed25519", kind)
	}
	pub, priv := section.str(), section.str()
	if section.err != nil {
		return Key{}, section.err
	}
	if len(pub) != ed25519.PublicKeySize || len(priv) != ed25519.PrivateKeySize {
		return Key{}, errors.New("the key is not the size an ed25519 key is")
	}
	key := ed25519.PrivateKey(priv)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), pub) {
		return Key{}, errors.New("the key's private half does not produce its public half")
	}
	return Key{priv: key}, nil
}

// sshBytes reads the ssh wire encoding, where a string is a four-byte
// big-endian length and that many bytes. The first error is kept and every
// later read is a no-op.
type sshBytes struct {
	rest []byte
	err  error
}

func (s *sshBytes) u32() uint32 {
	if s.err != nil {
		return 0
	}
	if len(s.rest) < 4 {
		s.err = errors.New("the key ends in the middle of a field")
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
