package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// A memo records a decision and not why, so a current decision says so.
const noReason = "The stored outcome has no detailed reason. Ask the instance operator for the integrator report.\n"

// writeHuman prints the state as the sentences an operator reads, and how
// to act on it.
func writeHuman(g git, s *status, w io.Writer) int {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	switch s.state {
	case "integrated":
		p("%s (%s): integrated into origin/main.\n", s.branch, short(s.head))
		if s.request != "" {
			p("The request for %s is still present at %s.\n", short(s.request), s.requestRef)
		}
		return writeCheckout(g, s.tip, w)
	case "integrated-recorded":
		p("%s (%s): the integrator recorded it integrated into origin/main.\n", s.branch, short(s.head))
		return writeCheckout(g, s.tip, w)
	case "outdated-request":
		p("%s (%s): latest commit not approved.\n", s.branch, short(s.head))
		p("An older request for %s is still pending at %s.\n", short(s.request), s.requestRef)
		p("Review the latest commit: valley review %s\n", shellQuote(s.branch))
		return 0
	case "outcome-unreadable":
		p("%s: the stored outcome is unreadable; status is unknown.\n", s.branch)
		p("Ask the instance operator to inspect %s.\n", s.outcomeRef)
		return 0
	case "not-requested":
		p("%s: not approved; no pending request for this commit.\n", s.branch)
		if s.memo.decision != "" {
			p("Last recorded decision: %s for %s.\n", s.memo.decision, short(s.memo.head))
		}
		p("Approve it through review: valley review %s\n", shellQuote(s.branch))
		return 0
	}

	p("%s (%s): ", s.branch, short(s.head))
	switch s.state {
	case "awaiting-decision":
		p("awaiting an integrator decision.\n")
	case "awaiting-new-decision":
		p("awaiting a new decision; the target or evidence has changed.\n")
		p("Last recorded decision: %s.\n", s.memo.decision)
	case "stale":
		p("needs attention; the integrator reported stale.\n")
		p("The change conflicts with main, or required evidence does not transfer.\n")
		p(noReason)
	case "rejected":
		p("the integrator rejected the evidence.\n")
		p(noReason)
	case "failed":
		p("the integrator failed while processing the request (%s).\n", s.memo.failedAt)
		p("This does not prove that nothing landed; recording can fail after main advances.\n")
		p(noReason)
	}
	p("Request: %s\n", s.requestRef)
	p("Check again: valley status %s\n", shellQuote(s.branch))
	return 0
}

// writeCheckout says how the local checkout stands against the main a
// change was integrated into, and how to bring it up to date. A checkout
// whose HEAD cannot be read ends the read with git's own status, after
// git has said why.
func writeCheckout(g git, tip string, w io.Writer) int {
	p := func(format string, args ...any) { fmt.Fprintf(w, format, args...) }
	if bare, err := g.output(false, "rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		p("This is a bare repository; there is no local checkout to update.\n")
		return 0
	}
	current, err := g.output(false, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		current = "detached HEAD"
	}
	localHead, err := g.output(false, "rev-parse", "--short", "HEAD")
	if err != nil {
		return exitCode(err)
	}
	p("Local checkout: %s at %s.\n", current, localHead)
	switch {
	case g.succeeds(false, "merge-base", "--is-ancestor", tip, "HEAD"):
		p("Your local checkout already contains origin/main.\n")
	case !g.succeeds(false, "show-ref", "--verify", "--quiet", "refs/heads/main"):
		p("To create a local main checkout:\n")
		p("  git fetch origin\n  git switch --track -c main origin/main\n")
	case g.succeeds(false, "merge-base", "--is-ancestor", tip, "refs/heads/main"):
		p("Local main already contains origin/main. To view it:\n  git switch main\n")
	case !g.succeeds(false, "merge-base", "--is-ancestor", "refs/heads/main", tip):
		p("Local main has diverged from origin/main. Inspect the commits before updating:\n")
		p("  git fetch origin\n  git log --oneline --left-right main...origin/main\n")
	default:
		p("To update your local main checkout:\n")
		if current != "main" {
			p("  git switch main\n")
		}
		p("  git pull --ff-only origin main\n")
	}
	return 0
}

func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// The valley-status/v1 object. docs/architecture.md documents it, and a
// change to any field's name or meaning is a new schema version.
type jsonStatus struct {
	Schema      string       `json:"schema"`
	Change      string       `json:"change"`
	RequestName string       `json:"request_name"`
	Target      string       `json:"target"`
	TargetTip   string       `json:"target_tip"`
	Head        *string      `json:"head"`
	Request     *jsonRequest `json:"request"`
	Outcome     *jsonOutcome `json:"outcome"`
	State       string       `json:"state"`
}

type jsonRequest struct {
	Ref    string `json:"ref"`
	Commit string `json:"commit"`
}

// An outcome record's head and tip are given only when they are object
// ids, and failed_at only for a failed pass: an unknown record is reported
// as present and unread, never as half of a decision.
type jsonOutcome struct {
	Decision string  `json:"decision"`
	Head     *string `json:"head"`
	Tip      *string `json:"tip"`
	FailedAt *string `json:"failed_at"`
}

// writeJSON prints the state as one valley-status/v1 object on one line.
// It is encoded whole before anything is written, so a reader gets the
// object or nothing.
func writeJSON(s *status, w, stderr io.Writer) int {
	out := jsonStatus{
		Schema:      "valley-status/v1",
		Change:      s.branch,
		RequestName: s.change,
		Target:      target,
		TargetTip:   s.tip,
		Head:        optional(s.head),
		State:       s.state,
	}
	if s.request != "" {
		out.Request = &jsonRequest{Ref: s.requestRef, Commit: s.request}
	}
	if s.outcome != "" {
		o := &jsonOutcome{Decision: s.memo.decision, FailedAt: optional(s.memo.failedAt)}
		if isObjectID(s.memo.head) {
			o.Head = &s.memo.head
		}
		if isObjectID(s.memo.tip) {
			o.Tip = &s.memo.tip
		}
		out.Outcome = o
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "valley: could not encode the status: %v\n", err)
		return 1
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		fmt.Fprintf(stderr, "valley: could not write the status: %v\n", err)
		return 1
	}
	return 0
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
