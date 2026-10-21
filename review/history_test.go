package review

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"the-valley/change"
)

var ref = Ref("main", changeID)

// as is a principal that may write reviews under any name.
func as(principal string) Pusher {
	return Pusher{Principal: principal, MayReview: func(string) bool { return true }}
}

// appendAs builds rec on the head of w's history, checks it as the hook
// will, as pusher, and moves the ref, and returns the new head.
func (w *world) appendAs(rec *Record, pusher Pusher) string {
	w.t.Helper()
	old := w.refAt(ref)
	commit, err := Build(w.repo(), old, rec, soon())
	if err != nil {
		w.t.Fatal(err)
	}
	if err := CheckAppend(w.repo(), Update{Ref: ref, Old: old, New: commit}, pusher, w.against()); err != nil {
		w.t.Fatalf("CheckAppend: %v", err)
	}
	w.setRef(ref, commit)
	return commit
}

// A history keeps every review in order, each commit on the one before it
// and the record it binds, and a withdrawal on the one before it alone. It
// keeps the records it binds once the change's own record ref is deleted
// and the repository pruned.
func TestAHistoryKeepsEveryReviewInOrder(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	r1 := w.verdict(s, "alice", RequestChanges)
	r2 := w.verdict(s, "bob", Approve)
	r3 := withdrawal(r1, "alice")
	c1 := w.appendAs(r1, as("alice"))
	c2 := w.appendAs(r2, as("bob"))
	c3 := w.appendAs(r3, as("alice"))

	h, err := ReadHistory(w.repo(), ref, c3, soon())
	if err != nil {
		t.Fatal(err)
	}
	got := []Entry{}
	for _, e := range h.Entries {
		got = append(got, Entry{Commit: e.Commit, Parents: e.Parents})
	}
	want := []Entry{{c1, []string{s.rec2}, nil}, {c2, []string{c1, s.rec2}, nil}, {c3, []string{c2}, nil}}
	if !reflect.DeepEqual(got, want) || h.Head() != c3 {
		t.Fatalf("the history is %+v, want %+v", got, want)
	}
	for i, r := range []*Record{r1, r2, r3} {
		got, _ := Encode(h.Entries[i].Review)
		want, _ := Encode(r)
		if !bytes.Equal(got, want) {
			t.Fatalf("review %d reads back as %s, want %s", i, got, want)
		}
	}
	if e, ok := h.Withdrawal(r1.ReviewID); !ok || e.Commit != c3 {
		t.Fatalf("the withdrawal of the first review: %+v, %v", e, ok)
	}
	if _, ok := h.Withdrawal(r2.ReviewID); ok {
		t.Fatal("the second review reads as withdrawn")
	}
	if e, ok := h.Find(r2.ReviewID); !ok || e.Commit != c2 {
		t.Fatalf("the second review: %+v, %v", e, ok)
	}

	// The change's record ref goes, and with it every other way to reach
	// its records. The history keeps them, and the heads they name.
	dangling := w.blob("nothing reaches this\n")
	w.git(nil, "update-ref", "-d", change.Ref("main", changeID))
	w.git(nil, "gc", "--quiet", "--prune=now")
	if _, err := w.run(nil, "cat-file", "-e", dangling); err == nil {
		t.Fatal("gc pruned nothing, so the test shows nothing")
	}
	for _, kept := range []string{s.rec2, s.rec1, s.head2, s.head1} {
		if _, err := w.run(nil, "cat-file", "-e", kept); err != nil {
			t.Fatalf("%s was pruned with the record ref: %v", kept, err)
		}
	}
	if _, err := ReadHistory(w.repo(), ref, c3, soon()); err != nil {
		t.Fatalf("the history after the record ref went: %v", err)
	}
	// No record now, so no new judgment; a withdrawal needs none.
	r4 := w.verdict(s, "carol", Approve)
	r4.Binding.Record = change.HeadPrefix + s.rec2
	c4, err := Build(w.repo(), c3, r4, soon())
	if err != nil {
		t.Fatal(err)
	}
	err = CheckAppend(w.repo(), Update{Ref: ref, Old: c3, New: c4}, as("carol"), w.against())
	if err == nil || !strings.Contains(err.Error(), "holds no record of the change") {
		t.Fatalf("a judgment of a change with no record: %v", err)
	}
	w.appendAs(withdrawal(r2, "bob"), as("bob"))
}

