package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"the-valley/change"
)

// A change's reviews are kept in one history, at
// refs/the-valley/reviews/<target>/<change ID>. The ref points at the
// newest review commit. Each review commit holds one review, as
// review.json, the one entry of its tree. Its parents say where it stands:
//
//	first review                          P = [record]
//	a later review that judges a version  P = [previous, record]
//	a withdrawal                          P = [previous]
//
// previous is the review commit before it, the ref's value when the review
// was appended, and record is the change record commit its binding names.
// So the first-parent chain is every review in order, and the ref keeps
// every record a review judged, with the versions that record holds, even
// once the change's own record ref is deleted.
//
// A history is append-only. Each review in it has its own review ID, so a
// client that publishes a review again, after a push it could not confirm,
// can tell that it is there already. A withdrawal names a review earlier in
// the same history that is not itself a withdrawal and that nothing has
// withdrawn yet, under the same review name. A withdrawal is final: no
// review withdraws one.
//
// An append is a compare-and-swap. The pusher moves the ref from the
// history's head as they read it to a commit whose first parent is that
// head, and the move fails if the ref has moved since. They then read the
// new head and append again; their review keeps its ID, so the retry needs
// no new judgment. CheckAppend is what the push hook holds an append to.

// RefPrefix opens every review history's ref.
const RefPrefix = "refs/the-valley/reviews/"

// File is the one file of a review commit's tree.
const File = "review.json"

// The bounds on the objects that hold reviews, and on a history. A review
// commit names a tree, at most two parents, an author, a committer and a
// short message, and its tree names one file. A history holds at most
// MaxHistory reviews, so reading one whole is bounded too.
const (
	MaxCommit  = 4 << 10 // bytes in a review commit
	MaxTree    = 1 << 10 // bytes in its tree
	MaxHistory = 512     // reviews in one history
)

// reviewLayout is a review commit's layout.
var reviewLayout = oneFile{subject: "the review commit", name: File, commit: MaxCommit, tree: MaxTree, blob: MaxRecord}

// Ref is the ref a change's review history lives at.
func Ref(target, id string) string {
	return RefPrefix + target + "/" + id
}

// ParseRef splits a review history's ref into its target and change ID.
// It reports false for a ref that is not refs/the-valley/reviews/<target>/<ID>
// with a branch name as the target and a change ID as the ID.
func ParseRef(ref string) (target, id string, ok bool) {
	rest, found := strings.CutPrefix(ref, RefPrefix)
	if !found {
		return "", "", false
	}
	slash := strings.LastIndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	target, id = rest[:slash], rest[slash+1:]
	if !change.ValidBranch(target) || !change.ValidID(id) {
		return "", "", false
	}
	return target, id, true
}

// Entry is one review of a history, with the commit that holds it and
// that commit's parents.
type Entry struct {
	Commit  string
	Parents []string
	Review  *Record
}

// History is a change's reviews, oldest first.
type History struct {
	Target, ChangeID string
	Entries          []Entry
}

// Head is the newest review commit, or "" for an empty history.
func (h *History) Head() string {
	if len(h.Entries) == 0 {
		return ""
	}
	return h.Entries[len(h.Entries)-1].Commit
}

