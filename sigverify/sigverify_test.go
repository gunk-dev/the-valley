package sigverify

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"the-valley/sigverify/internal/skforge"
)

var (
	approver = skforge.Ed25519SK("sigverify test approver")
	ecdsaSK  = skforge.ECDSASK("sigverify test ecdsa approver")
	software = skforge.Ed25519("sigverify test software key")
	stranger = skforge.Ed25519SK("sigverify test stranger")

	// tkey stands in for a Tillitis TKey. A TKey's signature is an ordinary
	// ssh-ed25519 SSHSIG, byte for byte what this software key makes. What
	// makes it trustworthy is in the device: the signer app demands a touch
	// for every signature, and the key is derived from the app's hash, so a
	// no-touch app has a different key. None of that is visible in the
	// bytes, so a verifier can only do what these tests check: accept the
	// pinned key, on a tkey-signer line, when the call opts in to the class.
	tkey = skforge.Ed25519("sigverify test tkey")

	message = []byte("object 0123\ntype commit\ntag v1\n\nrelease v1\n")
	when    = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
)

const (
	release = "release@cosmo.invalid"
	root    = "root@valley.invalid"
)

// signers is the allowed-signers file most cases verify against.
func signers(t *testing.T) *AllowedSigners {
	t.Helper()
	return parse(t, strings.Join([]string{
		"# comments and blank lines are skipped",
		"",
		release + ` namespaces="git,file" ` + approver.PublicLine() + " the approver",
		`ecdsa@cosmo.invalid ` + ecdsaSK.PublicLine(),
		`ci@cosmo.invalid ` + software.PublicLine(),
		root + ` tkey-signer="1.0.0" ` + tkey.PublicLine(),
	}, "\n"))
}

