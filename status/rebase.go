// valley-go rebase: a change's whole stack, rebased onto main.
//
// `valley rebase <change>` and review's [b]ase run it. The stack is what
// valley-go stack lists (stack.go): the change, and the chain of layers
// under it, each on the one below. A change with nothing under it is a
// stack of one, and is rebased the same way.
//
// Each layer's own commits are rebased onto the rebased layer below it,
// and the bottom's onto main, in one throwaway worktree, bottom first. So
// every layer keeps its own delta, and the stack stays a stack. Commits
// under the bottom that a lower change landed through an integrator
// merge drop out: their content is on main already. They are the commits
// a landed version's head reaches (inventory.go reads what landed).
//
// The rebase refuses, and changes nothing, when the stack cannot move
// whole:
//
//   - a change layer is not ready, names no branch, or its branch is
//     missing on origin or not at its recorded head;
//   - a plain branch in the stack is the branch of a change that is no
//     layer, as a change is once a rebase has moved its branch and could
//     not add the rebased head to its record;
//   - two layers are on one branch;
//   - the commits to be rebased hold a merge, or a version head of a
//     change that is not ready or is no layer of the stack. A rebase gives
//     every commit a new ID, so such a commit would ride into the stack
//     under an ID no version records. That is the rule the integrator
//     lands by (integrator/records.go), applied before the rebase;
//   - a layer is a change and git config valley.principal names no one.
//
// It then publishes in two phases:
//
//  1. One atomic push moves every layer's branch, each leased on the tip
//     the rebase read. Either every branch moves or none does.
//  2. Bottom first, each change's rebased head is added to its record as
//     a new version, as ready --if-active adds one: only while the change
//     is still ready at the head the rebase began with. A failure for one
//     change does not stop the others. The change may have been unreadied
//     meanwhile, its record may have moved, or the hook may refuse the
//     write.
//
// A stack prints a manifest at the end: each layer's old and new head,
// whether its branch moved, whether a version was added, and the exact
// valley ready command that finishes each layer left unfinished. A change
// alone prints what rebase has always printed.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"the-valley/change"
)

const rebaseUsage = "usage: valley-go rebase [--head <sha>] <change>"

// What became of a layer's branch in the push.
const (
	pushNot     = "not pushed"
	pushDone    = "pushed"
	pushUnknown = "may have moved"
)

// errReported is a failure whose words are printed already.
var errReported = errors.New("reported")

// layer is one layer of a stack as the rebase captured it, and what
// became of it.
type layer struct {
	k      int    // its place, from 0 at the bottom
	name   string // a change's handle, or a branch's name
	id     string // a change's ID; "" for a plain branch
	branch string // the branch the rebase pushes
	tip    string // that branch's tip on origin as read; "" when it is missing

	// A change's record as the rebase read it: its ref and commit, its
	// state, and its active version.
	ref, commit, state string
	record             *change.Record
	version            int

	head string // the head rebased: the active version's, or the branch's tip
	base string // the commit its own commits start above

	rebased string // its rebased head
	pushed  string // what became of its branch: pushNot, pushDone or pushUnknown
	added   int    // the version phase 2 added, or 0
}

// rebaseRun is one rebase of a stack.
type rebaseRun struct {
	g              git
	s              *snapshot
	given          string   // the name the change was given by
	layers         []*layer // bottom first, the change itself last
	tip            string   // main on origin as read: what the stack is rebased onto
	cut            bool     // whether landed commits under the bottom drop out
	stdout, stderr io.Writer
}

func (r *rebaseRun) top() *layer  { return r.layers[len(r.layers)-1] }
func (r *rebaseRun) single() bool { return len(r.layers) == 1 }

// place is where a layer sits, for a refusal that names it: nothing for a
// change alone, and otherwise ", layer <k> of <n> under <top>,".
func (r *rebaseRun) place(l *layer) string {
	if r.single() {
		return ""
	}
	return fmt.Sprintf(", layer %d of %d under %s,", l.k+1, len(r.layers), r.top().name)
}

func parseRebaseArgs(args []string) (name, want string, err error) {
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--head" && i+1 < len(args):
			want = args[i+1]
			i++
		case strings.HasPrefix(a, "--head="):
			want = strings.TrimPrefix(a, "--head=")
		case strings.HasPrefix(a, "-"):
			return "", "", failure(rebaseUsage)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) != 1 || (want != "" && !isObjectID(want)) {
		return "", "", failure(rebaseUsage)
	}
	return rest[0], want, nil
}

