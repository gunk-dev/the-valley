// Every change a repository holds a record of, and how far each version
// has landed.
//
// A snapshot is one read of origin: every branch, every record, every
// integration request and outcome, every attestation ref, and every
// restack report. From it, for each version of each change, it decides the
// landing evidence the version has. The first rule that applies is the
// answer:
//
//  1. landed: the version's head is on main's first-parent history, and
//     no commit on that history between the head and main's tip is a
//     merge. The integrator never writes a merge, and a fast-forward puts
//     every commit of a change on that history. A merge above the head
//     could have dropped the head's content, whichever parent the head is
//     reached through, so it leaves the head not landed by this rule.
//  2. landed-recorded: a commit L on main's first-parent history, with one
//     parent, carries a transfer statement, signed by the integrator,
//     whose change is a request name of the version and whose base is
//     the valley-tree-v1 digest of the version's tree. The integrator
//     writes one when it lands a change (integrator/transfer.go). The
//     integrator's outcome record for that request name says land, at
//     this version's head: only the integrator writes outcome records,
//     and the record names the exact head it landed, which a statement
//     about trees does not. And the integrator's own merge of the head
//     onto L's parent gives L's tree.
//  3. uncertain: a request for the version was consumed, or the outcome
//     record says the integrator failed on it, and neither of the above
//     holds. The integrator moves main before it stores its evidence
//     (integrator/refs.go), so a landing can exist without its evidence.
//  4. not-landed: otherwise.
//
// The outcome record alone is never proof of a landing, because a later
// pass can overwrite it. A record a later pass overwrote leaves a landing
// uncertain, never landed-recorded.

package rebase

import (
	"bytes"
	"context"
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
	Landed           = "landed"
	LandedRecorded   = "landed-recorded"
	LandingUncertain = "uncertain"
	NotLanded        = "not-landed"
)

const (
	transferNote      = "transfer.note"
	predicateTransfer = "the-valley/integration/transfer/v1"
)

// Verifier is what a transfer statement is checked with: the verifier
// keys of the identity registry's known-signers file, the name the
// integrator signs under, and the attest helper that checks a note.
// Without the first two, no transfer statement is believed, and a version
// that only one could show landed reads as uncertain or not-landed.
//
// Resolve, when set, finds the keys and the name when they are first
// needed: a snapshot calls it once, before it verifies its first
// statement, and checks with what it returns in place of Keys and Signer.
// A snapshot that verifies no statement never calls it.
type Verifier struct {
	Keys, Signer, Attest string
	Resolve              func() (keys, signer string)
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

// Snapshot is one read of a repository's origin. Its landing decisions,
// and the ancestry it reads, are read from git when first asked for, under
// the context the snapshot was made with, and kept.
type Snapshot struct {
	Origin
	Tip     string         // main on origin; "" when origin has none
	Changes []Recorded     // every record that reads, in the order of their IDs
	Unread  []UnreadRecord // each record that does not read, which no reader shows

	ctx    context.Context
	g      Git
	verify Verifier

	outcomes    map[string]Outcome   // outcome ref → the record it holds
	reports     map[string]Report    // change ID → its restack report, once read
	landings    map[string][2]string // "<change ID> <n>" → landing and commit
	firstParent map[string]bool      // main's first-parent history, down to its first merge; nil until read
	reachesOf   map[string]bool
	reachOf     map[string]map[string]bool
	digestOf    map[string]string
	transfers   []transferStatement
	read        bool // whether transfers has been read

	// unverified is set when a transfer statement named a version and
	// could not be checked, so its landing may be stronger than shown.
	unverified bool
}

// ReadSnapshot lists origin once and fetches every object the snapshot
// reads: main, every branch tip, every record commit, every outcome record
// and every restack report. A record commit has its version heads as
// parents, so they come with it.
func ReadSnapshot(ctx context.Context, g Git, v Verifier) (*Snapshot, error) {
	o, err := ListOrigin(ctx, g, InventoryPatterns...)
	if err != nil {
		return nil, err
	}
	var want []string
	for ref, oid := range o.Refs {
		if strings.HasPrefix(ref, "refs/heads/") || strings.HasPrefix(ref, OutcomePrefix) || strings.HasPrefix(ref, RestackPrefix) {
			want = append(want, oid)
		}
	}
	if err := Fetch(ctx, g, want...); err != nil {
		return nil, err
	}
	all, unread, err := ReadAll(ctx, g, o)
	if err != nil {
		return nil, err
	}
	s := NewSnapshot(ctx, g, o, all, v)
	s.Unread = unread
	return s, nil
}

// NewSnapshot is a snapshot of what o lists, with every record in all
// already read and every object it names already fetched.
func NewSnapshot(ctx context.Context, g Git, o Origin, all []Recorded, v Verifier) *Snapshot {
	s := &Snapshot{
		Origin: o, Tip: o.Refs["refs/heads/"+Target], Changes: all,
		ctx: ctx, g: g, verify: v,
		outcomes: map[string]Outcome{}, reports: map[string]Report{}, landings: map[string][2]string{},
		reachesOf: map[string]bool{}, reachOf: map[string]map[string]bool{}, digestOf: map[string]string{},
	}
	for ref, oid := range o.Refs {
		if strings.HasPrefix(ref, OutcomePrefix) {
			data, err := g.output(ctx, true, "cat-file", "blob", oid)
			if err != nil {
				data = ""
			}
			s.outcomes[ref] = ParseOutcome(data)
		}
	}
	return s
}

// Unverified reports whether a transfer statement named a version and
// could not be checked, so that version's landing may be stronger than
// the snapshot says.
func (s *Snapshot) Unverified() bool { return s.unverified }

// OnMain reports whether a commit is on main's first-parent history (main
// itself, its first parent, that commit's first parent, and so on) with no
// merge between it and main's tip: whether
// `git rev-list --first-parent --merges <oid>..main` would list nothing.
// This is where a landing puts a change. The history is read down to its
// first merge, which is itself the last commit counted, since no merge is
// above it.
func (s *Snapshot) OnMain(oid string) bool {
	if s.Tip == "" || oid == "" {
		return false
	}
	if s.firstParent == nil {
		s.firstParent = map[string]bool{}
		if out, err := s.g.output(s.ctx, true, "rev-list", "--first-parent", "--parents", s.Tip); err == nil {
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 {
					continue
				}
				s.firstParent[fields[0]] = true
				if len(fields) > 2 {
					break
				}
			}
		}
	}
	return s.firstParent[oid]
}