// Each case is an update the hook must refuse, and CheckAppend refuses it,
// saying why.
func TestCheckAppendRefusesWhatIsNoAppend(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	r1 := w.verdict(s, "alice", RequestChanges)
	r2 := w.verdict(s, "bob", Approve)
	c1 := w.appendAs(r1, as("alice"))
	c2 := w.appendAs(r2, as("bob"))
	withdrawn := w.appendAs(withdrawal(r1, "alice"), as("alice"))
	w.setRef(ref, c2)

	encode := func(r *Record) []byte {
		data, err := Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	// commit writes a review commit by hand, past every check Build makes.
	commit := func(r *Record, parents ...string) string { return w.oneFile(File, encode(r), parents...) }
	edited := func(edit func(*Record)) *Record {
		r := w.verdict(s, "carol", Approve)
		edit(r)
		return r
	}
	unknown := withdrawal(r2, "bob")
	*unknown.Withdraws = id1
	renamed := withdrawal(r2, "bob")
	renamed.Review = "operator"
	extra := w.git([]byte("100644 blob "+w.blob(string(encode(r2)))+"\t"+File+"\n100644 blob "+w.blob("x")+"\textra\n"), "mktree")
	big := w.oneFile(File, []byte(strings.Repeat(" ", MaxRecord+1)), c2, s.rec2)

	cases := []struct {
		name    string
		update  Update
		pusher  Pusher
		against func(*Against)
		says    string
	}{
		{name: "a delete", update: Update{ref, c2, strings.Repeat("0", 40)}, says: "no push deletes one"},
		{name: "a ref that is no history's", update: Update{RefPrefix + "main/" + id1, c2, c2}, says: "a review history's ref is"},
		{name: "a key that names no principal", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.rec2)}, pusher: Pusher{}, says: "named principal"},
		{name: "a review under a name the pusher may not write", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.rec2)}, pusher: Pusher{Principal: "carol", MayReview: func(name string) bool { return name == "operator" }}, says: "carol may not write reviews under that name"},
		{name: "a review by a pusher who may write none", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.rec2)}, pusher: Pusher{Principal: "carol"}, says: "carol may not write reviews under that name"},
		{name: "a review by someone else", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.rec2)}, pusher: as("dave"), says: "names carol as the reviewer, and dave is pushing it"},
		{name: "an ID the history holds", update: Update{ref, c2, commit(edited(func(r *Record) { r.ReviewID = r1.ReviewID }), c2, s.rec2)}, says: "is the ID of the review in " + c1},
		{name: "a review of another change", update: Update{ref, c2, commit(edited(func(r *Record) { r.ChangeID = otherID }), c2, s.rec2)}, says: "a review of the change " + otherID},
		{name: "a first review on a history", update: Update{ref, c2, commit(edited(func(r *Record) {}), s.rec2)}, says: "as the first review commit does"},
		{name: "a review on an older head", update: Update{ref, c2, commit(edited(func(r *Record) {}), c1, s.rec2)}, says: "its first parent is " + c1},
		{name: "a second history", update: Update{ref, "", commit(edited(func(r *Record) {}), c1, s.rec2)}, says: "it starts the history"},
		{name: "a judgment that does not keep its record", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.head2)}, says: "as its last parent"},
		{name: "a judgment with three parents", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.rec1, s.rec2)}, says: "has 3 parents"},
		{name: "a tree with another file", update: Update{ref, c2, w.commitTree(extra, c2, s.rec2)}, says: "holds something other than review.json alone"},
		{name: "a review of more than 64 KiB", update: Update{ref, c2, big}, says: "more than the 65536 it may be"},
		{name: "a blob", update: Update{ref, c2, w.blob("x")}, says: "is a blob, where a commit is expected"},
		{name: "a history that does not read", update: Update{ref, s.head2, commit(edited(func(r *Record) {}), s.head2, s.rec2)}, says: "the history it appends to does not read"},
		{name: "a binding that does not verify", update: Update{ref, c2, commit(edited(func(r *Record) { r.Binding.Delta = digest }), c2, s.rec2)}, says: "its binding does not verify"},
		{name: "a change with no record", update: Update{ref, c2, commit(edited(func(r *Record) {}), c2, s.rec2)}, against: func(a *Against) { a.RecordTip = "" }, says: "holds no record of the change"},
		{name: "a withdrawal of someone else's review", update: Update{ref, c2, commit(withdrawal(r1, "bob"), c2)}, pusher: as("bob"), says: "only its reviewer or an operator"},
		{name: "a withdrawal of a review the history lacks", update: Update{ref, c2, commit(unknown, c2)}, pusher: as("bob"), says: "holds no review with that ID"},
		{name: "a withdrawal under another name", update: Update{ref, c2, commit(renamed, c2)}, pusher: as("bob"), says: "under the review name operator"},
		{name: "a withdrawal twice", update: Update{ref, withdrawn, commit(withdrawal(r1, "alice"), withdrawn)}, pusher: as("alice"), says: "withdrew already"},
		{name: "a withdrawal with the record as a parent", update: Update{ref, c2, commit(withdrawal(r2, "bob"), c2, s.rec2)}, pusher: as("bob"), says: "where a withdrawal has one"},
		{name: "a withdrawal that starts a history", update: Update{ref, "", commit(withdrawal(r2, "bob"), s.rec2)}, pusher: as("bob"), says: "holds no review with that ID"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pusher := c.pusher
			if pusher.Principal == "" && !strings.Contains(c.says, "named principal") {
				pusher = as("carol")
			}
			a := w.against()
			if c.against != nil {
				c.against(&a)
			}
			err := CheckAppend(w.repo(), c.update, pusher, a)
			if err == nil {
				t.Fatal("it was admitted")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Fatalf("the refusal does not say %q: %v", c.says, err)
			}
		})
	}
	// A withdrawal is final: none withdraws it.
	w.setRef(ref, withdrawn)
	h, err := ReadHistory(w.repo(), ref, withdrawn, soon())
	if err != nil {
		t.Fatal(err)
	}
	final := withdrawal(h.Entries[2].Review, "alice")
	err = CheckAppend(w.repo(), Update{ref, withdrawn, commit(final, withdrawn)}, as("alice"), w.against())
	if err == nil || !strings.Contains(err.Error(), "a withdrawal is final") {
		t.Fatalf("a withdrawal of a withdrawal: %v", err)
	}
	// An operator may withdraw anyone's review, under any name, and needs
	// no grant of the name to do it; to judge a version, they need one.
	w.appendAs(withdrawal(r2, "operator"), Pusher{Principal: "operator", Operator: true})
	judged := w.verdict(s, "operator", Approve)
	err = CheckAppend(w.repo(), Update{ref, w.refAt(ref), commit(judged, w.refAt(ref), s.rec2)}, Pusher{Principal: "operator", Operator: true}, w.against())
	if err == nil || !strings.Contains(err.Error(), "operator may not write reviews under that name") {
		t.Fatalf("an operator's judgment under a name they may not write: %v", err)
	}
}

