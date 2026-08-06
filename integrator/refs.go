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
// deterministic.
func (in *integrator) requests() ([]request, error) {
	out, err := git(in.repo, "for-each-ref", "--format=%(refname) %(objectname)", requestPrefix)
	if err != nil {
		return nil, err
	}
	var found []request
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
		if !ok || strings.Contains(change, "/") {
			return nil, fmt.Errorf("%s is not a request ref: one is %s/<target>/<change>", ref, requestPrefix)
		}
		found = append(found, request{ref: ref, change: change, target: "refs/heads/" + target, head: head})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ref < found[j].ref })
	return found, nil
}

// reconcile is one pass: judge every request against the current tip of its
// target, and act where the answer has changed.
func (in *integrator) reconcile() error {
	requests, err := in.requests()
	if err != nil {
		return err
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
	// Level-triggering: the verdict is a function of the request's head
	// and the target's tip, so an unchanged pair has an unchanged answer
	// and re-announcing it would be a retry storm.
	memo := fmt.Sprintf("%s %s", r.head, tip)
	if last, _ := in.readMemo(r); last != "" && strings.HasPrefix(last, memo+" ") {
		return nil
	}

	ch, snap, landed, err := in.materialize(r, tip)
	if err != nil {
		return err
	}
	v := Decide(ch, snap)
	in.report(r, tip, v)

	switch v.Outcome {
	case Land:
		if err := in.land(ch, v, r, tip, landed); err != nil {
			return err
		}
	case Stale:
		in.publishStale(ch, v, tip)
	case Reject:
		// There is no rejection event. The failure model is unified on
		// staleness (architecture.md), and a change whose evidence is
		// forged is not stale — so rather than stretch either event to
		// cover it, the vocabulary stays as it is and the refusal is the
		// integrator's own output. The request stays put; nothing about
		// it will change on its own.
		fmt.Fprintf(in.out, "  no event: a forged attestation is not staleness, and the vocabulary has no other word\n")
	}
	return in.writeMemo(r, memo+" "+string(v.Outcome))
}

// materialized is what one pass computed about a request before deciding.
type landing struct {
	tree   string // the resulting tree's object id
	commit string // the commit that would be the target's new tip
	digest string // the valley-tree-v1 digest of the resulting tree
	policy string // a digest of the composed policy the verdict used
}

// materialize gathers everything the verdict is a function of: the applied
// delta, the required checks, the recomputed closures, and what intervening
// landings touched.
func (in *integrator) materialize(r request, tip string) (Change, Snapshot, landing, error) {
	var land landing
	wt, err := addWorktree(in.repo, tip)
	if err != nil {
		return Change{}, Snapshot{}, land, err
	}
	defer wt.remove()

	base, err := gitLine(in.repo, "merge-base", tip, r.head)
	if err != nil {
		return Change{}, Snapshot{}, land, fmt.Errorf("%s and %s share no history", short(r.head), r.target)
	}
	attested, err := in.treeDigest(wt, r.head)
	if err != nil {
		return Change{}, Snapshot{}, land, err
	}
	ch := Change{ID: r.change, Target: r.target, Base: base, Head: r.head, Attested: attested}
	if ch.Evidence, err = in.collectEvidence(wt, r.head, attested); err != nil {
		return ch, Snapshot{}, land, err
	}

	// The policy is read at the target tip, from the integrator's own
	// checkout — never from the tree being gated.
	cat, policyDigest, err := in.policy.catalogue(wt)
	if err != nil {
		return ch, Snapshot{}, land, err
	}
	land.policy = policyDigest
	owed, err := in.policy.derive(wt, base, r.head)
	if err != nil {
		return ch, Snapshot{}, land, err
	}
	required, err := in.policy.required(owed, cat)
	if err != nil {
		return ch, Snapshot{}, land, err
	}

	snap := Snapshot{Required: required, Touched: map[string]bool{}, Now: in.now()}

	// Apply the delta. A base that is already the tip is a fast-forward:
	// the head lands as it stands and its tree is the tree that was
	// attested.
	if base == tip {
		land.commit = r.head
		if land.tree, err = gitLine(in.repo, "rev-parse", r.head+"^{tree}"); err != nil {
			return ch, snap, land, err
		}
		land.digest = attested
		snap.Apply = Applied{Clean: true, Tree: attested}
	} else {
		tree, conflict, err := mergeDelta(in.repo, base, tip, r.head)
		if err != nil {
			return ch, snap, land, err
		}
		if conflict != "" {
			snap.Apply = Applied{Clean: false, Conflict: firstLine(conflict)}
			return ch, snap, land, nil
		}
		land.tree = tree
		if land.commit, err = in.commitTree(tree, tip, r); err != nil {
			return ch, snap, land, err
		}
		if land.digest, err = in.treeDigest(wt, tree); err != nil {
			return ch, snap, land, err
		}
		snap.Apply = Applied{Clean: true, Tree: land.digest}

		// What intervening landings touched, in the policy's own terms:
		// the classes the deriver matches over base..tip. Asking the
		// deriver rather than matching globs here keeps one implementation
		// of what a class covers.
		intervening, err := in.policy.derive(wt, base, tip)
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
		return "", fmt.Errorf("cannot read the authorship of %s", short(r.head))
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
func (in *integrator) land(ch Change, v Verdict, r request, tip string, l landing) error {
	transfer, err := in.composeTransfer(ch, v, l.digest, l.commit, tip, l.policy)
	if err != nil {
		return err
	}
	evidence, err := in.countersign(ch, v, transfer, l.digest)
	if err != nil {
		return err
	}
	if _, err := git(in.repo, "update-ref", ch.Target, l.commit, tip); err != nil {
		return fmt.Errorf("%s moved under the verdict; nothing landed: %w", ch.Target, err)
	}
	refs, err := in.store(evidence)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		fmt.Fprintf(in.out, "  evidence %s\n", ref)
	}
	fmt.Fprintf(in.out, "  landed   %s %s -> %s\n", ch.Target, short(tip), short(l.commit))
	if _, err := git(in.repo, "update-ref", "-d", r.ref); err != nil {
		return err
	}
	in.publishLanded(ch, v, tip, l.commit)
	return nil
}

// report prints the verdict, per check, in the order it was judged.
func (in *integrator) report(r request, tip string, v Verdict) {
	fmt.Fprintf(in.out, "%s -> %s @ %s: %s\n", r.change, r.target, short(tip), v.Outcome)
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

func (in *integrator) now() time.Time {
	if !in.clock.IsZero() {
		return in.clock
	}
	return time.Now().UTC()
}
