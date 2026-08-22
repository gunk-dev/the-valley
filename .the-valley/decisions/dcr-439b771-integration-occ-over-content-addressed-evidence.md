---
type: decision
id: dcr-439b771
status: decided
title: Integration is optimistic concurrency control over content-addressed evidence
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Integration is optimistic concurrency control over content-addressed evidence

Integration is optimistic concurrency control over content-addressed evidence. Optimistic
concurrency control is the discipline, from the database literature, in which work proceeds without
locks against a fixed snapshot of the data, and a commit point later checks whether anything the
work read has moved; work whose reads still hold commits as if nothing had intervened, and work
whose reads have moved is redone. This node maps Phase 3 integration onto that discipline exactly,
and in doing so answers the question [[ida-cbcbb3c]]
([ida-cbcbb3c-attestations-bind-to-a-base.md](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md))
left open: what happens to a change's attestations when its base moves.

## The transaction model

A change ([[ida-93e4f91]],
[ida-93e4f91-changes-not-branches.md](../ideas/ida-93e4f91-changes-not-branches.md)) is a
transaction — a unit of work that lands whole or not at all. Its snapshot — the state the work was
performed against — is the base tree its attestations were produced over. Its read set — everything
its validity depends on having read — is the union of its required checks' input closures, held per
check. A check's input closure is the complete set of inputs the check's computation consumes — the
sources it reads, the dependencies it builds against, its own definition — each named by a digest of
its content, so the closure as a whole has one digest. Its write set — what the transaction changes
— is its delta. The integrator is the commit point: a single writer per target stream, so landed
changes form one serial order per stream.

The commit rule has two parts. First, the delta must apply cleanly to the current tip. A conflict
means resolving it would produce a tree nobody has authored; the resolution is new authorship, and
new authorship requires a new attestation. A conflict therefore invalidates every required check,
not a subset: staleness is per-check only where the transaction's read set is what moved, and a tree
nobody has written has no read set to compare against. Second, per required check, the evidence must
transfer — by the rules below.

Which rule governs a check is the policy's to say. The policy names the check's runner, and the
runner is what makes the check pure or effectful; the attestation's own predicate type is a claim
about itself, not a licence to choose the rule it is judged under. An attestation whose predicate
type contradicts the runner the policy names is refused rather than judged, because judging it by
the rule it nominated is how an attester opts out of closure comparison.

Where several attestations are stored for one check — one per signer, as the storage rule provides —
the first admissible one is the evidence, and a note that does not verify at all stands rather than
being displaced by a good one found after it. Admissibility is settled before any transfer rule
runs, and a check whose evidence is inadmissible in the way a forgery is inadmissible ends the
judgement there: the change is not behind, and nothing about redoing work would change the answer.

## Pure checks transfer by closure-digest equality

A pure check is a computation with content-addressed inputs and a deterministic result
([verification.md](../../design/verification.md)). Its attestation records the digest of its input
closure ([[dcr-0de694f]]
([dcr-0de694f-phase2-attestation-shape.md](./dcr-0de694f-phase2-attestation-shape.md))). At commit,
the integrator recomputes each closure digest over the rebased tree. Equality means the rebased tree
presents the same computation over the same inputs, so the recorded result is the result on the new
base too — the argument that makes a content-addressed build cache sound. Inequality means that
check, and only that check, is stale. Content-addressed evidence does not age: a pure attestation
carries no expiry, because time is not among its inputs.

A pure check that fails on identical inputs is a falsified purity claim, and belongs to
re-verification and trust measurement — the mechanism stack in
[verification.md](../../design/verification.md) — never to a validity window.

## Effectful checks transfer while their scope is untouched and the observation is fresh

An effectful check reads mutable world state — real networks, external services
([verification.md](../../design/verification.md)). Mutable state has no digest, so the equality rule
cannot apply, and serializable semantics against it — every observation reading as if it happened at
the instant its change landed — are unattainable in principle: even a check re-run at integration
observes the world some interval before the ref moves. The honest semantics are bounded staleness:
an observation is accepted while nothing relevant has changed under it and it is recent enough.
Concretely, an effectful observation transfers iff both hold: no intervening landed change touched
the paths of the policy class that requires the check — a policy class being [[dcr-f41f718]]'s
([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md)) unit
of mapping, a named set of path patterns carrying required checks — and the observation is younger
than a validity window Δ. Δ is declared policy, per check: a template default a project may
override, floor-mandatable by the valley, in that decision's two layers. A network read of
immutable, content-addressed data is a pure read; Δ exists only for reads of mutable world.

## Staleness is per-check and computable