// A judgment needs the grant of its review name, and a withdrawal does
// not. A reviewer whose grant is revoked can still take back their own
// judgment, and an operator can take back anyone's. Nobody else can take
// one back, with the grant or without it.
func TestAWithdrawalNeedsNoGrantOfItsName(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	r1 := w.verdict(s, "alice", RequestChanges)
	r2 := w.verdict(s, "bob", RequestChanges)
	w.appendAs(r1, as("alice"))
	head := w.appendAs(r2, as("bob"))
	revoked := func(principal string) Pusher {
		return Pusher{Principal: principal, MayReview: func(string) bool { return false }}
	}
	check := func(rec *Record, p Pusher) error {
		t.Helper()
		c, err := Build(w.repo(), head, rec, soon())
		if err != nil {
			t.Fatal(err)
		}
		return CheckAppend(w.repo(), Update{ref, head, c}, p, w.against())
	}

	if err := check(w.verdict(s, "alice", Approve), revoked("alice")); err == nil || !strings.Contains(err.Error(), "alice may not write reviews under that name") {
		t.Fatalf("a judgment under a revoked grant: %v", err)
	}
	for _, p := range []Pusher{as("carol"), revoked("carol"), {Principal: "carol"}} {
		if err := check(withdrawal(r1, "carol"), p); err == nil || !strings.Contains(err.Error(), "only its reviewer or an operator") {
			t.Fatalf("a withdrawal of alice's review by carol: %v", err)
		}
	}

	w.appendAs(withdrawal(r1, "alice"), revoked("alice"))
	w.appendAs(withdrawal(r2, "operator"), Pusher{Principal: "operator", Operator: true})
	h, err := ReadHistory(w.repo(), ref, w.refAt(ref), soon())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*Record{r1, r2} {
		if _, ok := h.Withdrawal(r.ReviewID); !ok {
			t.Fatalf("%s's review is not withdrawn", r.Reviewer)
		}
	}
}

