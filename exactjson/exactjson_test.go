package exactjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// A registry-shaped document: a struct at the top, maps of structs, a
// slice of structs, a pointer, and a field read by nobody.
type doc struct {
	Principals map[string]principal `json:"principals"`
	Genesis    string               `json:"genesis"`
	Derived    json.RawMessage      `json:"derived"`
	Count      int64                `json:"count"`
}

type principal struct {
	Keys   []key            `json:"keys"`
	Grants map[string]grant `json:"grants"`
	Notes  any              `json:"notes"`
	hidden string
	Bare   string
}

type key struct {
	Public string `json:"public"`
	Proof  *proof `json:"proof,omitempty"`
}

type proof struct {
	Slot string `json:"slot"`
}

type grant struct {
	Boundary string `json:"boundary"`
}

// A document spelled exactly decodes as encoding/json decodes it. Map keys
// are data, whatever their case, and so are the keys inside an interface
// and a json.RawMessage. A number past a float64's range is read only by
// the decode.
func TestAnExactDocumentDecodesAsEncodingJSONDoes(t *testing.T) {
	const data = `{
		"principals": {
			"alice": {
				"keys": [{"public": "k1", "proof": {"slot": "s"}}, {"public": "k2"}],
				"grants": {"push": {"boundary": "b"}, "Push": {"boundary": "B"}},
				"notes": {"Anything": [{"goes": 1}]}
			},
			"Alice": {"keys": [], "grants": {}}
		},
		"genesis": "alice",
		"derived": {"Grants": [1, {"x": 2}]},
		"count": 9007199254740993
	}`
	var got, want doc
	if err := Decode([]byte(data), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(data), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode read %+v, encoding/json %+v", got, want)
	}
}

// Every key a struct does not name exactly is refused, at every depth,
// before anything is decoded. encoding/json would have read each of the
// spellings below into the field it folds to, under Unicode's simple case
// folding: ſ folds to s, and the Kelvin sign to k.
func TestAKeyNotSpelledExactlyIsRefused(t *testing.T) {
	for _, c := range []struct{ name, data, says string }{
		{"a field in another case, beside the field",
			`{"principals": {"alice": {"grants": {}, "Grants": {"push": {"boundary": "b"}}}}}`,
			`principals["alice"] holds the key "Grants", which is its field "grants" spelled another way`},
		{"at the top",
			`{"genesis": "alice", "GENESIS": "mallory"}`,
			`the document holds the key "GENESIS", which is its field "genesis" spelled another way`},
		{"in a map's struct value",
			`{"principals": {"alice": {"grants": {"push": {"boundary": "x", "Boundary": "b"}}}}}`,
			`principals["alice"].grants["push"] holds the key "Boundary", which is its field "boundary" spelled another way`},
		{"in a slice's element",
			`{"principals": {"alice": {"keys": [{"public": "k1"}, {"public": "k2", "Public": "k3"}]}}}`,
			`principals["alice"].keys[1] holds the key "Public", which is its field "public" spelled another way`},
		{"behind a pointer",
			`{"principals": {"alice": {"keys": [{"public": "k1", "proof": {"sLot": "s"}}]}}}`,
			`principals["alice"].keys[0].proof holds the key "sLot", which is its field "slot" spelled another way`},
		{"folded beyond ASCII",
			`{"principals": {"alice": {"grantſ": {"push": {"boundary": "b"}}}}}`,
			`principals["alice"] holds the key "grantſ", which is its field "grants" spelled another way`},
		{"with the Kelvin sign",
			"{\"principals\": {\"alice\": {\"Keys\": []}}}",
			`principals["alice"] holds the key "` + "K" + `eys", which is its field "keys" spelled another way`},
		{"a field no tag names",
			`{"principals": {"alice": {"Bare": "x"}}}`,
			`principals["alice"] holds the key "Bare", which is none of its fields`},
		{"an unexported field",
			`{"principals": {"alice": {"hidden": "x"}}}`,
			`principals["alice"] holds the key "hidden", which is none of its fields`},
		{"a field nobody declared",
			`{"genesis": "alice", "extra": true}`,
			`the document holds the key "extra", which is none of its fields`},
	} {
		var d doc
		err := Decode([]byte(c.data), &d)
		if err == nil || err.Error() != c.says {
			t.Errorf("%s: Decode read %+v, %v, not %q", c.name, d, err, c.says)
		}
		if !reflect.DeepEqual(d, doc{}) {
			t.Errorf("%s: a refused document decoded %+v", c.name, d)
		}
	}
}

