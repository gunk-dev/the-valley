// Package sigverify verifies OpenSSH signatures (SSHSIG) and enforces what
// standard OpenSSH verification does not: that a FIDO security key was
// touched when it signed.
//
// A security key does not sign a message directly. It signs authenticator
// data that holds the message's hash and a flags byte. The flags byte has a
// user-presence bit (UP, 0x01), set only when someone touched the key, and
// a user-verification bit (UV, 0x04), set only when the key also checked a
// PIN or biometric. Both bits are inside the signed bytes, so a verifier
// that reads them cannot be fooled about them.
//
// `ssh-keygen -Y verify`, and so `git verify-tag` with gpg.format=ssh,
// never reads them. And a process running as the key's owner can ask the
// key to sign without a touch: it only has to clear one flag in the key
// stub on disk. So a check that a person was present has to happen here,
// in the verifier.
//
// Verify checks, in order:
//
//  1. The signature is a well-formed SSHSIG.
//  2. Its namespace is the one the caller expects.
//  3. Its key is a security key, unless the policy admits software keys.
//  4. The signature verifies over the message.
//  5. The allowed-signers file lists the key, for the requested principal
//     if there is one, in this namespace, at the verify time.
//  6. A security key's signature has UP set, always.
//  7. It has UV set, when the policy or the signer's line requires it.
//
// The first check that fails is the refusal, with a Reason that names it.
package sigverify

import (
	"errors"
	"fmt"
	"time"
)

// The authenticator-data flag bits, from the WebAuthn specification.
const (
	FlagUserPresent  = 0x01
	FlagUserVerified = 0x04
)

// Reason names why a signature was refused. The values are stable, and
// callers may match on them.
type Reason string

const (
	// Malformed: the signature, or the git tag carrying it, cannot be read.
	Malformed Reason = "malformed"
	// Unsigned: a git tag carries no signature at all.
	Unsigned Reason = "unsigned"
	// WrongNamespace: the signature was made for a different namespace.
	WrongNamespace Reason = "wrong-namespace"
	// UnsupportedKey: the signing key is of a type this package does not
	// verify, such as a certificate.
	UnsupportedKey Reason = "unsupported-key"
	// NotSecurityKey: the key is a software key, and the policy requires a
	// FIDO security key.
	NotSecurityKey Reason = "not-security-key"
	// BadSignature: the signature does not verify over the message. Either
	// the message changed, or the signature is not that key's.
	BadSignature Reason = "bad-signature"
	// UnknownKey: no allowed-signers line lists the key.
	UnknownKey Reason = "unknown-key"
	// PrincipalNotAllowed: the key is listed, but not for the requested
	// principal.
	PrincipalNotAllowed Reason = "principal-not-allowed"
	// NamespaceNotAllowed: the key's line restricts it to other namespaces.
	NamespaceNotAllowed Reason = "namespace-not-allowed"
	// NotYetValid: the verify time is before the key's valid-after.
	NotYetValid Reason = "not-yet-valid"
	// Expired: the verify time is after the key's valid-before.
	Expired Reason = "expired"
	// NoUserPresence: a genuine security-key signature made without a
	// touch. The key's owner should treat this as a sign that something
	// running as them is using the key.
	NoUserPresence Reason = "no-user-presence"
	// NoUserVerification: user verification was required, and the
	// signature does not prove it.
	NoUserVerification Reason = "no-user-verification"
	// TagNameMismatch: a signed git tag object is reachable under a name
	// other than the one it was signed with.
	TagNameMismatch Reason = "tag-name-mismatch"
)

// Refusal is the error Verify returns when it examined a signature and
// does not accept it. Any other error means verification could not be
// attempted.
type Refusal struct {
	Reason Reason
	Detail string
	// Signer is what is known about the signing key. It is nil when the
	// signature could not be read far enough to name one.
	Signer *Signer
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("refused (%s): %s", r.Reason, r.Detail)
}

// Policy is what a caller requires beyond a valid, allowed signature.
// User presence is not in it: every security-key signature must have UP
// set, and nothing turns that off.
type Policy struct {
	// RequireUV refuses any signature that does not prove user
	// verification. A software key cannot prove it, so with RequireUV
	// set only security keys can pass.
	RequireUV bool
	// AllowNonSK admits signatures by software keys, which prove nothing
	// about presence. Leave it off for anything a human approves.
	AllowNonSK bool
}

