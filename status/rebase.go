// valley-go rebase: a change's whole stack, rebased onto main.
//
// `valley rebase <change>` and review's [b]ase run it. The stack is what
// valley-go stack lists (stack.go): the change, and the chain of layers
// under it, each on the one below. A change with nothing under it is a
// stack of one, and is rebased the same way.
//
// The rebase engine (../rebase) does the work. It captures the chain from
// one snapshot of origin, refuses a stack it cannot move whole and changes
// nothing, rebases every layer in one throwaway worktree, bottom first,
// and publishes in two phases: one atomic push of every layer's branch,
// leased on the tips it read, and then, bottom first, each change's
// rebased head added to its record as a new version, as
// ready --if-active adds one. This verb words what the engine reports. It
// refuses a stack that holds a change when git config valley.principal
// names no one, since phase 2 writes each record as that principal.
//
// A stack prints a manifest at the end: each layer's old and new head,
// whether its branch moved, whether a version was added, and the exact
// valley ready command that finishes each layer left unfinished. A change
// alone prints what rebase has always printed.
//
// An interrupt or a SIGTERM while the stack is rebased cancels the
// rebase: the engine kills git and anything git started, such as a hook,
// and removes its worktree and its refs. Nothing is pushed, and the verb
// exits 130.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"the-valley/change"
	"the-valley/rebase"
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

// errInterrupted is a rebase a signal cancelled. It prints nothing, and
// exits 130.
var errInterrupted = errors.New("interrupted")

// layer is one line of the manifest: a layer of the stack, and what became
// of it.
type layer struct {
	name    string // a change's handle, or a branch's name
	id      string // a change's ID; "" for a plain branch
	branch  string // the branch the rebase pushes
	head    string // the head rebased
	rebased string // its rebased head
	pushed  string // what became of its branch: pushNot, pushDone or pushUnknown
	added   int    // the version phase 2 added, or 0
}

// rebaseRun is one rebase of a stack.
type rebaseRun struct {
	g              git
	s              *snapshot
	f              *rebase.Family
	given          string // the name the change was given by
	stdout, stderr io.Writer
}

// top is the change the stack is under, the last of its layers.
func (r *rebaseRun) top() *rebase.Layer { return r.f.Start }
func (r *rebaseRun) single() bool       { return len(r.f.Layers) == 1 }

// index is a layer's place in the stack, from 0 at the bottom.
func (r *rebaseRun) index(l *rebase.Layer) int {
	for k, m := range r.f.Layers {
		if m == l {
			return k
		}
	}
	return -1
}

// place is where a layer sits, for a refusal that names it: nothing for a
// change alone, and otherwise ", layer <k> of <n> under <top>,".
func (r *rebaseRun) place(l *rebase.Layer) string {
	if r.single() {
		return ""
	}
	return fmt.Sprintf(", layer %d of %d under %s,", r.index(l)+1, len(r.f.Layers), r.top().Name)
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
	case errors.Is(err, errInterrupted):
		return 130
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
	f, err := rebase.Capture(s.land, rebase.Selection{ID: res.id, Branch: res.branch, Head: want})
	if err != nil {
		return err
	}
	r := &rebaseRun{g: g, s: s, f: f, given: name, stdout: stdout, stderr: stderr}
	if err := f.Check(); err != nil {
		return r.refused(err)
	}
	// review offers [b]ase only to a stack that is stale. One that
	// fast-forwards already is left alone, rather than rewritten for
	// nothing.
	if top := r.top(); f.FastForwards() {
		fmt.Fprintf(stdout, "%s already fast-forwards from origin/%s; nothing rebased\n  head     %s\n", top.Branch, recordTarget, top.Head)
		return nil
	}
	principal, err := r.needPrincipal()
	if err != nil {
		return err
	}
	eg := g.engine()
	eg.Stdout = stdout
	p, err := r.prepare(eg)
	if err != nil {
		return err
	}
	defer p.Release(context.Background(), eg)
	pub, err := rebase.Publish(context.Background(), eg, p)
	if err != nil {
		return r.refused(err)
	}
	return r.publish(eg, pub, principal)
}

