package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"the-valley/flow/internal/checksfix"
	"the-valley/flow/internal/event"
	"the-valley/flow/internal/git"
	"the-valley/flow/internal/integration"
	"the-valley/flow/internal/restack"
	"the-valley/flow/internal/run"
)

// TestQueueStateIsWhoseMove: each state is the coordinator's, the
// operator's, flow's or nobody's, and failed checks or a restack conflict
// are flow's only while its dispatcher works on them: a dispatch is due,
// or the run it dispatched is alive or left a new head.
func TestQueueStateIsWhoseMove(t *testing.T) {
	checks := func(state string) *checksfix.View { return &checksfix.View{State: state} }
	conflict := func(state string) *restack.View { return &restack.View{State: state} }
	s := &run.State{ID: "r1"}
	for _, tc := range []struct {
		name      string
		row       runRow
		wantState string
		want      party
	}{
		{"running", runRow{status: "running"}, "running", partyFlow},
		{"working", runRow{status: "working"}, "working", partyFlow},
		{"needs attention", runRow{status: "needs-attention"}, "needs-attention", partyCoordinator},
		{"draft awaiting review", runRow{status: "awaiting-review"}, "awaiting-review", partyCoordinator},
		{"stale", runRow{status: "stale"}, "stale", partyCoordinator},
		{"rejected", runRow{status: "rejected"}, "rejected", partyCoordinator},
		{"failed", runRow{status: "failed"}, "failed", partyCoordinator},
		{"draft", runRow{status: "draft"}, "draft", partyCoordinator},
		{"ready", runRow{status: "awaiting-approval", ci: "pending"}, "awaiting-approval", partyOperator},
		{"pushed branch", runRow{status: "awaiting-integration"}, "awaiting-integration", partyOperator},
		{"uncertain", runRow{status: "uncertain"}, "uncertain", partyOperator},
		{"approved", runRow{status: "approved"}, "approved", partyFlow},
		{"open PR", runRow{status: "pr-created", merge: "pending"}, "pr-created", partyFlow},
		{"PR ready to merge", runRow{status: "pr-created", merge: "ready"}, "ready-to-merge", partyOperator},
		{"PR ready and approved on any of its runs", runRow{status: "pr-created", merge: "ready", approved: true}, "pr-created", partyFlow},
		{"draft PR ready to merge", runRow{status: "pr-created", merge: "ready", draftPR: true}, "pr-created", partyFlow},
		{"tracked GitHub work ready to merge", runRow{status: "tracking", merge: "ready", run: &run.State{ID: "t1", Type: "track", PRURL: strPtr("https://github.com/acme/widget/pull/9")}}, "ready-to-merge", partyOperator},
		{"integrated", runRow{status: "integrated"}, "integrated", partyNone},
		{"older version", runRow{status: "older-version"}, "older-version", partyNone},
		{"checks failed, not flow's", runRow{status: "awaiting-approval", ci: "failing"}, "checks-failed", partyCoordinator},
		{"checks failed, fix due", runRow{status: "awaiting-approval", ci: "failing", checks: checks(checksfix.StateDue)}, "checks-failed", partyFlow},
		{"checks failed, fix run alive", runRow{status: "awaiting-approval", ci: "failing", checks: checks(checksfix.StateDispatched)}, "checks-failed", partyFlow},
		{"checks failed, fix run finished without a new head", runRow{status: "awaiting-approval", ci: "failing", checks: checks(checksfix.StateDispatched), checksDone: true}, "checks-failed", partyCoordinator},
		{"checks failed, fixes spent", runRow{status: "awaiting-approval", ci: "failing", checks: checks(checksfix.StateStopped)}, "checks-failed", partyCoordinator},
		{"checks failed, launch failed", runRow{status: "awaiting-approval", ci: "failing", checks: checks(checksfix.StateRefused)}, "checks-failed", partyCoordinator},
		{"stale for failed checks, fixes spent", runRow{status: "checks-failed", checks: checks(checksfix.StateStopped)}, "checks-failed", partyCoordinator},
		{"conflict, cooling down", runRow{status: "awaiting-approval", restack: conflict(restack.StateCooldown)}, "conflicting", partyFlow},
		{"conflict, resolution run alive", runRow{status: "awaiting-approval", restack: conflict(restack.StateDispatched)}, "conflicting", partyFlow},
		{"conflict, resolution run finished without a new head", runRow{status: "awaiting-approval", restack: conflict(restack.StateDispatched), restackDone: true}, "conflicting", partyCoordinator},
		{"conflict left alone", runRow{status: "awaiting-approval", restack: conflict(restack.StateHeld)}, "conflicting", partyCoordinator},
		{"stale for a conflict, not flow's", runRow{status: "conflicting"}, "conflicting", partyCoordinator},
		{"draft while flow's restack run launches", runRow{status: "draft", restack: conflict(restack.StateLaunching)}, "conflicting", partyFlow},
		{"draft, flow's restack runs spent", runRow{status: "draft", restack: conflict(restack.StateStopped)}, "conflicting", partyCoordinator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.row.run == nil {
				tc.row.run = s
			}
			state, p := queueState(tc.row)
			if state != tc.wantState || p != tc.want {
				t.Errorf("queueState = %s, %d; want %s, %d", state, p, tc.wantState, tc.want)
			}
		})
	}
}

