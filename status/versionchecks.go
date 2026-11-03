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

import (
	"fmt"
	"io"
	"os"
	"strings"
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
}

// jsonCheckState is one required check and the state of its note.
type jsonCheckState struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Signer string `json:"signer,omitempty"`
	Why    string `json:"why,omitempty"`
}

// addChecks gives the active version of each pending change in view its
// check state. view is this repository's.
func (l listed) addChecks(view *jsonRepo, stderr io.Writer) {
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
	for i := range view.Changes {
		c := &view.Changes[i]
		if pending[[2]string{c.Target, c.ChangeID}] {
			heads = append(heads, &c.Versions[c.Active-1])
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
		h := head{v: v, required: doc.Required, digest: digest}
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

// checksLine is valley pending's line for a version's check state:
// "checks: 18 passed, 1 failed (valley-request)".
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
	return "checks: " + strings.Join(parts, ", ")
}
