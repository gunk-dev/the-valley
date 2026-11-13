package main

// The check state valley changes --checks and valley pending --checks
// list for the active version of each ready change and each draft that
// has not landed as any version: the checks the policy requires of it,
// and for each the state of the note the integrator would judge it by.
// valley pending reads a draft's only when it lists the drafts, with
// --drafts, and valley review reads it for the change it shows.
//
// A draft's checks are read exactly as a ready change's. The required
// checks are derived as the check worker derives them (worker.go): over
// the head's merge base with main, under both policy layers read at
// main's tip on origin. Each nix check's note is read as coverage reads
// it (coverage.go), from origin's attestation refs for the head's tree,
// verified under the host's published known-signers file. A note is
// about a tree, not a change's state, so a draft made ready at the same
// head lists the same states:
//
//   - passed: the note judged is one coverage would reuse;
//   - failed: the note judged verifies, is signed by a signer who counts
//     in this project, and coverage would not reuse it, because it
//     records the check as failed or because it is the wrong kind, runner
//     or attribute. why says which;
//   - pending: no note about the check verifies and is signed by a signer
//     who counts. why says why a note origin holds does not count, when
//     one is there.
//
// A check with a runner other than nix is listed as other, since the host
// worker does not run it, and it does not count toward the overall state.
// VALLEY_KNOWN_SIGNERS does not narrow what is read here: the listing says
// what the integrator would judge.
//
// It is behind --checks because it costs each repository a read of the
// policy with cue and an attest verify for each note. The reads are
// batched by repository, over the ready changes and drafts together: the
// policy is read once, without asking cue which requirements are
// mandatory, since nothing here shows or decides by a pin; origin's
// attestation refs for every head are listed in one ls-remote and fetched
// in one fetch; and the host's signers are read once. A read that fails
// leaves the changes it was for without check state, and says so on
// stderr; it never reads as no evidence.
//
// Each nix check also says what it cost the host's check worker to build
// for this version, against what it recently cost (checkcost.go), when
// the host's worker telemetry can be read. That read runs while the
// policy is read. When it cannot be read, the fields are left out, stderr
// says why once, and the check states are listed all the same.
//
// The same read tells a check the worker built and that failed from one
// not built yet. attest publishes no note when any check of its batch
// fails, so such a check reads as pending. When the worker's newest build
// of a check for the head's tree failed and no note makes the check
// passed, the check carries the worker's report: its result, when it
// ended, and its attest run (checkcost.go). When a check is pending and
// the worker's newest batch for the tree failed before any check ran, the
// version carries that batch's report. A row and the checks line name a
// pending check as failed in the worker's build, in words apart from a
// failure a note records, and with --checks a see: line names the command
// that reads the worker's records and logs. This is diagnostic, never
// evidence: nothing in it is signed, a check's state and the version's
// come from notes alone, and neither approve nor the integrator reads it.
// When the telemetry cannot be read, nothing is claimed.

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"the-valley/change"
)

// The check states of a version's checks.
const (
	checkPassed  = "passed"
	checkFailed  = "failed"
	checkPending = "pending"
	checkOther   = "other"
)

// jsonVersionChecks is a version's check state, as valley-changes/v1
// writes it under checks.
type jsonVersionChecks struct {
	State    string           `json:"state"`
	Tree     string           `json:"tree"`
	Required []jsonCheckState `json:"required"`
	// BuildMS is the sum of the build times of the checks built for this
	// version, and EvalMS the sum of the evaluations of the runs that
	// built them, when the worker's telemetry holds any (checkcost.go).
	BuildMS *int64 `json:"build_ms,omitempty"`
	EvalMS  *int64 `json:"eval_ms,omitempty"`
	// Worker is the worker's report of its newest batch for this tree,
	// when a nix check is pending and that batch failed before any check
	// ran (addWorkerReports). It is diagnostic: State does not count it.
	Worker *jsonWorkerReport `json:"worker,omitempty"`
}

// jsonWorkerReport is what the worker's telemetry records of a build or a
// batch of its that failed: its result, when it ended, and the attest run
// whose records and logs say why.
type jsonWorkerReport struct {
	Result string `json:"result"`
	Time   string `json:"time"`
	Run    string `json:"run"`
}

