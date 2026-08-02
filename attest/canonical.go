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
// units; the two orderings agree over ASCII, and every key in
// schema/attestation.cue is ASCII.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

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

// writeCanonicalString delegates escaping to encoding/json, with HTML
// escaping off — Go escapes <, > and & by default, which RFC 8785 does
// not.
func writeCanonicalString(b *bytes.Buffer, s string) error {
	var enc bytes.Buffer
	e := json.NewEncoder(&enc)
	e.SetEscapeHTML(false)
	if err := e.Encode(s); err != nil {
		return err
	}
	b.Write(bytes.TrimRight(enc.Bytes(), "\n"))
	return nil
}
