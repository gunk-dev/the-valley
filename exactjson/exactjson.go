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

// Decode decodes data into v, which must be a non-nil pointer. Before it
// decodes anything, it reads every object in data, at every depth, and
// refuses:
//
//   - a key of an object read into a struct that is not the name a json
//     tag of that struct gives, spelled exactly;
//   - a key that appears twice in one object;
//   - anything after the first JSON value.
//
// A struct field takes a key only through the name its json tag gives, so
// a field with no tag name takes none. The keys of an object read into a
// map, an interface or a json.RawMessage are data, and any key passes: a
// map's values are held to its element type, and the rest are checked for
// repeated keys alone.
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

// Check reads one JSON value from tokens, and holds every object in it to
// t, the type the value is decoded into, by the rules Decode holds a
// document to: a key of an object read into a struct is the name a json
// tag of that struct gives, spelled exactly, and no key appears twice in
// one object. It reads nothing after the value.
//
// Check is for a caller that decodes with a token reader of its own, such
// as one that stops when a context is cancelled. Such a caller checks the
// value with Check first, and decodes it only when Check passes it. A
// token reader that reads numbers as json.Number keeps a number that
// overflows a float64 from failing the check.
func Check(tokens Tokens, t reflect.Type) error { return check(tokens, t, "") }

// check reads one value from dec, found at path ("" for the document),
// and holds every object in it to t, the type the value is decoded into.
func check(dec Tokens, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('['):
		elem := t
		if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			elem = t.Elem()
		}
		for i := 0; dec.More(); i++ {
			if err := check(dec, elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case json.Delim('{'):
		var fields map[string]reflect.Type
		if t.Kind() == reflect.Struct {
			if fields, err = tagged(t); err != nil {
				return fmt.Errorf("%s is read into %w", where(path), err)
			}
		}
		seen := map[string]bool{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			key := tok.(string)
			if seen[key] {
				return fmt.Errorf("%s holds the key %q twice", where(path), key)
			}
			seen[key] = true
			elem, at := t, fmt.Sprintf("%s[%q]", path, key)
			switch t.Kind() {
			case reflect.Struct:
				var ok bool
				if elem, ok = fields[key]; !ok {
					return unknown(path, key, fields)
				}
				at = strings.TrimPrefix(path+"."+key, ".")
			case reflect.Map:
				elem = t.Elem()
			}
			if err := check(dec, elem, at); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	// The closing delimiter.
	_, err = dec.Token()
	return err
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

// unknown is the error for a key no field of the struct at path names.
// When the key is a field's name spelled another way, as encoding/json
// would have matched it, the error names that field too.
func unknown(path, key string, fields map[string]reflect.Type) error {
	for name := range fields {
		if strings.EqualFold(name, key) {
			return fmt.Errorf("%s holds the key %q, which is its field %q spelled another way", where(path), key, name)
		}
	}
	return fmt.Errorf("%s holds the key %q, which is none of its fields", where(path), key)
}

// where names path in a message.
func where(path string) string {
	if path == "" {
		return "the document"
	}
	return path
}
