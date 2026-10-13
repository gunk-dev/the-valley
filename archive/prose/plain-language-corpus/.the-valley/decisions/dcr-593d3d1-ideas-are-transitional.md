---
type: decision
id: dcr-593d3d1
status: decided
title: "Ideas must graduate, be replaced, or be discarded"
created: 2026-08-05
source: design conversation, 2026-08-05
---

# Ideas must graduate, be replaced, or be discarded

An idea records thinking that has not yet reached its permanent form. Accepted design belongs in a
decision or design document. An idea finishes when its content moves there, another node replaces
it, or the project discards it.

The three terminal idea states are `graduated`, `superseded`, and `discarded`. The `adopted` state
means the idea has been accepted and is waiting to graduate. It is not terminal. An idea that still
needs work should remain in a nonterminal state.

## Record the destination

A graduated idea must carry `graduated_into`. Its value is either a decision node's ID or the
repository-relative path of a design document.

The field is required exactly when the idea's status is `graduated`.
[schema/node.cue](../../schema/node.cue) checks this relationship, and the knowledge lint checks
that the destination exists.

## Clearing an outcome's blockers

A node in an outcome's `blocked_by` list stops blocking when it reaches a terminal state for its
type:

| Type     | Terminal states                        |
| -------- | -------------------------------------- |
| Idea     | `graduated`, `superseded`, `discarded` |
| Decision | `decided`, `superseded`                |
| Bug      | `closed`                               |
| Outcome  | `done`, `abandoned`                    |

These rules let a scheduler determine readiness from frontmatter without interpreting prose.
[[oc-87deec8]](../outcomes/oc-87deec8-valley-can-host-cosmo.md) is an example of an outcome with
idea nodes among its blockers.