// runRebase rebases the stack under the change named. --head is the head
// the caller showed as the change's: the rebase refuses when the change
// is at another head by now.
func runRebase(g git, args []string, stdout, stderr io.Writer) int {
	err := rebaseStack(g, args, stdout, stderr)
	switch {
	case err == nil:
		return 0
	case !errors.Is(err, errReported):
		fmt.Fprintf(stderr, "valley: %s\n", err)
	}
	return 1
}

func rebaseStack(g git, args []string, stdout, stderr io.Writer) error {
	name, want, err := parseRebaseArgs(args)
	if err != nil {
		return err
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	res, err := resolveRecorded(g, name)
	if err != nil {
		return err
	}
	if res.id != "" && res.branch == "" {
		return failure(fmt.Sprintf("change %s v%d records no branch, and rebase acts on a change's branch. Approve its head alone: valley approve --head %s %s",
			res.handle, res.version, res.head, res.handle))
	}
	s, err := readSnapshot(g, verifierFromEnv())
	if err != nil {
		return err
	}
	if s.tip == "" {
		return failure("origin has no " + recordTarget + " to rebase onto")
	}
	chain, err := s.layers(res)
	if err != nil {
		return err
	}
	r := &rebaseRun{g: g, s: s, given: name, tip: s.tip, stdout: stdout, stderr: stderr}
	r.capture(chain)
	if err := r.preflight(want); err != nil {
		return err
	}
	// review offers [b]ase only to a stack that is stale. One that
	// fast-forwards already is left alone, rather than rewritten for
	// nothing.
	if top := r.top(); g.succeeds(true, "merge-base", "--is-ancestor", r.tip, r.layers[0].head) {
		fmt.Fprintf(stdout, "%s already fast-forwards from origin/%s; nothing rebased\n  head     %s\n", top.branch, recordTarget, top.head)
		return nil
	}
	if err := r.needPrincipal(); err != nil {
		return err
	}
	if err := r.rebase(); err != nil {
		return err
	}
	if err := r.revalidate(); err != nil {
		return err
	}
	return r.publish()
}

// capture reads each layer's identity from the snapshot the stack was
// read from: a change's record ref and commit, its active version, head
// and branch, and every branch's tip.
func (r *rebaseRun) capture(chain []*entry) {
	for k, e := range chain {
		l := &layer{k: k, name: e.ref, id: e.id, head: e.head, state: e.state}
		if e.id == "" {
			l.branch = e.ref
		}
		for _, n := range r.s.all {
			if e.id != "" && n.id == e.id {
				v := n.record.ActiveVersion()
				l.ref, l.commit, l.record = change.Ref(recordTarget, n.id), n.commit, n.record
				l.version, l.branch = v.N, v.Branch
			}
		}
		if l.branch != "" {
			l.tip = r.s.o.refs["refs/heads/"+l.branch]
		}
		r.layers = append(r.layers, l)
	}
}

// preflight refuses a stack the rebase cannot move whole, before anything
// is rebased, and finds where each layer's own commits start.
func (r *rebaseRun) preflight(want string) error {
	var tips []string // the branches not at their changes' recorded heads
	inStack := map[string]bool{}
	for _, l := range r.layers {
		inStack[l.id] = true
	}
	for _, l := range r.layers {
		switch {
		case l.id != "" && l.branch == "":
			return failure(fmt.Sprintf("change %s v%d%s records no branch, so the rebase has no branch to push it to; nothing rebased",
				l.name, l.version, r.place(l)))
		case l.tip == "" && r.single():
			return failure("no such branch on origin: " + l.branch)
		case l.tip == "":
			return failure(fmt.Sprintf("change %s v%d%s is on the branch %s, and origin has no such branch; nothing rebased",
				l.name, l.version, r.place(l), l.branch))
		case l.id != "" && l.state != change.Ready:
			return failure(fmt.Sprintf("change %s v%d%s is %s, not ready; nothing rebased. Ready it first: %s",
				l.name, l.version, r.place(l), l.state, readyHint(l.id, l.branch)))
		case l == r.top() && want != "" && l.head != want && l.id != "":
			return failure(fmt.Sprintf("change %s is at v%d, %s, and this run began at %s; nothing rebased. Review it again at its new head",
				l.name, l.version, l.head, r.versionAt(l, want)))
		case l == r.top() && want != "" && l.head != want:
			return failure(fmt.Sprintf("origin/%s is at %s, and is no longer at %s, the head shown; nothing rebased", l.branch, l.head, want))
		case l.id != "" && l.tip != l.head:
			tips = append(tips, fmt.Sprintf("change %s v%d%s records the head %s, and origin/%s is at %s; nothing rebased. Ready the branch's head as a new version of %s to act on it: %s",
				l.name, l.version, r.place(l), l.head, l.branch, l.tip, l.name, readyCommand(l.tip, l.id, l.branch)))
		}
		if l.id != "" {
			continue
		}
		// A plain branch that is a change's branch holds a head no version
		// of that change records, as after a rebase that moved the branch
		// and could not add the version.
		for _, n := range r.s.all {
			v := n.record.ActiveVersion()
			if inStack[n.id] || v.Branch != l.branch || !unlanded(r.s, n) {
				continue
			}
			h := r.s.o.handle(n.id)
			tips = append(tips, fmt.Sprintf("change %s v%d records the head %s, and origin/%s%s is at %s; nothing rebased. Ready the branch's head as a new version of %s to act on it: %s",
				h, v.N, headOf(v), l.branch, r.place(l), l.tip, h, readyCommand(l.tip, n.id, l.branch)))
		}
	}
	// Two layers on one branch leave a change's branch off its recorded
	// head as well, so they are refused first, for what they are.
	on := map[string]*layer{}
	for _, l := range r.layers {
		if o := on[l.branch]; o != nil {
			return failure(fmt.Sprintf("%s and %s, layers %d and %d of %d under %s, are both on the branch %s; nothing rebased",
				o.name, l.name, o.k+1, l.k+1, len(r.layers), r.top().name, l.branch))
		}
		on[l.branch] = l
	}
	if len(tips) > 0 {
		return failure(strings.Join(tips, "\nvalley: "))
	}
	return r.commits(inStack)
}

// commits reads the commits the rebase moves: those the top's head
// reaches, less those main reaches and those a landed version's head
// reaches under the bottom. It refuses a merge among them, and a version
// head of a change that is not ready or is no layer. It sets where each
// layer's own commits start: for the bottom, the parent of its oldest
// commit; for every other layer, the head of the layer below.
func (r *rebaseRun) commits(inStack map[string]bool) error {
	bottom, top := r.layers[0], r.top()
	not := []string{"^" + r.tip}
	reach := r.s.reach(bottom.head)
	for _, n := range r.s.all {
		for _, v := range n.record.Versions {
			if h := headOf(v); reach[h] && r.s.isLanded(n.id, v) {
				not, r.cut = append(not, "^"+h), true
			}
		}
	}
	out, err := r.g.output(true, append([]string{"rev-list", "--parents", top.head}, not...)...)
	if err != nil {
		return failure(fmt.Sprintf("could not read the commits under %s; nothing rebased", top.name))
	}
	moved := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) > 2 {
			return failure(fmt.Sprintf("the commits under %s include the merge %s, and a rebase moves a line of commits; nothing rebased", top.name, short(f[0])))
		}
		if len(f) > 0 {
			moved[f[0]] = true
		}
	}
	for _, n := range r.s.all {
		for _, v := range n.record.Versions {
			c := headOf(v)
			if !moved[c] || (inStack[n.id] && n.record.State == change.Ready) {
				continue
			}
			h, why := r.s.o.handle(n.id), "which is no layer of this stack"
			if n.record.State != change.Ready {
				why = fmt.Sprintf("which is %s, not ready", n.record.State)
			}
			return failure(fmt.Sprintf("the commit %s under %s is v%d of change %s, %s; nothing rebased. A rebase gives each commit a new ID, which no version of %s would record",
				short(c), top.name, v.N, h, why, h))
		}
	}
	out, err = r.g.output(true, append([]string{"rev-list", "--parents", "--reverse", bottom.head}, not...)...)
	if err != nil {
		return failure(fmt.Sprintf("could not read the commits of %s; nothing rebased", bottom.name))
	}
	bottom.base = bottom.head
	if oldest, _, _ := strings.Cut(out, "\n"); oldest != "" {
		f := strings.Fields(oldest)
		if len(f) < 2 {
			return failure(fmt.Sprintf("%s and %s share no history; nothing rebased", bottom.name, recordTarget))
		}
		bottom.base = f[1]
	}
	for k := 1; k < len(r.layers); k++ {
		r.layers[k].base = r.layers[k-1].head
	}
	return nil
}

