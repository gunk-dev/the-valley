package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

const (
	target          = "main"
	requestPrefix   = "refs/the-valley/integration-requests/" + target + "/"
	outcomePrefix   = "refs/the-valley/integration-outcomes/" + target + "/"
	attestationRefs = "refs/the-valley/attestations/"
)

// status is what one read of origin found for a change, and the state
// decide made of it.
type status struct {
	branch     string // the change as given, without origin/
	change     string // the request's name for it (changeName)
	requestRef string
	outcomeRef string

	tip     string // origin's main
	head    string // the change's head as this read knows it; "" for none
	request string // the commit the integration request names; "" for none
	outcome string // the outcome record's blob; "" for none
	memo    memo   // the outcome record, read; meaningful when outcome is set

	state string
}

// changeName is the change segment of a request ref. The integrator reads
// a request as <prefix>/<target>/<change> and refuses a change segment
// holding a slash (integrator/refs.go), so a branch name with one is
// written with dashes instead. bin/valley's change_name is the same
// mapping, and the request approve files is named by it.
func changeName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// read takes one snapshot of origin and decides the change's state from
// it. A status read is a snapshot of origin, not a replay of this
// checkout's cached refs: one ls-remote lists every ref the decision
// reads, and the objects they name are fetched by id, so the decision is
// about one moment of origin and never about refs/remotes. Nothing local
// is pruned, so evidence this checkout produced survives the read.
func read(g git, branch string) (*status, error) {
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return nil, failure("not a git repository")
	}
	if !g.succeeds(true, "check-ref-format", "refs/heads/"+branch) {
		return nil, failure("not a branch name: " + branch)
	}
	s := &status{branch: branch, change: changeName(branch)}
	s.requestRef = requestPrefix + s.change
	s.outcomeRef = outcomePrefix + s.change

	snapshot, err := g.output(false, "ls-remote", "--refs", "origin",
		"refs/heads/"+branch, "refs/heads/"+target, s.requestRef, s.outcomeRef,
		attestationRefs+"*")
	if err != nil {
		return nil, failure("could not read origin; integration status is unknown. Try valley status " +
			shellQuote(branch) + " again")
	}
	// ls-remote prints "<oid>\t<ref>", and a ref name may hold any space
	// but ASCII's, so the line is split at the tab alone. ls-remote matches
	// a pattern against the tail of a ref name, so every ref is compared
	// whole here.
	var attestations []string
	for _, line := range strings.Split(snapshot, "\n") {
		oid, ref, ok := strings.Cut(line, "\t")
		if !ok || ref == "" {
			continue
		}
		if ref == "refs/heads/"+target {
			s.tip = oid
		}
		if ref == "refs/heads/"+branch {
			s.head = oid
		}
		switch {
		case ref == s.requestRef:
			s.request = oid
		case ref == s.outcomeRef:
			s.outcome = oid
		case strings.HasPrefix(ref, attestationRefs):
			attestations = append(attestations, ref+" "+oid)
		}
	}
	if s.tip == "" {
		return nil, failure("origin has no main branch; integration status is unknown")
	}
	if s.head == "" && s.request == "" && s.outcome == "" {
		return nil, failure(branch + ": no remote branch or recorded integration request on origin")
	}
	fetch := []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", s.tip}
	for _, oid := range []string{s.head, s.request, s.outcome} {
		if oid != "" {
			fetch = append(fetch, oid)
		}
	}
	if _, err := g.output(false, fetch...); err != nil {
		return nil, failure("could not fetch the status objects; origin may have changed. Try valley status " +
			shellQuote(branch) + " again")
	}

	if s.outcome != "" {
		record, err := g.output(true, "cat-file", "blob", s.outcome)
		if err != nil {
			record = ""
		}
		s.memo = parseMemo(record)
	}
	// A deleted topic can still have a pending request or a recorded
	// result. The memo's head stands in only when it is an object id: any
	// other text would be resolved as a revision of this checkout.
	if s.head == "" {
		s.head = s.request
	}
	if s.head == "" && isObjectID(s.memo.head) {
		s.head = s.memo.head
	}
	s.state = decide(g, s, evidenceFingerprint(attestations))
	return s, nil
}

