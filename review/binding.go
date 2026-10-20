package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"the-valley/change"
	"the-valley/delta"
)

// A review's binding says exactly what its reviewer read, so anyone
// holding the objects can recompute it.
//
// The reviewer reads version n of a change: the head that the change
// record commit names as version n. They compare it with the target as
// it stood at a tip they name. What they read is the delta from the merge
// base of that tip and the head to the head, as valley-delta-v1 gives it:
// the files the version changes, whole. The binding records the record
// commit, n, the head, the tip, the base and the delta's digest.
//
// The base is the one merge base of the tip and the head. When they have
// none, or more than one, there is no single base to compare from, and so
// no binding and no review: Bind refuses with ErrNoBase or
// ErrAmbiguousBase, and Verify refuses a binding that names a base there.
// The reviewer can review again against another tip, or once the author
// rebases.

// ErrNoBase is returned when the head and the target tip share no history.
var ErrNoBase = errors.New("the head and the target tip share no history, so there is no base to compare from")

// ErrAmbiguousBase is returned when the head and the target tip have more
// than one merge base.
var ErrAmbiguousBase = errors.New("the head and the target tip have more than one merge base, so the base to compare from is ambiguous")

// recordLayout is a change record commit's layout, held to the bounds
// every reader of a record holds it to (../change).
var recordLayout = oneFile{
	subject: "the change record", name: "record.json",
	commit: change.MaxRecordCommit, tree: change.MaxRecordTree, blob: change.MaxRecordBlob,
}

// Bind makes the binding of a review of version n of a change, as the
// change record commit record names it, judged against the target tip
// tip. record and tip are full object IDs. It returns the change record
// too, which names the change and the target the review is about.
//
// Reading the record and the delta is held to the budget: each to its
// bytes and entries, and both to its deadline.
func Bind(r Repo, record string, n int, tip string, b delta.Budget) (*change.Record, Binding, error) {
	if b.Deadline.IsZero() {
		return nil, Binding{}, errors.New("a binding is made within a deadline, and the budget sets none")
	}
	ctx, cancel := context.WithDeadline(context.Background(), b.Deadline)
	defer cancel()
	o, err := openObjects(ctx, r, b.Bytes)
	if err != nil {
		return nil, Binding{}, err
	}
	defer o.close()
	rec, v, err := o.version(record, n)
	if err != nil {
		return nil, Binding{}, err
	}
	head := objectOf(v.Head)
	if err := o.isCommit(tip, "the target tip"); err != nil {
		return nil, Binding{}, err
	}
	if err := o.isCommit(head, "the head"); err != nil {
		return nil, Binding{}, err
	}
	base, err := mergeBase(ctx, r, tip, head)
	if err != nil {
		return nil, Binding{}, err
	}
	d, err := delta.BetweenWithin(r, base, head, b)
	if err != nil {
		return nil, Binding{}, fmt.Errorf("the delta from %s to %s: %w", base, head, err)
	}
	return rec, Binding{
		Record:    change.HeadPrefix + record,
		Version:   n,
		Head:      v.Head,
		TargetTip: change.HeadPrefix + tip,
		Base:      change.HeadPrefix + base,
		Delta:     d.Digest(),
	}, nil
}

// Against is what a binding is verified against, past the objects it
// names: refs as they stand now, and what the verification may spend.
type Against struct {
	// RecordTip is the commit the change's record ref points at now.
	// When it is set, the bound record commit must be the record of its
	// own seq on that ref's history, so a review binds a record the change
	// really has, and not one written to look like it.
	RecordTip string
	// TargetTip is the commit the target branch points at now. When it is
	// set, the bound target tip must be on its history.
	TargetTip string
	// Budget bounds the verification. The delta's reading and the reading
	// of the record and the notes' files are each held to its bytes, the
	// delta's to its entries, and all of it to its deadline.
	Budget delta.Budget
}

