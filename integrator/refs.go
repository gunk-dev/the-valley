package main

// The refs transport: a reconciliation loop over the integration requests
// durable in the served repository.
//
// It polls, deliberately. The bus is unauthenticated (bd-d853d9c), which
// gates automated consumers, and the requests are durable in git with the
// bus as a projection over them (architecture.md). So the controller
// level-triggers over the request refs themselves and converges: each pass
// recomputes the verdict for every request against the current tip, acts
// only where the answer has changed since the last pass, and needs no
// subscription at all. It publishes outcomes to the bus exactly as the
// post-receive hook publishes ref updates. Consuming events arrives in a
// later phase.
//
// # The request ref
//
// A request is a ref:
//
//	refs/the-valley/integration-requests/<target>/<change>  ->  commit
//
// The target segment names the protected branch, so the change's target
// stream is refs/heads/<target>; the last segment names the change. The
// commit is the change's head, its tree is what the attestations are over,
// and the delta is the merge base with the target's tip to that head.
// Nothing else is encoded: a change is a diff targeting a stream
// (ida-93e4f91), and every part of that is already in git.

import (
	"the-valley/integrator/verdict"

	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	requestPrefix = "refs/the-valley/integration-requests"
	outcomePrefix = "refs/the-valley/integration-outcomes"
)

// request is one integration request as the refs transport found it.
type request struct {
	ref    string
	change string
	target string
	head   string
}

// requests lists the pending requests, in ref order so a pass is
// deterministic, along with the refs under the namespace that are not
// requests at all.
//
// Anyone with push access can write this namespace, so a name that does not
// parse is an ordinary thing to find there and not a reason to stop: one
// unreadable ref must not hold up every readable one. It is reported and
// skipped.
func (in *integrator) requests() ([]request, []string, error) {
	out, err := git(in.repo, "for-each-ref", "--format=%(refname) %(objectname)", requestPrefix)
	if err != nil {
		return nil, nil, err
	}
	var found []request
	var malformed []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		ref, head, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		rest := strings.TrimPrefix(ref, requestPrefix+"/")
		target, change, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(change, "/") || target == "" || change == "" {
			malformed = append(malformed, ref)
			continue
		}
		found = append(found, request{ref: ref, change: change, target: "refs/heads/" + target, head: head})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ref < found[j].ref })
	sort.Strings(malformed)
	return found, malformed, nil
}

// reconcile is one pass: judge every request against the current tip of its
// target, and act where the answer has changed.
func (in *integrator) reconcile() error {
	requests, malformed, err := in.requests()
	if err != nil {
		return err
	}
	for _, ref := range malformed {
		fmt.Fprintf(in.out, "%s: not a request ref; one is %s/<target>/<change>. Skipped.\n", ref, requestPrefix)
	}
	for _, r := range requests {
		if err := in.handle(r); err != nil {
			// One bad request must not stop the pass: the integrator is a
			// controller, and a request it cannot judge is one request's
			// problem.
			fmt.Fprintf(in.out, "%s: %v\n", r.change, err)
		}
	}
	return nil
}

// handle judges one request and acts on the verdict.
func (in *integrator) handle(r request) error {
	tip, err := gitLine(in.repo, "rev-parse", "--verify", "--quiet", r.target)
	if err != nil {
		return fmt.Errorf("no target %s to integrate onto", r.target)
	}
	// Level-triggering: the verdict is a function of the request's head,
	// the target's tip, and the evidence stored for the change, so an
	// unchanged answer is one where none of the three has moved.
	// Re-announcing an unchanged answer would be a retry storm; missing a
	// moved one would strand a contributor who pushed the attestation a
	// previous pass said was absent.
	evidence, err := in.evidenceState()
	if err != nil {
		return err
	}
	memo := fmt.Sprintf("%s %s %s", r.head, tip, evidence)
	if done, err := in.answered(r, memo); err != nil {
		return err
	} else if done {
		return nil
	}
	// From here on the request has been taken up, so whatever happens is
	// this pass's answer to it — including a failure. Recording that is
	// what keeps the next tick from asking the identical question.
	outcome, err := in.act(r, tip)
	if err != nil {
		if memoErr := in.writeMemo(r, memo+" "+failedMemo+" "+in.now().Format(time.RFC3339)); memoErr != nil {
			return fmt.Errorf("%v (and the failure could not be recorded: %v)", err, memoErr)
		}
		return err
	}
	return in.writeMemo(r, memo+" "+string(outcome))
}

