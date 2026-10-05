---
type: bug
id: bd-57f7f9f
status: open
title: Two branches whose names differ only by a separator share one request ref
created: 2026-08-27
source: review of the request-resubmission lease, 2026-08-27
---

# A change name cannot tell a slash from a dash

A request ref is `refs/the-valley/integration-requests/<target>/<change>`, and the integrator
refuses a change segment holding a slash. So a branch name is written into that segment with its
slashes replaced by dashes, and `topic/x` and `topic-x` come out as the same change. They then share
one request ref, and nothing downstream can tell which branch a request is about.

The mapping is many-to-one and nothing records which name went in. The ref names a commit and a
target, and a change is a diff targeting a stream ([[ida-93e4f91]],
[ideas/ida-93e4f91-changes-not-branches.md](../ideas/ida-93e4f91-changes-not-branches.md)), so the
branch name is not something the design keeps. That is right, and it is exactly why the lossy
encoding has nowhere to be undone.

Filing a request for one of the two branches used to be an unleased push, so a collision showed up
as git's own non-fast-forward rejection whenever the two heads had diverged, and as a silent
fast-forward whenever they had not. Now that filing and replacing are both leased and both willing
to move the ref, a collision is a clean replacement of the other branch's pending request, reported
as a resubmission of this one. The evidence of both branches stands, since that namespace is keyed
by tree, but one branch's request is gone and the operator who filed it is not told.

## What holds in the meantime

The subject line of any request being replaced is printed beside its id, so a replacement of
something unexpected is visible at the moment it happens rather than discovered afterwards. That is
a diagnostic and not a guard: it relies on someone reading the line.

Ancestry cannot supply the guard. A sibling branch's request and this branch's own previous request
both point at commits that are neither ancestors nor descendants of the head being filed — that is
what a rebase onto a moved target produces — so the shape the collision presents is the shape the
ordinary case presents too. Telling them apart needs something the refs do not carry.

## Directions, not decisions

- **Encode the branch name reversibly.** Percent-encoding the separator, or any escape the
  integrator's parser learns to read, makes the segment injective. It is the smallest change and it
  moves a format the integrator already fixes.
- **Name the change by something that is not a branch name.** A change is a diff targeting a stream,
  and a name derived from the change itself would collide only where the changes are the same. This
  is the direction that agrees with what the design already says a change is.
- **Refuse the ambiguous case at the boundary.** A request whose change segment could have come from
  more than one branch on origin is refused rather than filed, which costs nothing where no such
  pair exists and is honest where one does.
