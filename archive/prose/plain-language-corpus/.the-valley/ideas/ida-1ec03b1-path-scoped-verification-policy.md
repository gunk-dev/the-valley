---
type: idea
id: ida-1ec03b1
status: exploring
title: "Determine required checks from the paths a change actually modifies"
created: 2026-07-06
source: conversation, 2026-07-06
---

# Determine required checks from the paths a change actually modifies

The integrator determines required checks from the actual tree diff and the governing policy. A
contributor's description of the change does not determine which checks apply.

Policy assigns checks to classes of paths. A knowledge-only change needs graph validation and prose
formatting. A code change needs the checks required for that code. A change touching both requires
the union.

This permits a code change and the completion of its outcome node to land together. Keeping the
graph in the repository gives both the same integration protection and backup path, while allowing
documentation-only work to use cheaper checks.

[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) defines the CUE policy
schema and how valley and project requirements compose. A change signature is required structurally,
independently of path classification.

## Knowledge checks

The knowledge lint is one pure derivation. It can run locally, supply evidence for integration, and
later be rerun by an independent verifier. [.the-valley/README.md](../README.md) lists its checks.

This matters to the scheduler proposal in [[ida-3145b7a]](ida-3145b7a-demand-pressure.md): updating
a node must be inexpensive enough for the graph to remain useful during ongoing work.

## Prose formatting

Markdown paragraphs are hard-wrapped at 100 characters by a deterministic formatter. The check
passes when formatting the tree produces no change.

This format keeps raw files readable in a terminal and makes the formatting requirement mechanically
checkable. Reflow can enlarge a diff after a small edit, so `valley review` uses word-level
highlighting to help review prose changes.
