package change

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// AttestationRefs is the namespace the integrator keeps evidence under.
const AttestationRefs = "refs/the-valley/attestations/"

// The bounds an attestation ref's tree is read within. The push hook
// refuses a pushed tree past any of them (valleyhook/repo.go), and the
// integrator refuses to write one (integrator/transfer.go), so every tree
// the integrator writes can be pushed to another host. The byte bounds
// count what the hook's walk reads, each tree object as git stores it.
const (
	AttestationDepth       = 4        // trees from the ref's tree down, the ref's tree counted as 1
	AttestationTreeEntries = 64       // entries in any one tree
	AttestationEntries     = 256      // entries in all the ref's trees together
	AttestationTreeBytes   = 16 << 10 // bytes in any one tree object
	AttestationNoteBytes   = 64 << 10 // bytes in any one note
	AttestationBytes       = 1 << 20  // bytes in every tree object and note the walk reads
)

// An attestation ref the integrator writes holds its transfer statements,
// one per landing of the ref's tree. Each statement sits at
// TransferPath(<landed commit>), under TransferDir at the top of the tree.
// A ref written before the integrator kept one statement per landing holds
// a single statement at TransferNote, at the top of the tree, instead.
const (
	TransferDir  = "transfer.d"
	TransferNote = "transfer.note"
)

// TransferPath is the path of the transfer statement for a landing as
// commit, a full object ID: the commit's first two hex digits, its next
// two, and the rest, as transfer.d/<2 hex>/<2 hex>/<rest>.note. The two
// levels of directories keep the number of entries in any one tree small
// however many times one tree lands.
func TransferPath(commit string) string {
	return TransferDir + "/" + commit[:2] + "/" + commit[2:4] + "/" + commit[4:] + ".note"
}

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
