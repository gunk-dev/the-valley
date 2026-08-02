package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Canonical form and the tree digest are the two pieces of genuinely
// tricky logic here: they decide which bytes get signed and what a subject
// is, and both must agree byte for byte with any other implementation.
// Everything else in this program is exercised end to end by the flake
// checks against the real binary.

func TestCanonicalJSONSortsAndTightens(t *testing.T) {
	got, err := canonicalJSON(map[string]any{
		"b": "two",
		"a": map[string]any{"z": "last", "y": []any{"1", "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"y":["1","2"],"z":"last"},"b":"two"}`
	if string(got) != want {
		t.Errorf("canonicalJSON =\n  %s\nwant\n  %s", got, want)
	}
}

func TestCanonicalJSONEscaping(t *testing.T) {
	// Go's encoding/json escapes <, > and & by default; RFC 8785 does not,
	// and a signer that escaped them would not match one that did not.
	got, err := canonicalJSON(map[string]any{"k": "a<b>c&d \"q\" \\ \n ü"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"k":"a<b>c&d \"q\" \\ \n ü"}`
	if string(got) != want {
		t.Errorf("canonicalJSON =\n  %s\nwant\n  %s", got, want)
	}
}

func TestCanonicalJSONRejectsNumbers(t *testing.T) {
	// A statement carries no numbers, so the canonical form of one has
	// never been decided. Refusing beats guessing.
	if _, err := canonicalize(map[string]any{"n": 1.5}); err == nil {
		t.Fatal("canonicalize accepted a number")
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
