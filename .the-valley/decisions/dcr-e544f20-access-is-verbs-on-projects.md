---
type: decision
id: dcr-e544f20
status: decided
title: "Access is verbs on projects; infrastructure is a compilation target"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Access is verbs on projects; infrastructure is a compilation target

The valley's config surface speaks the valley's domain: principals, projects, verbs. Infrastructure
— unix users, authorized_keys, hook configuration, bus accounts — is what grants compile into, never
vocabulary the instance writes.

The boundary rule of [[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
survives, relocated. Instead of each grant naming its enforcement boundary, the substrate defines a
closed verb list, and a verb exists only when its gate ships. Same teeth — a capability nothing
enforces cannot be declared — with no infrastructure in the declaration. A verb a deployment cannot
enforce fails at compile, loudly.

The verbs:

- **govern** — land changes to the valley's governing path classes; whose approval satisfies the
  human-approval check those classes require. Valley-scoped. The master verb: it dominates every
  other grant, no external principal may hold it, genesis anchors it, and a registry render that
  would orphan it is refused.
- **push** — write topic branches and the attestation namespace in a project's repositories.
  Project-scoped: the pre-receive hook enforces per repository, per principal. push subsumes fetch.
- **fetch** — read a valley's repositories. Valley-scoped until the forced-command gate ships
  (below), then project-scoped. Prospective only.
- **observe** — subscribe to the valley's event stream. Project-scoped from its first day: its gate
  is bus authentication ([[bd-d853d9c]]
  ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md))), whose subject
  permissions match the per-project namespace of [[dcr-62ecc36]]
  ([dcr-62ecc36-signal-contracts.md](./dcr-62ecc36-signal-contracts.md)). The verb ships with that
  gate, not before.
- **request** — write the request namespace targeting a project's stream. Project-scoped; its gate
  is a hook extension checking namespace writes against holders. Who holds request, together with
  which path classes demand approval statements, is the supervision dial: agent-class principals
  gaining request under an approval-requiring floor is how unsupervised landing arrives by
  declaration rather than by accident.

Deliberate absences: there is no **land** — writing a protected ref is the integrator's structural
privilege, below the grant system entirely — and no **admin** — escalation is govern, through the
strictest class, or the host owner's root, outside the system.

Gates on the roadmap: project-scoped fetch and push ride a forced-command dispatcher on the ssh path
— the pattern git hosting standardized on — compiled from the registry like every artifact; when it
ships, the shared-user constraint of [[dcr-0f5d9b1]]
([dcr-0f5d9b1-cue-config-host-module.md](./dcr-0f5d9b1-cue-config-host-module.md)) is superseded.
observe rides bus authentication.

Compilation: fetch and push render to authorized_keys and the dispatcher's grant table; request to
hook configuration; observe to bus permissions; govern to policy composition. The integrator holds
no verbs: its power is machinery, and its registry entry exists so its signatures verify.

## Related

- [[dcr-2965320]]
  ([dcr-2965320-valley-is-its-repository.md](./dcr-2965320-valley-is-its-repository.md)) — the axiom
  this verb model builds on.
- [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the grant declaration form this node refines.
- [[dcr-f41f718]]
  ([dcr-f41f718-declared-verification-policy.md](./dcr-f41f718-declared-verification-policy.md)) —
  the path classes govern requires approval statements on.
- [[dcr-62ecc36]] ([dcr-62ecc36-signal-contracts.md](./dcr-62ecc36-signal-contracts.md)) — the
  per-project namespace observe's bus gate matches.
- [[dcr-0f5d9b1]] ([dcr-0f5d9b1-cue-config-host-module.md](./dcr-0f5d9b1-cue-config-host-module.md))
  — the shared-user constraint the forced-command dispatcher supersedes.
- [[bd-d853d9c]] ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md)) —
  the bus authentication gate observe ships with.
- [[dcr-9b5da04]]
  ([dcr-9b5da04-hosts-serve-isolated-valleys.md](./dcr-9b5da04-hosts-serve-isolated-valleys.md)).
