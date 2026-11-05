package main

// What the check worker says about its own passes, with --telemetry DIR.
//
// DIR is the worker's diagnostic directory, apart from its state
// directory and its key: the host makes it group-readable by the
// collector, and the worker writes nothing else there. It holds two
// things:
//
//   - stream/, the telemetry stream (../telemetry) of the worker's passes
//     and of the attest runs a pass starts, whose progress attest records
//     itself;
//   - state.json, the worker's state now: the pass running and what it is
//     doing, each batch it runs, the last attempts and how each ended, and
//     every failure waiting out its backoff, with the change, version,
//     head and tree it was about and why it failed.
//
// Batches run at once, so nothing a record says about a batch or a push
// comes from what else the pass is doing: each names its own project,
// change, version, head and tree (identity), and a batch's records name
// its attest run too. The pass's own fields in state.json describe the
// change it reads during a scan, and otherwise its newest running batch;
// batches lists every running batch.
//
// failures.json in the state directory stays what decides a backoff, in
// its own format; state.json only describes it. A pass that starts and
// finds state.json naming a pass that never ended records that pass as
// interrupted, and each batch it was running: a pass that is killed
// cannot say so itself, so this is an inference, and is marked as one.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"the-valley/telemetry"
)

type workerTelemetry struct {
	dir   string
	tel   *telemetry.Emitter
	state workerState
}

type workerState struct {
	V        int            `json:"v"`
	Updated  string         `json:"updated"`
	Pass     *passState     `json:"pass"`
	Recent   []attempt      `json:"recent"`
	Failures []failureState `json:"failures"`
}

type passState struct {
	Run     string   `json:"run"`
	PID     int      `json:"pid"`
	Exe     string   `json:"exe,omitempty"`
	Version string   `json:"version,omitempty"`
	Started string   `json:"started"`
	Phase   string   `json:"phase"`
	Project string   `json:"project,omitempty"`
	Change  string   `json:"change,omitempty"`
	CV      int      `json:"change_version,omitempty"`
	Head    string   `json:"head,omitempty"`
	Tree    string   `json:"tree,omitempty"`
	Checks  []string `json:"checks,omitempty"`
	// Batches is every batch running, oldest first, each with the result
	// running.
	Batches []attempt `json:"batches,omitempty"`
}

type attempt struct {
	Kind     string   `json:"kind"` // pass, batch or push
	Run      string   `json:"run"`
	Project  string   `json:"project,omitempty"`
	Change   string   `json:"change,omitempty"`
	CV       int      `json:"change_version,omitempty"`
	Head     string   `json:"head,omitempty"`
	Tree     string   `json:"tree,omitempty"`
	Checks   []string `json:"checks,omitempty"`
	Started  string   `json:"started,omitempty"`
	Ended    string   `json:"ended,omitempty"`
	Result   string   `json:"result"` // passed, failed, cancelled or interrupted, or running in batches
	Reason   string   `json:"reason,omitempty"`
	Inferred bool     `json:"inferred,omitempty"`
	// AttestRun is a batch's attest run, which names its progress records.
	AttestRun string `json:"attest_run,omitempty"`
}

// identity is what a batch or a push is about.
type identity struct {
	project, change string
	cv              int
	head, tree      string
}

func (id identity) record() telemetry.Record {
	return telemetry.Record{Project: id.project, Change: id.change, Version: id.cv, Head: id.head, Tree: id.tree}
}

