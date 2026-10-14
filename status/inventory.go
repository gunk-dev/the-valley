// Every change a repository holds a record of, and how far each version
// has landed.
//
// changes and pending read one snapshot of origin for each repository
// they list: every branch, every record, every integration request and
// outcome, and every attestation ref. From it they decide, for each
// version of each change, the landing evidence it has. The first rule
// that applies is the answer:
//
//  1. landed: the version's head is on main's first-parent history. The
//     integrator never writes a merge, and a fast-forward puts every
//     commit of a change on that history. A head that main reaches only
//     through a merge's other parent may have had its content dropped by
//     the merge, so it has not landed by this rule.
//  2. landed-recorded: a commit L on main's first-parent history carries a
//     transfer statement, signed by the integrator, whose change is the
//     version's request name and whose base is the valley-tree-v1 digest
//     of the version's tree. The integrator writes one when it lands a
//     change as a new commit (integrator/transfer.go). And the
//     integrator's own merge of the version's head onto L's parent must
//     give L's tree: a head with the same tree on another parent is
//     another delta, which the statement says nothing about.
//  3. uncertain: a request for the version was consumed, or the outcome
//     record says the integrator failed on it, and neither of the above
//     holds. The integrator moves main before it stores its evidence
//     (integrator/refs.go), so a landing can exist without its evidence.
//  4. not-landed: otherwise.
//
// The outcome record alone is never proof of a landing, because a later
// pass can overwrite it.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"the-valley/change"
)

// The landing evidence a version can have.
const (
	landed         = "landed"
	landedRecorded = "landed-recorded"
	uncertain      = "uncertain"
	notLanded      = "not-landed"
)

const (
	transferNote      = "transfer.note"
	predicateTransfer = "the-valley/integration/transfer/v1"
)

// inventoryPatterns are the ls-remote patterns of every ref a snapshot
// reads.
var inventoryPatterns = []string{
	"refs/heads/*",
	change.RefPrefix + recordTarget + "/*",
	requestPrefix + "*",
	outcomePrefix + "*",
	attestationRefs + "*",
}

// verifier is what a transfer statement is checked with: the verifier
// keys of the identity registry's known-signers file, the name the
// integrator signs under, and the attest helper that checks a note.
// VALLEY_KNOWN_SIGNERS and VALLEY_INTEGRATOR_SIGNER name the first two.
// Without both, no transfer statement is believed, and a version that
// only one could show landed reads as uncertain or not-landed.
type verifier struct {
	keys, signer, attest string
}

func verifierFromEnv() verifier {
	return verifier{
		keys:   os.Getenv("VALLEY_KNOWN_SIGNERS"),
		signer: os.Getenv("VALLEY_INTEGRATOR_SIGNER"),
		attest: "attest",
	}
}

// transferStatement is one transfer statement found under an attestation
// ref, with the values a landing is matched on, read before it is
// verified.
type transferStatement struct {
	note   []byte
	change string // predicate.change: the request's change name
	base   string // predicate.base.valley-tree-v1: the tree the evidence was over
	landed string // subject.digest.git-sha1: the commit that landed
}

// snapshot is one read of a repository's origin.
type snapshot struct {
	g      git
	o      origin
	tip    string // main on origin; "" when origin has none
	all    []recorded
	verify verifier

	outcomes    map[string]memo      // outcome ref → the record it holds
	landings    map[string][2]string // "<change ID> <n>" → landing and commit
	firstParent map[string]bool      // main's first-parent history; nil until read
	reachesOf   map[string]bool
	reachOf     map[string]map[string]bool
	digestOf    map[string]string
	transfers   []transferStatement
	read        bool // whether transfers has been read

	// unverified is set when a transfer statement named a version and
	// could not be checked, so its landing may be stronger than shown.
	unverified bool
}

