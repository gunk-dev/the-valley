---
type: decision
id: dcr-db1acbb
status: superseded
title: "The current replication mechanism is defined in dcr-d7952bc"
created: 2026-07-04
---

# The current replication mechanism is defined in dcr-d7952bc

[[dcr-d7952bc]](dcr-d7952bc-phase0-replication-github-transitional.md) defines the current
combination of live replication and encrypted offsite backup. This node is superseded.

The replication requirements need both a usable remote copy and point-in-time recovery. A Git mirror
supplies the remote copy; restic supplies encrypted, deduplicated backups to a Hetzner Storage Box.

A dedicated Hetzner VPS mirror is deferred until GitHub exit under the current decision. ZFS send is
outside the selected mechanism: it requires a compatible receive target and does not directly
provide a usable Git remote.

[[oc-fc348f0]](../outcomes/oc-fc348f0-hetzner-mechanism.md) records selection of the mechanism, and
[[oc-9949561]](../outcomes/oc-9949561-push-replication.md) tracks its implementation.
