package main

// What the change records refuse, read from a real repository: a request
// named for a change lands only while that change is ready at the
// request's head, a request of any name is held back by a record that is
// not ready at its head, and a request whose commits hold a merge is
// refused. The records are written by hand, the way anyone holding a key
// could write one where no hook guards the repository.

import (
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
	} {
		r := request{ref: requestPrefix + "/main/" + c.change, change: c.change, target: "refs/heads/main", head: c.head}
		for _, reread := range []bool{false, true} {
			outcome, why, err := in.recordRefusal(r, idx, reread)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if refused := outcome != ""; refused != c.refused || (refused && outcome != refusedNotReady) {
				t.Errorf("%s (reread %v): refused %q (%s), want refused %v", c.name, reread, outcome, why, c.refused)
			}
			if !strings.Contains(why, c.says) {
				t.Errorf("%s (reread %v): refused saying %q, without %q", c.name, reread, why, c.says)
			}
		}
	}

	// Read again, a record that changed since the index was read is read
	// as it is now: the draft readied lets its head through, and the ready
	// change unreadied holds its heads back.
	s.record(draftID, change.Ready, drafted)
	s.record(readyID, change.Draft, v1, v2)
	r := request{change: "topic", target: "refs/heads/main", head: drafted}
	if outcome, why, _ := in.recordRefusal(r, idx, true); outcome != "" {
		t.Errorf("a draft readied since the index was read still refuses: %s", why)
	}
	r = request{change: "topic", target: "refs/heads/main", head: v1}
	if outcome, _, _ := in.recordRefusal(r, idx, true); outcome != refusedNotReady {
		t.Errorf("a change unreadied since the index was read refuses nothing")
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
	if len(idx.byHead[head]) != 0 || len(idx.unreadable) != 1 || !strings.Contains(idx.unreadable[0], "more than the 65536") {
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