// TestDispatchFinished: a dispatched run has finished once its record reads
// it not running, unless it left a new head, and once cleanup removed its
// record; a run with no record that cleanup didn't remove is still
// launching.
func TestDispatchFinished(t *testing.T) {
	store := run.NewHomeDirStoreFromPath(t.TempDir())
	if err := store.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	same, other := "aaaa", "bbbb"
	for _, s := range []*run.State{{ID: "idle"}, {ID: "pushed-same", PushedSHA: &same}, {ID: "pushed-new", PushedSHA: &other}, {ID: "alive"}, {ID: "removed"}} {
		if err := store.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	idle := CleanupDeps{IsRunActive: func(*run.State) bool { return false }}
	if err := cleanupOneTo(io.Discard, context.Background(), "", store, git.NewExecClient(), "removed", false, idle, isolatedTmux(t)); err != nil {
		t.Fatal(err)
	}
	states, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := clearedOf(store, states)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, len(states))
	for i, s := range states {
		statuses[i] = "exited"
		if s.ID == "alive" {
			statuses[i] = "running"
		}
	}
	for id, want := range map[string]bool{"idle": true, "pushed-same": true, "pushed-new": false, "alive": false, "removed": true, "launching": false, "": false} {
		if got := dispatchFinished(id, same, states, statuses, cleared.IDs); got != want {
			t.Errorf("dispatchFinished(%q) = %t, want %t", id, got, want)
		}
	}
}

// clockEvent is an event at t.
func clockEvent(t time.Time, runID, typ string, data map[string]interface{}) event.Event {
	return event.Event{Timestamp: t.UTC().Format(time.RFC3339), RunID: runID, Type: typ, Data: data}
}

