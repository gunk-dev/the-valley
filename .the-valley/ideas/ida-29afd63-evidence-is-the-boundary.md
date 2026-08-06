---
type: idea
id: ida-29afd63
status: exploring
title: Submission is transport; evidence is the boundary
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Submission is transport; evidence is the boundary

The integrator accepts a change from any source — a push, a fetched branch, a public submission, a
federated peer — because what gates landing is never where the change came from. What gates landing
is whether its provenance and evidence satisfy policy. Access control over submission stops being the
security boundary. Verification of evidence is the security boundary instead.

Two boundaries survive this move and must still hold. First, trust gates transfer, not submission:
evidence from an attester without standing does not transfer, no matter how it arrived. A change
carrying such evidence takes an accept-and-re-verify path instead, and that path's executor capacity
and trust bootstrap are open questions (see design/openquestions.md's contributor-bootstrap entry).
Second, authority never widens: protected refs still land only under the integrator's key, and
approval classes still require human presence ([[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))),
whatever the source of the change.

One resource question stays honestly open: verifying an unsolicited change spends compute on a
stranger, and the policy limiting that spend is undesigned.

This relates to [[dcr-439b771]]
([dcr-439b771-integration-is-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-is-occ-over-content-addressed-evidence.md)),
the verdict function this idea makes transport-agnostic; to [[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md)),
the registry that standing attaches to; to [[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)),
whose federated-peer case this idea covers the local half of; and to [[bd-8a591dc]]
([bd-8a591dc-machine-credentials-never-expire.md](../bugs/bd-8a591dc-machine-credentials-never-expire.md)),
whose pull-based direction says a contributor needs reachability, not authorization.
