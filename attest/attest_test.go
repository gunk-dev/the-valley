package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The written form, the note envelope and the tree digest are the pieces
// of genuinely tricky logic here: they decide which bytes get signed, what
// a signature is checked against, and what a subject is. All three must
// agree with any other implementation. Everything else in this program is
// exercised end to end by the flake checks against the real binary.

func TestRenderTextIsLinesSortedByKey(t *testing.T) {
	got, err := renderText(map[string]any{
		"b": "two",
		"a": map[string]any{"z": "last", "y": []any{"1", "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := statementForm + "\n" +
		"a.y.0 1\n" +
		"a.y.1 2\n" +
		"a.z last\n" +
		"b two\n"
	if string(got) != want {
		t.Errorf("renderText =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderTextWritesValuesLiterally(t *testing.T) {
	// The form has no escapes, which is what leaves two implementations
	// nothing to disagree about. Every character below is one a JSON
	// encoder would have had a choice about.
	got, err := renderText(map[string]any{"k": `a<b>c&d "q" \ / ü😀`})
	if err != nil {
		t.Fatal(err)
	}
	want := statementForm + "\n" + `k a<b>c&d "q" \ / ü😀` + "\n"
	if string(got) != want {
		t.Errorf("renderText =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderTextRefusesWhatItCannotWrite(t *testing.T) {
	// Each of these is a document with no written form. Refusing beats
	// guessing, and beats dropping something that was validated a moment
	// earlier and would then not be in the bytes that got signed.
	for name, doc := range map[string]map[string]any{
		"a number":             {"n": json.Number("1.5")},
		"a boolean":            {"b": true},
		"a null":               {"n": nil},
		"an empty object":      {"provenance": map[string]any{}},
		"an empty array":       {"delegation": []any{}},
		"an empty string":      {"k": ""},
		"a newline in a value": {"k": "two\nlines"},
		"a tab in a value":     {"k": "a\tb"},
		"invalid utf-8":        {"k": "\xff"},
		"a key with a dot":     {"a.b": "x"},
		"a key with a space":   {"a b": "x"},
		"a non-ascii key":      {"café": "x"},
		"an index-shaped key":  {"0": "x"},
	} {
		if _, err := renderText(doc); err == nil {
			t.Errorf("renderText accepted %s", name)
		}
	}
}

func TestParseTextRoundTrips(t *testing.T) {
	doc := map[string]any{
		"statementType": statementType,
		"subject": map[string]any{
			"primary": treeScheme,
			"digest":  map[string]any{treeScheme: strings.Repeat("a", 64)},
		},
		"provenance": map[string]any{
			"delegation": []any{
				map[string]any{"principal": "human:integrator", "grant": "land changes"},
				map[string]any{"principal": "agent:run"},
			},
		},
	}
	text, err := renderText(doc)
	if err != nil {
		t.Fatal(err)
	}
	back, err := parseText(text)
	if err != nil {
		t.Fatalf("parseText(%q): %v", text, err)
	}
	if !reflect.DeepEqual(back, doc) {
		t.Errorf("parseText(renderText(doc)) =\n  %#v\nwant\n  %#v", back, doc)
	}
	// The form is a fixed point, or a statement written out again anywhere
	// downstream stops matching what was signed.
	again, err := renderText(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(text) {
		t.Errorf("rendering a parsed statement changed it:\n%q\n%q", again, text)
	}
}

func TestParseTextAcceptsOnlyTheWrittenForm(t *testing.T) {
	// Text that parses is text the renderer would have produced. That is
	// what lets a verifier settle "these bytes are the written form of what
	// they say" by parsing, rather than trusting the sender.
	for name, text := range map[string]string{
		"no form line":        "a b\n",
		"the wrong form":      "valley-statement-v2\na b\n",
		"no closing newline":  statementForm + "\na b",
		"a key with no value": statementForm + "\na\n",
		"a blank line":        statementForm + "\na b\n\nc d\n",
		"lines out of order":  statementForm + "\nb two\na one\n",
		"a repeated key":      statementForm + "\na one\na two\n",
		"a sparse array":      statementForm + "\np.0 one\np.2 three\n",
		"index and field":     statementForm + "\np.0 one\np.name two\n",
		"value and path":      statementForm + "\np one\np.q two\n",
		"a leading zero":      statementForm + "\np.00 one\n",
		"nothing at all":      statementForm + "\n",
	} {
		if _, err := parseText([]byte(text)); err == nil {
			t.Errorf("parseText accepted %s: %q", name, text)
		}
	}
}

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
	text := []byte(statementForm + "\nk v\n")

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
	moved := strings.Replace(string(note), "k v", "k w", 1)
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

func TestTreeManifestIsUnambiguous(t *testing.T) {
	// A path holding the manifest's own separators must not be able to
	// forge an entry boundary: the byte length is what delimits it.
	entries := []entry{
		{mode: modeFile, path: "b\n100644 x 1 c", sum: [32]byte{}},
		{mode: modeExec, path: "a", sum: [32]byte{0xff}},
	}
	want := "valley-tree-v1\n" +
		"100755 ff00000000000000000000000000000000000000000000000000000000000000 1 a\n" +
		"100644 0000000000000000000000000000000000000000000000000000000000000000 14 b\n100644 x 1 c\n"
	if got := string(treeManifest(entries)); got != want {
		t.Errorf("treeManifest =\n%q\nwant\n%q", got, want)
	}
}

func TestTreeDigestIsPinned(t *testing.T) {
	// The digest of a fixed tree is a wire format: changing it invalidates
	// every attestation ever made. This value may only change with the
	// scheme's version.
	entries := []entry{{mode: modeFile, path: "a.txt", sum: sha256of("hi\n")}}
	const want = "74781d4ba499b9896e97f2fdd38815d5723cb7b12e448ef0ce5788a1e72c2220"
	if got := treeDigest(entries); got != want {
		t.Errorf("treeDigest = %s, want %s", got, want)
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
	fromGit := treeDigest(entries)

	export := t.TempDir()
	if err := exportTree(repo, "HEAD", export); err != nil {
		t.Fatal(err)
	}
	fsEnts, err := fsEntries(export)
	if err != nil {
		t.Fatal(err)
	}
	if fromFS := treeDigest(fsEnts); fromFS != fromGit {
		t.Errorf("git tree digest %s != filesystem digest %s\n git: %s\n  fs: %s",
			fromGit, fromFS, treeManifest(entries), treeManifest(fsEnts))
	}

	// The exec bit and the symlink are the two entries a naive walk gets
	// wrong, so pin that they are actually present.
	modes := map[string]string{}
	for _, e := range entries {
		modes[e.path] = e.mode
	}
	for path, want := range map[string]string{
		"plain.txt":            modeFile,
		"nested/dir/script.sh": modeExec,
		"link":                 modeSymlink,
	} {
		if modes[path] != want {
			t.Errorf("%s has mode %q, want %q", path, modes[path], want)
		}
	}
}

func sha256of(s string) [32]byte {
	var out [32]byte
	copy(out[:], sha256sum([]byte(s)))
	return out
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
