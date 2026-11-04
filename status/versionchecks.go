package main

// The check state valley changes --checks and valley pending --checks
// list for the active version of each ready change whose active version
// has not landed: the checks the policy requires of it, and for each the
// state of the note the integrator would judge it by.
//
// The required checks are derived as the check worker derives them
// (worker.go): over the head's merge base with main, under both policy
// layers read at main's tip on origin. Each nix check's note is read as
// coverage reads it (coverage.go), from origin's attestation refs for the
// head's tree, verified under the host's published known-signers file:
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
// It is behind --checks because it costs seconds a repository: reading
// the policy with cue is most of it, and attest verify for each note the
// rest. The reads are batched by repository: the policy is read once,
// origin's attestation refs for every head are listed in one ls-remote and
// fetched in one fetch, and the host's signers are read once. A read that fails
// leaves the changes it was for without check state, and says so on
// stderr; it never reads as no evidence.
//
// Each nix check also says what it cost the host's check worker to build
// for this version, against what it recently cost (checkcost.go), when
// the host's worker telemetry can be read. That read runs while the
// policy is read. When it cannot be read, the fields are left out, stderr
// says why once, and the check states are listed all the same.

import (
	"fmt"
	"io"
	"os"
	"strings"

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
}

// addChecks gives the active version of each pending change in view its
// check state, and its build times from the host's worker telemetry,
// read through c. view is this repository's.
func (l listed) addChecks(view *jsonRepo, c *costs, stderr io.Writer) {
	if l.err != nil {
		return
	}
	pending := map[[2]string]bool{}
	for _, n := range l.snap.all {
		if pendingChange(l.snap, n) {
			pending[[2]string{n.record.Target, n.id}] = true
		}
	}
	var heads []*jsonVersion
	changeOf := map[*jsonVersion]string{}
	for i := range view.Changes {
		ch := &view.Changes[i]
		if pending[[2]string{ch.Target, ch.ChangeID}] {
			heads = append(heads, &ch.Versions[ch.Active-1])
			changeOf[heads[len(heads)-1]] = ch.ChangeID
		}
	}
	if len(heads) == 0 {
		return
	}
	warn := func(format string, args ...any) {
		fmt.Fprintf(stderr, "valley: %s: %s\n", l.name, fmt.Sprintf(format, args...))
	}
	if l.snap.tip == "" {
		warn("origin has no main to read the policy at, so no ready change lists its checks")
		return
	}
	reading := c.start(l.g)
	instance, layer, done, err := policyLayers(l.g, l.snap.tip)
	defer done()
	if err != nil {
		warn("the policy at main did not read, so no ready change lists its checks: %v", err)
		return
	}
	schema, err := schemaFile(l.g, "")
	if err != nil {
		warn("%v, so no ready change lists its checks", err)
		return
	}
	model, err := policyModel(schema, instance, layer)
	if err != nil {
		warn("the policy at main did not read, so no ready change lists its checks: %v", err)
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
		warn("could not list the evidence origin holds, so no ready change lists its checks")
		return
	}
	var oids []string
	for _, oid := range o.refs {
		oids = append(oids, oid)
	}
	if len(oids) > 0 {
		if err := fetch(l.g, oids...); err != nil {
			warn("could not fetch the evidence origin holds, so no ready change lists its checks")
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
	builds, timed := c.wait(reading, warn)

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
			addCosts(h.v.Checks, h.required, l.name, h.change, h.v.N, builds)
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
// what its checks cost to build when that is known (costLine):
// "checks: 18 passed, 1 failed (valley-request) in 9m10s (eval 24s)".
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
	var parts []string
	for _, state := range []string{checkPassed, checkFailed, checkPending, checkOther} {
		if count[state] == 0 {
			continue
		}
		part := fmt.Sprintf("%d %s", count[state], state)
		if state == checkFailed {
			part += " (" + strings.Join(failed, ", ") + ")"
		}
		parts = append(parts, part)
	}
	return "checks: " + strings.Join(parts, ", ") + costLine(c)
}

// runCheckLine prints valley pending's checks line for the active version
// of each change named, for review to show above its diff, one line each:
//
//	<change ID>	checks: 21 passed in 9m10s (eval 24s)
//
// A change that is not ready, or whose active version has landed, has no
// check state, and so no line. What could not be read is said on stderr,
// as valley pending --checks says it, and leaves the line out.
func runCheckLine(g git, args []string, stdout, stderr io.Writer) error {
	wanted := map[string]bool{}
	for _, a := range args {
		if !change.ValidID(a) {
			return failure("usage: valley-go checkline <change ID>...")
		}
		wanted[a] = true
	}
	if !g.succeeds(true, "rev-parse", "--git-dir") {
		return failure("not a git repository")
	}
	list, _, err := sources(g, listing{}, stderr)
	if err != nil {
		return err
	}
	repos := readSources(list, newVerifier())
	r := repos[0]
	if r.err != nil {
		return r.err
	}
	named := func(s *snapshot, n recorded) bool { return wanted[n.id] && pendingChange(s, n) }
	view := r.repoView(handles(repos), named, unlanded)
	r.addChecks(&view, newCosts(), stderr)
	var b strings.Builder
	for _, c := range view.Changes {
		if v := c.Versions[c.Active-1]; v.Checks != nil {
			fmt.Fprintf(&b, "%s\t%s\n", c.ChangeID, readable(checksLine(v.Checks)))
		}
	}
	_, err = io.WriteString(stdout, b.String())
	return err
}
