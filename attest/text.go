package main

// The statement's written form: the exact bytes a signature covers.
//
// A statement is written as lines. The first line is the statement type,
// which says what the document is and which revision of it this is.
// Every line after it is a key, one space, and a value.
//
//	the-valley/attestation/v1
//	subject.primary valley-tree-v1
//	subject.digest.git-sha1 99b5f2f7…
//	subject.digest.valley-tree-v1 73847e0b…
//	predicateType the-valley/check/pure/v1
//	predicate.check.name prose-format
//	predicate.check.runner nix
//	predicate.check.attribute prose-format
//	predicate.result passed
//	…
//
// A key is the path to the value from the document's root, with a dot
// between segments. A segment that is a decimal number is an index into an
// array, so `provenance.delegation.0.principal` is the principal of the
// first recorded delegation.
//
// # Order
//
// Lines are written in the order a reader reads them: what the statement
// is about, then what kind of claim it makes, then the claim, then what
// produced the change. statementOrder is that order, written out, and it
// is the whole of what "the written form" means for line order. A field
// with no position in it has no written form, so a new field's position
// arrives with the field, and the predicate type is versioned so a claim
// shape can grow without moving what came before it.
//
// Within a set keyed by names a caller supplies rather than this table —
// a digest set, and any map added later — entries sort by key. Fixed order
// where position carries meaning, sorted order where it does not. An array
// is positional, so its elements are written in index order.
//
// # No escapes
//
// The form has no escapes, and that is the whole reason it was chosen. A
// value is written as its own bytes, so there is exactly one way to write
// it and nothing for two implementations to disagree about. Everything a
// value may not contain is refused instead of escaped: a newline or any
// other ASCII control character, because those are what make a line a
// line, and the empty string, because a line's value is what follows the
// first space and an empty one leaves an invisible trailing space behind.
//
// The rest of what makes one document into one byte sequence:
//
//  1. Every leaf is a string. A number, a boolean and a null are refused,
//     because nobody has decided how a statement writes one down and a
//     guess would be a guess about signed bytes.
//  2. An object or an array with nothing in it has no lines, so it cannot
//     be written down at all. It is refused rather than dropped, so that
//     what was validated and what was signed cannot come apart.
//  3. An array is dense: indices run from 0 with no gaps, written in
//     decimal without leading zeros.
//  4. A key segment starts with a letter and continues with letters,
//     digits and dashes. That keeps a segment clear of the dot that
//     separates segments, of the space that separates key from value, and
//     of the digits that mark an index.
//
// Text is accepted only if writing the document it says back out
// reproduces it byte for byte, so text that parses is text this renderer
// would have produced. attest/conformance/ pins that against a second
// implementation.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// field is one entry in the order table: a name, and how what sits under
// it is written.
type field struct {
	name string

	// set marks a map whose keys come from a caller rather than from this
	// table. Its entries carry no order of their own, so they sort by key.
	set bool

	// list marks an array. Its elements are positional, so they are
	// written in index order, each one under the fields below.
	list bool

	// under is the order of what sits beneath this field.
	under []field
}

func leaf(name string) field            { return field{name: name} }
func set(name string) field             { return field{name: name, set: true} }
func obj(name string, u ...field) field { return field{name: name, under: u} }

