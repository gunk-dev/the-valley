package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsnotify/fsnotify"
	"the-valley/flow/internal/jobs"
	"the-valley/flow/internal/run"
)

// sampleJobs returns a running threaded consult, a failed one, a succeeded
// review whose answer is in its record, a succeeded consult with an answer
// file, and one that finished too long ago to list.
func sampleJobs(t *testing.T, now time.Time) []*jobs.Job {
	t.Helper()
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	answer := filepath.Join(t.TempDir(), "answer")
	if err := os.WriteFile(answer, []byte("first line of the answer\nsecond line of the answer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return []*jobs.Job{
		{ID: "consult-20261017-1200-aaaa0001", Kind: "consult", Thread: "design", Backend: "codex", Model: "gpt-9",
			Prompt: "Design the jobs table", Status: jobs.Running, StartedAt: now.Add(-12 * time.Minute)},
		{ID: "consult-20261017-1150-bbbb0002", Kind: "consult", Backend: "claude", Prompt: "Why did it break",
			Status: jobs.Failed, Error: "consult claude: exit status 7", RawOutput: "/tmp/consult-0002.raw",
			StartedAt: now.Add(-6 * time.Minute), FinishedAt: at(5 * time.Minute)},
		{ID: "review-20261017-1140-cccc0003", Kind: "review", PR: "acme/widget#7", Backend: "agy",
			Status: jobs.Succeeded, StartedAt: now.Add(-23 * time.Minute), FinishedAt: at(20 * time.Minute),
			Review: &jobs.Review{Verdict: "Does what it says.", Findings: nil}},
		{ID: "consult-20261017-1130-dddd0004", Kind: "consult", Backend: "codex", Prompt: "Answer me",
			Status: jobs.Succeeded, AnswerFile: answer, StartedAt: now.Add(-41 * time.Minute), FinishedAt: at(40 * time.Minute)},
		{ID: "consult-20261017-0100-eeee0005", Kind: "consult", Backend: "codex", Prompt: "Long ago",
			Status: jobs.Succeeded, StartedAt: now.Add(-11 * time.Hour), FinishedAt: at(10 * time.Hour)},
	}
}

func newJobsModel(t *testing.T, width, height int) dashboardModel {
	t.Helper()
	now := time.Now()
	m := dashboardModel{
		states: []*run.State{{ID: "20261017-1200-0001", Type: "launch", Prompt: "active 01", TmuxPane: strPtr("%1"),
			CreatedAt: now.Format(time.RFC3339), Worktree: "/wt", TargetRepo: strPtr("testowner/testrepo")}},
		tmuxDeps:  testDashboardTmuxDeps(),
		ghStatus:  map[string]*prStatus{},
		hideAfter: defaultHideFinishedAfter,
	}
	m = send(m, tea.WindowSizeMsg{Width: width, Height: height})
	return send(m, jobsLoadedMsg{jobs: sampleJobs(t, now)})
}

func TestDashboardJobsSection(t *testing.T) {
	m := newJobsModel(t, 140, 30)
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	find := func(s string) int {
		for i, l := range lines {
			if strings.Contains(l, s) {
				return i
			}
		}
		return -1
	}
	row, header := find("active 01"), find("jobs · 1 running · 3 finished")
	if row < 0 || header <= row {
		t.Fatalf("jobs section should follow the run rows:\n%s", view)
	}
	for i, want := range [][]string{
		{"consult:0001", "design", "codex gpt-9", "RUNNING", "12m", "Design the jobs table"},
		{"consult:0002", "-", "claude", "FAILED", "1m", "Why did it break"},
		{"review:0003", "acme/widget#7", "agy", "SUCCEEDED", "3m"},
		{"consult:0004", "-", "codex", "SUCCEEDED", "1m", "Answer me"},
	} {
		line := lines[header+1+i]
		for _, w := range want {
			if !strings.Contains(line, w) {
				t.Errorf("job line %d %q missing %q", i, line, w)
			}
		}
	}
	if strings.Contains(view, "Long ago") {
		t.Errorf("a job that finished 10h ago is listed:\n%s", view)
	}
	if !strings.Contains(view, "o open · tab jobs") {
		t.Errorf("footer should offer tab:\n%s", view)
	}
	// The jobs don't join the run rows.
	if !strings.Contains(view, "1 agents") || strings.Contains(view, "1 done") {
		t.Errorf("summary counts jobs as rows:\n%s", view)
	}
}

// On a short pane the jobs lines go after the spacing and source lines and
// before the errors, the footer and any run row; their header goes last.
func TestDashboardFitLayoutDropsJobsBeforeRows(t *testing.T) {
	m := dashboardModel{recentErrors: []dashboardError{{Time: time.Now(), Message: "boom"}}}
	const rows, nJobs = 10, 3
	prevJobs := nJobs
	for h := 30; h >= 1; h-- {
		m.height = h
		items, bodyH := m.fitLayout(rows, nJobs, false)
		count := map[chromeKind]int{}
		for _, it := range items {
			count[it.kind]++
		}
		shown := count[chromeJob]
		if shown > prevJobs {
			t.Errorf("height %d: %d job lines, more than at a taller pane", h, shown)
		}
		prevJobs = shown
		if bodyH < min(3, h) && len(items) > 0 {
			t.Errorf("height %d: %d rows while %d chrome lines are kept", h, bodyH, len(items))
		}
		if shown < nJobs && (count[chromeBlank] > 0 || count[chromeSource] > 0) {
			t.Errorf("height %d: a job line went before the spacing and source lines: %+v", h, count)
		}
		if shown > 0 && (count[chromeError] == 0 || count[chromeFooter] == 0 || count[chromeJobs] == 0) {
			t.Errorf("height %d: the errors, footer or jobs header went before a job line: %+v", h, count)
		}
	}

	// The rendered view fits, and names the jobs it had no room for.
	view := newJobsModel(t, 80, 7).View()
	assertFits(t, view, 80, 7)
	if plain := ansi.Strip(view); !strings.Contains(plain, "active 01") || !strings.Contains(plain, "not shown") {
		t.Errorf("short pane should keep the row and count the hidden jobs:\n%s", plain)
	}
}

func TestDashboardSelectJobAndReadIt(t *testing.T) {
	m := newJobsModel(t, 140, 30)
	selected := func(view string) string {
		for _, l := range strings.Split(ansi.Strip(view), "\n") {
			if strings.HasPrefix(l, "> ") {
				return l
			}
		}
		return ""
	}
	if sel := selected(m.View()); !strings.Contains(sel, "active 01") {
		t.Fatalf("the run row starts selected, got %q", sel)
	}

	m = press(m, "tab")
	view := ansi.Strip(m.View())
	if sel := selected(view); !strings.Contains(sel, "consult:0001") || strings.Count(view, "\n> ") != 1 {
		t.Errorf("tab should select the top job, and only it: %q\n%s", sel, view)
	}
	if !strings.Contains(view, "consult:0001 running for 12m | enter read") {
		t.Errorf("footer should say how the running job stands:\n%s", view)
	}

	// The footer of a failed job says why, and where its raw output is.
	m = press(m, "j")
	view = ansi.Strip(m.View())
	if sel := selected(view); !strings.Contains(sel, "consult:0002") ||
		!strings.Contains(view, "failed: consult claude: exit status 7 · raw output: /tmp/consult-0002.raw") {
		t.Errorf("failed job selection %q:\n%s", sel, view)
	}

	// a and d act on PRs, not jobs; q still quits from the jobs.
	m = press(m, "a")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "a job has no PR") {
		t.Errorf("approve on a job should say why it can't:\n%s", view)
	}

	// enter reads a review's answer, its findings, in place of the list; q
	// closes it rather than quitting.
	m = press(m, "j", "enter")
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "Verdict: Does what it says.") || !strings.Contains(view, "pr:         acme/widget#7") || strings.Contains(view, "active 01") {
		t.Errorf("review detail:\n%s", view)
	}
	m = press(m, "q")
	if sel := selected(m.View()); !strings.Contains(sel, "review:0003") {
		t.Fatalf("q should close the detail back onto the job, got %q", sel)
	}

	// A consult's answer, scrolled with j/k; esc goes back, and again to
	// the runs.
	m = press(m, "G", "enter")
	view = ansi.Strip(m.View())
	for _, want := range []string{"job consult-20261017-1130-dddd0004", "status:     succeeded", "second line of the answer", "esc back"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail missing %q:\n%s", want, view)
		}
	}
	m = press(m, "esc", "esc")
	if sel := selected(m.View()); !strings.Contains(sel, "active 01") {
		t.Errorf("esc should go back to the runs, got %q", sel)
	}
}

