package checksfix

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"the-valley/flow/internal/event"
	"the-valley/flow/internal/restack"
	"the-valley/flow/internal/valley"
)

// TestPassCountsAttempts drives passes over a change whose checks keep
// failing on each new head. Each head gets one run; a run whose next head
// still fails counts as a failed attempt, and after 3 klaus stops, until
// checks pass and the count starts over. A change another principal owns
// gets nothing.
func TestPassCountsAttempts(t *testing.T) {
	dir := t.TempDir()
	l, err := LoadLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	var launched []Launch
	var stalls []map[string]interface{}
	n := 0
	d := Deps{
		Emit: func(runID, typ string, data map[string]interface{}) {
			if runID != "kxyz" || typ != event.ChangeChecksStalled {
				t.Errorf("emitted %s under %s", typ, runID)
			}
			stalls = append(stalls, data)
		},
		Principal: func(context.Context, string) string { return "operator" },
		NewRunID:  func() (string, error) { n++; return fmt.Sprintf("run-%d", n), nil },
		Launch:    func(_ context.Context, la Launch) error { launched = append(launched, la); return nil },
		Running:   func(restack.Key, string) bool { return false },
		Now:       time.Now,
	}
	repos := []restack.Repo{{Name: "widget", Dir: dir}}
	listing := func(owner, head, state string) *valley.Changes {
		v := valley.Version{N: 1, Head: head, Branch: "agent/x", Landing: valley.NotLanded,
			Checks: &valley.Checks{State: state, Required: []valley.Check{{Name: "web-unit", State: state, Why: "boom"}}}}
		return &valley.Changes{Repos: []valley.Repo{{Repo: "widget", Changes: []valley.Change{{
			ChangeID: "kxyz", Handle: "kxyz", Owner: owner, State: valley.StateReady, Active: 1, Versions: []valley.Version{v}}}}}}
	}
	pass := func(owner, head, state, want string) {
		t.Helper()
		views, err := Pass(context.Background(), d, l, repos, listing(owner, head, state), true)
		if err != nil {
			t.Fatalf("head %s %s: %v", head, state, err)
		}
		var lines []string
		for _, v := range views {
			lines = append(lines, v.String())
		}
		if got := strings.Join(lines, "\n"); !strings.Contains(got, want) {
			t.Errorf("head %s %s: pass said %q, want %q", head, state, got, want)
		}
	}

	pass("someone-else", "h0", valley.ChecksFailed, "")
	if len(launched) != 0 {
		t.Fatalf("another owner's change got a run: %+v", launched)
	}
	pass("operator", "h1", valley.ChecksFailed, "fix run run-1 dispatched (attempt 1 of 3)")
	pass("operator", "h1", valley.ChecksFailed, "fix run run-1 dispatched (attempt 1 of 3)")
	pass("operator", "h2", valley.ChecksFailed, "fix run run-2 dispatched (attempt 2 of 3)")
	pass("operator", "h3", valley.ChecksFailed, "fix run run-3 dispatched (attempt 3 of 3)")
	if len(stalls) != 0 {
		t.Errorf("stalled while fix runs were left: %v", stalls)
	}
	pass("operator", "h4", valley.ChecksFailed, "stopped after 3 fix runs")
	pass("operator", "h4", valley.ChecksFailed, "stopped after 3 fix runs")
	if len(stalls) != 1 || stalls[0]["head"] != "h4" || stalls[0]["attempts"] != 3 || stalls[0]["reason"] != "stopped after 3 fix runs" {
		t.Errorf("one stall for h4, want reported once: %v", stalls)
	}
	pass("operator", "h5", valley.ChecksFailed, "stopped after 3 fix runs")
	if len(stalls) != 2 || stalls[1]["head"] != "h5" {
		t.Errorf("a new failed head past the attempts stalls again: %v", stalls)
	}
	if len(launched) != 3 {
		t.Fatalf("launched %d runs, want 3", len(launched))
	}
	if la := launched[2]; la.Head != "h3" || la.Owner != "operator" || la.Branch != "agent/x" || la.ChangeID != "kxyz" {
		t.Errorf("the third launch expects %+v", la)
	}

	pass("operator", "h5", valley.ChecksPassed, "")
	pass("operator", "h6", valley.ChecksFailed, "fix run run-4 dispatched (attempt 1 of 3)")

	// A restarted pass reads what the ledger recorded.
	if l, err = LoadLedger(dir); err != nil {
		t.Fatal(err)
	}
	pass("operator", "h6", valley.ChecksFailed, "fix run run-4 dispatched (attempt 1 of 3)")
	if len(launched) != 4 {
		t.Errorf("launched %d runs, want 4", len(launched))
	}
}

// TestPassReportsAStallOnceSaved: a pass whose attempts are spent and whose
// ledger can't be saved ends with an error and reports nothing; once the
// ledger saves, the stall is reported, and a later pass, reading the
// ledger, reports it no more.
func TestPassReportsAStallOnceSaved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "session") // not made yet: saving fails
	stalls := 0
	d := Deps{
		Emit:      func(string, string, map[string]interface{}) { stalls++ },
		Principal: func(context.Context, string) string { return "operator" },
		NewRunID:  func() (string, error) { return "", fmt.Errorf("no run IDs") },
		Launch:    func(context.Context, Launch) error { t.Error("launched past the attempts"); return nil },
		Running:   func(restack.Key, string) bool { return false },
		Now:       time.Now,
	}
	repos := []restack.Repo{{Name: "widget", Dir: dir}}
	v := valley.Version{N: 4, Head: "h4", Branch: "agent/x", Landing: valley.NotLanded,
		Checks: &valley.Checks{State: valley.ChecksFailed, Required: []valley.Check{{Name: "web-unit", State: valley.ChecksFailed}}}}
	listed := &valley.Changes{Repos: []valley.Repo{{Repo: "widget", Changes: []valley.Change{{
		ChangeID: "kxyz", Handle: "kxyz", Owner: "operator", State: valley.StateReady, Active: 4, Versions: []valley.Version{v}}}}}}
	spent := func() *Ledger {
		l, err := LoadLedger(dir)
		if err != nil {
			t.Fatal(err)
		}
		l.entries["widget/kxyz"] = &Entry{Repo: "widget", ChangeID: "kxyz", Attempts: MaxAttempts, Heads: []string{"h3"}}
		return l
	}

	if _, err := Pass(context.Background(), d, spent(), repos, listed, true); err == nil || stalls != 0 {
		t.Fatalf("a ledger that can't be saved: err %v, %d stalls reported; want an error and none", err, stalls)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if views, err := Pass(context.Background(), d, spent(), repos, listed, true); err != nil || stalls != 1 || len(views) != 1 || views[0].State != StateStopped {
		t.Fatalf("once the ledger saves: %v, err %v, %d stalls reported; want stopped, reported once", views, err, stalls)
	}
	l, err := LoadLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Pass(context.Background(), d, l, repos, listed, true); err != nil || stalls != 1 {
		t.Errorf("a later pass: err %v, %d stalls reported; want still one", err, stalls)
	}
}
