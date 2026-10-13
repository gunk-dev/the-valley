---
type: decision
id: dcr-9c75283
status: decided
title: "Valleys adopt policy explicitly and share a minimal approval requirement"
created: 2026-08-23
source: design conversation, 2026-08-22/23
---

# Valleys adopt policy explicitly and share a minimal approval requirement

A valley adopts policy through an integrated change to its own repository. The-valley supplies
schemas, templates, and defaults for that purpose. Updating those supplied files does not change a
valley's adopted policy.

One small set of rules, called the **core**, applies through deployment. It requires human approval
by default and prevents policy from removing the approval requirement for changes to policy itself.

## Policy ownership

The repositories contain distinct policy layers:

| Repository | Layer                               | Applies to                   |
| ---------- | ----------------------------------- | ---------------------------- |
| The-valley | Supplied definitions and templates  | Valleys that adopt them      |
| The-valley | Its project policy                  | The-valley as a project      |
| A valley   | Its shared policy, called the floor | All projects it owns         |
| A valley   | Its project policy                  | The valley repository itself |

A valley embeds the policy it adopts at its own integrated revision. A later substrate update
changes what can be adopted in the future. It does not silently change that embedded policy.

The-valley is also an ordinary project of its owning valley. That valley's floor therefore governs
the repository that supplies the shared software.

## Default approval

The core requires an approval signed over a change's identity by hardware that a person physically
operates. Without additional policy, every change needs this approval before integration.

A valley with no floor can operate under the core alone. An empty floor does not remove the core's
requirements. This also protects the bootstrap process: the valley hosting the-valley cannot remove
the approval requirement on its own governance by changing its floor.

## Policy can exempt ordinary changes

Policy may allow a class of changes to integrate without another physical approval. One intended
example is implementation work derived from an already approved requirements document.

Changes to policy at every layer still require physical approval. An owner may delegate
policy-detail work to an agent, but the delegation is itself a signed act. The rules permitting
delegation cannot authorize their own relaxation.
[[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md) describes the human
decision involved.

The project expects suggested policies to develop through use. Human-approved intent followed by
automatically checked implementation is a direction to explore, not a complete default policy
already specified here.

## Activation and compatibility

The approval mechanism must be available before the core is activated. Deployment must never enable
a requirement that the valley has no means to satisfy.

Core updates take effect through deployment. This is a deliberate exception to explicit policy
adoption so that a small set of security requirements can reach all valleys. Keeping the core small
limits the consequences of that exception.

During the proof of concept, changes use compatibility windows and coordinated migrations. A mature
implementation is expected to use semantic versioning for configuration and code. Breaking schema or
core changes must be visible in the version, with conformance vectors testing the contract as
described in [[ida-4557af7]](../ideas/ida-4557af7-spec-driven-iteration.md).

[[dcr-f41f718]](dcr-f41f718-declared-verification-policy.md) defines policy composition.
[[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md) applies the distinction between
shared constraints and locally declared identities.