// versionAt names a head of a change by its version too, when its record
// has one there.
func (r *rebaseRun) versionAt(l *layer, head string) string {
	for _, v := range l.record.Versions {
		if headOf(v) == head {
			return fmt.Sprintf("v%d, %s", v.N, head)
		}
	}
	return head
}

// needPrincipal refuses a stack that holds a change when git config
// valley.principal names no one: phase 2 writes each change's record as
// that principal, and finding out there would leave every branch moved
// and no version added.
func (r *rebaseRun) needPrincipal() error {
	changes := false
	for _, l := range r.layers {
		changes = changes || l.id != ""
	}
	if p, err := r.g.output(true, "config", "valley.principal"); !changes || err == nil && change.ValidPrincipal(p) {
		return nil
	}
	what := fmt.Sprintf("rebasing change %s adds the rebased head to its record", r.top().name)
	if !r.single() {
		what = fmt.Sprintf("rebasing the stack under %s adds each change's rebased head to its record", r.top().name)
	}
	return failure(what + " as the principal your key pushes as, and git config valley.principal names none, so nothing is rebased; set it with git config valley.principal <name>")
}

// rebase rebases every layer in one throwaway worktree, bottom first, the
// user's checkout untouched: the bottom onto main, and each layer above
// onto the rebased layer below it. A conflict anywhere ends it with
// nothing pushed. The worktree is removed however the rebase ends, an
// interrupt included.
func (r *rebaseRun) rebase() error {
	dir, err := os.MkdirTemp("", "valley-rebase.")
	if err != nil {
		return failure("could not make a directory for the rebase; nothing changed")
	}
	wt := r.g
	wt.dir = dir
	added := false
	// git runs in dir only once it is a worktree: a directory that is not
	// one would leave git to find the repository above it.
	cleanup := func() {
		if added {
			wt.succeeds(true, "rebase", "--abort")
			r.g.succeeds(true, "worktree", "remove", "--force", dir)
		}
		os.RemoveAll(dir)
	}
	interrupted := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-interrupted:
			cleanup()
			os.Exit(130)
		case <-done:
		}
	}()
	defer func() {
		signal.Stop(interrupted)
		close(done)
		cleanup()
	}()

	bottom := r.layers[0]
	if _, err := r.g.output(false, "worktree", "add", "--quiet", "--detach", dir, bottom.head); err != nil {
		return failure(fmt.Sprintf("could not check out origin/%s for the rebase; nothing changed", bottom.branch))
	}
	added = true
	onto := r.tip
	for _, l := range r.layers {
		// rebase.updateRefs would move the person's own branches that
		// point into the commits rebased.
		args := []string{"-c", "rebase.updateRefs=false", "rebase", "--quiet"}
		if l.k == 0 && !r.cut {
			args = append(args, onto, l.head)
		} else {
			args = append(args, "--onto", onto, l.base, l.head)
		}
		if err := wt.passing(r.stdout, args...); err != nil {
			return failure(r.conflict(l))
		}
		head, err := wt.output(true, "rev-parse", "HEAD")
		if err != nil {
			return failure(fmt.Sprintf("could not read the rebased head of %s; nothing pushed", l.branch))
		}
		if head == onto {
			return failure(fmt.Sprintf("%s adds nothing to %s once rebased, as when its commits have landed already; nothing pushed", l.name, r.below(l)))
		}
		l.rebased, onto = head, head
	}
	return nil
}