// A key that appears twice in one object is refused wherever the object
// is, a map's and a raw value's included, and however the key is escaped.
// So is anything after the document.
func TestAKeyTwiceOrASecondDocumentIsRefused(t *testing.T) {
	for _, c := range []struct{ name, data, says string }{
		{"at the top",
			`{"principals": {}, "principals": {"mallory": {}}}`,
			`the document holds the key "principals" twice`},
		{"in a map",
			`{"principals": {"alice": {}, "alice": {"grants": {"push": {"boundary": "b"}}}}}`,
			`principals holds the key "alice" twice`},
		{"in a struct in a map",
			`{"principals": {"alice": {"grants": {"push": {"boundary": "x", "boundary": "b"}}}}}`,
			`principals["alice"].grants["push"] holds the key "boundary" twice`},
		{"escaped",
			`{"genesis": "alice", "\u0067enesis": "mallory"}`,
			`the document holds the key "genesis" twice`},
		{"in a raw value",
			`{"derived": [{"a": 1, "a": 2}]}`,
			`derived[0] holds the key "a" twice`},
		{"in an interface",
			`{"principals": {"alice": {"notes": {"n": {"x": 1, "x": 2}}}}}`,
			`principals["alice"].notes["n"] holds the key "x" twice`},
		{"a second document",
			`{"genesis": "alice"} {"genesis": "mallory"}`,
			`the document holds more than one JSON value`},
	} {
		var d doc
		if err := Decode([]byte(c.data), &d); err == nil || err.Error() != c.says {
			t.Errorf("%s: Decode read %+v, %v, not %q", c.name, d, err, c.says)
		}
	}
	if err := Decode([]byte(`{}`), doc{}); err == nil || !strings.Contains(err.Error(), "non-pointer") {
		t.Errorf("Decode into a value: %v", err)
	}
}

// Structs that embed a struct, as a value, as a pointer, and under a json
// tag name.
type embeds struct {
	grant
	Name string `json:"name"`
}

type embedsPointer struct {
	*grant
}

type embedsTagged struct {
	Extra `json:"extra"`
}

type Extra struct {
	Slot string `json:"slot"`
}

// encoding/json reads the fields of an embedded struct as the embedding
// struct's own, and Decode does not follow it there: it refuses any
// object read into a struct that embeds one, at any depth, whatever the
// object holds. A struct embedded under a json tag name is an ordinary
// field.
func TestAnEmbeddedStructIsRefused(t *testing.T) {
	var e embeds
	if err := json.Unmarshal([]byte(`{"boundary": "b"}`), &e); err != nil || e.Boundary != "b" {
		t.Fatalf("encoding/json read %+v, %v", e, err)
	}
	for _, c := range []struct {
		name, data, says string
		v                any
	}{
		{"a struct", `{"boundary": "b"}`,
			`the document is read into exactjson.embeds, which embeds exactjson.grant, and exactjson reads no embedded struct`,
			&embeds{}},
		{"an empty object", `{}`,
			`the document is read into exactjson.embeds, which embeds exactjson.grant, and exactjson reads no embedded struct`,
			&embeds{}},
		{"a pointer, in a map", `{"push": {"boundary": "b"}}`,
			`["push"] is read into exactjson.embedsPointer, which embeds *exactjson.grant, and exactjson reads no embedded struct`,
			&map[string]embedsPointer{}},
	} {
		if err := Decode([]byte(c.data), c.v); err == nil || err.Error() != c.says {
			t.Errorf("%s: Decode read %+v, %v, not %q", c.name, c.v, err, c.says)
		}
	}
	var tagged embedsTagged
	if err := Decode([]byte(`{"extra": {"slot": "s"}}`), &tagged); err != nil || tagged.Slot != "s" {
		t.Errorf("a struct embedded under a tag name: Decode read %+v, %v", tagged, err)
	}
	if err := Decode([]byte(`{"slot": "s"}`), &embedsTagged{}); err == nil {
		t.Errorf("a struct embedded under a tag name: Decode read its field's key as the embedding struct's")
	}
}

