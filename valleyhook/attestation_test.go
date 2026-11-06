package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"the-valley/note"
)

// An attestation ref takes added notes, and nothing else: every note it
// holds stays at its path, byte for byte, the tree it moves to is
// everything a created ref must be, and no push deletes it. Each update
// here is read from real git objects, as the hook reads a push.
func TestAnAttestationRefOnlyTakesAddedNotes(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "", "init", "--quiet", "--bare", dir)
	t.Setenv("GIT_DIR", dir)

	type signer struct {
		name string
		priv ed25519.PrivateKey
		key  note.VerifierKey
	}
	newSigner := func(name string) signer {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return signer{name, priv, note.NewVerifierKey(name, pub)}
	}
	worker, other := newSigner("worker.example.org/attestations"), newSigner("other.example.org/attestations")
	digest := strings.Repeat("d", 64)
	ref := fmt.Sprintf("refs/the-valley/attestations/%s/%08x", digest, worker.key.Hash)
	signed := func(s signer, check, result string) string {
		text := note.StatementType + "\nsubject.primary valley-tree-v1\nsubject.digest.valley-tree-v1 " + digest +
			"\npredicate.check.name " + check + "\npredicate.result " + result + "\n"
		n, err := note.Sign([]byte(text), s.name, s.priv)
		if err != nil {
			t.Fatal(err)
		}
		return gitIn(t, dir, string(n), "hash-object", "-w", "--stdin")
	}
	// tree writes an attestation's tree: one directory per check holding
	// its statement.note, and any further entries given as git mktree
	// reads them.
	tree := func(notes map[string]string, extra ...string) string {
		var lines []string
		for check, blob := range notes {
			leaf := mktree(t, dir, 1, "100644 blob "+blob, "statement.note")
			lines = append(lines, "040000 tree "+leaf+"\t"+check)
		}
		return gitIn(t, dir, strings.Join(append(lines, extra...), "\n")+"\n", "mktree")
	}
	one, two := signed(worker, "one", "passed"), signed(worker, "two", "passed")
	held := tree(map[string]string{"one": one})
	empty := gitIn(t, dir, "", "mktree")

	p := policy{project: "p", repo: gitRepository{}, verifiers: []note.VerifierKey{worker.key, other.key}}
	const zero = "0000000000000000000000000000000000000000"
	for _, c := range []struct {
		what, new string
		says      string
	}{
		{"a note added for another check", tree(map[string]string{"one": one, "two": two}), ""},
		{"a note changed", tree(map[string]string{"one": signed(worker, "one", "failed"), "two": two}), "changes the note at one/statement.note"},
		{"a note dropped", tree(map[string]string{"two": two}), "drops the note at one/statement.note"},
		{"a note added under another key", tree(map[string]string{"one": one, "two": signed(other, "two", "passed")}), "the note at two/statement.note carries no signature under the key hash"},
		{"another tree adding no note", tree(map[string]string{"one": one}, "040000 tree "+empty+"\tnothing"), "adds no note"},
		{"a note added past the bounds", tree(map[string]string{"one": one}, "040000 tree "+mktree(t, dir, maxEntriesInTree+1, "100644 blob "+two, "n")+"\tmany"), "more than 64 entries"},
		{"a delete", zero, "never removed"},
	} {
		got := p.decide("operator", update{old: held, new: c.new, ref: ref})
		if (got == "") != (c.says == "") || !strings.Contains(got, c.says) {
			t.Errorf("%s: got %q, want one saying %q", c.what, got, c.says)
		}
	}
}