// readSnapshot lists origin once and fetches every object the snapshot
// reads: main, every branch tip, every record commit and every outcome
// record. A record commit has its version heads as parents, so they come
// with it.
func readSnapshot(g git, v verifier) (*snapshot, error) {
	o, err := listOrigin(g, inventoryPatterns...)
	if err != nil {
		return nil, err
	}
	var want []string
	for ref, oid := range o.refs {
		if strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, outcomePrefix) {
			want = append(want, oid)
		}
	}
	if err := fetch(g, want...); err != nil {
		return nil, err
	}
	all, err := readAll(g, o)
	if err != nil {
		return nil, err
	}
	return newSnapshot(g, o, all, v), nil
}

// newSnapshot is a snapshot of what o lists, with every record in all
// already read and every object it names already fetched.
func newSnapshot(g git, o origin, all []recorded, v verifier) *snapshot {
	s := &snapshot{
		g: g, o: o, tip: o.refs["refs/heads/"+recordTarget], all: all, verify: v,
		outcomes: map[string]memo{}, landings: map[string][2]string{},
		reachesOf: map[string]bool{}, reachOf: map[string]map[string]bool{}, digestOf: map[string]string{},
	}
	for ref, oid := range o.refs {
		if strings.HasPrefix(ref, outcomePrefix) {
			data, err := g.output(true, "cat-file", "blob", oid)
			if err != nil {
				data = ""
			}
			s.outcomes[ref] = parseMemo(data)
		}
	}
	return s
}

// headOf is a version's head as an object ID.
func headOf(v change.Version) string {
	return strings.TrimPrefix(v.Head, change.HeadPrefix)
}

// onMain reports whether a commit is on main's first-parent history:
// main itself, its first parent, that commit's first parent, and so on.
// This is where a landing puts a change.
func (s *snapshot) onMain(oid string) bool {
	if s.tip == "" || oid == "" {
		return false
	}
	if s.firstParent == nil {
		s.firstParent = map[string]bool{}
		if out, err := s.g.output(true, "rev-list", "--first-parent", s.tip); err == nil {
			for _, c := range strings.Fields(out) {
				s.firstParent[c] = true
			}
		}
	}
	return s.firstParent[oid]
}

// reaches reports whether main reaches a commit by any path: whether the
// commit is an ancestor of main, or main itself. That is ancestry, which
// is not landing (onMain).
func (s *snapshot) reaches(oid string) bool {
	if s.tip == "" || oid == "" {
		return false
	}
	r, seen := s.reachesOf[oid]
	if !seen {
		r = s.g.succeeds(true, "merge-base", "--is-ancestor", oid, s.tip)
		s.reachesOf[oid] = r
	}
	return r
}

// requestNames are the names a request for a version is filed under: its
// branch, the way approve names one, and c-<change ID>.
func requestNames(id string, v change.Version) []string {
	var names []string
	if v.Branch != "" {
		names = append(names, changeName(v.Branch))
	}
	return append(names, "c-"+id)
}

// request is the integration request origin holds for a version: one
// filed under either of its names, at its head.
func (s *snapshot) request(id string, v change.Version) *jsonRequest {
	for _, name := range requestNames(id, v) {
		if ref := requestPrefix + name; s.o.refs[ref] == headOf(v) {
			return &jsonRequest{Ref: ref, Commit: headOf(v)}
		}
	}
	return nil
}

// outcome is the integrator's last decision about a version: the outcome
// record under either of its names whose head is the version's head.
func (s *snapshot) outcome(id string, v change.Version) (name string, m memo, found bool) {
	for _, name := range requestNames(id, v) {
		if m, ok := s.outcomes[outcomePrefix+name]; ok && m.head == headOf(v) {
			return name, m, true
		}
	}
	return "", memo{}, false
}

// landing is the landing evidence a version has, and the commit on main it
// landed as, when it has. Each version is decided once per snapshot.
func (s *snapshot) landing(id string, v change.Version) (kind, commit string) {
	key := fmt.Sprintf("%s %d", id, v.N)
	if l, ok := s.landings[key]; ok {
		return l[0], l[1]
	}
	kind, commit = s.decideLanding(id, v)
	s.landings[key] = [2]string{kind, commit}
	return kind, commit
}