// Find is the review with the ID given, if the history holds one.
func (h *History) Find(id string) (Entry, bool) {
	for _, e := range h.Entries {
		if e.Review.ReviewID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Withdrawal is the withdrawal of the review with the ID given, if the
// history holds one.
func (h *History) Withdrawal(id string) (Entry, bool) {
	for _, e := range h.Entries {
		if w := e.Review.Withdraws; w != nil && *w == id {
			return e, true
		}
	}
	return Entry{}, false
}

// ReadHistory reads the history whose ref is ref and whose head is the
// commit head, and holds it to the history's rules: every commit is a
// review commit of the ref's change and target with its parents as the
// table above gives them, every review ID is its own, and every
// withdrawal names a review it may withdraw. It reads at most MaxHistory
// reviews, each within its bounds, by the deadline.
//
// It does not verify the bindings, which the push hook verified as each
// review was appended. A reader that relies on one runs Verify.
func ReadHistory(r Repo, ref, head string, deadline time.Time) (*History, error) {
	target, id, ok := ParseRef(ref)
	if !ok {
		return nil, fmt.Errorf("%s is not a review history's ref, %s<target>/<change ID>", ref, RefPrefix)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	o, err := openObjects(ctx, r, historyBytes)
	if err != nil {
		return nil, err
	}
	defer o.close()
	return o.history(target, id, head)
}

// historyBytes is the most a whole history can take to read.
const historyBytes = MaxHistory * (MaxCommit + MaxTree + MaxRecord)

// history reads the history of the change id on target whose head is
// head, newest first, and checks it oldest first.
func (o *objects) history(target, id, head string) (*History, error) {
	h := &History{Target: target, ChangeID: id}
	for commit := head; commit != ""; {
		if len(h.Entries) == MaxHistory {
			return nil, fmt.Errorf("the history holds more than the %d reviews a history may", MaxHistory)
		}
		e, err := o.entry(target, id, commit)
		if err != nil {
			return nil, err
		}
		h.Entries = append(h.Entries, e)
		commit = previous(e)
	}
	for i, j := 0, len(h.Entries)-1; i < j; i, j = i+1, j-1 {
		h.Entries[i], h.Entries[j] = h.Entries[j], h.Entries[i]
	}
	checked := &History{Target: target, ChangeID: id}
	for _, e := range h.Entries {
		if err := checked.follows(e.Review); err != nil {
			return nil, fmt.Errorf("the review commit %s: %w", e.Commit, err)
		}
		checked.Entries = append(checked.Entries, e)
	}
	return h, nil
}

// entry reads one review commit of the change id on target, and holds its
// parents to the table above.
func (o *objects) entry(target, id, commit string) (Entry, error) {
	parents, file, err := o.readOneFile(commit, reviewLayout)
	if err != nil {
		return Entry{}, err
	}
	rec, err := Parse(file)
	if err != nil {
		return Entry{}, fmt.Errorf("the review commit %s holds a %s that does not read: %w", commit, File, err)
	}
	switch {
	case rec.ChangeID != id:
		return Entry{}, fmt.Errorf("the review commit %s holds a review of the change %s, and its history is %s's", commit, rec.ChangeID, id)
	case rec.Target != target:
		return Entry{}, fmt.Errorf("the review commit %s holds a review on the target %s, and its history is on %s", commit, rec.Target, target)
	}
	e := Entry{Commit: commit, Parents: parents, Review: rec}
	if err := checkParents(e); err != nil {
		return Entry{}, fmt.Errorf("the review commit %s %w", commit, err)
	}
	return e, nil
}

// checkParents holds a review commit's parents to the table above.
func checkParents(e Entry) error {
	p := e.Parents
	if !e.Review.Judges() {
		if len(p) != 1 {
			return fmt.Errorf("holds a withdrawal, and has %d parents where a withdrawal has one: the review before it", len(p))
		}
		return nil
	}
	record := objectOf(e.Review.Binding.Record)
	switch {
	case len(p) != 1 && len(p) != 2:
		return fmt.Errorf("has %d parents, where a review that judges a version has the review before it, if any, and then the record it binds", len(p))
	case p[len(p)-1] != record:
		return fmt.Errorf("has %s as its last parent, and its review binds the record %s, which the commit keeps as its last parent", p[len(p)-1], record)
	}
	return nil
}

// previous is the review commit before e, or "" for the first.
func previous(e Entry) string {
	if e.Review.Judges() && len(e.Parents) == 1 {
		return ""
	}
	return e.Parents[0]
}

// follows refuses a review that may not follow the history h: one whose
// ID the history holds already, and a withdrawal that names no review of
// h, names a withdrawal, names a review already withdrawn, or names a
// review under another review name.
func (h *History) follows(rec *Record) error {
	if e, ok := h.Find(rec.ReviewID); ok {
		return fmt.Errorf("its review_id %s is the ID of the review in %s, and each review has its own", rec.ReviewID, e.Commit)
	}
	if rec.Judges() {
		return nil
	}
	w := *rec.Withdraws
	target, ok := h.Find(w)
	if !ok {
		return fmt.Errorf("it withdraws %s, and the history before it holds no review with that ID", w)
	}
	if earlier, ok := h.Withdrawal(w); ok {
		return fmt.Errorf("it withdraws %s, which the review %s withdrew already", w, earlier.Review.ReviewID)
	}
	switch {
	case !target.Review.Judges():
		return fmt.Errorf("it withdraws %s, which is a withdrawal, and a withdrawal is final", w)
	case target.Review.Review != rec.Review:
		return fmt.Errorf("it withdraws %s under the review name %s, and %s is a review named %s", w, rec.Review, w, target.Review.Review)
	}
	return nil
}

// Update is one push's update of a review history's ref: the ref, and the
// commits it moves from and to. "" or git's all-zero ID is no commit.
type Update struct {
	Ref, Old, New string
}

// Pusher is who pushes an update, and what they may write, as the push
// hook knows them from its grants.
type Pusher struct {
	// Principal is the authenticated principal pushing, or "" for a key
	// that names none.
	Principal string
	// MayReview reports whether the principal may write reviews that
	// judge a version under a review's name. Nil allows no name. A
	// withdrawal needs no grant of its name.
	MayReview func(name string) bool
	// Operator reports whether the principal may withdraw any review,
	// whoever wrote it and under whatever name. A reviewer may always
	// withdraw their own, even once MayReview no longer allows its name.
	Operator bool
}

// CheckAppend is the push hook's decision on an update of a review
// history's ref. It returns nil when the update appends one review, and
// otherwise an error saying why it does not. The update appends one review
// when:
//
//   - the ref is a review history's ref, and the update moves it to a
//     commit rather than deleting it;
//   - the new commit is a review commit of the ref's change and target
//     whose first parent is the old commit, by the table above, and the
//     history at the old commit reads;
//   - the review names the pusher as its reviewer, and may follow the
//     history: its ID is new to it, and a withdrawal names a review of it,
//     under the same review name, that may be withdrawn;
//   - a review that judges a version is under a review name the pusher
//     may write, and a withdrawal names a review the pusher wrote, or any
//     review when the pusher is an operator. A withdrawal needs no grant
//     of its name, so a reviewer whose grant is revoked can still take
//     back their own judgment;
//   - a review that judges a version is about a change the repository
//     records, Against.RecordTip, and its binding verifies against a
//     (Verify).
//
// It decides who may write a review only through Pusher, which the hook
// fills in from its grants. Everything that reads no tree is decided
// before the binding is verified.
func CheckAppend(r Repo, u Update, p Pusher, a Against) error {
	target, id, ok := ParseRef(u.Ref)
	switch {
	case !ok:
		return fmt.Errorf("a review history's ref is %s<target>/<change ID>, with the change ID as 32 letters from k to z", RefPrefix)
	case none(u.New):
		return errors.New("a review history keeps every review of the change, and no push deletes one")
	case p.Principal == "":
		return errors.New("a review takes writes only from a named principal, and a key that names no principal is none")
	case a.Budget.Deadline.IsZero():
		return errors.New("an append is checked within a deadline, and the budget sets none")
	}
	ctx, cancel := context.WithDeadline(context.Background(), a.Budget.Deadline)
	defer cancel()
	o, err := openObjects(ctx, r, historyBytes)
	if err != nil {
		return err
	}
	defer o.close()
	h := &History{Target: target, ChangeID: id}
	if !none(u.Old) {
		if h, err = o.history(target, id, u.Old); err != nil {
			return fmt.Errorf("the history it appends to does not read: %w", err)
		}
	}
	if len(h.Entries) >= MaxHistory {
		return fmt.Errorf("the history holds %d reviews, the most a history may", MaxHistory)
	}
	e, err := o.entry(target, id, u.New)
	if err != nil {
		return err
	}
	rec := e.Review
	if rec.Reviewer != p.Principal {
		return fmt.Errorf("its review names %s as the reviewer, and %s is pushing it", rec.Reviewer, p.Principal)
	}
	if err := h.follows(rec); err != nil {
		return err
	}
	// A judgment needs the grant of its name. A withdrawal does not: its
	// reviewer may take it back after losing the grant, and an operator
	// may take back any.
	if rec.Judges() {
		if p.MayReview == nil || !p.MayReview(rec.Review) {
			return fmt.Errorf("it is a review named %s, and %s may not write reviews under that name", rec.Review, p.Principal)
		}
	} else {
		withdrawn, _ := h.Find(*rec.Withdraws)
		if withdrawn.Review.Reviewer != p.Principal && !p.Operator {
			return fmt.Errorf("it withdraws a review %s wrote, and only its reviewer or an operator may withdraw it", withdrawn.Review.Reviewer)
		}
	}
	switch prev := previous(e); {
	case prev == h.Head():
	case h.Head() == "":
		return fmt.Errorf("it starts the history, and the first review commit has the record it binds as its one parent, where this one has %d parents", len(e.Parents))
	case prev == "":
		return fmt.Errorf("it has the record it binds as its one parent, as the first review commit does, and an append has the history's head, %s, as its first parent", h.Head())
	default:
		return fmt.Errorf("its first parent is %s, and an append's is the history's head, %s", prev, h.Head())
	}
	if !rec.Judges() {
		return nil
	}
	if none(a.RecordTip) {
		return fmt.Errorf("the repository holds no record of the change %s, and a review judges a version of a recorded change", id)
	}
	if err := Verify(r, rec, a); err != nil {
		return fmt.Errorf("its binding does not verify: %w", err)
	}
	return nil
}

// ErrRecorded is returned by Build when the history holds the review
// already, as when an earlier publication of it went through unconfirmed.
var ErrRecorded = errors.New("the history holds this review already")

// Build writes the review commit that appends rec to the history of
// rec's change whose head is old, "" for none, and returns it. It moves no
// ref. It reads the history at old and refuses a review that may not
// follow it, as CheckAppend does. When the history holds a review with
// rec's ID that encodes as rec does, it returns that review's commit and
// ErrRecorded.
// It does not verify the binding: Bind makes one that verifies, and
// CheckAppend verifies it as the hook will.
//
// The commit's author and committer are git's, as the repository's
// environment and configuration name them.
func Build(r Repo, old string, rec *Record, deadline time.Time) (string, error) {
	data, err := Encode(rec)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	o, err := openObjects(ctx, r, historyBytes)
	if err != nil {
		return "", err
	}
	defer o.close()
	h := &History{Target: rec.Target, ChangeID: rec.ChangeID}
	if !none(old) {
		if h, err = o.history(rec.Target, rec.ChangeID, old); err != nil {
			return "", fmt.Errorf("the history it appends to does not read: %w", err)
		}
	}
	if e, ok := h.Find(rec.ReviewID); ok {
		if recorded, err := Encode(e.Review); err == nil && bytes.Equal(recorded, data) {
			return e.Commit, ErrRecorded
		}
	}
	if len(h.Entries) >= MaxHistory {
		return "", fmt.Errorf("the history holds %d reviews, the most a history may", MaxHistory)
	}
	if err := h.follows(rec); err != nil {
		return "", err
	}
	var parents []string
	if !none(old) {
		parents = append(parents, old)
	}
	if rec.Judges() {
		parents = append(parents, objectOf(rec.Binding.Record))
	}
	blob, err := git(ctx, r, data, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	tree, err := git(ctx, r, []byte("100644 blob "+strings.TrimSpace(string(blob))+"\t"+File+"\n"), "mktree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", strings.TrimSpace(string(tree)), "-m", rec.Verdict + " " + rec.Review + " " + rec.ReviewID}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	commit, err := git(ctx, r, nil, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(commit)), nil
}

// ErrStale is returned by Publish when the remote's history is no longer
// at the head the append was built on: another review was appended
// first. Read the new head, build on it, and publish again.
var ErrStale = errors.New("the remote's review history has moved since it was read")

// Publish moves the history ref on the remote from old to new, "" for
// none, and only if the ref is still at old: a compare-and-swap, as git's
// --force-with-lease makes it.
func Publish(r Repo, remote, ref, old, new string, deadline time.Time) error {
	if none(old) {
		old = ""
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	out, err := git(ctx, r, nil, "push", "--porcelain", "--force-with-lease="+ref+":"+old, remote, new+":"+ref)
	if err == nil {
		return nil
	}
	if strings.Contains(string(out), "(stale info)") || strings.Contains(string(out), "(fetch first)") {
		return ErrStale
	}
	return err
}
