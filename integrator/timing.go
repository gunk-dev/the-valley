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
// Nor may it stand in the way of a decision. The line is printed after the
// attempt to record the outcome, whether or not that attempt succeeds. The
// caller waits at most timingDeadline for the write, and a write still
// blocked then may finish later.
//
// Where the host names a telemetry stream in VALLEY_TELEMETRY_DIR, the
// same timing is also stored there as a decision record (../telemetry),
// which the collector serves. The record is the producer's own: the
// collector never reads one out of the printed line. It is stored after
// the attempt to record the outcome too, and dropped when it cannot be
// stored within telemetry.EmitDeadline.
//
// A decision begins when the integrator starts judging a request. Its line
// also says when this process first saw the request at its head, and which
// pass of which process made the decision (sightings). That is when this
// integrator process first listed the request ref. It is neither the
// approval's time nor the time the host accepted the ref, and nothing
// before the process started is known to it.

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
	observation
}

// observation says when the process that made a decision first saw its
// request, and which of its passes made the decision. A decision made
// outside a pass, as a test makes one, has it empty.
type observation struct {
	Process    string `json:"process"`     // this start of the integrator, apart from every other
	Pass       int    `json:"pass"`        // the deciding pass, counted from 1 in its process
	PassReason string `json:"pass_reason"` // why that pass started (passReason)
	PassListed string `json:"pass_listed"` // when that pass listed the requests, UTC
	// When this process first listed the request at its head, UTC, and in
	// which pass. A request first listed by the process's first pass may
	// have waited from before the process started.
	FirstSeen        string `json:"first_seen"`
	FirstSeenPass    int    `json:"first_seen_pass"`
	FirstSeenAtStart bool   `json:"first_seen_at_start"`
	SeenToBeganMS    int64  `json:"seen_to_began_ms"`   // from first_seen to the decision's start
	ListedToBeganMS  int64  `json:"listed_to_began_ms"` // from pass_listed to the decision's start
}

// timing composes the timing line for one decision about a request, and
// returns what prints it. The line is composed as the decision is made and
// printed after the attempt to record its outcome, so recording is not part
// of the time it reports, and printing it cannot keep the outcome from
// being recorded.
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
	t.observation = in.sightings.of(r, s.began)
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
	if o := t.observation; o.FirstSeen != "" {
		data["process"], data["pass"], data["pass_reason"], data["pass_listed"] = o.Process, o.Pass, o.PassReason, o.PassListed
		data["first_seen"], data["first_seen_pass"], data["first_seen_at_start"] = o.FirstSeen, o.FirstSeenPass, o.FirstSeenAtStart
		data["seen_to_began_ms"], data["listed_to_began_ms"] = o.SeenToBeganMS, o.ListedToBeganMS
	}
	return func() {
		e := telemetry.NewEmitter(dir, telemetry.Self("integrator", ""), "")
		e.Emit("decision", telemetry.Record{Project: t.Project, Change: t.Change, Head: t.Head}, data)
	}
}

// sightings is what one integrator process remembers of when it first
// saw each request. Like the rest of this file it is output only: nothing
// the integrator decides reads it.
//
// A pass sees a request when it lists it. A request whose head moves is
// seen again, as a new request. Only this process's own sightings are
// kept, so a request that was waiting when the process started is first
// seen by its first pass, and its decisions say so. A request a pass no
// longer lists has landed, been voided or been deleted, and is forgotten
// after the next successful listing that omits it. A failed listing leaves
// the table as it was. A nil sightings sees nothing.
type sightings struct {
	process string // this start of the integrator
	passes  int    // the passes this process has started
	reason  passReason
	listed  time.Time // when the pass in progress listed the requests
	seen    map[string]sighting
}

// sighting is when a request was first seen at its head.
type sighting struct {
	head string
	at   time.Time
	pass int
}

func newSightings() *sightings {
	return &sightings{process: telemetry.NewRunID(), seen: map[string]sighting{}}
}

// start counts a pass that starts for reason.
func (s *sightings) start(reason passReason) {
	if s == nil {
		return
	}
	s.passes++
	s.reason = reason
	s.listed = time.Time{}
}

// list remembers the requests the pass in progress listed: those it has
// not seen at their heads are seen now, and those it no longer lists are
// forgotten.
func (s *sightings) list(requests []request) {
	if s == nil {
		return
	}
	s.listed = time.Now()
	listed := make(map[string]bool, len(requests))
	for _, r := range requests {
		listed[r.ref] = true
		if was, ok := s.seen[r.ref]; !ok || was.head != r.head {
			s.seen[r.ref] = sighting{head: r.head, at: s.listed, pass: s.passes}
		}
	}
	for ref := range s.seen {
		if !listed[ref] {
			delete(s.seen, ref)
		}
	}
}

// of is the observation of a decision about r that began at began, or
// none where the pass in progress did not list r.
func (s *sightings) of(r request, began time.Time) observation {
	if s == nil || s.listed.IsZero() {
		return observation{}
	}
	seen, ok := s.seen[r.ref]
	if !ok || seen.head != r.head {
		return observation{}
	}
	return observation{
		Process:          s.process,
		Pass:             s.passes,
		PassReason:       string(s.reason),
		PassListed:       telemetry.Stamp(s.listed),
		FirstSeen:        telemetry.Stamp(seen.at),
		FirstSeenPass:    seen.pass,
		FirstSeenAtStart: seen.pass == 1,
		SeenToBeganMS:    began.Sub(seen.at).Milliseconds(),
		ListedToBeganMS:  began.Sub(s.listed).Milliseconds(),
	}
}

// timingDeadline is how long printing one timing line may take.
const timingDeadline = time.Second

// errDropped is what a timing line that was dropped says.
var errDropped = errors.New("dropped: not written within " + timingDeadline.String())

// droppable writes timing lines, which may be lost. Each write is made
// apart from the caller, which waits for it at most timingDeadline. A
// write that fails or takes longer is dropped, though a slow one is not
// cancelled and may finish later. While one is still stuck,
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