// The dashboard follows job records through its fsnotify watcher: a job
// appears when its record is written, and changes when it finishes.
func TestDashboardReloadsJobsOnRecordChange(t *testing.T) {
	dir := t.TempDir()
	store := run.NewHomeDirStoreFromPath(dir)
	jobStore := jobs.NewStore(dir)

	// A run's record is told apart from a job's.
	w, ok := startWatcherCmd(store, jobStore.Dir)().(*fsnotify.Watcher)
	if !ok {
		t.Fatal("watcher didn't start")
	}
	if err := store.Save(&run.State{ID: "20261017-1200-0001", Type: "launch", CreatedAt: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if msg := awaitCmd(t, watchFSCmd(w, jobStore.Dir)); msg != (fsEventMsg{}) {
		t.Errorf("a run's record gave %T, want fsEventMsg", msg)
	}
	w.Close()

	w, ok = startWatcherCmd(store, jobStore.Dir)().(*fsnotify.Watcher)
	if !ok {
		t.Fatal("watcher didn't start")
	}
	defer w.Close()
	l := &teaLoop{t: t, msgs: make(chan tea.Msg, 64), m: dashboardModel{
		store: store, jobsDir: jobStore.Dir, states: []*run.State{},
		tmuxDeps: testDashboardTmuxDeps(), ghStatus: map[string]*prStatus{}, width: 120, height: 30,
	}}
	l.deliver(w)

	job := &jobs.Job{ID: "consult-20261017-1200-abcd0001", Kind: "consult", Backend: "codex", Prompt: "watch me"}
	if err := jobStore.Start(job); err != nil { // by this test process, so it reads as running
		t.Fatal(err)
	}
	l.until("watch me", "RUNNING")
	if err := jobStore.Finish(job, jobs.TimedOut, "timeout after 1s"); err != nil {
		t.Fatal(err)
	}
	l.until("watch me", "TIMED OUT")
}

// awaitCmd runs cmd, failing the test if it doesn't return promptly.
func awaitCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("command didn't return")
	}
	return nil
}

