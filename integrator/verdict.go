package main

// The verdict: the whole of what integration decides, as a function of
// data. Nothing here reads git, runs nix, or looks at a clock — a transport
// gathers all of that into a Snapshot first, and this file turns the
// Snapshot into a decision. That split is what makes the rules of
// dcr-439b771 testable without a repository, and it is why the refs
// transport below is the only thing that has to change when a second one
// arrives.
//
// Integration is optimistic concurrency control over content-addressed
// evidence. A change is a transaction; its snapshot is the base tree its
// attestations were produced over; its read set is the union of its
// required checks' input closures; its write set is its delta. The
// integrator is the commit point, and the commit rule has two parts: the
// delta must apply cleanly to the current tip, and per required check the
// evidence must transfer.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Outcome is what the integrator does with a change.
type Outcome string

const (
	// Land: the delta applies and every required check's evidence stands.
	Land Outcome = "land"

	// Stale: the change is behind, not wrong. Either the delta no longer
	// applies or some evidence no longer transfers. The request stays put
	// and retry is partial — only the named checks need new evidence.
	Stale Outcome = "stale"

	// Reject: the evidence is not evidence. A note whose signature does
	// not check out, text that is not the written form of what it says, or
	// a statement about a different tree is not a staleness problem, and
	// re-attesting would not fix it.
	Reject Outcome = "reject"
)

// Kind is the two kinds of claim of design/verification.md, which decide
// which transfer rule applies.
type Kind string

const (
	Pure      Kind = "pure"
	Effectful Kind = "effectful"
)

// Admissibility is what a verifier made of an attestation before any
// transfer rule was applied.
type Admissibility string

const (
	// Admissible: the note verifies under a key the integrator holds, its
	// text is the written form of what it says, and its subject is the
	// tree the change produces.
	Admissible Admissibility = "admissible"

	// UnknownSigner: the note is well formed but nobody who signed it is
	// in the known-signers file. Evidence from a signer the integrator
	// does not hold does not transfer. Deciding what to do about an
	// unknown attester — re-verification, trust measurement — belongs to
	// the trust backstop and is deliberately not here.
	UnknownSigner Admissibility = "unknown-signer"

	// Tampered: everything else a verifier can refuse — a signature that
	// does not check out over the text, text that is not its own written
	// form, a statement whose subject digest is not the change's tree.
	Tampered Admissibility = "tampered"
)

// Change is the integration primitive (ida-93e4f91): a delta targeting a
// version stream, with the provenance and the attestations produced over
// the tree it results in. The branch it arrived on is transport.
type Change struct {
	// ID names the change. The refs transport takes it from the last
	// segment of the request ref.
	ID string

	// Target is the protected ref the delta targets, in full.
	Target string

	// Base is the commit the delta was authored against, and Head the
	// commit whose tree the attestations name.
	Base string
	Head string

	// Attested is the valley-tree-v1 digest of Head's tree — the snapshot
	// the transaction read, and the subject every attestation names.
	Attested string

	// Evidence is the attestations found for this change, by check name.
	Evidence map[string]Evidence

	// Provenance is the run record the attestations carry, as the first
	// statement stated it. Phase 2 records a delegation chain and Phase 3
	// does not yet check one, so this is carried into the outcome for a
	// reader and is not consulted by any rule below.
	Provenance string
}

// Evidence is one attestation as the integrator read it back.
type Evidence struct {
	Check      string
	Kind       Kind
	Admissible Admissibility

	// Result is what the statement recorded. A statement recording a
	// failure is not evidence that a check passed.
	Result string

	// Inputs is the input-closure digest the statement recorded, for a
	// pure check. This is the read set the transfer rule compares.
	Inputs string

	// ObservedAt is when an effectful check's result was observed.
	ObservedAt time.Time

	// TextDigest is a SHA-256 over the bytes the signature covers. The
	// transfer statement cites the original by this digest.
	TextDigest string

	// Signer names the key whose signature the integrator accepted, for
	// the reader of an outcome. Empty when none was accepted.
	Signer string

	// Carriage: the note as stored, the bytes its signature covers, the
	// ref and path it was found at, and what a verifier refused it with.
	// No rule below reads any of it; what lands does.
	note    []byte
	text    []byte
	ref     string
	entry   string
	refusal string
}

