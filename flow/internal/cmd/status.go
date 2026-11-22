package cmd

import (
	"context"
	"fmt"
	"the-valley/flow/internal/review"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"the-valley/flow/internal/checksfix"
	gh "the-valley/flow/internal/github"
	"the-valley/flow/internal/integration"
	"the-valley/flow/internal/jobs"
	"the-valley/flow/internal/restack"
	"the-valley/flow/internal/run"
	"the-valley/flow/internal/tmux"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all runs and their current state",
	Long: `Shows all runs in the session and their current state.

STATUS is one of: running (the agent's pane is still running it, judged as
the dashboard judges it: a dead pane, or an idle one once the run finished,
isn't running), needs-attention (finalize salvaged its work, it failed
after taking on the work of runs that needed attention, or the files its
clone shares across worktrees, its git config, info/attributes or hooks,
changed while it ran: a line below the table names them, and that holds
whatever became of its work), superseded (it
needed attention, but a run of its change, the same change ID in the same
repo, took its pushed work on and is running or finished: that run
recorded it, as launch does for a --pr, --change or --resume-from run
continuing a branch that holds the work, and finalize does when the head a
run left on origin contains it; that run carries the attention from then
on, and shows needs-attention itself if it ended needing attention or
failing; neither status nor the dashboard counts the superseded run as
needing attention), pr-created, merged, closed,
no-access (GitHub won't show the PR with the current credentials), exited,
and, for runs that finished by pushing a branch without a PR:

  awaiting-integration  the branch is on origin and hasn't landed
  integrated            its work is on origin's default branch, by ancestry
                        or by an equal patch-id, or the valley says its
                        change landed
  branch-gone           the branch was deleted from origin without landing

and, for a branch finalize recorded as a version of its change on a valley
(ready, or a draft when ready_at_finalize is off), where the valley's record
says the change stands:

  awaiting-review       a draft finalize left for review, as it does when
                        ready_at_finalize is off; approve refuses it until
                        'valley flow ready' marks it ready once its review passes
  awaiting-approval     ready and waiting for you to approve it
  approved              approved: the integrator hasn't decided, or judged it
                        stale only because its checks are still running (CI
                        shows them). Nothing to do
  uncertain             a request was consumed or failed with no landing
                        evidence: look at it
  checks-failed         stale, and the version's checks failed
  conflicting           stale, and the change's current restack report
                        says a rebase conflicted
  stale, rejected,      the integrator's decision on the request (stale
  failed                with passed checks, or on a valley that lists
                        none, is for you to look at)
  withdrawn             the change was withdrawn
  older-version         the change has a newer version, made by the
                        valley's restacker or a later run, and hasn't
                        landed; the run's row says nothing of whether the
                        change is done
  draft                 returned to draft ('valley unready'), as launch
                        does when a follow-up run continues the change;
                        approve refuses it until a head is ready again.
                        A draft reads draft even when its landing is
                        uncertain; only landing evidence comes first

A run that isn't running and isn't done shows working while another run of
its change (the same change ID in the same repo) is running: a follow-up
(--pr, --resume-from or a pipeline fix) is still pushing to it, so there is
nothing to approve yet.

AGE is how long the run or its change has been in its state, as valley
flow queue dates it: since the event that put it there (agent:change-draft
for awaiting-review, agent:change-ready for awaiting-approval, and so
on); a running run since its launch, and a working one since the launch
of the run working on its change. It reads - when no event marks the
state, or nothing is left to do.

A valley change added with valley flow track shows where the valley's listing
says its active version stands, by the statuses above (awaiting-approval,
approved, integrated, draft, withdrawn and so on), with its title as the
prompt; it reads tracking while the valley can't be asked. A tracked PR
reads tracking until GitHub says it is merged or closed.

CHANGE names the change a run belongs to: its PR's number (#319), else the
number of its change's PR (the PR of the newest run of the change that has
one), else its valley change's handle, else the first 8 characters of its
change ID, so runs of one change share it, running or not. The handle is the valley
listing's, else (when the valley can't be asked) the one a run of the
change recorded when finalize marked it ready or launch returned it to
draft. A valley run shows its change's title as the
prompt.

Integration is checked with one "valley changes --all --json" when a valley
is involved, and otherwise against each run's clone (one ls-remote and fetch
per repo). If the valley can't be asked, its runs are checked against their
clones. If the check can't complete, the run stays awaiting-integration (or
awaiting-approval, or awaiting-review). A deleted branch alone never settles a valley change: it
stays awaiting-approval rather than branch-gone.

A valley's host runs a ready change's required checks. CI reads where they
stand on the change's active version, as it reads a PR's CI: pending,
failing or passing, and a list after the table names each change's failed
checks. CI reads - while the change isn't ready, or the valley lists no
checks (one from before it listed them). The checks don't change the
status: a ready change reads awaiting-approval whatever they say, since you
may approve it before they finish, and it lands once both hold. The
dashboard dispatches a run to fix a failed head of a change flow owns (see
valley flow failed-checks).

A valley's restacker rebases ready changes onto main after each landing,
and reports a rebase that conflicts. For a change flow owns (its record's
owner is the repo's valley.principal), CONFLICTS reads restack while the
conflict stands, and a list after the table says where each stands: a
resolution run due, dispatched (attempt n of 3), being launched (flow
hasn't yet seen the run start), waiting out the cooldown, a run already
working on the change, stopped after 3 runs, not dispatched (and why: the
change couldn't be returned to draft, or the launch failed, perhaps after
starting its worker), or left alone (the conflict is in a change below it,
or the change sits on other changes). valley flow restack-conflicts, which the
dashboard runs, does the dispatching; status only reads.

A line after them sums the session's cost, counting the runs valley flow cleanup
removed. When finalize failed to push a run's data ref to origin (push_data_ref
on), a list names those runs and the failure's class and message, with a hint
saying what is known of each class; the run's STATUS does not change.

After the runs, a jobs table lists the session's consults, reviews, and running
prose passes (see valley flow jobs): every one running, and consults and reviews that
finished in the last 3 hours, with how long each has run or took. Finished prose
passes in the last 3 hours are counted on a single summary line under the table
(total and failed). Both are left out when there are none.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := sessionStore()
		if err != nil {
			return err
		}
		if err := printRuns(cmd.Context(), store); err != nil {
			return err
		}
		printRecentJobs(os.Stdout, store, time.Now())
		return nil
	},
}

// printRecentJobs prints, after a blank line, the jobs table of the
// session's running jobs and recent consults and reviews, followed by a
// single summary line of finished prose passes in recentJobs; nothing when
// there are none.
func printRecentJobs(w io.Writer, store run.StateStore, now time.Time) {
	hds, ok := store.(*run.HomeDirStore)
	if !ok {
		return
	}
	list, err := jobs.NewStore(hds.BaseDir()).List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: reading jobs: %v\n", err)
		return
	}
	listed, prose := partitionJobs(jobs.Recent(list, now, recentJobs))
	if len(listed) == 0 && prose.total == 0 {
		return
	}
	fmt.Fprintln(w)
	if len(listed) > 0 {
		printJobsTable(w, listed, now)
	}
	if prose.total > 0 {
		fmt.Fprintf(w, "%s (finished in the last 3h; valley flow jobs --kind prose)\n", prose)
	}
}

// printRuns prints the runs table and the session's spend, or says there
// are no runs.
func printRuns(ctx context.Context, store run.StateStore) error {
	states, err := store.List()
	if err != nil {
		return err
	}
	cleared := sessionCleared(store, states)
	if len(states) == 0 && cleared.Runs == 0 {
		fmt.Println("No runs found.")
		return nil
	}
	if len(states) > 0 {
		if err := runStatuses(ctx, store, states, os.Stdout); err != nil {
			return err
		}
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "Session cost: $%.2f", computeTotalCost(states)+cleared.CostUSD)
	if cleared.Runs > 0 {
		fmt.Fprintf(os.Stdout, " ($%.2f of it by %d runs cleanup removed)", cleared.CostUSD, cleared.Runs)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

// sessionCleared sums the runs cleanup removed from the session; zero when
// store isn't a session directory or the ledger can't be read.
func sessionCleared(store run.StateStore, live []*run.State) run.Cleared {
	c, err := clearedOf(store, live)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: reading cleared runs: %v\n", err)
	}
	return c
}

// clearedOf sums the runs cleanup removed from store's session, other than
// those in live; zero when store isn't a session directory.
func clearedOf(store run.StateStore, live []*run.State) (run.Cleared, error) {
	hds, ok := store.(*run.HomeDirStore)
	if !ok {
		return run.Cleared{}, nil
	}
	return run.ReadCleared(hds.BaseDir(), live)
}

// runStanding returns where each run of states stands, by index, as klaus
// status shows it, and the integration check it read.
func runStanding(ctx context.Context, store run.StateStore, states []*run.State) ([]string, integration.Report, error) {
	rows, report, err := runRows(ctx, store, states, loadClock(store), io.Discard)
	statuses := make([]string, len(rows))
	for i, r := range rows {
		statuses[i] = r.status
	}
	return statuses, report, err
}

// runStatuses writes the runs table for states to out.
func runStatuses(ctx context.Context, store run.StateStore, states []*run.State, out io.Writer) error {
	_, _, err := runRows(ctx, store, states, loadClock(store), out)
	return err
}

// runRow is where one run stands, as klaus status shows it.
type runRow struct {
	run    *run.State
	status string
	// since is when the run, or its change, entered its state as
	// queueState names it; zero when no event marks it, or nothing is
	// left to do.
	since time.Time
	ci    string // the CI column: pending, failing, passing or -
	merge string // the MERGE column of a PR's run
	label string // the CHANGE column
	// approved is set when a run of the run's PR was approved with klaus
	// approve; draftPR when GitHub says a PR whose MERGE reads ready is a
	// draft.
	approved, draftPR bool
	// checksDone and restackDone are set when the run flow's checks or
	// restack dispatch launched for the change has finished without a new
	// head (dispatchFinished).
	checksDone, restackDone bool
	// change is the valley's record of the run's version, or the version
	// finalize recorded; nil for a run no valley knows.
	change *integration.Change
	// restack and checks are where flow's dispatchers stand on a restack
	// conflict or failed checks of the run's change; nil for none.
	restack *restack.View
	checks  *checksfix.View
}

// runRows writes the runs table for states to out and returns where each
// run stands, by index into states, as the table shows it, dated by clock,
// and the integration check it read.
func runRows(ctx context.Context, store run.StateStore, states []*run.State, clock *stateClock, out io.Writer) ([]runRow, integration.Report, error) {
	tmuxClient := tmux.NewExecClient()
	ghClient := gh.NewGHCLIClient("")
	hds, _ := store.(*run.HomeDirStore)

	fmt.Fprintf(out, "%-22s  %-10s  %-5s  %-26s  %-8s  %-6s  %-20s  %-15s  %-8s  %-10s  %-10s  %-8s  %-10s  %s\n",
		"RUN ID", "STATUS", "AGE", "MODEL", "COST", "ISSUE", "REPO", "HOST", "CHANGE", "CI", "CONFLICTS", "BEHIND", "MERGE", "PROMPT")
	fmt.Fprintf(out, "%-22s  %-10s  %-5s  %-26s  %-8s  %-6s  %-20s  %-15s  %-8s  %-10s  %-10s  %-8s  %-10s  %s\n",
		"------", "------", "---", "-----", "----", "-----", "----", "----", "------", "--", "---------", "------", "-----", "------")

	statuses := make([]string, len(states))
	running := make(map[string]bool)
	for i, s := range states {
		if err := ctx.Err(); err != nil {
			return nil, integration.Report{}, err
		}
		statuses[i] = determineStatus(ctx, s, tmuxClient)
		running[s.ID] = statuses[i] == "running"
	}
	// A run whose work a later run took on hands its attention to that run.
	superseded := supersededRuns(states, func(s *run.State) bool { return running[s.ID] })
	var pushed []*run.State
	for i, s := range states {
		if statuses[i] == "needs-attention" && superseded[s.ID] && !s.SharedGitChanged() {
			statuses[i] = "superseded"
		}
		if statuses[i] == "awaiting-integration" || integration.TracksChange(s) {
			pushed = append(pushed, s)
		}
	}
	// Where pushed branches stand: one valley listing for the runs a
	// valley knows and the tracked valley changes, one check per repo
	// against each run's own clone for the rest.
	report := integration.NewValleyChecker(nil).Check(ctx, pushed)
	if note := integration.ValleyNote(report.ValleyErr); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	handles := changeHandles(states, report.Changes)
	prs := changePRs(states)
	// The restack conflicts and failed checks on changes klaus owns, and
	// what klaus did about them; reading them changes nothing.
	var restackList []restack.View
	checksViews := make(map[integration.ChangeKey]checksfix.View)
	if hds != nil && (report.Listing != nil || !report.Asked) {
		var err error
		if restackList, err = restackPass(ctx, hds, report.Listing, false); err != nil {
			fmt.Fprintf(os.Stderr, "warning: reading restack conflicts: %v\n", err)
		}
	}
	if hds != nil && report.Listing != nil {
		views, err := checksPass(ctx, hds, report.Listing, false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: reading failed-checks dispatches: %v\n", err)
		}
		for _, v := range views {
			checksViews[integration.ChangeKey{Repo: v.Repo, ChangeID: v.ChangeID}] = v
		}
	}
	restacks := restackViews(restackList)
	// The runs cleanup removed: a dispatched run among them has finished.
	cleared, err := clearedOf(store, states)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: reading cleared runs to settle dispatches: %v\n", err)
	}

	// The changes some run is still running on, and since when: any other
	// run of one that isn't done is working.
	changeOf := changeKeys(states)
	busy := make(map[integration.ChangeKey]bool)
	busySince := make(map[integration.ChangeKey]time.Time)
	for i, s := range states {
		k, ok := changeOf(s)
		if !ok || statuses[i] != "running" {
			continue
		}
		if t := runCreated(s); !busy[k] || t.Before(busySince[k]) {
			busySince[k] = t
		}
		busy[k] = true
	}

	// The PRs some run was approved on: approval covers every run of one.
	approvedPRs := make(map[string]bool)
	for _, s := range states {
		if ref := extractPRRef(s); ref != "" && s.Approved != nil && *s.Approved {
			approvedPRs[ref] = true
		}
	}

	rows := make([]runRow, len(states))
	for i, s := range states {
		if err := ctx.Err(); err != nil {
			return nil, report, err
		}
		status := statuses[i]
		prompt := truncate(s.Prompt, 40)
		var change *integration.Change
		if status == "awaiting-integration" {
			res := report.Runs[s.ID]
			change = res.Change
			if change == nil {
				change = integration.RecordedChange(s)
			}
			status = pushedStatus(res.Verdict, change != nil)
			if change != nil && awaitsReview(s, res.Verdict) {
				status = "awaiting-review"
			}
			if change != nil && change.Title != "" {
				prompt = truncate(change.Title, 40)
			}
		}
		// A tracked valley change stands where the valley's listing says
		// its active version does; tracking while the valley can't be
		// asked.
		if integration.TracksChange(s) && !s.SharedGitChanged() {
			if view := report.Changes[valleyKey(s)]; view.Change != nil {
				status = pushedStatus(view.Verdict, true)
				change = view.Change
				if view.Change.Title != "" {
					prompt = truncate(view.Change.Title, 40)
				}
			}
		}
		cost := formatCost(s)
		issue := "-"
		if s.Issue != nil {
			issue = *s.Issue
		}
		repo := "-"
		if s.TargetRepo != nil {
			repo = truncate(*s.TargetRepo, 20)
		}
		host := "-"
		if s.Host != nil {
			host = *s.Host
		}
		ci, conflicts, behind, merge := "-", "-", "-", "-"
		draftPR := false
		if prRef := extractPRRef(s); prRef != "" {
			prState := ghClient.GetState(ctx, prRef)
			switch prState {
			case "MERGED":
				status = prSettled(status, s, "merged")
				merge = "merged"
			case "CLOSED":
				status = prSettled(status, s, "closed")
			case gh.StateNoAccess:
				// GitHub won't show this PR to us: nothing else to ask.
				status = prSettled(status, s, "no-access")
			default:
				ci = ghClient.GetCI(ctx, prRef)
				conflicts = ghClient.GetConflicts(ctx, prRef)
				behindBy := ghClient.GetCommitsBehind(ctx, prRef)
				if behindBy > 0 {
					behind = fmt.Sprintf("%d", behindBy)
				}
				merge = computeMergeStatus(ci, conflicts, ghClient.GetReviewDecision(ctx, prRef), behindBy)
				draftPR = merge == "ready" && ghClient.IsDraft(ctx, prRef)
			}
		}

		if s.ChangeID != "" && ci == "-" {
			if view := report.Changes[valleyKey(s)]; view.Change != nil {
				if c := checksCI(view.Change.Checks); c != "" {
					ci = c
				}
			}
		}

		row := runRow{run: s, label: changeLabel(s, changeOf, prs, handles), change: change, approved: approvedPRs[extractPRRef(s)]}
		if v, ok := restacks[valleyKey(s)]; ok && s.ChangeID != "" {
			row.restack = &v
			row.restackDone = dispatchFinished(v.RunID, v.Head, states, statuses, cleared.IDs)
			if conflicts == "-" {
				conflicts = "restack"
			}
		}
		if v, ok := checksViews[valleyKey(s)]; ok && s.ChangeID != "" {
			row.checks = &v
			row.checksDone = dispatchFinished(v.RunID, v.Head, states, statuses, cleared.IDs)
		}

		k, inChange := changeOf(s)
		if inChange && busy[k] && live(status) && !s.SharedGitChanged() {
			status = "working"
		}

		row.status, row.ci, row.merge, row.draftPR = status, ci, merge, draftPR
		switch state, _ := queueState(row); state {
		case "working":
			row.since = busySince[k]
		case "running":
			row.since = runCreated(s)
		default:
			row.since = clock.since(s, state, rowHead(row))
		}
		rows[i] = row
		fmt.Fprintf(out, "%-22s  %-10s  %-5s  %-26s  %-8s  %-6s  %-20s  %-15s  %-8s  %-10s  %-10s  %-8s  %-10s  %s\n",
			s.ID, status, formatAge(row.since, time.Now()), runBackendName(s), cost, issue, repo, host, row.label, ci, conflicts, behind, merge, prompt)
	}

	// A run that changed the files its clone shares across worktrees
	// says which, for the operator to check before git runs there again.
	first := true
	for _, s := range states {
		if !s.SharedGitChanged() {
			continue
		}
		if first {
			fmt.Fprintln(out)
			first = false
		}
		fmt.Fprintf(out, "%s changed its clone's shared git files while it ran: %s (in %s); check them before running git in that clone\n",
			s.ID, strings.Join(s.SharedGit.Changed, ", "), s.SharedGit.Dir)
	}

	if failing := failingChecks(states, report.Changes); len(failing) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Failed valley checks:")
		for _, f := range failing {
			fmt.Fprintf(out, "  %s\n", f)
		}
	}

	if len(restackList) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Restack conflicts on changes flow owns:")
		for _, v := range restackList {
			fmt.Fprintf(out, "  %s\n", v)
		}
	}

	// What finalize recorded; nothing updates it if the ref reaches origin later.
	if failed := failedDataRefPushes(states); len(failed) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Data ref pushes that failed at finalize:")
		var hints []string
		for _, s := range failed {
			fmt.Fprintf(out, "  %s %s\n", s.ID, dataRefFailure(s.DataRefPush))
			if hint := dataRefHint(s.DataRefPush); !slices.Contains(hints, hint) {
				hints = append(hints, hint)
			}
		}
		for _, hint := range hints {
			fmt.Fprintf(out, "  %s\n", hint)
		}
	}

	return rows, report, nil
}

// formatAge is the AGE column: how long ago since was, "-" when it is
// zero, unknown. humanizeDuration reads a time ahead of now, after the
// clock stepped back, as 0s.
func formatAge(since, now time.Time) string {
	if since.IsZero() {
		return "-"
	}
	return humanizeDuration(now.Sub(since))
}

// prSettled is the status of run s, status so far, once GitHub says its PR
// is settled (merged, closed or out of reach): settled, unless the run
// still needs attention it took on with other runs' work (TookOver), which
// its PR's fate settles nothing about, as on the dashboard.
func prSettled(status string, s *run.State, settled string) string {
	if status == "needs-attention" && (len(s.TookOver) > 0 || s.SharedGitChanged()) {
		return status
	}
	return settled
}

func determineStatus(ctx context.Context, s *run.State, tc tmux.Client) string {
	if s.Type == "session" {
		if _, err := os.Stat(s.Worktree); err == nil {
			return "active"
		}
		return "ended"
	}

	if s.Type == "track" {
		if s.MergedAt != nil {
			return "merged"
		}
		return "tracking"
	}

	// Running as the dashboard decides it: a pane that exists isn't enough
	// when its command is dead, or, once the run finalized, idle.
	if s.IsAgentRunningWith(run.TmuxDepsFor(ctx, tc)) {
		return "running"
	}

	// The attention a run took on with other runs' work (TookOver) outlasts
	// its PR's merge, as on the dashboard, and so does a change to the
	// clone's shared git files.
	if needsAttention(s) && len(s.TookOver) > 0 || s.SharedGitChanged() {
		return "needs-attention"
	}
	if s.MergedAt != nil {
		return "merged"
	}
	if needsAttention(s) {
		return "needs-attention"
	}
	if s.PRURL != nil {
		return "pr-created"
	}
	if s.PushedSHA != nil {
		// Direct-push finish: the branch on origin waits for the operator.
		// The status command refines this with an integration check.
		return "awaiting-integration"
	}
	return "exited"
}

// live reports whether a status leaves something to do for a run that isn't
// running: salvaged work, an open PR, or a pushed branch that hasn't landed,
// gone, or been withdrawn or gained a newer version.
func live(status string) bool {
	switch status {
	case "needs-attention", "pr-created", "awaiting-integration", "awaiting-review", "awaiting-approval",
		"approved", "checks-failed", "conflicting", "uncertain", "stale", "rejected", "failed", "draft":
		return true
	}
	return false
}

// pushedStatus is the status of a run awaiting integration once an
// integration check has looked at it; valley says a valley knows the run's
// change. An unknown verdict (no clone, origin unreachable) keeps it
// awaiting.
func pushedStatus(v integration.Verdict, valley bool) string {
	switch v {
	case integration.Integrated, integration.IntegratedContent:
		return "integrated"
	case integration.Gone:
		return "branch-gone"
	case integration.Ready:
		return "awaiting-approval"
	case integration.Unknown, integration.Awaiting:
		if valley {
			return "awaiting-approval"
		}
		return "awaiting-integration"
	case integration.Superseded:
		// The change has a newer version, the restacker's or a later
		// run's; the change itself may still wait for approval.
		return "older-version"
	}
	// approved, checks-failed, conflicting, uncertain, stale, rejected,
	// failed, withdrawn, draft
	return v.String()
}

// awaitsReview reports whether a valley change's version, which run s
// pushed and a check says v of, awaits review rather than approval:
// finalize left it a draft (ready_at_finalize off), klaus ready hasn't
// marked it ready since, and the valley says it is still a draft, or can't
// be asked. A draft whose review passed is marked ready with klaus ready.
func awaitsReview(s *run.State, v integration.Verdict) bool {
	if s == nil || s.Valley == nil || !s.Valley.Draft {
		return false
	}
	return v == integration.Draft || v == integration.Unknown || v == integration.Awaiting
}

func formatCost(s *run.State) string {
	if s.CostUSD != nil {
		return fmt.Sprintf("$%.2f", *s.CostUSD)
	}
	if s.Budget != nil {
		return fmt.Sprintf("<$%s", *s.Budget)
	}
	return "-"
}

// changeLabel names the change a run belongs to, for the CHANGE column: its
// PR's number (#319), else its change's PR's (changePRs), else its valley
// change's handle (changeHandles), else the first 8 characters of its
// change ID, as the dashboard names a PR's or a change's row; "-" for a run
// with none of them.
func changeLabel(s *run.State, changeOf func(*run.State) (integration.ChangeKey, bool), prs, handles map[integration.ChangeKey]string) string {
	k, _ := changeOf(s)
	switch {
	case s.PRURL != nil:
		return formatPR(s)
	case prs[k] != "":
		return prs[k]
	case handles[k] != "":
		return handles[k]
	case s.ChangeID != "":
		return s.ChangeID[:min(8, len(s.ChangeID))]
	}
	return "-"
}

// changePRs names each change some run with a PR belongs to by that PR's
// number, the newest such run's, as the dashboard puts the change's runs
// without a PR on that PR's row.
func changePRs(states []*run.State) map[integration.ChangeKey]string {
	changeOf := changeKeys(states)
	out := make(map[integration.ChangeKey]string)
	by := make(map[integration.ChangeKey]*run.State) // the run whose PR names each change
	for _, s := range states {
		k, ok := changeOf(s)
		if !ok || s.PRURL == nil || by[k] != nil && createdBefore(s, by[k]) {
			continue
		}
		out[k], by[k] = formatPR(s), s
	}
	return out
}

// changeHandles names each valley change some run belongs to, so every run
// on it, running or not, shows the same handle: the valley's listing's, which
// is current (a handle lengthens when another change comes to share its
// prefix); else, when the valley can't be asked or doesn't list the change,
// the handle the latest run to record one got from finalize's ready or
// launch's unready.
func changeHandles(states []*run.State, listed map[integration.ChangeKey]integration.Result) map[integration.ChangeKey]string {
	changeOf := changeKeys(states)
	out := make(map[integration.ChangeKey]string)
	by := make(map[integration.ChangeKey]*run.State) // the run whose record names each unlisted change
	for _, s := range states {
		k, ok := changeOf(s)
		if !ok {
			continue
		}
		if c := listed[valleyKey(s)].Change; c != nil {
			out[k] = c.Handle
			continue
		}
		h := recordedHandle(s)
		if h == "" || by[k] != nil && createdBefore(s, by[k]) {
			continue
		}
		out[k], by[k] = h, s
	}
	return out
}

// recordedHandle is the handle a run recorded for its change: from
// finalize's ready, else from launch's unready; "" when neither got one.
func recordedHandle(s *run.State) string {
	if s.Valley != nil && s.Valley.Handle != "" {
		return s.Valley.Handle
	}
	return s.UnreadyHandle()
}

func formatPR(s *run.State) string {
	if s.PRURL == nil {
		return "-"
	}
	url := *s.PRURL
	// Extract PR number from URL
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return "#" + url[i+1:]
		}
	}
	return url
}

// extractPRNumber returns the PR number (e.g. "18") from a run state, or "".
func extractPRNumber(s *run.State) string {
	if s.PRURL == nil {
		return ""
	}
	url := *s.PRURL
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return url[i+1:]
		}
	}
	return ""
}

// extractPRRef returns the full PR URL from a run state (for use with gh CLI),
// or "" if no PRURL is set. Using the full URL ensures gh can find the PR
// regardless of the current working directory.
func extractPRRef(s *run.State) string {
	if s.PRURL == nil {
		return ""
	}
	return *s.PRURL
}

// computeMergeStatus determines overall merge readiness. behind is the base-ahead
// commit count; a would-be-ready-but-behind PR reads "behind N" (rebased at merge)
// so it is never shown as a clean "ready".
func computeMergeStatus(ci, conflicts, reviewDecision string, behind int) string {
	if conflicts == "yes" {
		return "blocked"
	}
	if ci == "failing" {
		return "blocked"
	}
	if strings.EqualFold(reviewDecision, "CHANGES_REQUESTED") {
		return "blocked"
	}
	if ci == "pending" || reviewDecision == "unknown" {
		return "pending"
	}
	if ci == "passing" && conflicts == "none" && (strings.EqualFold(reviewDecision, "APPROVED") || reviewDecision == "") {
		if behind > 0 {
			return fmt.Sprintf("behind %d", behind)
		}
		return "ready"
	}
	return "pending"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runBackendName(s *run.State) string {
	if s.Type == "track" {
		return "-"
	}
	return runBackendModel(s)
}

// runBackendModel is a run's backend and model: the model the worker
// reported, else the one launched with, else "(default model)".
func runBackendModel(s *run.State) string {
	b := s.Backend
	if b == "" {
		b = "claude"
	}
	model := s.ResolvedModel
	if model == "" && s.Model != nil {
		model = *s.Model
	}
	return review.BackendModel(b, model)
}
