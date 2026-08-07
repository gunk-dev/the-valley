package verdict

// The commit rule, case by case. The verdict is a function of data, so
// every rule of dcr-439b771 is exercised here without a repository, a key,
// or a clock. What a repository does to produce these inputs is the refs
// transport's, and it is exercised end to end by the integrator-e2e check.

import (
	"strings"
	"testing"
	"time"
)

const (
	attestedTree = "aaaa"
	movedTree    = "bbbb"
	closureA     = "1111"
	closureB     = "2222"
)

var observed = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

func pureCheck(name string) Required {
	return Required{Name: name, Classes: []string{"code"}, Runner: "nix", Attribute: name}
}

func effectfulCheck(name string, window time.Duration) Required {
	return Required{Name: name, Classes: []string{"deploy"}, Runner: "command", Validity: window}
}

func pureEvidence(name, inputs string) Evidence {
	return Evidence{Check: name, Kind: Pure, Admissible: Admissible, Result: "passed",
		Inputs: inputs, Derivation: "drv-" + inputs, TextDigest: "d" + name}
}

// closures is what a caller recomputed over the tree that would land: the
// same derivation the attestation names, unless a case says otherwise.
func closures(pairs ...string) map[string]Closure {
	out := map[string]Closure{}
	for i := 0; i < len(pairs); i += 2 {
		out[pairs[i]] = Closure{Inputs: pairs[i+1], Derivation: "drv-" + pairs[i+1]}
	}
	return out
}

func effectfulEvidence(name string, at time.Time) Evidence {
	return Evidence{Check: name, Kind: Effectful, Admissible: Admissible, Result: "passed", ObservedAt: at, TextDigest: "d" + name}
}

func change(evidence ...Evidence) Change {
	ch := Change{ID: "c1", Target: "refs/heads/main", Attested: attestedTree, Evidence: map[string]Evidence{}}
	for _, e := range evidence {
		ch.Evidence[e.Check] = e
	}
	return ch
}

func TestUnmovedTreeTransfersEveryPureCheck(t *testing.T) {
	// Nothing intervened, so the landed tree is the attested tree and
	// there is nothing for a closure comparison to compare.
	v := Decide(
		change(pureEvidence("build", closureA), pureEvidence("lint", closureB)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: attestedTree},
			Required: []Required{pureCheck("build"), pureCheck("lint")},
		})
	if v.Outcome != Land {
		t.Fatalf("outcome = %s (%s)", v.Outcome, v.Reason)
	}
	for _, cv := range v.Checks {
		if cv.Rule != "unmoved" {
			t.Errorf("%s transferred by %q, want unmoved", cv.Name, cv.Rule)
		}
	}
}

func TestClosureEqualityTransfersAndInequalityInvalidates(t *testing.T) {
	// The base moved. build reads what the intervening landing changed;
	// lint does not. Exactly one check is named, and the other is
	// reported as having transferred.
	v := Decide(
		change(pureEvidence("build", closureA), pureEvidence("lint", closureB)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: movedTree},
			Required: []Required{pureCheck("build"), pureCheck("lint")},
			Closures: closures("build", "3333", "lint", closureB),
		})
	if v.Outcome != Stale || v.StaleReason != "evidence" {
		t.Fatalf("outcome = %s/%s (%s)", v.Outcome, v.StaleReason, v.Reason)
	}
	if len(v.Invalidated) != 1 || v.Invalidated[0] != "build" {
		t.Fatalf("invalidated = %v, want exactly [build]", v.Invalidated)
	}
	for _, cv := range v.Checks {
		if cv.Name == "lint" && (!cv.Transferred || cv.Rule != "closure") {
			t.Errorf("lint = %+v, want transferred by closure equality", cv)
		}
		if cv.Name == "build" && !strings.Contains(cv.Reason, "input closure changed") {
			t.Errorf("build reason = %q", cv.Reason)
		}
	}
}

func TestConflictDemandsEveryCheckAgain(t *testing.T) {
	// A conflict is new authorship, so retry is not partial.
	v := Decide(
		change(pureEvidence("build", closureA), pureEvidence("lint", closureB)),
		Snapshot{
			Apply:    Applied{Clean: false, Conflict: "CONFLICT (content): src/main.txt"},
			Required: []Required{pureCheck("build"), pureCheck("lint")},
		})
	if v.Outcome != Stale || v.StaleReason != "conflict" {
		t.Fatalf("outcome = %s/%s", v.Outcome, v.StaleReason)
	}
	if got := strings.Join(v.Invalidated, ","); got != "build,lint" {
		t.Fatalf("invalidated = %q, want every required check", got)
	}
}