// below is what a layer is rebased onto, as a refusal names it.
func (r *rebaseRun) below(l *layer) string {
	if l.k == 0 {
		return recordTarget
	}
	return r.layers[l.k-1].name + " as rebased"
}

func (r *rebaseRun) conflict(l *layer) string {
	if r.single() {
		return fmt.Sprintf("%s conflicts with main — resolve by hand; nothing changed", l.branch)
	}
	return fmt.Sprintf("%s, layer %d of %d under %s, conflicts with %s — resolve by hand; nothing changed",
		l.name, l.k+1, len(r.layers), r.top().name, r.below(l))
}

// revalidate reads origin once more, just before the push: each change
// layer's record must still be the record commit the rebase read, and each
// plain branch must still be a branch no record is on.
//
// This is the last check, and a window remains between it and the push.
// A change unreadied or withdrawn in that window, or a branch readied, is
// rebased all the same. git compares only the refs a push changes, and the
// record refs are not among them, so no client can make the push
// conditional on a record. Phase 2 then adds each version only while the
// change is still ready at the head the rebase began with, under a lease
// on the record, so a change unreadied in that window stays a draft at
// its old head, the manifest names it, and the integrator lands nothing
// for it.
func (r *rebaseRun) revalidate() error {
	o, err := listOrigin(r.g, inventoryPatterns...)
	if err != nil {
		return failure("could not read origin again before the push; nothing pushed")
	}
	for _, l := range r.layers {
		if l.id != "" {
			if now := o.refs[l.ref]; now != l.commit {
				return r.recordMoved(l, now)
			}
			continue
		}
		name := r.given
		if !r.single() {
			name = "origin/" + l.branch + r.place(l)
		}
		found, err := unlandedOn(r.g, o, l.branch)
		if err != nil {
			return failure(fmt.Sprintf("could not read %s again against origin's records; nothing pushed", strings.TrimSuffix(name, ",")))
		}
		if len(found) == 0 {
			continue
		}
		n := found[0]
		v, h := n.record.ActiveVersion(), o.handle(n.id)
		switch {
		case n.record.State != change.Ready:
			return failure(fmt.Sprintf("%s is now change %s v%d, which is %s, not ready; nothing pushed. Ready it first: %s",
				name, h, v.N, n.record.State, readyHint(n.id, v.Branch)))
		case headOf(v) != l.head:
			return failure(fmt.Sprintf("%s is now change %s v%d, which records the head %s, and this run began at %s; nothing pushed. Review it again by its handle",
				name, h, v.N, headOf(v), l.head))
		}
		return failure(fmt.Sprintf("%s is now change %s v%d; nothing pushed. Rebase it again by its handle: valley rebase %s", name, h, v.N, h))
	}
	return nil
}

