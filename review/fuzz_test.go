package review

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The review parser reads bytes anyone with review authority can push.
// Whatever they are, it returns a review or an error and never panics.
// What it accepts is a review Encode writes back unchanged, and is never
// something encoding/json reads differently.
func FuzzParse(f *testing.F) {
	f.Add([]byte(text()))
	f.Add([]byte(withdrawalText()))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema": "the-valley/review/v1", "schema": "x"}`))
	f.Add([]byte(strings.Replace(text(), `"verdict"`, `"VERDICT"`, 1)))
	f.Add([]byte(strings.Replace(text(), `"version": 2`, `"version": 2e0`, 1)))
	f.Add([]byte(strings.Replace(text(), "Split this", `😀 é`, 1)))
	f.Add([]byte(text() + "{}"))
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
			t.Fatalf("a review Parse accepted cannot be encoded: %v", err)
		}
		again, err := Parse(out)
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Fatalf("an encoded review read back as %+v, %v; want %+v", again, err, r)
		}
		// encoding/json reads what this parser accepted as the same
		// review: none of the leniencies it has is reachable here.
		var lenient Record
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&lenient); err != nil || !reflect.DeepEqual(&lenient, r) {
			t.Fatalf("encoding/json read the review as %+v, %v; want %+v", lenient, err, r)
		}
	})
}