// Reaches reports whether main reaches a commit by any path: whether the
// commit is an ancestor of main, or main itself. That is ancestry, which
// is not landing (OnMain).
func (s *Snapshot) Reaches(oid string) bool {
	if s.Tip == "" || oid == "" {
		return false
	}
	r, seen := s.reachesOf[oid]
	if !seen {
		r = s.g.succeeds(s.ctx, true, "merge-base", "--is-ancestor", oid, s.Tip)
		s.reachesOf[oid] = r
	}
	return r
}

// Request is the ref of the integration request origin holds for a
// version: one filed under either of its names, at its head.
func (s *Snapshot) Request(id string, v change.Version) (ref string, ok bool) {
	for _, name := range RequestNames(id, v) {
		if ref := RequestPrefix + name; s.Refs[ref] == HeadOf(v) {
			return ref, true
		}
	}
	return "", false
}

// OutcomeOf is the outcome record origin holds under a request name, as
// ParseOutcome reads it, and whether origin holds one.
func (s *Snapshot) OutcomeOf(name string) (Outcome, bool) {
	m, ok := s.outcomes[OutcomePrefix+name]
	return m, ok
}

// Outcome is the integrator's last decision about a version: the outcome
// record under either of its names whose head is the version's head.
func (s *Snapshot) Outcome(id string, v change.Version) (name string, m Outcome, found bool) {
	for _, name := range RequestNames(id, v) {
		if m, ok := s.outcomes[OutcomePrefix+name]; ok && m.Head == HeadOf(v) {
			return name, m, true
		}
	}
	return "", Outcome{}, false
}

// Landing is the landing evidence a version has, and the commit on main it
// landed as, when it has. Each version is decided once per snapshot.
func (s *Snapshot) Landing(id string, v change.Version) (kind, commit string) {
	key := fmt.Sprintf("%s %d", id, v.N)
	if l, ok := s.landings[key]; ok {
		return l[0], l[1]
	}
	kind, commit = s.decideLanding(id, v)
	s.landings[key] = [2]string{kind, commit}
	return kind, commit
}

func (s *Snapshot) decideLanding(id string, v change.Version) (kind, commit string) {
	head := HeadOf(v)
	if s.OnMain(head) {
		return Landed, head
	}
	if commit, ok := s.recordedLanding(id, v); ok {
		return LandedRecorded, commit
	}
	if name, m, ok := s.Outcome(id, v); ok {
		consumed := m.Decision == "land" && s.Refs[RequestPrefix+name] != head
		if consumed || m.Decision == "failed" {
			return LandingUncertain, ""
		}
	}
	return NotLanded, ""
}

// IsLanded reports whether a version has landed by the first two rules.
func (s *Snapshot) IsLanded(id string, v change.Version) bool {
	kind, _ := s.Landing(id, v)
	return kind == Landed || kind == LandedRecorded
}

