package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// cmdVerify checks an attestation, in the order the protocol states it:
// parse, validate, verify. The note's text is parsed back into a
// statement, which admits nothing but the one written form of what it
// says; that statement is vetted against the schema; and every signature
// by a key the verifier holds is checked over those exact bytes. Given
// --repo, the tree the statement names is then compared with the tree in
// front of the verifier.
//
// Re-running a pure check and confirming its output digest is deliberately
// not here. That is witness re-verification, and it belongs to the trust
// backstop of a later phase; what this verifies is the claim's
// authenticity and its binding to a tree.
func cmdVerify(args []string) error {
	fs := newFlagSet("verify")
	noteFile := fs.String("note", "", "the signed note")
	known := fs.String("known-keys", "", "verifier keys the reader accepts")
	schemaFlag := fs.String("schema", "", "attestation schema")
	repo := fs.String("repo", "", "repository to re-check against")
	rev := fs.String("rev", "HEAD", "revision in it")
	var required repeatable
	fs.Var(&required, "signer", "signer whose signature must be present")
	if err := fs.Parse(args); err != nil {
		return err
	}
	for name, v := range map[string]string{"--note": *noteFile, "--known-keys": *known} {
		if v == "" {
			return fmt.Errorf("%s is required", name)
		}
	}

	note, err := os.ReadFile(*noteFile)
	if err != nil {
		return err
	}
	keys, err := readVerifierKeys(*known)
	if err != nil {
		return err
	}
	fmt.Printf("note      %s\n", *noteFile)

	// The signature covers the text, so the text is what everything below
	// is about. Parsing it admits no rendering but the one written form of
	// what it says: a statement stored some other way would verify here and
	// fail to verify wherever it was written out again.
	text, sigs, err := openNote(note, keys)
	if err != nil {
		return err
	}
	doc, err := parseText(text)
	if err != nil {
		return fmt.Errorf("%s: %w", *noteFile, err)
	}
	fmt.Printf("text      ok (%d bytes signed)\n", len(text))

	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	root := ""
	if *repo != "" {
		if root, err = repoRoot(*repo); err != nil {
			return err
		}
	}
	schema, err := schemaPath(*schemaFlag, root)
	if err != nil {
		return err
	}
	if err := vetStatement(schema, raw); err != nil {
		return err
	}
	fmt.Printf("schema    ok (%s)\n", schema)

	signed := map[string]bool{}
	for _, s := range sigs {
		state := "unknown"
		if s.verified {
			state = "ok"
			signed[s.name] = true
		}
		fmt.Printf("signature %-7s %s+%08x\n", state, s.name, s.hash)
	}
	for _, want := range required {
		if !signed[want] {
			return fmt.Errorf("no signature on this note is by %s", want)
		}
	}

	var st statement
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	recorded := st.Subject.Digest[st.Subject.Primary]
	fmt.Printf("subject   %s:%s\n", st.Subject.Primary, recorded)
	fmt.Printf("predicate %s (%s %s: %s)\n", st.PredicateType, st.Predicate.Check.Runner, st.Predicate.Check.Name, st.Predicate.Result)

	if root == "" {
		fmt.Printf("tree      not re-checked; pass --repo to bind this statement to a tree\n")
		return nil
	}
	entries, err := gitTreeEntries(root, *rev)
	if err != nil {
		return err
	}
	got := treeDigest(entries)
	fmt.Printf("tree      %s@%s %s:%s\n", root, *rev, treeScheme, got)
	if got != recorded {
		return fmt.Errorf("tree digest mismatch: the statement is about a different tree\n  recorded %s\n  found    %s", recorded, got)
	}
	fmt.Printf("          matches the recorded subject digest\n")
	return nil
}

// cmdRender writes a document as the statement text a signature covers.
//
// It takes either a JSON document or statement text, told apart by the
// first character a JSON object has to open with. Text in, text out is how
// a reader checks that a stored statement is in the one written form of
// what it says: the form is a fixed point, so anything this command
// changes was not it.
func cmdRender(args []string) error {
	fs := newFlagSet("render")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := readInput(fs.Args(), "render takes one file, or none to read stdin")
	if err != nil {
		return err
	}
	var out []byte
	if isJSON(raw) {
		if out, err = renderJSON(raw); err != nil {
			return err
		}
	} else {
		doc, err := parseText(raw)
		if err != nil {
			return err
		}
		if out, err = renderText(doc); err != nil {
			return err
		}
	}
	_, err = os.Stdout.Write(out)
	return err
}

// cmdSign signs statement text, or adds a signature to a note that already
// carries one. Which of the two it is doing is decided by the input:
// statement text holds no blank line, and a note is a text and a signature
// block with a blank line between them.
func cmdSign(args []string) error {
	fs := newFlagSet("sign")
	key := fs.String("key", "", "ed25519 private key to sign with")
	name := fs.String("name", defaultSignerName(), "the name this key signs under")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := readInput(fs.Args(), "sign takes one file, or none to read stdin")
	if err != nil {
		return err
	}
	s, err := loadSigner(*key, *name)
	if err != nil {
		return err
	}
	var note []byte
	if bytes.Contains(raw, []byte("\n\n")) {
		note, err = addSignature(raw, s)
	} else {
		note, err = signNote(raw, s)
	}
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(note)
	return err
}

// cmdKey prints the verifier key for a signing key: the one line a
// verifier is given in order to accept this signer, and nothing else.
func cmdKey(args []string) error {
	fs := newFlagSet("key")
	key := fs.String("key", "", "ed25519 private key")
	name := fs.String("name", defaultSignerName(), "the name this key signs under")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := loadSigner(*key, *name)
	if err != nil {
		return err
	}
	fmt.Println(s.verifierKey())
	return nil
}

// isJSON says whether input is a JSON document rather than statement text.
// A statement is an object, so a JSON one opens with a brace and statement
// text never does.
func isJSON(raw []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(raw, " \t\r\n"), []byte("{"))
}

func readInput(args []string, misuse string) ([]byte, error) {
	switch len(args) {
	case 0:
		return io.ReadAll(os.Stdin)
	case 1:
		return os.ReadFile(args[0])
	default:
		return nil, fmt.Errorf("%s", misuse)
	}
}
