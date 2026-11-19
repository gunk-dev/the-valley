package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"the-valley/flow/internal/event"
	"the-valley/flow/internal/jobs"
	"the-valley/flow/internal/run"
)

// jobRecords reads the session's job records as they are on disk, keyed by
// ID, without judging whether running ones died.
func (e *consultEnv) jobRecords(t *testing.T) map[string]*jobs.Job {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(e.sessionDir(), "jobs", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*jobs.Job{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var j jobs.Job
		if err := json.Unmarshal(data, &j); err != nil {
			t.Fatalf("bad record %s: %v", p, err)
		}
		out[j.ID] = &j
	}
	return out
}

// klaus runs another klaus command in the environment.
func (e *consultEnv) klaus(t *testing.T, args ...string) (string, error) {
	t.Helper()
	c := e.command()
	c.Args = append([]string{e.bin}, args...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// onlyJob returns the one job record, checking it shares its ID with the
// consult's events.
func onlyJob(t *testing.T, e *consultEnv) *jobs.Job {
	t.Helper()
	records := e.jobRecords(t)
	if len(records) != 1 {
		t.Fatalf("want one job record, got %+v", records)
	}
	for _, evt := range e.events(t) {
		if records[evt.RunID] == nil {
			t.Fatalf("event %s %s has no job record: %+v", evt.Type, evt.RunID, records)
		}
	}
	for _, j := range records {
		return j
	}
	return nil
}

func TestConsultJobRecords(t *testing.T) {
	bin := klausBinary(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		e.stub(t, "codex", "cat >/dev/null\necho the answer is 42\n")
		if out, err := e.run(t, "--backend", "codex", "--model", "gpt-9", "--effort", "high", "--role", "critic", "--dir", dir, "\n  What is the answer?\nmore detail"); err != nil {
			t.Fatalf("consult: %s, %v", out, err)
		}
		j := onlyJob(t, e)
		if j.Kind != "consult" || j.Status != jobs.Succeeded || j.Backend != "codex" || j.Model != "gpt-9" || j.Effort != "high" ||
			j.Role != "critic" || j.Dir != dir || j.Prompt != "What is the answer?" || j.Error != "" || j.Thread != "" || j.Panel != "" {
			t.Errorf("record: %+v", j)
		}
		if j.PID <= 0 || j.PIDStart == "" || j.FinishedAt == nil || j.FinishedAt.Before(j.StartedAt) {
			t.Errorf("process and times: %+v", j)
		}
		if data, err := os.ReadFile(j.AnswerFile); err != nil || string(data) != "the answer is 42\n" {
			t.Errorf("answer file %q: %q, %v", j.AnswerFile, data, err)
		}
		// The answer can be read back, by the ID's last four characters too.
		if out, err := e.klaus(t, "jobs", "show", "--answer", j.ID[len(j.ID)-4:]); err != nil || out != "the answer is 42\n" {
			t.Errorf("jobs show --answer: %q, %v", out, err)
		}
		out, err := e.klaus(t, "jobs", "show", j.ID)
		for _, want := range []string{"job:        " + j.ID, "status:     succeeded", "backend:    codex gpt-9, effort high, role critic", "\nthe answer is 42\n"} {
			if err != nil || !strings.Contains(out, want) {
				t.Errorf("jobs show missing %q: %s, %v", want, out, err)
			}
		}
	})

	t.Run("threaded", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		e.stub(t, "codex", "cat >/dev/null\necho 'session id: thread-1' >&2\necho threaded answer\n")
		if out, err := e.run(t, "--backend", "codex", "--thread", "design", "--dir", dir, "first question"); err != nil {
			t.Fatalf("consult: %s, %v", out, err)
		}
		j := onlyJob(t, e)
		if j.Status != jobs.Succeeded || j.Thread != "design" || j.Turn != 1 || j.AnswerFile != "" {
			t.Errorf("record: %+v", j)
		}
		// The answer stays in the thread's log, read from there.
		if out, err := e.klaus(t, "jobs", "show", "--answer", j.ID); err != nil || out != "threaded answer\n" {
			t.Errorf("jobs show --answer: %q, %v", out, err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		e.stub(t, "codex", "echo partial output\necho backend broke >&2\nexit 7\n")
		if out, err := e.run(t, "--backend", "codex", "--dir", dir, "question"); err == nil {
			t.Fatalf("failing backend succeeded: %s", out)
		}
		j := onlyJob(t, e)
		if j.Status != jobs.Failed || !strings.Contains(j.Error, "exit status 7") || j.AnswerFile != "" {
			t.Errorf("record: %+v", j)
		}
		if raw, err := os.ReadFile(j.RawOutput); err != nil || !strings.Contains(string(raw), "partial output") || !strings.Contains(string(raw), "backend broke") {
			t.Errorf("raw output %q: %s, %v", j.RawOutput, raw, err)
		}
		out, err := e.klaus(t, "jobs", "show", j.ID)
		if err != nil || !strings.Contains(out, "status:     failed: consult codex: exit status 7") || !strings.Contains(out, "raw output: "+j.RawOutput) {
			t.Errorf("jobs show: %s, %v", out, err)
		}
		if out, err := e.klaus(t, "jobs", "show", "--answer", j.ID); err == nil || !strings.Contains(out, "has no answer: failed") {
			t.Errorf("jobs show --answer of a failure: %s, %v", out, err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		e.stub(t, "codex", "sleep 30\n")
		if out, err := e.run(t, "--backend", "codex", "--dir", dir, "--timeout", "1s", "question"); err == nil {
			t.Fatalf("slow backend succeeded: %s", out)
		}
		if j := onlyJob(t, e); j.Status != jobs.TimedOut || j.Error != "timeout after 1s" {
			t.Errorf("record: %+v", j)
		}
	})

	t.Run("interrupted", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		e.stub(t, "codex", "sleep 30\n")
		c := e.command("--backend", "codex", "--dir", dir, "question")
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		waitForRecord(t, e, c, jobs.Running)
		if err := c.Process.Signal(syscall.SIGINT); err != nil {
			t.Fatal(err)
		}
		_ = c.Wait()
		if j := onlyJob(t, e); j.Status != jobs.Interrupted || j.Error != "interrupted" {
			t.Errorf("record: %+v", j)
		}
	})

	t.Run("panel", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		e.stub(t, "codex", "cat >/dev/null\necho codex answer\n")
		e.stub(t, "claude", "cat >/dev/null\necho claude broke >&2\nexit 3\n")
		if out, err := e.run(t, "--panel", "--dir", dir, "panel question"); err == nil {
			t.Fatalf("panel with a failing member succeeded: %s", out)
		}
		records := e.jobRecords(t)
		byBackend := map[string]*jobs.Job{}
		for _, j := range records {
			byBackend[j.Backend] = j
		}
		codex, claude := byBackend["codex"], byBackend["claude"]
		if len(records) != 2 || codex == nil || claude == nil || codex.Panel == "" || codex.Panel != claude.Panel || !strings.HasPrefix(codex.Panel, "panel-") {
			t.Fatalf("panel records: %+v", records)
		}
		if codex.Status != jobs.Succeeded || claude.Status != jobs.Failed {
			t.Errorf("members: codex %+v, claude %+v", codex, claude)
		}
		for _, evt := range e.events(t) {
			if records[evt.RunID] == nil || records[evt.RunID].Backend != evt.Data["backend"] {
				t.Errorf("event %s %s doesn't match a record", evt.Type, evt.RunID)
			}
		}
		// The panel's ID shows every member.
		out, err := e.klaus(t, "jobs", "show", codex.Panel)
		if err != nil || !strings.Contains(out, codex.ID) || !strings.Contains(out, claude.ID) || !strings.Contains(out, "codex answer") {
			t.Errorf("jobs show panel: %s, %v", out, err)
		}
	})

	t.Run("died", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		stubPID := filepath.Join(t.TempDir(), "stub.pid")
		e.stub(t, "codex", "echo $$ > "+stubPID+"\nexec sleep 30\n")
		c := e.command("--backend", "codex", "--dir", dir, "question")
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		waitForRecord(t, e, c, jobs.Running)
		if out, err := e.klaus(t, "jobs"); err != nil || !strings.Contains(out, "running") {
			t.Errorf("running job: %s, %v", out, err)
		}
		// SIGKILL leaves no end state; the backend, in its own process
		// group, outlives klaus.
		if err := c.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = c.Wait()
		t.Cleanup(func() {
			if data, err := os.ReadFile(stubPID); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		})
		if j := onlyJob(t, e); j.Status != jobs.Running {
			t.Fatalf("the record itself still says running: %+v", j)
		}
		out, err := e.klaus(t, "jobs")
		if err != nil || !strings.Contains(out, " died ") {
			t.Errorf("jobs should read the killed job as died: %s, %v", out, err)
		}
	})

	t.Run("prunes old records", func(t *testing.T) {
		e := newConsultEnv(t, bin, "session-jobs")
		store := jobs.NewStore(e.sessionDir())
		old := &jobs.Job{ID: "consult-20261001-0000-00000000", Kind: "consult", Backend: "codex"}
		if err := store.Start(old); err != nil {
			t.Fatal(err)
		}
		if _, err := store.WriteFile(old.ID, ".answer", []byte("old")); err != nil {
			t.Fatal(err)
		}
		if err := store.Finish(old, jobs.Succeeded, ""); err != nil {
			t.Fatal(err)
		}
		finished := time.Now().Add(-8 * 24 * time.Hour)
		old.StartedAt, old.FinishedAt = finished, &finished
		if err := store.Save(old); err != nil {
			t.Fatal(err)
		}
		e.stub(t, "codex", "cat >/dev/null\necho new answer\n")
		if out, err := e.run(t, "--backend", "codex", "--dir", dir, "question"); err != nil {
			t.Fatalf("consult: %s, %v", out, err)
		}
		left, _ := filepath.Glob(filepath.Join(store.Dir, old.ID+".*"))
		if records := e.jobRecords(t); len(records) != 1 || records[old.ID] != nil || len(left) != 0 {
			t.Errorf("old record not pruned: %+v, files %v", records, left)
		}
	})
}

// waitForRecord waits until the consult c has written its record with
// status want.
func waitForRecord(t *testing.T, e *consultEnv, c *exec.Cmd, want jobs.Status) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		for _, j := range e.jobRecords(t) {
			if j.Status == want {
				return
			}
		}
		if time.Now().After(deadline) {
			_ = c.Process.Kill()
			t.Fatalf("no %s job record appeared", want)
		}
	}
}