// needPrincipal is the principal git config valley.principal names, and
// refuses a stack that holds a change when it names no one: phase 2 writes
// each change's record as that principal, and finding out there would
// leave every branch moved and no version added.
func (r *rebaseRun) needPrincipal() (string, error) {
	changes := false
	for _, l := range r.f.Layers {
		changes = changes || l.ID != ""
	}
	p, err := r.g.output(true, "config", "valley.principal")
	if !changes || err == nil && change.ValidPrincipal(p) {
		return p, nil
	}
	what := fmt.Sprintf("rebasing change %s adds the rebased head to its record", r.top().Name)
	if !r.single() {
		what = fmt.Sprintf("rebasing the stack under %s adds each change's rebased head to its record", r.top().Name)
	}
	return "", failure(what + " as the principal your key pushes as, and git config valley.principal names none, so nothing is rebased; set it with git config valley.principal <name>")
}

// prepare has the engine rebase every layer, the user's checkout
// untouched. An interrupt or a SIGTERM meanwhile cancels the rebase, and
// once the engine has removed its worktree the run ends with
// errInterrupted.
func (r *rebaseRun) prepare(eg rebase.Git) (*rebase.Prepared, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupted)
	var signalled atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-interrupted:
			signalled.Store(true)
			cancel()
		case <-done:
		}
	}()
	p, err := rebase.Prepare(ctx, eg, r.f)
	if signalled.Load() || errors.Is(err, context.Canceled) || errors.Is(err, rebase.ErrInterrupted) {
		if p != nil {
			p.Release(context.Background(), eg)
		}
		return nil, errInterrupted
	}
	if err != nil {
		return nil, r.refused(err)
	}
	return p, nil
}

// refused words a refusal the engine gives as valley rebase has always
// worded it: with the layer's place in the stack, and the command that
// acts on it. Several are one line each.
func (r *rebaseRun) refused(err error) error {
	var many rebase.Refusals
	if errors.As(err, &many) {
		var words []string
		for _, one := range many {
			words = append(words, r.words(one))
		}
		return failure(strings.Join(words, "\nvalley: "))
	}
	var one *rebase.Refusal
	if errors.As(err, &one) {
		return failure(r.words(one))
	}
	return err
}

