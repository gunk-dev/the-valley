package change

import "testing"

// The choice SelectNotes makes is the contract the integrator and approve
// share, so each part of it is pinned: the first note that verifies is
// kept whatever it records, and until one verifies the last note read
// stands.
func TestSelectNotes(t *testing.T) {
	type note struct {
		check, what string
		verifies    bool
	}
	notes := []note{
		{"a", "a forgery", false},
		{"a", "a failure that verifies", true},
		{"a", "a pass that verifies", true},
		{"b", "a pass that verifies", true},
		{"b", "a forgery", false},
		{"c", "a forgery", false},
		{"c", "an unknown signer", false},
	}
	got := SelectNotes(notes, func(n note) string { return n.check }, func(n note) bool { return n.verifies })
	want := map[string]string{
		"a": "a failure that verifies",
		"b": "a pass that verifies",
		"c": "an unknown signer",
	}
	if len(got) != len(want) {
		t.Fatalf("picked %d checks, want %d: %v", len(got), len(want), got)
	}
	for check, what := range want {
		if got[check].what != what {
			t.Errorf("check %s: picked %q, want %q", check, got[check].what, what)
		}
	}
}
