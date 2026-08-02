---
type: idea
id: ida-a8243d2
status: exploring
title: Agent runs act under delegated authority
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Agent runs act under delegated authority

Contributors arrive in identity classes that differ in whether their signing can be automated. A
human decision requires physical presence, and must not be automatable — that is the whole point of
it. An agent run is the opposite. It is automation, and no human is present at the moment it acts.

An agent run holds no key of its own. What it holds instead is a bundle of permissions delegated
from the human who initiated the run, either directly or transitively through another agent that
dispatched it in turn. Authority is not a single fact that is either present or absent. It is a set
that can be narrowed.

Attenuation is monotonic. Each hop in a delegation chain may narrow the bundle and may never widen
it. A chain is trustworthy only if that property holds at every link, and it is precisely that
property which allows a chain to be checked without consulting the originating human at each step.

Some rights must be non-delegable, and this is the constraint that keeps the approval gate alive.
The right to approve terminates at the human. If it can be placed in any bundle at all, however
attenuated, delegation quietly reintroduces the very failure that physical presence exists to
prevent: a coordinating agent holding a delegated right to approve is once again a gate administered
by the thing it gates. See [[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md)).

Capability-based operating systems are the prior art, and they make these distinctions structurally
rather than by policy. In Fuchsia a handle is an unforgeable reference carrying a set of rights.
Duplicating or transferring a handle can only yield a narrower set. The rights to duplicate and to
transfer are themselves rights that may be withheld, so non-delegability is expressible in the model
instead of bolted on afterwards. A process receives only the handles placed in its startup table —
there is no ambient namespace to reach into — and component manifests declare which capabilities are
offered along which routes, so the reachability graph can be audited before anything runs.

This shape is chosen over the alternatives so that identity stays thin until a scenario forces it to
grow. Per-run keys give the finest provenance, but they accrue no reputation and carry real
key-management weight. A long-lived per-agent key does accrue trust, but it becomes a durable
forgery risk if it leaks. At present scale neither buys anything the delegated chain does not
already provide.

## Open

- What a dispatch authorization names, and how narrowly it scopes a run.
- How a chain is cited so that scope, and not merely origin, is answerable. A citation naming only
  the originating human answers who authorized the work, but not what the work was permitted to do.
- How the chain is verified when the harness that ran the agent is also the software recording the
  citation.
- That a capability model needs a mediator which cannot be bypassed. An operating-system kernel
  mediates every operation. The integrator plays that role for the changes it gates, but an agent
  run does a great deal that no mediator observes — so the model maps well onto integration-time
  authority and far less well onto the rest of what a run does.

## Related

- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — the human act that authority derives from, and the right that never enters a bundle.
- [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](./ida-51605e8-authenticity-not-git-coupled.md)) —
  what such a signature is taken over.
- [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
  — the carrier a delegation chain would be cited in.
- [contribute.md](../../design/contribute.md) — defines a contributor as human or agent.
- [[dcr-0de694f]]
  ([dcr-0de694f-phase2-attestation-shape.md](../decisions/dcr-0de694f-phase2-attestation-shape.md))
  — the attestation shape a run is attributed inside, holding no key of its own.
