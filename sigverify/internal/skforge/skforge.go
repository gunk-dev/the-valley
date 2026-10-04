// Package skforge makes security-key signatures in software, for tests.
//
// A security-key signature is supposed to mean that a person touched
// hardware, and no test machine has any. So the tests that pin what the
// verifier accepts and refuses need signatures nobody touched anything to
// make, with the authenticator flags chosen by the test. This package
// produces them as standard armored SSHSIG. It derives an ed25519 or P-256
// key from a seed string and wraps it in the sk- key and signature
// envelopes. The cryptography is real: each signature is a valid signature
// over the bytes a real authenticator would sign.
//
// It is a forgery tool, so it lives under internal/ and only tests import
// it. Nothing built from this module for shipping links it. The keys exist
// in their seeds and nowhere else.
//
// It rebuilds the signed bytes independently of the verifier, so that a
// mistake in one does not hide the same mistake in the other. The tests
// also check real ssh-keygen signatures, which keeps both honest.
//
// Envelope takes a finished signature apart so a test can change one field
// after signing, as an attacker holding the signature could.
package skforge

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// Counter is the signature counter every forged signature carries.
const Counter = 0x12345678

// Key is a seeded key that signs in software.
type Key struct {
	Type        string
	application string
	ed          ed25519.PrivateKey
	ec          *ecdsa.PrivateKey
}

// Ed25519SK is a sk-ssh-ed25519@openssh.com key derived from seed.
func Ed25519SK(seed string) Key {
	return Key{Type: "sk-ssh-ed25519@openssh.com", application: "ssh:", ed: edKey(seed)}
}

// ECDSASK is a sk-ecdsa-sha2-nistp256@openssh.com key derived from seed.
func ECDSASK(seed string) Key {
	sum := sha256.Sum256([]byte(seed))
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), sum[:])
	if err != nil {
		panic(err) // only if the seed's hash is not below the curve order
	}
	return Key{Type: "sk-ecdsa-sha2-nistp256@openssh.com", application: "ssh:", ec: priv}
}

// WithApplication returns the same security key enrolled under another
// FIDO application. Its public key blob changes, and so do the bytes it
// signs.
func (k Key) WithApplication(application string) Key {
	k.application = application
	return k
}

// Ed25519 is a plain ssh-ed25519 software key derived from seed. A TKey's
// signatures are exactly what this key makes: the TKey's guarantees live in
// the device and in which public key it has, not in the signature bytes.
func Ed25519(seed string) Key {
	return Key{Type: "ssh-ed25519", ed: edKey(seed)}
}

func edKey(seed string) ed25519.PrivateKey {
	sum := sha256.Sum256([]byte(seed))
	return ed25519.NewKeyFromSeed(sum[:])
}

// Blob is the public key's wire encoding.
func (k Key) Blob() []byte {
	b := str(nil, k.Type)
	if k.ec != nil {
		point, _ := k.ec.PublicKey.Bytes()
		b = str(b, "nistp256")
		b = bytesStr(b, point)
	} else {
		b = bytesStr(b, k.ed.Public().(ed25519.PublicKey))
	}
	if k.application != "" {
		b = str(b, k.application)
	}
	return b
}

// PublicLine is the key as an authorized_keys or allowed-signers line
// writes it: the type and the base64 blob.
func (k Key) PublicLine() string {
	return k.Type + " " + base64.StdEncoding.EncodeToString(k.Blob())
}