func TestReviewJobRecord(t *testing.T) {
	bin := klausBinary(t)
	e := newConsultEnv(t, bin, "session-review-jobs")
	e.stub(t, "gh", `case "$1 $2" in
"api repos/acme/widget/pulls/7") echo '{"title":"Add widget","body":"Adds it.","head":{"sha":"abc123"}}' ;;
"pr diff") printf 'diff --git a/w.go b/w.go\n--- a/w.go\n+++ b/w.go\n@@ -1 +1 @@\n-old\n+new\n' ;;
*) echo "unexpected gh $*" >&2; exit 9 ;;
esac
`)
	e.stub(t, "codex", `cat >/dev/null
while [ "$#" -gt 0 ]; do
 if [ "$1" = --output-last-message ]; then shift; printf '%s\n' '{"verdict":"Does what it says.","findings":[{"severity":"high","file":"w.go","line":1,"description":"new is wrong"}],"summary":"one finding"}' > "$1"; fi
 shift
done
`)
	out, err := e.klaus(t, "review", "7", "--repo", "acme/widget", "--backend", "codex", "--post=false")
	if err != nil || !strings.Contains(out, "new is wrong") {
		t.Fatalf("review: %s, %v", out, err)
	}
	j := onlyJob(t, e)
	if j.Kind != "review" || j.Status != jobs.Succeeded || j.PR != "acme/widget#7" || j.Backend != "codex" || j.Review == nil ||
		len(j.Review.Findings) != 1 || j.Review.Verdict != "Does what it says." || j.Review.Summary != "one finding" {
		t.Fatalf("record: %+v", j)
	}
	var types []string
	for _, evt := range e.events(t) {
		types = append(types, evt.Type)
		if evt.Type == event.ReviewCompleted && (evt.Data["success"] != true || evt.Data["findings"] != float64(1) || evt.Data["pr_number"] != "7") {
			t.Errorf("review:completed: %+v", evt.Data)
		}
	}
	if strings.Join(types, ",") != "review:started,review:completed" {
		t.Errorf("events: %v", types)
	}
	show, err := e.klaus(t, "jobs", "show", j.ID)
	for _, want := range []string{"kind:       review", "pr:         acme/widget#7", "Verdict: Does what it says.", "new is wrong"} {
		if err != nil || !strings.Contains(show, want) {
			t.Errorf("jobs show missing %q: %s, %v", want, show, err)
		}
	}

	// A review that can't reach the PR fails, and says why.
	if out, err := e.klaus(t, "review", "8", "--repo", "acme/widget", "--backend", "codex", "--post=false"); err == nil {
		t.Fatalf("review of an unknown PR succeeded: %s", out)
	}
	var failed *jobs.Job
	for _, r := range e.jobRecords(t) {
		if r.PR == "acme/widget#8" {
			failed = r
		}
	}
	if failed == nil || failed.Status != jobs.Failed || !strings.Contains(failed.Error, "fetching PR #8") || failed.Review != nil {
		t.Errorf("failed review record: %+v", failed)
	}
}

