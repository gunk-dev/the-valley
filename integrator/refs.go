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
// (ida-93e4f91), and every part of that is already in git. A request
// whose last segment is c-<change ID> is the request of the change with a
// record under that ID, and the records decide whether it may land at all
// (records.go).

import (
	"the-valley/change"
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

// landingTries is how many times a landing builds its ref transaction and
// applies it, while what fails it is a record ref that moved since it was
// read. A record ref moves under a landing only when something other than
// this pass writes it, such as a push creating an attestation ref the
// landing adds to.
const landingTries = 3

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
// Any holder of the approve grant can write this namespace, and anyone at all
// where a repository is served without the pre-receive hook, so a name that
// does not parse is an ordinary thing to find there and not a reason to stop: one
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
//
// The change records are read once, before any request is judged. A pass
// that cannot read them judges nothing: which heads are versions of
// changes that are not ready is what keeps those heads from landing. The
// requests a withdrawn change voids are deleted before the rest are
// judged.
func (in *integrator) reconcile() error {
	requests, malformed, err := in.requests()
	if err != nil {
		return err
	}
	for _, ref := range malformed {
		fmt.Fprintf(in.out, "%s: not a request ref; one is %s/<target>/<change>. Skipped.\n", ref, requestPrefix)
	}
	if len(requests) == 0 {
		return nil
	}
	idx, err := in.readRecords()
	if err != nil {
		return err
	}
	for _, u := range idx.unreadable {
		fmt.Fprintf(in.out, "WARNING: %s is a change record that does not read: %s. Requests are judged as though it did not exist, so a head it names is not held back by it. The hook refuses such a record, so the repository's storage is corrupt; restore the record or delete it.\n", u.ref, u.why)
	}
	requests = in.voidWithdrawn(requests, idx)
	for _, r := range requests {
		if err := in.handle(r, idx); err != nil {
			// One bad request must not stop the pass: the integrator is a
			// controller, and a request it cannot judge is one request's
			// problem.
			fmt.Fprintf(in.out, "%s: %v\n", r.change, err)
		}
	}
	return nil
}

// handle judges one request and acts on the verdict. A decision it
// reports is timed, and its timing line follows the report and the
// recorded outcome; a refusal said before, and so not said again, is not
// timed again either.
func (in *integrator) handle(r request, idx *recordIndex) error {
	watch := newStopwatch()
	tip, ok, err := resolveRef(in.repo, r.target)
	if err != nil {
		return err
	} else if !ok {
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
	last, err := in.readMemo(r)
	if err != nil {
		return err
	}
	if in.answered(last, memo) {
		return nil
	}
	// From here on the request has been taken up, so whatever happens is
	// this pass's answer to it — including a failure. Recording that is
	// what keeps the next tick from asking the identical question.
	done := watch.stage(stageRefusal)
	outcome, err := in.refuse(r, idx, tip, last, memo)
	done()
	if err == nil && outcome == "" {
		outcome, err = in.act(r, tip, memo, watch)
	}
	if err != nil {
		printTiming := in.timing(r, tip, failedMemo, watch)
		memoErr := in.writeMemo(r, memo+" "+failedMemo+" "+in.now().Format(time.RFC3339))
		printTiming()
		if memoErr != nil {
			return fmt.Errorf("%v (and the failure could not be recorded: %v)", err, memoErr)
		}
		return err
	}
	if last == memo+" "+string(outcome) {
		return nil
	}
	printTiming := in.timing(r, tip, string(outcome), watch)
	// A landing wrote its outcome record in the transaction that moved
	// the target.
	if outcome != verdict.Land {
		err = in.writeMemo(r, memo+" "+string(outcome))
	}
	printTiming()
	return err
}

// refuse applies the rules that refuse a request before it is judged
// (records.go), and reports a refusal. It reports one only when it is new:
// a refusal is decided again on every pass, and saying the same one every
// tick would bury it.
func (in *integrator) refuse(r request, idx *recordIndex, tip, last, memo string) (verdict.Outcome, error) {
	outcome, why, err := in.recordRefusal(r, idx, tip)
	if err == nil && outcome == "" {
		outcome, why, err = in.mergeRefusal(r, tip)
	}
	if err != nil {
		return "", err
	}
	if outcome != "" && last != memo+" "+string(outcome) {
		in.reportRefusal(r, tip, outcome, why)
	}
	// A record that does not read may name a commit this request brings,
	// and whether it holds that commit back cannot be told. That is said
	// with the request, whatever is decided about it.
	if outcome == "" || last != memo+" "+string(outcome) {
		for _, u := range in.uncheckable(r, idx, tip) {
			fmt.Fprintf(in.out, "WARNING: %s may name a commit %s brings, and does not read, so whether it holds that commit back cannot be checked\n", u.ref, r.change)
		}
	}
	return outcome, nil
}

// reportRefusal prints a refusal the way report prints a verdict.
func (in *integrator) reportRefusal(r request, tip string, outcome verdict.Outcome, why string) {
	fmt.Fprintf(in.out, "%s -> %s @ %s: %s\n", r.change, r.target, verdict.Short(tip), outcome)
	fmt.Fprintf(in.out, "  %s\n", why)
	fmt.Fprintf(in.out, "  no event: a refusal is not staleness, and the request stays where it is\n")
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

// answered says whether this pass already has this request's answer, given
// the last memo written for it. An outcome stands until the request, the
// tip, or the evidence moves; a recorded failure stands the same way, and
// additionally expires, so a transient one is retried without being
// retried at tick rate. A refusal never stands: what refused it can change
// while the three stay where they are, as when a draft is readied again,
// so it is decided again on every pass. Nor does a voiding: a request
// filed again at the same head is a fresh approval.
func (in *integrator) answered(last, memo string) bool {
	rest, ok := strings.CutPrefix(last, memo+" ")
	if !ok {
		return false
	}
	switch verdict.Outcome(rest) {
	case refusedNotReady, refusedMerge, voidedWithdrawn:
		return false
	}
	when, failed := strings.CutPrefix(rest, failedMemo+" ")
	if !failed {
		return true
	}
	at, err := time.Parse(time.RFC3339, when)
	if err != nil {
		return false
	}
	return in.now().Sub(at) < retryAfter
}

// act judges one request and does what the verdict says, returning the
// outcome so the memo can record it. A landing records its own: memo is
// what it records it after.
func (in *integrator) act(r request, tip, memo string, watch *stopwatch) (verdict.Outcome, error) {
	ch, snap, landed, err := in.materialize(r, tip, watch)
	if err != nil {
		return "", err
	}
	v := verdict.Decide(ch, snap)
	in.report(r, tip, v)
	if landed.composition != "" {
		fmt.Fprintf(in.out, "  policy   %s\n", landed.composition)
	}

	switch v.Outcome {
	case verdict.Land:
		outcome, err := in.land(ch, v, r, tip, landed, memo, watch)
		if outcome != "" || err != nil {
			if outcome == "" {
				outcome = v.Outcome
			}
			return outcome, err
		}
	case verdict.Stale:
		done := watch.stage(stagePublish)
		in.publishStale(ch, v, tip)
		done()
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

	// composition is what the pass has to say about how the policy layers
	// resolved, or empty when both were where they are configured to be.
	composition string

	// notes are the contributor's attestations as stored, by check name.
	// The verdict does not read them; what lands countersigns them.
	notes map[string][]byte
}

// materialize gathers everything the verdict is a function of: the applied
// delta, the required checks, the recomputed closures, and what intervening
// landings touched.
func (in *integrator) materialize(r request, tip string, watch *stopwatch) (verdict.Change, verdict.Snapshot, landing, error) {
	var land landing
	done := watch.stage(stageCheckout)
	wt, err := addWorktree(in.repo, tip)
	done()
	if err != nil {
		return verdict.Change{}, verdict.Snapshot{}, land, err
	}
	defer wt.remove()

	base, err := gitLine(in.repo, "merge-base", tip, r.head)
	if err != nil {
		return verdict.Change{}, verdict.Snapshot{}, land, fmt.Errorf("%s and %s share no history", verdict.Short(r.head), r.target)
	}
	done = watch.stage(stageEvidence)
	attested, err := in.treeDigest(wt, r.head)
	if err != nil {
		done()
		return verdict.Change{}, verdict.Snapshot{}, land, err
	}
	ch := verdict.Change{ID: r.change, Target: r.target, Base: base, Head: r.head, Attested: attested}
	ch.Evidence, land.notes, err = in.collectEvidence(wt, r.head, attested)
	done()
	if err != nil {
		return ch, verdict.Snapshot{}, land, err
	}

	// The policy is read from the integrator's own side and never from the
	// tree being gated: the project layer from this checkout of the target
	// tip, the floor from the instance repository's integrated tip. Both
	// are resolved once and held for the whole pass.
	done = watch.stage(stagePolicy)
	pol, closePolicy, err := in.policy.open(wt)
	if err != nil {
		done()
		return ch, verdict.Snapshot{}, land, err
	}
	defer closePolicy()
	land.composition = pol.note
	cat, policyDigest, err := pol.catalogue()
	if err != nil {
		done()
		return ch, verdict.Snapshot{}, land, err
	}
	land.policy = policyDigest
	derived, err := pol.derive(wt, base, r.head)
	if err != nil {
		done()
		return ch, verdict.Snapshot{}, land, err
	}
	required, err := pol.required(derived, cat)
	done()
	if err != nil {
		return ch, verdict.Snapshot{}, land, err
	}

	snap := verdict.Snapshot{Required: required, Touched: map[string]bool{}, Now: in.now()}

	// Apply the delta. Every change lands as one commit that the integrator
	// writes on the tip, whatever its base. None of the head's own commits
	// reaches the target, so nothing lands that review did not show. When
	// the base is already the tip, the candidate's tree is the head's tree,
	// which is the attested tree.
	if base == tip {
		done = watch.stage(stageApply)
		tree, err := gitLine(in.repo, "rev-parse", "--verify", r.head+"^{tree}")
		if err == nil {
			land.commit, err = in.commitTree(tree, tip, r)
		}
		done()
		if err != nil {
			return ch, snap, land, err
		}
		land.digest = attested
		snap.Apply = verdict.Applied{Clean: true, Tree: attested}
	} else {
		done = watch.stage(stageApply)
		tree, conflict, err := mergeDelta(in.repo, base, tip, r.head)
		if err != nil {
			done()
			return ch, snap, land, err
		}
		if conflict != "" {
			done()
			snap.Apply = verdict.Applied{Clean: false, Conflict: conflict}
			return ch, snap, land, nil
		}
		if land.commit, err = in.commitTree(tree, tip, r); err != nil {
			done()
			return ch, snap, land, err
		}
		land.digest, err = in.treeDigest(wt, tree)
		done()
		if err != nil {
			return ch, snap, land, err
		}
		snap.Apply = verdict.Applied{Clean: true, Tree: land.digest}

		// What intervening landings touched, in the policy's own terms:
		// the classes the deriver matches over base..tip. Asking the
		// deriver rather than matching globs here keeps one implementation
		// of what a class covers.
		done = watch.stage(stagePolicy)
		intervening, err := pol.derive(wt, base, tip)
		done()
		if err != nil {
			return ch, snap, land, err
		}
		snap.Touched = intervening.classes
	}

	// The closures only have to be recomputed when the tree moved: an
	// unmoved tree presents the identical computation by construction.
	if land.digest != attested {
		done = watch.stage(stageClosures)
		snap.Closures, snap.ClosureErrors = in.closures(wt, land.commit, required)
		done()
	}
	return ch, snap, land, nil
}

// committerEmail is the address on the commits the integrator makes. git
// wants an address and there is none to give: the integrator is a service,
// not a correspondent. .invalid never resolves, which says so in the address
// itself. Which integrator committed is carried by the name beside it.
const committerEmail = "integrator@the-valley.invalid"

// commitTree writes the candidate commit for the change's tree with the tip
// as its only parent. The ref write is then a fast-forward, and the change's
// authorship comes from its head.
//
// Both identities are stated here rather than left to git. The author is the
// asker's, because re-parenting a delta is not authoring it. The committer is
// the integrator, under the name it signs its transfer statement with: the
// commit and the statement about it then name the same actor. Leaving either
// to git would ask git to guess an identity from the environment, and under
// the unit there is nothing to guess from — which is a landing that fails for
// a reason that has nothing to do with the change.
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
	// The committer date is the author's, so that the commit id is a function
	// of what the pass decided from — the tree, the tip, the message and the
	// authorship — and of nothing else. A pass that dies between here and the
	// ref write recomputes the identical commit next time round instead of
	// leaving another unreferenced one behind. Nothing reads this date: when
	// the landing happened is recorded, signed, in the transfer statement.
	out, err := in.gitEnv(map[string]string{
		"GIT_AUTHOR_NAME":     fields[0],
		"GIT_AUTHOR_EMAIL":    fields[1],
		"GIT_AUTHOR_DATE":     fields[2],
		"GIT_COMMITTER_NAME":  in.signerName,
		"GIT_COMMITTER_EMAIL": committerEmail,
		"GIT_COMMITTER_DATE":  fields[2],
	}, cmd...)
	return strings.TrimSpace(out), err
}

// land moves the protected ref and records the landing, in one ref
// transaction, and then says so on the bus. The transaction compares the
// target against the tip the verdict was computed over, so a request that
// lost a race to another lands nothing and is judged again next pass
// against the tip that won.
//
// Everything that can fail happens before the transaction. Composing the
// transfer statement — vetting it, writing it down, signing it — is part
// of deciding: a claim the integrator cannot write down is a claim it has
// not made. Then every object the landing records is built: the evidence
// trees with the transfer statement, the landing record and the outcome
// record. The transaction is the commit point. It moves the target, moves
// every evidence and record ref from the value it was read at, and deletes
// the request, all or nothing. So no landing exists that its records do
// not name, and a failure before the commit point leaves the stream and
// the request where they were, which costs one pass to redo.
//
// When the transaction fails because a ref moved since it was read,
// nothing changed. A target that moved means nothing lands. A record ref
// that moved is read again, the objects are built again from what it
// holds, and the transaction is tried again, landingTries times in all.
//
// Past the commit point what is left is handing the ref move to the host
// to publish, and the bus events. Both are notices of what the
// transaction recorded, and a failure of either is reported and changes
// nothing.
func (in *integrator) land(ch verdict.Change, v verdict.Verdict, r request, tip string, l landing, memo string, watch *stopwatch) (verdict.Outcome, error) {
	done := watch.stage(stageSign)
	transfer, err := in.composeTransfer(ch, v, l.digest, l.commit, tip, l.policy)
	if err != nil {
		done()
		return "", err
	}
	evidence, err := in.countersign(ch, v, l, transfer)
	done()
	if err != nil {
		return "", err
	}

	// Judging and signing can take minutes, and a change can be unreadied
	// or withdrawn meanwhile, or a record can come to name a commit this
	// request brings. So every record is read again here, as the last
	// thing before the commit point, and a refusal lands nothing. A record
	// written between this read and the transaction is still missed: a
	// transaction can compare refs it knows of, and a record created since
	// is one it does not. What lands then is still the head the operator
	// approved.
	done = watch.stage(stageRefusal)
	outcome, why, err := in.finalRefusal(r, tip)
	done()
	if err != nil || outcome != "" {
		if outcome != "" {
			in.reportRefusal(r, tip, outcome, why)
		}
		return outcome, err
	}

	var tx transaction
	for try := 1; ; try++ {
		done = watch.stage(stageRecord)
		tx, err = in.prepareLanding(ch, r, tip, l, evidence, memo)
		done()
		if err != nil {
			return "", fmt.Errorf("nothing landed: %w", err)
		}
		// The commit point.
		done = watch.stage(stageLand)
		err = in.apply(tx.updates)
		done()
		if err == nil {
			break
		}
		if now, found, rerr := resolveRef(in.repo, ch.Target); rerr == nil && (!found || now != tip) {
			return "", fmt.Errorf("%s moved under the verdict; nothing landed: %w", ch.Target, err)
		}
		if try == landingTries {
			return "", fmt.Errorf("nothing landed: a ref the landing records moved under each of %d tries: %w", landingTries, err)
		}
	}
	fmt.Fprintf(in.out, "  landed   %s %s -> %s\n", ch.Target, verdict.Short(tip), verdict.Short(l.commit))
	for _, ref := range tx.evidence {
		fmt.Fprintf(in.out, "  evidence %s\n", ref)
	}
	fmt.Fprintf(in.out, "  record   %s\n", tx.landing)
	if tx.requestNow != "" {
		fmt.Fprintf(in.out, "  request  %s moved to %s while %s landed; it stands for the next pass\n",
			r.ref, verdict.Short(tx.requestNow), verdict.Short(r.head))
	}

	// The notices come last, as best effort: a queue or a bus that fails
	// or hangs changes nothing the transaction recorded.
	done = watch.stage(stagePublish)
	in.queueRefUpdate(ch.Target, tip, l.commit)
	in.publishRefUpdated(ch.Target, tip, l.commit)
	in.publishLanded(ch, v, tip, l.commit)
	done()
	return "", nil
}

// refUpdate is one ref a transaction moves: from old to new, where old is
// the zero object ID for a ref that must not exist yet, and new is "" for
// a ref the transaction deletes.
type refUpdate struct {
	ref, new, old string
}

// transaction is a landing's ref transaction, and what it records.
type transaction struct {
	updates  []refUpdate
	evidence []string // the evidence refs it moves, in order
	landing  string   // the landing record's ref

	// requestNow is the head the request names, when it no longer names
	// the head that lands: a second approval replaced it while this pass
	// worked. It is a new request, and it stands for the next pass.
	requestNow string
}

// prepareLanding builds every object a landing records and the ref
// transaction that applies them: the target moved from the tip to the
// landed commit; each evidence ref moved to its new tree; the landing
// record for the head; the outcome record, which says land after memo;
// and the request deleted. Every ref but the target is read here, and the
// transaction compares it against the value read.
//
// The request is deleted only while it names the head that lands. A
// delete that did not compare would take a second approval, filed while
// this pass worked, with the first. A request that is gone is not
// deleted, and a request that moved is left standing.
func (in *integrator) prepareLanding(ch verdict.Change, r request, tip string, l landing, e landedEvidence, memo string) (transaction, error) {
	zero := strings.Repeat("0", len(tip))
	read := func(ref string) (string, error) {
		oid, found, err := resolveRef(in.repo, ref)
		if !found && err == nil {
			oid = zero
		}
		return oid, err
	}
	tx := transaction{updates: []refUpdate{{ref: ch.Target, new: l.commit, old: tip}}}
	evidence, err := in.evidenceUpdates(e, zero)
	if err != nil {
		return tx, err
	}
	for _, u := range evidence {
		tx.updates = append(tx.updates, u)
		tx.evidence = append(tx.evidence, u.ref)
	}

	record, err := in.landingRecord(r, l.commit)
	if err != nil {
		return tx, err
	}
	tx.landing = change.LandingRef(strings.TrimPrefix(r.target, "refs/heads/"), r.head)
	old, err := read(tx.landing)
	if err != nil {
		return tx, err
	}
	tx.updates = append(tx.updates, refUpdate{ref: tx.landing, new: record, old: old})

	outcome, err := in.writeBlob([]byte(memo + " " + string(verdict.Land) + "\n"))
	if err != nil {
		return tx, err
	}
	if old, err = read(in.memoRef(r)); err != nil {
		return tx, err
	}
	tx.updates = append(tx.updates, refUpdate{ref: in.memoRef(r), new: outcome, old: old})

	now, found, err := resolveRef(in.repo, r.ref)
	switch {
	case err != nil:
		return tx, err
	case found && now == r.head:
		tx.updates = append(tx.updates, refUpdate{ref: r.ref, old: r.head})
	case found:
		tx.requestNow = now
	}
	return tx, nil
}

// landingRecord writes the commit of the landing record for r's head
// landing as commit (change.Landing): a commit with no parent whose tree
// holds the one file change.LandingFile. Its author and committer are the
// integrator, at the landed commit's committer date, so a pass that builds
// the record again builds the identical commit.
func (in *integrator) landingRecord(r request, commit string) (string, error) {
	file := change.Landing{Landed: commit, Request: r.change, Head: r.head}.Format()
	blob, err := in.writeBlob(file)
	if err != nil {
		return "", err
	}
	tree, err := in.writeTree(map[string]string{change.LandingFile: blob})
	if err != nil {
		return "", err
	}
	date, err := gitLine(in.repo, "log", "-1", "--format=%cI", commit)
	if err != nil {
		return "", err
	}
	out, err := in.gitEnv(map[string]string{
		"GIT_AUTHOR_NAME":     in.signerName,
		"GIT_AUTHOR_EMAIL":    committerEmail,
		"GIT_AUTHOR_DATE":     date,
		"GIT_COMMITTER_NAME":  in.signerName,
		"GIT_COMMITTER_EMAIL": committerEmail,
		"GIT_COMMITTER_DATE":  date,
	}, "commit-tree", tree, "-m", fmt.Sprintf("%s landed as %s", r.change, commit))
	return strings.TrimSpace(out), err
}

// apply makes the ref updates as one transaction, with git update-ref
// --stdin: every ref is locked and compared against its old value first,
// and then all of them move, or, when any old value does not match, none.
func (in *integrator) apply(updates []refUpdate) error {
	var stdin strings.Builder
	stdin.WriteString("start\n")
	for _, u := range updates {
		if u.new == "" {
			fmt.Fprintf(&stdin, "delete %s %s\n", u.ref, u.old)
			continue
		}
		fmt.Fprintf(&stdin, "update %s %s %s\n", u.ref, u.new, u.old)
	}
	stdin.WriteString("prepare\ncommit\n")
	_, err := gitInput(in.repo, []byte(stdin.String()), "update-ref", "--stdin")
	return err
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

// readMemo reads the memo of a request, or "" when it has none. The memo
// ref is read by its exact name, so a tag or branch named like a missing
// memo is no memo.
func (in *integrator) readMemo(r request) (string, error) {
	oid, ok, err := resolveRef(in.repo, in.memoRef(r))
	if err != nil || !ok {
		return "", err
	}
	out, ok := gitTry(in.repo, "cat-file", "blob", oid)
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
// the object it points at, as change.EvidenceFingerprint reads them.
// Attestation refs are create-only, so this moves exactly when evidence
// arrives.
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
	var lines []string
	if out = strings.TrimSuffix(out, "\n"); out != "" {
		lines = strings.Split(out, "\n")
	}
	return change.EvidenceFingerprint(lines), nil
}

func (in *integrator) now() time.Time {
	if !in.clock.IsZero() {
		return in.clock
	}
	return time.Now().UTC()
}
