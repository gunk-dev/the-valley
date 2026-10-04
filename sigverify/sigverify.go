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
// Every allowed signer has a class, and each call names the classes it
// accepts (see Class). Verify checks, in order:
//
//  1. The signature is a well-formed SSHSIG.
//  2. Its namespace is the one the caller expects.
//  3. The signature verifies over the message.
//  4. An allowed-signers line lists the key, in a class the call accepts,
//     for the requested principal, in this namespace, at the verify time.
//  5. A security key's signature has UP set, always.
//  6. It has UV set, when the policy or the signer's line requires it. A
//     TKey or software signature cannot prove UV, so then it is refused.
//
// The first check that fails is the refusal, with a Reason that names it.
package sigverify

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The authenticator-data flag bits, from the WebAuthn specification.
const (
	FlagUserPresent  = 0x01
	FlagUserVerified = 0x04
)

// Class is the kind of signer an allowed-signers line names. It decides
// what a signature by that line's key can prove.
type Class string

const (
	// ClassFIDO is a FIDO security key: sk-ssh-ed25519@openssh.com or
	// sk-ecdsa-sha2-nistp256@openssh.com. Its signatures carry the UP and UV
	// bits, and UP is always required.
	ClassFIDO Class = "fido-sk"
	// ClassTKey is a Tillitis TKey running the stock signer app, on a line
	// marked tkey-signer. Its signatures are plain ssh-ed25519 and carry no
	// flags. They can still be trusted to need a touch: the app requires
	// one for every signature, and the TKey derives the key from the hash
	// of the app it runs, so an app that skips the touch has a different
	// key. The line pins the key, and with it the app.
	ClassTKey Class = "tkey-signer"
	// ClassSoftware is any other key. Its signatures prove nothing about
	// presence.
	ClassSoftware Class = "software"
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
	// BadSignature: the signature does not verify over the message. Either
	// the message changed, or the signature is not that key's.
	BadSignature Reason = "bad-signature"
	// UnknownKey: no allowed-signers line lists the key.
	UnknownKey Reason = "unknown-key"
	// ClassNotAllowed: the key is listed, but only in a signer class this
	// call does not accept. A software key where a security key is
	// required is refused for this reason.
	ClassNotAllowed Reason = "class-not-allowed"
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
	// TargetNotCommit: a signed git tag points at something other than a
	// commit, such as another tag or a tree.
	TargetNotCommit Reason = "target-not-commit"
	// TargetMissing: the commit a signed git tag points at is not in the
	// repository.
	TargetMissing Reason = "target-missing"
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
	// Classes is the signer classes the call accepts. Empty means fido-sk
	// only. A trust-root operation that only the TKey may authorize asks
	// for exactly ClassTKey, and then no security key satisfies it.
	Classes []Class
	// AllowNonSK adds ClassSoftware to Classes. Software keys prove nothing
	// about presence: leave it off for anything a human approves. It never
	// makes a key count as a TKey: only a tkey-signer line does that.
	AllowNonSK bool
	// RequireUV refuses any signature that does not prove user
	// verification. Only a security key can prove it, so with RequireUV
	// set, TKey and software signatures are refused.
	RequireUV bool
}

func (p Policy) classes() map[Class]bool {
	set := map[Class]bool{}
	for _, c := range p.Classes {
		set[c] = true
	}
	if len(set) == 0 {
		set[ClassFIDO] = true
	}
	if p.AllowNonSK {
		set[ClassSoftware] = true
	}
	return set
}

