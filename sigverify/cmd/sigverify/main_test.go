package main

// These tests drive the command against real OpenSSH and real git. Every
// signature that ssh-keygen accepts and this verifier must refuse is made
// by ssh-keygen itself, and the cases where the two must agree are checked
// against ssh-keygen's own verdict.
//
// Security-key signatures come from OpenSSH's sk-dummy provider: a
// software authenticator from its regression suite that returns exactly
// the flags it is asked for. The nix check builds it and names it in
// SIGVERIFY_SK_PROVIDER. Without it, the security-key cases skip, and the
// forged signatures in the package tests still cover them.
//
// Some cases also record that ssh-keygen or git accepts a signature this
// verifier refuses. Those are expectations about other tools, kept in
// subtests named "interop: ...". If a future OpenSSH or git starts
// refusing silent signatures, only those subtests fail, and the failure
// says the change is upstream.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"the-valley/sigverify/internal/skforge"
)

func TestMain(m *testing.M) {
	// Nothing here may read the real user's git config or reach their
	// ssh-agent: a test that signs must sign only with its own keys.
	home, err := os.MkdirTemp("", "sigverify-test-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	for _, v := range []string{"SSH_AUTH_SOCK", "SSH_SK_PROVIDER", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY"} {
		os.Unsetenv(v)
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// skProvider returns the sk-dummy provider, or skips the test.
func skProvider(t *testing.T) string {
	t.Helper()
	p := os.Getenv("SIGVERIFY_SK_PROVIDER")
	if p == "" {
		t.Skip("SIGVERIFY_SK_PROVIDER is not set; the nix check sets it to OpenSSH's sk-dummy.so")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("SIGVERIFY_SK_PROVIDER: %v", err)
	}
	return p
}

// cli runs the command in-process, as main would.
func cli(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

// refused checks that a run was refused for reason, and that stdout says
// that and nothing more.
func refused(t *testing.T, what string, reason string, stdout, stderr string, code int) {
	t.Helper()
	if code != exitRefused || stdout != "refused "+reason+"\n" {
		t.Errorf("%s: exit %d, want a %s refusal and nothing else on stdout:\n%s%s", what, code, reason, stdout, stderr)
	}
}

// upstreamAccepts records that another tool accepts a signature this
// verifier refuses. See the file comment.
func upstreamAccepts(t *testing.T, tool string, ok bool, out string) {
	t.Helper()
	t.Run("interop: "+tool+" accepts it", func(t *testing.T) {
		if !ok {
			t.Errorf("an interoperability change, not a sigverify regression: %s now refuses this signature. Update the expectation.\n%s", tool, out)
		}
	})
}

// sh runs a program and returns its combined output and whether it exited
// zero.
func sh(t *testing.T, dir, stdin string, env []string, name string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if _, exited := err.(*exec.ExitError); err != nil && !exited {
		t.Fatalf("%s: %v", name, err)
	}
	return string(out), err == nil
}

func must(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	out, ok := sh(t, dir, "", env, name, args...)
	if !ok {
		t.Fatalf("%s %s failed:\n%s", name, strings.Join(args, " "), out)
	}
	return out
}

// keygen makes a key pair at dir/name with ssh-keygen.
func keygen(t *testing.T, dir, name string, env []string, args ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	must(t, dir, env, "ssh-keygen", append([]string{"-q", "-N", "", "-C", name, "-f", path}, args...)...)
	return path
}

// allowedSigners writes an allowed-signers file of the given lines.
func allowedSigners(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func pub(t *testing.T, key string) string {
	t.Helper()
	b, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func write(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// sshSign signs message with ssh-keygen -Y sign and returns the signature
// file.
func sshSign(t *testing.T, dir string, env []string, key, namespace, message string, opts ...string) string {
	t.Helper()
	msg := write(t, filepath.Join(dir, "message"), message)
	os.Remove(msg + ".sig")
	args := []string{"-Y", "sign", "-n", namespace, "-f", key}
	for _, o := range opts {
		args = append(args, "-O", o)
	}
	must(t, dir, env, "ssh-keygen", append(args, msg)...)
	sig := filepath.Join(dir, namespace+"-"+filepath.Base(key)+".sig")
	if err := os.Rename(msg+".sig", sig); err != nil {
		t.Fatal(err)
	}
	return sig
}

// sshVerify asks ssh-keygen -Y verify for its verdict.
func sshVerify(t *testing.T, dir, allowed, principal, namespace, sig, message string, opts ...string) (string, bool) {
	t.Helper()
	args := []string{"-Y", "verify", "-f", allowed, "-I", principal, "-n", namespace, "-s", sig}
	for _, o := range opts {
		args = append(args, "-O", o)
	}
	return sh(t, dir, message, nil, "ssh-keygen", args...)
}

// field returns the value of a "name value" line in the command's output.
func field(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, name+" "); ok {
			return v
		}
	}
	return ""
}

var goodSignature = regexp.MustCompile(`Good "[^"]+" signature for (\S+) with \S+ key (SHA256:\S+)`)

// The interoperability baseline: what ssh-keygen -Y sign writes, this
// verifies, and both report the same principal and key.
func TestInteroperatesWithSSHKeygen(t *testing.T) {
	dir := t.TempDir()
	const message = "a message ssh-keygen signs\n"
	for _, c := range []struct {
		name, keyType string
		signOpts      []string
	}{
		{"ed25519", "ed25519", nil},
		{"ed25519-sha256", "ed25519", []string{"hashalg=sha256"}},
		{"ecdsa", "ecdsa", nil},
		{"rsa", "rsa", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			key := keygen(t, dir, c.name, nil, "-t", c.keyType)
			allowed := allowedSigners(t, dir, "signer@test.invalid "+pub(t, key))
			sig := sshSign(t, dir, nil, key, "file", message, c.signOpts...)
			args := []string{"verify", "--namespace", "file", "--signature", sig,
				"--allowed-signers", allowed, "--principal", "signer@test.invalid"}

			ref, ok := sshVerify(t, dir, allowed, "signer@test.invalid", "file", sig, message)
			if !ok {
				t.Fatalf("ssh-keygen refused its own signature:\n%s", ref)
			}
			m := goodSignature.FindStringSubmatch(ref)
			if m == nil {
				t.Fatalf("unexpected ssh-keygen output:\n%s", ref)
			}

			out, errOut, code := cli(t, message, append(args, "--allow-non-sk")...)
			if code != exitVerified {
				t.Fatalf("exit %d\n%s%s", code, out, errOut)
			}
			if field(out, "principal") != m[1] || field(out, "fingerprint") != m[2] || field(out, "class") != "software" {
				t.Errorf("ssh-keygen says %s %s; sigverify says\n%s", m[1], m[2], out)
			}

			// Both refuse the same signature over a changed message.
			if ref, ok := sshVerify(t, dir, allowed, "signer@test.invalid", "file", sig, message+"!"); ok {
				t.Fatalf("ssh-keygen accepted a changed message:\n%s", ref)
			}
			out, errOut, code = cli(t, message+"!", append(args, "--allow-non-sk")...)
			refused(t, "a changed message", "bad-signature", out, errOut, code)

			// And a software key is refused outright unless admitted.
			out, errOut, code = cli(t, message, args...)
			refused(t, "without --allow-non-sk", "class-not-allowed", out, errOut, code)
		})
	}
}

// Principal patterns, namespaces and validity windows must mean what they
// mean to ssh-keygen. Each case is put to both, and the verdicts compared.
func TestAllowedSignersAgreeWithSSHKeygen(t *testing.T) {
	dir := t.TempDir()
	const message = "a message\n"
	key := keygen(t, dir, "signer", nil, "-t", "ed25519")
	sigs := map[string]string{
		"git":  sshSign(t, dir, nil, key, "git", message),
		"file": sshSign(t, dir, nil, key, "file", message),
	}
	k := pub(t, key)
	for _, c := range []struct {
		line, principal, namespace, at string
		accept                         bool
	}{
		{"a@x.invalid " + k, "a@x.invalid", "git", "", true},
		{"a@x.invalid,b@x.invalid " + k, "b@x.invalid", "git", "", true},
		{`"a@x.invalid,b@x.invalid" ` + k, "b@x.invalid", "git", "", true},
		{"a@x.invalid " + k, "b@x.invalid", "git", "", false},
		{"*@x.invalid " + k, "anyone@x.invalid", "git", "", true},
		{"*@x.invalid,!evil@x.invalid " + k, "evil@x.invalid", "git", "", false},
		{"*@x.invalid,!evil@x.invalid " + k, "good@x.invalid", "git", "", true},
		{"?@x.invalid " + k, "ab@x.invalid", "git", "", false},
		{"a*b " + k, "a*xb", "git", "", true},
		{"*,!a*b " + k, "a*xb", "git", "", false},
		{`a@x.invalid namespaces="git" ` + k, "a@x.invalid", "git", "", true},
		{`a@x.invalid namespaces="git" ` + k, "a@x.invalid", "file", "", false},
		{`a@x.invalid namespaces="fi*" ` + k, "a@x.invalid", "file", "", true},
		{`a@x.invalid NAMESPACES="git,file" ` + k, "a@x.invalid", "file", "", true},
		{`a@x.invalid valid-after="20990101" ` + k, "a@x.invalid", "git", "", false},
		{`a@x.invalid valid-before="20000101" ` + k, "a@x.invalid", "git", "", false},
		{`a@x.invalid valid-after="20000101",valid-before="20990101" ` + k, "a@x.invalid", "git", "", true},
		{`a@x.invalid valid-before="20300101Z" ` + k, "a@x.invalid", "git", "20291231Z", true},
		{`a@x.invalid valid-before="20300101Z" ` + k, "a@x.invalid", "git", "20300102Z", false},
		{`a@x.invalid valid-after="202601011200Z" ` + k, "a@x.invalid", "git", "202601011159Z", false},
		{`a@x.invalid valid-after="202601011200Z" ` + k, "a@x.invalid", "git", "202601011200Z", true},
		{"a@x.invalid cert-authority " + k, "a@x.invalid", "git", "", false},
	} {
		allowed := allowedSigners(t, dir, c.line)
		var opts, flags []string
		if c.at != "" {
			opts = []string{"verify-time=" + c.at}
			flags = []string{"--verify-time", c.at}
		}
		ref, refOK := sshVerify(t, dir, allowed, c.principal, c.namespace, sigs[c.namespace], message, opts...)
		out, errOut, code := cli(t, message, append([]string{"verify", "--namespace", c.namespace,
			"--signature", sigs[c.namespace], "--allowed-signers", allowed, "--principal", c.principal, "--allow-non-sk"}, flags...)...)
		if refOK != c.accept {
			t.Errorf("%q as %s in %s: ssh-keygen accepted=%v, the case expects %v\n%s", c.line, c.principal, c.namespace, refOK, c.accept, ref)
		}
		if (code == exitVerified) != refOK || (code != exitVerified && code != exitRefused) {
			t.Errorf("%q as %s in %s: ssh-keygen accepted=%v, sigverify exit %d\n%s%s", c.line, c.principal, c.namespace, refOK, code, out, errOut)
		}
	}
}

// Principal matching is held to ssh-keygen -Y match-principals, which
// applies OpenSSH's own pattern matcher to an allowed-signers file and
// nothing else. Each pattern list is one line, and sigverify must accept
// a signature for the principal exactly when ssh-keygen matches it.
func TestPrincipalMatchingAgreesWithSSHKeygen(t *testing.T) {
	dir := t.TempDir()
	key := skforge.Ed25519SK("principal matching")
	const message = "a message\n"
	sig := write(t, filepath.Join(dir, "sig"), string(key.Sign([]byte(message), "git", 0x01)))
	for _, c := range []struct {
		patterns, principal string
		match               bool
	}{
		{"a*b", "a*xb", true}, // the review's case: a "*" in the pattern is a wildcard
		{"a*b", "ab", true},
		{"a*b", "a", false},
		{"*,!a*b", "a*xb", false},
		{"*,!a*b", "c", true},
		{"!a*b", "c", false},
		{"a?c", "a?c", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"?", "*", true},
		{"?", "**", false},
		{"**a", "xa", true},
		{"*a*a*", "aa", true},
		{"*a*a*", "a", false},
		{`a\*`, `a\b`, true},
		{"a,,b", "b", true},
		{"!!a", "!a", false},
		{"A", "a", false},
		{"release@cosmo,!*@evil", "x@evil", false},
		{"*@cosmo.invalid", "release@cosmo.invalid", true},
		{"*@cosmo.invalid", "release@cosmo.invalid.evil", false},
	} {
		allowed := allowedSigners(t, dir, `"`+c.patterns+`" `+key.PublicLine())
		ref, refOK := sh(t, dir, "", nil, "ssh-keygen", "-Y", "match-principals", "-f", allowed, "-I", c.principal)
		if refOK != c.match {
			t.Errorf("ssh-keygen: %q against %q matched=%v, the case expects %v\n%s", c.principal, c.patterns, refOK, c.match, ref)
		}
		out, errOut, code := cli(t, message, "verify", "--namespace", "git", "--signature", sig,
			"--allowed-signers", allowed, "--principal", c.principal)
		if (code == exitVerified) != refOK || (code != exitVerified && code != exitRefused) {
			t.Errorf("%q against %q: ssh-keygen matched=%v, sigverify exit %d\n%s%s", c.principal, c.patterns, refOK, code, out, errOut)
		}
	}
}

// OpenSSH reads an allowed-signers line as a C string, so a NUL ends it.
// This line then has no key, and ssh-keygen rejects it. Read as Go bytes,
// "!release<NUL>x" would be an exclusion that excludes nothing, and the
// "*" before it would admit release. sigverify refuses the file instead.
func TestNULLinesAgreeWithSSHKeygen(t *testing.T) {
	dir := t.TempDir()
	key := skforge.Ed25519SK("nul lines")
	const message = "a message\n"
	sig := write(t, filepath.Join(dir, "sig"), string(key.Sign([]byte(message), "git", 0x01)))
	allowed := allowedSigners(t, dir, "*,!release\x00x "+key.PublicLine())

	if ref, ok := sh(t, dir, "", nil, "ssh-keygen", "-Y", "match-principals", "-f", allowed, "-I", "release"); ok {
		t.Errorf("ssh-keygen matched release against a line holding a NUL:\n%s", ref)
	}
	out, errOut, code := cli(t, message, "verify", "--namespace", "git", "--signature", sig,
		"--allowed-signers", allowed, "--principal", "release")
	if code != exitError || out != "" || !strings.Contains(errOut, "NUL") {
		t.Errorf("exit %d:\n%s%s", code, out, errOut)
	}
}

// The gap this verifier closes. ssh-keygen -Y verify accepts every one of
// these security-key signatures; sigverify refuses the ones nobody touched.
func TestSecurityKeysWithSSHKeygen(t *testing.T) {
	provider := skProvider(t)
	env := []string{"SSH_SK_PROVIDER=" + provider}
	dir := t.TempDir()
	const message = "release v1\n"
	const principal = "release@test.invalid"

	askpass := write(t, filepath.Join(dir, "askpass"), "#!/bin/sh\necho 1234\n")
	if err := os.Chmod(askpass, 0o755); err != nil {
		t.Fatal(err)
	}
	pinEnv := append(env, "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force")

	// verify returns sigverify's report and exit status, and ssh-keygen's
	// verdict on the same signature. options go on sigverify's line only:
	// ssh-keygen skips a line with an option it does not know, such as
	// verify-required.
	verify := func(t *testing.T, key, options, sig string, extra ...string) (string, int, string, bool) {
		t.Helper()
		ref, ok := sshVerify(t, dir, allowedSigners(t, dir, principal+" "+pub(t, key)), principal, "git", sig, message)
		allowed := allowedSigners(t, dir, principal+" "+options+pub(t, key))
		out, errOut, code := cli(t, message, append([]string{"verify", "--namespace", "git",
			"--signature", sig, "--allowed-signers", allowed, "--principal", principal}, extra...)...)
		return out + errOut, code, ref, ok
	}
	accepted := func(t *testing.T, key, options, sig string, extra ...string) string {
		t.Helper()
		out, code, ref, ok := verify(t, key, options, sig, extra...)
		if !ok {
			t.Fatalf("ssh-keygen refused a genuine, touched signature:\n%s", ref)
		}
		if code != exitVerified {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		return out
	}
	silent := func(t *testing.T, reason, key, options, sig string, extra ...string) string {
		t.Helper()
		out, code, ref, ok := verify(t, key, options, sig, extra...)
		upstreamAccepts(t, "ssh-keygen -Y verify", ok, ref)
		if code != exitRefused || !strings.HasPrefix(out, "refused "+reason+"\n") {
			t.Fatalf("exit %d, want %s:\n%s", code, reason, out)
		}
		return out
	}

	for _, typ := range []string{"ed25519-sk", "ecdsa-sk"} {
		t.Run(typ+": touched", func(t *testing.T) {
			key := keygen(t, dir, typ+"-touched", env, "-t", typ)
			out := accepted(t, key, "", sshSign(t, dir, env, key, "git", message))
			if field(out, "flags") != "0x01" || field(out, "user-presence") != "yes" || field(out, "class") != "fido-sk" {
				t.Errorf("report:\n%s", out)
			}
			// UV was not asked for, so requiring it refuses.
			_, code, _, _ := verify(t, key, "", sshSign(t, dir, env, key, "git", message), "--require-uv")
			if code != exitRefused {
				t.Errorf("--require-uv: exit %d", code)
			}
		})

		t.Run(typ+": generated with no-touch-required", func(t *testing.T) {
			key := keygen(t, dir, typ+"-notouch", env, "-t", typ, "-O", "no-touch-required")
			silent(t, "no-user-presence", key, "", sshSign(t, dir, env, key, "git", message))
		})

		// The attack the verifier exists for. Anything running as the key's
		// owner can rewrite the key stub on disk so that ssh-keygen stops
		// asking the authenticator for a touch. (OpenSSH 10.5 does it with
		// `ssh-keygen -p -O no-touch-required`; this works on every version.)
		// The key and its allowed-signers line do not change.
		t.Run(typ+": a stub flipped to no-touch-required", func(t *testing.T) {
			key := keygen(t, dir, typ+"-flipped", env, "-t", typ)
			before := accepted(t, key, "", sshSign(t, dir, env, key, "git", message))
			clearStubFlags(t, key, 0x01)
			after := silent(t, "no-user-presence", key, "", sshSign(t, dir, env, key, "git", message))
			if !strings.Contains(after, field(before, "fingerprint")) {
				t.Errorf("the refusal does not name the key %s:\n%s", field(before, "fingerprint"), after)
			}
		})
	}

	t.Run("a key generated with verify-required", func(t *testing.T) {
		key := keygen(t, dir, "uv", pinEnv, "-t", "ed25519-sk", "-O", "verify-required")
		out := accepted(t, key, "verify-required ", sshSign(t, dir, pinEnv, key, "git", message), "--require-uv")
		if field(out, "flags") != "0x05" || field(out, "user-verification") != "yes" {
			t.Errorf("report:\n%s", out)
		}
	})

	// The same attack one bit over. A release key enrolled with
	// verify-required, whose stub is rewritten to stop asking for the PIN:
	// the signature still proves a touch, and no longer proves the PIN.
	// Its allowed-signers line demands UV, so it is refused without any
	// flag.
	t.Run("a verify-required stub with UV cleared", func(t *testing.T) {
		key := keygen(t, dir, "uv-flipped", pinEnv, "-t", "ed25519-sk", "-O", "verify-required")
		clearStubFlags(t, key, 0x04)
		out := silent(t, "no-user-verification", key, "verify-required ", sshSign(t, dir, env, key, "git", message))
		if !strings.Contains(out, "0x01") {
			t.Errorf("the refusal does not give the flags:\n%s", out)
		}
	})
}

// clearStubFlags clears flag bits in an unencrypted OpenSSH security-key
// stub, leaving the key itself unchanged. 0x01 is touch-required, and 0x04
// is verify-required.
func clearStubFlags(t *testing.T, path string, mask byte) {
	t.Helper()
	const begin, end = "-----BEGIN OPENSSH PRIVATE KEY-----", "-----END OPENSSH PRIVATE KEY-----"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), begin)), end))
	blob, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(inner), ""))
	if err != nil {
		t.Fatal(err)
	}

	// openssh-key-v1: magic, cipher, kdf, kdf options, key count, public
	// key, then the private section. Its security-key entry is two check
	// integers, the type, the public key (for ECDSA, a curve name and a
	// point), the application, and then the flags byte.
	off := len("openssh-key-v1\x00")
	skip := func() string {
		n := int(binary.BigEndian.Uint32(blob[off:]))
		s := string(blob[off+4 : off+4+n])
		off += 4 + n
		return s
	}
	if cipher := skip(); cipher != "none" {
		t.Fatalf("the stub is encrypted with %s", cipher)
	}
	skip()   // kdf
	skip()   // kdf options
	off += 4 // key count
	skip()   // public key
	off += 4 // the private section's length
	off += 8 // check integers
	switch typ := skip(); typ {
	case "sk-ssh-ed25519@openssh.com":
		skip() // public key
	case "sk-ecdsa-sha2-nistp256@openssh.com":
		skip() // curve
		skip() // point
	default:
		t.Fatalf("the stub holds a %s key", typ)
	}
	skip() // application
	if blob[off]&mask != mask {
		t.Fatalf("the stub's flags are 0x%02x, without 0x%02x", blob[off], mask)
	}
	blob[off] &^= mask

	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString(begin + "\n")
	for len(enc) > 70 {
		b.WriteString(enc[:70] + "\n")
		enc = enc[70:]
	}
	b.WriteString(enc + "\n" + end + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitRepo makes a repository with one commit, configured to sign tags with
// key.
func gitRepo(t *testing.T, dir, name, key string) string {
	t.Helper()
	repo := filepath.Join(dir, name)
	must(t, dir, nil, "git", "init", "-q", repo)
	for _, kv := range [][2]string{
		{"user.name", "Release"}, {"user.email", "release@test.invalid"},
		{"gpg.format", "ssh"}, {"user.signingKey", key},
	} {
		must(t, repo, nil, "git", "config", kv[0], kv[1])
	}
	write(t, filepath.Join(repo, "file"), "content of "+name+"\n")
	must(t, repo, nil, "git", "add", "file")
	must(t, repo, nil, "git", "commit", "-q", "-m", "a release")
	return repo
}

// rev is git rev-parse, with replacement refs honoured as git's default.
func rev(t *testing.T, repo, name string) string {
	t.Helper()
	return strings.TrimSpace(must(t, repo, nil, "git", "rev-parse", name))
}

// craftTag writes a tag object holding exactly text and signature, which
// git tag would not make, and points refs/tags/name at it.
func craftTag(t *testing.T, repo, name, text string, signature []byte) string {
	t.Helper()
	path := write(t, filepath.Join(t.TempDir(), "tag"), text+string(signature))
	id := strings.TrimSpace(must(t, repo, nil, "git", "hash-object", "-t", "tag", "-w", "--literally", path))
	must(t, repo, nil, "git", "update-ref", "refs/tags/"+name, id)
	return id
}

func TestGitTag(t *testing.T) {
	dir := t.TempDir()
	key := keygen(t, dir, "release", nil, "-t", "ed25519")
	const principal = "release@test.invalid"
	allowed := allowedSigners(t, dir, principal+" "+pub(t, key))
	repo := gitRepo(t, dir, "repo", key)
	head := rev(t, repo, "HEAD")

	// The message holds a line that opens like a signature. git takes the
	// last such line as the signature's start, and so must this.
	must(t, repo, nil, "git", "tag", "-s", "v1.0.0", "-m",
		"release 1.0.0\n\n-----BEGIN PGP SIGNATURE-----\nnot a signature\n")
	if out, ok := sh(t, repo, "", nil, "git", "-c", "gpg.ssh.allowedSignersFile="+allowed, "verify-tag", "v1.0.0"); !ok {
		t.Fatalf("git verify-tag refused:\n%s", out)
	}

	tag := func(name string, extra ...string) (string, string, int) {
		return cli(t, "", append([]string{"git-tag", "--repo", repo, "--tag", name,
			"--allowed-signers", allowed, "--principal", principal, "--allow-non-sk"}, extra...)...)
	}

	out, errOut, code := tag("v1.0.0")
	if code != exitVerified {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
	want := "verified\nnamespace git\nprincipal " + principal + "\nclass software\nkey-type ssh-ed25519\n" +
		"fingerprint " + field(out, "fingerprint") + "\ntag v1.0.0\ncommit " + head + "\n"
	if out != want {
		t.Errorf("report:\n%s\nwant:\n%s", out, want)
	}
	if _, _, code := tag("refs/tags/v1.0.0"); code != exitVerified {
		t.Errorf("a full ref name: exit %d", code)
	}

	out, errOut, code = cli(t, "", "git-tag", "--repo", repo, "--tag", "v1.0.0", "--allowed-signers", allowed, "--principal", principal)
	refused(t, "without --allow-non-sk", "class-not-allowed", out, errOut, code)

	// A signed tag object published under another name. git verify-tag
	// is content with it; a release sequence must not be.
	tagObject := rev(t, repo, "refs/tags/v1.0.0")
	must(t, repo, nil, "git", "update-ref", "refs/tags/v2.0.0", tagObject)
	out, errOut, code = tag("v2.0.0")
	refused(t, "a renamed tag", "tag-name-mismatch", out, errOut, code)

	must(t, repo, nil, "git", "tag", "lightweight")
	out, errOut, code = tag("lightweight")
	refused(t, "a lightweight tag", "unsigned", out, errOut, code)
	must(t, repo, nil, "git", "tag", "-a", "annotated", "-m", "not signed")
	out, errOut, code = tag("annotated")
	refused(t, "an unsigned annotated tag", "unsigned", out, errOut, code)

	// Signed tags that point at something other than a commit.
	blob := strings.TrimSpace(must(t, repo, nil, "git", "hash-object", "-w", "file"))
	must(t, repo, nil, "git", "tag", "-s", "nested", "v1.0.0", "-m", "a tag of a tag")
	must(t, repo, nil, "git", "tag", "-s", "tree", "HEAD^{tree}", "-m", "a tag of a tree")
	must(t, repo, nil, "git", "tag", "-s", "blob", blob, "-m", "a tag of a blob")
	for _, name := range []string{"nested", "tree", "blob"} {
		out, errOut, code = tag(name)
		refused(t, "a tag of a "+name, "target-not-commit", out, errOut, code)
	}

	// Tags git would not write, signed all the same: a commit that is not
	// there, and a tree that the tag calls a commit.
	sign := func(text string) []byte {
		sig := sshSign(t, dir, nil, key, "git", text)
		b, err := os.ReadFile(sig)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	tagText := func(object, name string) string {
		return "object " + object + "\ntype commit\ntag " + name + "\ntagger Release <release@test.invalid> 1 +0000\n\ncrafted\n"
	}
	missing := strings.Repeat("0", len(head)-1) + "1"
	craftTag(t, repo, "missing", tagText(missing, "missing"), sign(tagText(missing, "missing")))
	out, errOut, code = tag("missing")
	refused(t, "a tag of a missing commit", "target-missing", out, errOut, code)
	tree := rev(t, repo, "HEAD^{tree}")
	craftTag(t, repo, "liar", tagText(tree, "liar"), sign(tagText(tree, "liar")))
	out, errOut, code = tag("liar")
	refused(t, "a tag calling a tree a commit", "target-not-commit", out, errOut, code)

	// A signed tag whose target was changed afterwards. The refusal must
	// not repeat the target: it is exactly what nobody signed.
	raw := must(t, repo, nil, "git", "cat-file", "tag", "v1.0.0")
	altered := strings.Replace(raw, "tag v1.0.0", "tag altered", 1)
	altered = strings.Replace(altered, head, missing, 1)
	payload, signature, _ := strings.Cut(altered, "-----BEGIN SSH SIGNATURE-----")
	craftTag(t, repo, "altered", payload, []byte("-----BEGIN SSH SIGNATURE-----"+signature))
	out, errOut, code = tag("altered")
	refused(t, "an altered target", "bad-signature", out, errOut, code)
	if strings.Contains(out+errOut, missing) {
		t.Errorf("the refusal repeats the unsigned target:\n%s%s", out, errOut)
	}

	// A name git would resolve elsewhere is not a tag. git rev-parse finds
	// v3.0.0 under refs/remotes; git-tag looks only in refs/tags.
	must(t, repo, nil, "git", "update-ref", "refs/remotes/v3.0.0", tagObject)
	if out, errOut, code := tag("v3.0.0"); code != exitError || out != "" {
		t.Errorf("a ref outside refs/tags: exit %d:\n%s%s", code, out, errOut)
	}
	if out, errOut, code := tag("nope"); code != exitError || out != "" {
		t.Errorf("a missing tag: exit %d:\n%s%s", code, out, errOut)
	}
}

// git replacement refs make one object stand in for another wherever git
// reads it. sigverify reads the objects themselves.
func TestGitTagIgnoresReplacements(t *testing.T) {
	dir := t.TempDir()
	key := keygen(t, dir, "release", nil, "-t", "ed25519")
	const principal = "release@test.invalid"
	allowed := allowedSigners(t, dir, principal+" "+pub(t, key))
	repo := gitRepo(t, dir, "repo", key)
	head := rev(t, repo, "HEAD")
	must(t, repo, nil, "git", "tag", "-s", "v1", "-m", "release 1")
	tagObject := rev(t, repo, "refs/tags/v1")
	tag := func() (string, string, int) {
		return cli(t, "", "git-tag", "--repo", repo, "--tag", "v1",
			"--allowed-signers", allowed, "--principal", principal, "--allow-non-sk")
	}

	// The signed commit, replaced by a tree. Through git's default
	// reading it is no longer a commit at all.
	must(t, repo, nil, "git", "update-ref", "refs/replace/"+head, rev(t, repo, "HEAD^{tree}"))
	if kind := strings.TrimSpace(must(t, repo, nil, "git", "cat-file", "-t", head)); kind != "tree" {
		t.Fatalf("the replacement did not take: git reads %s as a %s", head, kind)
	}
	if out, errOut, code := tag(); code != exitVerified || field(out, "commit") != head {
		t.Errorf("a replaced commit: exit %d:\n%s%s", code, out, errOut)
	}

	// The signed tag object itself, replaced by an unsigned one.
	must(t, repo, nil, "git", "tag", "-a", "decoy", "-m", "not signed")
	must(t, repo, nil, "git", "update-ref", "refs/replace/"+tagObject, rev(t, repo, "refs/tags/decoy"))
	if text := must(t, repo, nil, "git", "cat-file", "tag", tagObject); !strings.Contains(text, "not signed") {
		t.Fatalf("the replacement did not take:\n%s", text)
	}
	if out, errOut, code := tag(); code != exitVerified || field(out, "commit") != head {
		t.Errorf("a replaced tag object: exit %d:\n%s%s", code, out, errOut)
	}
}

// The caller's GIT_* environment does not reach git: GIT_DIR here would
// otherwise send every read to another repository.
func TestGitTagIgnoresTheGitEnvironment(t *testing.T) {
	dir := t.TempDir()
	key := keygen(t, dir, "release", nil, "-t", "ed25519")
	const principal = "release@test.invalid"
	allowed := allowedSigners(t, dir, principal+" "+pub(t, key))
	repo := gitRepo(t, dir, "repo", key)
	must(t, repo, nil, "git", "tag", "-s", "v1", "-m", "release 1")
	decoy := gitRepo(t, dir, "decoy", key)
	must(t, decoy, nil, "git", "tag", "-s", "v1", "-m", "the decoy's release 1")

	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.useReplaceRefs'='true'")
	out, errOut, code := cli(t, "", "git-tag", "--repo", repo, "--tag", "v1",
		"--allowed-signers", allowed, "--principal", principal, "--allow-non-sk")
	os.Unsetenv("GIT_DIR")
	if code != exitVerified || field(out, "commit") != rev(t, repo, "HEAD") {
		t.Errorf("exit %d, commit %s, want %s:\n%s%s", code, field(out, "commit"), rev(t, repo, "HEAD"), out, errOut)
	}
}

// git verify-tag accepts a security-key tag nobody touched; sigverify
// git-tag does not.
func TestGitTagWithSecurityKeys(t *testing.T) {
	provider := skProvider(t)
	t.Setenv("SSH_SK_PROVIDER", provider)
	dir := t.TempDir()
	key := keygen(t, dir, "release", nil, "-t", "ed25519-sk")
	const principal = "release@test.invalid"
	allowed := allowedSigners(t, dir, principal+" "+pub(t, key))
	repo := gitRepo(t, dir, "repo", key)
	tag := func(name string) (string, string, int) {
		return cli(t, "", "git-tag", "--repo", repo, "--tag", name, "--allowed-signers", allowed, "--principal", principal)
	}

	must(t, repo, nil, "git", "tag", "-s", "v1.0.0", "-m", "touched")
	out, errOut, code := tag("v1.0.0")
	if code != exitVerified || field(out, "user-presence") != "yes" || field(out, "commit") != rev(t, repo, "HEAD") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}

	clearStubFlags(t, key, 0x01)
	must(t, repo, nil, "git", "tag", "-s", "v1.0.1", "-m", "untouched")
	ref, ok := sh(t, repo, "", nil, "git", "-c", "gpg.ssh.allowedSignersFile="+allowed, "verify-tag", "v1.0.1")
	upstreamAccepts(t, "git verify-tag", ok, ref)
	out, errOut, code = tag("v1.0.1")
	refused(t, "an untouched tag", "no-user-presence", out, errOut, code)
}

// A TKey signs with the stock signer app through ssh-agent, so what reaches
// the verifier is an ordinary ssh-ed25519 signature from ssh-keygen -Y
// sign. This key stands in for one. The TKey's guarantees, a touch per
// signature and a key that exists only under that app, live in the device
// and in which public key the line pins, so a software key exercises
// everything the verifier does with the class.
func TestTKeyClass(t *testing.T) {
	dir := t.TempDir()
	const message = "rotate the release signers\n"
	tkey := keygen(t, dir, "tkey", nil, "-t", "ed25519")
	plain := keygen(t, dir, "plain", nil, "-t", "ed25519")
	allowed := allowedSigners(t, dir,
		`root@valley.invalid tkey-signer="1.0.0" `+pub(t, tkey),
		`root@valley.invalid `+pub(t, plain))
	verify := func(key string, extra ...string) (string, string, int) {
		return cli(t, message, append([]string{"verify", "--namespace", "trust-root",
			"--signature", sshSign(t, dir, nil, key, "trust-root", message),
			"--allowed-signers", allowed, "--principal", "root@valley.invalid"}, extra...)...)
	}

	out, errOut, code := verify(tkey, "--allow-class", "tkey-signer")
	if code != exitVerified || field(out, "class") != "tkey-signer" || field(out, "signer-app") != "1.0.0" || field(out, "flags") != "" {
		t.Errorf("a TKey with its class allowed: exit %d:\n%s%s", code, out, errOut)
	}

	out, errOut, code = verify(tkey)
	refused(t, "a TKey by default", "class-not-allowed", out, errOut, code)
	out, errOut, code = verify(tkey, "--allow-non-sk")
	refused(t, "a TKey with software keys allowed", "class-not-allowed", out, errOut, code)
	out, errOut, code = verify(tkey, "--allow-class", "tkey-signer", "--require-uv")
	refused(t, "a TKey when UV is required", "no-user-verification", out, errOut, code)
	out, errOut, code = verify(plain, "--allow-class", "tkey-signer")
	refused(t, "an unmarked key where only a TKey will do", "class-not-allowed", out, errOut, code)
	out, errOut, code = verify(plain, "--allow-class", "tkey-signer", "--allow-non-sk")
	if code != exitVerified || field(out, "class") != "software" {
		t.Errorf("an unmarked key with software allowed is a software key: exit %d:\n%s%s", code, out, errOut)
	}
}

// failingWriter is a stdout that cannot be written.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// Every refusal exits 1 with its reason alone on stdout, every failure to
// verify at all exits 2 with nothing on stdout, and a report that cannot
// be written is a failure. These use forged signatures, so they need no
// authenticator.
func TestVerifyExitStatus(t *testing.T) {
	dir := t.TempDir()
	key := skforge.Ed25519SK("sigverify cli test")
	const message = "approve v1\n"
	const principal = "release@test.invalid"
	allowed := write(t, filepath.Join(dir, "allowed"), principal+` valid-before="20300101Z" `+key.PublicLine()+"\n")
	touched := write(t, filepath.Join(dir, "touched.sig"), string(key.Sign([]byte(message), "approval", 0x01)))
	untouched := write(t, filepath.Join(dir, "untouched.sig"), string(key.Sign([]byte(message), "approval", 0x00)))
	args := func(sig string, extra ...string) []string {
		return append([]string{"verify", "--namespace", "approval", "--signature", sig, "--allowed-signers", allowed,
			"--verify-time", "20291231Z"}, extra...)
	}

	out, errOut, code := cli(t, message, args(touched, "--principal", principal)...)
	want := "verified\nnamespace approval\nprincipal " + principal + "\nclass fido-sk\nkey-type sk-ssh-ed25519@openssh.com\n" +
		"fingerprint " + field(out, "fingerprint") + "\nflags 0x01\nuser-presence yes\n" +
		"user-verification no\ncounter 305419896\n"
	if code != exitVerified || out != want || !strings.HasPrefix(field(out, "fingerprint"), "SHA256:") {
		t.Errorf("exit %d:\n%s%s", code, out, errOut)
	}
	if out, _, code := cli(t, message, args(touched, "--any-principal")...); code != exitVerified || field(out, "principal") != principal {
		t.Errorf("--any-principal: exit %d:\n%s", code, out)
	}

	for _, c := range []struct {
		name   string
		stdin  string
		args   []string
		reason string
	}{
		{"untouched", message, args(untouched, "--principal", principal), "no-user-presence"},
		{"UV required", message, args(touched, "--principal", principal, "--require-uv"), "no-user-verification"},
		{"expired", message, append(args(touched, "--principal", principal), "--verify-time", "20300102Z"), "expired"},
		{"tampered", message + "!", args(touched, "--principal", principal), "bad-signature"},
		{"another principal", message, args(touched, "--principal", "x@test.invalid"), "principal-not-allowed"},
		{"only TKeys accepted", message, args(touched, "--principal", principal, "--allow-class", "tkey-signer"), "class-not-allowed"},
	} {
		out, errOut, code := cli(t, c.stdin, c.args...)
		refused(t, c.name, c.reason, out, errOut, code)
		if !strings.HasPrefix(errOut, "sigverify: refused ("+c.reason+"): ") {
			t.Errorf("%s: stderr %q", c.name, errOut)
		}
	}

	noTouch := write(t, filepath.Join(dir, "no-touch"), principal+" no-touch-required "+key.PublicLine()+"\n")
	for name, a := range map[string][]string{
		"no principal":                 args(touched),
		"both principal flags":         args(touched, "--principal", principal, "--any-principal"),
		"an unknown class":             args(touched, "--principal", principal, "--allow-class", "yubikey"),
		"no namespace":                 {"verify", "--signature", touched, "--allowed-signers", allowed, "--principal", principal},
		"no allowed signers":           {"verify", "--namespace", "approval", "--signature", touched, "--principal", principal},
		"a missing allowed signers":    {"verify", "--namespace", "approval", "--signature", touched, "--allowed-signers", filepath.Join(dir, "absent"), "--principal", principal},
		"no-touch-required in signers": {"verify", "--namespace", "approval", "--signature", touched, "--allowed-signers", noTouch, "--principal", principal},
		"a missing signature":          {"verify", "--namespace", "approval", "--signature", filepath.Join(dir, "absent"), "--allowed-signers", allowed, "--principal", principal},
		"a bad verify time":            args(touched, "--principal", principal, "--verify-time", "tomorrow"),
		"an unknown command":           {"sign"},
		"an unknown flag":              {"verify", "--no-touch-required"},
	} {
		if out, errOut, code := cli(t, message, a...); code != exitError || out != "" {
			t.Errorf("%s: exit %d:\n%s%s", name, code, out, errOut)
		}
	}

	// A report that cannot be written is an error, whatever the verdict:
	// a full disk must never turn into exit status 0.
	var errOut2 bytes.Buffer
	if code := run(args(touched, "--principal", principal), strings.NewReader(message), failingWriter{}, &errOut2); code != exitError {
		t.Errorf("a verified report that could not be written: exit %d", code)
	}
	if code := run(args(untouched, "--principal", principal), strings.NewReader(message), failingWriter{}, &errOut2); code != exitError {
		t.Errorf("a refusal that could not be written: exit %d", code)
	}
	if full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0); err == nil {
		defer full.Close()
		if code := run(args(touched, "--principal", principal), strings.NewReader(message), full, &errOut2); code != exitError {
			t.Errorf("a report written to /dev/full: exit %d", code)
		}
	}
}
