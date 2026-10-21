package review

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"the-valley/change"
	"the-valley/delta"
)

// A binding names the record, the version and its head, the tip judged
// against, the one merge base of the tip and the head, and the digest of
// the delta between them, and a review bound with it verifies.
func TestABindingReproduces(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	cr, b, err := Bind(w.repo(), s.rec2, 2, s.tip, budget())
	if err != nil {
		t.Fatal(err)
	}
	d, err := delta.Between(delta.Repo{Dir: w.dir, Env: w.env}, s.base, s.head2)
	if err != nil {
		t.Fatal(err)
	}
	want := Binding{
		Record: change.HeadPrefix + s.rec2, Version: 2, Head: change.HeadPrefix + s.head2,
		TargetTip: change.HeadPrefix + s.tip, Base: change.HeadPrefix + s.base, Delta: d.Digest(),
	}
	if b != want || cr.ChangeID != changeID || cr.Target != "main" {
		t.Fatalf("Bind = %+v for %s on %s, want %+v", b, cr.ChangeID, cr.Target, want)
	}
	if len(d) != 2 {
		t.Fatalf("the delta has %d paths, where the version changes 2", len(d))
	}
	rec := w.verdict(s, "reviewer", Approve)
	if err := Verify(w.repo(), rec, w.against()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Bind(w.repo(), s.rec2, 3, s.tip, budget()); err == nil || !strings.Contains(err.Error(), "no version 3") {
		t.Fatalf("a version the record does not hold: %v", err)
	}
}

// A head and a tip with no history in common, or with two merge bases,
// give no base to compare from, so no binding: Bind refuses, and Verify
// refuses a binding that names one.
func TestNoBaseOrAnAmbiguousOneMakesNoBinding(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	unrelated := w.commit(map[string]string{"elsewhere": "x\n"})
	if _, _, err := Bind(w.repo(), s.rec2, 2, unrelated, budget()); !errors.Is(err, ErrNoBase) {
		t.Fatalf("a tip with no history in common: %v", err)
	}

	// A criss-cross: two merges of a and b, each the other way round, have
	// both a and b as merge bases.
	a := w.commit(map[string]string{"README.md": "a\n"}, s.base)
	b := w.commit(map[string]string{"README.md": "b\n"}, s.base)
	ab := w.commit(map[string]string{"README.md": "ab\n"}, a, b)
	ba := w.commit(map[string]string{"README.md": "ba\n"}, b, a)
	cr := &change.Record{
		Schema: change.Schema, ChangeID: otherID, Target: "main", Owner: "author", State: change.Ready, Seq: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + ba, Title: "Cross", Uploader: "author"}},
		Active:   1,
	}
	record := w.record(cr, ba)
	w.setRef(change.Ref("main", otherID), record)
	w.setRef("refs/heads/main", ab)
	_, _, err := Bind(w.repo(), record, 1, ab, budget())
	if !errors.Is(err, ErrAmbiguousBase) || !strings.Contains(err.Error(), a) || !strings.Contains(err.Error(), b) {
		t.Fatalf("a criss-cross: %v", err)
	}
	d, err := delta.Between(delta.Repo{Dir: w.dir, Env: w.env}, a, ba)
	if err != nil {
		t.Fatal(err)
	}
	rec := &Record{
		Schema: Schema, ReviewID: id1, ChangeID: otherID, Target: "main", Review: "operator", Reviewer: "reviewer",
		Verdict: Approve, Summary: "Fine.",
		Binding: &Binding{Record: change.HeadPrefix + record, Version: 1, Head: change.HeadPrefix + ba,
			TargetTip: change.HeadPrefix + ab, Base: change.HeadPrefix + a, Delta: d.Digest()},
	}
	against := Against{RecordTip: record, TargetTip: ab, Budget: budget()}
	if err := Verify(w.repo(), rec, against); !errors.Is(err, ErrAmbiguousBase) {
		t.Fatalf("a binding that picks one of two bases: %v", err)
	}
}