// recordMoved is the refusal for a change layer whose record is no longer
// the record commit the rebase read.
func (r *rebaseRun) recordMoved(l *layer, now string) error {
	if now == "" {
		return failure(fmt.Sprintf("origin no longer holds a record of change %s; nothing pushed", l.name))
	}
	rec, err := func() (*change.Record, error) {
		if err := fetch(r.g, now); err != nil {
			return nil, err
		}
		return readRecord(r.g, now)
	}()
	if err != nil {
		return failure(fmt.Sprintf("could not read the record of change %s again; nothing pushed", l.name))
	}
	v := rec.ActiveVersion()
	switch {
	case rec.State != change.Ready:
		return failure(fmt.Sprintf("change %s v%d%s is %s, not ready; nothing pushed. Ready it first: %s",
			l.name, v.N, r.place(l), rec.State, readyHint(l.id, v.Branch)))
	case headOf(v) != l.head:
		return failure(fmt.Sprintf("change %s%s is at v%d, %s, and this run began at v%d, %s; nothing pushed. Review it again at its new head",
			l.name, r.place(l), v.N, headOf(v), l.version, l.head))
	}
	return failure(fmt.Sprintf("the record of change %s%s changed while it was rebased; nothing pushed. Rebase it again: valley rebase %s",
		l.name, r.place(l), r.top().name))
}