// Two clients append to one history at once. The second's publish fails
// on its stale lease; it reads the new head and appends again, with the
// same review. Building that review once more finds it recorded.
func TestAppendsAreCompareAndSwap(t *testing.T) {
	origin := newWorld(t)
	s := origin.story()
	a, b := origin.clone(), origin.clone()
	ra := a.verdict(s, "alice", RequestChanges)
	rb := b.verdict(s, "bob", Approve)

	ca, err := Build(a.repo(), "", ra, soon())
	if err != nil {
		t.Fatal(err)
	}
	cb, err := Build(b.repo(), "", rb, soon())
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(a.repo(), "origin", ref, "", ca, soon()); err != nil {
		t.Fatal(err)
	}
	if err := Publish(b.repo(), "origin", ref, "", cb, soon()); !errors.Is(err, ErrStale) {
		t.Fatalf("a publish on a stale lease: %v", err)
	}
	b.git(nil, "fetch", "--quiet", "origin", "+"+ref+":"+ref)
	head := b.refAt(ref)
	if head != ca {
		t.Fatalf("the fetched head is %s, where %s published %s", head, "alice", ca)
	}
	cb, err = Build(b.repo(), head, rb, soon())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckAppend(b.repo(), Update{ref, head, cb}, as("bob"), b.against()); err != nil {
		t.Fatal(err)
	}
	if err := Publish(b.repo(), "origin", ref, head, cb, soon()); err != nil {
		t.Fatal(err)
	}
	if again, err := Build(b.repo(), cb, rb, soon()); !errors.Is(err, ErrRecorded) || again != cb {
		t.Fatalf("building a recorded review again: %s, %v", again, err)
	}
	changed := *rb
	changed.Summary = "Another judgment under the same ID."
	if _, err := Build(b.repo(), cb, &changed, soon()); err == nil || errors.Is(err, ErrRecorded) {
		t.Fatalf("another review under a recorded ID: %v", err)
	}
	if err := Publish(a.repo(), "origin", ref, ca, ca, soon()); !errors.Is(err, ErrStale) {
		t.Fatalf("a publish on a lease of an older head: %v", err)
	}
	// A withdrawal, which has no notes, is found recorded too.
	wb := withdrawal(rb, "bob")
	cw, err := Build(b.repo(), cb, wb, soon())
	if err != nil {
		t.Fatal(err)
	}
	if err := Publish(b.repo(), "origin", ref, cb, cw, soon()); err != nil {
		t.Fatal(err)
	}
	if again, err := Build(b.repo(), cw, wb, soon()); !errors.Is(err, ErrRecorded) || again != cw {
		t.Fatalf("building a recorded withdrawal again: %s, %v", again, err)
	}
	h, err := ReadHistory(origin.repo(), ref, origin.refAt(ref), soon())
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Entries) != 3 || h.Entries[0].Review.ReviewID != ra.ReviewID || h.Entries[1].Review.ReviewID != rb.ReviewID {
		t.Fatalf("origin's history holds %d reviews", len(h.Entries))
	}
}

// A history holds at most MaxHistory reviews: reading a longer one fails,
// and so does appending to a full one.
func TestAHistoryIsBounded(t *testing.T) {
	w := newWorld(t)
	s := w.story()
	r := w.verdict(s, "alice", Approve)
	var stream strings.Builder
	for k := 1; k <= MaxHistory+1; k++ {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		r.ReviewID = id
		data, err := Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&stream, "commit refs/heads/scratch\nmark :%d\ncommitter t <t@localhost> 0 +0000\ndata 1\nr\n", k)
		if k == 1 {
			fmt.Fprintf(&stream, "from %s\n", s.rec2)
		} else {
			fmt.Fprintf(&stream, "from :%d\nmerge %s\n", k-1, s.rec2)
		}
		fmt.Fprintf(&stream, "deleteall\nM 100644 inline %s\ndata %d\n%s\n", File, len(data), data)
	}
	marks := filepath.Join(t.TempDir(), "marks")
	w.git([]byte(stream.String()), "fast-import", "--quiet", "--export-marks="+marks)
	exported, err := os.ReadFile(marks)
	if err != nil {
		t.Fatal(err)
	}
	commits := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(exported)), "\n") {
		mark, oid, _ := strings.Cut(line, " ")
		commits[mark] = oid
	}
	full, past := commits[fmt.Sprintf(":%d", MaxHistory)], commits[fmt.Sprintf(":%d", MaxHistory+1)]
	h, err := ReadHistory(w.repo(), ref, full, soon())
	if err != nil || len(h.Entries) != MaxHistory {
		t.Fatalf("a history of %d reviews: %v", MaxHistory, err)
	}
	if _, err := ReadHistory(w.repo(), ref, past, soon()); err == nil || !strings.Contains(err.Error(), "more than the 512") {
		t.Fatalf("a history of %d reviews: %v", MaxHistory+1, err)
	}
	err = CheckAppend(w.repo(), Update{ref, full, past}, as("alice"), w.against())
	if err == nil || !strings.Contains(err.Error(), "the most a history may") {
		t.Fatalf("an append to a full history: %v", err)
	}
}