// teaLoop runs a dashboard model's commands and feeds their messages back
// through Update, as bubbletea does.
type teaLoop struct {
	t    *testing.T
	m    dashboardModel
	msgs chan tea.Msg
}

func (l *teaLoop) start(c tea.Cmd) {
	if c != nil {
		go func() { l.msgs <- c() }()
	}
}

func (l *teaLoop) deliver(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			l.start(c)
		}
	default:
		next, c := l.m.Update(msg)
		l.m = next.(dashboardModel)
		l.start(c)
	}
}

// until delivers messages until the view shows every one of want.
func (l *teaLoop) until(want ...string) {
	l.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		view := ansi.Strip(l.m.View())
		ok := true
		for _, w := range want {
			ok = ok && strings.Contains(view, w)
		}
		if ok {
			return
		}
		select {
		case msg := <-l.msgs:
			l.deliver(msg)
		case <-deadline:
			l.t.Fatalf("view never showed %q:\n%s", want, view)
		}
	}
}

func TestDashboardProseJobsCountedNotListed(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	runA := "20261017-1200-aaaa0001"
	runB := "20261017-1201-bbbb0002"

	// 34-job case: 1 finished consult + 33 finished prose passes (8 failed,
	// spanning failed/timed out/interrupted/died), with 13 belonging to runA
	// (3 failed) and 20 to runB (5 failed), plus 1 running prose pass on runA
	// and 1 finished prose pass older than 3h on runA.
	list := []*jobs.Job{
		{ID: "prose-20261017-1205-run00001", Kind: jobs.KindProse, Run: runA, Backend: "agy", Model: "gemini-4",
			Prompt: "rewrite docs/guide.md", Status: jobs.Running, StartedAt: now.Add(-30 * time.Second)},
		{ID: "consult-20261017-1150-cons0001", Kind: jobs.KindConsult, Thread: "design", Backend: "codex", Model: "gpt-9",
			Prompt: "Design the jobs table", Status: jobs.Succeeded, StartedAt: now.Add(-12 * time.Minute), FinishedAt: at(10 * time.Minute)},
	}
	failStatuses := []jobs.Status{jobs.Failed, jobs.TimedOut, jobs.Interrupted, jobs.Died}
	for i := 0; i < 33; i++ {
		owner := runB
		status := jobs.Succeeded
		if i < 13 {
			owner = runA
			if i < 3 {
				status = failStatuses[i%len(failStatuses)]
			}
		} else if i-13 < 5 {
			status = failStatuses[(i-13)%len(failStatuses)]
		}
		j := &jobs.Job{
			ID:        "prose-20261017-1100-" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "0001",
			Kind:      jobs.KindProse,
			Run:       owner,
			Backend:   "agy",
			Prompt:    "rewrite README.md",
			Status:    status,
			StartedAt: now.Add(-time.Duration(15+i) * time.Minute),
		}
		if status != jobs.Died {
			j.FinishedAt = at(time.Duration(14+i) * time.Minute)
		}
		list = append(list, j)
	}
	// Outside the 3h window: excluded from the header count, still counted on runA's footer.
	list = append(list, &jobs.Job{
		ID: "prose-20261017-0100-old00001", Kind: jobs.KindProse, Run: runA, Backend: "agy",
		Prompt: "rewrite old.md", Status: jobs.Succeeded,
		StartedAt: now.Add(-6 * time.Hour), FinishedAt: at(5 * time.Hour),
	})

	m := dashboardModel{
		states: []*run.State{
			{ID: runA, Type: "launch", Prompt: "long run", TmuxPane: strPtr("%1"),
				CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Worktree: "/wt-a", TargetRepo: strPtr("owner/app")},
			{ID: runB, Type: "launch", Prompt: "second run", TmuxPane: strPtr("%2"),
				CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339), Worktree: "/wt-b", TargetRepo: strPtr("owner/app")},
		},
		tmuxDeps:  testDashboardTmuxDeps(),
		ghStatus:  map[string]*prStatus{},
		hideAfter: defaultHideFinishedAfter,
	}
	m = send(m, tea.WindowSizeMsg{Width: 140, Height: 30})
	m = send(m, jobsLoadedMsg{jobs: list})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "jobs · 1 running · 1 finished · 33 prose passes, 8 failed") {
		t.Fatalf("jobs header should count listed rows and finished prose passes:\n%s", view)
	}
	if !strings.Contains(view, "prose:0001") || !strings.Contains(view, "rewrite docs/guide.md") {
		t.Errorf("running prose pass should be listed:\n%s", view)
	}
	if !strings.Contains(view, "consult:0001") || !strings.Contains(view, "Design the jobs table") {
		t.Errorf("finished consult should be listed:\n%s", view)
	}
	if strings.Contains(view, "rewrite README.md") || strings.Contains(view, "rewrite old.md") {
		t.Errorf("finished prose passes must not appear as rows:\n%s", view)
	}

	// Row B is newer so it is selected first; footer shows its 20 prose passes, 5 failed.
	if !strings.Contains(view, "20 prose passes, 5 failed |") {
		t.Errorf("selected runB footer missing its prose count:\n%s", view)
	}
	// Move selection to runA: 13 recent + 1 older = 14 finished prose passes, 3 failed (running pass not counted).
	m = press(m, "j")
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "14 prose passes, 3 failed |") {
		t.Errorf("selected runA footer should count all its finished prose passes:\n%s", view)
	}

	// Tab selects only the 2 listed jobs (running prose pass, then consult).
	m = press(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "> prose:0001") {
		t.Errorf("tab should land on running prose pass:\n%s", v)
	}
	m = press(m, "j")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "> consult:0001") {
		t.Errorf("j should move to the consult:\n%s", v)
	}
	// Another j stays clamped on consult:0001, never landing on a hidden prose pass.
	m = press(m, "j")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "> consult:0001") {
		t.Errorf("j past last listed job must stay on consult:0001:\n%s", v)
	}

	// When the running prose pass finishes, only the consult remains listed
	// and the header prose count increases to 34 (8 failed).
	list[0].Status = jobs.Succeeded
	list[0].FinishedAt = at(time.Second)
	m = send(m, jobsLoadedMsg{jobs: list})
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "jobs · 1 finished · 34 prose passes, 8 failed") {
		t.Errorf("after running prose finishes, header should update:\n%s", view)
	}
	if strings.Contains(view, "rewrite docs/guide.md") {
		t.Errorf("finished prose pass should leave the table:\n%s", view)
	}
	if !strings.Contains(view, "> consult:0001") {
		t.Errorf("selection should stay on consult:0001:\n%s", view)
	}
}

