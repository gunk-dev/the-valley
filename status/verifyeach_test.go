package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fakeVerify writes what attest verify --verdicts writes for the notes
// args name (attest/verify.go), judging each note by its text: each line
// "signer <name>+<hash>" is a signature that checks out when the
// --known-keys file holds a key of that name, and a text that opens with
// "refuse " is refused with the rest of it. In mode "old" it refuses
// --verdicts, as an attest without it does; in mode "status" it exits 1
// whatever its verdicts.
func fakeVerify(mode string, args []string) int {
	var notes []string
	verdicts, known := false, map[string]bool{}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--verdicts":
			verdicts = true
		case args[i] == "--note" && i+1 < len(args):
			notes = append(notes, args[i+1])
			i++
		case args[i] == "--known-keys" && i+1 < len(args):
			keys, _ := os.ReadFile(args[i+1])
			for _, line := range strings.Split(string(keys), "\n") {
				if name, _, ok := strings.Cut(line, "+"); ok {
					known[name] = true
				}
			}
			i++
		}
	}
	if mode == "old" && verdicts {
		fmt.Fprintln(os.Stderr, "flag provided but not defined: -verdicts")
		return 2
	}
	refused := 0
	for i, path := range notes {
		text, _ := os.ReadFile(path)
		if why, refuse := strings.CutPrefix(string(text), "refuse "); refuse {
			fmt.Printf("verdict %d refused %s\n", i+1, strconv.Quote(why))
			refused++
			continue
		}
		var signers []string
		for _, line := range strings.Split(string(text), "\n") {
			if signer, ok := strings.CutPrefix(line, "signer "); ok {
				if name, _, _ := strings.Cut(signer, "+"); known[name] {
					signers = append(signers, strconv.Quote(signer))
				}
			}
		}
		if len(signers) == 0 {
			fmt.Printf("verdict %d refused %s\n", i+1, strconv.Quote("no signature on this note is by a key the verifier holds"))
			refused++
			continue
		}
		fmt.Printf("verdict %d verified %s\n", i+1, strings.Join(signers, " "))
	}
	fmt.Printf("end %d\n", len(notes))
	if refused > 0 || mode == "status" {
		fmt.Fprintf(os.Stderr, "attest: %d of %d notes refused\n", refused, len(notes))
		return 1
	}
	return 0
}

// verifyEach runs attest verify --verdicts once for any number of notes,
// and reads each note's verdict: the signers that check out, or the
// first line of a refusal. An attest that refuses --verdicts, or exits
// other than its verdicts call for, gives no verdict on any note.
func TestVerifyEachReadsOneVerdictForEachNote(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "attest")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	in := func(mode string) git {
		t.Setenv("VALLEY_TEST_ATTEST_VERIFY", mode)
		return git{env: os.Environ()}
	}
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte("worker+00000001+key\nstranger+00000002+key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	notes := [][]byte{
		[]byte("signer worker+00000001\n"),
		[]byte("refuse tree digest mismatch: the statement is about a different tree\n  recorded aa\n  found    bb\n"),
		[]byte("signer stranger+00000002\nsigner worker+00000001\n"),
		[]byte("unsigned\n"),
	}
	want := []noteVerdict{
		{signers: []string{"worker+00000001"}},
		{refusal: "tree digest mismatch: the statement is about a different tree"},
		{signers: []string{"stranger+00000002", "worker+00000001"}},
		{refusal: "no signature on this note is by a key the verifier holds"},
	}
	if got, err := verifyEach(in("batch"), ".", "HEAD", keys, notes); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("verifyEach read the notes as %+v (%v), not %+v", got, err, want)
	}
	if got, err := verifyEach(in("batch"), ".", "HEAD", keys, notes[:1]); err != nil || !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("verifyEach read one note as %+v (%v), not %+v", got, err, want[:1])
	}
	for _, mode := range []string{"old", "status"} {
		if got, err := verifyEach(in(mode), ".", "HEAD", keys, [][]byte{notes[0], notes[2]}); err == nil {
			t.Errorf("an attest in mode %s gave verdicts %+v", mode, got)
		}
	}
}

// Only one verdict for each note, in the order the notes were given,
// then the line that counts them, reads as verdicts. Anything else is an
// error for the whole run: a forged line, whether a path's newline put it
// there or not, never decides a note, even beside a note genuinely
// refused.
func TestReadVerdictsRefusesAnythingElse(t *testing.T) {
	refused := `verdict 1 refused "tree digest mismatch: the statement is about a different tree\n  recorded aa"`
	verified := `verdict 2 verified "worker+00000001"`
	if got, any, err := readVerdicts(refused+"\n"+verified+"\nend 2\n", 2); err != nil || !any ||
		!reflect.DeepEqual(got, []noteVerdict{{refusal: "tree digest mismatch: the statement is about a different tree"},
			{signers: []string{"worker+00000001"}}}) {
		t.Fatalf("two well-formed verdicts read as %+v, %v, %v", got, any, err)
	}
	for _, c := range []struct{ name, out string }{
		{"a forged verified after a refusal", refused + "\nverified\n" + verified + "\nend 2\n"},
		{"a forged verdict after a refusal", refused + "\nverdict 1 verified \"worker+00000001\"\n" + verified + "\nend 2\n"},
		{"a duplicate verdict", refused + "\n" + refused + "\nend 2\n"},
		{"a contradictory verdict", refused + "\n" + `verdict 1 verified "worker+00000001"` + "\nend 2\n"},
		{"verdicts out of order", verified + "\n" + refused + "\nend 2\n"},
		{"an unknown note", refused + "\n" + `verdict 3 verified "worker+00000001"` + "\nend 2\n"},
		{"an extra verdict", refused + "\n" + verified + "\n" + `verdict 3 verified "worker+00000001"` + "\nend 2\n"},
		{"a missing verdict", refused + "\nend 2\n"},
		{"no end line", refused + "\n" + verified + "\n"},
		{"an end line counting other notes", refused + "\n" + verified + "\nend 3\n"},
		{"a step line", refused + "\n" + "signature ok      worker+00000001\n" + verified + "\nend 2\n"},
		{"a line after the end", refused + "\n" + verified + "\nend 2\nverified\n"},
		{"a refusal that says nothing", `verdict 1 refused ""` + "\n" + verified + "\nend 2\n"},
		{"a refusal with two reasons", `verdict 1 refused "a" "b"` + "\n" + verified + "\nend 2\n"},
		{"a value that is no string literal", "verdict 1 verified worker+00000001\n" + verified + "\nend 2\n"},
		{"a raw string literal", "verdict 1 verified `worker+00000001`\n" + verified + "\nend 2\n"},
		{"a trailing space", refused + "\n" + verified + " \nend 2\n"},
		{"another word", `verdict 1 accepted "worker+00000001"` + "\n" + verified + "\nend 2\n"},
		{"a verified verdict naming no signer", "verdict 1 verified\n" + verified + "\nend 2\n"},
	} {
		if got, _, err := readVerdicts(c.out, 2); err == nil {
			t.Errorf("%s read as verdicts %+v", c.name, got)
		}
	}
}
