package main

import (
	"errors"
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

// fakeRepository holds the symbolic refs of a repository and no objects.
type fakeRepository struct{ symrefs map[string]bool }

func (f fakeRepository) symbolic(ref string) (bool, error) { return f.symrefs[ref], nil }
func (f fakeRepository) notes(string) (map[string][]byte, error) {
	return nil, errors.New("holds no objects")
}

// Every rule that applies to a ref has to allow the write, and none of
// them can be satisfied by another. Each case here is a declaration that
// tries to stand in for a rule it is not: a writer list covering a
// namespace whose rule is a grant or a prohibition, a grant covering one
// whose rule is fixed, and a ref name standing for another ref.
func TestNoRuleStandsInForAnother(t *testing.T) {
	const zero = "0000000000000000000000000000000000000000"
	const some = "1111111111111111111111111111111111111111"
	p := policy{
		project: "everything",
		push: pushPolicy{
			// Every ref the project has is protected, and the writer is
			// also opened every ref by a grant.
			Refs:    []string{"refs/*"},
			Writers: []string{"writer"},
			Grants: map[string]refGrant{
				"everything": {Refs: []string{"refs/*"}, Writers: []string{"writer", "opened"}},
			},
		},
		grants: grants{"requester": {"request": true}},
		repo:   fakeRepository{symrefs: map[string]bool{"refs/heads/alias": true}},
	}
	for _, c := range []struct {
		who, ref string
		allowed  bool
		says     string
	}{
		// Prohibited outright, to the writer the grant and the protection
		// both name.
		{"writer", "refs/replace/" + some, false, "refs/replace/ is closed to every push"},
		{"writer", "refs/notes/commits", false, "refs/notes/ is closed to every push"},
		{"writer", "refs/the-valley/integration-outcomes/main/c", false, "written only by the machinery"},
		// A symbolic ref is refused whoever writes it.
		{"writer", "refs/heads/alias", false, "symbolic ref"},
		// The request grant is needed whatever else is declared, and
		// protection is needed on top of it.
		{"writer", "refs/the-valley/integration-requests/main/c", false, "request grant"},
		{"requester", "refs/the-valley/integration-requests/main/c", false, "a protected ref"},
		// A tag takes a grant, and protection on top: being opened the
		// ref does not make a principal its writer.
		{"writer", "refs/tags/v1", true, ""},
		{"opened", "refs/tags/v1", false, "a protected ref"},
		{"requester", "refs/tags/v1", false, "opens only through a named grant"},
		// An attestation named for a digest alone is refused before
		// anything is read: it would block every ref beneath it.
		{"writer", "refs/the-valley/attestations/" + strings.Repeat("a", 64), false, "<digest>/<key hash>"},
	} {
		got := p.decide(c.who, update{old: zero, new: some, ref: c.ref})
		if (got == "") != c.allowed || !strings.Contains(got, c.says) {
			t.Errorf("%s writing %s: got %q, want allowed=%v saying %q", c.who, c.ref, got, c.allowed, c.says)
		}
	}
}
