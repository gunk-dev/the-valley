package pushpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

// The patterns are documented as matching the full refname with `*`
// crossing path separators, and the hook they replace matched them as
// shell case patterns. A matcher is wrong exactly at the stars, so the
// stars are what is pinned.
func TestAPatternMatchesTheWholeRefnameAndItsStarsCrossSeparators(t *testing.T) {
	for _, c := range []struct {
		pattern, ref string
		want         bool
	}{
		{"refs/heads/main", "refs/heads/main", true},
		{"refs/heads/main", "refs/heads/main2", false},
		{"refs/heads/main", "refs/heads/mai", false},
		{"refs/heads/release/*", "refs/heads/release/1.0", true},
		{"refs/heads/release/*", "refs/heads/release/1/hotfix", true},
		{"refs/heads/release/*", "refs/heads/release/", true},
		{"refs/heads/release/*", "refs/heads/releases/1", false},
		{"refs/tags/release/*/v*", "refs/tags/release/app/v1", true},
		{"refs/tags/release/*/v*", "refs/tags/release/app/nightly", false},
		{"refs/*/main", "refs/heads/x/main", true},
		{"refs/*/main", "refs/heads/x/main/y", false},
		{"refs/heads/*a*b", "refs/heads/aab", true},
		{"refs/heads/*a*b", "refs/heads/aaba", false},
	} {
		if got := Match(c.pattern, c.ref); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.ref, got, c.want)
		}
	}
}

func write(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "push-policy.json")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A policy reads as the host module writes it, and answers the two
// questions a reader of approvals asks of it: whether the project retains
// its change records, and whether a target is protected with no writer.
// Left out, retention is off.
func TestAPolicyReadsAsTheHostWritesIt(t *testing.T) {
	for _, c := range []struct {
		json           string
		retains        bool
		sealed, guards bool
	}{
		{`{"grants":{},"refs":["refs/heads/main"],"writers":[]}`, false, true, true},
		{`{"grants":{},"refs":["refs/heads/main"],"retainChangeRecords":true,"writers":[]}`, true, true, true},
		{`{"grants":{},"refs":["refs/heads/*"],"writers":[]}`, false, true, true},
		{`{"grants":{},"refs":["refs/heads/main"],"writers":["integrator"]}`, false, false, true},
		{`{"grants":{},"refs":["refs/heads/release/*"],"writers":[]}`, false, false, false},
		{`{"grants":{"release-tags":{"refs":["refs/heads/main"],"writers":["integrator"]}},"refs":[],"writers":[]}`, false, false, false},
	} {
		p, err := Read(write(t, c.json))
		if err != nil {
			t.Errorf("%s: %v", c.json, err)
			continue
		}
		if p.RetainChangeRecords != c.retains || p.Sealed("refs/heads/main") != c.sealed || p.Protects("refs/heads/main") != c.guards {
			t.Errorf("%s: retains %v, seals main %v, protects main %v", c.json, p.RetainChangeRecords, p.Sealed("refs/heads/main"), p.Protects("refs/heads/main"))
		}
	}
}

// A policy holding a field this package does not know, a key spelled
// another way than its field, a key twice in one object, or a value of
// another kind does not read, and neither does a file that is not there.
// Each refusal names the file and says what is wrong in words, never in
// Go's type names, which the hook passes on to the pusher.
func TestAPolicyThatDoesNotReadIsRefused(t *testing.T) {
	for _, c := range []struct{ what, text, says string }{
		{"an unknown field", `{"grants":{},"refs":[],"writers":[],"sealed":true}`,
			`the document holds the key "sealed", which is none of its fields`},
		{"Writers beside writers", `{"grants":{},"refs":["refs/heads/main"],"writers":[],"Writers":["anyone"]}`,
			`the document holds the key "Writers", which is its field "writers" spelled another way`},
		{"writers twice", `{"grants":{},"refs":["refs/heads/main"],"writers":[],"writers":["anyone"]}`,
			`the document holds the key "writers" twice`},
		{"retention as a string", `{"grants":{},"refs":[],"writers":[],"retainChangeRecords":"true"}`,
			`retainChangeRecords is a string, where a boolean belongs`},
		{"refs as a string", `{"grants":{},"refs":"refs/heads/main","writers":[]}`,
			`refs is a string, where an array belongs`},
		{"grants as an array", `{"grants":[],"refs":[],"writers":[]}`,
			`grants is an array, where an object belongs`},
		{"a grant's refs as an object", `{"grants":{"g":{"refs":{},"writers":[]}},"refs":[],"writers":[]}`,
			`grants["g"].refs is an object, where an array belongs`},
		{"the document as an array", `[]`, `the document is an array, where an object belongs`},
		{"a grant's unknown field", `{"grants":{"g":{"refs":[],"writers":[],"readers":[]}},"refs":[],"writers":[]}`,
			`grants["g"] holds the key "readers", which is none of its fields`},
		{"a second document after one", `{"grants":{},"refs":[],"writers":[]}{}`,
			`the document holds more than one JSON value`},
	} {
		path := write(t, c.text)
		p, err := Read(path)
		if err == nil || err.Error() != path+": "+c.says {
			t.Errorf("%s read as %+v, %v, not %q", c.what, p, err, c.says)
		}
	}
	if p, err := Read(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Errorf("a missing policy read as %+v", p)
	}
}
