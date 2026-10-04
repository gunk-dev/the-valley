package sigverify

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash"
)

// The OpenSSH key types this package reads. The two sk- types are FIDO
// security keys. The others are software keys, and they carry no
// authenticator flags at all.
const (
	TypeSKEd25519 = "sk-ssh-ed25519@openssh.com"
	TypeSKECDSA   = "sk-ecdsa-sha2-nistp256@openssh.com"
	TypeEd25519   = "ssh-ed25519"
	TypeECDSA256  = "ecdsa-sha2-nistp256"
	TypeECDSA384  = "ecdsa-sha2-nistp384"
	TypeECDSA521  = "ecdsa-sha2-nistp521"
	TypeRSA       = "ssh-rsa"
)

// minRSABits is the smallest RSA modulus accepted. OpenSSH accepts 1024
// bits; this verifier holds RSA keys to 2048.
const minRSABits = 2048

// unsupportedKeyError is a key whose type this package does not verify,
// as opposed to a key whose encoding is broken.
type unsupportedKeyError struct{ reason string }

func (e unsupportedKeyError) Error() string { return e.reason }

// publicKey is one parsed public key.
type publicKey struct {
	typ  string
	blob []byte // the wire encoding, which allowed-signers lines are compared by

	// application is the FIDO application a security key was enrolled
	// under, "ssh:" by default. The authenticator hashes it into every
	// signature, so verification needs it.
	application string

	ed25519 ed25519.PublicKey
	ecdsa   *ecdsa.PublicKey
	rsa     *rsa.PublicKey
}

func (k *publicKey) securityKey() bool {
	return k.typ == TypeSKEd25519 || k.typ == TypeSKECDSA
}

