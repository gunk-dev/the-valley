// status reads one change's integration status from origin: the change's
// head, its integration request, and the integrator's last recorded
// outcome for it. It is `valley status`.
//
// A change with a record is read at the head its active version records,
// under the request name approve files it under, c-<change ID>, and its
// status carries what its record says and how far its active version has
// landed. Any other name is a branch, read at its head on origin under the
// request name approve gives a branch.
//
// It writes nothing. The objects it reads are fetched without writing
// FETCH_HEAD or moving a branch, so a status read leaves the checkout and
// its working tree exactly as it found them. No check runs and no request
// is filed.
//
// Without flags it prints the sentences an operator reads. With --json it
// prints one object, valley-status/v1, which is a contract front ends
// read; docs/architecture.md documents it. Both are rendered from one
// decision (decide), so the two forms cannot disagree about a change.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"the-valley/change"
)

const statusUsage = "usage: valley status [--json] <change>"

func runStatus(g git, args []string, stdout, stderr io.Writer) int {
	asJSON, name, err := parseStatusArgs(args)
	if err == nil && asJSON && !utf8.ValidString(name) {
		err = failure(name + ": not UTF-8, so JSON cannot carry the change name")
	}
	var s *status
	if err == nil {
		s, err = readNamed(g, name, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley: %s\n", err)
		return 1
	}
	if asJSON {
		return writeJSON(s, stdout, stderr)
	}
	return writeHuman(g, s, stdout)
}

// parseStatusArgs takes one change and an optional --json, in either
// order. The change is named the way every verb names one (changes.go).
func parseStatusArgs(args []string) (asJSON bool, name string, err error) {
	var rest []string
	for _, a := range args {
		if a == "--json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 {
		return false, "", failure(statusUsage)
	}
	return asJSON, rest[0], nil
}

const (
	target          = "main"
	requestPrefix   = "refs/the-valley/integration-requests/" + target + "/"
	outcomePrefix   = "refs/the-valley/integration-outcomes/" + target + "/"
	attestationRefs = "refs/the-valley/attestations/"
)

// status is what one read of origin found for a change, and the state
// decide made of it.
type status struct {
	branch     string // the change's branch: as given, without origin/, or as its record names it
	change     string // the request's name for it (changeName, or c-<change ID>)
	requestRef string
	outcomeRef string

	// recorded is what the change's record says, for a change with one,
	// and nil for a branch.
	recorded *statusRecord

	tip     string // origin's main
	head    string // the change's head as this read knows it; "" for none
	request string // the commit the integration request names; "" for none
	outcome string // the outcome record's blob; "" for none
	memo    memo   // the outcome record, read; meaningful when outcome is set

	state string
}

// statusRecord is what a change's record says about its active version,
// and how far that version has landed (inventory.go).
type statusRecord struct {
	id, handle, title, owner, landing string
	version                           int
}

// name is how the status names the change in the commands it suggests:
// its handle when it has a record, and otherwise its branch.
func (s *status) name() string {
	if s.recorded != nil {
		return s.recorded.handle
	}
	return s.branch
}

// label is how the status names the change in its sentences: its handle
// and version when it has a record, and otherwise its branch.
func (s *status) label() string {
	if s.recorded != nil {
		return fmt.Sprintf("%s v%d", s.recorded.handle, s.recorded.version)
	}
	return s.branch
}

// readNamed reads the status of a change by any of its names, as review
// and approve read one (resolveRecorded): a change's ID or handle, or a
// branch that exactly one record's active version is on. Any other name
// is a branch.
func readNamed(g git, name string, stderr io.Writer) (*status, error) {
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return nil, failure("not a git repository")
	}
	o, n, found, err := lookupRecorded(g, name)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return read(g, strings.TrimPrefix(name, "origin/"))
	}
	return readRecorded(g, o, n, stderr)
}