// failedMemo marks a memo written for a pass that could not act on its own
// verdict, and retryAfter is how long that answer stands.
//
// A failure here is a function of the same three things the verdict is, so
// the identical attempt fails identically until one of them moves — and
// moving any of them already re-judges the request, whatever the memo says.
// The window exists for the other kind of failure: a bus, a disk, a lock,
// something outside the three that will be different later. Minutes rather
// than ticks, because a controller that reports the same refusal every
// fifteen seconds has buried the report it was making.
const (
	failedMemo = "failed"
	retryAfter = 10 * time.Minute
)

// answered says whether this pass already has this request's answer. An
// outcome stands until the request, the tip, or the evidence moves; a
// recorded failure stands the same way, and additionally expires, so a
// transient one is retried without being retried at tick rate.
func (in *integrator) answered(r request, memo string) (bool, error) {
	last, err := in.readMemo(r)
	if err != nil || last == "" {
		return false, err
	}
	rest, ok := strings.CutPrefix(last, memo+" ")
	if !ok {
		return false, nil
	}
	when, failed := strings.CutPrefix(rest, failedMemo+" ")
	if !failed {
		return true, nil
	}
	at, err := time.Parse(time.RFC3339, when)
	if err != nil {
		return false, nil
	}
	return in.now().Sub(at) < retryAfter, nil
}

// act judges one request and does what the verdict says, returning the
// outcome so the memo can record it.
func (in *integrator) act(r request, tip string) (verdict.Outcome, error) {
	ch, snap, landed, err := in.materialize(r, tip)
	if err != nil {
		return "", err
	}
	v := verdict.Decide(ch, snap)
	in.report(r, tip, v)

	switch v.Outcome {
	case verdict.Land:
		if err := in.land(ch, v, r, tip, landed); err != nil {
			return v.Outcome, err
		}
	case verdict.Stale:
		in.publishStale(ch, v, tip)
	case verdict.Reject:
		// There is no rejection event. The failure model is unified on
		// staleness (architecture.md), and a change whose evidence is
		// forged is not stale — so rather than stretch either event to
		// cover it, the vocabulary stays as it is and the refusal is the
		// integrator's own output. The request stays put; nothing about
		// it will change on its own.
		fmt.Fprintf(in.out, "  no event: a forged attestation is not staleness, and the vocabulary has no other word\n")
	}
	return v.Outcome, nil
}

// landing is the commit a verdict would move the target to, and what the
// verdict was computed under.
type landing struct {
	commit string // the commit that would be the target's new tip
	digest string // the valley-tree-v1 digest of the resulting tree
	policy string // a digest of the composed policy the verdict used

	// notes are the contributor's attestations as stored, by check name.
	// The verdict does not read them; what lands countersigns them.
	notes map[string][]byte
}