// Required is one check the policy demands of this change, as the deriver
// named it.
type Required struct {
	Name string

	// Classes are the path classes that made this check required. An
	// effectful check's scope is these classes' paths: an intervening
	// landing inside one of them is what ends the observation's validity.
	Classes []string

	// Runner and Attribute are how the check is run, from the composed
	// policy's catalogue.
	Runner    string
	Attribute string

	// Validity is Δ: how long an effectful observation of this check stays
	// good. Zero means the policy declares no window, and absent a
	// declared window the check is re-demanded — a window nobody wrote
	// down is not a window the integrator may invent.
	Validity time.Duration
}

// Applied is what happened when the delta was applied to the current tip.
type Applied struct {
	// Clean says the delta applied with no conflict. A conflict means
	// resolving it would produce a tree nobody has authored, and new
	// authorship needs new evidence.
	Clean bool

	// Conflict is what the merge said, for the reader of the outcome.
	Conflict string

	// Tree is the valley-tree-v1 digest of the resulting tree. Equal to
	// the change's Attested digest exactly when nothing the change reads
	// has moved, whatever else landed in between.
	Tree string
}

// Snapshot is everything about a change that had to be computed by
// touching git, nix, or the clock. A transport fills it in; the verdict is
// a function of it.
type Snapshot struct {
	Apply Applied

	// Required is the check set derived from the diff by the policy
	// machinery, read at the target tip and never from the submitted tree.
	Required []Required

	// Closures holds each pure check's input-closure digest recomputed on
	// the rebased tree, keyed by check name. A check missing from both
	// this map and ClosureErrors was not recomputed because it did not
	// need to be.
	Closures map[string]string

	// ClosureErrors holds why a recomputation could not be done. A closure
	// the integrator cannot compute is a check it cannot transfer.
	ClosureErrors map[string]string

	// Touched are the policy classes that intervening landed changes
	// touched, between the change's base and the current tip.
	Touched map[string]bool

	// Now is when the judgement is being made, for the effectful window.
	Now time.Time
}

// CheckVerdict is one check's outcome at the commit point.
type CheckVerdict struct {
	Name string

	// Transferred says the evidence stands on the landed tree.
	Transferred bool

	// Rule is which transfer rule carried it: "unmoved" when the landed
	// tree is the attested tree, "closure" when the recomputed input
	// closure equalled the attested one, "untouched" when an effectful
	// observation's scope was untouched and it was inside its window.
	Rule string

	// Reason says why it did not transfer, in words a contributor can act
	// on. Empty when it did.
	Reason string

	// Evidence is the digest of the statement text this verdict is about,
	// and Inputs the closure digest recomputed under the closure rule.
	Evidence string
	Inputs   string

	Signer string
}

// Verdict is what the integrator decided, and everything the outcome event
// and the transfer statement are written from.
type Verdict struct {
	Outcome Outcome

	// Reason is the one-line summary a reader gets. For a rejection it is
	// what was wrong; for staleness, which rule the change fell to.
	Reason string

	// StaleReason distinguishes the two ways a change goes stale, as
	// #RequestStale carries it: "conflict" or "evidence".
	StaleReason string

	// Checks is the per-check verdict, in the order the checks were
	// required.
	Checks []CheckVerdict

	// Invalidated is exactly the checks that need new evidence — the
	// event's payload, and the whole of what a contributor has to redo.
	Invalidated []string
}

