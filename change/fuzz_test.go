package change

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The record parser reads bytes anyone can push. Whatever they are, it
// returns a record or an error and never panics. What it accepts is
// a record Encode writes back unchanged, and is never something
// encoding/json reads differently.
func FuzzParse(f *testing.F) {
	f.Add([]byte(record()))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema": "valley-change-record/v1", "schema": "x"}`))
	f.Add([]byte(strings.Replace(record(), "the first", `😀`, 1)))
	f.Add([]byte(strings.Replace(record(), `"seq": 2`, `"seq": 2e0`, 1)))
	f.Add([]byte(record() + "{}"))
	f.Add([]byte("[[[[[[[[[[[[[[[["))
	f.Add([]byte("\"\\u"))
	f.Add([]byte{0xff, '{', '}'})
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Parse(data)
		if err != nil {
			return
		}
		out, err := Encode(r)
		if err != nil {
			t.Fatalf("a record Parse accepted cannot be encoded: %v", err)
		}
		again, err := Parse(out)
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Fatalf("an encoded record read back as %+v, %v; want %+v", again, err, r)
		}
		// encoding/json reads what this parser accepted as the same
		// record: none of the leniencies it has is reachable here.
		var lenient Record
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&lenient); err != nil || !reflect.DeepEqual(&lenient, r) {
			t.Fatalf("encoding/json read the record as %+v, %v; want %+v", lenient, err, r)
		}
	})
}