A failed transfer surfaces as one `request-stale` event naming the invalidated checks — the
staleness failure mode
[architecture.md's integrator bet](../../design/architecture.md#bet-a-pull-based-integrator-not-a-pre-receive-gate)
commits to, made precise. Retry is partial: only the named checks need new evidence. Retry is
decentralized: re-attestation happens where the change is authored. The serialized critical section
at the commit point is digest comparison and a ref write; verification latency never enters it.

## The evidence composes as siblings

The landed change carries two statements side by side. The contributor's original attestation gains
the integrator's signature as a sibling line under the text it already carries, which is the
composition [[dcr-0de694f]]
([dcr-0de694f-phase2-attestation-shape.md](./dcr-0de694f-phase2-attestation-shape.md)) provides. The
integrator's transfer statement is the second: it cites the original by a digest of the bytes that
original's signature covers, and records the per-check verdicts and the check definitions used.
Those definitions are resolved from the instance's own pin, never from the tree being gated — the
direction [[bd-eaefe82]]
([bd-eaefe82-check-definitions-come-from-the-branch.md](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md))
names, adopted here for the floor's checks.

A transfer statement is written for every landing, including one the policy required no check of.
Requiring nothing is an ordinary result of the commit rule — every path the delta touches is covered
by a class that asks for nothing, or the delta touches no path at all — so the statement says so in
a line rather than carrying an empty list of verdicts, which is a container the written form cannot
write down at all ([[dcr-de9d996]]
([dcr-de9d996-statement-text-and-signed-note.md](./dcr-de9d996-statement-text-and-signed-note.md))).
The commit point records what it decided in every case it can decide, or there are landings nothing
attests to.

The order around the commit point follows from what a commit point is. The ref write is the commit;
everything that can fail while the transaction is still deciding happens before it, and composing
the transfer statement is part of deciding rather than of recording — a claim the integrator cannot
write down is a claim it has not made. A failure before the commit is a transaction abandoned, which
costs a pass and strands nothing. After the commit the landing is in the serial order whatever else
happens, so the steps that record it run to the end and report what they could not do, and the
request is consumed there rather than left to be judged again against a delta that is now empty.

The two are separate statements because they are about two different trees. A contributor's
statement is about the tree it was produced over; a transfer statement is about the tree that
landed, which is a different tree exactly when something intervened. An attestation is stored at a
ref keyed by its subject digest, so the two statements are keyed by the two trees, and a landing
where the base moved writes two refs. Where nothing intervened the trees are one tree and the two
statements sit under one ref — which is the case the phrase "side by side" describes, and it is the
common one rather than the general one.

## The reframe this rests on

An attestation is a signed cache entry for a content-addressed computation, and integration is a
cache lookup. The attestation store holds two standard entry classes: content-keyed entries for pure
checks, and time-bounded entries for effectful ones.

## Consequences

- Re-verification concentrates exactly where semantic-conflict risk concentrates: a check is
  re-demanded precisely when landed changes touched what it reads. A textually clean rebase over a
  semantically interfering change is caught by construction, because the interfering change is
  inside the invalidated closure.
- Granularity is the economic lever: the finer a check's declared source set, the more evidence
  survives integration. Declaring closures honestly and making builds hermetic are the same act, so
  the incentive gradient points toward the purity [verification.md](../../design/verification.md)
  already asks for.
- The requirements this serves ([requirements.md](../../design/requirements.md), need 2):
  verification performed where a change is authored counts across the integration boundary; checks
  run in one runtime everywhere; integration throughput is bounded by conflict rate and author-side
  re-attestation parallelism, never by check latency on the critical path.

## Prior art

Optimistic concurrency control is from the database literature. Content-addressed build caching and
early cutoff are from the Nix and Bazel lineage. The composition — commit-time conflict detection
via per-check content digests over signed evidence — is this design's own.

## Open

- Queue semantics under contention: batching (attesting once over several pending changes' combined
  tree), aging as pressure granting a brief reservation, and stream sharding are the known knobs;
  none is designed, and the core must not preclude them.
- The change-identity member of the subject digest set — naming what change a statement is about,
  distinct from which exact tree — is a separate small decision.
- Integrator self-integration remains open where it already is
  ([openquestions.md](../../design/openquestions.md)).

## Related

- The binding this designs the consequence of: [[ida-cbcbb3c]]
  ([ida-cbcbb3c-attestations-bind-to-a-base.md](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md))
- The transaction's unit, stable across rebases: [[ida-93e4f91]]
  ([ida-93e4f91-changes-not-branches.md](../ideas/ida-93e4f91-changes-not-branches.md))
- The statement the transfer rules read from, and the sibling composition the transfer statement
  uses: [[dcr-0de694f]]
  ([dcr-0de694f-phase2-attestation-shape.md](./dcr-0de694f-phase2-attestation-shape.md))
- The policy classes whose paths scope an effectful check, and the layering Δ is declared in:
  [[dcr-f41f718]]
  ([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md))
- The resolution of check definitions from the instance's pin: [[bd-eaefe82]]
  ([bd-eaefe82-check-definitions-come-from-the-branch.md](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md))
- The two kinds of checks and the mechanism stack behind falsified purity:
  [verification.md](../../design/verification.md)
- The integrator bet and staleness as the unified failure mode:
  [architecture.md](../../design/architecture.md#bet-a-pull-based-integrator-not-a-pre-receive-gate)