// Verify recomputes the binding of a review that judges a version, and
// refuses it unless every part reproduces. The bound record commit is a
// change record of the review's change and target, and names the bound
// version with the bound head. The target tip is a commit, and the base is
// the one merge base of it and the head. The delta from the base to the
// head has the bound digest. Each note's path holds its blob on its side,
// the base for before and the head for after, and a note's lines are
// within that blob. And the refs Against names, when set, hold the record
// and the tip on their histories.
//
// An error that wraps delta.ErrBudget says the binding could not be
// recomputed within the budget, not that it is wrong.
func Verify(r Repo, rec *Record, a Against) error {
	if !rec.Judges() || rec.Binding == nil {
		return errors.New("it is a withdrawal, and a withdrawal binds nothing to verify")
	}
	if a.Budget.Deadline.IsZero() {
		return errors.New("a binding is verified within a deadline, and the budget sets none")
	}
	ctx, cancel := context.WithDeadline(context.Background(), a.Budget.Deadline)
	defer cancel()
	o, err := openObjects(ctx, r, a.Budget.Bytes)
	if err != nil {
		return err
	}
	defer o.close()
	b := rec.Binding
	record, head, tip, base := objectOf(b.Record), objectOf(b.Head), objectOf(b.TargetTip), objectOf(b.Base)

	cr, v, err := o.version(record, b.Version)
	switch {
	case err != nil:
		return fmt.Errorf("its binding.record: %w", err)
	case cr.ChangeID != rec.ChangeID:
		return fmt.Errorf("its binding.record is a record of the change %s, and the review is about %s", cr.ChangeID, rec.ChangeID)
	case cr.Target != rec.Target:
		return fmt.Errorf("its binding.record is a record on the target %s, and the review is about %s", cr.Target, rec.Target)
	case v.Head != b.Head:
		return fmt.Errorf("its binding.record names %s as version %d, and the binding names %s", v.Head, b.Version, b.Head)
	}
	if a.RecordTip != "" {
		if err := o.onRecordHistory(ctx, r, a.RecordTip, record, cr.Seq); err != nil {
			return err
		}
	}
	if err := o.isCommit(tip, "its binding.target_tip"); err != nil {
		return err
	}
	if a.TargetTip != "" {
		on, err := isAncestor(ctx, r, tip, a.TargetTip)
		switch {
		case err != nil:
			return err
		case !on:
			return fmt.Errorf("its binding.target_tip %s is not on the history of the target's tip, %s", tip, a.TargetTip)
		}
	}
	found, err := mergeBase(ctx, r, tip, head)
	switch {
	case err != nil:
		return fmt.Errorf("its binding names %s as the base: %w", b.Base, err)
	case found != base:
		return fmt.Errorf("its binding.base is %s, and the merge base of its target_tip and its head is %s", base, found)
	}
	d, err := delta.BetweenWithin(r, base, head, a.Budget)
	switch {
	case err != nil:
		return fmt.Errorf("the delta from its base to its head: %w", err)
	case d.Digest() != b.Delta:
		return fmt.Errorf("its binding.delta is %s, and the delta from its base to its head has the digest %s", b.Delta, d.Digest())
	}
	for i, n := range rec.Notes {
		on := head
		if n.Side == Before {
			on = base
		}
		if err := o.anchor(on, n); err != nil {
			return fmt.Errorf("its notes[%d] %w", i, err)
		}
	}
	return nil
}

// version reads the change record commit record and the version n it
// names.
func (o *objects) version(record string, n int) (*change.Record, change.Version, error) {
	_, file, err := o.readOneFile(record, recordLayout)
	if err != nil {
		return nil, change.Version{}, err
	}
	cr, err := change.Parse(file)
	if err != nil {
		return nil, change.Version{}, fmt.Errorf("the change record %s does not read: %w", record, err)
	}
	if n < 1 || n > len(cr.Versions) {
		return nil, change.Version{}, fmt.Errorf("the change record %s holds %d versions, and no version %d", record, len(cr.Versions), n)
	}
	return cr, cr.Versions[n-1], nil
}

