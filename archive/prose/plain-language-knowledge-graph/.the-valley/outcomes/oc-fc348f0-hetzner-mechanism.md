---
type: outcome
id: oc-fc348f0
status: done
title: "The initial replication and backup mechanism is selected"
created: 2026-07-02
blocked_by: []
---

# The initial replication and backup mechanism is selected

The project has selected a live Git replica plus encrypted point-in-time backup.
[[dcr-d7952bc]](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md) is the current
decision: GitHub supplies the transitional live copy, and restic backs up to a Hetzner Storage Box.

The independent live remote must be provided before the GitHub mirror is removed.
[[oc-9949561]](oc-9949561-push-replication.md) records implementation and restore verification.

This completed decision task is also an example for
[[ida-3145b7a]](../ideas/ida-3145b7a-demand-pressure.md): work can be ready in the dependency graph
while waiting on a human choice.
