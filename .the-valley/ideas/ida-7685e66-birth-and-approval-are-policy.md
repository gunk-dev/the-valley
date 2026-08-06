---
type: idea
id: ida-7685e66
status: exploring
title: A project is born by a signed human act, and human approval is path-scoped policy
created: 2026-08-06
source: design conversation, 2026-08-06
---

# A project is born by a signed human act, and human approval is path-scoped policy

A project's birth is a human authorization, signed by a hardware key that requires physical
presence, gated through the instance's own policy. A project entering the instance is a change to
the instance repository: an addition to its registry entry, its policy, its declaration. Those paths
are a path class, and [[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
already requires human approval as evidence for registry changes. Project birth is that same class,
extended to the moment a project first appears.

The pattern is recursive. The instance itself has one genesis entry, created at instance birth. Each
project's chain of authority roots in a human signed act that landed through the instance's gate, the
same way the instance's own chain roots in its genesis entry. Every delegation chain in every run's
provenance ([[ida-a8243d2]]
([ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)))
terminates there.

After birth, the human controls which further content requires human approval through the project's
own policy framework. Approval is a requirable evidence type in a path class's `requires`, exactly as
any other check is ([[dcr-f41f718]]
([dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md))).
This answers [[ida-b7025b5]]'s
([ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
open question of which changes require presence: the classes whose `requires` names it. Some of that
is floor-mandated — changes to policy itself always require presence — and the rest is
project-tunable, narrowing as trust in non-human authors grows. Enforcement needs nothing new: the
integrator's verdict ([[dcr-439b771]]
([dcr-439b771-integration-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md)))
verifies an approval statement the same way it verifies any other evidence.

## Open

What an approval binds to across a rebase is unresolved, and the two ways of answering it pull
apart. Bound to the tree, every queue advance invalidates the approval and re-demands presence —
verification latency with a human inside it. Bound to the change's identity, the approval survives a
clean rebase while the context beneath it may have shifted underneath the approval without the
approver seeing it move. The likely answer is policy-declared: a path class states which binding it
demands. That choice makes the open change-identity member of the attestation subject
([[dcr-439b771]]'s
([dcr-439b771-integration-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md))
open item, also carried by [[ida-cbcbb3c]]
([ida-cbcbb3c-attestations-bind-to-a-base.md](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md)))
load-bearing: an approval wants to name what change it approves, not only which tree. The two open
items are one.