// Unlanded reports whether a change, in any state, has an active version
// that has not landed: the changes a stack's layers are among.
func (s *Snapshot) Unlanded(n Recorded) bool {
	return !s.IsLanded(n.ID, n.Record.ActiveVersion())
}

// recordedLanding looks for a transfer statement that records the version
// landing as a commit on main, and verifies the first that matches.
func (s *Snapshot) recordedLanding(id string, v change.Version) (string, bool) {
	names := map[string]bool{}
	for _, n := range RequestNames(id, v) {
		names[n] = true
	}
	head := HeadOf(v)
	for _, t := range s.transferStatements() {
		if !names[t.change] || !IsObjectID(t.landed) || !s.OnMain(t.landed) || !s.singleParent(t.landed) {
			continue
		}
		if m, ok := s.outcomes[OutcomePrefix+t.change]; !ok || m.Decision != "land" || m.Head != head {
			continue
		}
		digest, ok := s.digest(head)
		if !ok {
			s.unverified = true
			continue
		}
		if t.base != digest || !s.mergesTo(head, t.landed) {
			continue
		}
		if s.verified(t) {
			return t.landed, true
		}
	}
	return "", false
}

// singleParent reports whether a commit has exactly one parent, as every
// commit the integrator lands as does.
func (s *Snapshot) singleParent(oid string) bool {
	out, err := s.g.output(s.ctx, true, "rev-list", "--parents", "-n", "1", oid)
	return err == nil && len(strings.Fields(out)) == 2
}

