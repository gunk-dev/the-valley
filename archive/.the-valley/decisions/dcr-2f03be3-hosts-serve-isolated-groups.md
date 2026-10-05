---
type: decision
id: dcr-2f03be3
status: superseded
title: Hosts serve groups; groups on a shared host are strongly isolated
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Hosts serve groups; groups on a shared host are strongly isolated

**Decided 2026-08-22**; the current decision about hosts and the units they serve is [[dcr-9b5da04]]
([dcr-9b5da04-hosts-serve-isolated-valleys.md](./dcr-9b5da04-hosts-serve-isolated-valleys.md)).

A host is shared infrastructure. Host and group stand in an N : M relation, and the two directions
are not equally near: a host serving several groups is present intent and is what the machinery is
built for, while a group spanning several hosts is unprecluded and deferred. Replication stays
backup-depth until a rung of the ladder asks for more ([[dcr-d7952bc]]
([dcr-d7952bc-phase0-replication-github-transitional.md](./dcr-d7952bc-phase0-replication-github-transitional.md))).
Every project has exactly one owning group, which is the group whose floor and registry govern it.

## Strong isolation between groups on a host

Two groups on one host are separated as strongly as the mechanisms allow. As long as git access over
ssh rides the unix user model, the users are per group: each group has its own push user, and that
user's `authorized_keys` is compiled from that group's registry alone. No compiled artifact is ever
a union across groups, so a group's blast radius is its own user. The ssh user is the group selector
— it is what a clone URL names to reach one group's repositories rather than another's. Repositories
and service state split per group on the same principle, and the service split of [[bd-500adf7]]
([bd-500adf7-bus-shares-git-user.md](../bugs/bd-500adf7-bus-shares-git-user.md)) extends group-wise:
what earns a user of its own earns one per group.

Cross-group event visibility is not a default. How the bus provides group scoping — one bus per
group, or one bus with authenticated group-scoped subjects — is open
([openquestions.md](../../design/openquestions.md)).

## An identity may belong to several groups

The same person, machine, or service may act in more than one group. Each group's registry cites the
identity's keys independently and decides for itself what they may do there. Nothing is minted once
and shared: this is the cited-not-minted shape of [[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md)),
applied between groups on one host rather than across an organizational boundary.

## One integrator principal per group

Each group's registry declares one integrator principal, and every controller the group runs signs
as it. Controllers are processes, not identities — a group with many protected projects runs many
controllers under one principal. A principal already holds several keys, so a group whose
controllers run on more than one host needs nothing new.

## Registry compilation refuses to orphan governance

A registry state that leaves no non-expired key holding governance of the registry's own stream is
invalid, and compilation refuses to render it. An invalid render never touches the last-good
artifacts, so a group cannot lock itself out of its own governance by landing a change whose only
effect is arrived at later, when the last governing key expires.

## The host declaration, and what isolation is not

The host declaration is content in the repository of the group that owns the host: a host is
declared by a group, like any other thing a group is responsible for.

Isolation is between peer groups, not from the group that owns the host. Root on the host sees
everything on it, and no arrangement of unix users changes that. A group that requires independence
from the owner of the machine it runs on wants federation ([[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md))) — its own host,
peered — and that is the eventual answer, not a stronger separation within one machine.

## Open

- The bus's group scoping, gated on bus authentication ([[bd-d853d9c]]
  ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md))).
- Per-group user naming and filesystem layout, and which services beyond the push boundary split per
  group. The isolation principle answers the last of those in the affirmative; the mechanics are
  designed when the second group on a host arrives.

## Related

- [[dcr-0e9278a]] ([dcr-0e9278a-group-is-the-unit.md](./dcr-0e9278a-group-is-the-unit.md)) — the
  unit this decision is about.
- [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry each group's push boundary compiles from.
- [[bd-500adf7]] ([bd-500adf7-bus-shares-git-user.md](../bugs/bd-500adf7-bus-shares-git-user.md)) —
  the service split this extends group-wise.
- [[ida-8482624]] ([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) —
  the layer above, and where a group that wants independence from a host owner goes.
