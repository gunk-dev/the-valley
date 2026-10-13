---
type: idea
id: ida-93e4f91
status: adopted
title: "Give a proposed change an identity that survives branch movement"
created: 2026-07-14
source: brainstorm during the S1 week
---

# Give a proposed change an identity that survives branch movement

The object being reviewed is a change targeting a version stream: its delta, provenance, and
evidence about the resulting tree. A branch is one way to transport that object.

A stable change identity would connect review, revisions, retries, and integration even as the
branch moves or the change is rebased. It would also make repeated automated attempts recognizable
as work on the same intended change.

## Base changes and evidence

Applying a delta to a new base may produce a different tree even when there is no textual conflict.
Evidence about the original tree does not automatically establish the new result.

[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md) defines
when individual check results can transfer. Stable change identity preserves the relationship
between attempts; it does not remove the need to validate evidence after the base changes.

## Dependencies between changes

Changes that commute can be applied in either order. Changes with dependencies or conflicts need an
ordering.

This suggests representing pending work as a partial order rather than treating every branch as
competing for one unchanged base. The relationship resembles the outcome dependencies in
[[ida-eac723e]](ida-eac723e-outcome-dag.md), with code changes connected to the intent they serve.

Textually separate edits can still have semantic dependencies. The change model and evidence rules
must account for that distinction.

## Candidate representations

An integration request already carries much of a change object. A dedicated ref namespace such as
`refs/the-valley/changes/*` could hold revisions, with tools such as `git range-diff` helping
compare them.

Useful prior art includes jj change IDs, Gerrit's change and patchset model, the kernel's patch
workflow, and the patch theories used by Darcs and Pijul.

An agent could consume an outcome and emit a change. A paused run could preserve its unfinished
change, trajectory, and pause state for later continuation.
[[ida-594df79]](ida-594df79-klaus-s3-requirements-oracle.md) describes the Klaus requirements.

## Open questions

- What defines the identity of a change across rebases and regeneration?
- What counts as a new revision of the same change?
- Which conflicts or dependencies impose an order?
- How are approval, attribution, and the newly resulting tree linked after reapplication?

[contribute.md](../../design/contribute.md) describes the current request-based transport.