func (r *rebaseRun) words(x *rebase.Refusal) string {
	l := x.Layer
	switch x.Kind {
	case rebase.NoBranch:
		return fmt.Sprintf("change %s v%d%s records no branch, so the rebase has no branch to push it to; nothing rebased",
			l.Name, l.Version, r.place(l))
	case rebase.BranchMissing:
		if r.single() {
			return "no such branch on origin: " + l.Branch
		}
		return fmt.Sprintf("change %s v%d%s is on the branch %s, and origin has no such branch; nothing rebased",
			l.Name, l.Version, r.place(l), l.Branch)
	case rebase.NotReady:
		return fmt.Sprintf("change %s v%d%s is %s, not ready; nothing rebased. Ready it first: %s",
			l.Name, l.Version, r.place(l), l.Record.State, readyHint(l.ID, l.Branch))
	case rebase.HeadMoved:
		if l.ID == "" {
			return fmt.Sprintf("origin/%s is at %s, and is no longer at %s, the head shown; nothing rebased", l.Branch, l.Head, x.Commit)
		}
		return fmt.Sprintf("change %s is at v%d, %s, and this run began at %s; nothing rebased. Review it again at its new head",
			l.Name, l.Version, l.Head, versionAt(l, x.Commit))
	case rebase.TipMoved:
		return fmt.Sprintf("change %s v%d%s records the head %s, and origin/%s is at %s; nothing rebased. Ready the branch's head as a new version of %s to act on it: %s",
			l.Name, l.Version, r.place(l), l.Head, l.Branch, l.Tip, l.Name, readyCommand(l.Tip, l.ID, l.Branch))
	case rebase.BranchOfChange:
		return fmt.Sprintf("change %s v%d records the head %s, and origin/%s%s is at %s; nothing rebased. Ready the branch's head as a new version of %s to act on it: %s",
			x.Handle, x.Version.N, headOf(x.Version), l.Branch, r.place(l), l.Tip, x.Handle, readyCommand(l.Tip, x.Change.ID, l.Branch))
	case rebase.SharedBranch:
		return fmt.Sprintf("%s and %s, layers %d and %d of %d under %s, are both on the branch %s; nothing rebased",
			x.Other.Name, l.Name, r.index(x.Other)+1, r.index(l)+1, len(r.f.Layers), r.top().Name, l.Branch)
	case rebase.Conflict:
		if r.single() {
			return fmt.Sprintf("%s conflicts with main — resolve by hand; nothing changed", l.Branch)
		}
		return fmt.Sprintf("%s, layer %d of %d under %s, conflicts with %s — resolve by hand; nothing changed",
			l.Name, r.index(l)+1, len(r.f.Layers), r.top().Name, x.Below)
	case rebase.RecordNotReady:
		v := x.Change.Record.ActiveVersion()
		return fmt.Sprintf("change %s v%d%s is %s, not ready; nothing pushed. Ready it first: %s",
			l.Name, v.N, r.place(l), x.Change.Record.State, readyHint(l.ID, v.Branch))
	case rebase.RecordNewHead:
		v := x.Change.Record.ActiveVersion()
		return fmt.Sprintf("change %s%s is at v%d, %s, and this run began at v%d, %s; nothing pushed. Review it again at its new head",
			l.Name, r.place(l), v.N, headOf(v), l.Version, l.Head)
	case rebase.RecordChanged:
		return fmt.Sprintf("the record of change %s%s changed while it was rebased; nothing pushed. Rebase it again: valley rebase %s",
			l.Name, r.place(l), r.top().Name)
	case rebase.BranchUnread:
		return fmt.Sprintf("could not read %s again against origin's records; nothing pushed", strings.TrimSuffix(r.plainName(l), ","))
	case rebase.BecameChange:
		n, h, name := x.Change, x.Handle, r.plainName(l)
		v := n.Record.ActiveVersion()
		switch {
		case n.Record.State != change.Ready:
			return fmt.Sprintf("%s is now change %s v%d, which is %s, not ready; nothing pushed. Ready it first: %s",
				name, h, v.N, n.Record.State, readyHint(n.ID, v.Branch))
		case headOf(v) != l.Head:
			return fmt.Sprintf("%s is now change %s v%d, which records the head %s, and this run began at %s; nothing pushed. Review it again by its handle",
				name, h, v.N, headOf(v), l.Head)
		}
		return fmt.Sprintf("%s is now change %s v%d; nothing pushed. Rebase it again by its handle: valley rebase %s", name, h, v.N, h)
	}
	return x.Error()
}

// plainName is how a refusal names a plain branch of the stack: as it was
// given, for a branch alone, and otherwise as origin's, with its place.
func (r *rebaseRun) plainName(l *rebase.Layer) string {
	if r.single() {
		return r.given
	}
	return "origin/" + l.Branch + r.place(l)
}

// versionAt names a head of a change by its version too, when its record
// has one there.
func versionAt(l *rebase.Layer, head string) string {
	for _, v := range l.Record.Versions {
		if headOf(v) == head {
			return fmt.Sprintf("v%d, %s", v.N, head)
		}
	}
	return head
}

