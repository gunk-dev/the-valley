# Integration

How a change lands. The position this implements is
[dcr-439b771](../.the-valley/decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md):
integration is optimistic concurrency control over content-addressed evidence. This document is the
detailed design — the request's shape, the rules in the order they are applied, what the integrator
writes down, and what it publishes. The bet behind it is
[architecture.md](./architecture.md#bet-a-pull-based-integrator-not-a-pre-receive-gate)'s pull-based
integrator, and the failure mode is staleness.

The integrator runs no check. Verification happens where a change is authored
([verification.md](./verification.md)), and the serialized section at the commit point is digest
comparison and a ref write.

## A request is a ref

A contributor asks for integration by pushing one ref:

```
refs/the-valley/integration-requests/<target>/<change>  ->  commit
```

The target segment names the protected branch, so the stream is `refs/heads/<target>`; the last
segment names the change. The commit is the change's head. Its tree is what the attestations were
produced over, and the delta is the merge base with the target's tip up to that head. Nothing else
is encoded, because nothing else has to be: a change is a diff targeting a stream
([ida-93e4f91](../.the-valley/ideas/ida-93e4f91-changes-not-branches.md)), and every part of that is
already in git.

This settles what [contribute.md](./contribute.md) left open at its step 7. The attestations
themselves arrive the way that document already describes, at
`refs/the-valley/attestations/<tree digest>/<signer key hash>`, and the integrator finds them by
digesting the request's tree.

## The controller polls, deliberately

The integrator is a reconciliation loop over those refs. Each pass lists the pending requests,
judges each against the current tip of its target, and acts where the answer has changed since the
last pass. It subscribes to nothing.

That is not a shortcut around the event bus; it is what the bus's own contract says to do. The bus
is unauthenticated ([bd-d853d9c](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md)), and that
bug's gate is exactly "before any automated consumer acts on an event". And per
[architecture.md](./architecture.md#bet-git-as-event-source--a-log-not-a-workflow-engine) the
requests are durable in git with the bus as a projection over them, so level-triggering on the
durable state is the converging shape and a subscription would be the lossy one. Consuming events
arrives in a later phase, and the integrator does not become a different program when it does.

Its memory of what it last decided is durable in git too, at
`refs/the-valley/integration-outcomes/<target>/<change>`: the request's head, the tip it was judged
against, a digest of the attestation namespace, and the outcome. Those are the three things a
verdict is a function of, so a judgement is re-announced when one of them moves and not otherwise.
That is what keeps a level-triggered loop from being a retry storm, and it is why evidence pushed
after a no-evidence verdict is judged again rather than stranded. The digest covers the whole
namespace rather than one change's subject, so an attestation for any change re-judges every pending
request — the conservative direction, and bounded by how many are pending.

Anyone with push access can write the request namespace, so a ref in it that is not a request is an
ordinary thing to find. It is reported and skipped. One unreadable ref does not hold up the readable
ones beside it.

## The commit rule

Applied in this order. The whole of it is a function of data, so it is stated once in
`integrator/verdict.go` and exercised without a repository by the `integrator-unit` check.

**1. The delta must apply cleanly to the tip.** A three-way merge of the tip and the head against
the base the delta was authored against, with no working tree. A conflict means resolving it would
produce a tree nobody has authored; that resolution is new authorship, and new authorship needs new
evidence. So a conflict is stale with **every** required check named: retry is not partial from a
tree nobody wrote.

**2. The required checks come from the policy at the target tip.** The deriver of
[dcr-f41f718](../.the-valley/decisions/dcr-f41f718-declared-verification-policy.md) —
`valley checks` — matches the diff against the composed classes. Both layers are read from the
integrator's own side: the project layer from a checkout of the target tip, the instance layer from
where the integrator is configured to find it. A change never supplies the policy that gates it.

Which rule governs a check is the policy's, never the attestation's. The policy names the runner,
and the runner is what makes a check pure or effectful; a statement whose predicate type contradicts
it is refused rather than judged by the rule it nominated for itself. Where a subject carries
several attestations for one check — one per signer — the first admissible one is the evidence, and
a note that does not verify is not displaced by a good one found after it. A check refused that way
ends the judgement: the change is not behind, and redoing work would not change the answer.

**3. A pure check transfers by closure-digest equality.** The integrator recomputes the check's
input-closure digest over the tree that would land — `attest inputs`, which evaluates and digests
and builds nothing — and compares it with the digest the attestation recorded. Equality means the
landed tree presents the same computation over the same inputs, so the recorded result is the result
on the new base too. Inequality means that check, and only that check, is stale. When the landed
tree is the attested tree there is nothing to compare and the evidence stands by construction.

**4. An effectful check transfers while its scope is untouched and its observation is fresh.** Both
must hold: no intervening landed change touched the paths of a class that requires the check, and
the observation is younger than the validity window Δ the policy declares for it. The first is asked
of the deriver rather than matched here — the classes it reports over the intervening range are
exactly the classes those landings touched. Absent a declared window the check is re-demanded: a
window nobody wrote down is not a window a tool may invent.

**5. Everything standing lands.** The protected ref is fast-forwarded by a compare-and-swap against
the tip the verdict was computed over, so a request that lost a race lands nothing and is judged
again next pass against the tip that won. That local ref write is the integrator's own privilege,
and the structural invariant on the bare repo is what makes it the only one.

**6. Anything invalidated is one `request-stale` naming exactly those checks.** The request ref
stays where it is, so resubmitting is re-attesting the named checks and pushing again.

## What the integrator writes down

Two statements, side by side in one attestation ref per subject.

The contributor's original notes gain the integrator's signature as **sibling lines under the text
they already carry** — several parties attesting to one statement, which is the composition
[dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md) already provides.
Nothing about the contributor's statement changes; it stays about the tree it was made over, and it
stays at a ref keyed by that tree's digest.

The **transfer statement** is the integrator's own claim, and it is a separate statement because it
is about a different tree: the one that landed. Its predicate type is
`the-valley/integration/transfer/v1`, and it records the change, the target stream, the base the
evidence was produced over, the policy the verdict was derived under, and the per-check verdicts —
each citing the statement it is about by a digest of the bytes that statement's signature covers.
The policy it names is the one the integrator composed from its own checkout at the target tip,
never the submitted tree, which is the direction
[bd-eaefe82](../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md) points at.

When nothing intervened the two trees are the same tree and both notes sit under one ref. When the
delta landed on a moved tip they are two refs, each keyed by the tree its statement is about.

## What it publishes

Two events, in [schema/events.cue](../schema/events.cue), on
`valley.git.<repo>.integration-succeeded` and `valley.git.<repo>.request-stale`. The mechanism is
the post-receive hook's, exactly: one `nats pub`, best-effort, never fatal — git is the source of
truth and the bus is the replaceable component.

**There is no rejection event, on purpose.** A note whose signature does not check out, text that is
not the written form of what it says, or a statement about a different tree is not staleness:
re-attesting would not fix it, and calling it stale would tell a contributor to redo work that is
not the problem. Rather than stretch either event to cover a case they do not describe, the
vocabulary stays as it is and a refusal is the integrator's own output. Naming that case is a
decision this build deliberately does not take alone.

## Identity, for now

The integrator signs with a key given by configuration, and accepts attestations from the signers in
a known-signers file — one verifier key per line, the format
[dcr-de9d996](../.the-valley/decisions/dcr-de9d996-statement-text-and-signed-note.md) fixes.

That file is the registry's interim compilation. Identity is a governed registry
([dcr-b87f6e8](../.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md)) whose
entries compile into what each enforcement boundary checks, and no compiler exists yet, so the
compiled artifact is supplied directly. Nothing here parses a registry, and nothing should start to
until that decision's first compiler ships.

An attestation signed by nobody in the file does not transfer, and that is reported per check rather
than treated as a forgery. What to do about an unknown attester — re-verification, trust
measurement, a threshold — belongs to [Phase 6](./roadmap.md#phase-6--trust-backstop) and is
deliberately absent.

## What is deliberately absent

- **Bus consumption.** Above.
- **Batching, reservations, sharding.** The known knobs for queue semantics under contention
  ([dcr-439b771](../.the-valley/decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md),
  _Open_). None is designed, and the verdict stays per-change so none is precluded.
- **Trust thresholds and re-verification.** Above.
- **Self-integration.** The integrator is code in a repo; how its own changes land is open where it
  already is ([openquestions.md](./openquestions.md)).
- **Any transport but refs.** The verdict is a function of a change, a snapshot and the known
  signers, with no git, nix or clock inside it, so a second transport supplies those three and
  reuses the rule unchanged.
- **Check definitions from the instance's pin.** The policy comes from the integrator's side; the
  check derivations still come from the tree being gated. That is
  [bd-eaefe82](../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md), and an
  enforcement point existing now is what turns it from a structural defect into a live one.