// jsonCheckState is one required check and the state of its note.
type jsonCheckState struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Signer string `json:"signer,omitempty"`
	Why    string `json:"why,omitempty"`
	// What the check cost to build, from the worker's telemetry
	// (checkcost.go): this version's build, whether its output was
	// cached, the median of the check's recent builds, whether it has
	// none, and whether this build grew against them.
	BuildMS    *int64 `json:"build_ms,omitempty"`
	Cached     *bool  `json:"cached,omitempty"`
	BaselineMS *int64 `json:"baseline_ms,omitempty"`
	New        bool   `json:"new,omitempty"`
	// OtherCapacity says the check is new because its builds so far ran on
	// other capacity.
	OtherCapacity bool `json:"other_capacity,omitempty"`
	Grown         bool `json:"grown,omitempty"`
	// Worker is the worker's report of its newest build of the check for
	// this tree, when that build failed and no note makes the check
	// passed (addWorkerReports). It is diagnostic: State does not count
	// it.
	Worker *jsonWorkerReport `json:"worker,omitempty"`
}

// unlandedDraft is a draft that has not landed as any version.
func unlandedDraft(s *snapshot, n recorded) bool {
	return n.record.State == change.Draft && !s.isLanded(n)
}

// addChecks gives the active version of each pending change in view its
// check state, and, with drafts, that of each draft in view that has not
// landed, whether view lists it in changes or in drafts. Each also gets
// its build times from the host's worker telemetry, read through c, or
// none when c is nil. view is this repository's.
func (l listed) addChecks(view *jsonRepo, drafts bool, c *costs, stderr io.Writer) {
	if l.err != nil {
		return
	}
	wanted := map[[2]string]bool{}
	for _, n := range l.snap.all {
		if pendingChange(l.snap, n) || drafts && unlandedDraft(l.snap, n) {
			wanted[[2]string{n.record.Target, n.id}] = true
		}
	}
	lists := []*[]jsonChange{&view.Changes}
	if view.Drafts != nil {
		lists = append(lists, view.Drafts)
	}
	var heads []*jsonVersion
	changeOf := map[*jsonVersion]string{}
	for _, list := range lists {
		for i := range *list {
			ch := &(*list)[i]
			if wanted[[2]string{ch.Target, ch.ChangeID}] {
				heads = append(heads, &ch.Versions[ch.Active-1])
				changeOf[heads[len(heads)-1]] = ch.ChangeID
			}
		}
	}
	if len(heads) == 0 {
		return
	}
	warn := func(format string, args ...any) {
		fmt.Fprintf(stderr, "valley: %s: %s\n", l.name, fmt.Sprintf(format, args...))
	}
	if l.snap.tip == "" {
		warn("origin has no main to read the policy at, so no change lists its checks")
		return
	}
	var reading *costRead
	if c != nil {
		reading = c.start(l.g)
	}
	if _, err := l.snap.layers(); err != nil {
		warn("the policy at main did not read, so no change lists its checks: %v", err)
		return
	}
	if _, err := schemaFile(l.g, ""); err != nil {
		warn("%v, so no change lists its checks", err)
		return
	}
	model, err := l.snap.model()
	if err != nil {
		warn("the policy at main did not read, so no change lists its checks: %v", err)
		return
	}
	repo := l.g.dir
	if repo == "" {
		repo = "."
	}

	// Each head's required checks and tree digest.
	type head struct {
		v        *jsonVersion
		change   string
		required []jsonRequired
		asked    []nixCheck
		digest   string
	}
	var read []head
	var patterns []string
	for _, v := range heads {
		say := func(format string, args ...any) {
			warn("v%d of %s lists no checks: %s", v.N, v.Head, fmt.Sprintf(format, args...))
		}
		base, err := l.g.output(true, "merge-base", l.snap.tip, v.Head)
		if err != nil {
			say("it shares no history with main")
			continue
		}
		doc, err := deriveUnder(l.g, model, base, v.Head)
		if err != nil {
			say("its checks did not derive: %v", err)
			continue
		}
		digest, err := treeDigest(l.g, repo, v.Head)
		if err != nil {
			say("%v", err)
			continue
		}
		h := head{v: v, change: changeOf[v], required: doc.Required, digest: digest}
		for _, q := range doc.Required {
			if q.Runner == "nix" {
				h.asked = append(h.asked, nixCheck{q.Name, q.Attribute})
			}
		}
		read = append(read, h)
		patterns = append(patterns, attestationRefs+digest+"/*")
	}
	if len(read) == 0 {
		return
	}

	// The evidence origin holds for every head, listed and fetched at once.
	o, err := listOrigin(l.g, patterns...)
	if err != nil {
		warn("could not list the evidence origin holds, so no change lists its checks")
		return
	}
	var oids []string
	for _, oid := range o.refs {
		oids = append(oids, oid)
	}
	if len(oids) > 0 {
		if err := fetch(l.g, oids...); err != nil {
			warn("could not fetch the evidence origin holds, so no change lists its checks")
			return
		}
	}
	keys, p, unsigned := hostSigners(l.g)
	if unsigned == "" {
		defer os.Remove(keys)
	}
	var here counting
	if p != nil {
		here = counting{project: project(l.g), integrator: p.name, projects: p.projects}
	}
	timed := c != nil && c.wait(reading, warn)

	for _, h := range read {
		stored := storedFor(h.digest, o.refs)
		var judged map[string]judgment
		unjudged := unsigned
		if unjudged == "" && len(stored) > 0 && len(h.asked) > 0 {
			judged, unjudged, err = judge(l.g, repo, h.v.Head, keys, "", here, stored, o.refs, h.asked)
			if err != nil {
				warn("v%d of %s lists no checks: %v", h.v.N, h.v.Head, err)
				continue
			}
		}
		h.v.Checks = checkStates(h.digest, h.required, judged, unsigned, unjudged)
		if timed {
			addCosts(h.v.Checks, h.required, l.name, h.change, h.v.N, reading.builds)
			addWorkerReports(h.v.Checks, h.required, l.name, h.change, h.v.N, reading.builds, reading.batches)
		}
	}
}

