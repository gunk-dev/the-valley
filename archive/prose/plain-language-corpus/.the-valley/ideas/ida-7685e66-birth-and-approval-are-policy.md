---
type: idea
id: ida-7685e66
status: exploring
title: "Authorize project creation through a signed human decision"
created: 2026-08-06
source: design conversation, 2026-08-06
---

# Authorize project creation through a signed human decision

Adding a project to a valley is a change to the valley's declarations and policy. That change should
require a human approval signed with evidence of physical presence.

The new project's authority then traces through an integrated decision in the valley repository. The
valley itself begins with the genesis principal defined in
[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md). Agent delegation
chains ultimately depend on that human authorization, as described in
[[ida-a8243d2]](ida-a8243d2-agent-runs-act-under-delegated-authority.md).

[[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md) defines the current
approval model: physical approval is required by default, policy may exempt ordinary classes of
change, and changes to policy or delegation still require approval.
[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) supplies the path classes
used to express those requirements.

The integrator needs a verifiable approval statement in addition to execution evidence.
[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) describes the signed human act, and
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md)
describes evidence evaluation.

## Approval after a rebase

What exactly an approval identifies remains unresolved.

An approval of an exact tree applies only to that tree. A queue advance that changes the resulting
tree may require the person to approve again.

An approval of a stable change identity could survive reapplication to a new base. The surrounding
code may then differ from what the person reviewed.

Policy could state which binding a class of change requires. That proposal depends on defining
stable change identity and its representation in the attestation subject.
[[ida-cbcbb3c]](ida-cbcbb3c-attestations-bind-to-a-base.md) and
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md) record
the related open questions.
