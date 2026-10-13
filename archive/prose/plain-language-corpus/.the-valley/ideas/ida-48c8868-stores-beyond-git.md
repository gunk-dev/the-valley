---
type: idea
id: ida-48c8868
status: raw
title: "Separate store-specific replication from common backup requirements"
created: 2026-07-13
source: musing during the S1 migration, 2026-07-13
---

# Separate store-specific replication from common backup requirements

Future projects may keep important state outside Git. Live replication will need mechanisms suited
to each store, while point-in-time backup can share a common policy and storage layer.

Git can replicate through push mirrors. A database may use its transaction log, and an object store
may provide replication rules. Each store type would declare how it maintains a current remote copy.

Restic can protect the paths supplied by different stores. Those stores must first provide a
consistent backup source. A running database may need a dump or quiescing step; copying its files
alone may provide only crash-consistent data.

Filesystem snapshots could help prepare stable input for backup. This use is separate from the
choice of a live Git replication mechanism in
[[dcr-d7952bc]](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md).

Deployed-service data, such as a remediation notebook or home-automation state, motivates the idea.
Such data needs an explicit backup declaration even when it lives outside the valley data directory.
[S4](../../design/user-scenarios.md#the-rest-of-the-ladder) is the scenario where deployed-service
state becomes central.

[[dcr-0f5d9b1]](../decisions/dcr-0f5d9b1-cue-config-host-module.md) describes the configuration's
nested Git store. Expanding the supported store types remains exploratory under the current
one-repository project boundary.
