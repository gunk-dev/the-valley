package main

// The canonical form of a statement: the exact bytes the signature covers.
// Two tools that compose the same statement must produce the same bytes,
// or a signature made by one cannot be checked by the other.
//
// The rules are RFC 8785's, narrowed to what a statement can hold: object
// members sorted by key, no whitespace between tokens, and minimal string
// escaping. A statement carries strings, objects and arrays only — never a
// number — so the one genuinely hard part of RFC 8785, canonicalizing
// floating-point, cannot arise. A number reaching this code is a statement
// shape nobody has decided the canonical form of, so it is an error rather
// than a guess.
//
// Keys are sorted by their UTF-8 bytes. RFC 8785 sorts by UTF-16 code
// units; the two orderings agree over ASCII and part company above it. So
// ASCII is a condition of these bytes being the same bytes anyone else
// renders, not a fact that happens to hold: #AsciiKey in
// schema/attestation.cue keeps a non-ASCII key out of a statement, and a
// key that reaches here anyway is refused rather than sorted.
//
// attest/conformance/ holds the vectors that pin all of this against a
// second implementation.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"unicode/utf8"
)

// canonicalRender renders JSON bytes into their canonical form. UseNumber
// keeps a number a number instead of a float64, so a statement carrying one
// is refused rather than quietly rounded on the way through.
func canonicalRender(raw []byte) ([]byte, error) {
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return canonicalJSON(generic)
}

func canonicalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeCanonical(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// canonicalize round-trips a value through encoding/json so that struct
// tags, omitempty and nesting are all applied before canonical bytes are
// rendered from the resulting generic value.
func canonicalize(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return canonicalJSON(generic)
}

func writeCanonical(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		return writeCanonicalString(b, t)
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonical(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			if err := asciiKey(k); err != nil {
				return err
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonicalString(b, k); err != nil {
				return err
			}
			b.WriteByte(':')
			if err := writeCanonical(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonical json: a statement may not carry %T", v)
	}
	return nil
}

// asciiKey refuses a key this code cannot order the way RFC 8785 orders it.
// The failure it prevents is silent: a non-ASCII key sorts one way here and
// another way in an implementation that follows the specification exactly,
// so the two sign different bytes over the same statement and neither
// signature checks against the other. The schema narrows further, to
// printable ASCII; what is unorderable is what is refused here.
func asciiKey(k string) error {
	for i := 0; i < len(k); i++ {
		if k[i] >= 0x80 {
			return fmt.Errorf("canonical json: key %q is not ASCII; member order over it would not match RFC 8785's, so two implementations would sign different bytes", k)
		}
	}
	return nil
}

// writeCanonicalString escapes as RFC 8785 does: the five short escapes,
// \u00xx with lowercase hex for the control characters that have none, and
// every other character as itself — <, > and & included, and every
// non-ASCII character as its own UTF-8 bytes rather than an escape.
//
// Escaping is spelled out here rather than delegated to encoding/json,
// which escapes U+2028 and U+2029 for JavaScript's benefit and would put
// this signer a codepoint apart from one that follows the specification.
// Invalid UTF-8 has no canonical form, and encoding/json would substitute
// U+FFFD for it, so it is refused for the same reason a number is.
func writeCanonicalString(b *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("canonical json: %q is not valid utf-8; it has no canonical form", s)
	}
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20:
			fmt.Fprintf(b, `\u%04x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return nil
}
