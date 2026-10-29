package main

// The evidence a landing writes stays within every bound the push hook
// reads an attestation ref within (change.AttestationDepth and the bounds
// beside it), or nothing lands. Each case drives land over a real
// repository, with attest and cue stood in for: what they would say about
// a statement is not under test here.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"the-valley/change"
	"the-valley/integrator/verdict"
)

// landingWith sets up a request for a head on main's tip, whose check
// "build" holds note, and lands it. stored is notes the integrator's ref
// for the attested tree already holds, by path, as earlier landings of
// that tree left them. It returns the error land gave, and whether the
// target, the request and every record ref are as they were before.
func landingWith(t *testing.T, note string, stored map[string]string) (err error, untouched bool) {
	t.Helper()
	s := newScratchRepo(t)
	tip := s.git("rev-parse", "HEAD")
	head := s.commit("head", tip)
	landed := s.commit("landed", tip)
	keyHash := "cdcdcdcd"
	attested, digest := strings.Repeat("ab", 32), strings.Repeat("ef", 32)
	original := attestationPrefix + "/" + attested + "/" + keyHash
	if len(stored) > 0 {
		blobs := map[string]string{}
		for path, content := range stored {
			blobs[path] = s.input(content, "hash-object", "-w", "--stdin")
		}
		in := &integrator{repo: s.repo}
		tree, err := in.writeTree(blobs)
		if err != nil {
			t.Fatal(err)
		}
		s.git("update-ref", original, tree)
	}
	r := request{ref: requestPrefix + "/main/topic", change: "topic", target: "refs/heads/main", head: head}
	s.git("update-ref", r.ref, head)

	// attest renders a statement as it is given, and signs by adding a
	// line, as a countersignature adds one. cue passes every statement.
	bin := t.TempDir()
	attest, cue := filepath.Join(bin, "attest"), filepath.Join(bin, "cue")
	script := "#!/bin/sh\ncat\nif [ \"$1\" = sign ]; then echo '- the integrator signature'; fi\n"
	if err := os.WriteFile(attest, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cue, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	refs := func() string { return s.git("for-each-ref", "--format=%(refname) %(objectname)") }
	before := refs()
	in := &integrator{repo: s.repo, out: devNull(t), attest: attest, cue: cue, signerName: "integrator", keyHash: keyHash}
	ch := verdict.Change{ID: "topic", Target: r.target, Attested: attested}
	v := verdict.Verdict{Outcome: verdict.Land, Checks: []verdict.CheckVerdict{{Name: "build", Transferred: true, Rule: "unmoved"}}}
	l := landing{commit: landed, digest: digest, notes: map[string][]byte{"build": []byte(note)}}
	_, err = in.land(ch, v, r, tip, l, head+" "+head+" 0000000000000000", newStopwatch())
	return err, refs() == before
}

// statement is a note for check of exactly size bytes.
func statement(check string, size int) string {
	text := fmt.Sprintf("predicate.check.name %s\npredicate.result pass\n\n", check)
	return text + strings.Repeat("x", size-len(text)-1) + "\n"
}

func TestEvidenceWithinTheBoundsLands(t *testing.T) {
	stored := map[string]string{}
	for i := 0; i < 15; i++ {
		check := fmt.Sprintf("c%02d", i)
		stored[check+"/"+statementNote] = statement(check, 63<<10)
	}
	if err, untouched := landingWith(t, statement("build", 20<<10), stored); err != nil || untouched {
		t.Fatalf("evidence within every bound: land says %v, and the refs moved: %v", err, !untouched)
	}
}

func TestANoteOverTheNoteBoundLandsNothing(t *testing.T) {
	// The note fits, and the integrator's signature takes it over.
	err, untouched := landingWith(t, statement("build", change.AttestationNoteBytes), nil)
	if err == nil || !strings.Contains(err.Error(), "nothing landed") || !strings.Contains(err.Error(), "in one note") {
		t.Fatalf("a countersigned note over %d bytes: land says %v", change.AttestationNoteBytes, err)
	}
	if !untouched {
		t.Fatal("a countersigned note over the note bound: a ref moved")
	}
}

func TestMergedEvidenceOverTheByteBoundLandsNothing(t *testing.T) {
	// Each note fits, and so does what the ref held, and the two together
	// are past the bound on the bytes of the whole tree.
	stored := map[string]string{}
	for i := 0; i < 16; i++ {
		check := fmt.Sprintf("c%02d", i)
		stored[check+"/"+statementNote] = statement(check, 63<<10)
	}
	err, untouched := landingWith(t, statement("build", 20<<10), stored)
	if err == nil || !strings.Contains(err.Error(), "nothing landed") || !strings.Contains(err.Error(), "in all") {
		t.Fatalf("merged evidence over %d bytes: land says %v", change.AttestationBytes, err)
	}
	if !untouched {
		t.Fatal("merged evidence over the byte bound: a ref moved")
	}
}