// Options says what to verify against.
type Options struct {
	// Namespace is the SSHSIG namespace the signature must carry, such as
	// "git" for git objects. Required.
	Namespace string
	// Principal, if set, is the identity the signature must be valid for,
	// as with `ssh-keygen -Y verify -I`. If empty, any principal the key
	// is listed under will do, as with `ssh-keygen -Y find-principals`.
	Principal string
	Policy    Policy
	// Time is when the signer's validity window is judged. The zero value
	// means now.
	Time time.Time
}

// Signer describes the key that made a signature.
type Signer struct {
	KeyType     string
	Fingerprint string // SHA256:..., as `ssh-keygen -l` prints it
	SecurityKey bool
	// Flags and Counter are the authenticator's flags byte and signature
	// counter. They are zero for a software key.
	Flags   byte
	Counter uint32
}

// UserPresent reports whether the signature proves a touch.
func (s Signer) UserPresent() bool { return s.SecurityKey && s.Flags&FlagUserPresent != 0 }

// UserVerified reports whether the signature proves a PIN or biometric.
func (s Signer) UserVerified() bool { return s.SecurityKey && s.Flags&FlagUserVerified != 0 }

// Result is a verified signature.
type Result struct {
	Signer
	Namespace string
	// Principal is the requested principal or, if none was requested, the
	// principals field of the first allowed-signers line that matched.
	Principal string
}

// Verify checks signature, an armored or raw SSHSIG, over message.
// It returns a *Refusal if the signature is not acceptable.
func Verify(signers *AllowedSigners, message, signature []byte, opts Options) (*Result, error) {
	if opts.Namespace == "" {
		return nil, errors.New("sigverify: no namespace given")
	}
	if signers == nil {
		return nil, errors.New("sigverify: no allowed signers given")
	}
	at := opts.Time
	if at.IsZero() {
		at = time.Now()
	}

	env, err := parseSignature(signature)
	if err != nil {
		return nil, &Refusal{Reason: Malformed, Detail: "the signature is not an SSHSIG: " + err.Error()}
	}
	key, err := parsePublicKey(env.publicKey)
	if err != nil {
		var unsupported unsupportedKeyError
		if errors.As(err, &unsupported) {
			return nil, &Refusal{Reason: UnsupportedKey, Detail: err.Error()}
		}
		return nil, &Refusal{Reason: Malformed, Detail: err.Error()}
	}
	signer := &Signer{KeyType: key.typ, Fingerprint: key.fingerprint(), SecurityKey: key.securityKey()}
	refuse := func(reason Reason, format string, args ...any) (*Result, error) {
		return nil, &Refusal{Reason: reason, Detail: fmt.Sprintf(format, args...), Signer: signer}
	}

	if env.namespace != opts.Namespace {
		return refuse(WrongNamespace, "the signature is for namespace %q, not %q", env.namespace, opts.Namespace)
	}
	if !signer.SecurityKey && !opts.Policy.AllowNonSK {
		return refuse(NotSecurityKey, "the signature is by a %s software key, and only FIDO security keys are accepted", key.typ)
	}
	data, err := signedData(env.namespace, env.hashAlg, message)
	if err != nil {
		return refuse(Malformed, "%s", err)
	}
	auth, ok, err := key.verify(data, env.signature)
	if err != nil {
		return refuse(Malformed, "%s", err)
	}
	if !ok {
		return refuse(BadSignature, "the signature by %s does not verify over this message", signer.Fingerprint)
	}
	signer.Flags, signer.Counter = auth.flags, auth.counter

	principal, uvRequired, refusal := signers.lookup(key.blob, opts.Principal, opts.Namespace, at)
	if refusal != nil {
		refusal.Signer = signer
		return nil, refusal
	}

	if signer.SecurityKey && !signer.UserPresent() {
		return refuse(NoUserPresence, "the signature by %s is genuine, but its authenticator flags are 0x%02x: the key was not touched", signer.Fingerprint, signer.Flags)
	}
	if (opts.Policy.RequireUV || uvRequired) && !signer.UserVerified() {
		why := "the policy requires it"
		if !opts.Policy.RequireUV {
			why = "the signer's line carries verify-required"
		}
		if !signer.SecurityKey {
			return refuse(NoUserVerification, "user verification is required (%s), and a %s software key cannot prove it", why, key.typ)
		}
		return refuse(NoUserVerification, "user verification is required (%s), and the authenticator flags are 0x%02x", why, signer.Flags)
	}

	return &Result{Signer: *signer, Namespace: env.namespace, Principal: principal}, nil
}
