package sigverify

import (
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

	message = []byte("object 0123\ntype commit\ntag v1\n\nrelease v1\n")
	when    = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
)

// signers is the allowed-signers file every case verifies against.
func signers(t *testing.T) *AllowedSigners {
	t.Helper()
	text := strings.Join([]string{
		"# comments and blank lines are skipped",
		"",
		`release@cosmo.invalid namespaces="git,file" ` + approver.PublicLine() + " the approver",
		`ecdsa@cosmo.invalid ` + ecdsaSK.PublicLine(),
		`ci@cosmo.invalid ` + software.PublicLine(),
	}, "\n")
	a, err := ParseAllowedSigners("allowed_signers", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestVerify(t *testing.T) {
	gitOpts := Options{Namespace: "git", Time: when}
	withPolicy := func(p Policy) Options { o := gitOpts; o.Policy = p; return o }
	withPrincipal := func(p string) Options { o := gitOpts; o.Principal = p; return o }

	cases := []struct {
		name      string
		signers   string // replaces the default file when set
		sig       []byte
		message   []byte
		opts      Options
		want      Reason // empty: accepted
		principal string
	}{
		{name: "a touched sk-ed25519 signature", sig: approver.Sign(message, "git", 0x01), opts: gitOpts, principal: "release@cosmo.invalid"},
		{name: "a touched sk-ecdsa signature", sig: ecdsaSK.Sign(message, "git", 0x01), opts: gitOpts, principal: "ecdsa@cosmo.invalid"},
		{name: "touch and PIN, UV required", sig: approver.Sign(message, "git", 0x05), opts: withPolicy(Policy{RequireUV: true})},
		{name: "a named principal the line lists", sig: approver.Sign(message, "git", 0x01), opts: withPrincipal("release@cosmo.invalid"), principal: "release@cosmo.invalid"},
		{name: "a software key the policy admits", sig: software.Sign(message, "git", 0), opts: withPolicy(Policy{AllowNonSK: true}), principal: "ci@cosmo.invalid"},

		{name: "untouched: UP clear", sig: approver.Sign(message, "git", 0x00), opts: gitOpts, want: NoUserPresence},
		{name: "untouched sk-ecdsa", sig: ecdsaSK.Sign(message, "git", 0x00), opts: gitOpts, want: NoUserPresence},
		{name: "a PIN is no touch: UV without UP", sig: approver.Sign(message, "git", 0x04), opts: gitOpts, want: NoUserPresence},
		{name: "UP alone when UV is required", sig: approver.Sign(message, "git", 0x01), opts: withPolicy(Policy{RequireUV: true}), want: NoUserVerification},
		{name: "a software key cannot prove UV", sig: software.Sign(message, "git", 0), opts: withPolicy(Policy{AllowNonSK: true, RequireUV: true}), want: NoUserVerification},
		{name: "wrong namespace", sig: approver.Sign(message, "file", 0x01), opts: gitOpts, want: WrongNamespace},
		{name: "a namespace the line does not allow", sig: approver.Sign(message, "ssh-approval", 0x01), opts: Options{Namespace: "ssh-approval", Time: when}, want: NamespaceNotAllowed},
		{name: "signer not in allowed signers", sig: stranger.Sign(message, "git", 0x01), opts: gitOpts, want: UnknownKey},
		{name: "a principal the line does not list", sig: approver.Sign(message, "git", 0x01), opts: withPrincipal("someone@else.invalid"), want: PrincipalNotAllowed},
		{name: "tampered message", sig: approver.Sign(message, "git", 0x01), message: []byte("object 0123\ntype commit\ntag v2\n\nrelease v1\n"), opts: gitOpts, want: BadSignature},
		{name: "software key when sk is required", sig: software.Sign(message, "git", 0), opts: gitOpts, want: NotSecurityKey},
		{name: "not a signature at all", sig: []byte("-----BEGIN PGP SIGNATURE-----\n"), opts: gitOpts, want: Malformed},

		{
			name:    "expired signer",
			signers: `release@cosmo.invalid valid-before="20260101Z" ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: gitOpts, want: Expired,
		},
		{
			name:    "not-yet-valid signer",
			signers: `release@cosmo.invalid valid-after="20270101Z" ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: gitOpts, want: NotYetValid,
		},
		{
			name:    "inside the validity window",
			signers: `release@cosmo.invalid valid-after="20260101Z",valid-before="20270101Z" ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: gitOpts, principal: "release@cosmo.invalid",
		},
		{
			name:    "verify-required on the signer's line",
			signers: `release@cosmo.invalid verify-required ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: gitOpts, want: NoUserVerification,
		},
		{
			name:    "verify-required satisfied",
			signers: `release@cosmo.invalid verify-required ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x05), opts: gitOpts, principal: "release@cosmo.invalid",
		},
		{
			// Both lines grant this request. The first alone would not
			// demand UV, but the second does, and the stricter one holds.
			name: "verify-required on any matching line",
			signers: `release@cosmo.invalid ` + approver.PublicLine() + "\n" +
				`*@cosmo.invalid verify-required ` + approver.PublicLine(),
			sig: approver.Sign(message, "git", 0x01), opts: gitOpts, want: NoUserVerification,
		},
		{
			name:    "a certificate authority line is not the key itself",
			signers: `release@cosmo.invalid cert-authority ` + approver.PublicLine(),
			sig:     approver.Sign(message, "git", 0x01), opts: gitOpts, want: UnknownKey,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := signers(t)
			if c.signers != "" {
				var err error
				if a, err = ParseAllowedSigners("allowed_signers", []byte(c.signers)); err != nil {
					t.Fatal(err)
				}
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
				if c.principal != "" && result.Principal != c.principal {
					t.Errorf("principal %q, want %q", result.Principal, c.principal)
				}
				return
			}
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				t.Fatalf("got %v, %v; want a %s refusal", result, err, c.want)
			}
			if refusal.Reason != c.want {
				t.Fatalf("refused for %s (%s), want %s", refusal.Reason, refusal.Detail, c.want)
			}
		})
	}
}

// The refusal for an untouched signature names the key, because a genuine
// signature without a touch means something is using that key.
func TestNoUserPresenceNamesTheKey(t *testing.T) {
	_, err := Verify(signers(t), message, approver.Sign(message, "git", 0), Options{Namespace: "git"})
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

func TestParseAllowedSignersRefuses(t *testing.T) {
	key := approver.PublicLine()
	cases := map[string]string{
		"no-touch-required":     `a@b no-touch-required ` + key,
		"an unknown option":     `a@b permit-everything ` + key,
		"an unquoted value":     `a@b namespaces=git ` + key,
		"an unclosed quote":     `a@b namespaces="git ` + key,
		"a repeated option":     `a@b namespaces="git",namespaces="file" ` + key,
		"a trailing comma":      `a@b namespaces="git", ` + key,
		"an inverted window":    `a@b valid-after="20270101",valid-before="20260101" ` + key,
		"a bad time":            `a@b valid-after="2026-01-01" ` + key,
		"no key":                `a@b namespaces="git"`,
		"principals alone":      `a@b`,
		"a mislabelled key":     `a@b ssh-ed25519 ` + strings.Fields(key)[1],
		"an unsupported type":   `a@b ssh-dss AAAAB3NzaC1kc3MAAACBAP`,
		"a key that is not b64": `a@b ` + approver.Type + ` !!!`,
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
	} {
		if _, err := ParseAllowedSigners("f", []byte(line)); err != nil {
			t.Errorf("%q: %v", line, err)
		}
	}
}

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
	}
	for _, c := range cases {
		if got := matchPatternList(c.s, c.list); got != c.want {
			t.Errorf("matchPatternList(%q, %q) = %d, want %d", c.s, c.list, got, c.want)
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
	tag := "object 0123456789012345678901234567890123456789\ntype commit\ntag v1.2.0\ntagger T <t@t> 1 +0000\n\nrelease\n"
	object := []byte(tag + string(approver.Sign([]byte(tag), "git", 0x01)))

	result, header, err := VerifyTag(a, object, "v1.2.0", Options{Time: when})
	if err != nil {
		t.Fatal(err)
	}
	if result.Principal != "release@cosmo.invalid" || header.Object != "0123456789012345678901234567890123456789" || header.ObjectType != "commit" {
		t.Errorf("result %+v, tag %+v", result, header)
	}

	for name, c := range map[string]struct {
		object []byte
		tag    string
		want   Reason
	}{
		"republished under another name": {object, "v1.3.0", TagNameMismatch},
		"unsigned":                       {[]byte(tag), "v1.2.0", Unsigned},
		"signed in the wrong namespace":  {[]byte(tag + string(approver.Sign([]byte(tag), "file", 0x01))), "v1.2.0", WrongNamespace},
		"untouched":                      {[]byte(tag + string(approver.Sign([]byte(tag), "git", 0x00))), "v1.2.0", NoUserPresence},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := VerifyTag(a, c.object, c.tag, Options{Time: when})
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Reason != c.want {
				t.Fatalf("got %v, want %s", err, c.want)
			}
		})
	}
}
