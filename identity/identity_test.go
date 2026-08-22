package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// The key hash is the note format's own, and a compiler that gets it wrong
// writes verifier lines nothing can match. Two vectors pin it. The first is
// the one attest itself is held to (attest/attest_test.go): sum.golang.org
// publishes its verifier key, so the reference implementation has already
// computed the answer.
func TestKeyHashMatchesTheReferenceImplementation(t *testing.T) {
	const (
		name   = "sum.golang.org"
		public = "Ac4zctda0e5eza+HJyk9SxEdh+s3Ux18htTTAD8OuAn8"
		want   = "033de0ae"
	)
	raw, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal(err)
	}
	if got := verifierKey(name, raw[1:]); got != name+"+"+want+"+"+public {
		t.Errorf("verifierKey(%s) = %s, want the published line", name, got)
	}
}

// The second vector is the live one: the integrator's key, whose existing
// attestations are filed under the host's name and which is published under
// its own. The name is inside the hash, so one key under two names is two
// verifier keys — the reason a signing name is recorded per key.
func TestOneKeyUnderTwoNamesIsTwoVerifierKeys(t *testing.T) {
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINyd1zTLxcPuMDojZpjOdUxGj6AYl6RP7yiWb3Cd/fOQ"
	pub, err := sshEd25519Public(key)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"integrator":                  "integrator+78dfd4e6+Adyd1zTLxcPuMDojZpjOdUxGj6AYl6RP7yiWb3Cd/fOQ",
		"classic-laddie/attestations": "classic-laddie/attestations+9eb2dad8+Adyd1zTLxcPuMDojZpjOdUxGj6AYl6RP7yiWb3Cd/fOQ",
	} {
		if got := verifierKey(name, pub); got != want {
			t.Errorf("verifierKey(%s) = %s, want %s", name, got, want)
		}
	}
}

func TestPublicKeysThatAreNotEd25519AreRefused(t *testing.T) {
	for _, line := range []string{
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQ",
		"ssh-ed25519",
		"ssh-ed25519 not-base64!",
		// An ed25519 line whose blob names another algorithm.
		"ssh-ed25519 AAAAB3NzaC1yc2EAAAADAQABAAAAIHo0Oc728AfV2EMn30DhTWSqdWhmY8xR6np/qf6U7xvn",
	} {
		if _, err := sshEd25519Public(line); err == nil {
			t.Errorf("%q was read as an ed25519 key", line)
		}
	}
}

// Expiry is what makes a machine credential a revocation already scheduled
// (bd-8a591dc), so the day it lands on is worth pinning: the declared day is
// the first day the entry no longer counts.
func TestAnEntryExpiresOnTheDayItDeclares(t *testing.T) {
	for _, c := range []struct {
		expires string
		on      string
		want    bool
	}{
		{"2026-10-01", "2026-09-30", false},
		{"2026-10-01", "2026-10-01", true},
		{"2026-10-01", "2026-10-02", true},
		{"", "2999-01-01", false},
	} {
		got, err := hasExpired(c.expires, day(t, c.on))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("expires %q on %s: expired = %v, want %v", c.expires, c.on, got, c.want)
		}
	}
	if _, err := hasExpired("the first of never", day(t, "2026-01-01")); err == nil {
		t.Error("a malformed expiry compiled")
	}
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A grant naming a boundary the registry does not run must fail the render
// rather than compile into nothing: the schema refuses one, and a compiler
// that dropped it silently would turn a typo into a loss of access nobody
// asked for.
func TestAGrantAtAnUndeclaredBoundaryFailsTheRender(t *testing.T) {
	r := registry{
		Boundaries: map[string]boundary{"push": {Kind: boundaryGitPush}},
		Principals: map[string]principal{
			"someone": {
				Kind:   "human",
				Keys:   []key{{Public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINyd1zTLxcPuMDojZpjOdUxGj6AYl6RP7yiWb3Cd/fOQ"}},
				Grants: map[string]grant{"publish": {Boundary: "bus"}},
			},
		},
	}
	_, err := render(r, time.Now())
	if err == nil || !strings.Contains(err.Error(), "bus") {
		t.Errorf("render error = %v, want one naming the undeclared boundary", err)
	}
}

// The registry's own stream cannot be left in nobody's hand (dcr-2f03be3).
// The state is arrived at by the clock rather than declared, which is why
// the compiler refuses it and the schema cannot: one registry, unchanged,
// renders the day before its only governing entry expires and fails the day
// it does. The entry still holding a push grant does not save it — a
// boundary of another kind is not governance.
func TestARenderThatOrphansGovernanceIsRefused(t *testing.T) {
	r := governedRegistry()
	r.Principals["founder"] = expiring(r.Principals["founder"], "2026-10-01")
	if _, err := render(r, day(t, "2026-09-30")); err != nil {
		t.Fatalf("the day before the only governing entry expired: %v", err)
	}
	for _, on := range []string{"2026-10-01", "2026-11-01"} {
		_, err := render(r, day(t, on))
		if err == nil || !strings.Contains(err.Error(), "governanceOrphaned") {
			t.Errorf("render on %s: error = %v, want one naming governanceOrphaned", on, err)
		}
	}
}

// A registry that governs nothing on any day is refused too. The schema
// already rejects the document (genesisGovernance), so this is the compiler
// holding the same floor over whatever reaches it.
func TestARegistryGoverningNothingIsRefused(t *testing.T) {
	r := governedRegistry()
	r.Principals["founder"] = withGrants(r.Principals["founder"], map[string]grant{"push": {Boundary: "push"}})
	_, err := render(r, day(t, "2026-01-01"))
	if err == nil || !strings.Contains(err.Error(), "governanceOrphaned") {
		t.Errorf("render error = %v, want one naming governanceOrphaned", err)
	}
}

// A registry that compiles: one entry governing, one pushing.
func governedRegistry() registry {
	const public = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINyd1zTLxcPuMDojZpjOdUxGj6AYl6RP7yiWb3Cd/fOQ"
	return registry{
		Boundaries: map[string]boundary{
			"push":     {Kind: boundaryGitPush},
			"registry": {Kind: boundaryRegistry},
		},
		Genesis: "founder",
		Principals: map[string]principal{
			"founder": {
				Kind:   "human",
				Keys:   []key{{Public: public, Signs: "founder"}},
				Grants: map[string]grant{"govern": {Boundary: "registry"}},
			},
			"runner": {
				Kind:    "machine",
				Keys:    []key{{Public: public}},
				Grants:  map[string]grant{"push": {Boundary: "push"}},
				Expires: "2027-01-01",
			},
		},
	}
}

func expiring(p principal, expires string) principal {
	p.Expires = expires
	return p
}

func withGrants(p principal, g map[string]grant) principal {
	p.Grants = g
	return p
}
