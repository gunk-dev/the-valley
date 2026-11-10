// valley-go compare: a version under review, set against the version of
// its change the operator last approved. review runs it for each change it
// shows, a stack's layers included, and prints what it says (review.go).
// It is information only: it files no request and skips no check.
//
// The approved version is the greatest version below the one shown whose
// exact head is named by either of these:
//
//   - an integration request on origin for the change: one filed under
//     c-<change ID>, or under the request name of one of the change's
//     versions' branches, unless that name is c-<ID> for some change ID;
//   - an outcome record on origin under one of those names that says land,
//     stale or reject at that head. The integrator judges only requests
//     that were filed, so each of those is an approval it judged.
//
// A branch's request name is lossy, since a/b and a-b are both a-b. So a
// branch named c/<ID> or c-<ID> maps to c-<ID>, which is reserved for the
// change with that ID, and is never read as this change's. Another name
// is ambiguous when anything other than this change maps to it: a branch
// any version of another change names, or a branch on origin that is no
// branch of this change. A name is ambiguous too when a record on origin
// does not read, since that record could name such a branch. A request or
// outcome under an ambiguous name is no evidence of approval. When the
// greatest version with any evidence has only ambiguous evidence, which
// version was approved is not known, and there is no comparison. An older
// version is never searched for instead. Neither is one whose delta
// happens to match.
//
// The comparison freezes main's tip T, which review passes, and the two
// heads: H₀, the approved version's, and H₁, the one shown. Each head's
// base is its merge base with T, which must be one commit: B₀ and B₁. B₀
// must be B₁ or below it on T's first-parent history. Then the file delta
// from B₀ to H₀ is set against the one from B₁ to H₁ (valley-delta-v1,
// ../delta), and each commit from B₁ to H₁ against the one in its place
// from B₀ to H₀, by patch (git patch-id), message and author.
//
// The text opens with the answer: the shown version is rebased, when its
// commits are the same and B₀ is not B₁; edited, when a commit is not the
// same; and otherwise recommitted. Equal deltas are a clean restack, and
// review shows the landings in (B₀, B₁] instead of the diff. Different
// deltas list each file whose change differs, with the landings in
// (B₀, B₁] that edited it, since a rebase onto a file main also edited
// changes the file's delta and not the commit. Then, when a commit is
// edited, comes git range-diff B₀..H₀ B₁..H₁, and last the diff of those
// files from B₁ to H₁. range-diff's hunks carry no line numbers, so a
// note review takes beside them has no anchor. The diff is in git's
// ordinary form, so a note taken beside a line of it keeps its file and
// line. Either way, a message or an author that differs is shown whatever
// the files say.
//
// The output is one line naming the kind, none, clean or changed, and then
// the text review shows, in plain text: style (style.go) styles it for a
// terminal. none has text only when an approved version was found or was
// ambiguous, and says why it is not compared.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"the-valley/change"
	"the-valley/delta"
)

const compareUsage = "usage: valley-go compare [-v] --tip <commit> <change ID> <version> <head>"

// The kinds of comparison.
const (
	compareNone    = "none"
	compareClean   = "clean"
	compareChanged = "changed"
)

// approvalDecisions are the outcomes that judged a request, and so show
// that a head was approved.
var approvalDecisions = map[string]bool{"land": true, "stale": true, "reject": true}

type compareArgs struct {
	tip, id, head string
	version       int
	verbose       bool // list each differing file's change as modes and hashes
}

func parseCompareArgs(args []string) (compareArgs, error) {
	var a compareArgs
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--tip" && i+1 < len(args):
			a.tip = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--tip="):
			a.tip = strings.TrimPrefix(args[i], "--tip=")
		case args[i] == "-v":
			a.verbose = true
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) != 3 || !isObjectID(a.tip) || !change.ValidID(rest[0]) || !isObjectID(rest[2]) {
		return compareArgs{}, failure(compareUsage)
	}
	n, err := strconv.Atoi(rest[1])
	if err != nil || n < 1 || strconv.Itoa(n) != rest[1] {
		return compareArgs{}, failure(compareUsage)
	}
	a.id, a.version, a.head = rest[0], n, rest[2]
	return a, nil
}