func TestUnknownSignerDoesNotTransfer(t *testing.T) {
	// Not a forgery: a well-formed statement by a signer the integrator
	// does not hold. Deciding what to do about one belongs to the trust
	// backstop, so here it simply fails to transfer.
	ev := pureEvidence("build", closureA)
	ev.Admissible = UnknownSigner
	v := Decide(change(ev), Snapshot{
		Apply:    Applied{Clean: true, Tree: attestedTree},
		Required: []Required{pureCheck("build")},
	})
	if v.Outcome != Stale {
		t.Fatalf("outcome = %s, want stale", v.Outcome)
	}
	if !strings.Contains(v.Checks[0].Reason, "no signer the integrator holds") {
		t.Fatalf("reason = %q", v.Checks[0].Reason)
	}
}

func TestTamperedEvidenceIsRejectedNotStale(t *testing.T) {
	ev := pureEvidence("build", closureA)
	ev.Admissible = Tampered
	v := Decide(change(ev), Snapshot{
		Apply:    Applied{Clean: true, Tree: attestedTree},
		Required: []Required{pureCheck("build")},
	})
	if v.Outcome != Reject {
		t.Fatalf("outcome = %s, want reject", v.Outcome)
	}
	if len(v.Invalidated) != 0 {
		t.Fatalf("a rejection named %v as stale; re-attesting would not fix a forgery", v.Invalidated)
	}
}

func TestMissingAndFailedEvidence(t *testing.T) {
	failed := pureEvidence("lint", closureB)
	failed.Result = "failed"
	v := Decide(change(failed), Snapshot{
		Apply:    Applied{Clean: true, Tree: attestedTree},
		Required: []Required{pureCheck("build"), pureCheck("lint")},
	})
	if v.Outcome != Stale {
		t.Fatalf("outcome = %s", v.Outcome)
	}
	if got := strings.Join(v.Invalidated, ","); got != "build,lint" {
		t.Fatalf("invalidated = %q", got)
	}
	if !strings.Contains(v.Checks[0].Reason, "no attestation") {
		t.Errorf("build reason = %q", v.Checks[0].Reason)
	}
	if !strings.Contains(v.Checks[1].Reason, "records the check as failed") {
		t.Errorf("lint reason = %q", v.Checks[1].Reason)
	}
}

func TestEffectfulTransfersWhileScopeUntouchedAndObservationFresh(t *testing.T) {
	v := Decide(
		change(effectfulEvidence("reachable", observed)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: movedTree},
			Required: []Required{effectfulCheck("reachable", time.Hour)},
			Touched:  map[string]bool{"prose": true},
			Now:      observed.Add(30 * time.Minute),
		})
	if v.Outcome != Land {
		t.Fatalf("outcome = %s (%s)", v.Outcome, v.Reason)
	}
	if v.Checks[0].Rule != "untouched" {
		t.Fatalf("rule = %q, want untouched", v.Checks[0].Rule)
	}
}

func TestEffectfulFailsWhenItsClassWasTouched(t *testing.T) {
	v := Decide(
		change(effectfulEvidence("reachable", observed)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: movedTree},
			Required: []Required{effectfulCheck("reachable", time.Hour)},
			Touched:  map[string]bool{"deploy": true},
			Now:      observed.Add(time.Minute),
		})
	if v.Outcome != Stale || !strings.Contains(v.Checks[0].Reason, "touched the deploy class") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestEffectfulExpiresWithItsWindow(t *testing.T) {
	v := Decide(
		change(effectfulEvidence("reachable", observed)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: attestedTree},
			Required: []Required{effectfulCheck("reachable", time.Hour)},
			Now:      observed.Add(90 * time.Minute),
		})
	if v.Outcome != Stale || !strings.Contains(v.Checks[0].Reason, "the declared window is 1h0m0s") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestEffectfulWithNoDeclaredWindowIsReDemanded(t *testing.T) {
	// Δ is declared policy. Absent a declaration there is no window, and
	// the integrator may not invent one.
	v := Decide(
		change(effectfulEvidence("reachable", observed)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: attestedTree},
			Required: []Required{effectfulCheck("reachable", 0)},
			Now:      observed.Add(time.Second),
		})
	if v.Outcome != Stale || !strings.Contains(v.Checks[0].Reason, "declares no validity window") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestAnUnmovedTreeDoesNotExemptAnEffectfulCheckFromItsWindow(t *testing.T) {
	// A pure claim carries no expiry because time is not among its
	// inputs. An effectful one does, whatever happened to the tree.
	v := Decide(
		change(pureEvidence("build", closureA), effectfulEvidence("reachable", observed)),
		Snapshot{
			Apply:    Applied{Clean: true, Tree: attestedTree},
			Required: []Required{pureCheck("build"), effectfulCheck("reachable", time.Hour)},
			Now:      observed.Add(2 * time.Hour),
		})
	if v.Outcome != Stale {
		t.Fatalf("outcome = %s", v.Outcome)
	}
	if got := strings.Join(v.Invalidated, ","); got != "reachable" {
		t.Fatalf("invalidated = %q, want only the effectful check", got)
	}
}

