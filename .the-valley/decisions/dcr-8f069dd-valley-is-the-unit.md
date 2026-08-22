---
type: decision
id: dcr-8f069dd
status: decided
title: The valley is the unit
created: 2026-08-22
source: design conversation, 2026-08-22
supersedes: [dcr-0e9278a]
---

# The valley is the unit

**Decided 2026-08-22.** Supersedes [[dcr-0e9278a]]
([dcr-0e9278a-group-is-the-unit.md](./dcr-0e9278a-group-is-the-unit.md)).

A valley is the named unit of the system. A valley owns a repository, mandates a floor, declares an
identity registry, and has an integrator principal. Everything that is not a project hangs off it:
the repository is the valley's sovereign home, the floor is what the valley mandates over the
projects it governs, the registry says who may act in the valley, and the integrator principal is
who the valley's controllers sign as.

## The substrate defines what a valley is

The substrate is the thing this repository builds — its proper name is the-valley, and its common
noun is the substrate. The substrate defines what a valley is: the schema, the module, the floor,
and the machinery a valley runs. A valley is one of them — a set of people, agents, and projects
under shared law, with its own floor, its own registry, and its own integrator.

The general and the particular are told apart by the article, as English normally does it: the
substrate, against a valley. No second word is minted for the particular case, because the article
already carries the distinction wherever the two are named together.

## Two words are retired

"Group" and "instance" are both out of the vocabulary. Each named the unit at some point, and each
collided with a subsystem of the layer the substrate is built on: "instance" collided with systemd
template instances, and "group" collided with unix groups — which is the exact mechanism per-valley
isolation uses, so the concept term and the enforcement primitive would have been one word.

That collision is the rule, stated generally: a concept term must mean nothing in the systems layer
below. A word that already names a kernel object, a service-manager construct, or a filesystem
permission cannot also name a unit of governance, because every sentence about the unit then reads
twice.

## Bare `valley` names a valley; the substrate is always named in full

The namespacing principle follows from the article rule. A bare `valley` in a name refers to a
valley — one of them — and never to the substrate. Where a name genuinely means the substrate, it
says the-valley, or says "the substrate" in prose.

The names already minted re-ground correctly under this reading rather than needing correction.
`valley-integrator@…` is the integrator for that valley. `VALLEY_PRINCIPAL` is a principal in a
valley's registry. `valley review` runs against the valley whose repository is in hand. Each was
already named a particular valley; the principle says so out loud.

Two spellings keep the full name as proper-noun branding, on the pattern `.git` set: the
`.the-valley/` directory that holds a repository's knowledge graph, and the `refs/the-valley/`
namespace the machinery writes under.

## Renaming what is already minted is deliberate work

Names in the tree that do need correcting — module option names, `schema/valley.cue`, the code
artifacts under `identity/`, `nix/`, `integrator/` and `attest/` — are corrected by a change that
lands as itself. That work is an audit of a public module surface and a rename with its own
compatibility question for the deployments that set those options. It is never done implicitly, as a
side effect of a change about something else.

## Related

- [[dcr-9b5da04]]
  ([dcr-9b5da04-hosts-serve-isolated-valleys.md](./dcr-9b5da04-hosts-serve-isolated-valleys.md)) —
  what a valley is to a host, and what separates two valleys sharing one.
- [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](./dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry a valley declares.
- [[dcr-5da1f36]] ([dcr-5da1f36-project-is-repo.md](./dcr-5da1f36-project-is-repo.md)) — the unit
  below: a project is one git repository.
- [[ida-8482624]] ([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) —
  the federation layer above, which governs what crosses between valleys.