func TestStatusListsJobs(t *testing.T) {
	bin := klausBinary(t)
	e := newConsultEnv(t, bin, "session-status-jobs")

	// None: no jobs section at all.
	out, err := e.klaus(t, "status")
	if err != nil || out != "No runs found.\n" {
		t.Fatalf("status without jobs: %q, %v", out, err)
	}

	store := jobs.NewStore(e.sessionDir())
	add := func(j *jobs.Job, status jobs.Status, took, ago time.Duration) {
		t.Helper()
		if err := store.Start(j); err != nil { // by this test process, which is alive
			t.Fatal(err)
		}
		if status == jobs.Running {
			j.StartedAt = time.Now().Add(-took)
		} else {
			finished := time.Now().Add(-ago)
			j.Status, j.FinishedAt, j.StartedAt = status, &finished, finished.Add(-took)
		}
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
	}
	add(&jobs.Job{ID: "consult-20261017-1000-aaaa0001", Kind: "consult", Thread: "design", Backend: "codex", Model: "gpt-9", Prompt: "Design the jobs table"}, jobs.Running, 12*time.Minute, 0)
	add(&jobs.Job{ID: "review-20261017-0900-bbbb0002", Kind: "review", PR: "acme/widget#7", Backend: "claude"}, jobs.Succeeded, 3*time.Minute, 20*time.Minute)
	add(&jobs.Job{ID: "consult-20261017-0800-cccc0003", Kind: "consult", Backend: "agy", Prompt: "Too slow"}, jobs.TimedOut, 10*time.Minute, time.Hour)
	add(&jobs.Job{ID: "consult-20261016-0100-dddd0004", Kind: "consult", Backend: "codex", Prompt: "Long ago"}, jobs.Succeeded, time.Minute, 20*time.Hour)

	out, err = e.klaus(t, "status")
	if err != nil {
		t.Fatalf("status: %s, %v", out, err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 7 || lines[0] != "No runs found." || lines[1] != "" || !strings.HasPrefix(lines[2], "JOB ID ") {
		t.Fatalf("status layout:\n%s", out)
	}
	for i, want := range [][]string{
		{"consult-20261017-1000-aaaa0001", "consult", "design", "codex gpt-9", "running", "12m", "Design the jobs table"},
		{"review-20261017-0900-bbbb0002", "review", "acme/widget#7", "claude (default model)", "succeeded", "3m"},
		{"consult-20261017-0800-cccc0003", "consult", " - ", "agy (default model)", "timed out", "10m", "Too slow"},
	} {
		for _, w := range want {
			if !strings.Contains(lines[4+i], w) {
				t.Errorf("status line %d %q missing %q", i, lines[4+i], w)
			}
		}
	}
	if strings.Contains(out, "Long ago") {
		t.Errorf("status lists a job that finished 20h ago:\n%s", out)
	}
	if all, err := e.klaus(t, "jobs", "--all"); err != nil || !strings.Contains(all, "Long ago") {
		t.Errorf("jobs --all: %s, %v", all, err)
	}
}

func TestStatusCountsFinishedProsePasses(t *testing.T) {
	bin := klausBinary(t)
	e := newConsultEnv(t, bin, "session-status-prose")
	store := jobs.NewStore(e.sessionDir())
	add := func(j *jobs.Job, status jobs.Status, took, ago time.Duration) {
		t.Helper()
		if err := store.Start(j); err != nil {
			t.Fatal(err)
		}
		if status == jobs.Running {
			j.StartedAt = time.Now().Add(-took)
		} else {
			finished := time.Now().Add(-ago)
			j.Status, j.FinishedAt, j.StartedAt = status, &finished, finished.Add(-took)
		}
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
	}

	// Only finished prose passes: no table header, just the summary line.
	add(&jobs.Job{ID: "prose-20261017-1000-aaaa0001", Kind: jobs.KindProse, Run: "20261017-0950-0001", Backend: "agy", Prompt: "rewrite README.md"}, jobs.Succeeded, 10*time.Second, 5*time.Minute)
	add(&jobs.Job{ID: "prose-20261017-0100-old00001", Kind: jobs.KindProse, Run: "20261017-0050-0001", Backend: "agy", Prompt: "rewrite old.md"}, jobs.Failed, 10*time.Second, 10*time.Hour)
	out, err := e.klaus(t, "status")
	if err != nil {
		t.Fatalf("status: %s, %v", out, err)
	}
	if strings.Contains(out, "JOB ID") || strings.Contains(out, "rewrite README.md") {
		t.Errorf("status should not print a jobs table when only finished prose passes exist:\n%s", out)
	}
	if !strings.Contains(out, "1 prose pass (finished in the last 3h; klaus jobs --kind prose)") {
		t.Errorf("status missing prose summary line:\n%s", out)
	}

	// Add 32 more recent finished prose passes (8 failed total) and 1 finished consult (the 34-job case).
	failStatuses := []jobs.Status{jobs.Failed, jobs.TimedOut, jobs.Interrupted}
	for i := 0; i < 32; i++ {
		st := jobs.Succeeded
		if i < 8 {
			st = failStatuses[i%len(failStatuses)]
		}
		id := "prose-20261017-1100-" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "0001"
		add(&jobs.Job{ID: id, Kind: jobs.KindProse, Run: "20261017-0950-0001", Backend: "agy", Prompt: "rewrite docs.md"}, st, 20*time.Second, time.Duration(10+i)*time.Minute)
	}
	add(&jobs.Job{ID: "consult-20261017-1150-cons0001", Kind: jobs.KindConsult, Thread: "design", Backend: "codex", Model: "gpt-9", Prompt: "Design the jobs table"}, jobs.Succeeded, time.Minute, 8*time.Minute)

	out, err = e.klaus(t, "status")
	if err != nil {
		t.Fatalf("status: %s, %v", out, err)
	}
	if !strings.Contains(out, "consult-20261017-1150-cons0001") {
		t.Errorf("status should list finished consult:\n%s", out)
	}
	if strings.Contains(out, "rewrite README.md") || strings.Contains(out, "rewrite docs.md") {
		t.Errorf("status must not list finished prose passes as rows:\n%s", out)
	}
	if !strings.Contains(out, "33 prose passes, 8 failed (finished in the last 3h; klaus jobs --kind prose)") {
		t.Errorf("status missing summary line for 33 prose passes, 8 failed:\n%s", out)
	}

	// A running prose pass IS listed alongside the consult, while finished ones stay counted below.
	add(&jobs.Job{ID: "prose-20261017-1200-live0001", Kind: jobs.KindProse, Run: "20261017-1155-0002", Backend: "agy", Model: "gemini-4", Prompt: "rewrite guide.md"}, jobs.Running, 15*time.Second, 0)
	out, err = e.klaus(t, "status")
	if err != nil {
		t.Fatalf("status: %s, %v", out, err)
	}
	if !strings.Contains(out, "prose-20261017-1200-live0001") || !strings.Contains(out, "consult-20261017-1150-cons0001") {
		t.Errorf("status should list running prose pass and consult:\n%s", out)
	}
	if !strings.Contains(out, "33 prose passes, 8 failed (finished in the last 3h; klaus jobs --kind prose)") {
		t.Errorf("status missing summary line with running prose pass:\n%s", out)
	}
}

func TestJobsKindFlag(t *testing.T) {
	bin := klausBinary(t)
	e := newConsultEnv(t, bin, "session-jobs-kind")
	store := jobs.NewStore(e.sessionDir())
	add := func(j *jobs.Job, ago time.Duration) {
		t.Helper()
		if err := store.Start(j); err != nil {
			t.Fatal(err)
		}
		finished := time.Now().Add(-ago)
		j.Status, j.FinishedAt, j.StartedAt = jobs.Succeeded, &finished, finished.Add(-time.Minute)
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
	}
	add(&jobs.Job{ID: "consult-20261017-1150-aaaa0001", Kind: jobs.KindConsult, Backend: "codex", Prompt: "consult prompt"}, 10*time.Minute)
	add(&jobs.Job{ID: "review-20261017-1140-bbbb0002", Kind: jobs.KindReview, PR: "owner/app#7", Backend: "claude"}, 20*time.Minute)
	add(&jobs.Job{ID: "prose-20261017-1130-cccc0003", Kind: jobs.KindProse, Run: "20261017-1120-0001", Backend: "agy", Prompt: "rewrite README.md"}, 30*time.Minute)
	add(&jobs.Job{ID: "prose-20261016-0100-dddd0004", Kind: jobs.KindProse, Run: "20261016-0050-0001", Backend: "agy", Prompt: "rewrite old.md"}, 20*time.Hour)

	// Unfiltered klaus jobs still lists all three recent kinds, including prose.
	out, err := e.klaus(t, "jobs")
	if err != nil {
		t.Fatalf("jobs: %s, %v", out, err)
	}
	for _, want := range []string{"consult-20261017", "review-20261017", "prose-20261017"} {
		if !strings.Contains(out, want) {
			t.Errorf("klaus jobs missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "prose-20261016") {
		t.Errorf("klaus jobs without --all should omit old prose pass:\n%s", out)
	}

	// Single kind: prose, plus --all.
	out, err = e.klaus(t, "jobs", "--kind", "prose")
	if err != nil || !strings.Contains(out, "prose-20261017") || strings.Contains(out, "consult-20261017") || strings.Contains(out, "review-20261017") || strings.Contains(out, "prose-20261016") {
		t.Errorf("jobs --kind prose: %s, %v", out, err)
	}
	out, err = e.klaus(t, "jobs", "--kind", "prose", "--all")
	if err != nil || !strings.Contains(out, "prose-20261017") || !strings.Contains(out, "prose-20261016") || strings.Contains(out, "consult-20261017") {
		t.Errorf("jobs --kind prose --all: %s, %v", out, err)
	}

	// Repeatable and comma-separated kinds.
	for _, args := range [][]string{
		{"jobs", "--kind", "consult", "--kind", "review"},
		{"jobs", "--kind", "consult,review"},
	} {
		out, err = e.klaus(t, args...)
		if err != nil || !strings.Contains(out, "consult-20261017") || !strings.Contains(out, "review-20261017") || strings.Contains(out, "prose-20261017") {
			t.Errorf("%v: %s, %v", args, out, err)
		}
	}

	// Invalid or empty kind (including alongside a valid kind) fails cleanly.
	for _, args := range [][]string{
		{"jobs", "--kind", "bogus"},
		{"jobs", "--kind", ""},
		{"jobs", "--kind", "prose", "--kind="},
		{"jobs", "--kind", "consult,,review"},
	} {
		if out, err := e.klaus(t, args...); err == nil || !strings.Contains(out, "unknown job kind") {
			t.Errorf("%v should fail, got: %s, %v", args, out, err)
		}
	}
}

// TestStatusShowsRunModels runs the real `klaus status` over runs launched
// without a model, with one, and with a model the worker reported.
func TestStatusShowsRunModels(t *testing.T) {
	bin := klausBinary(t)
	e := newConsultEnv(t, bin, "session-status-models")
	store := run.NewHomeDirStoreFromPath(e.sessionDir())
	opus, haiku := "opus", "haiku"
	for _, s := range []*run.State{
		{ID: "20261030-1000-aaaa0001", Backend: "agy", CreatedAt: "2026-10-30T10:00:00Z"},
		{ID: "20261030-1001-bbbb0002", Model: &opus, CreatedAt: "2026-10-30T10:01:00Z"},
		{ID: "20261030-1002-cccc0003", Model: &haiku, ResolvedModel: "claude-haiku-5-5", CreatedAt: "2026-10-30T10:02:00Z"},
	} {
		if err := store.Save(s); err != nil {
			t.Fatal(err)
		}
	}
	out, err := e.klaus(t, "status")
	if err != nil {
		t.Fatalf("status: %s, %v", out, err)
	}
	for id, want := range map[string]string{
		"aaaa0001": "agy (default model)",
		"bbbb0002": "claude opus",
		"cccc0003": "claude claude-haiku-5-5",
	} {
		found := false
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, id) && strings.Contains(l, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no status line for %s with %q:\n%s", id, want, out)
		}
	}
	if !strings.Contains(out, "MODEL") {
		t.Errorf("status header lacks MODEL:\n%s", out)
	}
}
