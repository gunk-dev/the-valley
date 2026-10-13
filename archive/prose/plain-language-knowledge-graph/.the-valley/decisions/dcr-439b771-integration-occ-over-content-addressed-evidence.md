---
type: decision
id: dcr-439b771
status: decided
title: Reuse verification results when a change's base advances
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Reuse verification results when a change's base advances

When other changes land before a submitted change, the integrator determines which of that change's
verification results still apply. It checks that the change can be applied cleanly and that the
evidence remains valid for the resulting tree. Checks whose evidence still applies can be reused;
the contributor supplies new evidence for the others. The integrator does not run the checks itself.

This is the rule for transferring attestations to a new base, the question raised in
[[ida-cbcbb3c]](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md).

## Apply the change and validate its evidence

The integrator first tries to apply the change's delta to the current target tip. The delta is the
difference the contributor wants to land, as defined in
[[ida-93e4f91]](../ideas/ida-93e4f91-changes-not-branches.md).

A conflict requires the contributor to author a resolution and attest it. Until that resolution
exists, the integrator has no resulting tree against which to compare check inputs. A conflict
therefore requires new evidence for every required check.

If the change applies cleanly, the integrator evaluates the evidence for each required check. Policy
selects the runner and therefore whether the pure or effectful transfer rule applies. An attestation
whose declared type contradicts that runner is rejected. Allowing the attestation to choose its own
rule would let its author bypass the required verification.

Several signers may have supplied attestations for the same check. The integrator uses the first
admissible attestation. An invalid note must not be silently replaced by a valid note found later.
Evidence must pass validation before the transfer rules are applied. Forged or malformed evidence
ends the judgement with a rejection; it is not treated as a stale result that needs another run.

