package main

// The statement's written form: the exact bytes a signature covers.
//
// A statement is written as lines. The first line is "valley-statement-v1",
// the name of this form. Every line after it is a key, one space, and a
// value, and the lines are sorted by key, ascending, comparing bytes. The
// text ends with a newline.
//
//	valley-statement-v1
//	predicate.check.name prose-format
//	predicate.result passed
//	predicateType the-valley/check/pure/v1
//	subject.primary valley-tree-v1
//
// A key is the path to the value from the document's root, with a dot
// between segments. A segment that is a decimal number is an index into an
// array, so `provenance.delegation.0.principal` is the principal of the
// first recorded delegation.
//
// The form has no escapes, and that is the whole reason it was chosen. A
// value is written as its own bytes, so there is exactly one way to write
// it and nothing for two implementations to disagree about. Everything a
// value may not contain is refused instead of escaped: a newline or any
// other ASCII control character, because those are what make a line a
// line, and the empty string, because a line's value is what follows the
// first space and an empty one leaves an invisible trailing space behind.
//
// The rules below are what make one document into one byte sequence.
//
//  1. Every leaf is a string. A number, a boolean and a null are refused,
//     because nobody has decided how a statement writes one down and a
//     guess would be a guess about signed bytes.
//  2. Lines are sorted by key bytes and no key repeats. Byte order is the
//     only order because a key segment is ASCII.
//  3. An object or an array with nothing in it has no lines, so it cannot
//     be written down at all. It is refused rather than dropped, so that
//     what was validated and what was signed cannot come apart.
//  4. An array is dense: indices run from 0 with no gaps, written in
//     decimal without leading zeros. Line order over indices is byte
//     order, so `.10` sits between `.1` and `.2`; a reader rebuilds the
//     array from the index and never from the order the lines arrive in.
//  5. A key segment starts with a letter and continues with letters,
//     digits and dashes. That keeps a segment clear of the dot that
//     separates segments, of the space that separates key from value, and
//     of the digits that mark an index.
//
// Parsing enforces every one of these, so text that parses is text this
// renderer would have produced. attest/conformance/ pins that against a
// second implementation.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// statementForm names this written form, and is the first line of every
// statement written in it. A signature covers the text and nothing else,
// so this line is what tells one kind of signed note from another: an
// attestation and a log checkpoint signed by the same key are different
// claims because their first lines differ, not because of anything in the
// envelope around them.
const statementForm = "valley-statement-v1"

// renderText writes a document as statement text.
func renderText(doc any) ([]byte, error) {
	var lines []textLine
	if err := flatten("", doc, &lines); err != nil {
		return nil, err
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].key < lines[j].key })

	var b bytes.Buffer
	b.WriteString(statementForm)
	b.WriteByte('\n')
	for _, l := range lines {
		b.WriteString(l.key)
		b.WriteByte(' ')
		b.WriteString(l.value)
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
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

type textLine struct{ key, value string }

func flatten(key string, v any, out *[]textLine) error {
	where := key
	if where == "" {
		where = "the statement"
	}
	switch t := v.(type) {
	case string:
		if err := checkValue(t); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		*out = append(*out, textLine{key, t})
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

// parseText reads statement text back into a document. It accepts only
// text this renderer would have produced: lines sorted by key with none
// repeated, segments and values within their shapes, and arrays dense from
// zero. That is what makes "the text is the written form of what it
// says" something a parser settles rather than a claim a verifier has to
// re-derive.
func parseText(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return nil, fmt.Errorf("statement text must end with a newline")
	}
	lines := strings.Split(string(raw[:len(raw)-1]), "\n")
	if lines[0] != statementForm {
		return nil, fmt.Errorf("line 1 is %q; statement text opens with %q", lines[0], statementForm)
	}

	root := &treeNode{children: map[string]*treeNode{}}
	previous := ""
	for i, l := range lines[1:] {
		n := i + 2
		key, value, found := strings.Cut(l, " ")
		if !found || value == "" {
			return nil, fmt.Errorf("line %d is %q; a line is a key, one space, and a value", n, l)
		}
		if err := checkValue(value); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if key <= previous {
			return nil, fmt.Errorf("line %d: key %q does not follow %q; lines are sorted by key, ascending, and no key repeats", n, key, previous)
		}
		previous = key
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
	if len(root.children) == 0 {
		return nil, fmt.Errorf("the text carries no lines, so it says nothing")
	}
	doc, err := root.document("")
	if err != nil {
		return nil, err
	}
	return doc.(map[string]any), nil
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
