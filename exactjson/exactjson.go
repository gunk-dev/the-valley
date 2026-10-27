// Package exactjson decodes one JSON document into a Go value, and first
// refuses any key that the value's type does not name exactly.
//
// encoding/json matches an object key to a struct field without regard to
// case, and DisallowUnknownFields does not change that. cue keeps keys that
// differ only in case apart, and so does Nix. A CUE document may reopen a
// definition of the schema it is checked against, and add a field to it.
// So a document can carry Grants beside an empty grants, and encoding/json
// reads the first into the second: a reviewer reading the canonical field
// sees nothing, and the program reads a grant.
//
// A key that appears twice in one object is the same problem in another
// form. encoding/json keeps the last of two values, and merges two objects
// read into one map or struct, so a reader of the first sees less than the
// program reads.
//
// The check also refuses an array or object that the value's type cannot
// hold, before it reads inside it, and arrays and objects nested past
// MaxDepth. So a malformed document costs the check no more than the part
// of it read before the refusal, however deeply it nests.
//
// The package depends on the standard library alone.
package exactjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// MaxDepth is how deeply arrays and objects may nest in a value Decode or
// Check reads. The value's own array or object is one deep, and an array
// or object inside it two. It is far deeper than any document the valley
// reads nests. encoding/json stops at 10000.
const MaxDepth = 1000

// Decode decodes data into v, which must be a non-nil pointer. Before it
// decodes anything, it reads every object in data, at every depth, and
// refuses:
//
//   - a key of an object read into a struct that is not the name a json
//     tag of that struct gives, spelled exactly;
//   - a key that appears twice in one object;
//   - an array read into a type that is not a slice or an array, or an
//     object read into a type that is not a struct or a map;
//   - arrays and objects nested more than MaxDepth deep;
//   - anything after the first JSON value.
//
// It refuses an array or object of the wrong kind before it reads inside
// it, so a document that is refused reads no further than the value that
// is refused.
//
// A struct field takes a key only through the name its json tag gives, so
// a field with no tag name takes none. The keys of an object read into a
// map, an interface or a type that decodes itself, such as a
// json.RawMessage, are data, and any key passes: a map's values are held
// to its element type, and an interface's or a json.RawMessage's are
// checked for repeated keys and depth alone.
//
// Decode does not read embedded structs. encoding/json reads the fields of
// a struct embedded with no json tag name as fields of the struct that
// embeds it, chosen by Go's rules for selecting a field, and the check
// does not follow those rules. So Decode refuses any object read into a
// struct that embeds a struct, or a pointer to one, with no json tag name.
// A struct embedded under a json tag name is an ordinary field.
func Decode(data []byte, v any) error {
	t := reflect.TypeOf(v)
	if t == nil || t.Kind() != reflect.Pointer {
		return &json.InvalidUnmarshalError{Type: t}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// The check reads numbers as written, so a number the decode reads
	// into an int64 or a json.Number is not refused for overflowing a
	// float64.
	dec.UseNumber()
	if err := Check(dec, t); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("the document holds more than one JSON value")
	}
	dec = json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Tokens is a stream of JSON tokens, as a *json.Decoder reads them.
type Tokens interface {
	Token() (json.Token, error)
	More() bool
}

// Check reads one JSON value from tokens, and holds it to t, the type the
// value is decoded into, by the rules Decode holds a document to: a key of
// an object read into a struct is the name a json tag of that struct
// gives, spelled exactly, no key appears twice in one object, every array
// and object is read into a type that holds one, and no array or object
// is nested more than MaxDepth deep. It reads nothing after the value.
//
// Check is for a caller that decodes with a token reader of its own, such
// as one that stops when a context is cancelled. Such a caller checks the
// value with Check first, and decodes it only when Check passes it. A
// token reader that reads numbers as json.Number keeps a number that
// overflows a float64 from failing the check.
func Check(tokens Tokens, t reflect.Type) error {
	return (&checker{tokens: tokens}).check(t)
}

var (
	anything    = reflect.TypeFor[any]()
	unmarshaler = reflect.TypeFor[json.Unmarshaler]()
)

// checker reads one value from tokens. path holds a step for each array
// and object the value being read is in, so its length is the value's
// depth, and the value's place is written out only for an error.
type checker struct {
	tokens Tokens
	path   []step
}

// step is where a value is in the array or object that holds it: at index
// in an array, or under key in an object, with an index of -1. field is
// true when the object is read into a struct, so key names a field.
type step struct {
	index int
	key   string
	field bool
}

// check reads one value, and holds every array and object in it to t,
// the type the value is decoded into.
func (c *checker) check(t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	// The check cannot tell how a type that decodes itself reads its
	// value, so it reads the value as encoding/json reads one into an
	// interface.
	if reflect.PointerTo(t).Implements(unmarshaler) {
		t = anything
	}
	tok, err := c.tokens.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('['):
		if len(c.path) == MaxDepth {
			return tooDeep()
		}
		var elem reflect.Type
		switch t.Kind() {
		case reflect.Slice, reflect.Array:
			elem = t.Elem()
		case reflect.Interface:
			elem = t
		default:
			return fmt.Errorf("%s is an array, and %s holds no array", c.where(), t)
		}
		for i := 0; c.tokens.More(); i++ {
			c.path = append(c.path, step{index: i})
			if err := c.check(elem); err != nil {
				return err
			}
			c.path = c.path[:len(c.path)-1]
		}
	case json.Delim('{'):
		if len(c.path) == MaxDepth {
			return tooDeep()
		}
		var fields map[string]reflect.Type
		switch t.Kind() {
		case reflect.Struct:
			if fields, err = tagged(t); err != nil {
				return fmt.Errorf("%s is read into %w", c.where(), err)
			}
		case reflect.Map, reflect.Interface:
		default:
			return fmt.Errorf("%s is an object, and %s holds no object", c.where(), t)
		}
		seen := map[string]bool{}
		for c.tokens.More() {
			tok, err := c.tokens.Token()
			if err != nil {
				return err
			}
			key := tok.(string)
			if seen[key] {
				return fmt.Errorf("%s holds the key %q twice", c.where(), key)
			}
			seen[key] = true
			elem := t
			switch t.Kind() {
			case reflect.Struct:
				var ok bool
				if elem, ok = fields[key]; !ok {
					return unknown(c.where(), key, fields)
				}
			case reflect.Map:
				elem = t.Elem()
			}
			c.path = append(c.path, step{index: -1, key: key, field: t.Kind() == reflect.Struct})
			if err := c.check(elem); err != nil {
				return err
			}
			c.path = c.path[:len(c.path)-1]
		}
	default:
		return nil
	}
	// The closing delimiter.
	_, err = c.tokens.Token()
	return err
}