// isCommit refuses an ID that names no commit.
func (o *objects) isCommit(id, what string) error {
	if !validObjectID(id) {
		return fmt.Errorf("%s %q is not a full SHA-1 object ID", what, id)
	}
	_, kind, _, err := o.info(id)
	switch {
	case err != nil:
		return fmt.Errorf("%s %s %w", what, id, err)
	case kind != "commit":
		return fmt.Errorf("%s %s is a %s, where a commit is expected", what, id, kind)
	}
	return nil
}

// onRecordHistory refuses a record commit that is not the record of its
// seq on the history of the change's record ref, whose tip is tip. Every
// record after the first has the record before it as its first parent,
// and each seq is one more than the last, so the record of seq s is the
// commit S-s first parents down from the tip's record of seq S.
func (o *objects) onRecordHistory(ctx context.Context, r Repo, tip, record string, seq int) error {
	_, file, err := o.readOneFile(tip, recordLayout)
	if err != nil {
		return fmt.Errorf("the change's record ref: %w", err)
	}
	now, err := change.Parse(file)
	if err != nil {
		return fmt.Errorf("the change's record ref points at a record that does not read: %w", err)
	}
	if seq > now.Seq {
		return fmt.Errorf("its binding.record has the seq %d, and the change's record is at %d, so it is no record the change had", seq, now.Seq)
	}
	out, err := git(ctx, r, nil, "rev-list", "--first-parent", "--skip="+strconv.Itoa(now.Seq-seq), "--max-count=1", tip)
	if err != nil {
		return err
	}
	if found := strings.TrimSpace(string(out)); found != record {
		return fmt.Errorf("its binding.record %s is not the change's record of seq %d, which is %s", record, seq, found)
	}
	return nil
}

// anchor refuses a note whose path does not hold its blob in the commit
// given, or whose lines run past the blob's last line.
func (o *objects) anchor(commit string, n Note) error {
	id, kind, _, err := o.info(commit + ":" + n.Path)
	switch {
	case err != nil:
		return fmt.Errorf("names %q on the %s side, where there is no such file", n.Path, n.Side)
	case kind != "blob":
		return fmt.Errorf("names %q on the %s side, where it is a %s, not a file", n.Path, n.Side, kind)
	case change.HeadPrefix+id != n.Blob:
		return fmt.Errorf("names %q on the %s side as %s, where it is %s", n.Path, n.Side, n.Blob, change.HeadPrefix+id)
	case n.Lines == nil:
		return nil
	}
	content, err := o.read(id, "blob", o.bytes)
	if err != nil {
		return fmt.Errorf("names lines of %q, which %w", n.Path, err)
	}
	lines := bytes.Count(content, []byte{'\n'})
	if len(content) > 0 && content[len(content)-1] != '\n' {
		lines++
	}
	if n.Lines.End > lines {
		return fmt.Errorf("names lines %d to %d of %q on the %s side, which has %d", n.Lines.Start, n.Lines.End, n.Path, n.Side, lines)
	}
	return nil
}

// mergeBase is the one merge base of the commits a and b. It refuses with
// ErrNoBase when they have none and ErrAmbiguousBase when they have more.
func mergeBase(ctx context.Context, r Repo, a, b string) (string, error) {
	out, err := git(ctx, r, nil, "merge-base", "--all", a, b)
	var exit *exec.ExitError
	switch {
	case err != nil && errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0:
		return "", ErrNoBase
	case err != nil:
		return "", err
	}
	bases := strings.Fields(string(out))
	switch {
	case len(bases) == 0:
		return "", ErrNoBase
	case len(bases) > 1:
		return "", fmt.Errorf("%w: %s", ErrAmbiguousBase, strings.Join(bases, ", "))
	}
	return bases[0], nil
}

// isAncestor reports whether the commit a is on the history of the commit
// b, b itself included.
func isAncestor(ctx context.Context, r Repo, a, b string) (bool, error) {
	_, err := git(ctx, r, nil, "merge-base", "--is-ancestor", a, b)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
}
