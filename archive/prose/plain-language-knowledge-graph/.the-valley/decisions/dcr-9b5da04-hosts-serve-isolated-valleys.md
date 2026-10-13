---
type: decision
id: dcr-9b5da04
status: decided
title: "Isolate valleys that share a host"
created: 2026-08-22
source: design conversation, 2026-08-22
supersedes: [dcr-2f03be3]
---

# Isolate valleys that share a host

A host can serve several valleys. Each valley has separate repository access, service state, and
permissions. Every project belongs to exactly one valley, whose shared policy and identity registry
govern it.

Hosting several valleys on one machine is a current design goal. A valley spanning several hosts
remains possible but deferred. Replication provides backup depth until a
[user scenario](../../design/user-scenarios.md) requires more; see
[[dcr-d7952bc]](dcr-d7952bc-phase0-replication-github-transitional.md).

## Access and service isolation

While Git access uses SSH and Unix users, each valley has its own push user. That user's
`authorized_keys` is compiled only from the valley's registry. Permissions from different valleys
must not be combined into one compiled artifact.

The SSH user in a clone URL selects the valley. Repositories and service state are separated on the
same basis. A service that needs a separate user also needs a separate user for each valley;
[[bd-500adf7]](../bugs/bd-500adf7-bus-shares-git-user.md) describes the bus example.

Valleys do not receive one another's events by default. Bus isolation could use separate buses or
authenticated subjects scoped to each valley. This remains open and depends on the authentication
work in [[bd-d853d9c]](../bugs/bd-d853d9c-bus-unauthenticated.md).

## Identity and integrators

A person, machine, or service can belong to several valleys. Each registry independently names the
keys it recognizes and grants permissions within that valley.
[[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md) defines this registry model.

Each valley declares one integrator principal. Its controllers sign as that principal, even when
there is a controller for each of several protected projects. A principal can hold several keys,
allowing controllers on different machines to use different keys under the same identity.

## Preserve access to governance

The registry compiler must reject a state with no unexpired key authorized to govern the registry's
own stream. A rejected compilation must leave the last valid artifacts in place.

This check protects against rendering a registry with no governing key. Credential expiry and
continued access also require the lifecycle rules in
[[dcr-b87f6e8]](dcr-b87f6e8-identity-is-a-governed-registry.md); compilation does not make keys
immune to later expiry.

## Host ownership

The valley that owns the host stores its declaration in its repository.

The isolation described here separates valleys from their peers on the same machine. The host
administrator retains root access to the machine and its data. A valley that needs independence from
that administrator needs infrastructure under separate control.
[[ida-8482624]](../ideas/ida-8482624-federation-groups.md) explores cooperation between such
valleys.

## Open questions

Bus scoping, per-valley user names, filesystem layout, and the full set of services requiring
separate users need concrete designs when a second valley is deployed on one host.

[[dcr-8f069dd]](dcr-8f069dd-valley-is-the-unit.md) defines the valley as the unit of shared policy.