// statementOrder is the order a statement's lines are written in.
//
// A statement is a document a reader reads in order to decide something,
// so the reader's first question is answered first: what is this about.
// The kind of claim comes next, because it says how to read what follows;
// then the claim itself; then the provenance of the run that produced the
// change, which is context for the claim rather than part of it.
//
// The predicate's own order is per predicate type, because the claim reads
// differently for each. A pure claim reads as: the check, its result, and
// then the three digests a verifier re-derives against. A notarization
// reads as: the check, the environment it ran in, when that was, and what
// was observed.
func statementOrder(predicateType string) ([]field, error) {
	var predicate field
	switch predicateType {
	case predicatePure:
		predicate = obj("predicate",
			checkOrder,
			leaf("result"),
			set("inputs"),
			set("derivation"),
			set("output"),
		)
	case predicateEffect:
		predicate = obj("predicate",
			checkOrder,
			leaf("environment"),
			leaf("observedAt"),
			leaf("result"),
		)
	case "":
		return nil, fmt.Errorf("the statement names no predicate type, so how its claim is written down is not decided")
	default:
		return nil, fmt.Errorf("predicate type %q has no written form; a claim shape is written down by the revision that publishes it", predicateType)
	}
	return []field{
		obj("subject", leaf("primary"), set("digest")),
		leaf("predicateType"),
		predicate,
		obj("provenance",
			leaf("harness"),
			leaf("model"),
			set("prompt"),
			set("context"),
			field{name: "delegation", list: true, under: []field{
				leaf("principal"),
				leaf("grant"),
			}},
		),
	}, nil
}

// checkOrder is the check that ran: what it is called, how it was run, and
// then whichever of the two the runner names it by.
var checkOrder = obj("check",
	leaf("name"),
	leaf("runner"),
	leaf("attribute"),
	leaf("command"),
)

// renderText writes a document as statement text.
func renderText(doc any) ([]byte, error) {
	root, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a statement is an object, and this is %T", doc)
	}
	kind, _ := root["statementType"].(string)
	if kind != statementType {
		return nil, fmt.Errorf("the statement type is %q; %q is the one this writes down", root["statementType"], statementType)
	}
	predicateType, _ := root["predicateType"].(string)
	order, err := statementOrder(predicateType)
	if err != nil {
		return nil, err
	}

	// Flatten first, which settles every value and every field name, then
	// place the lines. A key the order table has no position for is left
	// over, and left over is refused.
	lines := map[string]string{}
	if err := flatten("", root, lines); err != nil {
		return nil, err
	}
	delete(lines, "statementType") // the header line carries it

	var b bytes.Buffer
	b.WriteString(kind)
	b.WriteByte('\n')
	placed := 0
	for _, key := range orderedKeys(root, order, "") {
		value, ok := lines[key]
		if !ok {
			continue
		}
		b.WriteString(key)
		b.WriteByte(' ')
		b.WriteString(value)
		b.WriteByte('\n')
		delete(lines, key)
		placed++
	}
	for _, key := range sortedKeys(lines) {
		return nil, fmt.Errorf("%s has no position in the written form of a %s", key, predicateType)
	}
	if placed == 0 {
		return nil, fmt.Errorf("the statement carries nothing but its type")
	}
	return b.Bytes(), nil
}