func TestDashboardOnlyFinishedProseJobs(t *testing.T) {
	now := time.Now()
	finished := now.Add(-5 * time.Minute)
	m := dashboardModel{
		states: []*run.State{
			{ID: "20261017-1200-aaaa0001", Type: "launch", Prompt: "only run", TmuxPane: strPtr("%1"),
				CreatedAt: now.Format(time.RFC3339), Worktree: "/wt", TargetRepo: strPtr("owner/app")},
		},
		tmuxDeps:  testDashboardTmuxDeps(),
		ghStatus:  map[string]*prStatus{},
		hideAfter: defaultHideFinishedAfter,
	}
	m = send(m, tea.WindowSizeMsg{Width: 140, Height: 20})
	m = send(m, jobsLoadedMsg{jobs: []*jobs.Job{
		{ID: "prose-20261017-1155-aaaa0001", Kind: jobs.KindProse, Run: "20261017-1200-aaaa0001",
			Backend: "agy", Prompt: "rewrite README.md", Status: jobs.Succeeded,
			StartedAt: now.Add(-6 * time.Minute), FinishedAt: &finished},
	}})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "jobs · 1 prose pass") || strings.Contains(view, "failed") {
		t.Errorf("header should show singular prose pass with no failed count:\n%s", view)
	}
	if strings.Contains(view, "tab jobs") {
		t.Errorf("footer must not advertise tab jobs when no job rows are listed:\n%s", view)
	}
	if !strings.Contains(view, "1 prose pass |") {
		t.Errorf("selected run footer should show its 1 prose pass:\n%s", view)
	}

	// Pressing tab does not focus the jobs section.
	m = press(m, "tab")
	if m.jobFocus {
		t.Error("tab must not enter jobFocus when only finished prose passes exist")
	}

	// Pressing x notes the prose pass in the prompt; on a narrow pane it
	// falls back to the compact variant so y/n stays visible. Confirming
	// with y hides it from the header.
	m = press(m, "x")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "hide 0 finished jobs, 1 prose pass? y/n") {
		t.Errorf("clear prompt should report prose count honestly:\n%s", v)
	}
	narrow := send(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	if v := ansi.Strip(narrow.View()); !strings.Contains(v, "clear: 0 runs, 0 jobs, 1 prose? y/n") {
		t.Errorf("narrow clear prompt should keep y/n visible:\n%s", v)
	}
	m = press(m, "y")
	if v := ansi.Strip(m.View()); strings.Contains(v, "jobs ·") {
		t.Errorf("jobs header should disappear after clearing finished prose passes:\n%s", v)
	}
}

