---
type: idea
id: ida-29afd63
status: exploring
title: "Judge contributions by their evidence across different submission methods"
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Judge contributions by their evidence across different submission methods

The integrator should be able to evaluate a change submitted by push, fetched branch, public API, or
federated peer. The admission decision should depend on its provenance, evidence, and governing
policy.

Transport access and permission to land are separate questions. An accepted submission still has to
satisfy the integration rules.

Evidence from a trusted attester may be reusable under
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md).
Evidence from an attester without the required standing would need independent verification before
it could count. The executor capacity and trust bootstrap for that path remain open.

Only the integrator writes protected refs. Required human approvals still apply regardless of where
a contribution came from; see [[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md).

Processing unsolicited contributions consumes resources. The policy limiting that expenditure has
not been designed.

[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) defines the registry
against which standing is checked. [[ida-8482624]](ida-8482624-federation-groups.md) covers
federated submissions, and [[ida-0f580e2]](ida-0f580e2-contributors-without-write-access.md)
explores contributions without repository write access.
[openquestions.md](../../design/openquestions.md) records contributor bootstrap.