// Each case changes one part of a good review, and Verify refuses it,
// saying which part does not reproduce.
func TestVerifyRefusesWhatDoesNotReproduce(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	other := &change.Record{
		Schema: change.Schema, ChangeID: otherID, Target: "main", Owner: "author", State: change.Ready, Seq: 2,
		Versions: s.record.Versions, Active: 2,
	}
	otherRecord := w.record(other, s.rec1, s.head2)
	// A record commit that reads exactly as the change's record of seq 2
	// does, written beside its history rather than on it.
	forged := w.record(s.record, s.head2)
	offMain := w.commit(map[string]string{"README.md": "elsewhere\n", "docs/guide.md": "one\ntwo\nthree\n"}, s.base)
	blob := w.blob("x\n")
	cases := []struct {
		name    string
		change  func(r *Record, a *Against)
		says    string
		budgets bool
	}{
		{"another delta", func(r *Record, a *Against) { r.Binding.Delta = digest }, "binding.delta is " + digest, false},
		{"another base", func(r *Record, a *Against) { r.Binding.Base = change.HeadPrefix + s.tip }, "binding.base is " + s.tip, false},
		{"another head", func(r *Record, a *Against) { r.Binding.Head = change.HeadPrefix + s.head1 }, "as version 2", false},
		{"a version the record does not hold", func(r *Record, a *Against) { r.Binding.Version = 3 }, "no version 3", false},
		{"a record that holds one version", func(r *Record, a *Against) { r.Binding.Record = change.HeadPrefix + s.rec1 }, "no version 2", false},
		{"another change's record", func(r *Record, a *Against) { r.Binding.Record = change.HeadPrefix + otherRecord }, "a record of the change " + otherID, false},
		{"a record that is no commit", func(r *Record, a *Against) { r.Binding.Record = change.HeadPrefix + blob }, "is a blob", false},
		{"a record off the change's history", func(r *Record, a *Against) { r.Binding.Record = change.HeadPrefix + forged }, "is not the change's record of seq 2", false},
		{"a record past the change's", func(r *Record, a *Against) { a.RecordTip = s.rec1 }, "the change's record is at 1", false},
		{"a tip that is no commit", func(r *Record, a *Against) { r.Binding.TargetTip = change.HeadPrefix + blob }, "is a blob, where a commit is expected", false},
		{"a tip off the target's history", func(r *Record, a *Against) { r.Binding.TargetTip = change.HeadPrefix + offMain }, "is not on the history of the target's tip", false},
		{"a note on another blob", func(r *Record, a *Against) { r.Notes[0].Blob = change.HeadPrefix + blob }, "notes[0] names \"docs/guide.md\" on the after side as", false},
		{"a note on the other side", func(r *Record, a *Against) { r.Notes[2].Side = Before }, "notes[2] names \"docs/new file.md\" on the before side, where there is no such file", false},
		{"a note on a directory", func(r *Record, a *Against) { r.Notes[0].Path = "docs" }, "where it is a tree", false},
		{"a note past the last line", func(r *Record, a *Against) { r.Notes[1].Lines = &Lines{Start: 3, End: 4} }, "names lines 3 to 4 of \"docs/guide.md\" on the before side, which has 3", false},
		{"no time left", func(r *Record, a *Against) { a.Budget.Deadline = time.Now().Add(-time.Second) }, "deadline", true},
		{"no deadline at all", func(r *Record, a *Against) { a.Budget.Deadline = time.Time{} }, "sets none", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, a := w.verdict(s, "reviewer", RequestChanges), w.against()
			if err := Verify(w.repo(), rec, a); err != nil {
				t.Fatalf("the review before the change: %v", err)
			}
			c.change(rec, &a)
			err := Verify(w.repo(), rec, a)
			if err == nil {
				t.Fatal("it verified")
			}
			if !strings.Contains(err.Error(), c.says) || errors.Is(err, delta.ErrBudget) != c.budgets {
				t.Fatalf("the refusal does not say %q, or is wrongly a budget's: %v", c.says, err)
			}
		})
	}
	if err := Verify(w.repo(), withdrawal(w.verdict(s, "reviewer", Approve), "reviewer"), w.against()); err == nil {
		t.Fatal("a withdrawal verified")
	}
}

// A version whose tree is hostile, a few small trees each naming the next
// 64 times, or whose files are larger than the budget, cannot be bound or
// verified within the budget. The refusal says so, as a budget's, and not
// that the binding is wrong.
func TestHostileTreesHitTheBudget(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	tree := w.tree(map[string]string{"leaf": "x\n"})
	for level := 0; level < 4; level++ {
		var entries strings.Builder
		for i := 0; i < 64; i++ {
			fmt.Fprintf(&entries, "040000 tree %s\td%02d\n", tree, i)
		}
		tree = w.git([]byte(entries.String()), "mktree")
	}
	hostile := w.commitTree(tree, s.base)
	large := w.commit(map[string]string{"README.md": strings.Repeat("0123456789abcdef", 1<<18)}, s.base)
	for _, c := range []struct {
		name, head string
		budget     func(*delta.Budget)
		says       string
	}{
		{"64 to the 4th paths", hostile, func(b *delta.Budget) { b.Entries = 10000 }, "10000 entries"},
		{"a file of 4 MiB", large, func(b *delta.Budget) { b.Bytes = 1 << 20 }, "1048576 bytes"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cr := &change.Record{
				Schema: change.Schema, ChangeID: otherID, Target: "main", Owner: "author", State: change.Ready, Seq: 1,
				Versions: []change.Version{{N: 1, Head: change.HeadPrefix + c.head, Title: "Hostile", Uploader: "author"}},
				Active:   1,
			}
			record := w.record(cr, c.head)
			b := budget()
			c.budget(&b)
			start := time.Now()
			_, _, err := Bind(w.repo(), record, 1, s.tip, b)
			if !errors.Is(err, delta.ErrBudget) || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("Bind: %v", err)
			}
			rec := &Record{
				Schema: Schema, ReviewID: id1, ChangeID: otherID, Target: "main", Review: "operator", Reviewer: "reviewer",
				Verdict: RequestChanges, Summary: "Too large to read.",
				Binding: &Binding{Record: change.HeadPrefix + record, Version: 1, Head: change.HeadPrefix + c.head,
					TargetTip: change.HeadPrefix + s.tip, Base: change.HeadPrefix + s.base, Delta: digest},
			}
			err = Verify(w.repo(), rec, Against{RecordTip: record, TargetTip: s.tip, Budget: b})
			if !errors.Is(err, delta.ErrBudget) || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("Verify: %v", err)
			}
			if elapsed := time.Since(start); elapsed > 20*time.Second {
				t.Fatalf("stopping at the budget took %s", elapsed)
			}
		})
	}
}