// publish pushes every branch, then adds each change's version, and says
// what became of each layer.
func (r *rebaseRun) publish() error {
	top := r.top()
	switch outcome, why := r.pushBranches(); outcome {
	case pushNot:
		if r.single() {
			return failure(fmt.Sprintf("lease on origin/%s failed (it moved since the fetch); nothing pushed", top.branch))
		}
		r.writeManifest()
		return failure(fmt.Sprintf("origin refused the push of the stack's branches, so none moved (%s); nothing pushed. Rebase the stack again: valley rebase %s",
			strings.Join(why, "; "), top.name))
	case pushUnknown:
		r.writeManifest()
		var refs []string
		for _, l := range r.layers {
			refs = append(refs, shellQuote("refs/heads/"+l.branch))
		}
		return failure("the push of the branches failed, and whether origin took it is not known. Check with git ls-remote origin " +
			strings.Join(refs, " ") + ": a branch at its new head is pushed, and the command above finishes its change")
	}
	if r.single() {
		fmt.Fprintf(r.stdout, "rebased %s onto origin/%s, pushed with --force-with-lease\n", top.branch, recordTarget)
		fmt.Fprintf(r.stdout, "  head     %s\n", top.rebased)
	}
	r.addVersions()
	if !r.single() {
		r.writeManifest()
	}
	r.otherStacks()
	n := len(unfinished(r.layers))
	switch {
	case n > 0 && r.single():
		return errReported
	case n == 1:
		return failure(fmt.Sprintf("1 change of the stack under %s is unfinished; finish it with the command above", top.name))
	case n > 0:
		return failure(fmt.Sprintf("%d changes of the stack under %s are unfinished; finish each with the command above", n, top.name))
	}
	return nil
}

// pushBranches is phase 1: one atomic push of every layer's branch, each
// leased on the tip the rebase read. It says whether the branches moved,
// did not, or may have; and, when origin refused them, why, as git
// reports each ref.
func (r *rebaseRun) pushBranches() (string, []string) {
	args := []string{"push", "--atomic", "--porcelain"}
	for _, l := range r.layers {
		args = append(args, "--force-with-lease=refs/heads/"+l.branch+":"+l.tip)
	}
	args = append(args, "origin")
	for _, l := range r.layers {
		args = append(args, l.rebased+":refs/heads/"+l.branch)
	}
	out, err := r.g.output(false, args...)
	outcome := pushDone
	var why []string
	if err != nil {
		outcome = pushUnknown
		// A ref git reports as refused is one origin did not move, and
		// an atomic push moves all of its refs or none. With no ref
		// reported, the push may have reached origin before it failed.
		for _, line := range strings.Split(out, "\n") {
			f := strings.SplitN(line, "\t", 3)
			if len(f) == 3 && f[0] == "!" {
				_, to, _ := strings.Cut(f[1], ":")
				why = append(why, "origin/"+strings.TrimPrefix(to, "refs/heads/")+": "+f[2])
				outcome = pushNot
			}
		}
	}
	for _, l := range r.layers {
		l.pushed = outcome
	}
	return outcome, why
}

// addVersions is phase 2: bottom first, each change's rebased head is
// added to its record as a new version, while the change is still ready
// at the head the rebase began with. A change alone says what ready says,
// as it always has.
func (r *rebaseRun) addVersions() {
	for _, l := range r.layers {
		if l.id == "" {
			continue
		}
		var w io.Writer = io.Discard
		if r.single() {
			w = r.stdout
		}
		n, err := ready(r.g, readyArgs{head: l.rebased, id: l.id, ifActive: l.head, branch: l.branch}, w)
		if err != nil {
			fmt.Fprintf(r.stderr, "valley: %s\n", err)
			if r.single() {
				fmt.Fprintf(r.stderr, "valley: origin/%s is rebased to %s, and no version of %s records it. Ready it once the change is ready: %s\n",
					l.branch, l.rebased, l.name, readyCommand(l.rebased, l.id, l.branch))
			}
			continue
		}
		l.added = n
	}
}

// unfinished is each change layer whose branch may hold its rebased head
// with no version recording it.
func unfinished(layers []*layer) []*layer {
	var left []*layer
	for _, l := range layers {
		if l.id != "" && l.pushed != pushNot && l.added == 0 {
			left = append(left, l)
		}
	}
	return left
}

func (r *rebaseRun) writeManifest() {
	io.WriteString(r.stdout, manifest(r.top().name, r.layers))
}

