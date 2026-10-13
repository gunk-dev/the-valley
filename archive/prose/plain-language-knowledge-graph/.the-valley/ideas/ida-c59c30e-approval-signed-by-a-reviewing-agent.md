---
type: idea
id: ida-c59c30e
status: exploring
title: "Use independent agent review as signed evidence"
created: 2026-08-01
source: design conversation, 2026-08-01
---

# Use independent agent review as signed evidence

An independent reviewing agent could produce signed evidence about a proposed change. The value of
that review would depend on the run's provenance, including its model, tools, prompt, and relevant
expertise.

Review may involve repeated feedback and revision. Blocking findings and advisory feedback should be
available to the author during that process, with the final statement describing the result.

The reviewing run must be distinct from the authoring run. This avoids allowing the author to
satisfy its own review requirement, the failure discussed in
[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md).

## Relationship to human approval

The proposal leaves open whether an agent's result is called an approval for eligible change classes
or a separate review attestation.

Either approach must preserve the physical human approval required by
[[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md). Policy could require
agent review for a class exempted from additional human approval; the agent's result cannot
substitute for a human gesture where that gesture remains mandatory.

[[ida-a8243d2]](ida-a8243d2-agent-runs-act-under-delegated-authority.md) keeps required human
approval out of delegated agent permissions.
[[ida-45178f6]](ida-45178f6-agent-identity-is-provenance.md) describes the run provenance on which
confidence in the review would depend.

Agent runs currently have no independent signing key. A signed review would therefore also need to
fit the host-signing model in [[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md).
The statement format and the authority that attests to the review remain to be designed.