// where names the place of the value being read, for a message: a field
// of a struct by its key after a dot, a map's or an interface's value by
// its key in brackets, and an array's element by its index.
func (c *checker) where() string {
	if len(c.path) == 0 {
		return "the document"
	}
	var b strings.Builder
	for _, s := range c.path {
		switch {
		case s.index >= 0:
			fmt.Fprintf(&b, "[%d]", s.index)
		case s.field && b.Len() > 0:
			fmt.Fprintf(&b, ".%s", s.key)
		case s.field:
			b.WriteString(s.key)
		default:
			fmt.Fprintf(&b, "[%q]", s.key)
		}
	}
	return b.String()
}

// tooDeep is the error for an array or object nested past MaxDepth.
func tooDeep() error {
	return fmt.Errorf("the document nests arrays and objects more than %d deep", MaxDepth)
}

// tagged maps the name each json tag of struct type t gives to the type
// of its field. Its error is for a t that embeds a struct with no json tag
// name, which Decode does not read.
func tagged(t reflect.Type) (map[string]reflect.Type, error) {
	fields := map[string]reflect.Type{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		embedded := f.Type
		if embedded.Kind() == reflect.Pointer {
			embedded = embedded.Elem()
		}
		if f.Anonymous && name == "" && embedded.Kind() == reflect.Struct {
			return nil, fmt.Errorf("%s, which embeds %s, and exactjson reads no embedded struct", t, f.Type)
		}
		if name != "" && name != "-" {
			fields[name] = f.Type
		}
	}
	return fields, nil
}

// unknown is the error for a key no field of the struct at where names.
// When the key is a field's name spelled another way, as encoding/json
// would have matched it, the error names that field too.
func unknown(where, key string, fields map[string]reflect.Type) error {
	for name := range fields {
		if strings.EqualFold(name, key) {
			return fmt.Errorf("%s holds the key %q, which is its field %q spelled another way", where, key, name)
		}
	}
	return fmt.Errorf("%s holds the key %q, which is none of its fields", where, key)
}