// checkStates is a version's check state, from the note judged for each
// nix check. unsigned says why no note could be verified, and unjudged why
// the integrator cannot judge the tree's evidence at all.
func checkStates(digest string, required []jsonRequired, judged map[string]judgment, unsigned, unjudged string) *jsonVersionChecks {
	out := &jsonVersionChecks{State: checkPassed, Tree: digest, Required: []jsonCheckState{}}
	for _, q := range required {
		s := jsonCheckState{Name: q.Name}
		j, ok := judged[q.Name]
		switch {
		case q.Runner != "nix":
			s.State = checkOther
		case unsigned != "":
			s.State, s.Why = checkPending, unsigned
		case unjudged != "":
			s.State, s.Why = checkFailed, unjudged+", so the integrator cannot judge this tree's evidence"
		case !ok:
			s.State = checkPending
		case j.note.refusal != "":
			s.State, s.Why = checkPending, j.note.refusal
		case j.why != "":
			s.State, s.Signer, s.Why = checkFailed, j.note.signer, j.why
		default:
			s.State, s.Signer = checkPassed, j.note.signer
		}
		switch {
		case s.State == checkFailed:
			out.State = checkFailed
		case s.State == checkPending && out.State == checkPassed:
			out.State = checkPending
		}
		out.Required = append(out.Required, s)
	}
	return out
}

// checksLine is valley pending's line for a version's check state, with
// what the worker reports of the pending checks' failed builds
// (workerSays), and what its checks cost to build when that is known
// (costLine): "checks: 18 passed, 1 failed (valley-request) in 9m10s
// (eval 24s)", or "checks: 18 passed, 1 pending (valley-request failed in
// the worker's build, 5m ago)".
func checksLine(c *jsonVersionChecks) string {
	if len(c.Required) == 0 {
		return "checks: none required"
	}
	count := map[string]int{}
	var failed []string
	for _, q := range c.Required {
		count[q.State]++
		if q.State == checkFailed {
			failed = append(failed, q.Name)
		}
	}
	worker := workerSays(c)
	var parts []string
	for _, state := range []string{checkPassed, checkFailed, checkPending, checkOther} {
		if count[state] == 0 {
			continue
		}
		part := fmt.Sprintf("%d %s", count[state], state)
		if state == checkFailed {
			part += " (" + strings.Join(failed, ", ") + ")"
		}
		if state == checkPending && worker != "" {
			part += " (" + worker + ")"
		}
		parts = append(parts, part)
	}
	return "checks: " + strings.Join(parts, ", ") + costLine(c)
}

