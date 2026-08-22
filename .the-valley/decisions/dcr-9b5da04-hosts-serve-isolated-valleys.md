---
type: decision
id: dcr-9b5da04
status: decided
title: Hosts serve valleys; valleys on a shared host are strongly isolated
created: 2026-08-22
source: design conversation, 2026-08-22
supersedes: [dcr-2f03be3]
---

# Hosts serve valleys; valleys on a shared host are strongly isolated

**Decided 2026-08-22.** Supersedes [[dcr-2f03be3]]
([dcr-2f03be3-hosts-serve-isolated-groups.md](./dcr-2f03be3-hosts-serve-isolated-groups.md)).

A host is shared infrastructure. Host and valley stand in an N : M relation, and the two directions
are not equally near: a host serving several valleys is present intent and is what the machinery is
built for, while a valley spanning several hosts is unprecluded and deferred. Replication stays
backup-depth until a rung of the ladder asks for more ([[dcr-d7952bc]]
([dcr-d7952bc-phase0-replication-github-transitional.md](./dcr-d7952bc-phase0-replication-github-transitional.md))).
Every project has exactly one owning valley, which is the valley whose floor and registry govern it.

## Strong isolation between valleys on a host

Two valleys on one host are separated as strongly as the mechanisms allow. As long as git access
over ssh rides the unix user model, the users are per valley: each valley has its own push user, and
that user's `authorized_keys` is compiled from that valley's registry alone. No compiled artifact is
ever a union across valleys, so a valley's blast radius is its own user. The ssh user is the valley
selector — it is what a clone URL names to reach one valley's repositories rather than another's.
Repositories and service state split per valley on the same principle, and the service split of
[[bd-500adf7]] ([bd-500adf7-bus-shares-git-user.md](../bugs/bd-500adf7-bus-shares-git-user.md))
extends valley-wise: what earns a user of its own earns one per valley.

Cross-valley event visibility is not a default. How the bus provides valley scoping — one bus per
valley, or one bus with authenticated valley-scoped subjects — is open
([openquestions.md](../../design/openquestions.md)).

## An identity may belong to several valleys

The same person, machine, or service may act in more than one valley. Each valley's registry cites
the identity's keys independently and decides for itself what they may do there. Nothing is minted
once and shared: this is the cited-not-minted shape of [[dcr-b87f6e8]]
([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md)),
applied between valleys on one host rather than across an organizational boundary.

## One integrator principal per valley

Each valley's registry declares one integrator principal, and every controller the valley runs signs
as it. Controllers are processes, not identities — a valley with many protected projects runs many
controllers under one principal. A principal already holds several keys, so a valley whose
controllers run on more than one host needs nothing new.

## Registry compilation refuses to orphan governance

A registry state that leaves no non-expired key holding governance of the registry's own stream is
invalid, and compilation refuses to render it. An invalid render never touches the last-good
artifacts, so a valley cannot lock itself out of its own governance by landing a change whose only
effect is arrived at later, when the last governing key expires.

## The host declaration, and what isolation is not

The host declaration is content in the repository of the valley that owns the host: a host is
declared by a valley, like any other thing a valley is responsible for.

Isolation is between peer valleys, not from the valley that owns the host. Root on the host sees
everything on it, and no arrangement of unix users changes that. A valley that requires independence
from the owner of the machine it runs on wants federation ([[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md))) — its own host,
peered — and that is the eventual answer, not a stronger separation within one machine.

## Open

- The bus's valley scoping, gated on bus authentication ([[bd-d853d9c]]
  ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md))).
- Per-valley user naming and filesystem layout, and which services beyond the push boundary split
  per valley. The isolation principle answers the last of those in the affirmative; the mechanics
  are designed when the second valley on a host arrives.

## Related

- [[dcr-8f069dd]] ([dcr-8f069dd-valley-is-the-unit.md](./dcr-8f069dd-valley-is-the-unit.md)) — the
  unit this decision is about.
- [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry each valley's push boundary compiles from.
- [[bd-500adf7]] ([bd-500adf7-bus-shares-git-user.md](../bugs/bd-500adf7-bus-shares-git-user.md)) —
  the service split this extends valley-wise.
- [[ida-8482624]] ([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) —
  the layer above, and where a valley that wants independence from a host owner goes.