// fingerprint is the SHA-256 fingerprint `ssh-keygen -l` prints.
func (k *publicKey) fingerprint() string {
	sum := sha256.Sum256(k.blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func parsePublicKey(blob []byte) (*publicKey, error) {
	r := reader{rest: blob}
	k := &publicKey{typ: string(r.str()), blob: blob}
	if r.err != nil {
		return nil, fmt.Errorf("the public key %w", r.err)
	}
	switch k.typ {
	case TypeEd25519, TypeSKEd25519:
		pub := r.str()
		if k.typ == TypeSKEd25519 {
			k.application = string(r.str())
		}
		if err := r.end(); err != nil {
			return nil, fmt.Errorf("the %s public key %w", k.typ, err)
		}
		if len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("the %s public key holds %d bytes, not %d", k.typ, len(pub), ed25519.PublicKeySize)
		}
		k.ed25519 = ed25519.PublicKey(pub)
	case TypeECDSA256, TypeECDSA384, TypeECDSA521, TypeSKECDSA:
		curveName, point := string(r.str()), r.str()
		if k.typ == TypeSKECDSA {
			k.application = string(r.str())
		}
		if err := r.end(); err != nil {
			return nil, fmt.Errorf("the %s public key %w", k.typ, err)
		}
		curve, want := curveFor(k.typ)
		if curveName != want {
			return nil, fmt.Errorf("the %s public key names curve %q", k.typ, curveName)
		}
		pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
		if err != nil {
			return nil, fmt.Errorf("the %s public key: %w", k.typ, err)
		}
		k.ecdsa = pub
	case TypeRSA:
		e, n := r.mpint(), r.mpint()
		if err := r.end(); err != nil {
			return nil, fmt.Errorf("the %s public key %w", k.typ, err)
		}
		if n.BitLen() < minRSABits {
			return nil, unsupportedKeyError{fmt.Sprintf("the RSA key has %d bits, and at least %d are required", n.BitLen(), minRSABits)}
		}
		if !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31-1 || e.Bit(0) == 0 {
			return nil, fmt.Errorf("the RSA public exponent %v is not usable", e)
		}
		k.rsa = &rsa.PublicKey{N: n, E: int(e.Int64())}
	default:
		return nil, unsupportedKeyError{fmt.Sprintf("keys of type %q are not supported", k.typ)}
	}
	return k, nil
}

func curveFor(typ string) (elliptic.Curve, string) {
	switch typ {
	case TypeECDSA384:
		return elliptic.P384(), "nistp384"
	case TypeECDSA521:
		return elliptic.P521(), "nistp521"
	default:
		return elliptic.P256(), "nistp256"
	}
}

// authenticatorData is what a security key's signature carries besides the
// signature itself.
type authenticatorData struct {
	flags   byte
	counter uint32
}

// verify checks sig, a signature blob in the SSH wire encoding, over data.
// It returns an error if the blob is malformed, and ok false if it is well
// formed but does not verify.
//
// A security key does not sign data directly. The authenticator signs its
// own authenticator data, with the hash of data inside it:
//
//	sha256(application) || flags || counter || sha256(data)
//
// The flags byte holds the user-presence and user-verification bits. They
// are inside the signed bytes, so nobody can set or clear them after the
// fact, and verify returns them for the caller to check.
func (k *publicKey) verify(data, sig []byte) (auth authenticatorData, ok bool, err error) {
	r := reader{rest: sig}
	sigType := string(r.str())
	if r.err != nil {
		return auth, false, fmt.Errorf("the signature blob %w", r.err)
	}
	if k.typ == TypeRSA {
		ok, err := verifyRSA(k.rsa, sigType, &r, data)
		return auth, ok, err
	}
	if sigType != k.typ {
		return auth, false, fmt.Errorf("a %s key cannot make a %q signature", k.typ, sigType)
	}

	raw := r.str()
	if k.securityKey() {
		auth.flags, auth.counter = r.u8(), r.u32()
	}
	if err := r.end(); err != nil {
		return auth, false, fmt.Errorf("the %s signature %w", k.typ, err)
	}

	signed := data
	if k.securityKey() {
		app := sha256.Sum256([]byte(k.application))
		msg := sha256.Sum256(data)
		signed = make([]byte, 0, len(app)+1+4+len(msg))
		signed = append(signed, app[:]...)
		signed = append(signed, auth.flags)
		signed = binary.BigEndian.AppendUint32(signed, auth.counter)
		signed = append(signed, msg[:]...)
	}

	if k.ed25519 != nil {
		if len(raw) != ed25519.SignatureSize {
			return auth, false, fmt.Errorf("the %s signature holds %d bytes, not %d", k.typ, len(raw), ed25519.SignatureSize)
		}
		return auth, ed25519.Verify(k.ed25519, signed, raw), nil
	}

	// An ECDSA signature is the two integers r and s, inside the string.
	rs := reader{rest: raw}
	sr, ss := rs.mpint(), rs.mpint()
	if err := rs.end(); err != nil {
		return auth, false, fmt.Errorf("the %s signature %w", k.typ, err)
	}
	h := ecdsaHash(k.typ)
	h.Write(signed)
	return auth, ecdsa.Verify(k.ecdsa, h.Sum(nil), sr, ss), nil
}

// ecdsaHash is the digest each ECDSA type signs with. A security key's
// authenticator data is always hashed with SHA-256.
func ecdsaHash(typ string) hash.Hash {
	switch typ {
	case TypeECDSA384:
		return sha512.New384()
	case TypeECDSA521:
		return sha512.New()
	default:
		return sha256.New()
	}
}

// verifyRSA checks an RSA signature. ssh-keygen accepts only the SHA-2
// signature types for SSHSIG, never SHA-1 "ssh-rsa", and so does this.
func verifyRSA(pub *rsa.PublicKey, sigType string, r *reader, data []byte) (bool, error) {
	var h crypto.Hash
	switch sigType {
	case "rsa-sha2-256":
		h = crypto.SHA256
	case "rsa-sha2-512":
		h = crypto.SHA512
	default:
		return false, fmt.Errorf("an RSA signature of type %q is not accepted", sigType)
	}
	raw := r.str()
	if err := r.end(); err != nil {
		return false, fmt.Errorf("the RSA signature %w", err)
	}
	// OpenSSH left-pads a short signature to the modulus length.
	size := pub.Size()
	if len(raw) > size {
		return false, fmt.Errorf("the RSA signature is longer than the key")
	}
	padded := make([]byte, size)
	copy(padded[size-len(raw):], raw)
	d := h.New()
	d.Write(data)
	return rsa.VerifyPKCS1v15(pub, h, d.Sum(nil), padded) == nil, nil
}
