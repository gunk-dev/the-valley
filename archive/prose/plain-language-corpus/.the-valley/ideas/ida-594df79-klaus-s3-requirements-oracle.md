---
type: idea
id: ida-594df79
status: exploring
title: "Use Klaus to identify requirements for agent orchestration"
created: 2026-07-13
source: directed review, 2026-07-13
---

# Use Klaus to identify requirements for agent orchestration

Klaus is a working agent orchestrator whose daily use can expose the requirements for
[S3](../../design/user-scenarios.md#the-ladder). Its dependence on GitHub reveals which capabilities
the-valley must supply for equivalent work.

The migration needs more than a replacement event feed. Klaus currently uses pull requests as
workflow identities, GitHub as a state source, and pull-request metadata to persist some paused
work.

## Capabilities to preserve

| Klaus capability                                     | Capability needed from the-valley                                 |
| ---------------------------------------------------- | ----------------------------------------------------------------- |
| Webhooks followed by state reconciliation            | Replayable events and queryable current state                     |
| Approval and merge-readiness policy                  | Integration requests evaluated under declared policy              |
| Opening a pull request                               | Submitting an identifiable change for integration                 |
| Launching work from an issue or prompt               | Dispatch against an outcome with a complete brief                 |
| Run state, trajectories, dashboard, and watch stream | A queryable history linking runs, changes, and outcomes           |
| Review feedback that triggers further work           | Recorded feedback available to a repair or revision run           |
| Budget pause and later resume                        | Durable pause state, work-in-progress changes, and a resume index |

Klaus's `pr:approval-changed` event already anticipates merge-readiness sources beyond GitHub. That
is a useful point of separation, but other assumptions still need to be examined.

## Failure modes the migration must address

A pushed branch must be discoverable as work awaiting integration even when no pull request exists.
A run ending with a generic exit status is insufficient.

A budget pause must preserve both the work and the fact that it is paused. Pushing a
work-in-progress branch alone does not supply the resume marker that a GitHub draft, label, and
comment currently provide.

Run attribution must be explicit. Scraping a transcript for a pull-request URL can associate the run
with an unrelated request it merely mentions.

An interrupted stream may end without the final record expected by the harness. Crash and
usage-limit handling must still produce an accurate failure or attention state. These are examples
of the stalled work described in [[ida-3145b7a]](ida-3145b7a-demand-pressure.md).

## Events and request state

Ref-update events need repository, ref, old and new revision, and pusher identity. Workstation
clients must be able to subscribe and replay.

The orchestrator also needs queryable request status. Integration success and stale-evidence events
can invalidate its view, but it must recover the current state without receiving every event. A
branch awaiting integration must be a recognizable state.

An absent expected push can help detect unfinished work, but detecting a dead run also needs
run-liveness information.

## Dispatch, attribution, and pause

Dispatch targets an outcome as proposed in [[ida-eac723e]](ida-eac723e-outcome-dag.md). The node
must provide a sufficient brief, or dispatch must supply additional context explicitly.

The host-signed provenance defined in
[[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) records the relationship
between run and change. It removes the need to infer that relationship from transcript text.

Klaus already stores resume trajectories through Git under `refs/klaus/data`. A valley workflow
still needs durable homes for the pause marker and resume lookup. A node plus work-in-progress
branch is a candidate arrangement.

These are migration requirements to verify against the current Klaus implementation. They do not
imply that replacing its GitHub event source completes the migration.