// orderedKeys walks the order table over a document and returns the keys
// it names, in order. A field the document does not carry contributes
// nothing; what the table has no entry for is not reached at all, which is
// how renderText finds it left over.
func orderedKeys(doc map[string]any, order []field, prefix string) []string {
	var out []string
	for _, f := range order {
		v, ok := doc[f.name]
		if !ok {
			continue
		}
		key := join(prefix, f.name)
		switch {
		case f.set:
			members, ok := v.(map[string]any)
			if !ok {
				out = append(out, key)
				continue
			}
			for _, name := range sortedAnyKeys(members) {
				out = append(out, join(key, name))
			}
		case f.list:
			elements, ok := v.([]any)
			if !ok {
				out = append(out, key)
				continue
			}
			for i, e := range elements {
				under, ok := e.(map[string]any)
				if !ok {
					out = append(out, join(key, strconv.Itoa(i)))
					continue
				}
				out = append(out, orderedKeys(under, f.under, join(key, strconv.Itoa(i)))...)
			}
		case f.under != nil:
			under, ok := v.(map[string]any)
			if !ok {
				out = append(out, key)
				continue
			}
			out = append(out, orderedKeys(under, f.under, key)...)
		default:
			out = append(out, key)
		}
	}
	return out
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderJSON writes a JSON document as statement text. JSON is how a
// statement is composed and how it is handed to `cue vet`; this form is
// how it is signed. UseNumber keeps a number a number rather than a
// float64, so a document carrying one is refused rather than quietly
// rounded on the way through.
func renderJSON(raw []byte) ([]byte, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return renderText(doc)
}

func flatten(key string, v any, out map[string]string) error {
	where := key
	if where == "" {
		where = "the statement"
	}
	switch t := v.(type) {
	case string:
		if err := checkValue(t); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		out[key] = t
	case map[string]any:
		if len(t) == 0 {
			return fmt.Errorf("%s: an object with no fields has no lines, so it cannot be written down", where)
		}
		for k, e := range t {
			if err := checkSegment(k); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			if err := flatten(join(key, k), e, out); err != nil {
				return err
			}
		}
	case []any:
		if len(t) == 0 {
			return fmt.Errorf("%s: an array with no elements has no lines, so it cannot be written down", where)
		}
		for i, e := range t {
			if err := flatten(join(key, strconv.Itoa(i)), e, out); err != nil {
				return err
			}
		}
	case json.Number:
		return fmt.Errorf("%s: %s is a number; a statement carries none, and how one is written down has never been decided", where, t)
	case bool:
		return fmt.Errorf("%s: %t is a boolean; a statement carries none, and how one is written down has never been decided", where, t)
	case nil:
		return fmt.Errorf("%s: is null; a field a statement does not carry is left out rather than written as null", where)
	default:
		return fmt.Errorf("%s: a statement may not carry %T", where, v)
	}
	return nil
}

func join(key, segment string) string {
	if key == "" {
		return segment
	}
	return key + "." + segment
}

// checkSegment holds a key segment to what a line can carry unambiguously:
// a letter, then letters, digits and dashes. A dot would look like the
// separator between segments, a space like the separator between key and
// value, and a leading digit like an array index.
func checkSegment(s string) error {
	switch {
	case s == "":
		return fmt.Errorf("a key segment may not be empty")
	case strings.Contains(s, "."):
		return fmt.Errorf("key segment %q contains a dot, which separates one segment from the next", s)
	case strings.Contains(s, " "):
		return fmt.Errorf("key segment %q contains a space, which separates the key from the value", s)
	case s[0] >= '0' && s[0] <= '9':
		return fmt.Errorf("key segment %q starts with a digit, which is how an array index is written", s)
	}
	for i, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
		if !ok {
			return fmt.Errorf("key segment %q holds %q at byte %d; a segment is a letter followed by letters, digits and dashes", s, r, i)
		}
	}
	return nil
}

// checkValue holds a value to what one line can carry. There is no escape
// mechanism to fall back on, so anything unwritable is an error here
// rather than a second way of writing something.
func checkValue(s string) error {
	if s == "" {
		return fmt.Errorf("the value is empty; a field a statement does not carry is left out rather than written as an empty string")
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("the value is not valid utf-8, so it has no written form")
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\n' {
			return fmt.Errorf("the value holds a newline, and a line ends where its newline is; there is no escape to write one with")
		} else if c < 0x20 || c == 0x7f {
			return fmt.Errorf("the value holds the control character %#02x, and there is no escape to write one with", c)
		}
	}
	return nil
}

