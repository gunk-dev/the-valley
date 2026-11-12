package statement

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The written form and the tree digest decide which bytes get signed and
// what a subject is, so both must agree with any other implementation.
// attest/conformance/ holds the written form to recorded vectors through
// attest render as well.

// pureStatement is a whole statement, which is the only thing that has a
// written form: order is defined per predicate type, so a fragment has no
// order at all.
func pureStatement() map[string]any {
	return map[string]any{
		"statementType": Type,
		"predicateType": PredicatePure,
		"subject": map[string]any{
			"primary": TreeScheme,
			"digest": map[string]any{
				TreeScheme: strings.Repeat("a", 64),
				"git-sha1": strings.Repeat("b", 40),
			},
		},
		"predicate": map[string]any{
			"check":      map[string]any{"name": "prose-format", "runner": "nix", "attribute": "prose-format"},
			"result":     "passed",
			"inputs":     map[string]any{"valley-inputs-v1": strings.Repeat("c", 64)},
			"derivation": map[string]any{"sha256": strings.Repeat("d", 64)},
			"output":     map[string]any{TreeScheme: strings.Repeat("e", 64)},
		},
		"provenance": map[string]any{
			"harness": "a harness",
			"model":   "a model",
			"delegation": []any{
				map[string]any{"principal": "human:integrator", "grant": "land changes"},
				map[string]any{"principal": "agent:run"},
			},
		},
	}
}

func TestRenderTextWritesLinesInNarrativeOrder(t *testing.T) {
	// The reader's first question — what is this about — is answered
	// first, and what produced the change comes last. Within the digest
	// set, whose keys are naming schemes rather than positions, entries
	// sort by key.
	got, err := Render(pureStatement())
	if err != nil {
		t.Fatal(err)
	}
	want := Type + "\n" +
		"subject.primary valley-tree-v1\n" +
		"subject.digest.git-sha1 " + strings.Repeat("b", 40) + "\n" +
		"subject.digest.valley-tree-v1 " + strings.Repeat("a", 64) + "\n" +
		"predicateType " + PredicatePure + "\n" +
		"predicate.check.name prose-format\n" +
		"predicate.check.runner nix\n" +
		"predicate.check.attribute prose-format\n" +
		"predicate.result passed\n" +
		"predicate.inputs.valley-inputs-v1 " + strings.Repeat("c", 64) + "\n" +
		"predicate.derivation.sha256 " + strings.Repeat("d", 64) + "\n" +
		"predicate.output.valley-tree-v1 " + strings.Repeat("e", 64) + "\n" +
		"provenance.harness a harness\n" +
		"provenance.model a model\n" +
		"provenance.delegation.0.principal human:integrator\n" +
		"provenance.delegation.0.grant land changes\n" +
		"provenance.delegation.1.principal agent:run\n"
	if string(got) != want {
		t.Errorf("Render =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderTextWritesArrayElementsInIndexOrder(t *testing.T) {
	// An array is positional, so its elements are written in index order.
	// Eleven of them is where an order taken from the key's bytes rather
	// than from the index would put the tenth in the wrong place.
	doc := pureStatement()
	var steps []any
	for i := 0; i < 11; i++ {
		steps = append(steps, map[string]any{"principal": fmt.Sprintf("step-%d", i)})
	}
	doc["provenance"].(map[string]any)["delegation"] = steps
	got, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, l := range strings.Split(string(got), "\n") {
		if strings.HasPrefix(l, "provenance.delegation.") {
			seen = append(seen, strings.TrimPrefix(l, "provenance.delegation."))
		}
	}
	if len(seen) != 11 || seen[1] != "1.principal step-1" || seen[10] != "10.principal step-10" {
		t.Errorf("delegation steps are not in index order: %q", seen)
	}
}

func TestRenderTextWritesValuesLiterally(t *testing.T) {
	// The form has no escapes, which is what leaves two implementations
	// nothing to disagree about. Every character below is one a JSON
	// encoder would have had a choice about.
	const odd = `a<b>c&d "q" \ / ü😀`
	doc := pureStatement()
	doc["provenance"].(map[string]any)["harness"] = odd
	got, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\nprovenance.harness "+odd+"\n") {
		t.Errorf("Render did not write the value as its own bytes:\n%s", got)
	}
}

func TestRenderTextRefusesWhatItCannotWrite(t *testing.T) {
	// Each of these is a document with no written form. Refusing beats
	// guessing, and beats dropping something that was validated a moment
	// earlier and would then not be in the bytes that got signed.
	for name, spoil := range map[string]func(map[string]any){
		"a number":             func(d map[string]any) { d["provenance"] = map[string]any{"model": json.Number("1.5")} },
		"a boolean":            func(d map[string]any) { d["provenance"] = map[string]any{"model": true} },
		"a null":               func(d map[string]any) { d["provenance"] = map[string]any{"model": nil} },
		"an empty object":      func(d map[string]any) { d["provenance"] = map[string]any{} },
		"an empty array":       func(d map[string]any) { d["provenance"] = map[string]any{"delegation": []any{}} },
		"an empty string":      func(d map[string]any) { d["provenance"] = map[string]any{"model": ""} },
		"a newline in a value": func(d map[string]any) { d["provenance"] = map[string]any{"model": "two\nlines"} },
		"a tab in a value":     func(d map[string]any) { d["provenance"] = map[string]any{"model": "a\tb"} },
		"invalid utf-8":        func(d map[string]any) { d["provenance"] = map[string]any{"model": "\xff"} },
		"a key with a dot":     func(d map[string]any) { d["provenance"] = map[string]any{"a.b": "x"} },
		"a key with a space":   func(d map[string]any) { d["provenance"] = map[string]any{"a b": "x"} },
		"a non-ascii key":      func(d map[string]any) { d["provenance"] = map[string]any{"café": "x"} },
		"an index-shaped key":  func(d map[string]any) { d["provenance"] = map[string]any{"0": "x"} },

		// A field the order table has no position for cannot be placed, so
		// it is refused rather than written somewhere a second
		// implementation would not look for it.
		"a field with no position": func(d map[string]any) { d["provenance"] = map[string]any{"authority": "root"} },
		"an unknown predicate type": func(d map[string]any) {
			d["predicateType"] = "the-valley/check/pure/v2"
		},
		"an unknown statement type": func(d map[string]any) {
			d["Type"] = "the-valley/attestation/v2"
		},
	} {
		doc := pureStatement()
		spoil(doc)
		if _, err := Render(doc); err == nil {
			t.Errorf("Render accepted %s", name)
		}
	}
}

func TestParseTextRoundTrips(t *testing.T) {
	doc := pureStatement()
	text, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	if !reflect.DeepEqual(back, doc) {
		t.Errorf("Parse(Render(doc)) =\n  %#v\nwant\n  %#v", back, doc)
	}
	// The form is a fixed point, or a statement written out again anywhere
	// downstream stops matching what was signed.
	again, err := Render(back)
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
	// they say" by reading, rather than trusting the sender.
	good, err := Render(pureStatement())
	if err != nil {
		t.Fatal(err)
	}
	swap := func(a, b string) string {
		lines := strings.Split(string(good), "\n")
		i, j := indexOfPrefix(t, lines, a), indexOfPrefix(t, lines, b)
		lines[i], lines[j] = lines[j], lines[i]
		return strings.Join(lines, "\n")
	}
	for name, text := range map[string]string{
		"no header":           strings.TrimPrefix(string(good), Type+"\n"),
		"another header":      "the-valley/attestation/v2\n" + strings.TrimPrefix(string(good), Type+"\n"),
		"no closing newline":  strings.TrimSuffix(string(good), "\n"),
		"a key with no value": string(good) + "provenance.model\n",
		"a blank line":        strings.Replace(string(good), "predicateType ", "\npredicateType ", 1),
		"a repeated key":      string(good) + "provenance.model twice\n",
		"nothing at all":      Type + "\n",

		// The claim before what it is about, which is the order a reader
		// does not read in.
		"sections out of order": swap("subject.primary ", "predicateType "),
		// A set whose entries carry no order of their own, out of the one
		// order they have.
		"a set out of order": swap("subject.digest.git-sha1 ", "subject.digest.valley-tree-v1 "),
		// An array element out of its position.
		"an array out of order": swap("provenance.delegation.0.principal ", "provenance.delegation.1.principal "),
		// A field the order table has no position for.
		"a field with no position": string(good) + "provenance.authority root\n",

		"a sparse array":  Type + "\np.0 one\np.2 three\n",
		"index and field": Type + "\np.0 one\np.name two\n",
		"value and path":  Type + "\np one\np.q two\n",
		"a leading zero":  Type + "\np.00 one\n",
	} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("Parse accepted %s: %q", name, text)
		}
	}
}