// materialize gathers everything the verdict is a function of: the applied
// delta, the required checks, the recomputed closures, and what intervening
// landings touched.
func (in *integrator) materialize(r request, tip string) (verdict.Change, verdict.Snapshot, landing, error) {
	var land landing
	wt, err := addWorktree(in.repo, tip)
	if err != nil {
		return verdict.Change{}, verdict.Snapshot{}, land, err
	}
	defer wt.remove()

	base, err := gitLine(in.repo, "merge-base", tip, r.head)
	if err != nil {
		return verdict.Change{}, verdict.Snapshot{}, land, fmt.Errorf("%s and %s share no history", verdict.Short(r.head), r.target)
	}
	attested, err := in.treeDigest(wt, r.head)
	if err != nil {
		return verdict.Change{}, verdict.Snapshot{}, land, err
	}
	ch := verdict.Change{ID: r.change, Target: r.target, Base: base, Head: r.head, Attested: attested}
	if ch.Evidence, land.notes, err = in.collectEvidence(wt, r.head, attested); err != nil {
		return ch, verdict.Snapshot{}, land, err
	}

	// The policy is read from the integrator's own side and never from the
	// tree being gated: the project layer from this checkout of the target
	// tip, the floor from the instance repository's integrated tip. Both
	// are resolved once and held for the whole pass.
	pol, closeInstance, err := in.policy.open()
	if err != nil {
		return ch, verdict.Snapshot{}, land, err
	}
	defer closeInstance()
	cat, policyDigest, err := pol.catalogue(wt)
	if err != nil {
		return ch, verdict.Snapshot{}, land, err
	}
	land.policy = policyDigest
	owed, err := pol.derive(wt, base, r.head)
	if err != nil {
		return ch, verdict.Snapshot{}, land, err
	}
	required, err := pol.required(owed, cat)
	if err != nil {
		return ch, verdict.Snapshot{}, land, err
	}

	snap := verdict.Snapshot{Required: required, Touched: map[string]bool{}, Now: in.now()}

	// Apply the delta. A base that is already the tip is a fast-forward:
	// the head lands as it stands and its tree is the tree that was
	// attested.
	if base == tip {
		land.commit = r.head
		land.digest = attested
		snap.Apply = verdict.Applied{Clean: true, Tree: attested}
	} else {
		tree, conflict, err := mergeDelta(in.repo, base, tip, r.head)
		if err != nil {
			return ch, snap, land, err
		}
		if conflict != "" {
			snap.Apply = verdict.Applied{Clean: false, Conflict: conflict}
			return ch, snap, land, nil
		}
		if land.commit, err = in.commitTree(tree, tip, r); err != nil {
			return ch, snap, land, err
		}
		if land.digest, err = in.treeDigest(wt, tree); err != nil {
			return ch, snap, land, err
		}
		snap.Apply = verdict.Applied{Clean: true, Tree: land.digest}

		// What intervening landings touched, in the policy's own terms:
		// the classes the deriver matches over base..tip. Asking the
		// deriver rather than matching globs here keeps one implementation
		// of what a class covers.
		intervening, err := pol.derive(wt, base, tip)
		if err != nil {
			return ch, snap, land, err
		}
		snap.Touched = intervening.classes
	}

	// The closures only have to be recomputed when the tree moved: an
	// unmoved tree presents the identical computation by construction.
	if land.digest != attested {
		snap.Closures, snap.ClosureErrors = in.closures(wt, land.commit, required)
	}
	return ch, snap, land, nil
}

// commitTree turns the merged tree into the commit that would become the
// target's new tip. The tip is its only parent, so the ref write is a
// fast-forward; the change's authorship is carried over from its head.
func (in *integrator) commitTree(tree, tip string, r request) (string, error) {
	message, err := git(in.repo, "log", "-1", "--format=%B", r.head)
	if err != nil {
		return "", err
	}
	author, err := git(in.repo, "log", "-1", "--format=%an%n%ae%n%aI", r.head)
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.TrimRight(author, "\n"), "\n")
	if len(fields) != 3 {
		return "", fmt.Errorf("cannot read the authorship of %s", verdict.Short(r.head))
	}
	cmd := []string{"commit-tree", tree, "-p", tip, "-m", strings.TrimRight(message, "\n")}
	out, err := in.gitEnv(map[string]string{
		"GIT_AUTHOR_NAME":  fields[0],
		"GIT_AUTHOR_EMAIL": fields[1],
		"GIT_AUTHOR_DATE":  fields[2],
	}, cmd...)
	return strings.TrimSpace(out), err
}

