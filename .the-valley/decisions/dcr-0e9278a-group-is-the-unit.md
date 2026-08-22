---
type: decision
id: dcr-0e9278a
status: decided
title: The group is the unit
created: 2026-08-22
source: design conversation, 2026-08-22
---

# The group is the unit

The group is the named unit of the system. A group owns a repository, mandates a floor, declares an
identity registry, and has an integrator principal. Everything that is not a project hangs off it:
the repository is the group's sovereign home, the floor is what the group mandates over the projects
it governs, the registry says who may act in the group, and the integrator principal is who the
group's controllers sign as. Writing about the substrate says group.

## The word "instance" is retired

A group has exactly one instance ([[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md))), so the second name
draws no distinction the first does not already draw. It is overloaded besides — a running
deployment, a module instantiation, one copy of the substrate — and each reading points at
infrastructure rather than at the thing that has an owner, a boundary, and a registry. Group is the
word that carries; instance is not part of the vocabulary.

## Renaming what is already minted is deliberate work

Names already in the tree — module option names, node prose, design-document phrasings — say
instance in many places. Correcting them is an audit of the corpus and a rename of a public module
surface, which is a change with its own scope, its own review, and its own compatibility question
for the deployments that set those options. That work is taken on deliberately and lands as itself.
It is never done implicitly, as a side effect of a change about something else.

## Related

- [[ida-8482624]] ([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) —
  the group-instance binding this rests on, and the federation layer above the group.
- [[dcr-2f03be3]]
  ([dcr-2f03be3-hosts-serve-isolated-groups.md](./dcr-2f03be3-hosts-serve-isolated-groups.md)) —
  what a group is to a host, and what separates two groups sharing one.
- [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry a group declares.
- [[dcr-5da1f36]] ([dcr-5da1f36-project-is-repo.md](./dcr-5da1f36-project-is-repo.md)) — the unit
  below: a project is one git repository.