func indexOfPrefix(t *testing.T, lines []string, prefix string) int {
	t.Helper()
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	t.Fatalf("no line starting %q", prefix)
	return -1
}

func TestTreeManifestIsUnambiguous(t *testing.T) {
	// A path holding the manifest's own separators must not be able to
	// forge an entry boundary: the byte length is what delimits it.
	entries := []Entry{
		{Mode: ModeFile, Path: "b\n100644 x 1 c", Sum: [32]byte{}},
		{Mode: ModeExec, Path: "a", Sum: [32]byte{0xff}},
	}
	want := "valley-tree-v1\n" +
		"100755 ff00000000000000000000000000000000000000000000000000000000000000 1 a\n" +
		"100644 0000000000000000000000000000000000000000000000000000000000000000 14 b\n100644 x 1 c\n"
	if got := string(TreeManifest(entries)); got != want {
		t.Errorf("TreeManifest =\n%q\nwant\n%q", got, want)
	}
}

func TestTreeDigestIsPinned(t *testing.T) {
	// The digest of a fixed tree is a wire format: changing it invalidates
	// every attestation ever made. This value may only change with the
	// scheme's version.
	entries := []Entry{{Mode: ModeFile, Path: "a.txt", Sum: sha256of("hi\n")}}
	const want = "74781d4ba499b9896e97f2fdd38815d5723cb7b12e448ef0ce5788a1e72c2220"
	if got := TreeDigest(entries); got != want {
		t.Errorf("TreeDigest = %s, want %s", got, want)
	}
}

func sha256of(s string) [32]byte { return sha256.Sum256([]byte(s)) }
