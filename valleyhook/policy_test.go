package main

import (
	"strings"
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
		{"refs/tags/release/*/v*", "refs/tags/release/cosmo/v1", true},
		{"refs/tags/release/*/v*", "refs/tags/release/cosmo/nightly", false},
		{"refs/*/main", "refs/heads/x/main", true},
		{"refs/*/main", "refs/heads/x/main/y", false},
		{"refs/heads/*a*b", "refs/heads/aab", true},
		{"refs/heads/*a*b", "refs/heads/aaba", false},
	} {
		if got := match(c.pattern, c.ref); got != c.want {
			t.Errorf("match(%q, %q) = %v, want %v", c.pattern, c.ref, got, c.want)
		}
	}
}

// The rules are tried in order, and the order is what keeps a declaration
// from opening more than it says. Each case here is one where a later rule
// would answer differently from the one that decides.
func TestTheFirstRuleThatMatchesDecides(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	const some = "1111111111111111111111111111111111111111"
	p := policy{
		project: "everything",
		protection: protection{
			// Every ref the project has is protected, so only the fixed
			// rules ahead of protection can say anything different.
			Refs:    []string{"refs/*"},
			Writers: []string{"writer"},
			Allow: []opening{
				{Refs: []string{"refs/*"}, Writers: []string{"opened"}},
			},
		},
		grants: grants{"requester": {"request": true}},
	}
	for _, c := range []struct {
		who, ref, old string
		allowed       bool
		says          string
	}{
		// No declaration reaches a replacement ref, not even a writer of
		// a pattern that covers it.
		{"writer", "refs/replace/" + some, zero, false, "refs/replace/ is closed to every push"},
		// Attestations are create-only for everyone, ahead of protection.
		{"anyone", "refs/the-valley/attestations/d/k", zero, true, ""},
		{"writer", "refs/the-valley/attestations/d/k", some, false, "create-only"},
		// A protected ref is its writers' alone: neither an allow entry
		// nor the request grant opens it.
		{"writer", "refs/tags/v1", zero, true, ""},
		{"opened", "refs/tags/v1", zero, false, "writers: writer"},
		{"requester", "refs/the-valley/integration-requests/main/c", zero, false, "a protected ref"},
	} {
		got := p.decide(c.who, update{old: c.old, new: some, ref: c.ref})
		if (got == "") != c.allowed || !strings.Contains(got, c.says) {
			t.Errorf("%s writing %s: got %q, want allowed=%v saying %q", c.who, c.ref, got, c.allowed, c.says)
		}
	}

	// With nothing protected, the request namespace answers before an
	// allow entry can: opening it by declaration would be a request grant
	// nobody holds in the registry.
	p.protection.Refs = nil
	for _, c := range []struct {
		who     string
		allowed bool
	}{
		{"requester", true},
		{"opened", false},
		{"", false},
	} {
		got := p.decide(c.who, update{old: zero, new: some, ref: "refs/the-valley/integration-requests/main/c"})
		if (got == "") != c.allowed {
			t.Errorf("%q filing a request: got %q, want allowed=%v", c.who, got, c.allowed)
		}
	}
}