type failureState struct {
	Project  string `json:"project"`
	Key      string `json:"key"`
	Failures int    `json:"failures"`
	Retry    string `json:"retry"`
	FailedAt string `json:"failed_at,omitempty"`
	Change   string `json:"change,omitempty"`
	CV       int    `json:"change_version,omitempty"`
	Head     string `json:"head,omitempty"`
	Tree     string `json:"tree,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

const recentAttempts = 20

// openWorkerTelemetry starts a pass's telemetry in dir, or returns nil
// without a dir. The previous pass, if state.json still names it as
// running, is recorded as interrupted.
func openWorkerTelemetry(dir string) *workerTelemetry {
	if dir == "" {
		return nil
	}
	t := &workerTelemetry{dir: dir}
	if data, err := os.ReadFile(t.statePath()); err == nil {
		json.Unmarshal(data, &t.state)
	}
	self := telemetry.Self("valley-worker", os.Getenv("VALLEY_VERSION"))
	t.tel = telemetry.NewEmitter(filepath.Join(dir, "stream"), self, "")
	if prev := t.state.Pass; prev != nil {
		reason := fmt.Sprintf("pass %s (pid %d) recorded no end: it was killed, or the host stopped, during its %s", prev.Run, prev.PID, prev.Phase)
		t.remember(attempt{Kind: "pass", Run: prev.Run, Project: prev.Project, Change: prev.Change, CV: prev.CV,
			Head: prev.Head, Tree: prev.Tree, Checks: prev.Checks, Started: prev.Started, Result: "interrupted", Reason: reason, Inferred: true})
		var runs []string
		for _, b := range prev.Batches {
			b.Result, b.Inferred = "interrupted", true
			b.Reason = fmt.Sprintf("batch %s of pass %s (pid %d) recorded no end: it was killed, or the host stopped, while it ran", b.AttestRun, prev.Run, prev.PID)
			t.remember(b)
			runs = append(runs, b.AttestRun)
		}
		t.tel.Emit("pass.interrupted", telemetry.Record{Project: prev.Project, Change: prev.Change, Version: prev.CV, Head: prev.Head},
			map[string]any{"previous_run": prev.Run, "previous_pid": prev.PID, "phase": prev.Phase, "inferred": true, "batches": runs})
	}
	t.state.V = 1
	t.state.Pass = &passState{Run: t.tel.Run(), PID: self.PID, Exe: self.Exe, Version: self.Version,
		Started: telemetry.Stamp(time.Now()), Phase: "reading"}
	t.save()
	t.tel.Emit("pass.start", telemetry.Record{}, nil)
	return t
}

func (t *workerTelemetry) statePath() string { return filepath.Join(t.dir, "state.json") }

func (t *workerTelemetry) stream() string { return filepath.Join(t.dir, "stream") }

func (t *workerTelemetry) remember(a attempt) {
	t.state.Recent = append([]attempt{a}, t.state.Recent...)
	if len(t.state.Recent) > recentAttempts {
		t.state.Recent = t.state.Recent[:recentAttempts]
	}
}

// reading says which change a scan is reading now. The pass says it saw
// the change only the first time it reads the change's version.
func (t *workerTelemetry) reading(project string, c jsonChange, v jsonVersion, first bool) {
	if t == nil {
		return
	}
	p := t.state.Pass
	p.Phase, p.Project, p.Change, p.CV, p.Head, p.Tree, p.Checks = "deriving", project, c.ChangeID, v.N, v.Head, "", nil
	if first {
		t.tel.Emit("change.seen", telemetry.Record{Project: project, Change: c.ChangeID, Version: v.N, Head: v.Head},
			map[string]any{"handle": c.Handle})
	}
	t.save()
}

// settle says what the pass is doing between scans: building, as its
// newest running batch, or reading.
func (t *workerTelemetry) settle() {
	if t == nil {
		return
	}
	p := t.state.Pass
	if len(p.Batches) == 0 {
		p.Phase = "reading"
	} else {
		b := p.Batches[len(p.Batches)-1]
		p.Phase, p.Project, p.Change, p.CV, p.Head, p.Tree, p.Checks = "build", b.Project, b.Change, b.CV, b.Head, b.Tree, b.Checks
	}
	t.save()
}

// begin says the pass is building or publishing for id, and returns the
// attempt it makes. A batch names its attest run.
func (t *workerTelemetry) begin(kind string, id identity, checks []string, attestRun string, data map[string]any) *attempt {
	if t == nil {
		return nil
	}
	p := t.state.Pass
	p.Phase, p.Project, p.Change, p.CV, p.Head, p.Tree, p.Checks = kind, id.project, id.change, id.cv, id.head, id.tree, checks
	a := &attempt{Kind: kind, Run: t.tel.Run(), Project: id.project, Change: id.change, CV: id.cv, Head: id.head, Tree: id.tree,
		Checks: checks, Started: telemetry.Stamp(time.Now()), AttestRun: attestRun}
	if kind == "build" {
		running := *a
		running.Result = "running"
		p.Batches = append(p.Batches, running)
	}
	if data == nil {
		data = map[string]any{}
	}
	data["checks"] = checks
	t.tel.Emit(map[string]string{"build": "batch.start", "push": "publish.start"}[kind], id.record(), data)
	t.save()
	return a
}

// end records how an attempt ended, as result: passed, failed, or
// cancelled when the pass stopped a batch, with the reason for either of
// the last two, and its backoff when it failed. A batch's records name its
// attest run, so each attempt pairs with its start from its own records.
func (t *workerTelemetry) end(a *attempt, result, reason string, key string, f *backoff) {
	if t == nil || a == nil {
		return
	}
	id := identity{project: a.Project, change: a.Change, cv: a.CV, head: a.Head, tree: a.Tree}
	a.Ended, a.Result, a.Reason = telemetry.Stamp(time.Now()), result, reason
	data := map[string]any{"result": result}
	if a.AttestRun != "" {
		data["attest_run"] = a.AttestRun
	}
	if reason != "" {
		data["reason"] = reason
	}
	t.remember(*a)
	if a.Kind == "build" {
		p := t.state.Pass
		for i, b := range p.Batches {
			if b.AttestRun == a.AttestRun {
				p.Batches = append(p.Batches[:i:i], p.Batches[i+1:]...)
				break
			}
		}
	}
	t.tel.Emit(map[string]string{"build": "batch.end", "push": "publish.end"}[a.Kind], id.record(), data)
	if f != nil {
		backoff := map[string]any{"key": key, "failures": f.Failures, "retry": telemetry.Stamp(f.Retry), "reason": reason}
		if a.AttestRun != "" {
			backoff["attest_run"] = a.AttestRun
		}
		t.tel.Emit("backoff", id.record(), backoff)
		t.failure(failureState{Project: a.Project, Key: key, Failures: f.Failures, Retry: telemetry.Stamp(f.Retry),
			FailedAt: a.Ended, Change: a.Change, CV: a.CV, Head: a.Head, Tree: a.Tree, Reason: reason})
	}
	t.save()
}

// failure keeps what a failure waiting out its backoff was about.
func (t *workerTelemetry) failure(f failureState) {
	for i, old := range t.state.Failures {
		if old.Project == f.Project && old.Key == f.Key {
			t.state.Failures[i] = f
			return
		}
	}
	t.state.Failures = append(t.state.Failures, f)
}

// capacity says a batch's evaluation is estimated at more than the whole
// evaluation budget, so it evaluates alone.
func (t *workerTelemetry) capacity(id identity, estimate, budget int64, source string) {
	if t == nil {
		return
	}
	t.tel.Emit("capacity", id.record(), map[string]any{"eval_estimate_bytes": estimate, "eval_budget_bytes": budget, "budget_from": source})
}

// finish ends the pass, and keeps only the failures the failure record
// still holds, with their waits as it holds them.
func (t *workerTelemetry) finish(r *failureRecord, troubles int, err error) {
	if t == nil {
		return
	}
	var kept []failureState
	for project, failures := range r.Projects {
		for key, b := range failures {
			f := failureState{Project: project, Key: key}
			for _, old := range t.state.Failures {
				if old.Project == project && old.Key == key {
					f = old
				}
			}
			f.Failures, f.Retry = b.Failures, telemetry.Stamp(b.Retry)
			kept = append(kept, f)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Project+kept[i].Key < kept[j].Project+kept[j].Key })
	t.state.Failures = kept
	p := t.state.Pass
	result := "passed"
	reason := ""
	if err != nil {
		result, reason = "failed", err.Error()
	}
	t.remember(attempt{Kind: "pass", Run: p.Run, Started: p.Started, Ended: telemetry.Stamp(time.Now()), Result: result, Reason: reason})
	t.state.Pass = nil
	t.tel.Emit("pass.end", telemetry.Record{}, map[string]any{"result": result, "troubles": troubles, "waiting": len(kept)})
	t.save()
}

// save writes state.json whole, through a file renamed into place,
// readable by the collector's group. A state that cannot be written is
// dropped: it describes the pass and decides nothing.
func (t *workerTelemetry) save() {
	t.state.Updated = telemetry.Stamp(time.Now())
	data, err := json.MarshalIndent(t.state, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(t.dir, ".state-*")
	if err != nil {
		return
	}
	_, err = tmp.Write(append(data, '\n'))
	if err == nil {
		err = tmp.Chmod(0o640)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), t.statePath())
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
}

// failedChecks names the checks an attest run recorded as not passing in
// the stream at dir, among the records after the one numbered after. It
// reads files and nothing else, so a batch's goroutine calls it. Other
// batches' records come between a run's own, so it reads to the end.
func failedChecks(dir, run string, after int64) []string {
	if dir == "" {
		return nil
	}
	st := &telemetry.Stream{Dir: dir}
	var failed []string
	for pages := 0; pages < 100; pages++ {
		recs, _, err := st.Read(after, 1000, 1<<20)
		if err != nil || len(recs) == 0 {
			break
		}
		for _, r := range recs {
			if r.Run == run && r.Type == "check.result" && r.Data["result"] != "passed" {
				failed = append(failed, fmt.Sprintf("%s %v", r.Check, r.Data["result"]))
			}
		}
		after = recs[len(recs)-1].Seq
	}
	return failed
}

// streamEnd is the number of the stream's newest record.
func (t *workerTelemetry) streamEnd() int64 {
	if t == nil {
		return 0
	}
	rng, _ := (&telemetry.Stream{Dir: t.stream()}).Range()
	return rng.Last
}