func parse(t *testing.T, text string) *AllowedSigners {
	t.Helper()
	a, err := ParseAllowedSigners("allowed_signers", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// as is the options for verifying in the git namespace as principal.
func as(principal string, p Policy) Options {
	return Options{Namespace: "git", Principal: principal, Policy: p, Time: when}
}

func TestVerify(t *testing.T) {
	none := Policy{}
	tkeyOnly := Policy{Classes: []Class{ClassTKey}}

	cases := []struct {
		name    string
		signers string // replaces the default file when set
		sig     []byte
		message []byte
		opts    Options
		want    Reason // empty: accepted
		class   Class  // checked when accepted
	}{
		{name: "a touched sk-ed25519 signature", sig: approver.Sign(message, "git", 0x01), opts: as(release, none), class: ClassFIDO},
		{name: "a touched sk-ecdsa signature", sig: ecdsaSK.Sign(message, "git", 0x01), opts: as("ecdsa@cosmo.invalid", none), class: ClassFIDO},
		{name: "touch and PIN, UV required", sig: approver.Sign(message, "git", 0x05), opts: as(release, Policy{RequireUV: true}), class: ClassFIDO},
		{name: "a software key the policy admits", sig: software.Sign(message, "git", 0), opts: as("ci@cosmo.invalid", Policy{AllowNonSK: true}), class: ClassSoftware},
		{name: "a TKey, with its class allowed", sig: tkey.Sign(message, "git", 0), opts: as(root, tkeyOnly), class: ClassTKey},
		{name: "a security key, with both classes allowed", sig: approver.Sign(message, "git", 0x01), opts: as(release, Policy{Classes: []Class{ClassFIDO, ClassTKey}}), class: ClassFIDO},

		{name: "untouched: UP clear", sig: approver.Sign(message, "git", 0x00), opts: as(release, none), want: NoUserPresence},
		{name: "untouched sk-ecdsa", sig: ecdsaSK.Sign(message, "git", 0x00), opts: as("ecdsa@cosmo.invalid", none), want: NoUserPresence},
		{name: "a PIN is no touch: UV without UP", sig: approver.Sign(message, "git", 0x04), opts: as(release, none), want: NoUserPresence},
		{name: "UP alone when UV is required", sig: approver.Sign(message, "git", 0x01), opts: as(release, Policy{RequireUV: true}), want: NoUserVerification},
		{name: "a software key cannot prove UV", sig: software.Sign(message, "git", 0), opts: as("ci@cosmo.invalid", Policy{AllowNonSK: true, RequireUV: true}), want: NoUserVerification},
		{name: "wrong namespace", sig: approver.Sign(message, "file", 0x01), opts: as(release, none), want: WrongNamespace},
		{name: "a namespace the line does not allow", sig: approver.Sign(message, "ssh-approval", 0x01), opts: Options{Namespace: "ssh-approval", Principal: release, Time: when}, want: NamespaceNotAllowed},
		{name: "signer not in allowed signers", sig: stranger.Sign(message, "git", 0x01), opts: as(release, none), want: UnknownKey},
		{name: "a principal the line does not list", sig: approver.Sign(message, "git", 0x01), opts: as("someone@else.invalid", none), want: PrincipalNotAllowed},
		{name: "tampered message", sig: approver.Sign(message, "git", 0x01), message: []byte("object 0123\ntype commit\ntag v2\n\nrelease v1\n"), opts: as(release, none), want: BadSignature},
		{name: "a software key when only security keys are accepted", sig: software.Sign(message, "git", 0), opts: as("ci@cosmo.invalid", none), want: ClassNotAllowed},
		{name: "not a signature at all", sig: []byte("-----BEGIN PGP SIGNATURE-----\n"), opts: as(release, none), want: Malformed},

		// The TKey class is opt-in, and opting in to it admits nothing else.
		{name: "a TKey by default", sig: tkey.Sign(message, "git", 0), opts: as(root, none), want: ClassNotAllowed},
		{name: "a TKey with software keys allowed", sig: tkey.Sign(message, "git", 0), opts: as(root, Policy{AllowNonSK: true}), want: ClassNotAllowed},
		{name: "a TKey when UV is required", sig: tkey.Sign(message, "git", 0), opts: as(root, Policy{Classes: []Class{ClassTKey}, RequireUV: true}), want: NoUserVerification},
		{name: "a software key where only a TKey will do", sig: software.Sign(message, "git", 0), opts: as("ci@cosmo.invalid", tkeyOnly), want: ClassNotAllowed},
		{name: "a security key where only a TKey will do", sig: approver.Sign(message, "git", 0x05), opts: as(release, tkeyOnly), want: ClassNotAllowed},
		{
			name:    "a TKey's key on an unmarked line is a software key",
			signers: root + " " + tkey.PublicLine(),
			sig:     tkey.Sign(message, "git", 0), opts: as(root, tkeyOnly), want: ClassNotAllowed,
		},

		{
			name:    "expired signer",
			signers: release + ` valid-before="20260101Z" ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as(release, none), want: Expired,
		},
		{
			name:    "not-yet-valid signer",
			signers: release + ` valid-after="20270101Z" ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as(release, none), want: NotYetValid,
		},
		{
			name:    "inside the validity window",
			signers: release + ` valid-after="20260101Z",valid-before="20270101Z" ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as(release, none), class: ClassFIDO,
		},
		{
			name:    "verify-required on the signer's line",
			signers: release + ` verify-required ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as(release, none), want: NoUserVerification,
		},
		{
			name:    "verify-required satisfied",
			signers: release + ` verify-required ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x05), opts: as(release, none), class: ClassFIDO,
		},
		{
			// Both lines grant this request. The first alone would not
			// demand UV, but the second does, and the stricter one holds.
			name: "verify-required on any matching line",
			signers: release + ` ` + approver.PublicLine() + "\n" +
				`*@cosmo.invalid verify-required ` + approver.PublicLine(),
			sig: approver.Sign(message, "git", 0x01), opts: as(release, none), want: NoUserVerification,
		},
		{
			name:    "a certificate authority line is not the key itself",
			signers: release + ` cert-authority ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as(release, none), want: UnknownKey,
		},

		// The review's two cases. A "*" in the pattern is a wildcard even
		// where the principal holds a "*", so "a*b" matches "a*xb".
		{
			name: "a wildcard principal cannot skip a verify-required line",
			signers: `* ` + approver.PublicLine() + "\n" +
				`a*b verify-required ` + approver.PublicLine(),
			sig: approver.Sign(message, "git", 0x01), opts: as("a*xb", none), want: NoUserVerification,
		},
		{
			name:    "a wildcard principal cannot skip an exclusion",
			signers: `*,!a*b ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as("a*xb", none), want: PrincipalNotAllowed,
		},
		{
			name:    "a line that excludes every principal",
			signers: `!* ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: as(release, none), want: PrincipalNotAllowed,
		},
		{
			// With AnyPrincipal the principals field is not consulted at
			// all, as with ssh-keygen -Y find-principals. That is why it
			// is opt-in.
			name:    "any principal ignores the principals field",
			signers: `!* ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: Options{Namespace: "git", AnyPrincipal: true, Time: when}, class: ClassFIDO,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := signers(t)
			if c.signers != "" {
				a = parse(t, c.signers)
			}
			msg := message
			if c.message != nil {
				msg = c.message
			}
			result, err := Verify(a, msg, c.sig, c.opts)
			if c.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if result.Class != c.class {
					t.Errorf("class %s, want %s", result.Class, c.class)
				}
				return
			}
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("got %+v, %v; want a %s refusal", result, err, c.want)
			}
			if refusal.Reason != c.want {
				t.Fatalf("refused for %s (%s), want %s", refusal.Reason, refusal.Detail, c.want)
			}
		})
	}
}

func TestVerifyReports(t *testing.T) {
	r, err := Verify(signers(t), message, tkey.Sign(message, "git", 0), as(root, Policy{Classes: []Class{ClassTKey}}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Principal != root || r.SignerApp != "1.0.0" || r.SecurityKey || r.Flags != 0 {
		t.Errorf("TKey result %+v", r)
	}
	_, err = Verify(signers(t), message, tkey.Sign(message, "git", 0), as(root, Policy{Classes: []Class{ClassTKey}, RequireUV: true}))
	if err == nil || !strings.Contains(err.Error(), "a TKey signature cannot prove it") {
		t.Errorf("a TKey under RequireUV: %v", err)
	}

	r, err = Verify(signers(t), message, approver.Sign(message, "git", 0x05), Options{Namespace: "git", AnyPrincipal: true, Time: when})
	if err != nil {
		t.Fatal(err)
	}
	if r.Principal != release || r.Flags != 0x05 || r.Counter != skforge.Counter || !r.UserPresent() || !r.UserVerified() {
		t.Errorf("result %+v", r)
	}
}

// A principal is required. Leaving it out is a caller's mistake, not a
// verdict, so it is an error and not a refusal.
func TestVerifyRequiresAPrincipal(t *testing.T) {
	sig := approver.Sign(message, "git", 0x01)
	for name, opts := range map[string]Options{
		"none":     {Namespace: "git"},
		"a NUL":    {Namespace: "git", Principal: "release\x00x"},
		"both":     {Namespace: "git", Principal: release, AnyPrincipal: true},
		"no class": {Namespace: "git", Principal: release, Policy: Policy{Classes: []Class{"yubikey"}}},
	} {
		_, err := Verify(signers(t), message, sig, opts)
		var refusal *Refusal
		if err == nil || errors.As(err, &refusal) {
			t.Errorf("%s: got %v, want an error", name, err)
		}
	}
}

// The refusal for an untouched signature names the key, because a genuine
// signature without a touch means something is using that key.
func TestNoUserPresenceNamesTheKey(t *testing.T) {
	_, err := Verify(signers(t), message, approver.Sign(message, "git", 0), as(release, Policy{}))
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Signer == nil {
		t.Fatalf("got %v", err)
	}
	if !refusal.Signer.SecurityKey || refusal.Signer.Flags != 0 || refusal.Signer.Counter != skforge.Counter {
		t.Errorf("signer %+v", *refusal.Signer)
	}
	if !strings.Contains(refusal.Detail, refusal.Signer.Fingerprint) {
		t.Errorf("the detail does not name the key: %s", refusal.Detail)
	}
}

// Signatures altered after they were made. Each one is a signature that
// once verified, with one thing changed that a signer did not sign or a
// parser must not let through.
func TestAlteredSignatures(t *testing.T) {
	touched := approver.Sign(message, "git", 0x01)
	env := func() skforge.Envelope { return skforge.ParseEnvelope(touched) }
	// The flags byte and the counter are the last five bytes of a security
	// key's signature.
	flags := func(e skforge.Envelope) int { return len(e.Signature) - 5 }

	otherApp := approver.WithApplication("ssh:elsewhere")
	cases := []struct {
		name    string
		signers string
		sig     func() []byte
		opts    Options
		want    Reason // empty: accepted
	}{
		{name: "the original, armored", sig: func() []byte { return touched }},
		{name: "the original, raw", sig: func() []byte { return env().Blob() }},
		{name: "whitespace after the footer", sig: func() []byte { return append(touched, "\n \t\n"...) }},
		{
			// ssh-keygen ignores the reserved field, and it is never
			// signed: the signed bytes always hold an empty one.
			name: "a non-empty reserved field",
			sig:  func() []byte { e := env(); e.Reserved = []byte("anything"); return e.Armored() },
		},

		{
			name: "UV added after signing", opts: as(release, Policy{RequireUV: true}), want: BadSignature,
			sig: func() []byte { e := env(); e.Signature[flags(e)] |= FlagUserVerified; return e.Armored() },
		},
		{
			name: "UP cleared after signing", want: BadSignature,
			sig: func() []byte { e := env(); e.Signature[flags(e)] = 0; return e.Armored() },
		},
		{
			name: "the counter changed", want: BadSignature,
			sig: func() []byte { e := env(); e.Signature[len(e.Signature)-1]++; return e.Armored() },
		},
		{
			// The same key enrolled under another application is listed;
			// the signature was made under "ssh:", and the application is
			// hashed into what the authenticator signed.
			name:    "the application substituted",
			signers: release + " " + otherApp.PublicLine(),
			want:    BadSignature,
			sig:     func() []byte { e := env(); e.PublicKey = otherApp.Blob(); return e.Armored() },
		},
		{
			name: "an ssh-ed25519 key carrying a security-key signature", want: Malformed,
			sig: func() []byte { e := env(); e.PublicKey = software.Blob(); return e.Armored() },
		},
		{
			name: "a security key carrying an ssh-ed25519 signature type", want: Malformed,
			sig: func() []byte {
				e := env()
				r := skforge.Reader{Rest: e.Signature}
				r.String()
				e.Signature = append(skforge.String([]byte(TypeEd25519)), r.Rest...)
				return e.Armored()
			},
		},
		{
			name: "an ECDSA integer with a redundant leading zero", opts: as("ecdsa@cosmo.invalid", Policy{}), want: Malformed,
			sig: func() []byte {
				e := skforge.ParseEnvelope(ecdsaSK.Sign(message, "git", 0x01))
				r := skforge.Reader{Rest: e.Signature}
				typ, inner := r.String(), r.String()
				ints := skforge.Reader{Rest: inner}
				sr, ss := ints.String(), ints.String()
				padded := append(skforge.String(append([]byte{0}, sr...)), skforge.String(ss)...)
				e.Signature = append(append(skforge.String(typ), skforge.String(padded)...), r.Rest...)
				return e.Armored()
			},
		},
		{name: "version 0", want: Malformed, sig: func() []byte { e := env(); e.Version = 0; return e.Armored() }},
		{name: "version 2", want: Malformed, sig: func() []byte { e := env(); e.Version = 2; return e.Armored() }},
		{name: "hash sha1", want: Malformed, sig: func() []byte { e := env(); e.HashAlg = "sha1"; return e.Armored() }},
		{name: "hash SHA512", want: Malformed, sig: func() []byte { e := env(); e.HashAlg = "SHA512"; return e.Armored() }},
		{name: "trailing binary data", want: Malformed, sig: func() []byte { e := env(); e.Trailing = []byte{0}; return e.Armored() }},
		{name: "a truncated blob", want: Malformed, sig: func() []byte { b := env().Blob(); return b[:len(b)-1] }},
		{
			// The signature is the last field, so its length prefix sits
			// just before it. One more than the bytes that remain.
			name: "a length past the end", want: Malformed,
			sig: func() []byte { e := env(); b := e.Blob(); b[len(b)-len(e.Signature)-1]++; return b },
		},
		{name: "text after the footer", want: Malformed, sig: func() []byte { return append(touched, "release v9\n"...) }},
		{name: "a space inside the armor", want: Malformed, sig: func() []byte { return bytes.Replace(touched, []byte("\n"), []byte("\n "), 2) }},
		{name: "base64 with stray padding bits", want: Malformed, sig: func() []byte { return strayPaddingBits(t, env()) }},
		{name: "text before the header", want: Malformed, sig: func() []byte { return append([]byte("\n"), touched...) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := signers(t)
			if c.signers != "" {
				a = parse(t, c.signers)
			}
			opts := c.opts
			if opts.Namespace == "" {
				opts = as(release, Policy{})
			}
			_, err := Verify(a, message, c.sig(), opts)
			if c.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Reason != c.want {
				t.Fatalf("got %v, want %s", err, c.want)
			}
		})
	}
}

// strayPaddingBits armors an envelope with base64 whose padding bits are
// not zero. A lenient decoder reads the same bytes from it, so it is a
// second encoding of the same signature.
func strayPaddingBits(t *testing.T, e skforge.Envelope) []byte {
	t.Helper()
	// Grow the ignored reserved field until the blob needs padding.
	for len(e.Blob())%3 == 0 {
		e.Reserved = append(e.Reserved, 'x')
	}
	enc := strayBits(t, base64.StdEncoding.EncodeToString(e.Blob()))
	armored := skforge.Armor(nil)
	return bytes.Replace(armored, []byte("\n\n"), []byte("\n"+wrap(enc)+"\n"), 1)
}

// strayBits sets a padding bit in padded base64, which a lenient decoder
// ignores.
func strayBits(t *testing.T, enc string) string {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := strings.TrimRight(enc, "=")
	if last == enc {
		t.Fatal("the base64 has no padding")
	}
	i := len(last) - 1
	stray := last[:i] + string(alphabet[strings.IndexByte(alphabet, last[i])+1]) + enc[len(last):]
	want, _ := base64.StdEncoding.DecodeString(enc)
	if got, err := base64.StdEncoding.DecodeString(stray); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("a lenient decoder does not read the same bytes: %v", err)
	}
	return stray
}

func wrap(s string) string {
	var lines []string
	for len(s) > 70 {
		lines, s = append(lines, s[:70]), s[70:]
	}
	return strings.Join(append(lines, s), "\n")
}

func TestParseAllowedSignersRefuses(t *testing.T) {
	key := approver.PublicLine()
	cases := map[string]string{
		"no-touch-required":                  `a@b no-touch-required ` + key,
		"an unknown option":                  `a@b permit-everything ` + key,
		"an unquoted value":                  `a@b namespaces=git ` + key,
		"an unclosed quote":                  `a@b namespaces="git ` + key,
		"a repeated option":                  `a@b namespaces="git",namespaces="file" ` + key,
		"a trailing comma":                   `a@b namespaces="git", ` + key,
		"an inverted window":                 `a@b valid-after="20270101",valid-before="20260101" ` + key,
		"a bad time":                         `a@b valid-after="2026-01-01" ` + key,
		"no key":                             `a@b namespaces="git"`,
		"principals alone":                   `a@b`,
		"a mislabelled key":                  `a@b ssh-ed25519 ` + strings.Fields(key)[1],
		"an unsupported type":                `a@b ssh-dss AAAAB3NzaC1kc3MAAACBAP`,
		"a key that is not b64":              `a@b ` + approver.Type + ` !!!`,
		"a NUL in the principals":            "*,!release\x00x " + key,
		"a NUL in a comment":                 "# a comment\x00",
		"a key with stray b64 bits":          `a@b ` + approver.Type + " " + strayBits(t, strings.Fields(key)[1]),
		"tkey-signer on a security key":      `a@b tkey-signer ` + key,
		"tkey-signer on sk-ecdsa":            `a@b tkey-signer ` + ecdsaSK.PublicLine(),
		"tkey-signer, verify-required":       `a@b tkey-signer,verify-required ` + tkey.PublicLine(),
		"tkey-signer as a CA":                `a@b tkey-signer,cert-authority ` + tkey.PublicLine(),
		"an empty tkey-signer version":       `a@b tkey-signer="" ` + tkey.PublicLine(),
		"a tkey-signer version with a space": `a@b tkey-signer="1 0" ` + tkey.PublicLine(),
		"an unquoted tkey-signer version":    `a@b tkey-signer=1.0 ` + tkey.PublicLine(),
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAllowedSigners("f", []byte(line+"\n")); err == nil {
				t.Fatalf("accepted %q", line)
			} else if !strings.HasPrefix(err.Error(), "f:1: ") {
				t.Errorf("error does not name the line: %v", err)
			}
		})
	}
}

func TestParseAllowedSignersAccepts(t *testing.T) {
	key := approver.PublicLine()
	for _, line := range []string{
		`a@b ` + key,
		`a@b ` + key + ` a comment with spaces`,
		`  a@b,c@d ` + key,
		`"a@b,c d" ` + key,
		`a@b NAMESPACES="git",Valid-After="202601011200Z" ` + key,
		`a@b namespaces="g\"it" ` + key,
		`a@b cert-authority,valid-before="20990101010101" ` + key,
		"a@b\t" + key,
		`a@b tkey-signer ` + tkey.PublicLine(),
		`a@b TKEY-SIGNER="v1.0.0+tillitis:2" ` + tkey.PublicLine(),
		`a@b namespaces="git",tkey-signer="1" ` + tkey.PublicLine(),
	} {
		if _, err := ParseAllowedSigners("f", []byte(line)); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

// matchPatternList is held to OpenSSH's match.c. The cmd/sigverify tests
// also compare it with ssh-keygen -Y match-principals.
func TestMatchPatternList(t *testing.T) {
	cases := []struct {
		s, list string
		want    int
	}{
		{"a@b", "a@b", 1},
		{"a@b", "x@y,a@b", 1},
		{"a@b", "*@b", 1},
		{"a@b", "?@b", 1},
		{"aa@b", "?@b", 0},
		{"a@b", "*", 1},
		{"a@b", "*@b,!a@b", -1},
		{"c@b", "*@b,!a@b", 1},
		{"a@b", "!a@b", -1},
		{"c@b", "!a@b", 0},
		{"A@b", "a@b", 0},
		{"a@b.c", "a@*.c", 1},
		{"a@bxc", "a@*.c", 0},
		{"abcbd", "a*b*d", 1},
		{"abcbe", "a*b*d", 0},
		{"", "", 0},
		// A "*" or "?" in the pattern is a wildcard, whatever the
		// principal holds at that position.
		{"a*xb", "a*b", 1},
		{"a*xb", "*,!a*b", -1},
		{"a*b", "a*b", 1},
		{"a?c", "a?c", 1},
		{"abc", "a?c", 1},
		{"ac", "a?c", 0},
		{"*", "?", 1},
		{"**", "?", 0},
		{"a", "a**", 1},
		{"xa", "**a", 1},
		{"aa", "*a*a*", 1},
		{"a", "*a*a*", 0},
		{"b", "a,,b", 1},
		{`a\b`, `a\*`, 1},
		{"!a", "!!a", -1},
		// A pattern too long for OpenSSH's buffer voids the whole list,
		// even after an earlier match.
		{"x", "x," + strings.Repeat("y", 1023), 0},
		{"x", "x," + strings.Repeat("y", 1022), 1},
	}
	for _, c := range cases {
		if got := matchPatternList(c.s, c.list); got != c.want {
			t.Errorf("matchPatternList(%q, %.40q) = %d, want %d", c.s, c.list, got, c.want)
		}
	}
}

func TestSplitTag(t *testing.T) {
	sig := "-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"
	header := "object 0123456789012345678901234567890123456789\ntype commit\ntag v1\ntagger T <t@t> 1 +0000\n"
	// A line that merely looks like a signature, earlier in the message,
	// is part of the payload: git splits at the last one.
	body := "\nrelease\n-----BEGIN SSH SIGNATURE-----\nnot really\n"
	payload, signature := SplitTag([]byte(header + body + sig))
	if string(payload) != header+body || string(signature) != sig {
		t.Errorf("payload %q\nsignature %q", payload, signature)
	}

	// A signature for the other hash algorithm rides in a gpgsig-sha256
	// header, and git takes it out of the payload.
	other := "gpgsig-sha256 -----BEGIN SSH SIGNATURE-----\n U1NIU0lH\n -----END SSH SIGNATURE-----\n"
	payload, _ = SplitTag([]byte(header + other + body + sig))
	if string(payload) != header+body {
		t.Errorf("payload %q", payload)
	}

	if _, signature := SplitTag([]byte(header + "\nunsigned\n")); signature != nil {
		t.Errorf("an unsigned tag has signature %q", signature)
	}
}

func TestVerifyTag(t *testing.T) {
	a := signers(t)
	const commit = "0123456789abcdef0123456789abcdef01234567"
	tagText := func(object, kind, name string) string {
		return "object " + object + "\ntype " + kind + "\ntag " + name + "\ntagger T <t@t> 1 +0000\n\nrelease\n"
	}
	signed := func(text string, flags byte) []byte {
		return []byte(text + string(approver.Sign([]byte(text), "git", flags)))
	}
	text := tagText(commit, "commit", "v1.2.0")
	object := signed(text, 0x01)

	result, tag, err := VerifyTag(a, object, "v1.2.0", as(release, Policy{}))
	if err != nil {
		t.Fatal(err)
	}
	if result.Principal != release || *tag != (Tag{Name: "v1.2.0", Commit: commit}) {
		t.Errorf("result %+v, tag %+v", result, tag)
	}

	for name, c := range map[string]struct {
		object []byte
		tag    string
		want   Reason
	}{
		"republished under another name": {object, "v1.3.0", TagNameMismatch},
		"unsigned":                       {[]byte(text), "v1.2.0", Unsigned},
		"signed in the wrong namespace":  {[]byte(text + string(approver.Sign([]byte(text), "file", 0x01))), "v1.2.0", WrongNamespace},
		"untouched":                      {signed(text, 0x00), "v1.2.0", NoUserPresence},
		"a target changed after signing": {bytes.Replace(object, []byte(commit), []byte(strings.Repeat("f", 40)), 1), "v1.2.0", BadSignature},
		"pointing at a tag":              {signed(tagText(commit, "tag", "v1.2.0"), 0x01), "v1.2.0", TargetNotCommit},
		"pointing at a tree":             {signed(tagText(commit, "tree", "v1.2.0"), 0x01), "v1.2.0", TargetNotCommit},
		"pointing at no object id":       {signed(tagText("HEAD", "commit", "v1.2.0"), 0x01), "v1.2.0", Malformed},
	} {
		t.Run(name, func(t *testing.T) {
			_, tag, err := VerifyTag(a, c.object, c.tag, as(release, Policy{}))
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Reason != c.want {
				t.Fatalf("got %v, want %s", err, c.want)
			}
			if tag != nil {
				t.Errorf("a refusal reported the tag %+v", tag)
			}
		})
	}
}
