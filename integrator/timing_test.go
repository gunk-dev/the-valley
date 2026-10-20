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

	"the-valley/change"
)

func TestADecisionIsFollowedByItsTimingLine(t *testing.T) {
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

	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in := &integrator{repo: s.repo, project: "project", out: out}
	pass := func() []string {
		t.Helper()
		idx, err := in.readRecords()
		if err != nil {
			t.Fatal(err)
		}
		if err := in.handle(r, idx); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out.Name())
		if err != nil {
			t.Fatal(err)
		}
		var timings []string
		for _, line := range strings.Split(string(data), "\n") {
			if rest, ok := strings.CutPrefix(line, "  timing   "); ok {
				timings = append(timings, rest)
			}
		}
		return timings
	}

	timings := pass()
	if len(timings) != 1 {
		t.Fatalf("one refusal printed %d timing lines, want 1", len(timings))
	}
	var got decisionTiming
	if err := json.Unmarshal([]byte(timings[0]), &got); err != nil {
		t.Fatalf("the timing line is not JSON: %v: %s", err, timings[0])
	}
	if got.Project != "project" || got.Request != r.ref || got.Change != r.change || got.Head != drafted ||
		got.Target != "refs/heads/main" || got.Tip != base || got.Outcome != string(refusedNotReady) {
		t.Errorf("the timing line says %+v", got)
	}
	if _, ok := got.StagesMS[stageRefusal]; !ok || len(got.StagesMS) != 1 {
		t.Errorf("a refusal is timed in the stages %v, want the refusal stage alone", got.StagesMS)
	}
	if got.DurationMS < got.StagesMS[stageRefusal] {
		t.Errorf("the decision took %dms, less than its refusal stage's %dms", got.DurationMS, got.StagesMS[stageRefusal])
	}

	// The same refusal on the next pass is not reported, and not timed.
	if timings := pass(); len(timings) != 1 {
		t.Fatalf("a refusal repeated printed a timing line again: %q", timings)
	}
}