// memo is the integrator's outcome record for a request, one line: the
// head it judged, the target tip it judged that head against, the
// fingerprint of the evidence it saw, and its decision. A failed pass
// adds when it failed (integrator/refs.go writes it). The first three are
// what the integrator compares to decide whether to judge a request again.
type memo struct {
	head, tip, evidence string
	decision            string // land, stale, reject, failed, or unknown
	failedAt            string // set for failed alone
}

// parseMemo reads an outcome record. Anything it does not recognise is
// unknown, never a decision: a damaged record, or one in a format newer
// than this reader, must not read as a landing or as any other answer the
// integrator did not give. Fields are separated by spaces or tabs, and
// trailing newlines are dropped, as bin/valley read it with read(1); a
// second line, a NUL, or a sixth field makes the record unknown.
func parseMemo(record string) memo {
	line := strings.TrimRight(record, "\n")
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '\t' })
	field := func(i int) string {
		if i < len(fields) {
			return fields[i]
		}
		return ""
	}
	m := memo{head: field(0), tip: field(1), evidence: field(2), decision: field(3), failedAt: field(4)}
	if strings.ContainsAny(line, "\n\x00") || len(fields) > 5 ||
		!isObjectID(m.head) || !isObjectID(m.tip) || !isHex(m.evidence, 16) {
		m.decision = "unknown"
	}
	switch m.decision {
	case "land", "stale", "reject":
		if m.failedAt != "" {
			m.decision = "unknown"
		}
	case "failed":
		if m.failedAt == "" {
			m.decision = "unknown"
		}
	default:
		m.decision = "unknown"
	}
	if m.decision != "failed" {
		m.failedAt = ""
	}
	return m
}

// isObjectID is a full SHA-1 or SHA-256 object id, lowercase.
func isObjectID(s string) bool {
	return isHex(s, 40) || isHex(s, 64)
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// evidenceFingerprint is the evidence state the integrator records in a
// memo: the first 16 hex digits of the SHA-256 over every attestation ref
// on origin, one "<ref> <oid>" line each, in byte order. A memo with a
// different fingerprint was written about other evidence.
func evidenceFingerprint(attestations []string) string {
	sorted := append([]string(nil), attestations...)
	sort.Strings(sorted)
	h := sha256.New()
	for _, line := range sorted {
		h.Write([]byte(line + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// decide names the change's state, one per answer status gives.
//
// main is authoritative for a landing, so ancestry is read first: main can
// advance before the integrator records its outcome, and a failure
// storing evidence afterwards must not hide an integration already there.
// A delta applied onto a moved tip lands as a different commit, so a land
// memo for this head is the next answer. Only after both does a request or
// a recorded decision speak for the change.
func decide(g git, s *status, evidence string) string {
	if s.head != "" && g.succeeds(true, "cat-file", "-e", s.head+"^{commit}") &&
		g.succeeds(false, "merge-base", "--is-ancestor", s.head, s.tip) {
		return "integrated"
	}
	decision := s.memo.decision // "" when there is no outcome record
	switch {
	case decision == "land" && s.memo.head == s.head && (s.request == "" || s.request == s.head):
		return "integrated-recorded"
	case s.request != "" && s.request != s.head:
		return "outdated-request"
	case decision == "unknown":
		return "outcome-unreadable"
	case s.request == "":
		return "not-requested"
	case decision == "" || s.memo.head != s.head:
		return "awaiting-decision"
	case s.memo.tip != s.tip || s.memo.evidence != evidence:
		return "awaiting-new-decision"
	}
	switch decision {
	case "stale":
		return "stale"
	case "reject":
		return "rejected"
	case "failed":
		return "failed"
	}
	// A land memo for this head with the request at this head was
	// integrated-recorded above, and no other decision parses.
	panic("valley-status: no state for decision " + decision)
}
