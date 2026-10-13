---
type: decision
id: dcr-8f069dd
status: decided
title: A valley groups projects under shared policy
created: 2026-08-22
source: design conversation, 2026-08-22
supersedes: [dcr-0e9278a]
---

# A valley groups projects under shared policy

A valley is a named set of people, agents, and projects governed by shared policy. It owns the
repository that records its governing state. Its identity registry declares who may act, and its
integrator principal is the identity under which its integration controllers sign their decisions.

The common requirements a valley sets for its projects are called its **floor**. These requirements,
the identity registry, and the integrator belong to the valley and govern the projects it owns.

## The substrate defines what a valley is

**The substrate** is the software this repository builds, named **the-valley**. It supplies the
schemas, hosting module, policy definitions, and tools used to operate valleys. Each valley uses
that software with its own governing state. The rules for adopting supplied policy are defined in
[[dcr-9c75283]](dcr-9c75283-substrate-adoption-and-the-core.md).

## Keep domain names distinct from operating-system terms

Use **valley** for the collection governed by shared policy. Reserve **group** for Unix groups and
**instance** for systemd template instances. Unix groups are also the mechanism used to isolate
valleys, so using the same term for both would make configuration and access rules ambiguous.

The naming rule applies throughout the design: a domain concept must have a name distinct from the
kernel objects, service-manager constructs, and filesystem permissions used to implement it.

## Names for a particular valley and for the shared software

A bare `valley` in a name refers to a particular valley. Use `the-valley`, or **the substrate** in
prose, when referring to the software shared by all valleys.

For example, `valley-integrator@…` names an integrator serving a valley. `VALLEY_PRINCIPAL` names a
principal in that valley's registry. `valley review` operates in the context of the valley that owns
the project being reviewed.

The `.the-valley/` knowledge-graph directory and the `refs/the-valley/` ref namespace use the
software's full name to identify its storage conventions.

## Existing code names require an explicit migration

Applying this naming rule to existing code requires a separate, explicit change. The audit covers
module options, `schema/valley.cue`, and artifacts under `identity/`, `nix/`, `integrator/`, and
`attest/`. A rename must account for deployments that use the current names, including any required
compatibility or migration steps.

## Related

- [[dcr-9b5da04]](dcr-9b5da04-hosts-serve-isolated-valleys.md) — what a valley is to a host, and
  what separates two valleys sharing one.
- [[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md) — the registry a valley declares.
- [[dcr-5da1f36]](dcr-5da1f36-project-is-repo.md) — the unit below: a project is one git repository.
- [[ida-8482624]](../ideas/ida-8482624-federation-groups.md) — the federation layer above, which
  governs what crosses between valleys.
