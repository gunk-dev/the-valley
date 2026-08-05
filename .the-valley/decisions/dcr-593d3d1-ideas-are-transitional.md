---
type: decision
id: dcr-593d3d1
status: decided
title: An idea graduates or is discarded
created: 2026-08-05
source: design conversation, 2026-08-05
---

# An idea graduates or is discarded

An idea is transitional. Its terminal states are `graduated`, `superseded`, and `discarded`: its
thinking moved into a decision or a design document, another node replaced it, or it was dropped.
`adopted` is explicitly not terminal — an adopted idea is accepted and awaiting graduation.

Ideas are where thinking settles, not where it lives permanently; the design's durable homes are
decisions and design documents. An idea that can neither graduate nor be discarded is unsettled
thinking, and its status should say so.

## Graduation is declared in frontmatter

A graduated idea carries `graduated_into`: the id of the decision node, or the repo-root-relative
path of the design document, its thinking moved to. The field is required exactly when the status is
`graduated`, so a graduation cannot be half-declared — the same coupling `supersedes` has with the
`superseded` status. [schema/node.cue](../../schema/node.cue) makes the states and the field
checkable, and the knowledge lint checks that the destination exists.

## When a blocker clears

A node named in an outcome's `blocked_by` stops blocking when its own status is terminal for its
type: an idea at `graduated`, `superseded` or `discarded`; a decision at `decided` or `superseded`;
a bug at `closed`; an outcome at `done` or `abandoned`. This is what makes an outcome's frontier
computable from frontmatter alone, whatever the types of its blockers. [[oc-87deec8]]
([oc-87deec8-valley-can-host-cosmo.md](../outcomes/oc-87deec8-valley-can-host-cosmo.md)) waits on
four ideas among its five blockers; each of those clears the frontier by graduating, being
superseded, or being discarded.