func runCompare(g git, args []string, w io.Writer) error {
	a, err := parseCompareArgs(args)
	if err != nil {
		return err
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	if !g.succeeds(true, "cat-file", "-e", a.tip+"^{commit}") {
		return failure("the tip " + a.tip + " is no commit here")
	}
	h, err := readHistory(g, a.id)
	if err != nil {
		return err
	}
	kind, text, err := compareVersion(g, h, a)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n%s", kind, text)
	return err
}

// history is what origin holds about one change's approvals, read once.
type history struct {
	id, handle string
	tip        string // main on origin; "" when origin has none
	versions   []change.Version
	requests   map[string]string  // request name → the head its ref names
	outcomes   map[string]outcome // request name → its outcome record
	others     map[string]bool    // request names something other than this change maps to
	unread     bool               // a record on origin does not read
}

// outcome is what an outcome record says: the head it judged, and how.
type outcome struct {
	head, decision string
}

// readHistory reads origin once: every branch, record, request and
// outcome. It is the one place this file uses the inventory's readings of
// origin (changes.go, inventory.go, status.go), so moving them changes
// this function and nothing else here.
func readHistory(g git, id string) (*history, error) {
	o, err := listOrigin(g, inventoryPatterns...)
	if err != nil {
		return nil, err
	}
	all, unread, err := readAll(g, o)
	if err != nil {
		return nil, err
	}
	var want []string
	for ref, oid := range o.refs {
		if strings.HasPrefix(ref, outcomePrefix) {
			want = append(want, oid)
		}
	}
	if err := fetch(g, want...); err != nil {
		return nil, err
	}
	h := &history{
		id: id, handle: o.handle(id), unread: len(unread) > 0,
		requests: map[string]string{}, outcomes: map[string]outcome{}, others: map[string]bool{},
	}
	ours := map[string]bool{}
	for _, n := range all {
		if n.id == id {
			h.versions = n.record.Versions
		}
	}
	if h.versions == nil {
		return nil, failure("origin holds no record of change " + id)
	}
	for _, v := range h.versions {
		if v.Branch != "" {
			ours[v.Branch] = true
		}
	}
	for _, n := range all {
		if n.id == id {
			continue
		}
		for _, v := range n.record.Versions {
			if v.Branch != "" {
				h.others[changeName(v.Branch)] = true
			}
		}
	}
	for ref, oid := range o.refs {
		if ref == "refs/heads/"+recordTarget {
			h.tip = oid
		}
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok && !ours[branch] {
			h.others[changeName(branch)] = true
		}
		if name, ok := strings.CutPrefix(ref, requestPrefix); ok {
			h.requests[name] = oid
		}
		if name, ok := strings.CutPrefix(ref, outcomePrefix); ok {
			record, err := g.output(true, "cat-file", "blob", oid)
			if err != nil {
				record = ""
			}
			m := parseMemo(record)
			h.outcomes[name] = outcome{head: m.head, decision: m.decision}
		}
	}
	return h, nil
}

// head is version n's head, as an object ID.
func (h *history) head(n int) string {
	return strings.TrimPrefix(h.versions[n-1].Head, change.HeadPrefix)
}

// names are the request names a request for this change may be filed
// under: c-<change ID> first, then each version's branch's, each once. A
// branch whose name maps to c-<ID> adds nothing. That name is reserved
// for the change with that ID, which may be another change, and this
// change's own is already first.
func (h *history) names() []string {
	names := []string{change.RequestName(h.id)}
	seen := map[string]bool{names[0]: true}
	for _, v := range h.versions {
		if v.Branch == "" {
			continue
		}
		name := changeName(v.Branch)
		if _, reserved := change.RequestID(name); reserved {
			continue
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// ambiguous reports whether a request name may stand for something other
// than this change. c-<change ID> is this change's alone: no branch is
// filed under it.
func (h *history) ambiguous(name string) bool {
	return name != change.RequestName(h.id) && (h.unread || h.others[name])
}

// reference is the earlier version a version is compared with: the
// approved one, or for carry the one review showed.
type reference struct {
	version int    // the approved version; 0 for none
	by      string // the ref that names its head as approved; "" when it is not an approval
	unclear string // when there is none: why the approved version is not known, if it may exist
}

// comparison is what comparing a version with its reference found.
type comparison struct {
	kind, text string
	verb       string   // rebased, edited or recommitted, as the text opens
	edited     int      // the shown version's commits that differ from the earlier version's
	had, has   int      // the earlier and the shown version's commit counts
	mainEdited []string // the files whose change differs that a landing in between edited
	meta       bool     // the commits' messages or authors differ
	base, head string   // the shown version's base and head
	landings   []string // main's first-parent history from the shown version's base down to the reference's, both included
}

// reference finds the approved version below the version shown, as the
// file's comment describes.
func (h *history) reference(shown int) reference {
	for m := shown - 1; m >= 1; m-- {
		head := h.head(m)
		var ambiguousRef string
		for _, name := range h.names() {
			var refs []string
			if h.requests[name] == head {
				refs = append(refs, requestPrefix+name)
			}
			if o, ok := h.outcomes[name]; ok && o.head == head && approvalDecisions[o.decision] {
				refs = append(refs, outcomePrefix+name)
			}
			if len(refs) == 0 {
				continue
			}
			if !h.ambiguous(name) {
				return reference{version: m, by: refs[0]}
			}
			if ambiguousRef == "" {
				ambiguousRef = refs[0]
			}
		}
		if ambiguousRef != "" {
			return reference{unclear: fmt.Sprintf(
				"v%d at %s is named by %s, and another change or branch maps to that name too, so whether v%d was approved is not known",
				m, short(head), ambiguousRef, m)}
		}
	}
	return reference{}
}

// compareVersion compares the version a names with its approved version,
// and returns the kind and the text review shows.
func compareVersion(g git, h *history, a compareArgs) (kind, text string, err error) {
	if a.version > len(h.versions) {
		return "", "", failure(fmt.Sprintf("change %s has no v%d", h.handle, a.version))
	}
	if got := h.head(a.version); got != a.head {
		return "", "", failure(fmt.Sprintf("v%d of change %s records the head %s, not %s", a.version, h.handle, got, a.head))
	}
	ref := h.reference(a.version)
	if ref.version == 0 {
		if ref.unclear == "" {
			return compareNone, "", nil
		}
		shown := fmt.Sprintf("%s v%d at %s", h.handle, a.version, short(a.head))
		return compareNone, fmt.Sprintf("%s: %s; reviewing it in full.\n", shown, ref.unclear), nil
	}
	c, err := compareWith(g, h, a.tip, ref, a.version, a.verbose)
	return c.kind, c.text, err
}

// compareWith compares version n with the reference, against main's tip,
// as the file's comment describes.
func compareWith(g git, h *history, tip string, ref reference, n int, verbose bool) (comparison, error) {
	a := compareArgs{tip: tip, id: h.id, version: n, head: h.head(n), verbose: verbose}
	shown := fmt.Sprintf("%s v%d at %s", h.handle, a.version, short(a.head))
	old := ref.version
	h0 := h.head(old)
	approved, by := fmt.Sprintf("v%d at %s", old, short(h0)), ""
	if ref.by != "" {
		approved, by = "approved "+approved, " ("+ref.by+")"
	}
	withheld := func(why string) (comparison, error) {
		return comparison{kind: compareNone, text: fmt.Sprintf("%s: %s%s is not compared, since %s; reviewing it in full.\n",
			shown, approved, by, why)}, nil
	}

	b0, ok := uniqueBase(g, a.tip, h0)
	if !ok {
		return withheld(fmt.Sprintf("v%d's merge base with main is not one commit", old))
	}
	b1, ok := uniqueBase(g, a.tip, a.head)
	if !ok {
		return withheld(fmt.Sprintf("v%d's merge base with main is not one commit", a.version))
	}
	line, err := g.output(true, "rev-list", "--first-parent", a.tip)
	if err != nil {
		return comparison{}, failure("could not read main's history")
	}
	firstParent := strings.Fields(line)
	i0, i1 := indexOf(firstParent, b0), indexOf(firstParent, b1)
	switch {
	case i0 < 0:
		return withheld(fmt.Sprintf("v%d's base %s is not on main's first-parent history", old, short(b0)))
	case i1 < 0:
		return withheld(fmt.Sprintf("v%d's base %s is not on main's first-parent history", a.version, short(b1)))
	case i0 < i1:
		return withheld(fmt.Sprintf("v%d's base %s is above v%d's base %s on main", old, short(b0), a.version, short(b1)))
	}
	repo := delta.Repo{Dir: g.dir, Env: g.env}
	d0, err := delta.Between(repo, b0, h0)
	if err != nil {
		return withheld(fmt.Sprintf("v%d's files do not read as valley-delta-v1: %v", old, err))
	}
	d1, err := delta.Between(repo, b1, a.head)
	if err != nil {
		return withheld(fmt.Sprintf("v%d's files do not read as valley-delta-v1: %v", a.version, err))
	}
	c0, err := rangeCommits(g, b0, h0)
	if err != nil {
		return comparison{}, err
	}
	c1, err := rangeCommits(g, b1, a.head)
	if err != nil {
		return comparison{}, err
	}
	ids, err := patchIDs(g, append(oidsOf(c0), oidsOf(c1)...))
	if err != nil {
		return comparison{}, err
	}
	edited := editedCommits(c0, c1, ids)
	moved, sameDelta := b0 != b1, d0.Digest() == d1.Digest()
	verb := "edited"
	if edited == 0 && len(c0) == len(c1) {
		switch {
		case moved:
			verb = "rebased"
		case sameDelta:
			verb = "recommitted"
		}
	}
	// since names the earlier version: the one the operator approved, or
	// for carry the one review showed.
	since := fmt.Sprintf("v%d", old)
	if ref.by != "" {
		since = "you approved " + since
	}
	meta := metadataChanges(c0, c1, since, old, a.version)
	c := comparison{verb: verb, edited: edited, had: len(c0), has: len(c1), meta: meta != "",
		base: b1, head: a.head, landings: firstParent[i1 : i0+1]}

	var b strings.Builder
	if sameDelta {
		c.kind = compareClean
		fmt.Fprintf(&b, "%s: %s since %s at %s, with the same file changes.\n", shown, verb, since, short(h0))
		b.WriteString(meta)
		landings := firstParent[i1:i0]
		if !moved {
			fmt.Fprintf(&b, "Its base is still %s.\n", short(b1))
			c.text = b.String()
			return c, nil
		}
		fmt.Fprintf(&b, "Main moved %s → %s, landing %s:\n", short(b0), short(b1), counted(len(landings), "commit"))
		commits, err := readCommits(g, landings)
		if err != nil {
			return comparison{}, err
		}
		for i := len(commits) - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "  %s %s\n", short(commits[i].oid), visible(commits[i].subject()))
		}
		c.text = b.String()
		return c, nil
	}
	c.kind = compareChanged
	fmt.Fprintf(&b, "%s: %s since %s at %s.\n", shown, verb, since, short(h0))
	b.WriteString(meta)
	files := deltaDifferences(d0, d1)
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	edits, err := landingEdits(g, firstParent[i1:i0+1], paths)
	if err != nil {
		return comparison{}, err
	}
	if verb == "rebased" {
		if len(c1) == 1 {
			b.WriteString("Its commit is unchanged: same patch, same message.\n")
		} else {
			fmt.Fprintf(&b, "Its %d commits are unchanged: same patches, same messages.\n", len(c1))
		}
		for _, f := range files {
			if len(edits[f.path]) > 0 {
				c.mainEdited = append(c.mainEdited, f.path)
			}
		}
		touched := len(c.mainEdited)
		of := fmt.Sprintf("%d of its files", touched)
		if touched < len(files) {
			of = fmt.Sprintf("%d of the %d files whose change differs", touched, len(files))
		}
		fmt.Fprintf(&b, "Main moved %s → %s and also edited %s:\n", short(b0), short(b1), of)
	} else {
		if moved {
			fmt.Fprintf(&b, "Main moved %s → %s.\n", short(b0), short(b1))
		} else {
			fmt.Fprintf(&b, "Its base is still %s.\n", short(b1))
		}
		had := ""
		if len(c0) != len(c1) {
			had = fmt.Sprintf(" (v%d had %d)", old, len(c0))
		}
		differ := fmt.Sprintf("%d files differ", len(files))
		if len(files) == 1 {
			differ = "1 file differs"
		}
		fmt.Fprintf(&b, "%d of %s changed%s, and %s from v%d:\n", edited, counted(len(c1), "commit"), had, differ, old)
	}
	listing, err := fileListing(g, files, edits, moved, a.verbose, old, a.version)
	if err != nil {
		return comparison{}, err
	}
	b.WriteString(listing)
	if verb != "rebased" {
		fmt.Fprintf(&b, "\n── git range-diff %s..%s %s..%s ──\n", short(b0), short(h0), short(b1), short(a.head))
		out, err := g.output(true, "range-diff", "--no-color", b0+".."+h0, b1+".."+a.head)
		if err != nil {
			b.WriteString("  (git range-diff failed; view the full diff)\n")
		} else {
			b.WriteString(out + "\n")
		}
	}
	these := fmt.Sprintf("these %d files", len(files))
	if len(files) == 1 {
		these = "this file"
	}
	fmt.Fprintf(&b, "\n── v%d's diff of %s against main %s ──\n", a.version, these, short(b1))
	// The prefixes and the options are fixed, so that the person's
	// configuration cannot change the form review anchors notes by. The
	// paths are taken literally, as the delta has them, and a rename is a
	// deletion and an addition, as it is in the delta.
	out, err := g.output(true, append([]string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "--no-textconv",
		"--no-renames", "--no-relative", "--src-prefix=a/", "--dst-prefix=b/", b1, a.head, "--"}, paths...)...)
	switch {
	case err != nil:
		b.WriteString("  (git diff failed; view the full diff)\n")
	case out == "":
		fmt.Fprintf(&b, "  none: v%d leaves each of them as its base has it.\n", a.version)
	default:
		b.WriteString(out + "\n")
	}
	c.text = b.String()
	return c, nil
}

// uniqueBase is head's merge base with tip, when there is exactly one.
func uniqueBase(g git, tip, head string) (string, bool) {
	out, err := g.output(true, "merge-base", "--all", tip, head)
	bases := strings.Fields(out)
	if err != nil || len(bases) != 1 {
		return "", false
	}
	return bases[0], true
}

func indexOf(list []string, s string) int {
	for i, e := range list {
		if e == s {
			return i
		}
	}
	return -1
}

// fileDifference is a path whose change differs between two deltas, and
// its change in each: nil where a delta leaves it alone.
type fileDifference struct {
	path     string
	old, new *delta.Change
}

// deltaDifferences are the paths whose change differs between two
// deltas, sorted. A binary file or a mode that changed is one like any
// other.
func deltaDifferences(d0, d1 delta.Delta) []fileDifference {
	byPath := map[string][2]*delta.Change{}
	for side, d := range []delta.Delta{d0, d1} {
		for i := range d {
			pair := byPath[d[i].Path]
			pair[side] = &d[i]
			byPath[d[i].Path] = pair
		}
	}
	var files []fileDifference
	for p, pair := range byPath {
		if pair[0] != nil && pair[1] != nil && *pair[0] == *pair[1] {
			continue
		}
		files = append(files, fileDifference{path: p, old: pair[0], new: pair[1]})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files
}

// fileListing lists the files whose change differs. When main moved,
// each names the landings that edited it, oldest first, with their
// subjects cut to fit 100 columns. verbose adds each file's change in
// each version, as the mode and the first twelve hex digits of the
// SHA-256 of each image.
func fileListing(g git, files []fileDifference, edits map[string][]string, moved, verbose bool, old, shown int) (string, error) {
	var oids []string
	for _, f := range files {
		oids = append(oids, edits[f.path]...)
	}
	subjects := map[string]string{}
	commits, err := readCommits(g, oids)
	if err != nil {
		return "", err
	}
	for _, c := range commits {
		subjects[c.oid] = visible(c.subject())
	}
	width := 0
	for _, f := range files {
		width = max(width, columns(quotedPath(f.path)))
	}
	var b strings.Builder
	for _, f := range files {
		name := quotedPath(f.path)
		lead := "  " + name + strings.Repeat(" ", width-columns(name)) + "  "
		switch {
		case !moved:
			b.WriteString("  " + name + "\n")
		case len(edits[f.path]) == 0:
			b.WriteString(lead + "not edited on main\n")
		}
		for i, oid := range edits[f.path] {
			if i > 0 {
				lead = strings.Repeat(" ", columns(lead))
			}
			line := short(oid) + " " + subjects[oid]
			b.WriteString(lead + fit(line, 100-columns(lead)) + "\n")
		}
		if verbose {
			fmt.Fprintf(&b, "    v%d: %s\n    v%d: %s\n", old, described(f.old), shown, described(f.new))
		}
	}
	return b.String(), nil
}

// landingEdits maps each path to the landings in chain that edited it,
// oldest first. chain is main's first-parent history from B₁ down to B₀,
// so each landing is diffed against the commit after it, its first
// parent, and B₀ is no landing.
func landingEdits(g git, chain, paths []string) (map[string][]string, error) {
	edits := map[string][]string{}
	if len(chain) < 2 || len(paths) == 0 {
		return edits, nil
	}
	var in strings.Builder
	landing := map[string]bool{}
	for k := len(chain) - 2; k >= 0; k-- {
		fmt.Fprintf(&in, "%s %s\n", chain[k], chain[k+1])
		landing[chain[k]] = true
	}
	out, err := g.input(true, strings.NewReader(in.String()), append([]string{"--literal-pathspecs", "diff-tree", "--stdin",
		"-r", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--"}, paths...)...)
	if err != nil {
		return nil, failure("could not read which landings edited the files")
	}
	current := ""
	for _, field := range strings.Split(out, "\x00") {
		switch {
		case landing[field]:
			current = field
		case field != "" && current != "":
			edits[field] = append(edits[field], current)
		}
	}
	return edits, nil
}

// patchIDs maps each commit to git patch-id --verbatim of its diff from
// its first parent. The diff is in a fixed form, with binary content,
// modes and whole object names, so that the person's configuration
// cannot change it and two commits share an ID only when they make the
// same edit. A commit whose diff is empty has none.
func patchIDs(g git, oids []string) (map[string]string, error) {
	ids := map[string]string{}
	if len(oids) == 0 {
		return ids, nil
	}
	patches, err := g.input(true, strings.NewReader(strings.Join(oids, "\n")+"\n"), "log", "--stdin", "--no-walk=unsorted",
		"--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-relative", "--src-prefix=a/", "--dst-prefix=b/",
		"--binary", "--full-index", "--diff-merges=first-parent", "--patch", "--format=commit %H")
	if err != nil {
		return nil, failure("could not read the commits' patches")
	}
	out, err := g.input(true, strings.NewReader(patches+"\n"), "patch-id", "--verbatim")
	if err != nil {
		return nil, failure("could not read the commits' patch IDs")
	}
	for _, line := range strings.Split(out, "\n") {
		if id, oid, ok := strings.Cut(line, " "); ok {
			ids[oid] = id
		}
	}
	return ids, nil
}

// editedCommits counts the shown version's commits that are not the
// earlier version's, by patch, message and author. With as many commits
// on each side, each is set against the one in its place, so commits
// reordered are edited. Otherwise a commit is edited when no commit of
// the earlier version is the same.
func editedCommits(c0, c1 []commitText, ids map[string]string) int {
	key := func(c commitText) string {
		return ids[c.oid] + "\x00" + c.encoding + "\x00" + c.author + "\x00" + c.message
	}
	n := 0
	if len(c0) == len(c1) {
		for i := range c0 {
			if key(c0[i]) != key(c1[i]) {
				n++
			}
		}
		return n
	}
	left := map[string]int{}
	for _, c := range c0 {
		left[key(c)]++
	}
	for _, c := range c1 {
		if left[key(c)] > 0 {
			left[key(c)]--
		} else {
			n++
		}
	}
	return n
}

func oidsOf(commits []commitText) []string {
	oids := make([]string, len(commits))
	for i, c := range commits {
		oids[i] = c.oid
	}
	return oids
}

// described is a path's change in one delta, or unchanged for none.
func described(c *delta.Change) string {
	if c == nil {
		return "unchanged"
	}
	image := func(i delta.Image) string {
		if i.Absent() {
			return "absent"
		}
		return i.Mode + " " + fmt.Sprintf("%x", i.Sum[:6])
	}
	return image(c.Pre) + " -> " + image(c.Post)
}

// quotedPath is a path as it is printed: as it is when every character is
// printable, and otherwise in Go's quoting, which escapes the rest.
func quotedPath(p string) string {
	if utf8.ValidString(p) && strings.IndexFunc(p, func(r rune) bool { return !strconv.IsPrint(r) }) < 0 {
		return p
	}
	return strconv.Quote(p)
}

// commitText is one commit object's author and message, as stored.
type commitText struct {
	oid, author, message string
	encoding             string // the encoding header, when the commit has one
}

func (c commitText) subject() string {
	line, _, _ := strings.Cut(c.message, "\n")
	return line
}

// rangeCommits are the commits from base to head, oldest first, read
// whole.
func rangeCommits(g git, base, head string) ([]commitText, error) {
	out, err := g.output(true, "rev-list", "--reverse", base+".."+head)
	if err != nil {
		return nil, failure("could not list the commits from " + short(base) + " to " + short(head))
	}
	return readCommits(g, strings.Fields(out))
}

// readCommits reads commit objects with one git cat-file --batch, whose
// output is read as git wrote it: a message's own trailing newlines are
// part of the object.
func readCommits(g git, oids []string) ([]commitText, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Env, cmd.Dir = g.env, g.dir
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, failure("could not read the commits")
	}
	r := bufio.NewReader(bytes.NewReader(out))
	var commits []commitText
	for _, oid := range oids {
		header, err := r.ReadString('\n')
		fields := strings.Fields(header)
		if err != nil || len(fields) != 3 || fields[0] != oid || fields[1] != "commit" {
			return nil, failure("could not read the commit " + oid)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 {
			return nil, failure("could not read the commit " + oid)
		}
		data := make([]byte, size+1)
		if _, err := io.ReadFull(r, data); err != nil || data[size] != '\n' {
			return nil, failure("could not read the commit " + oid)
		}
		commits = append(commits, parseCommit(oid, data[:size]))
	}
	return commits, nil
}

// parseCommit reads the author and the message of a commit object: the
// headers up to the first blank line, and everything after it.
func parseCommit(oid string, data []byte) commitText {
	c := commitText{oid: oid}
	headers, message, _ := bytes.Cut(data, []byte("\n\n"))
	c.message = string(message)
	for _, line := range strings.Split(string(headers), "\n") {
		if v, ok := strings.CutPrefix(line, "author "); ok {
			c.author = v
		}
		if v, ok := strings.CutPrefix(line, "encoding "); ok {
			c.encoding = v
		}
	}
	return c
}

// metadataChanges says how the shown version's commits' messages and
// authors differ from the earlier version's, from each base to its head:
// nothing when they are the same. since names the earlier version, as
// "you approved v<n>" or "v<n>". With as many commits on each side, each
// commit is set against the one in its place, and only those whose
// message or author differs are shown. Otherwise every commit on each
// side is, with its author and its message.
func metadataChanges(c0, c1 []commitText, since string, old, shown int) string {
	var b strings.Builder
	author := func(c commitText) string { return " " + visible(c.author) }
	message := func(c commitText) string {
		lines := []string{""}
		if c.encoding != "" {
			lines = append(lines, "    (encoding "+visible(c.encoding)+")")
		}
		for _, l := range strings.Split(strings.TrimRight(c.message, "\n"), "\n") {
			lines = append(lines, "    "+visible(l))
		}
		return strings.Join(lines, "\n")
	}
	entry := func(v int, list []commitText, i int, show func(commitText) string) {
		fmt.Fprintf(&b, "  v%d, commit %d of %d, %s:%s\n", v, i+1, len(list), short(list[i].oid), show(list[i]))
	}
	if len(c0) != len(c1) {
		fmt.Fprintf(&b, "Commits edited since %s: v%d has %s, and v%d had %s. Read their authors and messages before approving:\n",
			since, shown, counted(len(c1), "commit"), old, counted(len(c0), "commit"))
		both := func(c commitText) string { return author(c) + message(c) }
		for i := range c0 {
			entry(old, c0, i, both)
		}
		for i := range c1 {
			entry(shown, c1, i, both)
		}
		return b.String()
	}
	for _, field := range []struct {
		heading string
		of      func(commitText) string
		show    func(commitText) string
	}{
		{"Commit messages edited since %s. Read them before approving:\n",
			func(c commitText) string { return c.encoding + "\x00" + c.message }, message},
		{"Commit authors edited since %s:\n",
			func(c commitText) string { return c.author }, author},
	} {
		headed := false
		for i := range c0 {
			if field.of(c0[i]) == field.of(c1[i]) {
				continue
			}
			if !headed {
				fmt.Fprintf(&b, field.heading, since)
				headed = true
			}
			entry(old, c0, i, field.show)
			entry(shown, c1, i, field.show)
		}
	}
	return b.String()
}