// publish says what became of the branch push, adds each change's version
// when the branches moved, and says what became of each layer.
func (r *rebaseRun) publish(eg rebase.Git, pub *rebase.Publication, principal string) error {
	top := r.top()
	switch pub.Push {
	case rebase.Refused:
		if r.single() {
			return failure(fmt.Sprintf("lease on origin/%s failed (it moved since the fetch); nothing pushed", top.Branch))
		}
		r.writeManifest(pub)
		var why []string
		for _, ref := range pub.Refused {
			why = append(why, "origin/"+strings.TrimPrefix(ref.Ref, "refs/heads/")+": "+ref.Why)
		}
		return failure(fmt.Sprintf("origin refused the push of the stack's branches, so none moved (%s); nothing pushed. Rebase the stack again: valley rebase %s",
			strings.Join(why, "; "), top.Name))
	case rebase.Unknown:
		r.writeManifest(pub)
		var refs []string
		for _, l := range r.f.Layers {
			refs = append(refs, shellQuote("refs/heads/"+l.Branch))
		}
		return failure("the push of the branches failed, and whether origin took it is not known. Check with git ls-remote origin " +
			strings.Join(refs, " ") + ": a branch at its new head is pushed, and the command above finishes its change")
	case rebase.Unpushed:
		return failure("could not run the push of the branches; nothing pushed")
	}
	if r.single() {
		fmt.Fprintf(r.stdout, "rebased %s onto origin/%s, pushed with --force-with-lease\n", top.Branch, recordTarget)
		fmt.Fprintf(r.stdout, "  head     %s\n", pub.Result(top).Head)
	}
	r.addVersions(eg, pub, principal)
	if !r.single() {
		r.writeManifest(pub)
	}
	r.otherStacks()
	n := len(unfinished(r.rows(pub)))
	switch {
	case n > 0 && r.single():
		return errReported
	case n == 1:
		return failure(fmt.Sprintf("1 change of the stack under %s is unfinished; finish it with the command above", top.Name))
	case n > 0:
		return failure(fmt.Sprintf("%d changes of the stack under %s are unfinished; finish each with the command above", n, top.Name))
	}
	return nil
}

// addVersions is phase 2: bottom first, each change's rebased head is
// added to its record as a new version, while the change is still ready
// at the head the rebase began with. A change alone says what ready says,
// as it always has.
func (r *rebaseRun) addVersions(eg rebase.Git, pub *rebase.Publication, principal string) {
	for _, res := range pub.Results {
		l := res.Layer
		if l.ID == "" {
			continue
		}
		pub.Append(context.Background(), eg, principal, res)
		if ap := res.Appended; ap.Err != nil {
			fmt.Fprintf(r.stderr, "valley: %s\n", ap.Err)
			if r.single() {
				fmt.Fprintf(r.stderr, "valley: origin/%s is rebased to %s, and no version of %s records it. Ready it once the change is ready: %s\n",
					l.Branch, res.Head, l.Name, readyCommand(res.Head, l.ID, l.Branch))
			}
		} else if r.single() {
			fmt.Fprintf(r.stdout, "%s %s v%d ready\n", project(r.g), ap.Handle, ap.Version)
		}
	}
}

// rows is the manifest's line for each layer, bottom first.
func (r *rebaseRun) rows(pub *rebase.Publication) []*layer {
	var rows []*layer
	for _, res := range pub.Results {
		l := res.Layer
		row := &layer{name: l.Name, id: l.ID, branch: l.Branch, head: l.Head, rebased: res.Head, pushed: pushNot}
		switch res.Branch {
		case rebase.Pushed:
			row.pushed = pushDone
		case rebase.Unknown:
			row.pushed = pushUnknown
		}
		if res.Appended != nil {
			row.added = res.Appended.Version
		}
		rows = append(rows, row)
	}
	return rows
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

func (r *rebaseRun) writeManifest(pub *rebase.Publication) {
	io.WriteString(r.stdout, manifest(r.top().Name, r.rows(pub)))
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
	for _, l := range r.f.Layers {
		ids[l.ID], branches[l.Branch] = true, true
	}
	on := func(head string) *rebase.Layer {
		for k := len(r.f.Layers) - 1; k >= 0; k-- {
			if l := r.f.Layers[k]; head == l.Head || r.s.reach(head)[l.Head] {
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
			fmt.Fprintf(r.stdout, "%s is now on an old head of %s; rebase it with valley rebase %s\n", h, l.Name, h)
		}
	}
	for _, b := range r.s.unrecorded() {
		if l := on(b.Head); l != nil && !branches[b.Branch] {
			fmt.Fprintf(r.stdout, "%s is now on an old head of %s; rebase it with valley rebase %s\n", branchName(b.Branch), l.Name, shellQuote(b.Branch))
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
