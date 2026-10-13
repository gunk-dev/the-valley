# Integration

The integrator evaluates a requested change against the current target and governing policy. It
lands the change when the delta applies and every required check has acceptable evidence that still
applies to the resulting tree.

The integrator does not run checks. It evaluates their inputs, verifies statements, and compares
digests. [Verification](./verification.md) defines that evidence. The
[integration decision](../.the-valley/decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md)
describes the approach as optimistic concurrency control: work is prepared independently, then
validated against the state present when it commits.

## Request refs

A request is a Git ref pointing to the submitted head:

```text
refs/the-valley/integration-requests/<target>/<change> -> commit
```

The target identifies `refs/heads/<target>`. The final segment identifies the change. The integrator
computes the delta from the merge base of the submitted head and the target tip.

The submitted head's tree is the subject of the contributor's attestations. The integrator digests
that tree and finds its evidence beneath:

```text
refs/the-valley/attestations/<tree digest>/<signer key hash>
```

A person, agent, or tool can submit a request under the access granted to it. Requesting integration
and approving a change are separate acts. Where policy requires human judgment, the intended
approval is a signed statement about the change, as described in the
[approval decision](../.the-valley/decisions/dcr-9c75283-substrate-adoption-and-the-core.md).

The ref is the current transport. A
[change](../.the-valley/ideas/ida-93e4f91-changes-not-branches.md) is conceptually a delta targeting
a stream, independent of the branch used to carry it.

## Request reconciliation

The current controller polls Git refs. Each pass lists pending requests and evaluates those whose
recorded inputs have changed. It does not subscribe to the event bus. Requests remain available even
if event publication fails.

The controller stores its last answer at:

```text
refs/the-valley/integration-outcomes/<target>/<change>
```

The stored comparison key contains the request head, the target tip, and a digest of the attestation
namespace. New evidence therefore triggers reconsideration even when the request head has not moved.
The digest covers the entire namespace, so evidence for one change may cause other pending requests
to be reconsidered too.

An unchanged answer is not announced every polling interval. Operational failures are recorded with
a timestamp and retried after ten minutes, or sooner if one of those inputs changes. Malformed or
unreadable request refs are reported and skipped so other requests can proceed.