// An array or object read into a type that holds none is refused where it
// is, before anything inside it is read. encoding/json would refuse each
// of these too, but only after reading the whole document.
func TestAContainerOfTheWrongKindIsRefused(t *testing.T) {
	for _, c := range []struct{ name, data, says string }{
		{"an array for the document",
			`[{"genesis": "alice"}]`,
			`the document is an array, and exactjson.doc holds no array`},
		{"an array for a map",
			`{"principals": [{"keys": []}]}`,
			`principals is an array, and map[string]exactjson.principal holds no array`},
		{"an object for a string",
			`{"genesis": {"Genesis": "alice"}}`,
			`genesis is an object, and string holds no object`},
		{"an object for a slice",
			`{"principals": {"alice": {"keys": {"public": "k1"}}}}`,
			`principals["alice"].keys is an object, and []exactjson.key holds no object`},
		{"an array for a number",
			`{"count": [1]}`,
			`count is an array, and int64 holds no array`},
		{"an array behind a pointer",
			`{"principals": {"alice": {"keys": [{"public": "k1", "proof": []}]}}}`,
			`principals["alice"].keys[0].proof is an array, and exactjson.proof holds no array`},
	} {
		var d doc
		if err := Decode([]byte(c.data), &d); err == nil || err.Error() != c.says {
			t.Errorf("%s: Decode read %+v, %v, not %q", c.name, d, err, c.says)
		}
		if err := json.Unmarshal([]byte(c.data), &doc{}); err == nil {
			t.Errorf("%s: encoding/json reads the document", c.name)
		}
	}
}

// Arrays and objects may nest MaxDepth deep, counting the document's own,
// and no deeper. Only a raw value or an interface nests freely enough to
// reach the bound.
func TestNestingPastMaxDepthIsRefused(t *testing.T) {
	nested := func(depth int) string {
		return `{"derived": ` + strings.Repeat("[", depth-1) + strings.Repeat("]", depth-1) + `}`
	}
	var d doc
	if err := Decode([]byte(nested(MaxDepth)), &d); err != nil {
		t.Errorf("a document %d deep: %v", MaxDepth, err)
	}
	says := fmt.Sprintf("the document nests arrays and objects more than %d deep", MaxDepth)
	if err := Decode([]byte(nested(MaxDepth+1)), &d); err == nil || err.Error() != says {
		t.Errorf("a document %d deep: Decode read %v, not %q", MaxDepth+1, err, says)
	}
	var v any
	if err := Decode([]byte(strings.Repeat(`{"a":`, MaxDepth)+"1"+strings.Repeat("}", MaxDepth)), &v); err != nil {
		t.Errorf("objects %d deep in an interface: %v", MaxDepth, err)
	}
	if err := Decode([]byte(strings.Repeat(`{"a":`, MaxDepth+1)+"1"+strings.Repeat("}", MaxDepth+1)), &v); err == nil || err.Error() != says {
		t.Errorf("objects %d deep in an interface: Decode read %v, not %q", MaxDepth+1, err, says)
	}
}

// counted counts the bytes read from r.
type counted struct {
	r io.Reader
	n int
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// A million brackets around null is refused at the first bracket the type
// cannot hold, or at the bracket past MaxDepth, whichever comes first.
// The check reads no further than that, and what it holds while it reads
// grows with the depth it reaches, not with the document.
func TestDeepBracketsAreRefusedEarly(t *testing.T) {
	const n = 1 << 20
	brackets := strings.Repeat("[", n) + "null" + strings.Repeat("]", n)
	deep := fmt.Sprintf("the document nests arrays and objects more than %d deep", MaxDepth)
	for _, c := range []struct {
		name, data, says string
		v                any
	}{
		{"a struct", brackets,
			`the document is an array, and exactjson.doc holds no array`, &doc{}},
		{"a slice of structs", brackets,
			`[0] is an array, and exactjson.doc holds no array`, &[]doc{}},
		{"an interface", brackets, deep, new(any)},
		{"a raw value", brackets, deep, &json.RawMessage{}},
		{"a raw value in a struct", `{"derived": ` + brackets + `}`, deep, &doc{}},
	} {
		in := []byte(c.data)
		r := &counted{r: bytes.NewReader(in)}
		if err := Check(json.NewDecoder(r), reflect.TypeOf(c.v)); err == nil || err.Error() != c.says {
			t.Errorf("%s: Check read %v, not %q", c.name, err, c.says)
		}
		if r.n > 64<<10 {
			t.Errorf("%s: Check read %d bytes of %d", c.name, r.n, len(in))
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := Decode(in, c.v); err == nil || err.Error() != c.says {
			t.Errorf("%s: Decode read %v, not %q", c.name, err, c.says)
		}
		runtime.ReadMemStats(&after)
		if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
			t.Errorf("%s: Decode allocated %d bytes", c.name, grew)
		}
	}
}
