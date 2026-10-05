---
type: idea
id: ida-f1b39e8
status: exploring
title: Outbound effects pass through an actuator
created: 2026-07-25
source: design conversation, 2026-07-25
supersedes: [ida-53ec742]
---

# Outbound effects pass through an actuator

A run's dangerous capability is not what it can read or write on the machine it runs on. It is which
durable systems of record beyond that machine it can mutate, and how far such a change propagates
before anyone examines it. The axis is reversibility and propagation rather than sensitivity.
Discarding a workspace undoes local work completely. An integration that lands can trigger a
reconciliation that reconfigures a machine — several systems deep before anyone looks — and the
clock on that propagation starts immediately.

Authority is therefore enforced at the effect boundary. Fine-grained constraint on what a run may
touch locally carries a real productivity cost and buys little where isolation already holds. Effort
spent narrowing a workspace is effort not spent on the boundary that matters. Permissive within,
strict at the edge.

The failure this explains is the approval recorded by the agent that proposed the change. That was
never a containment breach. It was an outbound action against an external system of record, made
with legitimate credentials, doing exactly what the tool was built to do. No isolation boundary
would have prevented it, because nothing was escaping — the effect _was_ the intended egress. A
boundary contains processes. It does not contain credentials handed across it. An isolated run
holding unrestricted credentials for an external system of record has containment without any
reduction in the authority that matters. The remedy is a credential that cannot perform the effect,
not a wall around the process.

The actuator is that boundary made concrete. A run holds no credentials for any external system.
When it needs something to happen out there, it expresses an intent to an actuator. The actuator
holds the credentials, performs the call, and returns the response to the run. This is a shape being
explored rather than a design that has settled; what follows is what the shape appears to buy and
where it is still unresolved.

Two properties are sought. Credentials never enter the run's trajectory, and interactions with
external stateful systems are auditable. These two are orthogonal. Brokering credentials without
recording anything is possible, and so is recording every interaction while the run still holds the
credentials itself. They are coupled deliberately. When the actuator is the only thing able to act,
the record is complete by construction, and an unrecorded action becomes impossible rather than
merely undetected. That is the same move as a gate that refuses rather than one that documents — see
[[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md)).

A further separation is worth keeping in view. Completeness needs a log and an ordering rule, and
nothing more. Cryptographic transparency does something additional: it resists a log operator that
tampers with entries or shows different views to different readers. The second is not required by
the first. Adopting it is a judgement about marginal cost, not a consequence of wanting a complete
record.

The actuator is the mediator the capability model otherwise lacks. It is where a run's permission
bundle is enforced. Without something occupying that position, a permission bundle is a description
of what a run was supposed to do rather than a constraint on what it can do.

Credential non-disclosure is not authority reduction. An actuator that performs whatever it is asked
has concealed the credential and preserved the blast radius entirely. Enforcing policy is the
actuator's primary purpose; withholding the credential is secondary.

The action surface must therefore be enumerable rather than a general forwarder. A proxy that relays
arbitrary requests with credentials injected enforces nothing, and the records it produces are
opaque — a stream of requests nobody can reason about after the fact. A fixed vocabulary of
operations is what makes both policy and audit tractable.

Withholding credentials matters more for a run driven by a language model than for an ordinary
process. Whatever such a run observes enters its trajectory, and a trajectory is persisted,
summarised, replayed into later turns, and sometimes copied into artifacts. A credential held in
process memory is not comparable.

Exchanging a run's own identity for a scoped, short-lived credential is a different technique and
solves a different part of the problem. It provides scoping and lifetime but not non-disclosure. It
composes with an actuator rather than replacing it: the actuator performs the exchange, and the run
never observes the result.

Isolating the run is not thereby dismissed. Runs execute on human-owned workstations and on other
compute the project does not control — machines that also hold signing keys, decryption identities,
and decrypted secrets. Where a run executes as the human whose machine it is, it inherits everything
that human can reach, and securing that is outstanding work rather than a solved problem. The two
concerns are ordered, not exclusive: isolation is necessary and insufficient, and enforcement of
authority belongs at the effect boundary.

One control survives a compromised run host. A signature requiring physical presence cannot be
produced by any process, however privileged, because a hardware token must be touched. See
[[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md)).

Effects already appear in the design at two altitudes. Checks are split into pure and effectful in
[verification.md](../../design/verification.md), and effectful reactions become first-class in
[roadmap.md, Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller).
Both sit well after the attestation work in the plan, so this concern is scheduled considerably
later than the signing and identity questions it touches.

## Open

- What constitutes an effect boundary for this project, and which systems of record sit beyond it.
- Whether effects are declared ahead of a run, in the manner of a capability manifest, or mediated
  as they are attempted.
- An integrator mediates one class of effect — changes landing — while a run does much that no
  mediator observes. Whether the remainder needs a mediator at all, or only scoped credentials, is
  undecided.
- What is assumed about compute the project does not control, given that runs execute on machines
  belonging to their operators.
- Ordering. Recording an intent before acting permits a record of actions that did not occur. Acting
  before recording permits actions that escape the record. Whether intent and outcome are separate
  entries, and what a gap between them is taken to mean, is unsettled.
- Latency. Obtaining an inclusion proof for every call is prohibitive for a run making many. Whether
  only mutations of durable systems of record take the recorded path, while reads pass directly, is
  open.
- How simple the actuator can remain while still enforcing anything. Simplicity keeps the trusted
  surface small, but a sufficiently simple actuator is a forwarder that enforces nothing.
- Whether an actuator constrains exfiltration of data at all. It withholds credentials, but a run
  may still ask it to emit outward whatever the run can see.
- What the action vocabulary contains, and how it grows without decaying into a passthrough.

## Related

- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — refusing rather than documenting, and the control that survives a compromised run host.
- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  — the bundle the actuator enforces.
- [roadmap.md, Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller)
  — where effectful reactions arrive.