func (s *snapshot) decideLanding(id string, v change.Version) (kind, commit string) {
	head := headOf(v)
	if s.onMain(head) {
		return landed, head
	}
	if commit, ok := s.recordedLanding(id, v); ok {
		return landedRecorded, commit
	}
	if name, m, ok := s.outcome(id, v); ok {
		consumed := m.decision == "land" && s.o.refs[requestPrefix+name] != head
		if consumed || m.decision == "failed" {
			return uncertain, ""
		}
	}
	return notLanded, ""
}

// isLanded reports whether a version has landed by the first two rules.
func (s *snapshot) isLanded(id string, v change.Version) bool {
	kind, _ := s.landing(id, v)
	return kind == landed || kind == landedRecorded
}

// recordedLanding looks for a transfer statement that records the version
// landing as a commit on main, and verifies the first that matches.
func (s *snapshot) recordedLanding(id string, v change.Version) (string, bool) {
	names := map[string]bool{}
	for _, n := range requestNames(id, v) {
		names[n] = true
	}
	for _, t := range s.transferStatements() {
		if !names[t.change] || !isObjectID(t.landed) || !s.onMain(t.landed) {
			continue
		}
		digest, ok := s.digest(headOf(v))
		if !ok {
			s.unverified = true
			continue
		}
		if t.base != digest || !s.mergesTo(headOf(v), t.landed) {
			continue
		}
		if s.verified(t) {
			return t.landed, true
		}
	}
	return "", false
}

// mergesTo reports whether the integrator's merge of head onto landed's
// parent gives landed's tree. It is the merge integrator/git.go's
// mergeDelta makes: merge-tree onto the tip, with the merge base of the
// tip and the head as the base. A head that merges to another tree, or
// not at all, is not what landed.
func (s *snapshot) mergesTo(head, landed string) bool {
	tip, err := s.g.output(true, "rev-parse", "--verify", "--quiet", landed+"^1^{commit}")
	if err != nil {
		return false
	}
	want, err := s.g.output(true, "rev-parse", "--verify", "--quiet", landed+"^{tree}")
	if err != nil {
		return false
	}
	base, err := s.g.output(true, "merge-base", tip, head)
	if err != nil {
		return false
	}
	out, err := s.g.output(true, "merge-tree", "--write-tree", "--merge-base="+base, tip, head)
	if err != nil {
		return false
	}
	tree, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(tree) == want
}

// transferStatements reads every transfer statement under origin's
// attestation refs, once. A transfer statement is the transfer.note at
// the top of an attestation ref's tree. Anything unreadable is skipped:
// what is not read is not believed.
func (s *snapshot) transferStatements() []transferStatement {
	if s.read {
		return s.transfers
	}
	s.read = true
	var trees []string
	for ref, oid := range s.o.refs {
		if strings.HasPrefix(ref, attestationRefs) {
			trees = append(trees, oid)
		}
	}
	if len(trees) == 0 {
		return nil
	}
	sort.Strings(trees)
	if err := fetch(s.g, trees...); err != nil {
		s.unverified = true
		return nil
	}
	var query bytes.Buffer
	for _, tree := range trees {
		fmt.Fprintf(&query, "%s:%s\n", tree, transferNote)
	}
	out, err := s.g.input(true, &query, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		s.unverified = true
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		oid, kind, _ := strings.Cut(line, " ")
		if kind != "blob" {
			continue
		}
		note, err := s.g.output(true, "cat-file", "blob", oid)
		if err != nil {
			continue
		}
		note += "\n"
		text, ok := noteText(note)
		if !ok || statementValue(text, "predicateType") != predicateTransfer {
			continue
		}
		s.transfers = append(s.transfers, transferStatement{
			note:   []byte(note),
			change: statementValue(text, "predicate.change"),
			base:   statementValue(text, "predicate.base.valley-tree-v1"),
			landed: statementValue(text, "subject.digest.git-sha1"),
		})
	}
	return s.transfers
}

