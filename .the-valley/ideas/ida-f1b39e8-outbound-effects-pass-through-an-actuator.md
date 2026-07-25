---
type: idea
id: ida-f1b39e8
status: exploring
title: Outbound effects pass through an actuator
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Outbound effects pass through an actuator

A run holds no credentials for any external system. When it needs something to happen out there, it
expresses an intent to an actuator. The actuator holds the credentials, performs the call, and
returns the response to the run. This is a shape being explored rather than a design that has
settled; what follows is what the shape appears to buy and where it is still unresolved.

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
bundle is enforced, and it is the effect boundary made concrete. Without something occupying that
position, a permission bundle is a description of what a run was supposed to do rather than a
constraint on what it can do.

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

## Open

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
  — refusing rather than documenting.
- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  — the bundle the actuator enforces.
- [roadmap.md, Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller)
  — where effectful reactions arrive.