func TestClosureThatCouldNotBeRecomputedIsNotATransfer(t *testing.T) {
	v := Decide(
		change(pureEvidence("build", closureA)),
		Snapshot{
			Apply:         Applied{Clean: true, Tree: movedTree},
			Required:      []Required{pureCheck("build")},
			ClosureErrors: map[string]string{"build": "no such check attribute"},
		})
	if v.Outcome != Stale || !strings.Contains(v.Checks[0].Reason, "could not be recomputed") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestTheDerivationIsComparedToo(t *testing.T) {
	// The closure held and the derivation moved. A pure attestation asserts
	// both, so evidence whose derivation no longer matches is evidence
	// about a computation this tree does not present.
	moved := closures("build", closureA)
	moved["build"] = Closure{Inputs: closureA, Derivation: "some other derivation"}
	v := Decide(change(pureEvidence("build", closureA)), Snapshot{
		Apply:    Applied{Clean: true, Tree: movedTree},
		Required: []Required{pureCheck("build")},
		Closures: moved,
	})
	if v.Outcome != Stale || !strings.Contains(v.Checks[0].Reason, "its derivation changed") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestThePolicyDecidesWhichRuleApplies(t *testing.T) {
	// An attestation declaring a notarization for a check the policy runs
	// with nix would otherwise dodge closure comparison entirely: no
	// intervening landing touched its class, so the effectful rule would
	// pass it straight through onto a base it was never produced over.
	lying := effectfulEvidence("build", observed)
	v := Decide(change(lying), Snapshot{
		Apply:    Applied{Clean: true, Tree: movedTree},
		Required: []Required{pureCheck("build")},
		Closures: closures("build", "a completely different closure"),
		Now:      observed.Add(time.Minute),
	})
	if v.Outcome != Reject {
		t.Fatalf("outcome = %s, want reject", v.Outcome)
	}
	if !strings.Contains(v.Checks[0].Reason, "requires a pure check and the attestation claims an effectful check") {
		t.Fatalf("reason = %q", v.Checks[0].Reason)
	}
}

func TestAPureClaimForAnEffectfulCheckIsAlsoRefused(t *testing.T) {
	// The mismatch is refused in both directions: the rule follows the
	// policy, and a statement that contradicts it is not judged at all.
	v := Decide(change(pureEvidence("reachable", closureA)), Snapshot{
		Apply:    Applied{Clean: true, Tree: attestedTree},
		Required: []Required{effectfulCheck("reachable", time.Hour)},
		Now:      observed,
	})
	if v.Outcome != Reject || !strings.Contains(v.Checks[0].Reason, "requires an effectful check and the attestation claims a pure check") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestARunnerWithNoTransferRuleIsRefused(t *testing.T) {
	v := Decide(change(pureEvidence("build", closureA)), Snapshot{
		Apply:    Applied{Clean: true, Tree: attestedTree},
		Required: []Required{{Name: "build", Runner: "something-new"}},
	})
	if v.Outcome != Reject || !strings.Contains(v.Checks[0].Reason, "no transfer rule is defined") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Checks[0].Reason)
	}
}

func TestNothingRequiredIsALandingWithNoCheckVerdicts(t *testing.T) {
	// A policy that asks nothing of a diff is an ordinary result of the
	// commit rule, and it lands. What the caller must not be handed is a
	// verdict that looks like a count of zero: the check list is empty, so
	// whatever is written down about this landing cannot be a list.
	v := Decide(change(), Snapshot{
		Apply: Applied{Clean: true, Tree: attestedTree},
	})
	if v.Outcome != Land || len(v.Checks) != 0 {
		t.Fatalf("outcome = %s with %d check verdicts (%s)", v.Outcome, len(v.Checks), v.Reason)
	}
	if !strings.Contains(v.Reason, "requires no check of the paths") {
		t.Fatalf("reason = %q", v.Reason)
	}
}

func TestAChangeAlreadyInTheTargetSaysThatRatherThanNothing(t *testing.T) {
	// A resubmitted request whose head is already in the stream derives an
	// empty delta, so the policy requires nothing of it — for a reason that
	// has nothing to do with the policy. The base is the merge base with
	// the tip, so base equal to head is exactly that case, and a reader of
	// the outcome has to be told which of the two zeroes this is.
	ch := change()
	ch.Base, ch.Head = "cafe", "cafe"
	v := Decide(ch, Snapshot{Apply: Applied{Clean: true, Tree: attestedTree}})
	if v.Outcome != Land || !strings.Contains(v.Reason, "already in the target's history") {
		t.Fatalf("verdict = %s, reason = %q", v.Outcome, v.Reason)
	}
}
