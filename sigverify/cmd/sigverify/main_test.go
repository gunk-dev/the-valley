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

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
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
	for _, v := range []string{"SSH_AUTH_SOCK", "SSH_SK_PROVIDER", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
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

// allowedSigners writes an allowed-signers file listing each key's public
// half under a principal, with options if any.
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

// sshSign signs message with ssh-keygen -Y sign and returns the signature
// file.
func sshSign(t *testing.T, dir string, env []string, key, namespace, message string, opts ...string) string {
	t.Helper()
	msg := filepath.Join(dir, "message")
	if err := os.WriteFile(msg, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
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

			ref, ok := sshVerify(t, dir, allowed, "signer@test.invalid", "file", sig, message)
			if !ok {
				t.Fatalf("ssh-keygen refused its own signature:\n%s", ref)
			}
			m := goodSignature.FindStringSubmatch(ref)
			if m == nil {
				t.Fatalf("unexpected ssh-keygen output:\n%s", ref)
			}

			out, errOut, code := cli(t, message, "verify", "--namespace", "file", "--signature", sig,
				"--allowed-signers", allowed, "--allow-non-sk")
			if code != exitVerified {
				t.Fatalf("exit %d\n%s%s", code, out, errOut)
			}
			if field(out, "principal") != m[1] || field(out, "fingerprint") != m[2] {
				t.Errorf("ssh-keygen says %s %s; sigverify says\n%s", m[1], m[2], out)
			}
			if field(out, "security-key") != "no" {
				t.Errorf("a software key reported as a security key:\n%s", out)
			}

			// Both refuse the same signature over a changed message.
			if ref, ok := sshVerify(t, dir, allowed, "signer@test.invalid", "file", sig, message+"!"); ok {
				t.Fatalf("ssh-keygen accepted a changed message:\n%s", ref)
			}
			out, _, code = cli(t, message+"!", "verify", "--namespace", "file", "--signature", sig,
				"--allowed-signers", allowed, "--allow-non-sk")
			if code != exitRefused || !strings.HasPrefix(out, "refused bad-signature\n") {
				t.Errorf("exit %d for a changed message:\n%s", code, out)
			}

			// And a software key is refused outright unless admitted.
			out, _, code = cli(t, message, "verify", "--namespace", "file", "--signature", sig, "--allowed-signers", allowed)
			if code != exitRefused || !strings.HasPrefix(out, "refused not-security-key\n") {
				t.Errorf("exit %d without --allow-non-sk:\n%s", code, out)
			}
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

// The gap this verifier closes. ssh-keygen -Y verify accepts each of these
// security-key signatures; sigverify refuses the ones nobody touched.
func TestSecurityKeysWithSSHKeygen(t *testing.T) {
	provider := skProvider(t)
	env := []string{"SSH_SK_PROVIDER=" + provider}
	dir := t.TempDir()
	const message = "release v1\n"

	verify := func(t *testing.T, key string, sig string, extra ...string) (string, int) {
		t.Helper()
		allowed := allowedSigners(t, dir, "release@test.invalid "+pub(t, key))
		if ref, ok := sshVerify(t, dir, allowed, "release@test.invalid", "git", sig, message); !ok {
			t.Fatalf("ssh-keygen refused:\n%s", ref)
		}
		out, errOut, code := cli(t, message, append([]string{"verify", "--namespace", "git",
			"--signature", sig, "--allowed-signers", allowed}, extra...)...)
		return out + errOut, code
	}

	t.Run("a touched ed25519-sk signature", func(t *testing.T) {
		key := keygen(t, dir, "touched", env, "-t", "ed25519-sk")
		out, code := verify(t, key, sshSign(t, dir, env, key, "git", message))
		if code != exitVerified || field(out, "flags") != "0x01" || field(out, "user-presence") != "yes" {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		// UV was not asked for, so requiring it refuses.
		out, code = verify(t, key, sshSign(t, dir, env, key, "git", message), "--require-uv")
		if code != exitRefused || !strings.HasPrefix(out, "refused no-user-verification\n") {
			t.Fatalf("exit %d with --require-uv:\n%s", code, out)
		}
	})

	t.Run("a touched ecdsa-sk signature", func(t *testing.T) {
		key := keygen(t, dir, "touched-ecdsa", env, "-t", "ecdsa-sk")
		out, code := verify(t, key, sshSign(t, dir, env, key, "git", message))
		if code != exitVerified || field(out, "key-type") != "sk-ecdsa-sha2-nistp256@openssh.com" {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	t.Run("a key generated with no-touch-required", func(t *testing.T) {
		key := keygen(t, dir, "notouch", env, "-t", "ed25519-sk", "-O", "no-touch-required")
		out, code := verify(t, key, sshSign(t, dir, env, key, "git", message))
		if code != exitRefused || !strings.HasPrefix(out, "refused no-user-presence\n") || field(out, "flags") != "0x00" {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})

	// The attack the verifier exists for. Anything running as the key's
	// owner can rewrite the key stub on disk so that ssh-keygen stops
	// asking the authenticator for a touch. (OpenSSH 10.5 does it with
	// `ssh-keygen -p -O no-touch-required`; this works on every version.)
	// The key and its allowed-signers line do not change, and ssh-keygen
	// still calls the signature good.
	t.Run("a stub flipped to no-touch-required", func(t *testing.T) {
		key := keygen(t, dir, "flipped", env, "-t", "ed25519-sk")
		before, code := verify(t, key, sshSign(t, dir, env, key, "git", message))
		if code != exitVerified {
			t.Fatalf("before the flip, exit %d:\n%s", code, before)
		}
		clearTouchRequired(t, key)
		out, code := verify(t, key, sshSign(t, dir, env, key, "git", message))
		if code != exitRefused || !strings.HasPrefix(out, "refused no-user-presence\n") {
			t.Fatalf("after the flip, exit %d:\n%s", code, out)
		}
		if field(out, "fingerprint") != field(before, "fingerprint") {
			t.Errorf("the flip changed the key:\n%s\n%s", before, out)
		}
	})

	t.Run("a key generated with verify-required", func(t *testing.T) {
		askpass := filepath.Join(dir, "askpass")
		if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho 1234\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		pinEnv := append(env, "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force")
		key := keygen(t, dir, "uv", pinEnv, "-t", "ed25519-sk", "-O", "verify-required")
		out, code := verify(t, key, sshSign(t, dir, pinEnv, key, "git", message), "--require-uv")
		if code != exitVerified || field(out, "flags") != "0x05" || field(out, "user-verification") != "yes" {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}

// clearTouchRequired clears the user-presence-required flag in an
// unencrypted OpenSSH security-key stub, leaving the key itself unchanged.
func clearTouchRequired(t *testing.T, path string) {
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
	// key, then the private section, whose sk-ed25519 entry is: two check
	// integers, type, public key, application, flags.
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
	if typ := skip(); typ != "sk-ssh-ed25519@openssh.com" {
		t.Fatalf("the stub holds a %s key", typ)
	}
	skip() // public key
	skip() // application
	if blob[off]&0x01 == 0 {
		t.Fatal("the stub already has no-touch-required")
	}
	blob[off] &^= 0x01

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
func gitRepo(t *testing.T, dir, key string) string {
	t.Helper()
	repo := filepath.Join(dir, "repo")
	must(t, dir, nil, "git", "init", "-q", repo)
	for _, kv := range [][2]string{
		{"user.name", "Release"}, {"user.email", "release@test.invalid"},
		{"gpg.format", "ssh"}, {"user.signingKey", key},
	} {
		must(t, repo, nil, "git", "config", kv[0], kv[1])
	}
	if err := os.WriteFile(filepath.Join(repo, "file"), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, repo, nil, "git", "add", "file")
	must(t, repo, nil, "git", "commit", "-q", "-m", "a release")
	return repo
}

func TestGitTag(t *testing.T) {
	dir := t.TempDir()
	key := keygen(t, dir, "release", nil, "-t", "ed25519")
	allowed := allowedSigners(t, dir, "release@test.invalid "+pub(t, key))
	repo := gitRepo(t, dir, key)
	head := strings.TrimSpace(must(t, repo, nil, "git", "rev-parse", "HEAD"))

	// The message holds a line that opens like a signature. git takes the
	// last such line as the signature's start, and so must this.
	must(t, repo, nil, "git", "tag", "-s", "v1.0.0", "-m",
		"release 1.0.0\n\n-----BEGIN PGP SIGNATURE-----\nnot a signature\n")
	if out, ok := sh(t, repo, "", nil, "git", "-c", "gpg.ssh.allowedSignersFile="+allowed, "verify-tag", "v1.0.0"); !ok {
		t.Fatalf("git verify-tag refused:\n%s", out)
	}

	tag := func(name string, extra ...string) (string, int) {
		out, errOut, code := cli(t, "", append([]string{"git-tag", "--repo", repo, "--tag", name, "--allowed-signers", allowed}, extra...)...)
		return out + errOut, code
	}

	out, code := tag("v1.0.0", "--allow-non-sk")
	if code != exitVerified {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for name, want := range map[string]string{
		"namespace": "git", "principal": "release@test.invalid",
		"tag": "v1.0.0", "object": head, "object-type": "commit",
	} {
		if got := field(out, name); got != want {
			t.Errorf("%s is %q, want %q:\n%s", name, got, want, out)
		}
	}
	if out2, code := tag("refs/tags/v1.0.0", "--allow-non-sk"); code != exitVerified {
		t.Errorf("a full ref name: exit %d:\n%s", code, out2)
	}

	// The same tag is refused when only security keys are accepted.
	if out, code := tag("v1.0.0"); code != exitRefused || !strings.HasPrefix(out, "refused not-security-key\n") {
		t.Errorf("without --allow-non-sk: exit %d:\n%s", code, out)
	}

	// A signed tag object published under another name. git verify-tag
	// is content with it; a release sequence must not be.
	tagObject := strings.TrimSpace(must(t, repo, nil, "git", "rev-parse", "refs/tags/v1.0.0"))
	must(t, repo, nil, "git", "update-ref", "refs/tags/v2.0.0", tagObject)
	if out, ok := sh(t, repo, "", nil, "git", "-c", "gpg.ssh.allowedSignersFile="+allowed, "verify-tag", "v2.0.0"); !ok {
		t.Fatalf("git verify-tag refused the renamed tag:\n%s", out)
	}
	if out, code := tag("v2.0.0", "--allow-non-sk"); code != exitRefused || !strings.HasPrefix(out, "refused tag-name-mismatch\n") {
		t.Errorf("a renamed tag: exit %d:\n%s", code, out)
	}

	must(t, repo, nil, "git", "tag", "lightweight")
	if out, code := tag("lightweight", "--allow-non-sk"); code != exitRefused || !strings.HasPrefix(out, "refused unsigned\n") {
		t.Errorf("a lightweight tag: exit %d:\n%s", code, out)
	}
	must(t, repo, nil, "git", "tag", "-a", "annotated", "-m", "not signed")
	if out, code := tag("annotated", "--allow-non-sk"); code != exitRefused || !strings.HasPrefix(out, "refused unsigned\n") {
		t.Errorf("an unsigned annotated tag: exit %d:\n%s", code, out)
	}

	// A name git would resolve elsewhere is not a tag. git rev-parse
	// finds v3.0.0 under refs/remotes; git-tag looks only in refs/tags.
	must(t, repo, nil, "git", "update-ref", "refs/remotes/v3.0.0", tagObject)
	if out, code := tag("v3.0.0", "--allow-non-sk"); code != exitError {
		t.Errorf("a ref outside refs/tags: exit %d:\n%s", code, out)
	}
	if out, code := tag("nope", "--allow-non-sk"); code != exitError {
		t.Errorf("a missing tag: exit %d:\n%s", code, out)
	}
}

// git verify-tag accepts a security-key tag nobody touched; sigverify
// git-tag does not.
func TestGitTagWithSecurityKeys(t *testing.T) {
	provider := skProvider(t)
	t.Setenv("SSH_SK_PROVIDER", provider)
	dir := t.TempDir()
	key := keygen(t, dir, "release", nil, "-t", "ed25519-sk")
	allowed := allowedSigners(t, dir, "release@test.invalid "+pub(t, key))
	repo := gitRepo(t, dir, key)

	must(t, repo, nil, "git", "tag", "-s", "v1.0.0", "-m", "touched")
	out, errOut, code := cli(t, "", "git-tag", "--repo", repo, "--tag", "v1.0.0", "--allowed-signers", allowed)
	if code != exitVerified || field(out, "user-presence") != "yes" {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}

	clearTouchRequired(t, key)
	must(t, repo, nil, "git", "tag", "-s", "v1.0.1", "-m", "untouched")
	if out, ok := sh(t, repo, "", nil, "git", "-c", "gpg.ssh.allowedSignersFile="+allowed, "verify-tag", "v1.0.1"); !ok {
		t.Fatalf("git verify-tag refused the untouched tag, so the gap is closed upstream:\n%s", out)
	}
	out, errOut, code = cli(t, "", "git-tag", "--repo", repo, "--tag", "v1.0.1", "--allowed-signers", allowed)
	if code != exitRefused || !strings.HasPrefix(out, "refused no-user-presence\n") {
		t.Fatalf("exit %d:\n%s%s", code, out, errOut)
	}
}

// Every refusal the package knows exits 1 and names its reason, and every
// failure to verify at all exits 2. These use forged signatures, so they
// need no authenticator.
func TestVerifyExitStatus(t *testing.T) {
	dir := t.TempDir()
	key := skforge.Ed25519SK("sigverify cli test")
	const message = "approve v1\n"
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	allowed := write("allowed", `release@test.invalid valid-before="20300101Z" `+key.PublicLine()+"\n")
	touched := write("touched.sig", string(key.Sign([]byte(message), "approval", 0x01)))
	untouched := write("untouched.sig", string(key.Sign([]byte(message), "approval", 0x00)))
	args := func(sig string, extra ...string) []string {
		return append([]string{"verify", "--namespace", "approval", "--signature", sig, "--allowed-signers", allowed}, extra...)
	}

	out, errOut, code := cli(t, message, args(touched, "--verify-time", "20291231Z")...)
	want := "verified\nnamespace approval\nprincipal release@test.invalid\nkey-type sk-ssh-ed25519@openssh.com\n" +
		"fingerprint " + field(out, "fingerprint") + "\nsecurity-key yes\nflags 0x01\nuser-presence yes\n" +
		"user-verification no\ncounter 305419896\n"
	if code != exitVerified || out != want || !strings.HasPrefix(field(out, "fingerprint"), "SHA256:") {
		t.Errorf("exit %d:\n%s%s", code, out, errOut)
	}

	for _, c := range []struct {
		name   string
		stdin  string
		args   []string
		reason string
	}{
		{"untouched", message, args(untouched, "--verify-time", "20291231Z"), "no-user-presence"},
		{"UV required", message, args(touched, "--verify-time", "20291231Z", "--require-uv"), "no-user-verification"},
		{"expired", message, args(touched, "--verify-time", "20300102Z"), "expired"},
		{"tampered", message + "!", args(touched, "--verify-time", "20291231Z"), "bad-signature"},
		{"another principal", message, args(touched, "--verify-time", "20291231Z", "--principal", "x@test.invalid"), "principal-not-allowed"},
	} {
		out, errOut, code := cli(t, c.stdin, c.args...)
		if code != exitRefused || !strings.HasPrefix(out, "refused "+c.reason+"\n") || !strings.HasPrefix(errOut, "sigverify: refused: ") {
			t.Errorf("%s: exit %d:\n%s%s", c.name, code, out, errOut)
		}
	}

	noTouch := write("no-touch", "release@test.invalid no-touch-required "+key.PublicLine()+"\n")
	for name, a := range map[string][]string{
		"no namespace":                 {"verify", "--signature", touched, "--allowed-signers", allowed},
		"no allowed signers":           {"verify", "--namespace", "approval", "--signature", touched},
		"a missing allowed signers":    {"verify", "--namespace", "approval", "--signature", touched, "--allowed-signers", filepath.Join(dir, "absent")},
		"no-touch-required in signers": {"verify", "--namespace", "approval", "--signature", touched, "--allowed-signers", noTouch},
		"a missing signature":          {"verify", "--namespace", "approval", "--signature", filepath.Join(dir, "absent"), "--allowed-signers", allowed},
		"a bad verify time":            args(touched, "--verify-time", "tomorrow"),
		"an unknown command":           {"sign"},
		"an unknown flag":              {"verify", "--no-touch-required"},
	} {
		if out, errOut, code := cli(t, message, a...); code != exitError {
			t.Errorf("%s: exit %d:\n%s%s", name, code, out, errOut)
		}
	}
}
