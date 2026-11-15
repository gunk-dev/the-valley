package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"the-valley/note/statement"
)

// cmdVerify checks an attestation, in the order the protocol states it:
// parse, validate, verify. The note's text is parsed back into a
// statement, which admits nothing but the one written form of what it
// says; that statement is vetted against the schema; and every signature
// by a key the verifier holds is checked over those exact bytes. Given
// --repo, the tree the statement names is then compared with the tree in
// front of the verifier.
//
// With --verdicts it takes --note once or more, and checks each note as a
// run for that note alone would, reading the keys and the tree once. The
// statements are vetted in one cue run, which checks each file against the
// schema on its own; when that run fails, each statement is vetted alone,
// so a refusal says what it would say for that note alone. It prints
// nothing until every note is checked, and then only its verdicts
// (verdictLines), so no line it prints carries text it did not write. The
// run exits 1 when any note is refused.
//
// Re-running a pure check and confirming its output digest is deliberately
// not here. That is witness re-verification, and it belongs to the trust
// backstop of a later phase; what this verifies is the claim's
// authenticity and its binding to a tree.
func cmdVerify(args []string) error {
	fs := newFlagSet("verify")
	var notes repeatable
	fs.Var(&notes, "note", "the signed note, repeatable")
	known := fs.String("known-keys", "", "verifier keys the reader accepts")
	schemaFlag := fs.String("schema", "", "attestation schema")
	repo := fs.String("repo", "", "repository to re-check against")
	rev := fs.String("rev", "HEAD", "revision in it")
	var required repeatable
	fs.Var(&required, "signer", "signer whose signature must be present")
	verdicts := fs.Bool("verdicts", false, "print one verdict line for each note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(notes) == 0 || slices.Contains(notes, "") {
		return fmt.Errorf("--note is required")
	}
	if *known == "" {
		return fmt.Errorf("--known-keys is required")
	}
	v := &verifying{known: *known, schemaFlag: *schemaFlag, repo: *repo, rev: *rev, required: required}
	switch {
	case *verdicts:
		return v.each(notes)
	case len(notes) > 1:
		return fmt.Errorf("--note is given more than once, which only --verdicts reads")
	}
	return v.alone(notes[0])
}

// verifying is one verify run's inputs, and what it has read of them. A
// value is read when the first note needs it and kept for the rest, so a
// run over several notes reads the keys, the repository, the schema and
// the tree once, and each note gets the answer a run of its own would.
type verifying struct {
	known, schemaFlag, repo, rev string
	required                     []string

	keysRead   bool
	keys       []verifierKey
	keysErr    error
	rootRead   bool
	root       string
	rootErr    error
	schemaRead bool
	schema     string
	schemaErr  error
	treeRead   bool
	tree       string
	treeErr    error
	vetted     map[string]bool // statements, as JSON, that cue has vetted
}

func (v *verifying) readKeys() ([]verifierKey, error) {
	if !v.keysRead {
		v.keysRead = true
		v.keys, v.keysErr = readVerifierKeys(v.known)
	}
	return v.keys, v.keysErr
}

func (v *verifying) readRoot() (string, error) {
	if !v.rootRead {
		v.rootRead = true
		if v.repo != "" {
			v.root, v.rootErr = repoRoot(v.repo)
		}
	}
	return v.root, v.rootErr
}

func (v *verifying) readSchema(root string) (string, error) {
	if !v.schemaRead {
		v.schemaRead = true
		v.schema, v.schemaErr = schemaPath(v.schemaFlag, root)
	}
	return v.schema, v.schemaErr
}

// readTree is the digest of the revision's tree in root.
func (v *verifying) readTree(root string) (string, error) {
	if !v.treeRead {
		v.treeRead = true
		entries, err := gitTreeEntries(root, v.rev)
		if err != nil {
			v.treeErr = err
		} else {
			v.tree = statement.TreeDigest(entries)
		}
	}
	return v.tree, v.treeErr
}

// alone verifies one note, printing its lines on stdout as each step
// passes.
func (v *verifying) alone(path string) error {
	note, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	keys, err := v.readKeys()
	if err != nil {
		return err
	}
	fmt.Printf("note      %s\n", path)
	_, err = v.check(path, note, keys, os.Stdout)
	return err
}

// each verifies each note as alone would, after vetting their statements
// together, and prints the verdicts once every note is checked.
func (v *verifying) each(paths []string) error {
	notes := make([][]byte, len(paths))
	unread := make([]error, len(paths))
	for i, path := range paths {
		notes[i], unread[i] = os.ReadFile(path)
	}
	keys, unkeyed := v.readKeys()
	if unkeyed == nil {
		v.vetTogether(notes, unread, keys)
	}
	signers := make([][]string, len(paths))
	refusals := make([]error, len(paths))
	for i, path := range paths {
		switch {
		case unread[i] != nil:
			refusals[i] = unread[i]
		case unkeyed != nil:
			refusals[i] = unkeyed
		default:
			signers[i], refusals[i] = v.check(path, notes[i], keys, io.Discard)
		}
	}
	// A reader that does not get every verdict line gets no verdict, so a
	// write that fails ends the run with an error.
	if _, err := os.Stdout.WriteString(verdictLines(signers, refusals)); err != nil {
		return err
	}
	refused := 0
	for _, err := range refusals {
		if err != nil {
			refused++
		}
	}
	if refused > 0 {
		return fmt.Errorf("%d of %d notes refused", refused, len(paths))
	}
	return nil
}

