package main

// A decision the integrator reports is followed by its timing line, and a
// decision it does not report again is not timed again. A refusal is the
// decision used here: it is decided on every pass, reported only when it
// is new, and needs no policy or evidence to reach.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/change"
)

// refusedRequest files a request for a draft, which a pass refuses.
func refusedRequest(t *testing.T) (*scratchRepo, request) {
	t.Helper()
	s := newScratchRepo(t)
	base := s.git("rev-parse", "HEAD")
	drafted := s.commit("drafted", base)
	s.record(draftID, change.Draft, drafted)
	r := request{
		ref:    requestPrefix + "/main/" + change.RequestName(draftID),
		change: change.RequestName(draftID),
		target: "refs/heads/main",
		head:   drafted,
	}
	s.git("update-ref", r.ref, drafted)
	return s, r
}

// timingWatch is where a test's timing lines go. Beside each line it keeps
// what the request's outcome record said when the line was written.
type timingWatch struct {
	in      *integrator
	r       request
	lines   []string
	records []string
}

func (w *timingWatch) Write(p []byte) (int, error) {
	record, _ := w.in.readMemo(w.r)
	w.lines = append(w.lines, strings.TrimSuffix(string(p), "\n"))
	w.records = append(w.records, record)
	return len(p), nil
}

func TestADecisionIsFollowedByItsTimingLine(t *testing.T) {
	s, r := refusedRequest(t)
	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in := &integrator{repo: s.repo, project: "project", out: out}
	watch := &timingWatch{in: in, r: r}
	in.timings = watch
	pass := func() {
		t.Helper()
		idx, err := in.readRecords()
		if err != nil {
			t.Fatal(err)
		}
		if err := in.handle(r, idx, inputs{digest: "i"}); err != nil {
			t.Fatal(err)
		}
	}

	pass()
	if len(watch.lines) != 1 {
		t.Fatalf("one refusal printed %d timing lines, want 1", len(watch.lines))
	}
	line, ok := strings.CutPrefix(watch.lines[0], "  timing   ")
	if !ok {
		t.Fatalf("the timing line has no label: %q", watch.lines[0])
	}
	var got decisionTiming
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("the timing line is not JSON: %v: %s", err, line)
	}
	if got.Project != "project" || got.Request != r.ref || got.Change != r.change || got.Head != r.head ||
		got.Target != "refs/heads/main" || got.Tip != s.git("rev-parse", "HEAD") || got.Outcome != string(refusedNotReady) {
		t.Errorf("the timing line says %+v", got)
	}
	if _, ok := got.StagesMS[stageRefusal]; !ok || len(got.StagesMS) != 1 {
		t.Errorf("a refusal is timed in the stages %v, want the refusal stage alone", got.StagesMS)
	}
	if got.DurationMS < got.StagesMS[stageRefusal] {
		t.Errorf("the decision took %dms, less than its refusal stage's %dms", got.DurationMS, got.StagesMS[stageRefusal])
	}
	// The outcome was recorded before its timing line was printed.
	if !strings.HasSuffix(watch.records[0], " "+string(refusedNotReady)) {
		t.Errorf("when the timing line was printed, the outcome record said %q", watch.records[0])
	}

	// The same refusal on the next pass is not reported, and not timed.
	if pass(); len(watch.lines) != 1 {
		t.Fatalf("a refusal repeated printed a timing line again: %q", watch.lines)
	}
}

// A timing line nobody reads holds no decision up. Here the integrator's
// timing output is a pipe that is full and never read, so the line can
// never be written: the outcome is recorded all the same, and the pass
// goes on once the line's deadline has passed.
func TestATimingLineNobodyReadsHoldsNothingUp(t *testing.T) {
	s, r := refusedRequest(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	pw.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	for err == nil {
		_, err = pw.Write(make([]byte, 1<<16))
	}
	pw.SetWriteDeadline(time.Time{})

	in := &integrator{repo: s.repo, project: "project", out: devNull(t), timings: newDroppable(pw)}
	idx, err := in.readRecords()
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if err := in.handle(r, idx, inputs{digest: "i"}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > timingDeadline+5*time.Second {
		t.Errorf("a timing line nobody reads held the decision for %v", took)
	}
	if record, _ := in.readMemo(r); !strings.HasSuffix(record, " "+string(refusedNotReady)) {
		t.Errorf("behind a timing line nobody reads, the outcome record says %q", record)
	}
}

// A request is first seen once at each head, by the pass that first lists
// it there. A moved head is seen anew, a request no longer listed is
// forgotten, and a new process remembers nothing of the one before it.
func TestARequestIsFirstSeenOnceAtEachHead(t *testing.T) {
	a := request{ref: requestPrefix + "/main/a", head: "a1"}
	b := request{ref: requestPrefix + "/main/b", head: "b1"}
	s := newSightings()
	pass := func(reason passReason, requests ...request) {
		t.Helper()
		s.start(reason)
		s.list(requests)
		time.Sleep(5 * time.Millisecond)
	}
	firstSeen := func(r request, wantPass int, wantAtStart bool) observation {
		t.Helper()
		o := s.of(r, time.Now())
		if o.FirstSeenPass != wantPass || o.FirstSeenAtStart != wantAtStart || o.Process != s.process || o.Pass != s.passes {
			t.Fatalf("%s at %s in pass %d: %+v, want first seen in pass %d", r.ref, r.head, s.passes, o, wantPass)
		}
		if o.SeenToBeganMS < o.ListedToBeganMS {
			t.Errorf("%s was first seen after its pass listed it: %+v", r.ref, o)
		}
		return o
	}

	pass(reasonStartup, a)
	firstSeen(a, 1, true)
	if o := s.of(b, time.Now()); o != (observation{}) {
		t.Errorf("a request the pass did not list has an observation: %+v", o)
	}
	pass(reasonDue, a, b)
	seen := firstSeen(a, 1, true).FirstSeen
	if o := firstSeen(b, 2, false); o.PassReason != string(reasonDue) || o.FirstSeen == seen {
		t.Errorf("b, first listed by the due pass, says %+v", o)
	}
	moved := request{ref: b.ref, head: "b2"}
	pass(reasonTimer, a, moved)
	firstSeen(moved, 3, false)
	if o := s.of(b, time.Now()); o != (observation{}) {
		t.Errorf("a decision about b's old head has an observation: %+v", o)
	}
	pass(reasonFollowUp, a)
	if len(s.seen) != 1 {
		t.Errorf("after b was no longer listed, %d requests are remembered", len(s.seen))
	}
	pass(reasonDue, a, moved)
	firstSeen(moved, 5, false)

	restarted := newSightings()
	if restarted.process == s.process {
		t.Error("two starts share a process")
	}
	s = restarted
	pass(reasonStartup, a)
	firstSeen(a, 1, true)
}