// parseText reads statement text back into a document.
//
// It accepts text only if writing the document that text says back out
// reproduces it byte for byte. That is the whole acceptance rule, and it
// makes "these bytes are the written form of what they say" something a
// verifier settles by reading rather than a claim it has to take on trust:
// line order, a duplicated key, a set out of order and a value no line can
// carry all fail the same way, because none of them is what a renderer
// would have produced.
func parseText(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return nil, fmt.Errorf("statement text must end with a newline")
	}
	lines := strings.Split(string(raw[:len(raw)-1]), "\n")

	root := &treeNode{children: map[string]*treeNode{}}
	if err := root.insert([]string{"statementType"}, lines[0]); err != nil {
		return nil, err
	}
	if err := checkValue(lines[0]); err != nil {
		return nil, fmt.Errorf("line 1: %w", err)
	}
	for i, l := range lines[1:] {
		n := i + 2
		key, value, found := strings.Cut(l, " ")
		if !found || value == "" {
			return nil, fmt.Errorf("line %d is %q; a line is a key, one space, and a value", n, l)
		}
		if err := checkValue(value); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		segments := strings.Split(key, ".")
		for _, s := range segments {
			if err := checkKeyOrIndex(s); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
		}
		if err := root.insert(segments, value); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
	}
	if len(root.children) < 2 {
		return nil, fmt.Errorf("the text carries no lines, so it says nothing")
	}
	built, err := root.document("")
	if err != nil {
		return nil, err
	}
	doc := built.(map[string]any)

	again, err := renderText(doc)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(again, raw) {
		return nil, fmt.Errorf("this is not the written form of what it says:\n%s", firstDifference(raw, again))
	}
	return doc, nil
}

// firstDifference reports the first line the two texts disagree on, so a
// reader is pointed at the line rather than handed two documents to
// compare by eye.
func firstDifference(got, want []byte) string {
	a := strings.Split(string(got), "\n")
	b := strings.Split(string(want), "\n")
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := "", ""
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return fmt.Sprintf("  line %d is   %q\n  and would be %q", i+1, x, y)
		}
	}
	return "  the two are equal"
}

// checkKeyOrIndex admits what a rendered key segment can be: a name, or an
// array index written in decimal with no leading zeros.
func checkKeyOrIndex(s string) error {
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		if s != "0" && s[0] == '0' {
			return fmt.Errorf("index %q has a leading zero; an index is written in decimal without one", s)
		}
		if _, err := strconv.Atoi(s); err != nil {
			return fmt.Errorf("key segment %q starts with a digit but is not an array index", s)
		}
		return nil
	}
	return checkSegment(s)
}

// treeNode is the document being rebuilt: either a value or the segments
// found beneath a key.
type treeNode struct {
	value    string
	leaf     bool
	children map[string]*treeNode
}

func (t *treeNode) insert(segments []string, value string) error {
	if t.leaf {
		return fmt.Errorf("a key is both a value and a path to other values")
	}
	head := segments[0]
	child, ok := t.children[head]
	if !ok {
		child = &treeNode{children: map[string]*treeNode{}}
		t.children[head] = child
	}
	if len(segments) == 1 {
		if child.leaf || len(child.children) > 0 {
			return fmt.Errorf("a key is both a value and a path to other values")
		}
		child.leaf, child.value = true, value
		return nil
	}
	return child.insert(segments[1:], value)
}

func (t *treeNode) document(key string) (any, error) {
	if t.leaf {
		return t.value, nil
	}
	where := key
	if where == "" {
		where = "the statement"
	}
	indexed := 0
	for s := range t.children {
		if s[0] >= '0' && s[0] <= '9' {
			indexed++
		}
	}
	if indexed > 0 && indexed != len(t.children) {
		return nil, fmt.Errorf("%s has both array indices and named fields beneath it", where)
	}
	if indexed > 0 {
		out := make([]any, len(t.children))
		for s, child := range t.children {
			i, _ := strconv.Atoi(s)
			if i >= len(t.children) {
				return nil, fmt.Errorf("%s runs to index %d but holds %d elements; an array's indices run from 0 with no gaps", where, i, len(t.children))
			}
			v, err := child.document(join(key, s))
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	out := make(map[string]any, len(t.children))
	for s, child := range t.children {
		v, err := child.document(join(key, s))
		if err != nil {
			return nil, err
		}
		out[s] = v
	}
	return out, nil
}
