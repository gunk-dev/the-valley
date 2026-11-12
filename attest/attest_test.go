package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/note/statement"
)

// The note envelope and the keys it is signed with are the pieces of
// genuinely tricky logic here: they decide what a signature is checked
// against. The written form and the tree digest are note/statement's, and
// its tests hold them. Everything else in this program is exercised end to
// end by the flake checks against the real binary.

// The key hash is the note format's own, and getting it wrong means
// signing notes nothing else can find the key for. sum.golang.org
// publishes its verifier key, so its name and public key are an input the
// reference implementation has already computed the answer for.
func TestKeyHashMatchesTheReferenceImplementation(t *testing.T) {
	const (
		name   = "sum.golang.org"
		public = "Ac4zctda0e5eza+HJyk9SxEdh+s3Ux18htTTAD8OuAn8"
		want   = "033de0ae"
	)
	raw, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal(err)
	}
	if got := keyHash(name, raw[1:]); hex8(got) != want {
		t.Errorf("keyHash(%s) = %s, want %s", name, hex8(got), want)
	}
	// And the same fact from the other side: the published verifier key
	// parses, which checks its hash against the one this code computes.
	if _, err := parseVerifierKey(name + "+" + want + "+" + public); err != nil {
		t.Errorf("the published verifier key of %s does not parse: %v", name, err)
	}
}

func TestNoteCarriesSiblingSignatures(t *testing.T) {
	first := testSigner(t, "one.example/attestations")
	second := testSigner(t, "two.example/attestations")
	text := []byte(statementType + "\nsubject.primary valley-tree-v1\n")

	note, err := signNote(text, first)
	if err != nil {
		t.Fatal(err)
	}
	if note, err = addSignature(note, second); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(note), "\n— "); lines != 2 {
		t.Errorf("note does not carry two sibling signature lines:\n%s", note)
	}

	known := []verifierKey{first.verifierKey(), second.verifierKey()}
	got, sigs, err := openNote(note, known)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(text) {
		t.Errorf("openNote returned %q, want %q", got, text)
	}
	if len(sigs) != 2 || !sigs[0].verified || !sigs[1].verified {
		t.Errorf("both signatures should verify over one text: %+v", sigs)
	}

	// A verifier holding one of the two keys accepts the note and reports
	// the other signature as one it cannot speak to.
	_, sigs, err = openNote(note, known[:1])
	if err != nil {
		t.Fatal(err)
	}
	if !sigs[0].verified || sigs[1].known {
		t.Errorf("a signature by an unheld key is unknown, not bad: %+v", sigs)
	}

	// A signature cannot be lifted onto another statement: it covers the
	// text it sits under.
	moved := strings.Replace(string(note), "valley-tree-v1\n", "valley-tree-v2\n", 1)
	if _, _, err := openNote([]byte(moved), known); err == nil {
		t.Error("a signature verified over text it was not made over")
	}

	// One good signature standing beside a forgery is not a note to read
	// past.
	forged := flipLastSignatureByte(t, string(note))
	if _, _, err := openNote([]byte(forged), known); err == nil {
		t.Error("a note with one bad signature by a known key must be refused")
	}
}

func TestOpenSSHKeyIsReadAsSshKeygenWroteIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "host")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t", "-f", path).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen is not available: %v: %s", err, out)
	}
	s, err := loadSigner(path, "host.example/attestations")
	if err != nil {
		t.Fatal(err)
	}
	// The public half ssh-keygen wrote beside it is the public half this
	// parse produced, or the key was read wrong in a way that only shows
	// up as signatures nobody can check.
	pub, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	blob, err := base64.StdEncoding.DecodeString(strings.Fields(string(pub))[1])
	if err != nil {
		t.Fatal(err)
	}
	want := blob[len(blob)-ed25519.PublicKeySize:]
	if got := s.priv.Public().(ed25519.PublicKey); string(got) != string(want) {
		t.Errorf("parsed public key %x, want %x", got, want)
	}
}

func TestInputsManifestSortsAndDeduplicates(t *testing.T) {
	want := "valley-inputs-v1\n1 a\n1 b\n"
	if got := string(inputsManifest([]string{"b", "a", "b"})); got != want {
		t.Errorf("inputsManifest = %q, want %q", got, want)
	}
}

// The digest of a committed tree must equal the digest of that tree
// written to disk — the two enumerators are the same scheme or the check
// output digest and the subject digest mean different things.
func TestGitTreeAndFilesystemAgree(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@l",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@l")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string, mode os.FileMode) {
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "--quiet", "-b", "main")
	write("plain.txt", "hello\n", 0o644)
	write("nested/dir/script.sh", "#!/bin/sh\n", 0o755)
	if err := os.Symlink("plain.txt", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "seed")

	entries, err := gitTreeEntries(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	fromGit := statement.TreeDigest(entries)

	export := t.TempDir()
	if err := exportTree(repo, "HEAD", export); err != nil {
		t.Fatal(err)
	}
	fsEnts, err := fsEntries(export)
	if err != nil {
		t.Fatal(err)
	}
	if fromFS := statement.TreeDigest(fsEnts); fromFS != fromGit {
		t.Errorf("git tree digest %s != filesystem digest %s\n git: %s\n  fs: %s",
			fromGit, fromFS, statement.TreeManifest(entries), statement.TreeManifest(fsEnts))
	}

	// The exec bit and the symlink are the two entries a naive walk gets
	// wrong, so pin that they are actually present.
	modes := map[string]string{}
	for _, e := range entries {
		modes[e.Path] = e.Mode
	}
	for path, want := range map[string]string{
		"plain.txt":            statement.ModeFile,
		"nested/dir/script.sh": statement.ModeExec,
		"link":                 statement.ModeSymlink,
	} {
		if modes[path] != want {
			t.Errorf("%s has mode %q, want %q", path, modes[path], want)
		}
	}
}

func hex8(v uint32) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[v&0xf]
		v >>= 4
	}
	return string(out)
}

func testSigner(t *testing.T, name string) signer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return signer{name: name, priv: priv, hash: keyHash(name, pub)}
}

// flipLastSignatureByte changes one byte of the last signature line's
// signature, leaving the key hash in front of it alone. That is what a
// forgery by a key the verifier holds looks like — as opposed to a
// signature it simply has no key for.
func flipLastSignatureByte(t *testing.T, note string) string {
	t.Helper()
	body := strings.TrimSuffix(note, "\n")
	head, last := "", body
	if cut := strings.LastIndex(body, "\n"); cut >= 0 {
		head, last = body[:cut+1], body[cut+1:]
	}
	cut := strings.LastIndex(last, " ")
	if cut < 0 {
		t.Fatalf("no signature to perturb in %q", note)
	}
	blob, err := base64.StdEncoding.DecodeString(last[cut+1:])
	if err != nil || len(blob) < 5 {
		t.Fatalf("no signature to perturb in %q: %v", note, err)
	}
	blob[len(blob)-1] ^= 1
	return head + last[:cut+1] + base64.StdEncoding.EncodeToString(blob) + "\n"
}
