---
type: decision
id: dcr-e544f20
status: decided
title: "Declare access as permitted actions on projects"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Declare access as permitted actions on projects

A valley declares which principals may perform which actions on its projects. The system compiles
those grants into SSH access, hook rules, bus permissions, and policy checks.

The shared schema defines a closed set of action names, called **verbs**. A verb becomes available
only when its enforcement mechanism exists. Compilation must fail if a deployment cannot enforce a
declared grant.

## Verbs

| Verb      | Meaning                                               | Scope and enforcement                                                          |
| --------- | ----------------------------------------------------- | ------------------------------------------------------------------------------ |
| `govern`  | Supply approval for changes to governing path classes | Valley-scoped; checked through approval policy                                 |
| `push`    | Write topic branches and attestation refs             | Project-scoped; checked at repository access and receive hooks; includes fetch |
| `fetch`   | Read repositories                                     | Valley-scoped until the forced-command dispatcher provides project scoping     |
| `observe` | Subscribe to project events                           | Project-scoped from introduction; requires authenticated bus subjects          |
| `request` | Write integration requests for a project's stream     | Project-scoped; checked on writes to the request namespace                     |

Governance can change other grants. It is rooted in the genesis principal, cannot be granted to an
external principal, and must remain available to at least one unexpired governing key.

Revoking fetch access prevents future reads through that boundary. It cannot remove copies already
obtained.

The combination of request permission and required approvals determines the level of supervision. An
automated caller may be allowed to submit requests while policy still requires human approval before
particular changes land.

## Integrator authority

Only the integrator writes protected refs. This is a property of the integration machinery, rather
than a grant available to ordinary principals.

The integrator's registry entry makes its signatures verifiable. Its ability to land changes comes
from its configured role in that machinery.

Governance changes pass through the governed policy path. The host owner's root access remains
outside this model.

## Enforcement

A forced-command dispatcher on the SSH path provides project-level repository access. The registry
compiler generates its grant table and authorized keys. This is the enforcement needed to provide
finer access than the shared-user arrangement described in
[[dcr-0f5d9b1]](dcr-0f5d9b1-cue-config-host-module.md).

Request grants compile into receive-hook configuration. Observe grants compile into the bus
permissions required by [[bd-d853d9c]](../bugs/bd-d853d9c-bus-unauthenticated.md), using the subject
namespace in [[dcr-62ecc36]](dcr-62ecc36-signal-contracts.md). Govern grants determine whose
approvals satisfy policy.

[[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md) defines the registry and credential
model. [[dcr-f41f718]](dcr-f41f718-declared-verification-policy.md) defines governing path classes
and verification policy. [[dcr-9b5da04]](dcr-9b5da04-hosts-serve-isolated-valleys.md) covers
separation between valleys sharing a host.
