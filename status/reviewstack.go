package main

// A stack, a change with layers under it that have not landed, is
// reviewed one layer at a time, bottom first, each against the layer below
// it, the way it was built. Approving the top lands every layer under it,
// so reading the top against main alone would ask the operator to judge
// the whole stack as one diff.
//
// The layers are stack.go's, bottom first, the change itself last. A
// layer's notes are kept under its branch: a branch's own name, or the
// branch a change's active version names.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
)

// reviewLayer is one layer of the stack under review.
type reviewLayer struct {
	stackLayer
	id, version, branch, owner string
	notes                      []string // taken on the layer, for the print after the verdict
}

// readStack reads the stack under the change review resolved. Each change
// under the top is read again with resolve, for its ID, version, branch
// and owner, and must still be at the head the stack names. The top must
// be what review resolved. It reports false, having said why, when either
// does not hold or the stack cannot be read.
func (r *review) readStack() bool {
	r.layers = nil
	name := r.c.id
	if name == "" {
		name = "origin/" + r.c.branch
	}
	read, err := stackLayers(r.g, name)
	if err != nil {
		fmt.Fprintf(r.stderr, "valley: %s\n", err)
		return false
	}
	var layers []reviewLayer
	for _, l := range read {
		layers = append(layers, reviewLayer{stackLayer: l, branch: l.name})
	}
	last := len(layers) - 1
	kind := "branch"
	if r.c.id != "" {
		kind = "change"
	}
	if last < 0 || layers[last].head != r.changeHead(r.c.branch) || layers[last].kind != kind {
		return r.refuse(r.topName + " moved while its stack was read")
	}
	top := &layers[last]
	top.id, top.version, top.branch, top.owner = r.c.id, r.c.version, r.c.branch, r.c.owner
	for k := range layers[:last] {
		l := &layers[k]
		if l.kind != "change" {
			continue
		}
		res, err := resolveRecorded(r.g, l.name)
		if err != nil {
			fmt.Fprintf(r.stderr, "valley: %s\n", err)
			return false
		}
		if res.id == "" || res.head != l.head {
			return r.refuse("change " + l.name + ", under " + r.topName + ", moved while the stack was read")
		}
		l.id, l.version, l.branch, l.state, l.owner = res.id, strconv.Itoa(res.version), res.branch, res.state, res.owner
	}
	r.layers = layers
	return true
}

// useLayer acts on layer k as review acts on a change it was given,
// binding [a]pprove to the head the stack shows. A branch under the top is
// read again as origin/<branch>, which no handle can shadow.
func (r *review) useLayer(k int) {
	l := r.layers[k]
	r.c = shownChange{name: "origin/" + l.name, id: l.id, version: l.version, branch: l.branch, state: l.state, owner: l.owner}
	if k == len(r.layers)-1 {
		r.c.name = r.topName
	}
	if l.id != "" {
		r.c.name, r.c.handle, r.c.head = l.name, l.name, l.head
	}
	r.bind()
	if l.id == "" {
		r.approveHead = l.head
	}
}

// notesKey is the branch layer k's notes are kept under. A change whose
// active version names no branch keeps them under its handle.
func (r *review) notesKey(k int) string {
	if b := r.layers[k].branch; b != "" {
		return b
	}
	return r.layers[k].name
}

// layerSkipped reports a layer below the top that is approved, which asks
// nothing of the reviewer: the valley carries it to landing once its
// checks pass. The review does not page it. The top is the change review
// was named, so it is always paged.
func (r *review) layerSkipped(k int) bool {
	return k < len(r.layers)-1 && r.layers[k].approved == "approved"
}

// layerLabel is how a layer is named in the list: its name, then branch,
// or the change's state.
func (r *review) layerLabel(k int) string {
	l := r.layers[k]
	switch {
	case l.kind == "branch":
		return l.name + " (branch)"
	case r.layerSkipped(k):
		return l.name + " (approved, not paged)"
	}
	return l.name + " (" + l.state + ")"
}

// aState is a state that is not ready, as a sentence names it.
func aState(state string) string {
	if state == "draft" {
		return "a draft"
	}
	return "withdrawn"
}

// summarizeStack says what is coming: the layers bottom first, the lower
// layers the integrator would refuse, and what each answer lands.
func (r *review) summarizeStack() {
	n := len(r.layers)
	top := r.layers[n-1].name
	fmt.Fprintf(r.stdout, "%s is a stack of %d changes; reviewing from the bottom, each against the one below it:\n", top, n)
	for k, l := range r.layers {
		fmt.Fprintf(r.stdout, "  %d. %s: %s\n", k+1, r.layerLabel(k), l.title)
	}
	for k, l := range r.layers[:n-1] {
		if l.state == "draft" || l.state == "withdrawn" {
			fmt.Fprintf(r.stdout, "warning: %s, change %d of %d, is %s. The integrator refuses to land it or anything above it (refused-not-ready).\n",
				l.name, k+1, n, aState(l.state))
		}
	}
	fmt.Fprintf(r.stdout, "read each layer and go on with [n]; approving %s at the top lands all %d. [a] on a lower layer lands it and the layers under it, and ends the review.\n", top, n)
}

