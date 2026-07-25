---
type: idea
id: ida-53ec742
status: exploring
title: Authority is enforced at the effect boundary
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Authority is enforced at the effect boundary

A run's dangerous capability is not what it can read or write on the machine it runs on. It is which
durable systems of record beyond that machine it can mutate, and how far such a change propagates
before anyone examines it.

The axis is reversibility and propagation rather than sensitivity. Discarding a workspace undoes
local work completely. An integration that lands can trigger a reconciliation that reconfigures a
machine — several systems deep before anyone looks — and the clock on that propagation starts
immediately.

Fine-grained constraint on what a run may touch locally carries a real productivity cost and buys
little where isolation already holds. Effort spent narrowing a workspace is effort not spent on the
boundary that matters. Permissive within, strict at the edge.

The failure this explains is the approval recorded by the agent that proposed the change. That was
never a containment breach. It was an outbound action against an external system of record, made
with legitimate credentials, doing exactly what the tool was built to do. No isolation boundary
would have prevented it, because nothing was escaping — the effect _was_ the intended egress.

A boundary contains processes. It does not contain credentials handed across it. An isolated run
holding unrestricted credentials for an external system of record has containment without any
reduction in the authority that matters. The remedy is a credential that cannot perform the effect,
not a wall around the process.

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

## Related

- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — the control that survives a compromised run host.
- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  — the bundle whose contents this bounds.
