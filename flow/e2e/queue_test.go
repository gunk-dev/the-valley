//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/flow/internal/run"
)

// TestDraftAwaitingReviewWaitsOnTheCoordinator lets the real finalize
// leave a draft for review (ready_at_finalize off) while a queue watch
// waits. An empty queue can't end the watch, so it returns only once
// finalize's draft is in the queue: it prints the queue with the draft and
// the valley flow ready that readies it, and a next: line naming the
// queue's digest. valley flow queue lists the draft too, as does --json,
// and status dates it. The watch on that next: line returns once valley
// flow ready marks the draft ready, which puts it in the operator's group,
// with no command.
func TestDraftAwaitingReviewWaitsOnTheCoordinator(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	h.installValleyStub()
	h.joinValley()
	if err := os.WriteFile(filepath.Join(h.E2EDir, "valley.has-unready"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.AmendRepoConfig(map[string]any{"ready_at_finalize": false})

	res := h.RunKlaus("launch", "add a health check endpoint")
	if res.ExitCode != 0 {
		t.Fatalf("launch exited %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	id := h.RunIDs()[0]
	h.WaitForClaudeStart(30 * time.Second)
	st, err := h.ReadState(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Worktree, "health.go"), []byte("package health\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(st.Worktree, "add", "-A")
	h.git(st.Worktree, "commit", "-qm", "Add a health check")
	h.git(st.Worktree, "push", "-q", "origin", st.Branch)
	head := h.GitOutput(st.Worktree, "rev-parse", "HEAD")
	// The fake valley's ready names the change nxvp.
	rec := *st
	rec.Valley = &run.Valley{Handle: "nxvp"}
	listing := func(state string) {
		v := version(1, head, "Add a health check", "not-landed")
		v["branch"] = st.Branch
		h.writeValleyChanges(map[string]any{
			"schema": "valley-changes/v1",
			"host":   "valley-host",
			"repos": []any{map[string]any{
				"repo": "widget", "url": "git@valley-host:widget.git", "error": nil,
				"changes": []any{change(&rec, state, 1, v)}, "branches": []any{},
			}},
		})
	}
	listing("draft")

	// Nothing waits while the agent runs.
	if res := h.RunKlaus("queue"); res.ExitCode != 0 || res.Stdout != "" {
		t.Fatalf("queue while the agent runs exited %d with:\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	w := h.startKlaus(nil, "watch", "--queue")

	h.ReleaseClaude()
	h.WaitForState(id, func(s *run.State) bool { return s.DurationMS != nil && s.TmuxPane == nil }, 60*time.Second)
	out := w.waitWithin(60 * time.Second)
	ready := "  run: valley flow ready " + id + " --head " + head + "\n"
	for _, want := range []string{"Waiting on the coordinator\n", "◇ widget nxvp · awaiting-review · for ", ready} {
		if !strings.Contains(out.Stdout, want) {
			t.Errorf("watch --queue output lacks %q:\n%s", want, out.Stdout)
		}
	}
	next := queueWatchNext(t, out.Stdout)

	res = h.RunKlaus("queue")
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, ready) || strings.Contains(res.Stdout, "Waiting on the operator") {
		t.Errorf("queue exited %d, want the draft alone:\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	res = h.RunKlaus("queue", "--json")
	var q struct {
		Coordinator []map[string]any `json:"coordinator"`
		Operator    []map[string]any `json:"operator"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &q); err != nil {
		t.Fatalf("queue --json: %v\n%s", err, res.Stdout)
	}
	if len(q.Coordinator) != 1 || q.Coordinator[0]["run"] != id || q.Coordinator[0]["state"] != "awaiting-review" ||
		q.Coordinator[0]["head"] != head || q.Coordinator[0]["change"] != "nxvp" || len(q.Operator) != 0 {
		t.Errorf("queue --json = %s", res.Stdout)
	}

	res = h.RunKlaus("status")
	if cells := statusCells(t, res.Stdout, id); cells[1] != "awaiting-review" || cells[2] == "-" {
		t.Errorf("status shows %q for %q, want awaiting-review with its age:\n%s", cells[2], cells[1], res.Stdout)
	}

	// The watch from the first one's next: line returns once the draft
	// is ready, with the change in the operator's group.
	w = h.startKlaus(nil, next...)
	listing("ready")
	if res := h.RunKlaus("ready", id, "--head", head); res.ExitCode != 0 {
		t.Fatalf("ready exited %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	out = w.waitWithin(60 * time.Second)
	if strings.Contains(out.Stdout, "Waiting on the coordinator") || strings.Contains(out.Stdout, "run:") ||
		!strings.Contains(out.Stdout, "Waiting on the operator\n◇ widget nxvp · awaiting-approval · for ") {
		t.Errorf("the next watch, once the draft is ready, printed:\n%s", out.Stdout)
	}
	if again := queueWatchNext(t, out.Stdout); strings.Join(again, " ") == strings.Join(next, " ") {
		t.Errorf("the next: line names the queue it already saw: %v", again)
	}
	res = h.RunKlaus("queue")
	if res.ExitCode != 0 || strings.Contains(res.Stdout, "Waiting on the coordinator") ||
		!strings.Contains(res.Stdout, "Waiting on the operator\n◇ widget nxvp · awaiting-approval · for ") || strings.Contains(res.Stdout, "run:") {
		t.Errorf("queue once ready exited %d, want the change waiting on the operator, with no command:\n%s%s", res.ExitCode, res.Stdout, res.Stderr)
	}
}

// waitWithin waits up to d for p to exit 0, and returns its outcome.
func (p *klausProcess) waitWithin(d time.Duration) RunResult {
	p.t.Helper()
	select {
	case <-p.done:
	case <-time.After(d):
		res := p.stop()
		p.t.Fatalf("klaus didn't exit within %s\nstdout:\n%s\nstderr:\n%s", d, res.Stdout, res.Stderr)
	}
	res := p.wait()
	if res.ExitCode != 0 {
		p.t.Fatalf("klaus exited %d\nstdout:\n%s\nstderr:\n%s", res.ExitCode, res.Stdout, res.Stderr)
	}
	return res
}

// queueWatchNext is the klaus command line on the last line of a watch
// --queue's output, its next: line, which waits from the queue it printed.
func queueWatchNext(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	last := lines[len(lines)-1]
	cmdline, ok := strings.CutPrefix(last, "next: valley flow ")
	if !ok || !strings.HasPrefix(cmdline, "watch --queue --seen ") {
		t.Fatalf("last line %q is not a next: line naming a digest", last)
	}
	return strings.Fields(cmdline)
}
