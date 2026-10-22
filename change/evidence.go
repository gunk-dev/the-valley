package change

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// AttestationRefs is the namespace the integrator keeps evidence under.
const AttestationRefs = "refs/the-valley/attestations/"

// EvidenceFingerprint is the evidence state the integrator writes into an
// outcome record (integrator/refs.go): the first 16 hex digits of the
// SHA-256 over every attestation ref on origin, one "<ref> <oid>" line
// each, every line ending in a newline, in byte order. An outcome record
// with a different fingerprint was written about other evidence.
//
// The integrator, valley status and the restacker each compute it from
// what they read, so it is defined once, here.
func EvidenceFingerprint(attestations []string) string {
	sorted := append([]string(nil), attestations...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, line := range sorted {
		h.Write([]byte(line + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// SelectNotes picks the note each check is judged by, from every note
// stored for one tree. notes must be in the order the integrator reads
// them: the attestation refs in byte order, and each ref's notes in the
// order git ls-tree -r lists them. check names the check a note says it
// is about, and verifies says whether the note verifies.
//
// The first note for a check that verifies is the one it is judged by,
// and no later note replaces it, whatever it records. Until one verifies,
// each note read replaces the one before, so the last refusal stands.
//
// The integrator judges a request by this choice (integrator/evidence.go),
// and approve reuses evidence only where this choice is evidence it
// accepts (status/coverage.go), so the two never judge a check by
// different notes.
func SelectNotes[N any](notes []N, check func(N) string, verifies func(N) bool) map[string]N {
	picked := map[string]N{}
	for _, n := range notes {
		name := check(n)
		if old, seen := picked[name]; seen && verifies(old) {
			continue
		}
		picked[name] = n
	}
	return picked
}