// TestStateClockDatesStates: a state is dated by the latest event that
// enters it, about the run, its change or its PR as the state's scope
// says, and not about another head; with no such event, and for a
// finished state, the age is unknown. A ready change waits from when its
// head was marked ready, not from when its checks passed.
func TestStateClockDatesStates(t *testing.T) {
	base := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	c := newStateClock()
	for _, e := range []event.Event{
		clockEvent(at(10), "run-a", event.AgentChangeDraft, map[string]interface{}{"change_id": "cid"}),
		clockEvent(at(30), "run-b", event.AgentChangeDraft, map[string]interface{}{"change_id": "cid"}),
		clockEvent(at(35), "run-b", event.AgentChangeReady, map[string]interface{}{"change_id": "cid", "head": "h1"}),
		clockEvent(at(36), "nxvp", event.ChangeChecksFailed, map[string]interface{}{"change_id": "cid", "head": "h1"}),
		clockEvent(at(38), "nxvp", event.ChangeChecksPassed, map[string]interface{}{"change_id": "cid"}),
		clockEvent(at(40), "nxvp", event.ChangeStale, map[string]interface{}{"change_id": "cid", "repo": "widget", "head": "h1"}),
		clockEvent(at(50), "run-b", event.AgentNeedsAttention, map[string]interface{}{"change_id": "cid"}),
		clockEvent(at(60), "42", event.PRAwaitingApproval, map[string]interface{}{"pr_number": "42", "key": "acme/widget#42"}),
		clockEvent(at(70), "42", event.PRAwaitingApproval, map[string]interface{}{"pr_number": "42", "key": "acme/gadget#42"}),
	} {
		c.observe(e)
	}
	created := at(0).Format(time.RFC3339)
	dur := int64(5 * time.Minute / time.Millisecond)
	a := &run.State{ID: "run-a", ChangeID: "cid", CreatedAt: created, DurationMS: &dur}
	pr, url := "42", "https://github.com/acme/widget/pull/42"
	p := &run.State{ID: "run-p", ChangeID: "pid", CreatedAt: created, PR: &pr, PRURL: &url}
	for _, tc := range []struct {
		name  string
		s     *run.State
		state string
		head  string
		want  time.Time
	}{
		{"a change's state, by any run of it", a, "awaiting-review", "h1", at(30)},
		{"ready since marked ready, not since checks passed", a, "awaiting-approval", "h1", at(35)},
		{"failed checks at the head they failed", a, "checks-failed", "h1", at(40)},
		{"not by another head's failure", a, "checks-failed", "h2", time.Time{}},
		{"not by another head's readying", a, "awaiting-approval", "h2", time.Time{}},
		{"a change's state, by the change's event", a, "stale", "h1", at(40)},
		{"a run's own state, by its own events only", a, "needs-attention", "h1", time.Time{}},
		{"a PR's state, by its repo's PR only", p, "ready-to-merge", "", at(60)},
		{"no entering event", a, "awaiting-integration", "h1", time.Time{}},
		{"a finished state", a, "integrated", "h1", time.Time{}},
	} {
		if got := c.since(tc.s, tc.state, tc.head); !got.Equal(tc.want) {
			t.Errorf("%s: since = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := (*stateClock)(nil).since(a, "awaiting-review", "h1"); !got.IsZero() {
		t.Errorf("no clock dates %v", got)
	}
}

// TestBuildQueue: the coordinator's items and the operator's, each oldest
// first, a change once under its newest run and a run's own attention as
// its own, with the command that moves each of the coordinator's on and
// none for the operator's, whose checks it shows. A change all of whose
// runs read older-version stands where the valley says. An unknown age
// reads - and null; one ahead of the clock, 0.
func TestBuildQueue(t *testing.T) {
	base := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return base.Add(time.Duration(m) * time.Minute) }
	repo := "widget"
	mk := func(id, change string, m int) *run.State {
		sha := strings.Repeat(id[len(id)-1:], 40)
		return &run.State{ID: id, ChangeID: change, CreatedAt: at(m).Format(time.RFC3339), TargetRepo: &repo,
			Valley: &run.Valley{Repo: "widget", Handle: "h" + change}, PushedSHA: &sha}
	}
	oldDraft, newDraft := mk("run-1", "c1", 0), mk("run-2", "c1", 10)
	salvaged := mk("run-3", "c1", 5)
	ready := mk("run-4", "c2", 20)
	restacked := mk("run-5", "c3", 1)
	running := mk("run-6", "c4", 2)
	pushed := mk("run-7", "c5", 3)
	rows := []runRow{
		{run: oldDraft, status: "older-version"},
		{run: newDraft, status: "awaiting-review", since: at(40), label: "hc1", change: &integration.Change{Repo: "widget", Handle: "hc1", Head: *newDraft.PushedSHA}},
		{run: salvaged, status: "needs-attention", since: at(30), label: "hc1"},
		{run: ready, status: "awaiting-approval", since: at(25), label: "hc2", ci: "pending"},
		{run: restacked, status: "older-version", label: "hc3"},
		{run: running, status: "running", since: at(2)},
		{run: pushed, status: "awaiting-integration", since: at(120), label: "hc5", ci: "-"},
	}
	report := integration.Report{Changes: map[integration.ChangeKey]integration.Result{
		{Repo: "widget", ChangeID: "c3"}: {Verdict: integration.Stale, Change: &integration.Change{Repo: "widget", Handle: "hc3", Head: "f00d"}},
	}}
	q := buildQueue(rows, report, newStateClock())

	var got []string
	for _, it := range q.coordinator {
		got = append(got, it.Run+" "+it.State+" "+it.Next)
	}
	want := []string{
		`run-3 needs-attention valley flow launch --repo widget --resume-from run-3 "<brief>"`,
		"run-2 awaiting-review valley flow ready run-2 --head " + *newDraft.PushedSHA,
		`run-5 stale valley flow launch --repo widget --change run-5 "<brief>"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("coordinator's items:\n%s\nwant (oldest first, the undated last):\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(q.operator) != 2 || q.operator[0].Run != "run-4" || q.operator[0].Checks != "pending" || q.operator[0].Next != "" || q.operator[1].Run != "run-7" {
		t.Errorf("operator's items = %+v, want run-4 awaiting approval, checks pending, no command; then run-7", q.operator)
	}

	var text bytes.Buffer
	q.write(&text, at(100))
	for _, line := range []string{
		"Waiting on the coordinator",
		"◇ widget hc1 · awaiting-review · for 1h · head 2222222",
		"  run: valley flow ready run-2 --head " + *newDraft.PushedSHA,
		"◇ widget hc3 · stale · for - · head f00d",
		"Waiting on the operator",
		"◇ widget hc2 · awaiting-approval · for 1h · head 4444444 · checks pending",
		"◇ widget hc5 · awaiting-integration · for 0s · head 7777777",
	} {
		if !strings.Contains(text.String(), line+"\n") {
			t.Errorf("text queue lacks %q:\n%s", line, text.String())
		}
	}
	var js map[string][]map[string]interface{}
	var buf bytes.Buffer
	if err := q.writeJSON(&buf, at(100)); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buf.Bytes(), &js); err != nil {
		t.Fatalf("queue --json: %v\n%s", err, buf.String())
	}
	if it := js["coordinator"][1]; it["state"] != "awaiting-review" || it["age_seconds"] != float64(3600) || it["since"] != at(40).Format(time.RFC3339) {
		t.Errorf("JSON item = %v", it)
	}
	if it := js["coordinator"][2]; it["since"] != nil || it["age_seconds"] != nil {
		t.Errorf("an undated JSON item = %v, want since and age_seconds null", it)
	}
	if it := js["operator"][1]; it["age_seconds"] != float64(0) {
		t.Errorf("a JSON item ahead of the clock = %v, want age_seconds 0", it)
	}

	buf.Reset()
	if err := (queue{}).writeJSON(&buf, at(0)); err != nil || strings.TrimSpace(buf.String()) != `{"coordinator":[],"operator":[]}` {
		t.Errorf("empty queue --json = %q, %v", buf.String(), err)
	}
	buf.Reset()
	queue{}.write(&buf, at(0))
	if buf.Len() != 0 {
		t.Errorf("empty queue printed %q", buf.String())
	}
}

// TestBuildQueueListsAChangeNoRunSpeaksFor: a change with a run that reads
// older-version stands where the valley lists it unless another run speaks
// for it. A run that exited without pushing speaks for no version; a run at
// work on the change does.
func TestBuildQueueListsAChangeNoRunSpeaksFor(t *testing.T) {
	repo := "widget"
	mk := func(id, change string, pushed bool) *run.State {
		s := &run.State{ID: id, ChangeID: change, CreatedAt: "2026-11-20T09:00:00Z", TargetRepo: &repo, Valley: &run.Valley{Repo: "widget"}}
		if pushed {
			sha := strings.Repeat("a", 40)
			s.PushedSHA = &sha
		}
		return s
	}
	rows := []runRow{
		{run: mk("run-1", "c1", false), status: "exited"},
		{run: mk("run-2", "c1", true), status: "older-version"},
		{run: mk("run-3", "c2", true), status: "older-version"},
		{run: mk("run-4", "c2", false), status: "running"},
	}
	ready := func(head string) integration.Result {
		return integration.Result{Verdict: integration.Ready, Change: &integration.Change{Repo: "widget", Head: head}}
	}
	report := integration.Report{Changes: map[integration.ChangeKey]integration.Result{
		{Repo: "widget", ChangeID: "c1"}: ready("f00d"),
		{Repo: "widget", ChangeID: "c2"}: ready("beef"),
	}}
	q := buildQueue(rows, report, newStateClock())
	if len(q.coordinator) != 0 || len(q.operator) != 1 {
		t.Fatalf("queue = %+v, want c1's active version alone, awaiting approval", q)
	}
	if it := q.operator[0]; it.key != "change:widget/c1" || it.State != "awaiting-approval" || it.Head != "f00d" {
		t.Errorf("operator's item = %+v, want c1 awaiting approval at f00d", it)
	}
}

var waitingDraft = queueItem{party: partyCoordinator, key: "change:widget/cid-b", Run: "run-b", Change: "nxvp", ChangeID: "cid-b", Project: "widget",
	Head: "1a2b3c4d", State: "awaiting-review", Since: time.Now().Add(-time.Hour), Next: "valley flow ready run-b --head 1a2b3c4d"}

var waitingApproval = queueItem{party: partyOperator, key: "change:widget/cid-c", Run: "run-c", Change: "k3fd", ChangeID: "cid-c", Project: "widget",
	Head: "9f8e7d6c", State: "awaiting-approval", Checks: "pending", Since: time.Now().Add(-time.Minute), Note: "checks pending"}

// TestQueueDigest: the digest stays as it was while only ages, notes and
// order change, and changes with every transition: an item arriving or
// leaving, its state, its head, its group, and the operator's checks.
func TestQueueDigest(t *testing.T) {
	q := queue{coordinator: []queueItem{waitingDraft}, operator: []queueItem{waitingApproval}}
	d := q.digest()
	if d == (queue{}).digest() {
		t.Fatal("a queue with items has an empty queue's digest")
	}

	aged := queue{coordinator: []queueItem{waitingDraft}, operator: []queueItem{waitingApproval}}
	aged.coordinator[0].Since = aged.coordinator[0].Since.Add(-3 * time.Hour)
	aged.operator[0].Since = time.Time{}
	aged.coordinator[0].Note = "something else"
	if aged.digest() != d {
		t.Error("ages and notes changed the digest")
	}
	second := waitingDraft
	second.key, second.Run = "change:widget/cid-d", "run-d"
	if (queue{coordinator: []queueItem{waitingDraft, second}}).digest() != (queue{coordinator: []queueItem{second, waitingDraft}}).digest() {
		t.Error("order changed the digest")
	}

	with := func(f func(*queue)) string {
		c := queue{coordinator: []queueItem{waitingDraft}, operator: []queueItem{waitingApproval}}
		f(&c)
		return c.digest()
	}
	for name, changed := range map[string]string{
		"an item arrives":        with(func(c *queue) { c.coordinator = append(c.coordinator, second) }),
		"an item leaves":         with(func(c *queue) { c.operator = nil }),
		"a state changes":        with(func(c *queue) { c.coordinator[0].State = "draft" }),
		"a head changes":         with(func(c *queue) { c.coordinator[0].Head = "5e6f7a8b" }),
		"the checks pass":        with(func(c *queue) { c.operator[0].Checks = "passing" }),
		"an item changes groups": with(func(c *queue) { c.operator = append(c.operator, c.coordinator[0]); c.coordinator = nil }),
	} {
		if changed == d {
			t.Errorf("%s: the digest stayed %s", name, d)
		}
	}
}

// watchQueueHarness runs watchQueue in a temp session, with a queue source
// the test drives: each read of the queue is announced on looks and
// answered by answer.
type watchQueueHarness struct {
	t      *testing.T
	src    queueSource
	looks  chan int
	answer func(n int) (queue, bool)
	out    bytes.Buffer
	done   chan error
	cancel context.CancelFunc
}

func newWatchQueueHarness(t *testing.T, answer func(n int) (queue, bool)) *watchQueueHarness {
	dir := t.TempDir()
	h := &watchQueueHarness{t: t, looks: make(chan int, 16), answer: answer, done: make(chan error, 1),
		src: queueSource{events: filepath.Join(dir, "events.jsonl"), runs: filepath.Join(dir, "runs")}}
	var n int32
	h.src.compute = func(context.Context) (queue, bool, error) {
		k := int(atomic.AddInt32(&n, 1))
		q, provisional := h.answer(k)
		h.looks <- k
		return q, provisional, nil
	}
	return h
}

func (h *watchQueueHarness) start(seen string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	h.cancel = cancel
	h.t.Cleanup(cancel)
	next := func(d string) string { return nextQueueWatchCommand("", d) }
	go func() { h.done <- watchQueue(ctx, h.src, seen, next, &h.out) }()
}

// look waits for the watch's next read of the queue and returns its number.
func (h *watchQueueHarness) look() int {
	h.t.Helper()
	select {
	case n := <-h.looks:
		return n
	case err := <-h.done:
		h.t.Fatalf("the watch ended (%v) before it read the queue:\n%s", err, h.out.String())
	}
	return 0
}

// write appends to the session's event log, or writes a run record.
func (h *watchQueueHarness) write(path string) {
	h.t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("{}\n"); err != nil {
		h.t.Fatal(err)
	}
}

func (h *watchQueueHarness) wait() string {
	h.t.Helper()
	if err := <-h.done; err != nil {
		h.t.Fatalf("watch --queue: %v\n%s", err, h.out.String())
	}
	return h.out.String()
}

// TestWatchQueueReturnsAtOnceWhenAnythingWaits: without --seen the watch
// compares with an empty queue, so it returns from its first read when
// anything waits, printing the queue and the next: line that names its
// digest.
func TestWatchQueueReturnsAtOnceWhenAnythingWaits(t *testing.T) {
	q := queue{coordinator: []queueItem{waitingDraft}}
	h := newWatchQueueHarness(t, func(int) (queue, bool) { return q, false })
	h.start("")
	out := h.wait()
	for _, want := range []string{"Waiting on the coordinator\n", "◇ widget nxvp · awaiting-review · for 1h", "  run: valley flow ready run-b --head 1a2b3c4d\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if want := "next: valley flow watch --queue --seen " + q.digest() + "\n"; !strings.HasSuffix(out, want) {
		t.Errorf("output doesn't end with %q:\n%s", want, out)
	}
}

// TestWatchQueueWaitsForADifferentQueue: a watch from the last one's
// digest reads the queue again on each write to the event log or the run
// records, and doesn't return while the queue is as it saw it, the draft
// still waiting. It returns once a second item comes to wait beside the
// first, printing both. It reads nothing but the queue: no run, pane or
// event decides when it returns.
func TestWatchQueueWaitsForADifferentQueue(t *testing.T) {
	first := queue{coordinator: []queueItem{waitingDraft}}
	both := queue{coordinator: []queueItem{waitingDraft}, operator: []queueItem{waitingApproval}}
	h := newWatchQueueHarness(t, func(n int) (queue, bool) {
		if n < 3 {
			return first, false
		}
		return both, false
	})
	h.start(first.digest())
	h.look()
	h.write(h.src.events)
	if n := h.look(); n != 2 {
		t.Fatalf("read %d on the event log's write", n)
	}
	h.write(filepath.Join(h.src.runs, "run-c.json"))
	out := h.wait()
	if !strings.Contains(out, "◇ widget nxvp · awaiting-review") || !strings.Contains(out, "Waiting on the operator\n◇ widget k3fd · awaiting-approval") {
		t.Errorf("want both items:\n%s", out)
	}
	if want := "next: valley flow watch --queue --seen " + both.digest() + "\n"; !strings.HasSuffix(out, want) {
		t.Errorf("output doesn't end with %q:\n%s", want, out)
	}
}

// TestWatchQueueWaitsOutAHalfWrittenRecord: a queue read while a run
// record reads half written is provisional: the watch doesn't return on
// it, even when it differs, and the write that completes the record ends
// the wait with the draft the record holds.
func TestWatchQueueWaitsOutAHalfWrittenRecord(t *testing.T) {
	ready := queue{operator: []queueItem{waitingApproval}}
	h := newWatchQueueHarness(t, func(n int) (queue, bool) {
		if n == 1 {
			return ready, true // the draft's record caught mid-write
		}
		return queue{coordinator: []queueItem{waitingDraft}, operator: []queueItem{waitingApproval}}, false
	})
	h.start("")
	h.look()
	h.write(filepath.Join(h.src.runs, "run-b.json"))
	if out := h.wait(); !strings.Contains(out, "◇ widget nxvp · awaiting-review") {
		t.Errorf("want the draft once its record is whole:\n%s", out)
	}
}

// TestWatchQueueInterrupted: cancelled while it waits, the watch prints
// just the next: line, which waits from the digest it was given.
func TestWatchQueueInterrupted(t *testing.T) {
	q := queue{coordinator: []queueItem{waitingDraft}}
	h := newWatchQueueHarness(t, func(int) (queue, bool) { return q, false })
	h.start(q.digest())
	h.look()
	h.cancel()
	if out, want := h.wait(), "next: valley flow watch --queue --seen "+q.digest()+"\n"; out != want {
		t.Errorf("interrupted watch printed %q, want %q", out, want)
	}
}

// TestComputeQueueIsProvisionalOnAHalfWrittenRecord: a run record that
// doesn't parse makes the queue provisional; with every record whole, it
// isn't.
func TestComputeQueueIsProvisionalOnAHalfWrittenRecord(t *testing.T) {
	store := run.NewHomeDirStoreFromPath(t.TempDir())
	if err := store.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, provisional, err := computeQueue(context.Background(), store); err != nil || provisional {
		t.Fatalf("an empty session: provisional %t, %v", provisional, err)
	}
	if err := os.WriteFile(filepath.Join(store.StateDir(), "run-b.json"), []byte(`{"id": "run-b", "pro`), 0o644); err != nil {
		t.Fatal(err)
	}
	if q, provisional, err := computeQueue(context.Background(), store); err != nil || !provisional || len(q.coordinator)+len(q.operator) != 0 {
		t.Errorf("a half-written record: provisional %t, %v, %+v", provisional, err, q)
	}
}

// TestWatchOnceHandlesLinesAsItReads: a one-shot watch from the start of a
// backlog writes the first matching event before it reads the rest. The
// output truncates the file at that first write, so a watch that read the
// whole backlog before handling its lines would print the later match
// too.
func TestWatchOnceHandlesLinesAsItReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	line := func(typ, runID string) string {
		data, err := json.Marshal(event.New(runID, typ, nil))
		if err != nil {
			t.Fatal(err)
		}
		return string(data) + "\n"
	}
	backlog := line(event.PRMerged, "1") + strings.Repeat(line(event.PhaseStarted, "routine"), 4096) + line(event.PRMerged, "2")
	if err := os.WriteFile(path, []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := &truncatingWriter{path: path}
	flt := buildFilter([]string{event.PRMerged}, nil, nil)
	next := func(int64) string { return "next" }
	if err := watchOnce(context.Background(), path, flt, 0, true, next, out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, `"run_id":"1"`) || strings.Contains(got, `"run_id":"2"`) {
		t.Errorf("want the first merge alone, written before the backlog was read:\n%s", got)
	}
}

// truncatingWriter empties the file at path at its first write.
type truncatingWriter struct {
	bytes.Buffer
	path string
}

func (w *truncatingWriter) Write(p []byte) (int, error) {
	if w.Len() == 0 {
		if err := os.Truncate(w.path, 0); err != nil {
			return 0, err
		}
	}
	return w.Buffer.Write(p)
}