// land moves the protected ref, stores the evidence, and says so on the
// bus. The ref write is a compare-and-swap against the tip the verdict was
// computed over, so a request that lost a race to another lands nothing and
// is judged again next pass against the tip that won.
//
// The order is the commit point's. Everything that can fail while the
// integrator is still deciding happens first, and the ref write is the
// commit. Composing the transfer statement — vetting it, writing it down,
// signing it — is part of deciding: a claim the integrator cannot write
// down is a claim it has not made, and a stream moved on a decision that
// cannot be stated is a landing nothing records. A failure before the
// commit point leaves the stream where it was and the request where it was,
// which is a transaction abandoned before it committed and costs one pass
// to redo.
//
// Past the commit point the landing is a fact in the stream, and what is
// left is recording it: storing the evidence, consuming the request, and
// saying so on the bus. None of those may be skipped because an earlier one
// failed, and consuming the request in particular may not be reachable only
// through something that can fail — a request left pending for a change
// already in the stream is re-judged against an empty delta on every pass,
// forever. So each step runs, each failure is reported, and the landing is
// reported as having happened, because it did.
func (in *integrator) land(ch verdict.Change, v verdict.Verdict, r request, tip string, l landing) error {
	transfer, err := in.composeTransfer(ch, v, l.digest, l.commit, tip, l.policy)
	if err != nil {
		return err
	}
	evidence, err := in.countersign(ch, v, l, transfer)
	if err != nil {
		return err
	}

	// The commit point.
	if _, err := git(in.repo, "update-ref", ch.Target, l.commit, tip); err != nil {
		return fmt.Errorf("%s moved under the verdict; nothing landed: %w", ch.Target, err)
	}
	fmt.Fprintf(in.out, "  landed   %s %s -> %s\n", ch.Target, verdict.Short(tip), verdict.Short(l.commit))

	var unrecorded []string
	refs, err := in.store(evidence)
	if err != nil {
		unrecorded = append(unrecorded, fmt.Sprintf("the evidence was not stored (%v)", err))
	}
	for _, ref := range refs {
		fmt.Fprintf(in.out, "  evidence %s\n", ref)
	}
	if _, err := git(in.repo, "update-ref", "-d", r.ref); err != nil {
		unrecorded = append(unrecorded, fmt.Sprintf("the request ref %s was not consumed (%v)", r.ref, err))
	}
	in.publishLanded(ch, v, tip, l.commit)
	if len(unrecorded) > 0 {
		return fmt.Errorf("%s landed as %s and the landing is not fully recorded: %s",
			ch.ID, verdict.Short(l.commit), strings.Join(unrecorded, "; "))
	}
	return nil
}

// report prints the verdict, per check, in the order it was judged.
func (in *integrator) report(r request, tip string, v verdict.Verdict) {
	fmt.Fprintf(in.out, "%s -> %s @ %s: %s\n", r.change, r.target, verdict.Short(tip), v.Outcome)
	fmt.Fprintf(in.out, "  %s\n", v.Reason)
	for _, cv := range v.Checks {
		if cv.Transferred {
			fmt.Fprintf(in.out, "  check    %-24s transferred (%s)\n", cv.Name, cv.Rule)
			continue
		}
		fmt.Fprintf(in.out, "  check    %-24s %s\n", cv.Name, cv.Reason)
	}
}

// The memo is the level-trigger's memory: what this integrator last decided
// about a request, keyed by the request. It is durable in git like
// everything else the transport reads, so a restarted controller does not
// re-announce a judgement it has already made.
func (in *integrator) memoRef(r request) string {
	return outcomePrefix + "/" + strings.TrimPrefix(r.ref, requestPrefix+"/")
}

func (in *integrator) readMemo(r request) (string, error) {
	out, ok := gitTry(in.repo, "cat-file", "blob", in.memoRef(r))
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

func (in *integrator) writeMemo(r request, value string) error {
	blob, err := in.writeBlob([]byte(value + "\n"))
	if err != nil {
		return err
	}
	_, err = git(in.repo, "update-ref", in.memoRef(r), blob)
	return err
}

// evidenceState digests the attestation namespace: every ref under it and
// the object it points at. Attestation refs are create-only, so this moves
// exactly when evidence arrives.
//
// It is deliberately the whole namespace rather than the refs for one
// change's subject digest, which would take a checkout to compute and would
// have to be recomputed before the memo could rule the work out. So an
// attestation for one change re-judges every pending request. That is the
// conservative direction — a pass that runs and changes nothing costs a
// pass; a verdict never revisited strands a change — and it is bounded by
// the number of requests pending.
func (in *integrator) evidenceState() (string, error) {
	out, err := git(in.repo, "for-each-ref", "--format=%(refname) %(objectname)", attestationPrefix)
	if err != nil {
		return "", err
	}
	return sha256hex([]byte(out))[:16], nil
}

func (in *integrator) now() time.Time {
	if !in.clock.IsZero() {
		return in.clock
	}
	return time.Now().UTC()
}
