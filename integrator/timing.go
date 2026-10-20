package main

// How long each decision took, said in the integrator's own output.
//
// Every decision the integrator reports also gets one timing line: which
// request, what was decided, how long the whole decision took, and how
// that time divides among the stages it went through. The line is JSON
// after a fixed label, so it can be read out of the journal without
// parsing the rest. It is output and nothing else: no ref, record or event
// carries it, and nothing the integrator decides reads it.

import (
	"encoding/json"
	"fmt"
	"time"
)

// The stages a decision is timed in. A stage a decision did not reach is
// absent from its line.
const (
	stageRefusal  = "refusal"  // the record and merge rules, before judging and again before landing
	stageCheckout = "checkout" // the worktree of the target's tip
	stageEvidence = "evidence" // the head's tree digest and verifying its notes
	stagePolicy   = "policy"   // composing the policy and deriving the required checks
	stageApply    = "apply"    // merging the delta onto the tip
	stageClosures = "closures" // recomputing input closures on a moved tree
	stageSign     = "sign"     // composing and signing the transfer statement
	stageLand     = "land"     // the ref write that moves the target
	stageRecord   = "record"   // storing the evidence and consuming the request
	stagePublish  = "publish"  // the bus events
)

// stopwatch times one decision, as a whole and by stage. A nil stopwatch
// times nothing, so a caller with no decision to time can pass none.
type stopwatch struct {
	began  time.Time
	stages map[string]time.Duration
}

func newStopwatch() *stopwatch {
	return &stopwatch{began: time.Now(), stages: map[string]time.Duration{}}
}

// stage starts timing a stage and returns the function that stops it. A
// stage timed more than once adds up.
func (s *stopwatch) stage(name string) func() {
	if s == nil {
		return func() {}
	}
	began := time.Now()
	return func() { s.stages[name] += time.Since(began) }
}

// decisionTiming is the timing line's JSON.
type decisionTiming struct {
	Time       string           `json:"time"` // when the decision began, UTC
	Project    string           `json:"project"`
	Request    string           `json:"request"`
	Change     string           `json:"change"`
	Head       string           `json:"head"`
	Target     string           `json:"target"`
	Tip        string           `json:"tip"`
	Outcome    string           `json:"outcome"`
	DurationMS int64            `json:"duration_ms"`
	StagesMS   map[string]int64 `json:"stages_ms"`
}

// reportTiming prints the timing line for one decision about a request.
func (in *integrator) reportTiming(r request, tip, outcome string, s *stopwatch) {
	t := decisionTiming{
		Time:       s.began.UTC().Format("2006-01-02T15:04:05.000Z"),
		Project:    in.project,
		Request:    r.ref,
		Change:     r.change,
		Head:       r.head,
		Target:     r.target,
		Tip:        tip,
		Outcome:    outcome,
		DurationMS: time.Since(s.began).Milliseconds(),
		StagesMS:   map[string]int64{},
	}
	for name, d := range s.stages {
		t.StagesMS[name] = d.Milliseconds()
	}
	line, err := json.Marshal(t)
	if err != nil {
		return
	}
	fmt.Fprintf(in.out, "  timing   %s\n", line)
}