func TestDashboardFooterCombinesApproveCommandAndProse(t *testing.T) {
	now := time.Now()
	finished := now.Add(-2 * time.Minute)
	branch, sha := "fix-bug-12345678", strings.Repeat("a", 40)
	m := dashboardModel{
		states: []*run.State{
			{ID: "20261017-1200-aaaa0001", Type: "launch", Prompt: "fix bug",
				CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
				TargetRepo: strPtr("owner/app"), ChangeID: "chg-12345678",
				PushedBranch: &branch, PushedSHA: &sha,
				Valley: &run.Valley{Repo: "app", Handle: "abcd", Version: 1}},
			{ID: "20261017-1150-aaaa0000", Type: "launch", Prompt: "initial attempt",
				CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339),
				TargetRepo: strPtr("owner/app"), ChangeID: "chg-12345678"},
		},
		tmuxDeps:        testDashboardTmuxDeps(),
		ghStatus:        map[string]*prStatus{},
		approveTemplate: "valley approve {handle} --head {head}",
		hideAfter:       defaultHideFinishedAfter,
	}
	m = send(m, tea.WindowSizeMsg{Width: 160, Height: 20})
	m = send(m, jobsLoadedMsg{jobs: []*jobs.Job{
		{ID: "prose-20261017-1158-aaaa0001", Kind: jobs.KindProse, Run: "20261017-1200-aaaa0001",
			Backend: "agy", Status: jobs.Succeeded, StartedAt: now.Add(-3 * time.Minute), FinishedAt: &finished},
		{ID: "prose-20261017-1159-aaaa0002", Kind: jobs.KindProse, Run: "20261017-1150-aaaa0000",
			Backend: "agy", Status: jobs.Failed, StartedAt: now.Add(-3 * time.Minute), FinishedAt: &finished},
	}})

	view := ansi.Strip(m.View())
	want := "approve: valley approve abcd --head " + sha + " · 2 prose passes, 1 failed"
	if !strings.Contains(view, want) {
		t.Errorf("footer missing %q:\n%s", want, view)
	}
}