// reservedName refuses a branch whose request name is c-<change ID>.
// Every such name is reserved for the change with that ID, so a branch
// mapping to one is never filed or read under it.
func reservedName(branch string) error {
	name := changeName(branch)
	if id, ok := change.RequestID(name); ok {
		return failure(fmt.Sprintf("the branch %s would be requested as %s, and that name is reserved for the change with the ID %s; rename the branch, or name the change by its ID", branch, name, id))
	}
	return nil
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
	if err := reservedName(branch); err != nil {
		return nil, err
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
	// whole by settle.
	refs := map[string]string{}
	for _, line := range strings.Split(snapshot, "\n") {
		if oid, ref, ok := strings.Cut(line, "\t"); ok && ref != "" {
			refs[ref] = oid
		}
	}
	s.head = refs["refs/heads/"+branch]
	if err := s.settle(g, refs); err != nil {
		return nil, err
	}
	return s, nil
}

// readRecorded reads a change with a record from the snapshot o its name
// was resolved in. The change is read at the head its active version
// records, under its request name, c-<change ID>.
func readRecorded(g git, o origin, n recorded, stderr io.Writer) (*status, error) {
	v := n.record.ActiveVersion()
	name := change.RequestName(n.id)
	s := &status{
		branch: v.Branch, change: name, requestRef: requestPrefix + name, outcomeRef: outcomePrefix + name,
		head: headOf(v),
		recorded: &statusRecord{
			id: n.id, handle: o.handle(n.id), version: v.N, title: v.Title, owner: n.record.Owner,
		},
	}
	if s.branch == "" {
		s.branch = s.recorded.handle
	}
	if err := s.settle(g, o.refs); err != nil {
		return nil, err
	}
	// How far the active version has landed, by the rules changes uses.
	var want []string
	for ref, oid := range o.refs {
		if strings.HasPrefix(ref, outcomePrefix) {
			want = append(want, oid)
		}
	}
	if err := fetch(g, want...); err != nil {
		return nil, err
	}
	snap := newSnapshot(g, o, nil, verifierFromEnv())
	s.recorded.landing, _ = snap.landing(n.id, v)
	if snap.unverified {
		fmt.Fprintf(stderr, "valley: a transfer statement could not be verified, so %s may have landed though its landing does not say so. %s\n", s.recorded.handle, verifierHint)
	}
	return s, nil
}

// settle reads the request, the outcome record and the evidence from
// refs, a snapshot of origin, fetches what they name, and decides the
// change's state. s.head is the change's head as the snapshot knows it,
// or "" for none.
func (s *status) settle(g git, refs map[string]string) error {
	var attestations []string
	for ref, oid := range refs {
		switch {
		case ref == "refs/heads/"+target:
			s.tip = oid
		case ref == s.requestRef:
			s.request = oid
		case ref == s.outcomeRef:
			s.outcome = oid
		case strings.HasPrefix(ref, attestationRefs):
			attestations = append(attestations, ref+" "+oid)
		}
	}
	if s.tip == "" {
		return failure("origin has no main branch; integration status is unknown")
	}
	if s.head == "" && s.request == "" && s.outcome == "" {
		return failure(s.branch + ": no remote branch or recorded integration request on origin")
	}
	fetch := []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", s.tip}
	for _, oid := range []string{s.head, s.request, s.outcome} {
		if oid != "" {
			fetch = append(fetch, oid)
		}
	}
	if _, err := g.output(false, fetch...); err != nil {
		return failure("could not fetch the status objects; origin may have changed. Try valley status " +
			shellQuote(s.name()) + " again")
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
	return nil
}

// memo is the integrator's outcome record for a request, one line: the
// head it judged, the target tip it judged that head against, the
// fingerprint of the evidence it saw, and its decision. A failed pass
// adds when it failed (integrator/refs.go writes it). The first three are
// what the integrator compares to decide whether to judge a request again.
type memo struct {
	head, tip, evidence string
	decision            string // land, stale, reject, refused-not-ready, refused-merge, failed, or unknown
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
	case "land", "stale", "reject", "refused-not-ready", "refused-merge":
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
	case "refused-not-ready", "refused-merge":
		return decision
	case "failed":
		return "failed"
	}
	// A land memo for this head with the request at this head was
	// integrated-recorded above, and no other decision parses.
	panic("valley-status: no state for decision " + decision)
}
