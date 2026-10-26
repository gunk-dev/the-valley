package exactjson

import (
	"encoding/json"
	"reflect"
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
			`{"genesis": "alice", "genesis": "mallory"}`,
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
