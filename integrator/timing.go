package main

// How long each decision took, said in the integrator's own output.
//
// Every decision the integrator reports also gets one timing line: which
// request, what was decided, how long the whole decision took, and how
// that time divides among the stages it went through. The line is JSON
// after a fixed label, so it can be read out of the journal without
// parsing the rest. It is output and nothing else: no ref, record or event
// carries it, and nothing the integrator decides reads it.
//
// Nor may it stand in the way of a decision. The line is printed only
// after the decision's outcome is recorded, and it is dropped when it
// cannot be written within timingDeadline.
//
// Where the host names a telemetry stream in VALLEY_TELEMETRY_DIR, the
// same timing is also stored there as a decision record (../telemetry),
// which the collector serves. The record is the producer's own: the
// collector never reads one out of the printed line. It is stored after
// the outcome is recorded too, and dropped when it cannot be stored
// within telemetry.EmitDeadline. A decision begins when the integrator
// starts judging a request; when it first saw the request is not
// recorded here.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"the-valley/telemetry"
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
	stageLand     = "land"     // the ref transaction that moves the target and records the landing
	stageRecord   = "record"   // building what the transaction records: evidence trees, landing and outcome records
	stagePublish  = "publish"  // the publisher's queue and the bus events
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

// timing composes the timing line for one decision about a request, and
// returns what prints it. The line is composed as the decision is made and
// printed once its outcome is recorded, so recording is not part of the
// time it reports, and printing it cannot keep the outcome from being
// recorded.
func (in *integrator) timing(r request, tip, outcome string, s *stopwatch) func() {
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
	record := decisionRecord(t, s.began)
	line, err := json.Marshal(t)
	if err != nil || in.timings == nil {
		return record
	}
	return func() {
		fmt.Fprintf(in.timings, "  timing   %s\n", line)
		record()
	}
}

// decisionRecord returns what stores a decision's timing as a telemetry
// record, or does nothing where the host names no stream. Each decision
// is a run of its own.
func decisionRecord(t decisionTiming, began time.Time) func() {
	dir := os.Getenv("VALLEY_TELEMETRY_DIR")
	if dir == "" {
		return func() {}
	}
	data := map[string]any{"request": t.Request, "target": t.Target, "tip": t.Tip, "outcome": t.Outcome,
		"duration_ms": t.DurationMS, "began": telemetry.Stamp(began)}
	for name, ms := range t.StagesMS {
		data["stage_"+name+"_ms"] = ms
	}
	return func() {
		e := telemetry.NewEmitter(dir, telemetry.Self("integrator", ""), "")
		e.Emit("decision", telemetry.Record{Project: t.Project, Change: t.Change, Head: t.Head}, data)
	}
}

// timingDeadline is how long printing one timing line may take.
const timingDeadline = time.Second

// errDropped is what a timing line that was dropped says.
var errDropped = errors.New("dropped: not written within " + timingDeadline.String())

// droppable writes timing lines, which may be lost. Each write is made
// apart from the caller, which waits for it at most timingDeadline. A
// write that fails or takes longer is dropped. While one is still stuck,
// every later one is dropped at once rather than queued behind it.
type droppable struct {
	w    io.Writer
	busy chan struct{} // holds a token while a write is in flight
}

func newDroppable(w io.Writer) *droppable {
	return &droppable{w: w, busy: make(chan struct{}, 1)}
}

func (d *droppable) Write(p []byte) (int, error) {
	select {
	case d.busy <- struct{}{}:
	default:
		return 0, errDropped
	}
	line := bytes.Clone(p)
	done := make(chan error, 1)
	go func() {
		_, err := d.w.Write(line)
		<-d.busy
		done <- err
	}()
	timer := time.NewTimer(timingDeadline)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return 0, err
		}
		return len(p), nil
	case <-timer.C:
		return 0, errDropped
	}
}

// duplicate duplicates the descriptor of standard output, for timing
// lines. Go ends a process whose write to its own standard output fails
// because nobody reads it, as SIGPIPE would. Through a duplicate, the same
// write fails with EPIPE and nothing more. Output that cannot be
// duplicated gets no timing lines.
func duplicate(fd int, name string) io.Writer {
	syscall.ForkLock.RLock()
	dup, err := syscall.Dup(fd)
	if err == nil {
		syscall.CloseOnExec(dup)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return io.Discard
	}
	return os.NewFile(uintptr(dup), name)
}