func TestDashboardJobsNotShownExcludesProse(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	list := []*jobs.Job{
		{ID: "consult-20261017-1200-aaaa0001", Kind: jobs.KindConsult, Backend: "codex", Prompt: "c1", Status: jobs.Running, StartedAt: now.Add(-2 * time.Minute)},
		{ID: "consult-20261017-1155-bbbb0002", Kind: jobs.KindConsult, Backend: "codex", Prompt: "c2", Status: jobs.Succeeded, StartedAt: now.Add(-6 * time.Minute), FinishedAt: at(5 * time.Minute)},
		{ID: "review-20261017-1150-cccc0003", Kind: jobs.KindReview, PR: "owner/app#1", Backend: "agy", Status: jobs.Succeeded, StartedAt: now.Add(-11 * time.Minute), FinishedAt: at(10 * time.Minute)},
		{ID: "prose-20261017-1145-dddd0004", Kind: jobs.KindProse, Backend: "agy", Prompt: "p1", Status: jobs.Succeeded, StartedAt: now.Add(-16 * time.Minute), FinishedAt: at(15 * time.Minute)},
		{ID: "prose-20261017-1140-eeee0005", Kind: jobs.KindProse, Backend: "agy", Prompt: "p2", Status: jobs.Failed, StartedAt: now.Add(-21 * time.Minute), FinishedAt: at(20 * time.Minute)},
	}
	m := dashboardModel{
		states: []*run.State{{ID: "20261017-1200-0001", Type: "launch", Prompt: "active 01", TmuxPane: strPtr("%1"),
			CreatedAt: now.Format(time.RFC3339), Worktree: "/wt", TargetRepo: strPtr("owner/app")}},
		tmuxDeps:  testDashboardTmuxDeps(),
		ghStatus:  map[string]*prStatus{},
		hideAfter: defaultHideFinishedAfter,
	}
	// Height 7 leaves room for header, summary, 1 row, jobs header, 2 of 3 listed jobs, footer -> 1 not shown (not 3).
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 7})
	m = send(m, jobsLoadedMsg{jobs: list})
	view := ansi.Strip(m.View())
	assertFits(t, m.View(), 120, 7)
	if !strings.Contains(view, "jobs · 1 running · 2 finished · 1 not shown · 2 prose passes, 1 failed") {
		t.Errorf("short pane header counts wrong:\n%s", view)
	}
}