This describes the current retry mechanism. Changes to governing policy or signer state outside
those three inputs also matter to a decision. Whether they should invalidate the stored answer is a
remaining [reconciliation question](./openquestions.md#integration-and-evidence).

## Evaluating a request

The rule is implemented as a function of supplied data in
[integrator/verdict/verdict.go](../integrator/verdict/verdict.go). Repository access, policy
resolution, input evaluation, and the current time are collected before calling it.

### 1. Apply the delta

The integrator attempts a three-way merge of the target tip and submitted head using their merge
base. A conflict makes the request stale.

Resolving a conflict creates newly authored content. The contributor must resolve it and produce new
evidence for every check required of that resolution. The integrator does not invent a resolution
and reuse evidence for a tree no one checked.

### 2. Derive the required checks

[Policy composition](../.the-valley/decisions/dcr-f41f718-declared-verification-policy.md) selects
checks from the paths touched by the change. The project layer comes from the target's integrated
tip. The floor comes from the owning valley's integrated repository. The submitted tree cannot
supply the policy that admits itself.

The policy's runner selects the transfer rule: `nix` uses the pure rule, and `command` uses the
effectful rule. An attestation claiming a different kind is rejected.

Reading policy from governing state does not yet secure the check implementation. Check derivations
still come from the candidate tree. The
[check-definition bug](../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)
tracks this gap.

### 3. Establish admissible evidence

For each required check, the integrator verifies the selected note's text, schema, signatures, and
subject.

- A passing statement signed by a recognized signer can be considered for transfer.
- Missing evidence, an unknown signer, or a recorded failure does not establish that the check
  passed.
- Invalid signatures, noncanonical text, a wrong subject, or an incompatible predicate are rejection
  cases.

Unknown signers are reported as evidence that does not transfer. The current integrator does not
assign them a trust score or arrange independent verification.

Several signers may supply notes for the same check. The intended rule must not allow invalid
evidence to be hidden by later valid evidence. The current collector's selection behavior needs
review against that rule; see [open questions](./openquestions.md#integration-and-evidence). Note
verification itself rejects an invalid signature by a recognized key even when that same note has a
valid sibling signature.

### 4. Transfer pure evidence

If the resulting tree is the attested tree, acceptable pure evidence applies directly.

If the tree changed, the integrator runs `attest inputs` over the tree that would land. This
evaluates the check and computes its input and derivation digests without building it. Both must
equal the values in the attestation.

Equal digests identify the same computation over the same inputs. A mismatch or failure to compute
them makes that check stale. Other checks whose evidence transfers do not need to be repeated merely
because this one failed to transfer.

Pure evidence has no age-based expiry in this rule. Whether the material needed for later
re-verification remains available is a separate retention question.

### 5. Transfer effectful evidence

An effectful observation transfers only when both conditions hold:

- Intervening integrated changes have not touched any path class that requires the check.
- The observation is younger than the validity window declared in policy.

The policy deriver identifies classes touched between the submitted change's base and the current
target tip. A missing validity window does not receive an invented default; the check is requested
again.

This rule permits bounded staleness in an observation of the external world. It does not establish
that the observed service or environment has remained unchanged.

### 6. Land or report what prevents landing

If all required evidence transfers, the integrator prepares the landing and updates the protected
ref. If evidence is missing or stale, it leaves the request pending and reports the affected checks.
A signature or statement rejection is reported as such rather than disguised as a request to rerun
ordinary checks.

A request requiring no checks can also land. The record explicitly states that no checks were
required.

## The ref update and its consequences

The protected-ref write is the commit point. It uses compare-and-swap against the tip used for the
decision. If another write won the race, nothing from this attempt lands and the request must be
evaluated against the new tip.

Before that write, the integrator composes, validates, and signs the transfer statement and prepares
the evidence it will store. A failure during preparation leaves the target and request unchanged.

After the ref moves, the landing has happened. The integrator then attempts each remaining step:

1. Store the evidence.
2. Delete the consumed request ref.
3. Publish the landing event.

A failure in one step does not skip the later steps. In particular, the controller attempts to
consume the request even if storing evidence fails. Otherwise an already landed change could remain
pending and be repeatedly evaluated as an empty delta.

These writes are not one atomic transaction. A post-landing storage or deletion failure is reported
as an incompletely recorded landing. Event publication is best-effort. Recovery must distinguish
these cases from a change that never landed.

## Evidence recorded at landing

The integrator records two kinds of statement.

The contributor's accepted notes gain the integrator's signature under their existing text. Their
subject remains the originally checked tree. The original signatures are preserved.

A separate **transfer statement** describes the integrator's own decision about the landed tree. Its
predicate type is `the-valley/integration/transfer/v1`. It records:

- The change and target ref.
- The originally attested tree.
- The composed policy digest and the target tip used for the decision.
- Each required check's transfer rule and the digest of the statement text it relies on.

If no checks were required, it records `predicate.required none` instead of a checks array.
[Verification](./verification.md#line-order) defines the exact field order.

When the original and landed trees match, their notes can be stored under the same subject. When
they differ, each statement remains stored under the digest of the tree it describes. The transfer
statement connects them.

## Published outcomes

[schema/events.cue](../schema/events.cue) defines the two currently published outcomes:

| Subject                                   | Meaning                                                                |
| ----------------------------------------- | ---------------------------------------------------------------------- |
| `valley.git.<repo>.integration-succeeded` | The protected ref was updated                                          |
| `valley.git.<repo>.request-stale`         | The request needs conflict resolution or evidence for the named checks |

Publication uses the same best-effort mechanism as ref-update projection. Bus failure does not undo
a landing.

There is currently no rejection event. Invalid evidence is reported through the integrator's output,
and the outcome is remembered in Git. Adding a distinct event remains a design decision; the absence
of that event does not mean the integrator has no rejection case.

## Identity and remaining work

The integrator signs with its configured key and verifies attestations against a known-signers file.
The [identity registry](../.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) now
has a compiler that produces verifier and access artifacts from accepted governing state. The
integrator consumes those artifacts rather than parsing the registry itself.

The current design still needs trusted sources for mandated check definitions, verified delegation
and physical approval, and a trust process for unknown signers. It also needs a complete procedure
for changes to the integrator and its governing policy.

Contention policy, batching, reservations, and sharding remain open. Bus events may later wake the
reconciliation loop, but event consumption requires the
[bus authorization work](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md). Other request
transports could reuse the verdict function by supplying the same change and snapshot data.
