package main

// What the change records refuse, read from a real repository: a request
// named for a change lands only while that change is ready at the
// request's head, a request of any name is held back by a record that is
// not ready at its head, and a request whose commits hold a merge is
// refused. The records are written by hand, the way anyone holding a key
// could write one where no hook guards the repository.

import (
	"os"
	"strings"
	"testing"

	"the-valley/change"
)

// scratchRepo is a repository with a main, and a way to make commits and
// records in it.
type scratchRepo struct {
	t    *testing.T
	repo string
}

func newScratchRepo(t *testing.T) *scratchRepo {
	s := &scratchRepo{t: t, repo: t.TempDir()}
	s.git("init", "--quiet", "--initial-branch=main")
	s.git("commit", "--quiet", "--allow-empty", "-m", "base")
	return s
}

func (s *scratchRepo) git(args ...string) string {
	s.t.Helper()
	out, err := git(s.repo, append([]string{"-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func (s *scratchRepo) input(stdin string, args ...string) string {
	s.t.Helper()
	out, err := gitInput(s.repo, []byte(stdin), append([]string{"-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// commit is a new commit on parents, each with a tree of its own.
func (s *scratchRepo) commit(subject string, parents ...string) string {
	s.t.Helper()
	blob := s.input(subject+"\n", "hash-object", "-w", "--stdin")
	tree := s.input("100644 blob "+blob+"\t"+subject+".txt\n", "mktree")
	args := []string{"commit-tree", tree, "-m", subject}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return s.git(args...)
}

// record writes a record of the change at the heads given, in the state
// given, as the ref for id.
func (s *scratchRepo) record(id, state string, heads ...string) {
	s.t.Helper()
	r := &change.Record{
		Schema: change.Schema, ChangeID: id, Target: "main", Owner: "owner", State: state,
		Seq: len(heads) + 1, Active: len(heads),
	}
	for i, h := range heads {
		r.Versions = append(r.Versions, change.Version{
			N: i + 1, Head: change.HeadPrefix + h, Title: "a version", Branch: "topic", Uploader: "owner",
		})
	}
	data, err := change.Encode(r)
	if err != nil {
		s.t.Fatal(err)
	}
	s.recordText(change.Ref("main", id), string(data), heads...)
}

// recordText writes a record commit holding text as its record.json, at ref.
func (s *scratchRepo) recordText(ref, text string, parents ...string) {
	s.t.Helper()
	blob := s.input(text, "hash-object", "-w", "--stdin")
	tree := s.input("100644 blob "+blob+"\trecord.json\n", "mktree")
	args := []string{"commit-tree", tree, "-m", "a change record"}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	s.git("update-ref", ref, s.git(args...))
}

const (
	readyID = "kkkkllllmmmmnnnnooooppppqqqqrrrr"
	draftID = "zzzzyyyyxxxxwwwwvvvvuuuuttttssss"
	goneID  = "ppppppppppppppppqqqqqqqqqqqqqqqq"
)

func TestTheRecordsRefuseARequestForAChangeThatIsNotReady(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	v1 := s.commit("v1", base)
	v2 := s.commit("v2", base)
	drafted := s.commit("drafted", base)
	child := s.commit("child of drafted", drafted)
	loose := s.commit("loose", base)
	s.record(readyID, change.Ready, v1, v2)
	s.record(draftID, change.Draft, drafted)
	// A record that does not read, and one whose ref names another change.
	s.recordText(change.Ref("main", goneID), "{}\n", loose)
	s.recordText(change.Ref("main", "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"),
		strings.Replace(recordJSON(t, readyID, change.Draft, loose), readyID, draftID, 1), loose)

	in := &integrator{repo: s.repo}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.unreadable) != 2 {
		t.Fatalf("unreadable = %q, want the two records that do not read", idx.unreadable)
	}
	if len(idx.byHead[v1]) != 1 || len(idx.byHead[v2]) != 1 || len(idx.byHead[drafted]) != 1 || len(idx.byHead[loose]) != 0 {
		t.Fatalf("the index by head is %v", idx.byHead)
	}

	for _, c := range []struct {
		name, change, head string
		refused            bool
		says               string
	}{
		{"its change's request at a version head", change.RequestName(readyID), v2, false, ""},
		{"its change's request at an older version head", change.RequestName(readyID), v1, false, ""},
		{"its change's request at a head no version records", change.RequestName(readyID), loose, true, "is no version of change " + readyID},
		{"a draft's request", change.RequestName(draftID), drafted, true, "change " + draftID + " is draft, not ready"},
		{"a request for a change with no record", change.RequestName("llllllllllllllllllllllllllllllll"), loose, true, "holds no record of it"},
		{"a request for a change whose record does not read", change.RequestName(goneID), loose, true, "does not read"},
		{"a branch's request at a draft's head", "topic", drafted, true, "is v1 of change " + draftID + ", which is draft, not ready"},
		{"a request for a ready change at a draft's head", change.RequestName(readyID), drafted, true, ""},
		{"a branch's request at a ready change's head", "topic", v1, false, ""},
		{"a branch's request no record knows", "loose", loose, false, ""},
		{"a branch named like a request but not one", "c-" + readyID[:31], drafted, true, "which is draft"},
		// A line of commits above a draft's head brings the draft with it.
		{"another name's request for a child of a draft's head", "other", child, true,
			"the commit " + drafted[:12] + " it brings is v1 of change " + draftID + ", which is draft, not ready"},
	} {
		r := request{ref: requestPrefix + "/main/" + c.change, change: c.change, target: "refs/heads/main", head: c.head}
		outcome, why, err := in.recordRefusal(r, idx, base)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if refused := outcome != ""; refused != c.refused || (refused && outcome != refusedNotReady) {
			t.Errorf("%s: refused %q (%s), want refused %v", c.name, outcome, why, c.refused)
		}
		if !strings.Contains(why, c.says) {
			t.Errorf("%s: refused saying %q, without %q", c.name, why, c.says)
		}
	}
	// Once the draft has landed, its head is on the tip and brings nothing.
	r := request{change: "other", target: "refs/heads/main", head: child}
	if outcome, why, _ := in.recordRefusal(r, idx, drafted); outcome != "" {
		t.Errorf("a draft's head already on the tip still refuses its child: %s", why)
	}

	// The final check reads every record again: the draft readied lets its
	// head through, the ready change unreadied holds its heads back, and a
	// record that newly names a head the pass's index did not know holds
	// that head back too.
	s.record(draftID, change.Ready, drafted)
	s.record(readyID, change.Draft, v1, v2)
	newID := "lllllllllllllllllllllllllllllllk"
	s.record(newID, change.Draft, loose)
	for _, c := range []struct {
		head    string
		refused bool
	}{{drafted, false}, {v1, true}, {loose, true}} {
		r := request{change: "topic", target: "refs/heads/main", head: c.head}
		outcome, why, err := in.finalRefusal(r, base)
		if err != nil {
			t.Fatal(err)
		}
		if (outcome == refusedNotReady) != c.refused {
			t.Errorf("the final check of %s: refused %q (%s), want refused %v", c.head[:12], outcome, why, c.refused)
		}
	}
}

// A record that does not read may name only commits its record commit
// reaches, so a request is flagged when a commit it brings is one of
// those, and not otherwise.
func TestARecordThatDoesNotReadIsReportedWhereItMayMatter(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	named := s.commit("named", base)
	above := s.commit("above", named)
	elsewhere := s.commit("elsewhere", base)
	s.recordText(change.Ref("main", draftID), "{}\n", named)
	in := &integrator{repo: s.repo}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, head, tip string
		flagged         bool
	}{
		{"its own head", named, base, true},
		{"a child of its head", above, base, true},
		{"a head it cannot reach", elsewhere, base, false},
		{"a child of its head, once its head is on the tip", above, named, false},
	} {
		r := request{change: "topic", target: "refs/heads/main", head: c.head}
		if got := len(in.uncheckable(r, idx, c.tip)) == 1; got != c.flagged {
			t.Errorf("%s: flagged %v, want %v", c.name, got, c.flagged)
		}
		if outcome, _, _ := in.recordRefusal(r, idx, c.tip); outcome != "" {
			t.Errorf("%s: a record that does not read refused it", c.name)
		}
	}
}

// A withdrawn change's requests are deleted, named for it or at one of its
// version heads, each only while the change is still withdrawn and the
// request still where the pass found it.
func TestAWithdrawnChangeVoidsItsRequests(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	h := s.commit("withdrawn", base)
	other := s.commit("other", base)
	readied := s.commit("readied", base)
	s.record(draftID, change.Withdrawn, h)
	s.record(readyID, change.Withdrawn, readied)
	ref := func(name string) string { return requestPrefix + "/main/" + name }
	for name, head := range map[string]string{
		change.RequestName(draftID): h, "topic": h, "unrelated": other, "moved": h, "readied": readied,
	} {
		s.git("update-ref", ref(name), head)
	}
	in := &integrator{repo: s.repo, out: devNull(t)}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	// Between the index and the voiding, one request is approved again at
	// another head, and one change is readied again.
	s.git("update-ref", ref("moved"), other)
	s.record(readyID, change.Ready, readied)
	var requests []request
	for _, name := range []string{change.RequestName(draftID), "topic", "unrelated", "moved", "readied"} {
		head := h
		switch name {
		case "unrelated":
			head = other
		case "readied":
			head = readied
		}
		requests = append(requests, request{ref: ref(name), change: name, target: "refs/heads/main", head: head})
	}
	kept := in.voidWithdrawn(requests, idx)
	var names []string
	for _, r := range kept {
		names = append(names, r.change)
	}
	if strings.Join(names, " ") != "unrelated moved readied" {
		t.Fatalf("kept %v, want the unrelated request, the one approved again, and the readied change's", names)
	}
	for name, gone := range map[string]bool{
		change.RequestName(draftID): true, "topic": true, "unrelated": false, "moved": false, "readied": false,
	} {
		if _, there := gitTry(s.repo, "rev-parse", "--verify", "--quiet", ref(name)); there == gone {
			t.Errorf("the request %s: deleted %v, want %v", name, !there, gone)
		}
	}
	memo, _ := gitTry(s.repo, "cat-file", "blob", outcomePrefix+"/main/topic")
	if fields := strings.Fields(memo); len(fields) != 4 || fields[3] != string(voidedWithdrawn) {
		t.Errorf("the voided request's outcome record is %q", memo)
	}
}

func recordJSON(t *testing.T, id, state, head string) string {
	t.Helper()
	data, err := change.Encode(&change.Record{
		Schema: change.Schema, ChangeID: id, Target: "main", Owner: "owner", State: state, Seq: 1, Active: 1,
		Versions: []change.Version{{N: 1, Head: change.HeadPrefix + head, Title: "a version", Branch: "topic", Uploader: "owner"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A record past a bound is not read, so it names no head.
func TestARecordPastItsBoundsIsNotRead(t *testing.T) {
	s := newScratchRepo(t)
	head := s.commit("head", s.git("rev-parse", "HEAD"))
	text := recordJSON(t, draftID, change.Draft, head)
	s.recordText(change.Ref("main", draftID), text+strings.Repeat(" ", change.MaxRecordBlob), head)
	in := &integrator{repo: s.repo}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.byHead[head]) != 0 || len(idx.unreadable) != 1 || !strings.Contains(idx.unreadable[0].why, "more than the 65536") {
		t.Fatalf("an oversized record read as %v, unreadable %q", idx.byHead, idx.unreadable)
	}
}

func TestARequestWhoseCommitsHoldAMergeIsRefused(t *testing.T) {
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	side := s.commit("side", base)
	line := s.commit("line", base)
	merged := s.commit("merged", line, side)
	above := s.commit("above", merged)
	// main itself merged once, below where the change begins.
	tip := s.commit("main merged", base, side)
	onTip := s.commit("on the tip", tip)

	in := &integrator{repo: s.repo}
	for _, c := range []struct {
		name, head string
		refused    bool
	}{
		{"a line of commits", line, false},
		{"a merge at the head", merged, true},
		{"a merge below the head", above, true},
		{"a line above a merge main already holds", onTip, false},
	} {
		r := request{change: "topic", target: "refs/heads/main", head: c.head}
		outcome, why, err := in.mergeRefusal(r, tip)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if refused := outcome == refusedMerge; refused != c.refused {
			t.Errorf("%s: refused %v (%s), want %v", c.name, refused, why, c.refused)
		}
	}
}

// A refusal is decided again on every pass, so its memo never stands as
// the answer: a draft readied is judged on the next pass though its head,
// the tip and the evidence have not moved.
func TestARefusalIsNeverTheStandingAnswer(t *testing.T) {
	in := &integrator{}
	memo := "h t e"
	for outcome, stands := range map[string]bool{
		"land": true, "stale": true, "reject": true,
		string(refusedNotReady): false, string(refusedMerge): false,
	} {
		if got := in.answered(memo+" "+outcome, memo); got != stands {
			t.Errorf("a %s memo stands: %v, want %v", outcome, got, stands)
		}
	}
	if in.answered("other t e land", memo) {
		t.Errorf("a memo about another head stands")
	}
}

func devNull(t *testing.T) *os.File {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