// Decide applies the commit rule of dcr-439b771 to a change.
func Decide(ch Change, s Snapshot) Verdict {
	required := append([]Required(nil), s.Required...)
	sort.Slice(required, func(i, j int) bool { return required[i].Name < required[j].Name })

	// First part of the commit rule: the delta must apply cleanly. A
	// conflict is new authorship, so every check is re-demanded rather
	// than a subset — there is no partial retry from a tree nobody wrote.
	if !s.Apply.Clean {
		v := Verdict{
			Outcome:     Stale,
			StaleReason: "conflict",
			Reason:      "the delta does not apply cleanly to the tip; resolving it is new authorship, so every check needs new evidence",
		}
		for _, r := range required {
			v.Checks = append(v.Checks, CheckVerdict{Name: r.Name, Reason: "the delta conflicts, so the tree this would be evidence about does not exist yet"})
			v.Invalidated = append(v.Invalidated, r.Name)
		}
		return v
	}

	unmoved := s.Apply.Tree != "" && s.Apply.Tree == ch.Attested

	v := Verdict{Outcome: Land}
	for _, r := range required {
		cv := judge(ch, s, r, unmoved)
		v.Checks = append(v.Checks, cv)
		switch {
		case cv.Rule == "rejected":
			// A forgery ends the judgement: the change is not behind, its
			// evidence is not evidence.
			return Verdict{
				Outcome: Reject,
				Reason:  fmt.Sprintf("%s: %s", r.Name, cv.Reason),
				Checks:  v.Checks,
			}
		case !cv.Transferred:
			v.Invalidated = append(v.Invalidated, r.Name)
		}
	}
	if len(v.Invalidated) > 0 {
		v.Outcome = Stale
		v.StaleReason = "evidence"
		v.Reason = fmt.Sprintf("%d of %d required checks no longer carry evidence that transfers", len(v.Invalidated), len(required))
		return v
	}
	if len(required) == 1 {
		v.Reason = "the one required check carries evidence that transfers"
	} else {
		v.Reason = fmt.Sprintf("all %d required checks carry evidence that transfers", len(required))
	}
	return v
}

// refusalLine is the sentence a verifier refused with. attest reports what
// it checked and then the refusal, prefixed with its own name; the lines
// after the refusal elaborate it and the reader of a verdict wants the
// sentence, not the elaboration.
func refusalLine(s string) string {
	var last string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if why, ok := strings.CutPrefix(line, "attest: "); ok {
			return strings.TrimSpace(why)
		}
		if strings.TrimSpace(line) != "" {
			last = strings.TrimSpace(line)
		}
	}
	return last
}

// judge is the second part of the commit rule, for one check.
func judge(ch Change, s Snapshot, r Required, unmoved bool) CheckVerdict {
	cv := CheckVerdict{Name: r.Name}
	ev, ok := ch.Evidence[r.Name]
	if !ok {
		cv.Reason = "no attestation for this check accompanies the change"
		return cv
	}
	cv.Evidence, cv.Signer = ev.TextDigest, ev.Signer

	switch ev.Admissible {
	case Tampered:
		cv.Rule = "rejected"
		cv.Reason = "the attestation does not verify as the statement it claims to be"
		if why := refusalLine(ev.refusal); why != "" {
			cv.Reason += ": " + why
		}
		return cv
	case UnknownSigner:
		cv.Reason = "signed by no signer the integrator holds, so its evidence does not transfer"
		return cv
	}
	if ev.Result != "passed" {
		cv.Reason = fmt.Sprintf("the attestation records the check as %s", ev.Result)
		return cv
	}

	if ev.Kind == Pure {
		// Content-addressed evidence does not age: a pure attestation
		// carries no expiry, because time is not among its inputs.
		if unmoved {
			cv.Transferred, cv.Rule = true, "unmoved"
			return cv
		}
		if why, bad := s.ClosureErrors[r.Name]; bad {
			cv.Reason = "the input closure could not be recomputed on the landed tree: " + why
			return cv
		}
		got, ok := s.Closures[r.Name]
		if !ok {
			cv.Reason = "the input closure was not recomputed on the landed tree"
			return cv
		}
		if got != ev.Inputs {
			cv.Reason = fmt.Sprintf("its input closure changed: attested %s, landed tree %s", short(ev.Inputs), short(got))
			return cv
		}
		cv.Transferred, cv.Rule, cv.Inputs = true, "closure", got
		return cv
	}

	// An effectful observation transfers while its scope is untouched and
	// it is recent enough. Serializable semantics against mutable world
	// state are unattainable in principle, so the honest semantics are
	// bounded staleness.
	for _, class := range r.Classes {
		if s.Touched[class] {
			cv.Reason = fmt.Sprintf("an intervening landed change touched the %s class, which is what this check reads", class)
			return cv
		}
	}
	if r.Validity == 0 {
		cv.Reason = "the policy declares no validity window for this effectful check, so its observation is re-demanded"
		return cv
	}
	if age := s.Now.Sub(ev.ObservedAt); age >= r.Validity {
		cv.Reason = fmt.Sprintf("the observation is %s old and the declared window is %s", age.Round(time.Second), r.Validity)
		return cv
	}
	cv.Transferred, cv.Rule = true, "untouched"
	return cv
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