func classList(set map[Class]bool) string {
	var names []string
	for c := range set {
		names = append(names, string(c))
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// ParseClass reads a class name.
func ParseClass(name string) (Class, error) {
	switch c := Class(name); c {
	case ClassFIDO, ClassTKey, ClassSoftware:
		return c, nil
	}
	return "", fmt.Errorf("%q is not a signer class: use %s, %s or %s", name, ClassFIDO, ClassTKey, ClassSoftware)
}

// Options says what to verify against.
type Options struct {
	// Namespace is the SSHSIG namespace the signature must carry, such as
	// "git" for git objects. Required.
	Namespace string
	// Principal is the identity the signature must be valid for, as with
	// `ssh-keygen -Y verify -I`. Required, unless AnyPrincipal is set.
	Principal string
	// AnyPrincipal accepts any line that lists the key, whatever its
	// principals field says, as `ssh-keygen -Y find-principals` does. Even
	// a line whose principals are all negated, such as "!*", then
	// authorizes its key. Use it only where the key alone decides.
	AnyPrincipal bool
	Policy       Policy
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
	// counter. They are zero for a key that is not a security key.
	Flags   byte
	Counter uint32
}

// UserPresent reports whether the signature carries the UP bit.
func (s Signer) UserPresent() bool { return s.SecurityKey && s.Flags&FlagUserPresent != 0 }

// UserVerified reports whether the signature carries the UV bit.
func (s Signer) UserVerified() bool { return s.SecurityKey && s.Flags&FlagUserVerified != 0 }

// Result is a verified signature.
type Result struct {
	Signer
	Namespace string
	// Principal is the requested principal or, with AnyPrincipal, the
	// principals field of the first allowed-signers line that matched.
	Principal string
	// Class is the matching line's signer class.
	Class Class
	// SignerApp is the version a tkey-signer line records, if any.
	SignerApp string
}

// Verify checks signature, an armored or raw SSHSIG, over message.
// It returns a *Refusal if the signature is not acceptable.
func Verify(signers *AllowedSigners, message, signature []byte, opts Options) (*Result, error) {
	switch {
	case opts.Namespace == "":
		return nil, errors.New("sigverify: no namespace given")
	case signers == nil:
		return nil, errors.New("sigverify: no allowed signers given")
	case opts.Principal == "" && !opts.AnyPrincipal:
		return nil, errors.New("sigverify: no principal given, and AnyPrincipal is not set")
	case opts.Principal != "" && opts.AnyPrincipal:
		return nil, errors.New("sigverify: both a principal and AnyPrincipal given")
	case strings.IndexByte(opts.Principal, 0) >= 0 || strings.IndexByte(opts.Namespace, 0) >= 0:
		return nil, errors.New("sigverify: a principal or namespace holds a NUL byte")
	}
	for _, c := range opts.Policy.Classes {
		if _, err := ParseClass(string(c)); err != nil {
			return nil, fmt.Errorf("sigverify: %w", err)
		}
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

	m, refusal := signers.lookup(request{
		key:       key.blob,
		principal: opts.Principal,
		namespace: opts.Namespace,
		at:        at,
		classes:   opts.Policy.classes(),
	})
	if refusal != nil {
		refusal.Signer = signer
		return nil, refusal
	}

	if m.class == ClassFIDO && !signer.UserPresent() {
		return refuse(NoUserPresence, "the signature by %s is genuine, but its authenticator flags are 0x%02x: the key was not touched", signer.Fingerprint, signer.Flags)
	}
	if opts.Policy.RequireUV || m.uvRequired {
		why := "the policy requires it"
		if !opts.Policy.RequireUV {
			why = "the signer's line carries verify-required"
		}
		switch {
		case m.class == ClassTKey:
			return refuse(NoUserVerification, "user verification is required (%s), and a TKey signature cannot prove it", why)
		case m.class != ClassFIDO:
			return refuse(NoUserVerification, "user verification is required (%s), and a %s software key cannot prove it", why, key.typ)
		case !signer.UserVerified():
			return refuse(NoUserVerification, "user verification is required (%s), and the authenticator flags are 0x%02x", why, signer.Flags)
		}
	}

	return &Result{
		Signer:    *signer,
		Namespace: env.namespace,
		Principal: m.principal,
		Class:     m.class,
		SignerApp: m.signerApp,
	}, nil
}
