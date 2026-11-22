package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"the-valley/flow/internal/checksfix"
	"the-valley/flow/internal/event"
	"the-valley/flow/internal/integration"
	"the-valley/flow/internal/restack"
	"the-valley/flow/internal/run"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "List what waits on the coordinator, oldest first",
	Long: `Lists what waits on the coordinator, oldest first, each with how long it
has waited and the one command that moves it on; then, as a group of its
own, what waits on the operator. Prints nothing and exits 0 when nothing
waits.

It waits on the coordinator when nothing else will move it:
needs-attention, awaiting-review (a draft to review, then valley flow
ready), stale, rejected, failed, draft (no run working on it), and
checks-failed or conflicting once flow's dispatch for it stopped short,
its run finished without a new head, or the change isn't flow's. It waits
on the operator when only approval or integration moves it:
awaiting-approval, awaiting-integration, uncertain, and a PR ready to
merge that nobody approved; that group carries no commands. Work in
progress and done work are left out. A change is listed once, under its
newest run.

AGE runs from the event that put the item in its state (agent:change-draft
for awaiting-review, agent:change-ready for awaiting-approval, and so on),
and reads - when no event marks it. valley flow watch --queue waits until
the queue differs from the one it last printed.`,
	Example: `  valley flow queue
  valley flow queue --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")
		store, err := sessionStore()
		if err != nil {
			return err
		}
		q, _, err := computeQueue(cmd.Context(), store)
		if err != nil {
			return err
		}
		if asJSON {
			return q.writeJSON(cmd.OutOrStdout(), time.Now())
		}
		q.write(cmd.OutOrStdout(), time.Now())
		return nil
	},
}

func init() {
	queueCmd.Flags().Bool("json", false, "Print the queue as one JSON object: {\"coordinator\": [...], \"operator\": [...]}")
	rootCmd.AddCommand(queueCmd)
}

// party is whose move a state is.
type party int

const (
	partyNone        party = iota // done: nothing is left to do
	partyFlow                     // in progress: an agent, the pipeline, the integrator or flow's dispatchers move it
	partyCoordinator              // only the coordinator moves it
	partyOperator                 // only the operator's approval or integration moves it
)

// stateRule says, for a state valley flow status shows, whose move it is and
// which events put a run or change in it.
type stateRule struct {
	party   party
	entered []string
}

// stateRules says whose move each state is, and which events date it. A
// state missing from it is done. queueState refines it for failed checks,
// restack conflicts and PRs ready to merge.
var stateRules = map[string]stateRule{
	"running":              {party: partyFlow},
	"working":              {party: partyFlow},
	"tracking":             {party: partyFlow},
	"active":               {party: partyFlow},
	"pr-created":           {party: partyFlow, entered: []string{event.AgentPRCreated}},
	"approved":             {party: partyFlow, entered: []string{event.ChangeApproved}},
	"needs-attention":      {party: partyCoordinator, entered: []string{event.AgentNeedsAttention, event.AgentSharedGitChanged}},
	"awaiting-review":      {party: partyCoordinator, entered: []string{event.AgentChangeDraft}},
	"checks-failed":        {party: partyCoordinator, entered: []string{event.ChangeChecksFailed, event.ChangeChecksStalled, event.ChangeStale}},
	"conflicting":          {party: partyCoordinator, entered: []string{event.ChangeRestackStalled, event.ChangeStale}},
	"stale":                {party: partyCoordinator, entered: []string{event.ChangeStale}},
	"rejected":             {party: partyCoordinator, entered: []string{event.ChangeRejected}},
	"failed":               {party: partyCoordinator, entered: []string{event.ChangeFailed}},
	"draft":                {party: partyCoordinator, entered: []string{event.AgentCompleted, event.AgentNeedsAttention}},
	"awaiting-approval":    {party: partyOperator, entered: []string{event.AgentChangeReady, event.ChangeNewVersion}},
	"awaiting-integration": {party: partyOperator, entered: []string{event.AgentBranchPushed}},
	"uncertain":            {party: partyOperator},
	"ready-to-merge":       {party: partyOperator, entered: []string{event.PRAwaitingApproval}},
}

// stateClock dates states by the session's events: when each event type
// was last emitted for each run, change and PR, and about which head.
type stateClock struct {
	latest map[string]map[string]stamp // subject → event type → latest
}

// stamp is when an event was emitted, and the head it names, if any.
type stamp struct {
	at   time.Time
	head string
}

func newStateClock() *stateClock {
	return &stateClock{latest: make(map[string]map[string]stamp)}
}

// loadStateClock reads the events of the session in dir; a log it can't
// read dates nothing.
func loadStateClock(dir string) *stateClock {
	c := newStateClock()
	events, err := event.NewLog(dir).Read()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: reading events to date states: %v\n", err)
	}
	for _, e := range events {
		c.observe(e)
	}
	return c
}

// loadClock is the state clock of store's session; empty for a store with
// no session directory.
func loadClock(store run.StateStore) *stateClock {
	if hds, ok := store.(*run.HomeDirStore); ok {
		return loadStateClock(hds.BaseDir())
	}
	return newStateClock()
}

// Subjects an event is about: its run (a run's ID, a PR's number or a
// change's handle or ID), its change (by change ID), and its PR (by the
// repo-qualified key every PR event carries, owner/repo#number).
func runSubject(id string) string    { return "run:" + id }
func changeSubject(id string) string { return "change:" + id }
func prSubject(key string) string    { return "pr:" + key }

// observe records evt.
func (c *stateClock) observe(evt event.Event) {
	t, err := time.Parse(time.RFC3339, evt.Timestamp)
	if err != nil {
		return
	}
	subjects := []string{runSubject(evt.RunID)}
	if id, _ := evt.Data["change_id"].(string); id != "" {
		subjects = append(subjects, changeSubject(id))
	}
	if key, _ := evt.Data["key"].(string); strings.Contains(key, "#") {
		subjects = append(subjects, prSubject(key))
	}
	head, _ := evt.Data["head"].(string)
	for _, s := range subjects {
		m := c.latest[s]
		if m == nil {
			m = make(map[string]stamp)
			c.latest[s] = m
		}
		if t.After(m[evt.Type].at) {
			m[evt.Type] = stamp{t, head}
		}
	}
}

// since is when run s, or its change, entered state at head: the latest
// event that enters it, about the run itself for needs-attention, its PR
// for ready-to-merge, and the run or its change otherwise, and not about
// another head; zero, unknown, when none is.
func (c *stateClock) since(s *run.State, state, head string) time.Time {
	rule, ok := stateRules[state]
	if c == nil || !ok {
		return time.Time{}
	}
	subjects := []string{runSubject(s.ID)}
	switch {
	case state == "ready-to-merge":
		subjects = nil
		if key := runPRKey(s); key != "" {
			subjects = []string{prSubject(key)}
		}
	case state != "needs-attention" && s.ChangeID != "":
		subjects = append(subjects, changeSubject(s.ChangeID))
	}
	var out time.Time
	for _, sub := range subjects {
		for _, t := range rule.entered {
			st := c.latest[sub][t]
			if st.head != "" && head != "" && st.head != head {
				continue
			}
			if st.at.After(out) {
				out = st.at
			}
		}
	}
	return out
}

// rowHead is the head row r stands at: its version's, else its push's.
func rowHead(r runRow) string {
	switch {
	case r.change != nil:
		return r.change.Head
	case r.run.PushedSHA != nil:
		return *r.run.PushedSHA
	}
	return r.run.HeadSHA
}

// runCreated is when run s was launched; zero when its record doesn't say.
func runCreated(s *run.State) time.Time {
	t, _ := time.Parse(time.RFC3339, s.CreatedAt)
	return t
}

// runPRKey names run s's PR qualified by its repo, owner/repo#number, as PR
// events name it; "" for a run without one.
func runPRKey(s *run.State) string {
	if pr := runPRNumber(s); pr != "" {
		if key := changeKey(runRepoKey(s), pr, s.ChangeID); strings.Contains(key, "#") {
			return key
		}
	}
	return ""
}

// dispatchFinished reports whether the run flow dispatched as runID, for a
// failed or conflicting head, has finished without leaving a new one: its
// record reads it not running, and it pushed nothing or head again; or
// cleanup removed its record, as cleared says. A dispatch that still reads
// dispatched is for the change's current head, so a removed run left none
// newer. A run with no record that cleanup didn't remove is still
// launching: the failed-check dispatcher records a dispatch before it
// launches the run.
func dispatchFinished(runID, head string, states []*run.State, statuses []string, cleared map[string]bool) bool {
	if runID == "" {
		return false
	}
	for i, s := range states {
		if s.ID == runID {
			return statuses[i] != "running" && (s.PushedSHA == nil || *s.PushedSHA == head)
		}
	}
	return cleared[runID]
}

// queueItem is one thing that waits on the coordinator or the operator.
type queueItem struct {
	party    party
	key      string // what the item is: a run, a change or a PR
	Run      string // the run the item names, whose ID the next command takes
	Change   string // the change as valley flow status names it: handle, PR number or change ID prefix
	ChangeID string
	Project  string
	Head     string
	State    string
	Checks   string    // the operator's items: the checks' or CI's result, when there is one
	Since    time.Time // zero when no event dates the state
	Next     string    // the one command that moves it on; none for the operator's
	Note     string    // the failed checks, where a restack conflict stands, or the checks' result
}

// queue is what waits, each group oldest first.
type queue struct {
	coordinator, operator []queueItem
}

// computeQueue reads where the session's runs stand, as valley flow status
// does, and returns what waits on the coordinator and on the operator.
// provisional is set when a run record didn't parse, as one caught while
// it is rewritten doesn't, so the queue may lack that run.
func computeQueue(ctx context.Context, store run.StateStore) (q queue, provisional bool, err error) {
	var states []*run.State
	if hds, ok := store.(*run.HomeDirStore); ok {
		var unparsed []string
		states, unparsed, err = hds.Scan()
		provisional = len(unparsed) > 0
	} else {
		states, err = store.List()
	}
	if err != nil {
		return queue{}, false, err
	}
	clock := loadClock(store)
	rows, report, err := runRows(ctx, store, states, clock, io.Discard)
	if err != nil {
		return queue{}, false, err
	}
	return buildQueue(rows, report, clock), provisional, nil
}

// queueState is the one rule for whose move a row is, and the state it
// stands in for that. A ready change whose checks failed, or whose restack
// conflict stands, is flow's while its dispatcher works on it: a dispatch
// is due, or the run it dispatched is alive or left a new head. It is the
// coordinator's once the dispatcher stops short, its run finished without
// a new head, or the change isn't flow's; it waits on the operator's
// approval only with neither. A PR ready to merge that nobody approved
// waits on the operator.
func queueState(r runRow) (string, party) {
	state := r.status
	switch {
	case state == "awaiting-approval" && r.ci == "failing":
		state = "checks-failed"
	case (state == "awaiting-approval" || state == "draft") && r.restack != nil:
		// A restack conflict stands, or flow's dispatch returned the
		// change to draft to resolve it.
		state = "conflicting"
	case (state == "pr-created" || state == "tracking" && r.run.PRURL != nil) && r.merge == "ready" && !r.approved && !r.draftPR:
		state = "ready-to-merge"
	}
	switch {
	case state == "checks-failed" && r.checks != nil && checksAtWork(r):
		return state, partyFlow
	case state == "conflicting" && r.restack != nil && restackAtWork(r):
		return state, partyFlow
	}
	return state, stateRules[state].party
}

// checksAtWork reports whether flow's failed-check dispatch works on row
// r's change: a fix run is due, a run is on it, or the fix run it
// dispatched hasn't finished without a new head.
func checksAtWork(r runRow) bool {
	switch r.checks.State {
	case checksfix.StateDue, checksfix.StateBusy:
		return true
	case checksfix.StateDispatched:
		return !r.checksDone
	}
	return false
}

// restackAtWork reports whether flow's restack dispatch works on row r's
// conflict: anything short of stopping, refusing or leaving it alone,
// unless the resolution run it dispatched finished without a new head.
func restackAtWork(r runRow) bool {
	switch r.restack.State {
	case restack.StateStopped, restack.StateRefused, restack.StateHeld:
		return false
	case restack.StateDispatched:
		return !r.restackDone
	}
	return true
}

// speaksForChange reports whether row r stands for its change as the
// valley lists it now: its status is its version's, the active one, or a
// run is at work on the change. A run that exited without pushing, or
// whose attention is its own, speaks for no version.
func speaksForChange(r runRow) bool {
	return r.change != nil || r.status == "running" || r.status == "working"
}

// buildQueue returns what waits among rows. A run's own attention is its
// item; everything else is its change's, listed once under its newest run.
// A valley change with runs that read older-version, as when the restacker
// made its active version, and none that speaks for it (speaksForChange)
// stands where the valley's listing says.
func buildQueue(rows []runRow, report integration.Report, clock *stateClock) queue {
	states := make([]*run.State, len(rows))
	for i, r := range rows {
		states[i] = r.run
	}
	changeOf := changeKeys(states)
	type slot struct {
		row   runRow
		state string
		party party
	}
	seen := make(map[string]slot)
	var order []string
	older := make(map[integration.ChangeKey][]runRow) // changes with runs that read older-version
	current := make(map[integration.ChangeKey]bool)   // changes some run speaks for
	for _, r := range rows {
		k, inChange := changeOf(r.run)
		if inChange && r.status == "older-version" {
			older[k] = append(older[k], r)
			continue
		}
		if inChange && speaksForChange(r) {
			current[k] = true
		}
		state, p := queueState(r)
		if p != partyCoordinator && p != partyOperator {
			continue
		}
		id := "run:" + r.run.ID
		switch key := runPRKey(r.run); {
		case key != "" && state == "ready-to-merge":
			// A PR's state is one item, whichever of its runs, an agent's
			// or a track record, speaks for it.
			id = "pr:" + key
		case inChange && state != "needs-attention":
			// A run's attention is its own; any other state, its change's.
			id = fmt.Sprintf("change:%s/%s", k.Repo, k.ChangeID)
		}
		if prev, ok := seen[id]; ok && !createdBefore(prev.row.run, r.run) {
			continue
		} else if !ok {
			order = append(order, id)
		}
		seen[id] = slot{r, state, p}
	}
	olderKeys := make([]integration.ChangeKey, 0, len(older))
	for k := range older {
		olderKeys = append(olderKeys, k)
	}
	sort.Slice(olderKeys, func(i, j int) bool {
		return olderKeys[i].Repo+"/"+olderKeys[i].ChangeID < olderKeys[j].Repo+"/"+olderKeys[j].ChangeID
	})
	for _, k := range olderKeys {
		rs := older[k]
		if current[k] {
			continue
		}
		newest := rs[0]
		for _, r := range rs[1:] {
			if createdBefore(newest.run, r.run) {
				newest = r
			}
		}
		view := report.Changes[valleyKey(newest.run)]
		if view.Change == nil {
			continue
		}
		r := newest
		r.status, r.change, r.ci = pushedStatus(view.Verdict, true), view.Change, "-"
		if c := checksCI(view.Change.Checks); c != "" {
			r.ci = c
		}
		state, p := queueState(r)
		if p != partyCoordinator && p != partyOperator {
			continue
		}
		r.since = clock.since(r.run, state, rowHead(r))
		id := fmt.Sprintf("change:%s/%s", k.Repo, k.ChangeID)
		order = append(order, id)
		seen[id] = slot{r, state, p}
	}

	var q queue
	for _, id := range order {
		sl := seen[id]
		it := newQueueItem(sl.row, sl.state, sl.party)
		it.key = id
		if sl.party == partyCoordinator {
			q.coordinator = append(q.coordinator, it)
		} else {
			q.operator = append(q.operator, it)
		}
	}
	// Oldest first; an item no event dates goes last, as the newest.
	for _, items := range [][]queueItem{q.coordinator, q.operator} {
		sort.SliceStable(items, func(i, j int) bool {
			a, b := items[i].Since, items[j].Since
			return !a.IsZero() && (b.IsZero() || a.Before(b))
		})
	}
	return q
}

// newQueueItem is row r's item in state, dated as the row is.
func newQueueItem(r runRow, state string, p party) queueItem {
	s := r.run
	it := queueItem{party: p, Run: s.ID, ChangeID: s.ChangeID, State: state, Since: r.since, Head: rowHead(r)}
	if r.label != "-" {
		it.Change = r.label
	}
	if r.change != nil {
		it.Project = r.change.Repo
		if r.change.Handle != "" {
			it.Change = r.change.Handle
		}
	}
	if it.Project == "" && s.TargetRepo != nil {
		it.Project = *s.TargetRepo
	}
	switch {
	case state == "checks-failed" && r.change != nil && r.change.Checks != nil:
		it.Note = strings.Join(r.change.Checks.FailedNames(), ", ")
	case state == "conflicting" && r.restack != nil:
		it.Note = r.restack.Summary()
	case p == partyOperator && r.ci != "" && r.ci != "-":
		it.Checks = r.ci
		it.Note = "checks " + r.ci
	}
	if p == partyCoordinator {
		it.Next = nextCommand(s, state, it.Head)
	}
	return it
}

// nextCommand is the one command that moves run s, in state, on.
func nextCommand(s *run.State, state, head string) string {
	repo := ""
	if s.TargetRepo != nil && *s.TargetRepo != "" {
		repo = " --repo " + shellWord(*s.TargetRepo)
	}
	switch state {
	case "needs-attention":
		if s.SharedGitChanged() {
			return "valley flow status"
		}
		return "valley flow launch" + repo + " --resume-from " + s.ID + ` "<brief>"`
	case "awaiting-review":
		return "valley flow ready " + s.ID + " --head " + head
	}
	return "valley flow launch" + repo + " --change " + s.ID + ` "<brief>"`
}

// glyph marks an item: ✗ for a failure, ◇ for anything else that waits.
func (it queueItem) glyph() string {
	switch it.State {
	case "checks-failed", "rejected", "failed":
		return "✗"
	}
	return "◇"
}

// line is the item's verdict line: what it is, its state, how long it has
// waited, its head and any note.
func (it queueItem) line(now time.Time) string {
	name := it.Change
	if name == "" {
		name = it.Run
	}
	if it.Project != "" {
		name = shortRepoName(it.Project) + " " + name
	}
	parts := []string{it.glyph() + " " + name, it.State, "for " + formatAge(it.Since, now)}
	if it.Head != "" {
		parts = append(parts, "head "+shortSHA(it.Head))
	}
	line := strings.Join(parts, " · ")
	// The note gets what is left of 80 columns.
	if room := 80 - len([]rune(line)) - len(" · "); it.Note != "" && room > 1 {
		line += " · " + truncateLine(it.Note, room)
	}
	return line
}

// digest is a stable hash of what waits: each item's group, identity,
// state and head, and for the operator's its checks. Ages, notes and order
// are left out: they change without anything entering or leaving a state.
func (q queue) digest() string {
	var lines []string
	for _, it := range q.coordinator {
		lines = append(lines, strings.Join([]string{"coordinator", it.key, it.State, it.Head}, "\x00"))
	}
	for _, it := range q.operator {
		lines = append(lines, strings.Join([]string{"operator", it.key, it.State, it.Head, it.Checks}, "\x00"))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:8])
}

// write prints the queue: the coordinator's items, each with the command
// that moves it on, then the operator's; nothing when nothing waits.
func (q queue) write(w io.Writer, now time.Time) {
	groups := []struct {
		title string
		items []queueItem
	}{{"Waiting on the coordinator", q.coordinator}, {"Waiting on the operator", q.operator}}
	first := true
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		if !first {
			fmt.Fprintln(w)
		}
		first = false
		fmt.Fprintln(w, g.title)
		for _, it := range g.items {
			fmt.Fprintln(w, it.line(now))
			if it.Next != "" {
				verb := "run"
				if it.Next == "valley flow status" {
					verb = "see"
				}
				fmt.Fprintf(w, "  %s: %s\n", verb, it.Next)
			}
		}
	}
}

// jsonValue is the queue as valley flow queue --json prints it, with each
// item's age in seconds: null, as its since is, when no event dates it.
func (q queue) jsonValue(now time.Time) map[string][]map[string]interface{} {
	conv := func(items []queueItem) []map[string]interface{} {
		out := make([]map[string]interface{}, 0, len(items))
		for _, it := range items {
			m := map[string]interface{}{"run": it.Run, "state": it.State, "since": nil, "age_seconds": nil}
			if !it.Since.IsZero() {
				m["since"] = it.Since.UTC().Format(time.RFC3339)
				m["age_seconds"] = int64(max(now.Sub(it.Since), 0).Seconds())
			}
			for k, v := range map[string]string{"change": it.Change, "change_id": it.ChangeID, "project": it.Project,
				"head": it.Head, "checks": it.Checks, "next": it.Next, "note": it.Note} {
				if v != "" {
					m[k] = v
				}
			}
			out = append(out, m)
		}
		return out
	}
	return map[string][]map[string]interface{}{"coordinator": conv(q.coordinator), "operator": conv(q.operator)}
}

// writeJSON prints the queue as one JSON object, its groups empty lists
// when nothing waits.
func (q queue) writeJSON(w io.Writer, now time.Time) error {
	data, err := json.Marshal(q.jsonValue(now))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