// mergesTo reports whether the integrator's merge of head onto landed's
// parent gives landed's tree. It is the merge integrator/git.go's
// mergeDelta makes: merge-tree onto the tip, with the merge base of the
// tip and the head as the base. A head that merges to another tree, or
// not at all, is not what landed.
func (s *Snapshot) mergesTo(head, landed string) bool {
	tip, err := s.g.output(s.ctx, true, "rev-parse", "--verify", "--quiet", landed+"^1^{commit}")
	if err != nil {
		return false
	}
	want, err := s.g.output(s.ctx, true, "rev-parse", "--verify", "--quiet", landed+"^{tree}")
	if err != nil {
		return false
	}
	base, err := s.g.output(s.ctx, true, "merge-base", tip, head)
	if err != nil {
		return false
	}
	out, err := s.g.output(s.ctx, true, "merge-tree", "--write-tree", "--merge-base="+base, tip, head)
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
func (s *Snapshot) transferStatements() []transferStatement {
	if s.read {
		return s.transfers
	}
	s.read = true
	var trees []string
	for ref, oid := range s.Refs {
		if strings.HasPrefix(ref, AttestationRefs) {
			trees = append(trees, oid)
		}
	}
	if len(trees) == 0 {
		return nil
	}
	sort.Strings(trees)
	if err := Fetch(s.ctx, s.g, trees...); err != nil {
		s.unverified = true
		return nil
	}
	var query bytes.Buffer
	for _, tree := range trees {
		fmt.Fprintf(&query, "%s:%s\n", tree, transferNote)
	}
	out, err := s.g.input(s.ctx, true, &query, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		s.unverified = true
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		oid, kind, _ := strings.Cut(line, " ")
		if kind != "blob" {
			continue
		}
		note, err := s.g.output(s.ctx, true, "cat-file", "blob", oid)
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
func (s *Snapshot) repoDir() string {
	if s.g.Dir == "" {
		return "."
	}
	return s.g.Dir
}

// digest is the valley-tree-v1 digest of a commit's tree, from attest,
// which defines the scheme.
func (s *Snapshot) digest(oid string) (string, bool) {
	if d, ok := s.digestOf[oid]; ok {
		return d, d != ""
	}
	out, err := s.g.tool(s.ctx, s.verify.Attest, "digest", "--repo", s.repoDir(), "--rev", oid).Output()
	d := ""
	if scheme, value, ok := strings.Cut(strings.TrimSpace(string(out)), ":"); finished(err) == nil && ok && scheme == "valley-tree-v1" {
		d = value
	}
	s.digestOf[oid] = d
	return d, d != ""
}

// verified runs attest verify over a transfer statement: its signature
// must be the integrator's, by a key the known-signers file holds, and
// its subject must be the tree of the commit it says landed.
func (s *Snapshot) verified(t transferStatement) bool {
	if s.verify.Resolve != nil {
		s.verify.Keys, s.verify.Signer = s.verify.Resolve()
		s.verify.Resolve = nil
	}
	if s.verify.Keys == "" || s.verify.Signer == "" {
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
	cmd := s.g.tool(s.ctx, s.verify.Attest, "verify", "--note", file.Name(),
		"--known-keys", s.verify.Keys, "--signer", s.verify.Signer,
		"--repo", s.repoDir(), "--rev", t.landed)
	if err := finished(cmd.Run()); err != nil {
		// attest refusing the note is an answer. Failing to run it is not.
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			s.unverified = true
		}
		return false
	}
	return true
}

// Reach is every commit reachable from oid that main does not reach.
func (s *Snapshot) Reach(oid string) map[string]bool {
	if r, ok := s.reachOf[oid]; ok {
		return r
	}
	args := []string{"rev-list", oid}
	if s.Tip != "" {
		args = append(args, "^"+s.Tip)
	}
	r := map[string]bool{}
	if out, err := s.g.output(s.ctx, true, args...); err == nil {
		for _, c := range strings.Fields(out) {
			r[c] = true
		}
	}
	s.reachOf[oid] = r
	return r
}

// StrictAncestor reports whether a is an ancestor of b, and not b itself.
// A commit main does not reach is an ancestor of b exactly when it is in
// Reach(b), so git is asked only about a commit main reaches.
func (s *Snapshot) StrictAncestor(a, b string) bool {
	switch {
	case a == b:
		return false
	case s.Reach(b)[a]:
		return true
	case s.Reaches(a):
		return s.g.succeeds(s.ctx, true, "merge-base", "--is-ancestor", a, b)
	}
	return false
}

// BranchHead is a branch on origin and the commit at its tip.
type BranchHead struct {
	Branch, Head string
}

// Unrecorded is every branch on origin, other than main, whose tip main
// does not reach and that is not the head of any change's active version,
// in the order of their names.
func (s *Snapshot) Unrecorded() []BranchHead {
	active := map[string]bool{}
	for _, n := range s.Changes {
		active[HeadOf(n.Record.ActiveVersion())] = true
	}
	var branches []BranchHead
	for ref, oid := range s.Refs {
		name, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok || name == Target || active[oid] || s.Reaches(oid) {
			continue
		}
		branches = append(branches, BranchHead{Branch: name, Head: oid})
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].Branch < branches[j].Branch })
	return branches
}

// Node is one head among those Nest stacks: a change's active head or a
// branch's tip, with the commit time it is ordered by and the name ties
// are broken by.
type Node struct {
	Name string
	Head string
	When int64
}

// Nesting is how a set of nodes stack, by ancestry alone. Each slice is
// indexed as the nodes were given.
type Nesting struct {
	// Parent is the node each is nested under: the nearest node whose head
	// strictly contains its own, or -1 for none. Where two are equally
	// near, the one with the later commit is taken.
	Parent []int
	// Base is the nearest node whose head its own head strictly contains,
	// or -1 for main. Where two are equally near, the one nested under it
	// is taken, and otherwise the one with the later commit.
	Base []int
	// Inside is every node whose head strictly contains its own, in the
	// order given.
	Inside [][]int
}

// Nest stacks the nodes: valley pending nests its entries this way, and
// a stack's layers each sit on their base.
func (s *Snapshot) Nest(nodes []Node) Nesting {
	n := len(nodes)
	in := make([][]bool, n) // in[i][j]: node i's head strictly contains node j's
	nest := Nesting{Parent: make([]int, n), Base: make([]int, n), Inside: make([][]int, n)}
	for i := range nodes {
		in[i] = make([]bool, n)
		for j := range nodes {
			in[i][j] = i != j && s.StrictAncestor(nodes[j].Head, nodes[i].Head)
			if in[i][j] {
				nest.Inside[j] = append(nest.Inside[j], i)
			}
		}
	}
	// nearest is the nodes i contains, or that contain i, with none
	// between, the latest commit first.
	nearest := func(i int, above bool) []int {
		var near []int
		for j := range nodes {
			outer, inner := j, i
			if !above {
				outer, inner = i, j
			}
			if !in[outer][inner] {
				continue
			}
			between := false
			for k := range nodes {
				between = between || in[outer][k] && in[k][inner]
			}
			if !between {
				near = append(near, j)
			}
		}
		sort.SliceStable(near, func(a, b int) bool {
			x, y := nodes[near[a]], nodes[near[b]]
			if x.When != y.When {
				return x.When > y.When
			}
			return x.Name < y.Name
		})
		return near
	}
	for i := range nodes {
		nest.Parent[i] = -1
		if above := nearest(i, true); len(above) > 0 {
			nest.Parent[i] = above[0]
		}
	}
	for i := range nodes {
		nest.Base[i] = -1
		below := nearest(i, false)
		for _, d := range below {
			if nest.Parent[d] == i {
				nest.Base[i] = d
				break
			}
		}
		if nest.Base[i] == -1 && len(below) > 0 {
			nest.Base[i] = below[0]
		}
	}
	return nest
}