// Sign returns an armored SSHSIG over message in namespace. For a security
// key, flags is the authenticator flags byte: 0x01 is a touch, 0x05 a
// touch and a PIN, and 0x00 a signature nobody was present for. A software
// key ignores it.
func (k Key) Sign(message []byte, namespace string, flags byte) []byte {
	sum := sha512.Sum512(message)
	data := []byte("SSHSIG")
	data = str(data, namespace)
	data = str(data, "")
	data = str(data, "sha512")
	data = bytesStr(data, sum[:])

	signed := data
	if k.application != "" {
		app := sha256.Sum256([]byte(k.application))
		msg := sha256.Sum256(data)
		signed = append(app[:], flags)
		signed = binary.BigEndian.AppendUint32(signed, Counter)
		signed = append(signed, msg[:]...)
	}

	sig := str(nil, k.Type)
	if k.ec != nil {
		h := sha256.Sum256(signed)
		r, s := mustSignECDSA(k.ec, h[:])
		sig = bytesStr(sig, bytesStr(bytesStr(nil, r), s))
	} else {
		sig = bytesStr(sig, ed25519.Sign(k.ed, signed))
	}
	if k.application != "" {
		sig = append(sig, flags)
		sig = binary.BigEndian.AppendUint32(sig, Counter)
	}

	blob := []byte("SSHSIG")
	blob = binary.BigEndian.AppendUint32(blob, 1)
	blob = bytesStr(blob, k.Blob())
	blob = str(blob, namespace)
	blob = str(blob, "")
	blob = str(blob, "sha512")
	blob = bytesStr(blob, sig)
	return Armor(blob)
}

// Envelope is an SSHSIG blob split into its fields, so a test can change
// one and reassemble the rest exactly.
type Envelope struct {
	Version   uint32
	PublicKey []byte
	Namespace string
	Reserved  []byte
	HashAlg   string
	Signature []byte
	Trailing  []byte // bytes after the last field
}

// ParseEnvelope splits an armored SSHSIG. It panics on input this package
// did not make.
func ParseEnvelope(armored []byte) Envelope {
	text := strings.TrimSpace(string(armored))
	text = strings.TrimPrefix(text, "-----BEGIN SSH SIGNATURE-----")
	text = strings.TrimSuffix(text, "-----END SSH SIGNATURE-----")
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	if err != nil || !strings.HasPrefix(string(blob), "SSHSIG") {
		panic("skforge: not an armored SSHSIG")
	}
	r := Reader{Rest: blob[len("SSHSIG"):]}
	e := Envelope{Version: r.U32()}
	e.PublicKey = r.String()
	e.Namespace = string(r.String())
	e.Reserved = r.String()
	e.HashAlg = string(r.String())
	e.Signature = r.String()
	e.Trailing = r.Rest
	return e
}

// Blob reassembles the raw SSHSIG.
func (e Envelope) Blob() []byte {
	b := binary.BigEndian.AppendUint32([]byte("SSHSIG"), e.Version)
	b = bytesStr(b, e.PublicKey)
	b = str(b, e.Namespace)
	b = bytesStr(b, e.Reserved)
	b = str(b, e.HashAlg)
	b = bytesStr(b, e.Signature)
	return append(b, e.Trailing...)
}

// Armored reassembles the armored SSHSIG.
func (e Envelope) Armored() []byte { return Armor(e.Blob()) }

// Reader reads SSH wire strings, for taking a signature blob apart. It
// panics on a short read: tests only use it on blobs they made.
type Reader struct{ Rest []byte }

func (r *Reader) U32() uint32 {
	v := binary.BigEndian.Uint32(r.Rest)
	r.Rest = r.Rest[4:]
	return v
}

func (r *Reader) String() []byte {
	n := r.U32()
	s := r.Rest[:n]
	r.Rest = r.Rest[n:]
	return s
}

// String encodes b as an SSH wire string.
func String(b []byte) []byte { return bytesStr(nil, b) }

// Armor wraps a raw SSHSIG blob the way ssh-keygen does.
func Armor(blob []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n-----END SSH SIGNATURE-----\n")
	return []byte(b.String())
}

// mustSignECDSA signs and returns r and s as SSH mpints' contents.
func mustSignECDSA(priv *ecdsa.PrivateKey, digest []byte) (r, s []byte) {
	ri, si, err := ecdsa.Sign(rand.Reader, priv, digest)
	if err != nil {
		panic(err)
	}
	return mpint(ri.Bytes()), mpint(si.Bytes())
}

// mpint prefixes a zero byte where the high bit would read as negative.
func mpint(b []byte) []byte {
	if len(b) > 0 && b[0]&0x80 != 0 {
		return append([]byte{0}, b...)
	}
	return b
}

func str(dst []byte, s string) []byte { return bytesStr(dst, []byte(s)) }

func bytesStr(dst, b []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(b)))
	return append(dst, b...)
}