// baseStack offers a stack whose bottom does not fast-forward from main,
// before any layer is paged, [b]ase, which rebases every layer as the
// rebase verb does (rebase.go), [v]iew diff, which pages the top's whole
// diff, or [s]kip. After a rebase the stack is read again and listed
// again, so the heads paged are the heads approved. A rebase refused or
// left unfinished has said what happened, and only [v]iew diff and [s]kip
// are offered then. It reports false when the review stops here.
func (r *review) baseStack() bool {
	clause, stopped := "behind main", "; nothing done"
	for !r.fastForwards(r.layers[0].head) {
		n := len(r.layers)
		keys, answers := []string{"[b]ase onto main", "[v]iew diff", "[s]kip"}, "b, v, or s"
		if clause != "behind main" {
			keys, answers = []string{"[v]iew diff", "[s]kip"}, "v or s"
		}
		r.prompt([]string{r.layers[n-1].name, fmt.Sprintf("stack of %d", n), clause}, keys...)
		switch r.mustAnswer() {
		case "b":
			switch {
			case clause != "behind main":
				fmt.Fprintln(r.stdout, "answer "+answers)
			case r.rebaseShown(r.layers[n-1].head):
				_ = fetchOrigin(r.g, r.stdout, r.stderr)
				if r.c.id != "" {
					handle := r.c.handle
					if err := r.resolve(r.c.id); err != nil {
						fmt.Fprintf(r.stderr, "valley: %s\n", err)
						r.die("could not read change " + handle + " again after the rebase")
					}
				}
				r.bind()
				if !r.readStack() {
					r.die("could not read the stack under " + r.topName + " again after the rebase")
				}
				r.summarizeStack()
			default:
				clause, stopped, r.walkRefused = "not rebased", "", true
			}
		case "v":
			r.viewFull(r.c.branch)
		case "s":
			fmt.Fprintf(r.stdout, "stopped reviewing %s before its first layer%s\n", r.layers[n-1].name, stopped)
			r.skipped()
			return false
		default:
			fmt.Fprintln(r.stdout, "answer "+answers)
		}
	}
	return true
}

// rebaseShown is [b]ase onto main for the stack under review, while its
// top is still at head, the head review showed of it. It is the rebase
// verb (rebase.go): it rebases the change's whole stack in a throwaway
// worktree of its own, never the user's checkout, pushes every layer's
// branch in one atomic push, leased on the tips it read, and adds each
// change's rebased head to its record as a new version. The judgment on
// the rebased diff stays with the operator, back at the prompt.
func (r *review) rebaseShown(head string) bool {
	name := r.c.id
	if name == "" {
		name = r.c.name
	}
	status := 0
	r.inForeground(func() {
		status = rebaseCancelledBy(r.g, []string{"--head", head, name}, r.stdout, r.stderr, os.Interrupt)
	})
	if status != 0 {
		return false
	}
	// What the checks read was for the heads before the rebase.
	r.restartChecksRead()
	return true
}

// layerHeading is the line that heads layer k wherever it is shown.
func (r *review) layerHeading(k int) string {
	below := "main"
	if k > 0 {
		below = r.layers[k-1].name
	}
	return fmt.Sprintf("change %d of %d: %s on %s", k+1, len(r.layers), r.layers[k].name, below)
}

// layerHeld is why layer k can't be approved, when it or a layer under it
// is not ready, or "". A layer that is not ready says so as a change alone
// does (notReady). A layer above one says which: approving it would land
// that layer too, and the integrator lands nothing that would bring a
// version of a change that is not ready.
func (r *review) layerHeld(k int) string {
	l := r.layers[k]
	if held := notReady(l.name, l.version, l.state, l.owner); held != "" {
		return held
	}
	for j := k - 1; j >= 0; j-- {
		if s := r.layers[j].state; s == "draft" || s == "withdrawn" {
			return fmt.Sprintf("it would bring %s, change %d of %d, which is %s, so it can't be approved yet.",
				r.layers[j].name, j+1, len(r.layers), aState(s))
		}
	}
	return ""
}

// pageLayer shows layer k, as showReview shows a change alone. The layer
// is compared with its own change's approved version, so a lower layer is
// set against its own approval, not the top's. Its whole diff is against
// the layer below it. The notes taken in it are kept under its own branch.
// When it can't be approved, layerHeld's line follows its heading, before
// the layer's knots.
func (r *review) pageLayer(k int) {
	r.notes = nil
	l := r.layers[k]
	heading := r.layerHeading(k)
	if held := r.layerHeld(k); held != "" {
		heading += "\n" + held
	}
	r.newScratch()
	kind, compared := r.compareShown(l.id, l.version, l.head)
	r.showCompared(r.notesKey(k), l.head, heading, r.doesOf(l.id), kind, compared, func(w io.Writer) { r.layerDiff(w, k) })
	if line := r.checksLine(l.id, true); line != "" {
		fmt.Fprintln(r.stdout, line)
	}
	r.keepLayerNotes(k)
}

