---
type: idea
id: ida-b7025b5
status: exploring
title: Human decisions are signed acts
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Human decisions are signed acts

A human decision or approval is a cryptographic signature, ideally one that requires physical
presence — a hardware key (YubiKey / FIDO) that must be touched.

The motivating failure is an approval gate administered by the agent it gates. When a coordinating
agent both proposes a change and records its approval, the gate is just a variable that agent sets.
A change can be approved and merged that was never actually reviewed, with no intent anywhere to
defeat the gate.

Recording who approved does not prevent this. The requirement is that the system be _unable_ to
proceed, not merely able to state afterwards who said yes. Audit is not enforcement.

Physical presence makes compliance structural rather than a matter of good behaviour. An agent
cannot touch a hardware key, so it cannot manufacture the approval, accidentally or otherwise.

## Left open

**Which changes require it.** Certain classes of change gate on human approval. The direction is
that the class could ultimately be every change.

**Where the check runs.** A verifier the acting agent can modify is not a constraint on that agent.
The refusal must happen somewhere the agent's own change is subject to the gate rather than
performing it.

## Related

- Where the refusal would structurally live — the protected-ref invariant and the policy mapping
  path classes to required checks:
  [roadmap.md, Phase 3](../../design/roadmap.md#phase-3--the-integrator)
- The attestation the self-transparency invariant already requires:
  [self-transparency.md](../../design/self-transparency.md)
- The human counterpart to the open agent-signing question:
  [openquestions.md § Identity & trust bootstrapping](../../design/openquestions.md#identity--trust-bootstrapping)
- The participant whose outputs carry this provenance: [[ida-3145b7a]]
  ([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md))
