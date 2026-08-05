---
type: idea
id: ida-5a59fef
status: exploring
title: One uniform plumbing carries every self-improving loop
created: 2026-08-05
source: design conversation, 2026-08-05
---

# One uniform plumbing carries every self-improving loop

The engineering goal is the plumbing: one substrate by which all of the system's feedback loops can
self-improve, with the design as simple and uniform as possible. The individual loops matter less
than the shared shape that carries them.

## The anatomy — five shared pieces

Every loop is built from the same five pieces:

- **Sense.** Signals arrive under the three signal contracts — domain events, metrics, and logs —
  decided in [[dcr-62ecc36]]
  ([dcr-62ecc36-signal-contracts.md](../decisions/dcr-62ecc36-signal-contracts.md)).
- **Remember.** The promotion ladder distills decaying raw signal into durable insight events and,
  where warranted, knowledge-graph nodes.
- **Orient.** Derived views over the one history.
- **Decide.** A declared policy: a CUE document versioned in a repository, per the pattern
  [[ida-1ec03b1]]
  ([ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md))
  establishes — policy is data.
- **Act.** A controller subscribing to events and acting through gated effects — the reactive
  controller pattern of [architecture.md](../../design/architecture.md#the-concerns-unbundled), with
  effects crossing the boundary in [[ida-f1b39e8]]
  ([ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](./ida-f1b39e8-outbound-effects-pass-through-an-actuator.md))'s
  direction.

A new loop adds exactly two artifacts: a policy document and a controller. Nothing else is ever new.
A proposed loop that needs a sixth piece is evidence against the proposal.

## Self-improvement is ordinary change authorship

A loop improves itself by authoring a change to its own policy document, and that change lands
through the same integration path as any change: review, verification, and the gates the policy's
path class requires ([[ida-1ec03b1]]). Self-improvement therefore needs no mechanism of its own and
escapes no oversight — it is ordinary change authorship. This makes
[self-transparency.md](../../design/self-transparency.md)'s candidate invariant operational: the
system's own configuration is an outcome, produced and integrated exactly like code. Where a policy
class requires human presence, the gate of [[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
applies to the loop's own improvements automatically. Uniformity is what makes self-improvement
safe, not an aesthetic preference.

## The instances

Each of these is an instance of the shape:

- The neural-node performance loop and its task-sizing function — [[ida-a006b02]]
  ([ida-a006b02-ooda-over-neural-nodes.md](./ida-a006b02-ooda-over-neural-nodes.md)).
- The observability loop — [[ida-f16bd96]]
  ([ida-f16bd96-the-system-observes-itself.md](./ida-f16bd96-the-system-observes-itself.md)).
- Retention policy tuned by the observed value of what is retained — part of the signal-contracts
  decision [[dcr-62ecc36]].
- The trust loop already designed in [verification.md](../../design/verification.md), where confirm
  rates drive revocation.

## The today-form

Every loop named above already runs, manually: incidents become bug nodes, corrections become
restatements, and dispatch sizing is a practiced habit about to become a declared default. The shape
is already the practice; mechanization replaces the executor, not the shape. This is [[ida-b48bded]]
([ida-b48bded-production-dags-and-events.md](./ida-b48bded-production-dags-and-events.md))'s arc
applied to the loops themselves: the neural closure of a loop self-obsolesces into a deterministic
controller.

## Open

- Which loop mechanizes first.
- Whether a controller that authors policy changes needs its own authority class — what a delegation
  bundle for "may propose changes to policy X" looks like ([[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))).
- How loop health is itself observed without infinite regress — the loop that watches the loops.