// workerSays is what the worker reports of a version's pending checks, in
// words that tell it from a failure a note records: the checks whose
// newest build failed, and when the newest of those builds ended, then
// the batch that failed before any check ran, and when: "valley-request
// failed in the worker's build, 5m ago; the worker's batch failed, 1m
// ago". It is "" when the worker reports neither.
func workerSays(c *jsonVersionChecks) string {
	var names []string
	newest := ""
	for _, q := range c.Required {
		if q.State == checkPending && q.Worker != nil {
			names = append(names, q.Name)
			newest = max(newest, q.Worker.Time)
		}
	}
	var says []string
	switch len(names) {
	case 0:
	case 1:
		says = append(says, names[0]+" failed in the worker's build"+agoText(newest))
	default:
		says = append(says, firstFew(names)+" failed in the worker's builds"+agoText(newest))
	}
	if c.Worker != nil {
		says = append(says, "the worker's batch failed"+agoText(c.Worker.Time))
	}
	return strings.Join(says, "; ")
}

// agoText is ", 5m ago" for a record's time, or "" when it does not read.
func agoText(stamp string) string {
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return ""
	}
	return ", " + ago(time.Now().Unix(), at.Unix()) + " ago"
}

// rowChecks is a version's checks as a row says them: in checkCount's
// words, or, when no note records a failure and the worker reports one,
// with the glyph ✗, what it reports (workerSays).
func rowChecks(c *jsonVersionChecks) (glyph, says string) {
	if c != nil && c.State == checkPending {
		if w := workerSays(c); w != "" {
			return "✗", w
		}
	}
	return "", checkCount(c)
}

// workerSee is the line under a version's checks line that reads what the
// worker reports, when it reports a failure (workerSays), or "".
func workerSee(c *jsonVersionChecks) string {
	if c == nil || workerSays(c) == "" {
		return ""
	}
	return "see: valley telemetry read --source worker --logs"
}

// runCheckLine prints valley pending's checks line for the active version
// of each change named, for review to show above its diff, one line each:
//
//	<change ID>	checks: 21 passed in 9m10s (eval 24s)
//
// A ready change and a draft each have one. A withdrawn change, or one
// that has landed as any version, has no check state, and so no line. What could not be read is said on stderr,
// as valley pending --checks says it, and leaves the line out.
func runCheckLine(g git, args []string, stdout, stderr io.Writer) error {
	for _, a := range args {
		if !change.ValidID(a) {
			return failure("usage: valley checkline <change ID>...")
		}
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	changes, err := activeChecks(g, args, newCosts(), stderr)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, c := range changes {
		if v := c.Versions[c.Active-1]; v.Checks != nil {
			fmt.Fprintf(&b, "%s\t%s\n", c.ChangeID, readable(checksLine(v.Checks)))
		}
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}

// activeChecks is each change named that is ready or a draft and has not
// landed, in this repository, with its active version's check state, as
// valley pending --drafts --checks reads it, and with build times read
// through c when it is given.
func activeChecks(g git, ids []string, c *costs, stderr io.Writer) ([]jsonChange, error) {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	list, _, err := sources(g, listing{}, stderr)
	if err != nil {
		return nil, err
	}
	repos := readSources(list, newVerifier())
	defer closeSources(repos)
	r := repos[0]
	if r.err != nil {
		return nil, r.err
	}
	named := func(s *snapshot, n recorded) bool {
		return wanted[n.id] && (pendingChange(s, n) || unlandedDraft(s, n))
	}
	view := r.repoView(handles(repos), named, unlanded)
	r.addChecks(&view, true, c, stderr)
	return view.Changes, nil
}
