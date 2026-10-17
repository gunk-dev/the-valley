// Every change a repository holds a record of, and how far each version
// has landed.
//
// changes and pending read one snapshot of origin for each repository
// they list: every branch, every record, every integration request and
// outcome, and every attestation ref. From it they decide, for each
// version of each change, the landing evidence it has: landed,
// landed-recorded, uncertain or not-landed. The rules are the rebase
// engine's (../rebase/snapshot.go), so a listing and a rebase read one
// landing the same way.

package main

import (
	"context"
	"os"
	"sort"

	"the-valley/change"
	"the-valley/rebase"
)

// The landing evidence a version can have.
const (
	landed         = rebase.Landed
	landedRecorded = rebase.LandedRecorded
	uncertain      = rebase.LandingUncertain
	notLanded      = rebase.NotLanded
)

// inventoryPatterns are the ls-remote patterns of every ref a snapshot
// reads.
var inventoryPatterns = rebase.InventoryPatterns

// verifier is what a transfer statement is checked with: the verifier
// keys of the identity registry's known-signers file, the name the
// integrator signs under, and the attest helper that checks a note.
// VALLEY_KNOWN_SIGNERS and VALLEY_INTEGRATOR_SIGNER name the first two.
// Without both, no transfer statement is believed, and a version that
// only one could show landed reads as uncertain or not-landed.
type verifier = rebase.Verifier

// verifierHint says what a transfer statement is verified with, for a
// command that could not verify one.
const verifierHint = "Set VALLEY_KNOWN_SIGNERS to the known-signers file and VALLEY_INTEGRATOR_SIGNER to the integrator's signing name, with attest on PATH"

func verifierFromEnv() verifier {
	return verifier{
		Keys:   os.Getenv("VALLEY_KNOWN_SIGNERS"),
		Signer: os.Getenv("VALLEY_INTEGRATOR_SIGNER"),
		Attest: "attest",
	}
}

// snapshot is one read of a repository's origin: the engine's snapshot,
// which decides each landing, with the records as this program holds them.
type snapshot struct {
	land *rebase.Snapshot
	g    git
	o    origin
	tip  string // main on origin; "" when origin has none
	all  []recorded

	// unread is each record that does not read, which no listing shows.
	unread []unreadRecord
}

// readSnapshot lists origin once and fetches every object the snapshot
// reads: main, every branch tip, every record commit and every outcome
// record.
func readSnapshot(g git, v verifier) (*snapshot, error) {
	land, err := rebase.ReadSnapshot(context.Background(), g.engine(), v)
	if err != nil {
		return nil, err
	}
	s := wrapSnapshot(g, land)
	for _, u := range land.Unread {
		s.unread = append(s.unread, unreadRecord{ref: u.Ref, commit: u.Commit, why: u.Why})
	}
	return s, nil
}

// newSnapshot is a snapshot of what o lists, with every record in all
// already read and every object it names already fetched.
func newSnapshot(g git, o origin, all []recorded, v verifier) *snapshot {
	var read []rebase.Recorded
	for _, n := range all {
		read = append(read, n.engine())
	}
	return wrapSnapshot(g, rebase.NewSnapshot(context.Background(), g.engine(), o.engine(), read, v))
}

func wrapSnapshot(g git, land *rebase.Snapshot) *snapshot {
	return &snapshot{land: land, g: g, o: origin{refs: land.Refs}, tip: land.Tip, all: fromEngine(land.Changes)}
}

// headOf is a version's head as an object ID.
func headOf(v change.Version) string { return rebase.HeadOf(v) }

// unverified reports whether a transfer statement named a version and
// could not be checked, so its landing may be stronger than shown.
func (s *snapshot) unverified() bool { return s.land.Unverified() }

// reaches reports whether main reaches a commit by any path.
func (s *snapshot) reaches(oid string) bool { return s.land.Reaches(oid) }

// request is the integration request origin holds for a version: one
// filed under either of its names, at its head.
func (s *snapshot) request(id string, v change.Version) *jsonRequest {
	if ref, ok := s.land.Request(id, v); ok {
		return &jsonRequest{Ref: ref, Commit: headOf(v)}
	}
	return nil
}

// outcome is the integrator's last decision about a version: the outcome
// record under either of its names whose head is the version's head.
func (s *snapshot) outcome(id string, v change.Version) (name string, m memo, found bool) {
	name, o, found := s.land.Outcome(id, v)
	return name, memoOf(o), found
}

// landing is the landing evidence a version has, and the commit on main it
// landed as, when it has.
func (s *snapshot) landing(id string, v change.Version) (kind, commit string) {
	return s.land.Landing(id, v)
}

// isLanded reports whether a version has landed by the first two rules.
func (s *snapshot) isLanded(id string, v change.Version) bool { return s.land.IsLanded(id, v) }

// reach is every commit reachable from oid that main does not reach.
func (s *snapshot) reach(oid string) map[string]bool { return s.land.Reach(oid) }

// strictAncestor reports whether a is an ancestor of b, and not b itself.
func (s *snapshot) strictAncestor(a, b string) bool { return s.land.StrictAncestor(a, b) }

// includes is the change IDs among candidates whose active head is a
// strict ancestor of n's active head, nearest only: one that is itself an
// ancestor of another such change's head is left out. It is ancestry
// alone, so a change that includes another lands the other's content too.
func (s *snapshot) includes(n recorded, candidates []recorded) []string {
	head := headOf(n.record.ActiveVersion())
	var below []recorded
	for _, c := range candidates {
		if c.id != n.id && s.strictAncestor(headOf(c.record.ActiveVersion()), head) {
			below = append(below, c)
		}
	}
	ids := []string{}
	for _, c := range below {
		nearest := true
		for _, d := range below {
			if s.strictAncestor(headOf(c.record.ActiveVersion()), headOf(d.record.ActiveVersion())) {
				nearest = false
				break
			}
		}
		if nearest {
			ids = append(ids, c.id)
		}
	}
	sort.Strings(ids)
	return ids
}

// unrecorded is every branch on origin, other than main, whose tip main
// does not reach and that is not the head of any change's active version,
// in the order of their names.
func (s *snapshot) unrecorded() []jsonBranch {
	branches := []jsonBranch{}
	for _, b := range s.land.Unrecorded() {
		branches = append(branches, jsonBranch{Branch: b.Branch, Head: b.Head})
	}
	return branches
}