// noteText is the statement text of a note: everything before the last
// blank line, which separates the text from its signatures.
func noteText(note string) (string, bool) {
	split := strings.LastIndex(note, "\n\n")
	if split < 0 {
		return "", false
	}
	return note[:split+1], true
}

// statementValue reads one key's value from statement text, where each
// line is a key, one space and a value. It is a lookup, and it decides
// nothing: what is read here only chooses which statement to verify.
func statementValue(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == key {
			return v
		}
	}
	return ""
}

// repoDir is the directory attest is pointed at.
func (s *snapshot) repoDir() string {
	if s.g.dir == "" {
		return "."
	}
	return s.g.dir
}

// digest is the valley-tree-v1 digest of a commit's tree, from attest,
// which defines the scheme.
func (s *snapshot) digest(oid string) (string, bool) {
	if d, ok := s.digestOf[oid]; ok {
		return d, d != ""
	}
	cmd := exec.Command(s.verify.attest, "digest", "--repo", s.repoDir(), "--rev", oid)
	cmd.Env = s.g.env
	out, err := cmd.Output()
	d := ""
	if scheme, value, ok := strings.Cut(strings.TrimSpace(string(out)), ":"); err == nil && ok && scheme == "valley-tree-v1" {
		d = value
	}
	s.digestOf[oid] = d
	return d, d != ""
}

// verified runs attest verify over a transfer statement: its signature
// must be the integrator's, by a key the known-signers file holds, and
// its subject must be the tree of the commit it says landed.
func (s *snapshot) verified(t transferStatement) bool {
	if s.verify.keys == "" || s.verify.signer == "" {
		s.unverified = true
		return false
	}
	file, err := os.CreateTemp("", "valley-transfer-*.note")
	if err != nil {
		s.unverified = true
		return false
	}
	defer os.Remove(file.Name())
	_, err = file.Write(t.note)
	if cerr := file.Close(); err != nil || cerr != nil {
		s.unverified = true
		return false
	}
	cmd := exec.Command(s.verify.attest, "verify", "--note", file.Name(),
		"--known-keys", s.verify.keys, "--signer", s.verify.signer,
		"--repo", s.repoDir(), "--rev", t.landed)
	cmd.Env = s.g.env
	if err := cmd.Run(); err != nil {
		// attest refusing the note is an answer. Failing to run it is not.
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			s.unverified = true
		}
		return false
	}
	return true
}

// reach is every commit reachable from oid that main does not reach.
func (s *snapshot) reach(oid string) map[string]bool {
	if r, ok := s.reachOf[oid]; ok {
		return r
	}
	args := []string{"rev-list", oid}
	if s.tip != "" {
		args = append(args, "^"+s.tip)
	}
	r := map[string]bool{}
	if out, err := s.g.output(true, args...); err == nil {
		for _, c := range strings.Fields(out) {
			r[c] = true
		}
	}
	s.reachOf[oid] = r
	return r
}

// strictAncestor reports whether a is an ancestor of b, and not b itself.
// A commit main does not reach is an ancestor of b exactly when it is in
// reach(b), so git is asked only about a commit main reaches.
func (s *snapshot) strictAncestor(a, b string) bool {
	switch {
	case a == b:
		return false
	case s.reach(b)[a]:
		return true
	case s.reaches(a):
		return s.g.succeeds(true, "merge-base", "--is-ancestor", a, b)
	}
	return false
}

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
	active := map[string]bool{}
	for _, n := range s.all {
		active[headOf(n.record.ActiveVersion())] = true
	}
	branches := []jsonBranch{}
	for ref, oid := range s.o.refs {
		name, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok || name == recordTarget || active[oid] || s.reaches(oid) {
			continue
		}
		branches = append(branches, jsonBranch{Branch: name, Head: oid})
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Branch < branches[j].Branch })
	return branches
}