// A deadline that passes after the last read from git, while the delta is
// hashed, fails Bind and Verify as a budget's refusal. Neither returns a
// result computed late, and a note looked up after the deadline is not
// taken for a note on a file that is not there.
func TestTheDeadlineHoldsAfterTheLastRead(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	var deadline time.Time
	reached := false
	digested = func() {
		reached = true
		time.Sleep(time.Until(deadline) + 10*time.Millisecond)
	}
	defer func() { digested = func() {} }()
	tight := func() delta.Budget {
		b := budget()
		deadline = time.Now().Add(2 * time.Second)
		b.Deadline = deadline
		reached = false
		return b
	}
	withNotes := w.verdict(s, "reviewer", Approve)
	noNotes := w.verdict(s, "reviewer", Approve)
	noNotes.Notes = nil
	for _, c := range []struct {
		name string
		run  func(delta.Budget) error
	}{
		{"Bind", func(b delta.Budget) error {
			_, _, err := Bind(w.repo(), s.rec2, 2, s.tip, b)
			return err
		}},
		{"Verify, with notes to look up", func(b delta.Budget) error {
			a := w.against()
			a.Budget = b
			return Verify(w.repo(), withNotes, a)
		}},
		{"Verify, with no notes", func(b delta.Budget) error {
			a := w.against()
			a.Budget = b
			return Verify(w.repo(), noNotes, a)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := c.run(tight())
			if !reached {
				t.Fatalf("it did not reach the digest within two seconds: %v", err)
			}
			if !errors.Is(err, delta.ErrBudget) || !strings.Contains(err.Error(), "deadline") {
				t.Fatalf("a deadline passed while hashing: %v", err)
			}
		})
	}
}

// A file a note names has no bound of its own, only the budget's bytes. A
// note on an unchanged file too large for them, which the delta never
// reads, is refused as the budget's, so the caller can tell a review too
// large to check from one that is wrong. Within a larger budget it
// verifies.
func TestANoteOnAFileTooLargeForTheBudget(t *testing.T) {
	w := newWorld(t)
	large := strings.Repeat("0123456789abcdef\n", 1<<16)
	base := w.commit(map[string]string{"large.txt": large, "README.md": "hello\n"})
	head := w.commit(map[string]string{"large.txt": large, "README.md": "hello, world\n"}, base)
	cr := &change.Record{
		Schema: change.Schema, ChangeID: otherID, Target: "main", Owner: "author", State: change.Ready, Seq: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: "Greet the world", Uploader: "author"}},
		Active:   1,
	}
	record := w.record(cr, head)
	_, b, err := Bind(w.repo(), record, 1, base, budget())
	if err != nil {
		t.Fatal(err)
	}
	rec := &Record{
		Schema: Schema, ReviewID: id1, ChangeID: otherID, Target: "main", Review: "operator", Reviewer: "reviewer",
		Verdict: Approve, Binding: &b, Summary: "Fine.",
		Notes: []Note{{Side: After, Path: "large.txt", Blob: change.HeadPrefix + w.git(nil, "rev-parse", head+":large.txt"),
			Lines: &Lines{Start: 1, End: 2}, Body: "These lines are fine."}},
	}
	against := Against{RecordTip: record, TargetTip: base, Budget: budget()}
	if err := Verify(w.repo(), rec, against); err != nil {
		t.Fatalf("within a budget of 64 MiB: %v", err)
	}
	against.Budget.Bytes = 1 << 20
	err = Verify(w.repo(), rec, against)
	if !errors.Is(err, delta.ErrBudget) || !strings.Contains(err.Error(), `notes[0] names lines of "large.txt"`) {
		t.Fatalf("a note on a file of %d bytes within 1 MiB: %v", len(large), err)
	}
	rec.Notes[0].Path = "missing.txt"
	err = Verify(w.repo(), rec, against)
	if errors.Is(err, delta.ErrBudget) || err == nil || !strings.Contains(err.Error(), "where there is no such file") {
		t.Fatalf("a note on a file that is not there: %v", err)
	}
}
