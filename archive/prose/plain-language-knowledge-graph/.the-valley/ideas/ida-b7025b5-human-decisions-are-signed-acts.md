---
type: idea
id: ida-b7025b5
status: exploring
title: "Require verifiable human approval for protected decisions"
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Require verifiable human approval for protected decisions

A required human approval must be a signed act that an automated run cannot produce on its own. The
design uses a hardware-backed signature with evidence of physical presence, bound to the decision
being approved.

If an agent can both propose a change and set the approval flag that admits it, the flag does not
establish human review. Recording the agent's identity afterward does not repair that missing
control.

The refusal must occur in machinery the submitted change cannot alter before it is judged. The
protected-ref and policy model in [Phase 3](../../design/roadmap.md#phase-3--the-integrator)
provides that location for integration.

[[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md) defines which decisions
require this approval: it is the default for changes, policy may exempt ordinary classes, and policy
or delegation changes retain the requirement.

## Open implementation questions

How is the physical-presence assertion represented and verified? How is it bound to the exact
decision presented to the human? Which aspects of that approval remain valid when the change is
rebased?

[[ida-7685e66]](ida-7685e66-birth-and-approval-are-policy.md) examines the binding question.
[openquestions.md](../../design/openquestions.md#identity-approval-and-bootstrap) records the
identity work.

The signed act also supplies provenance for the human work represented in
[[ida-3145b7a]](ida-3145b7a-demand-pressure.md) and the system history described in
[self-transparency.md](../../design/self-transparency.md).