// manifest is what the rebase did to each layer, bottom first, one line
// each: its old and new head, what became of its branch, and for a change
// whether a version was added. Then, for each change left unfinished, the
// valley ready command that finishes it.
func manifest(top string, layers []*layer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "the stack under %s, bottom first:\n", top)
	width := 0
	for _, l := range layers {
		width = max(width, columns(l.name))
	}
	for _, l := range layers {
		rebased := "(not rebased)"
		if l.rebased != "" {
			rebased = short(l.rebased)
		}
		status := "origin/" + l.branch + " " + l.pushed
		switch {
		case l.id != "" && l.added > 0:
			status += fmt.Sprintf(", v%d added", l.added)
		case l.id != "":
			status += ", no version added"
		}
		fmt.Fprintf(&b, "  %s%s  %s -> %s  %s\n", l.name, strings.Repeat(" ", width-columns(l.name)), short(l.head), rebased, status)
	}
	for _, l := range unfinished(layers) {
		fmt.Fprintf(&b, "to finish %s: %s\n", l.name, readyCommand(l.rebased, l.id, l.branch))
	}
	return b.String()
}

// otherStacks names each ready change and plain branch outside the stack
// whose head contains a layer's old head, such as a second stack on a
// lower layer. The rebase left it on that old head, and does not move it.
func (r *rebaseRun) otherStacks() {
	ids, branches := map[string]bool{}, map[string]bool{}
	for _, l := range r.layers {
		ids[l.id], branches[l.branch] = true, true
	}
	on := func(head string) *layer {
		for k := len(r.layers) - 1; k >= 0; k-- {
			if l := r.layers[k]; head == l.head || r.s.reach(head)[l.head] {
				return l
			}
		}
		return nil
	}
	for _, n := range r.s.all {
		if ids[n.id] || n.record.State != change.Ready || !unlanded(r.s, n) {
			continue
		}
		if l := on(headOf(n.record.ActiveVersion())); l != nil {
			h := r.s.o.handle(n.id)
			fmt.Fprintf(r.stdout, "%s is now on an old head of %s; rebase it with valley rebase %s\n", h, l.name, h)
		}
	}
	for _, b := range r.s.unrecorded() {
		if l := on(b.Head); l != nil && !branches[b.Branch] {
			fmt.Fprintf(r.stdout, "%s is now on an old head of %s; rebase it with valley rebase %s\n", branchName(b.Branch), l.name, shellQuote(b.Branch))
		}
	}
}

// readyHint is the valley ready line that readies a change again, naming
// the branch unless its version records none, as bin/valley's ready_hint
// writes it.
func readyHint(id, branch string) string {
	if branch == "" {
		return "valley ready --change " + id
	}
	return "valley ready --change " + id + " " + shellQuote(branch)
}

// readyCommand is the valley ready line that adds head to a change's
// record as a new version on branch.
func readyCommand(head, id, branch string) string {
	return "valley ready --head " + head + " --change " + id + " " + shellQuote(branch)
}

// passing runs git with its output on w, and its diagnostics on g's
// stderr.
func (g git) passing(w io.Writer, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = g.env, g.dir, w, g.stderr
	return cmd.Run()
}

// runBrings prints each change that has not landed, other than the one
// --change names, with a version head among the commits a head would
// bring to main: those it reaches and main does not. One handle a line,
// in the order of the changes' IDs. approve refuses a head that does not
// fast-forward from main and would bring one, since it is a layer of a
// stack that has to be rebased first.
func runBrings(g git, args []string, w io.Writer) error {
	const usage = "usage: valley-go brings [--change <id>] <head>"
	var except, head string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--change" && i+1 < len(args):
			except = args[i+1]
			i++
		case head == "" && isObjectID(args[i]):
			head = args[i]
		default:
			return failure(usage)
		}
	}
	if head == "" {
		return failure(usage)
	}
	if !g.succeeds(true, "cat-file", "-e", head+"^{commit}") {
		return failure("no commit " + head + " here")
	}
	s, err := readSnapshot(g, verifierFromEnv())
	if err != nil {
		return err
	}
	if s.tip == "" {
		return failure("origin has no " + recordTarget + " to read against")
	}
	reach := s.reach(head)
	var b strings.Builder
	for _, n := range s.all {
		if n.id == except || !unlanded(s, n) {
			continue
		}
		for _, v := range n.record.Versions {
			if reach[headOf(v)] {
				fmt.Fprintln(&b, s.o.handle(n.id))
				break
			}
		}
	}
	_, err = io.WriteString(w, b.String())
	return err
}