// viewLayer is [v]iew full diff for layer k: its whole diff against the
// layer below it.
func (r *review) viewLayer(k int) {
	r.notes = nil
	dir := r.newScratch()
	var b bytes.Buffer
	b.WriteString(r.layerHeading(k) + "\n")
	r.layerDiff(&b, k)
	r.writePage(dir, b.Bytes())
	r.pageBuffer(r.notesKey(k), r.short(r.layers[k].head))
	r.keepLayerNotes(k)
}

// keepLayerNotes adds the notes just taken to layer k's.
func (r *review) keepLayerNotes(k int) {
	r.layers[k].notes = append(r.layers[k].notes, r.notes...)
}

// layerDiff writes layer k's whole diff: its log and its diff against the
// layer below it.
func (r *review) layerDiff(w io.Writer, k int) {
	l := r.layers[k]
	below := "main"
	if k > 0 {
		below = r.layers[k-1].name
	}
	sha := r.short(l.head)
	on := below + " at " + r.short(l.base)
	switch {
	case l.kind == "branch":
		fmt.Fprintf(w, "branch %s, log against %s:\n\n", l.name, on)
	case l.branch != "":
		fmt.Fprintf(w, "change %s v%s at %s, on branch %s, log against %s:\n\n", l.name, l.version, sha, l.branch, on)
	default:
		fmt.Fprintf(w, "change %s v%s at %s, log against %s:\n\n", l.name, l.version, sha, on)
	}
	r.commitLog(w, l.base+".."+l.head)
	r.gitTo(w, "--no-pager", "diff", l.base+"..."+l.head)
}

// reviewStack shows each layer under the top and asks after each, naming
// the layer and its place: [n]ext layer goes on to the layer above,
// [a]pprove approves that layer at the head shown, which lands it and
// every layer under it, as the prompt names them, [v]iew full diff pages
// the layer's whole diff, and [s]top ends the review. [r]eject is not
// offered under the top: deleting a lower layer's branch would leave the
// layers above still holding it. [a]pprove is not offered at or above a
// layer that is not ready, and the prompt says why in its place
// (layerHeld). After the top, its verdict is taken as for any change, with
// a prompt that names the top and says approving it lands all n.
func (r *review) reviewStack() {
	r.summarizeStack()
	if !r.baseStack() {
		return
	}
	fmt.Fprintln(r.stdout)
	n, top := len(r.layers), len(r.layers)-1
	for k := 0; k < top; k++ {
		if r.layerSkipped(k) {
			continue
		}
		r.pageLayer(k)
		l := r.layers[k]
		var lands string
		switch k {
		case 0:
			lands = r.layers[0].name + " alone"
		case 1:
			lands = r.layers[0].name + " and " + r.layers[1].name
		default:
			lands = fmt.Sprintf("the %d up to %s", k+1, l.name)
		}
		held := r.layerHeld(k)
	answering:
		for {
			failed, answers := "", "n, a, v, or s"
			if r.checksFailedAt == l.head {
				failed = r.short(l.head)
			}
			switch {
			case held != "":
				fmt.Fprintf(r.stdout, "%s, %d of %d: %s [n]ext layer, [v]iew full diff, [s]top? ", l.name, k+1, n, held)
				answers = "n, v, or s"
			case failed != "":
				fmt.Fprintf(r.stdout, "%s, %d of %d: its checks failed at %s, so approving needs a new head: [n]ext layer, [v]iew full diff, [s]top? ", l.name, k+1, n, failed)
				answers = "n, v, or s"
			default:
				fmt.Fprintf(r.stdout, "%s, %d of %d: [n]ext layer, [a]pprove %s, [v]iew full diff, [s]top? ", l.name, k+1, n, lands)
			}
			switch r.mustAnswer() {
			case "n":
				break answering
			case "a":
				if failed != "" || held != "" {
					fmt.Fprintln(r.stdout, "answer "+answers)
					continue
				}
				r.useLayer(k)
				if r.moved(true) || r.approve(r.c.branch) {
					return
				}
				r.useLayer(top)
			case "v":
				r.viewLayer(k)
			case "s":
				fmt.Fprintf(r.stdout, "stopped reviewing %s at change %d of %d, %s; nothing done\n", r.layers[top].name, k+1, n, l.name)
				r.skipped()
				return
			default:
				fmt.Fprintln(r.stdout, "answer "+answers)
			}
		}
	}
	r.pageLayer(top)
	r.useLayer(top)
	r.decide(r.layers[top].branch, r.layers[top].name, n)
}

// printStackNotes prints the notes taken on every layer, each under its
// own branch.
func (r *review) printStackNotes() {
	for k, l := range r.layers {
		r.printNotes(r.notesKey(k), l.notes)
	}
}