// verdictLines are the verdicts of the notes given, the first note's
// first, one line each, then a line that counts them:
//
//	verdict 1 verified "worker/attestations+74a84b34"
//	verdict 2 refused "tree digest mismatch: the statement is about a different tree\n  recorded …"
//	end 2
//
// A note is named by its place among the --note flags rather than by its
// object ID, since two notes may hold the same bytes. A verified note's
// line names every signer whose signature checked out, in the note's
// order; a refused note's gives why, the error a run for that note alone
// ends with. Every value is a Go string literal, so a newline in a path,
// a name or a tool's output stays inside its line.
func verdictLines(signers [][]string, refusals []error) string {
	var b strings.Builder
	for i := range refusals {
		if refusals[i] != nil {
			fmt.Fprintf(&b, "verdict %d refused %s\n", i+1, strconv.Quote(refusals[i].Error()))
			continue
		}
		fmt.Fprintf(&b, "verdict %d verified", i+1)
		for _, s := range signers[i] {
			b.WriteString(" " + strconv.Quote(s))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "end %d\n", len(refusals))
	return b.String()
}

// vetTogether vets in one cue run the statements of the notes that open
// and parse, and remembers them as vetted when every one passes. When any
// fails, it remembers none, and check vets each alone.
func (v *verifying) vetTogether(notes [][]byte, unread []error, keys []verifierKey) {
	root, err := v.readRoot()
	if err != nil {
		return
	}
	schema, err := v.readSchema(root)
	if err != nil {
		return
	}
	var docs [][]byte
	for i := range notes {
		if unread[i] != nil {
			continue
		}
		text, _, err := openNote(notes[i], keys)
		if err != nil {
			continue
		}
		doc, err := statement.Parse(text)
		if err != nil {
			continue
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			continue
		}
		docs = append(docs, raw)
	}
	if len(docs) < 2 || vetStatements(schema, docs) != nil {
		return
	}
	v.vetted = map[string]bool{}
	for _, raw := range docs {
		v.vetted[string(raw)] = true
	}
}

// check verifies one note's bytes, read from path, under keys, prints its
// lines to w as each step passes, and returns the signers whose signatures
// checked out, as name+hash, once every step has passed.
func (v *verifying) check(path string, note []byte, keys []verifierKey, w io.Writer) ([]string, error) {
	// The signature covers the text, so the text is what everything below
	// is about. Parsing it admits no rendering but the one written form of
	// what it says: a statement stored some other way would verify here and
	// fail to verify wherever it was written out again.
	text, sigs, err := openNote(note, keys)
	if err != nil {
		return nil, err
	}
	doc, err := statement.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	fmt.Fprintf(w, "text      ok (%d bytes signed)\n", len(text))

	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	root, err := v.readRoot()
	if err != nil {
		return nil, err
	}
	schema, err := v.readSchema(root)
	if err != nil {
		return nil, err
	}
	if !v.vetted[string(raw)] {
		if err := vetStatement(schema, raw); err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(w, "schema    ok (%s)\n", schema)

	signed := map[string]bool{}
	var signers []string
	for _, s := range sigs {
		state := "unknown"
		if s.verified {
			state = "ok"
			signed[s.name] = true
			signers = append(signers, fmt.Sprintf("%s+%08x", s.name, s.hash))
		}
		fmt.Fprintf(w, "signature %-7s %s+%08x\n", state, s.name, s.hash)
	}
	for _, want := range v.required {
		if !signed[want] {
			return nil, fmt.Errorf("no signature on this note is by %s", want)
		}
	}

	var st document
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	recorded := st.Subject.Digest[st.Subject.Primary]
	fmt.Fprintf(w, "subject   %s:%s\n", st.Subject.Primary, recorded)
	fmt.Fprintf(w, "predicate %s (%s %s: %s)\n", st.PredicateType, st.Predicate.Check.Runner, st.Predicate.Check.Name, st.Predicate.Result)

	if root == "" {
		fmt.Fprintf(w, "tree      not re-checked; pass --repo to bind this statement to a tree\n")
		return signers, nil
	}
	got, err := v.readTree(root)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "tree      %s@%s %s:%s\n", root, v.rev, statement.TreeScheme, got)
	if got != recorded {
		return nil, fmt.Errorf("tree digest mismatch: the statement is about a different tree\n  recorded %s\n  found    %s", recorded, got)
	}
	fmt.Fprintf(w, "          matches the recorded subject digest\n")
	return signers, nil
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
		if out, err = statement.RenderJSON(raw); err != nil {
			return err
		}
	} else {
		doc, err := statement.Parse(raw)
		if err != nil {
			return err
		}
		if out, err = statement.Render(doc); err != nil {
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
