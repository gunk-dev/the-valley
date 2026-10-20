package review

import (
	"errors"
	"fmt"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// This file is a JSON reader stricter than encoding/json, which accepts a
// duplicate key, matches a key to a field whatever its case, and silently
// replaces bytes that are not UTF-8. A review in which one key appears
// twice reads differently to two readers that keep different copies, so
// the reader here refuses it, at any depth. It is the change record's
// reader (../change/json.go), read the same way.
//
// It reads the JSON a review is written in and nothing more. Every value
// is an object, an array, a string or a number, and true, false and null
// are read as values of their own kind, so the schema can refuse them by
// name. A number is kept as its text, and the schema decides whether it
// is an integer in range.

// maxDepth is how deeply values may nest. A review nests four deep: the
// review, its notes, one note, and the note's lines.
const maxDepth = 8

// kind is the kind of a JSON value.
type kind int

const (
	kindObject kind = iota
	kindArray
	kindString
	kindNumber
	kindLiteral // true, false or null
)

func (k kind) String() string {
	return [...]string{"an object", "an array", "a string", "a number", "a literal"}[k]
}

// value is one JSON value as read.
type value struct {
	kind   kind
	text   string            // a string's content, a number's or a literal's text
	keys   []string          // an object's keys, in the order they appear
	fields map[string]*value // an object's values
	items  []*value          // an array's values
}

// readJSON reads data as exactly one JSON value, with only white space
// around it.
func readJSON(data []byte) (*value, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("it is not valid UTF-8")
	}
	r := reader{data: data}
	r.space()
	v, err := r.value(1)
	if err != nil {
		return nil, err
	}
	r.space()
	if r.pos != len(r.data) {
		return nil, fmt.Errorf("it holds more after the value it opens with, at byte %d", r.pos)
	}
	return v, nil
}

type reader struct {
	data []byte
	pos  int
}

func (r *reader) space() {
	for r.pos < len(r.data) {
		switch r.data[r.pos] {
		case ' ', '\t', '\n', '\r':
			r.pos++
		default:
			return
		}
	}
}

func (r *reader) fail(what string) error {
	if r.pos >= len(r.data) {
		return fmt.Errorf("it ends where %s was expected", what)
	}
	return fmt.Errorf("it holds %q at byte %d, where %s was expected", r.data[r.pos], r.pos, what)
}

func (r *reader) value(depth int) (*value, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("it nests values more than %d deep", maxDepth)
	}
	if r.pos >= len(r.data) {
		return nil, r.fail("a value")
	}
	switch c := r.data[r.pos]; {
	case c == '{':
		return r.object(depth)
	case c == '[':
		return r.array(depth)
	case c == '"':
		s, err := r.string()
		if err != nil {
			return nil, err
		}
		return &value{kind: kindString, text: s}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		return r.number()
	default:
		for _, lit := range []string{"true", "false", "null"} {
			if len(r.data)-r.pos >= len(lit) && string(r.data[r.pos:r.pos+len(lit)]) == lit {
				r.pos += len(lit)
				return &value{kind: kindLiteral, text: lit}, nil
			}
		}
		return nil, r.fail("a value")
	}
}

func (r *reader) object(depth int) (*value, error) {
	v := &value{kind: kindObject, fields: map[string]*value{}}
	r.pos++ // {
	r.space()
	if r.pos < len(r.data) && r.data[r.pos] == '}' {
		r.pos++
		return v, nil
	}
	for {
		r.space()
		if r.pos >= len(r.data) || r.data[r.pos] != '"' {
			return nil, r.fail("a key")
		}
		key, err := r.string()
		if err != nil {
			return nil, err
		}
		if _, seen := v.fields[key]; seen {
			return nil, fmt.Errorf("it names the key %q twice in one object", key)
		}
		r.space()
		if r.pos >= len(r.data) || r.data[r.pos] != ':' {
			return nil, r.fail("a colon")
		}
		r.pos++
		r.space()
		field, err := r.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.keys = append(v.keys, key)
		v.fields[key] = field
		r.space()
		if r.pos >= len(r.data) {
			return nil, r.fail("a comma or a closing brace")
		}
		switch r.data[r.pos] {
		case ',':
			r.pos++
		case '}':
			r.pos++
			return v, nil
		default:
			return nil, r.fail("a comma or a closing brace")
		}
	}
}

