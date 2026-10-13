# The-valley knowledge graph

This directory holds the project's ideas, decisions, bugs, and outcomes as Markdown files. The graph
travels with the repository, uses its history, and changes through the same integration path as
code.

A node's status describes its role in the design. A decided requirement may still need
implementation. An exploring idea or open risk is material for review, rather than a commitment to a
finished design.

## Reading the graph

Start with the subject relevant to the work, then follow links to its supporting decisions and open
questions.

| Subject                         | Starting points                                                                                                                                                                                                                                     |
| ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Product interface and migration | [Project interface](./ideas/ida-23aa413-project-surfaces-are-derived-views.md), [cosmo migration](./outcomes/oc-87deec8-valley-can-host-cosmo.md)                                                                                                   |
| Projects, valleys, and hosts    | [Valley definition](./decisions/dcr-8f069dd-valley-is-the-unit.md), [governing state](./decisions/dcr-2965320-valley-is-its-repository.md), [host isolation](./decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md)                               |
| Policy and identity             | [Policy composition](./decisions/dcr-f41f718-declared-verification-policy.md), [approval requirements](./decisions/dcr-9c75283-substrate-adoption-and-the-core.md), [identity registry](./decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) |
| Contributions and verification  | [Change identity](./ideas/ida-93e4f91-changes-not-branches.md), [attestations](./decisions/dcr-0de694f-phase2-attestation-shape.md), [evidence transfer](./decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md)                |
| Outcomes and agent work         | [Scheduling](./ideas/ida-eac723e-outcome-dag.md), [stalled work](./ideas/ida-3145b7a-demand-pressure.md), [Klaus requirements](./ideas/ida-594df79-klaus-s3-requirements-oracle.md)                                                                 |
| Learning from work              | [Run provenance](./ideas/ida-45178f6-agent-identity-is-provenance.md), [feedback loops](./ideas/ida-5a59fef-one-plumbing-for-every-loop.md), [reconstructing design reasoning](./ideas/ida-f83d7ba-theory-revival-accelerator.md)                   |

The full collections are [ideas](./ideas/), [decisions](./decisions/), [bugs](./bugs/), and
[outcomes](./outcomes/).

## Files and metadata

Each node has its own file with YAML frontmatter and a Markdown body:

```yaml
---
type: idea
id: ida-eac723e
status: adopted
title: Schedule work from outcomes and their dependencies
created: 2026-07-02
source: PR #1
---
```

The required fields identify the node and its status. The optional `source` records where the
content came from. [schema/node.cue](../schema/node.cue) is the checkable definition.

| Type     | Directory    | ID prefix | Status values                                                         |
| -------- | ------------ | --------- | --------------------------------------------------------------------- |
| Outcome  | `outcomes/`  | `oc-`     | `open`, `in-progress`, `done`, `abandoned`                            |
| Bug      | `bugs/`      | `bd-`     | `open`, `closed`                                                      |
| Idea     | `ideas/`     | `ida-`    | `raw`, `exploring`, `adopted`, `graduated`, `superseded`, `discarded` |
| Decision | `decisions/` | `dcr-`    | `proposed`, `decided`, `superseded`                                   |

An ID consists of the type prefix and the first seven hexadecimal characters of the slug's SHA-256
hash. The filename is `<id>-<slug>.md`.

```sh
printf '%s' '<slug>' | sha256sum | cut -c1-7
```

The lint enforces this derivation for nodes created after 2026-08-05. Earlier IDs keep their
existing values and are checked for shape and uniqueness. Editing a title does not rename a node or
change its ID.

## Relationships

Outcome nodes must carry `blocked_by`, a list of node IDs. Any node type may block an outcome. A
blocker clears at a terminal state for its type, as defined in
[[dcr-593d3d1]](decisions/dcr-593d3d1-ideas-are-transitional.md):

| Type     | States that clear a blocker            |
| -------- | -------------------------------------- |
| Idea     | `graduated`, `superseded`, `discarded` |
| Decision | `decided`, `superseded`                |
| Bug      | `closed`                               |
| Outcome  | `done`, `abandoned`                    |

An adopted idea has been accepted but still awaits graduation. It remains a blocker.

Ideas and decisions may carry `supersedes`, listing the IDs they replace. Each listed node must have
status `superseded`. The surviving node declares the relationship, and the replaced node directs
readers to the current material.

A graduated idea must carry `graduated_into`. It names either a decision ID or a repository-relative
Markdown path containing the developed design. This field is allowed exactly when the idea has
status `graduated`.

Other relationships, including proposed completion and discussion edges, use prose links until the
schema supports them. Node IDs can be linked as `[[ida-eac723e]]`; ordinary relative Markdown links
also work.

## Writing and revising nodes

Open with a plain paragraph saying what the node proposes, decides, requires, or reports. Context,
reasoning, consequences, and open questions follow as needed. [AGENTS.md](../AGENTS.md) defines the
writing standard and document levels.

The graph presents current understanding. Git preserves the history of how that understanding
developed.

When a claim changes, restate it where it appears. Do not leave an earlier assertion followed by an
update that retracts it. Preserve the reason for the current design; remove discussion of discarded
alternatives unless it contains a distinct insight that still needs a home.

Where a decision replaces another, update the relevant references and examples in the same change. A
superseded node should identify its replacement before a reader encounters an obsolete rule.

For conflicting claims, use `git blame` on the claims themselves to establish which was written
later. A formatting sweep does not make an old claim new. Recency helps identify the current
decision but does not establish correctness. Resolve the conflict so the documents give a coherent
account.

Use a project voice. Refer to participants by role, and keep names out of node prose and `source`
fields. External authors and prior art may be cited.

Paragraphs are hard-wrapped at 100 characters by the repository formatter. `nix run .#fmt` applies
the format, and `prose-format` checks it.

## Editing and validation

Creating a node or changing its status is a repository change. Ordinary file tools provide listing
and search; Git provides history.

The `knowledge-lint` check validates frontmatter, IDs, filenames, typed relationships, wiki links,
graduation destinations, and relative links. Supersession targets must have the corresponding
status. The check runs without an integrator and can provide required evidence for integration under
the project's policy.

The flake exports this check as `lib.knowledgeLint` so another project can validate its graph using
the same implementation. [flake.nix](../flake.nix) documents the consuming interface.
[[ida-a9e274c]](ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md) explains the source of
valley-mandated checks.

Structural checks cannot establish that a node is clear, correct, or implemented. Those judgments
remain part of review.
