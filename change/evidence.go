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