func (r *reader) array(depth int) (*value, error) {
	v := &value{kind: kindArray}
	r.pos++ // [
	r.space()
	if r.pos < len(r.data) && r.data[r.pos] == ']' {
		r.pos++
		return v, nil
	}
	for {
		r.space()
		item, err := r.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.items = append(v.items, item)
		r.space()
		if r.pos >= len(r.data) {
			return nil, r.fail("a comma or a closing bracket")
		}
		switch r.data[r.pos] {
		case ',':
			r.pos++
		case ']':
			r.pos++
			return v, nil
		default:
			return nil, r.fail("a comma or a closing bracket")
		}
	}
}

// string reads one JSON string. An escape that names half of a UTF-16
// surrogate pair alone names no character, so it is refused rather than
// read as U+FFFD, the way encoding/json reads it.
func (r *reader) string() (string, error) {
	r.pos++ // "
	var out []byte
	for {
		if r.pos >= len(r.data) {
			return "", r.fail("the end of a string")
		}
		c := r.data[r.pos]
		switch {
		case c == '"':
			r.pos++
			return string(out), nil
		case c < 0x20:
			return "", fmt.Errorf("it holds the control character %q unescaped in a string, at byte %d", c, r.pos)
		case c != '\\':
			out = append(out, c)
			r.pos++
			continue
		}
		r.pos++ // the backslash
		if r.pos >= len(r.data) {
			return "", r.fail("an escape")
		}
		e := r.data[r.pos]
		r.pos++
		switch e {
		case '"', '\\', '/':
			out = append(out, e)
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			rn, err := r.hex4()
			if err != nil {
				return "", err
			}
			if utf16.IsSurrogate(rn) {
				if rn >= 0xdc00 || len(r.data)-r.pos < 2 || r.data[r.pos] != '\\' || r.data[r.pos+1] != 'u' {
					return "", fmt.Errorf("it escapes half of a surrogate pair alone, at byte %d", r.pos)
				}
				r.pos += 2
				low, err := r.hex4()
				if err != nil {
					return "", err
				}
				rn = utf16.DecodeRune(rn, low)
				if rn == utf8.RuneError {
					return "", fmt.Errorf("it escapes half of a surrogate pair alone, at byte %d", r.pos)
				}
			}
			out = utf8.AppendRune(out, rn)
		default:
			return "", fmt.Errorf("it holds the escape \\%c, which JSON does not have, at byte %d", e, r.pos-1)
		}
	}
}

func (r *reader) hex4() (rune, error) {
	if len(r.data)-r.pos < 4 {
		return 0, r.fail("four hex digits")
	}
	n, err := strconv.ParseUint(string(r.data[r.pos:r.pos+4]), 16, 32)
	if err != nil {
		return 0, r.fail("four hex digits")
	}
	r.pos += 4
	return rune(n), nil
}

// number reads one JSON number, keeping its text.
func (r *reader) number() (*value, error) {
	start := r.pos
	if r.data[r.pos] == '-' {
		r.pos++
	}
	switch {
	case r.pos < len(r.data) && r.data[r.pos] == '0':
		r.pos++
	case r.pos < len(r.data) && r.data[r.pos] >= '1' && r.data[r.pos] <= '9':
		r.digits()
	default:
		return nil, r.fail("a digit")
	}
	if r.pos < len(r.data) && r.data[r.pos] == '.' {
		r.pos++
		if !r.digits() {
			return nil, r.fail("a digit")
		}
	}
	if r.pos < len(r.data) && (r.data[r.pos] == 'e' || r.data[r.pos] == 'E') {
		r.pos++
		if r.pos < len(r.data) && (r.data[r.pos] == '+' || r.data[r.pos] == '-') {
			r.pos++
		}
		if !r.digits() {
			return nil, r.fail("a digit")
		}
	}
	return &value{kind: kindNumber, text: string(r.data[start:r.pos])}, nil
}

func (r *reader) digits() bool {
	start := r.pos
	for r.pos < len(r.data) && r.data[r.pos] >= '0' && r.data[r.pos] <= '9' {
		r.pos++
	}
	return r.pos > start
}