The current collector needs review against the rule for multiple notes; the observed selection
behavior is recorded in [open questions](../../design/openquestions.md#integration-and-evidence).

## Pure checks transfer by closure-digest equality

A pure check produces a deterministic result from content-addressed inputs
([verification.md](../../design/verification.md)). Its **input closure** includes all the sources,
dependencies, and check definitions used in that computation. The closure has a digest derived from
its contents, which the attestation records
([[dcr-0de694f]](dcr-0de694f-phase2-attestation-shape.md)).

The reference implementation records an input digest and a separate derivation digest. The
integrator recomputes both for the tree that would land:

- If both match the attested values, the check identifies the same computation over the same inputs
  and its result can be reused.
- If either differs, that check is stale and requires new evidence.

For example, a documentation change can land while an application change is being checked. If the
documentation is outside a particular check's input closure, that check's evidence still applies
after the application change is rebased. A change to a dependency inside the closure makes that
check's evidence stale even when the rebase has no text conflicts.

Pure attestations do not expire: time is not an input to a pure check. A conflicting result on
identical inputs challenges the claimed purity or the evidence. It is handled by the re-verification
and trust mechanisms in [verification.md](../../design/verification.md).

## Effectful checks transfer while their scope is untouched and the observation is fresh

An effectful check observes mutable external state, such as a running service. That state may change
between the observation and integration, including when the check runs immediately before
integration. An input digest cannot establish that the observation is still true. Policy instead
sets a limit on how old the observation may be.

An effectful result can be reused only when both conditions hold:

1. No intervening landed change touched the paths in the policy class that requires the check.
2. The observation is younger than the check's declared validity window, Δ.

A **policy class** is a named set of path patterns with required checks, defined in
[[dcr-f41f718]](dcr-f41f718-declared-verification-policy.md). The validity window follows the same
policy layering: a template can supply a default, a project can override an overridable value, and a
valley can mandate a value in its floor.

Reading immutable, content-addressed data over a network can still be a pure read. The validity
window is needed for observations of mutable state.

## Staleness is per-check and computable

When evidence cannot transfer, the integrator emits one `request-stale` event listing the checks
that need new evidence. The contributor reruns those checks where the change is authored. The
remaining evidence can be reused. This gives the staleness result described in
[architecture.md](../../design/architecture.md#integration-through-a-controller) an explicit set of
next actions.

Checking and rechecking happen outside the serialized integration step. That step compares digests
and updates the target ref, so it does not wait for test execution.

## Record the contributor's results and the integration decision

Each landing records the evidence it used and the integrator's decision:

1. The integrator adds its signature to the contributor's original statement. The statement text
   stays unchanged, and the signatures are separate lines in the same note, as specified in
   [[dcr-0de694f]](dcr-0de694f-phase2-attestation-shape.md).
2. The integrator writes a **transfer statement**. It identifies the original statement by the
   digest of its signed text and records each check's verdict and the check definitions used.

For the floor's checks, those definitions must come from the valley's own pinned source. The
submitted tree must not supply the definitions that judge it. The implementation gap is tracked in
[[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md).

Every landing gets a transfer statement, even when policy requires no checks. This can happen when
all changed paths belong to classes that require nothing, or when the delta touches no paths. The
statement explicitly records that no checks were required. The text format cannot represent an empty
verdict list, so it uses a line stating that result
([[dcr-de9d996]](dcr-de9d996-statement-text-and-signed-note.md)).

The contributor's statement describes the tree it checked. The transfer statement describes the tree
that lands. Each is stored under the ref for its subject digest. If the base advanced and the trees
differ, the statements occupy two refs; if the trees are identical, they share one ref.

The integrator composes the transfer statement before updating the target ref. If a prerequisite
fails before the ref update, the change has not landed and can be considered on a later pass. Once
the ref is updated, the change has landed. The integrator then completes its recording steps,
reports any failures, and consumes the request so it cannot be processed again as an empty delta.

## Relationship to optimistic concurrency control and caching

These rules apply **optimistic concurrency control** to integration. Contributors work against a
fixed tree while other changes proceed. At integration, the system checks whether the assumptions
behind their results still hold. A change lands as one unit, and one integrator writes each target
stream, establishing a serial order of landed changes.

The correspondence gives database theory a concrete place to start:

| Database concept | Repository counterpart                                             |
| ---------------- | ------------------------------------------------------------------ |
| Transaction      | A proposed change that lands as a unit                             |
| Snapshot         | The tree used to produce the evidence                              |
| Read set         | Inputs and definitions on which the required checks depend         |
| Write set        | The change's delta                                                 |
| Validation       | Clean application and evidence transfer against the current target |
| Commit           | The successful protected-ref update                                |
| Retry            | Refreshing the change or the checks whose evidence became stale    |

This is a model of evidence validity. A check's inputs do not capture every assumption a contributor
made while authoring a change. Governing policy and signer state also affect admission. A serial
order of ref updates therefore does not by itself prove that concurrent authorship is equivalent to
serial execution. Effectful observations have the bounded-staleness semantics described above. These
limits are part of the formalization still to do.

An attestation serves as a signed cache entry for a check result. Pure results are keyed by input
content; effectful observations have a time limit. Integration determines which entries remain
usable on the tree that would land.

## Throughput and contention: a connection to explore

Database concurrency theory offers tools for studying how much work a repository can integrate and
when adding more contributors stops helping. The chosen integration rule makes this a useful
research direction; it does not establish a throughput guarantee.

Kung and Robinson's
[optimistic concurrency control paper](https://www.eecs.harvard.edu/~htk/publication/1981-tods-kung-robinson.pdf)
separates transaction work from validation and writing. It also examines conflict assumptions and
starvation. The repository analogue is to prepare changes independently, then validate their
remaining evidence before landing them.

Three quantities need separate measurement:

- **Integration capacity:** how many requests the controller can evaluate and land per unit of time.
  Policy resolution, input evaluation, signature verification, and Git operations all take time.
- **Evidence-production capacity:** how much check work contributors and builders can complete per
  unit of time, including work repeated after invalidation.
- **Contention:** how often landings invalidate evidence for pending work. Overlap between changed
  files and check inputs matters more than the number of contributors alone.

Moving tests outside the integrator removes their execution from its serial work. Long tests can
still reduce useful throughput: more changes may land while a test runs, and refreshing its evidence
uses capacity that could have served another change. More parallel workers may help produce evidence
or may spend their time repeating invalidated work. Which effect dominates is a workload question.

A shared build definition is a possible hotspot because many checks depend on it. Narrower, complete
input closures reduce unnecessary overlap. Batching can let several changes share one checked tree.
A reservation can give repeatedly invalidated work time to finish, at the cost of delaying competing
landings. These are candidate policies to compare, not consequences already proved by the analogy.

A useful experiment would vary check duration, input overlap, arrival rate, and builder capacity.
Measure successful landings per unit of time, time from submission to landing, repeated check work,
and the longest waits. High total throughput can coexist with starvation of a particular request.
[[bd-c324184]](../bugs/bd-c324184-occ-starvation-under-contention.md) records that failure
condition.

The open theoretical question is which database results still apply when validation is per-check,
retry can reuse part of the evidence, and authoring is not a deterministic transaction that can
simply be replayed. A model should state those assumptions before predicting a benefit from
batching, reservations, or additional concurrency.

## Consequences

- A clean textual rebase can still invalidate check evidence. Changes inside a pure check's input
  closure require that check to run again, covering dependencies that text-conflict detection alone
  would miss.
- Smaller, accurately declared input closures let more results survive unrelated changes. Every
  actual dependency still has to be included for reuse to be valid. This is the same requirement
  that makes a build hermetic.
- Verification performed in the contributor's environment can count at integration. Checks use the
  same runtime wherever they execute. Integration throughput depends on conflicts and the capacity
  to produce new evidence in parallel, without serializing test execution. These are the
  requirements in [requirements.md](../../design/requirements.md), need 2.

## Prior art

The design combines optimistic concurrency control from databases with content-addressed build
caching and early cutoff, as used in Nix and Bazel. It uses signed evidence and per-check digests to
decide whether earlier verification results apply when a change lands.

## Open

- Queue behavior under contention remains undecided. Possible approaches include checking several
  pending changes together, giving a long-waiting change a brief reservation, and splitting work
  across target streams. The integration rule must allow these approaches.
- A statement needs a way to identify the change it concerns independently of the exact tree it
  checked. The corresponding member of the subject digest set remains undecided.
- How the integrator integrates changes to itself remains open in
  [openquestions.md](../../design/openquestions.md).

## Related

- Attestations when a change's base moves:
  [[ida-cbcbb3c]](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md)
- Change identity across rebases: [[ida-93e4f91]](../ideas/ida-93e4f91-changes-not-branches.md)
- Attestation format and multiple signatures:
  [[dcr-0de694f]](dcr-0de694f-phase2-attestation-shape.md)
- Required checks, path classes, and validity windows:
  [[dcr-f41f718]](dcr-f41f718-declared-verification-policy.md)
- Check definitions supplied by the governing valley:
  [[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)
- Pure and effectful checks, and verification of their claims:
  [verification.md](../../design/verification.md)
- The integration controller and how it reports stale requests:
  [architecture.md](../../design/architecture.md#integration-through-a-controller)
