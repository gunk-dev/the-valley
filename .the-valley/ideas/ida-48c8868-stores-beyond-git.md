---
type: idea
id: ida-48c8868
status: raw
title: "Stores beyond git: liveness is store-native, depth is store-agnostic"
created: 2026-07-13
source: musing during the S1 migration, 2026-07-13
---

# Stores beyond git

Observation (2026-07-13): restic's virtue is that it is not tied to git — it protects a directory
tree. Given devops volatility and the ambition past SDLC use-cases
([README § where this goes](../../README.md#where-this-goes)), projects will eventually maintain
state in systems other than git.

The layering already falls out of Phase 0's durability stack:

- **Live replication (RPO≈0) is store-native.** Git has push mirrors; a database would bring WAL
  streaming; an object store brings replication policy. Store types should eventually declare their
  own liveness mechanism.
- **Depth (point-in-time, verified-restorable) is store-agnostic.** The restic layer covers whatever
  paths a store declares; the `#Backup` depth layer stays one generic thing.

Consistency wrinkle: bare git repos are file-backup-friendly; volatile stores (live databases) are
crash-consistent at best under file-level snapshots — future store types need a declared
quiesce/dump prelude. ZFS snapshots re-enter here: [[dcr-d7952bc]]
([dcr-d7952bc-phase0-replication-github-transitional.md](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md))
rejected ZFS send _for git repo replication_ specifically; atomic filesystem snapshots are the
natural substrate for freezing volatile non-git state before the depth pass — and the pilot host is
already ZFS.

Demand signal on the pilot host today: deployed-service state (reel-life's notebook, Home Assistant
state) lives outside the valley dataDir, covered by nothing declared. The rung that makes
deployed-service state matter is [S4](../../design/user-scenarios.md#the-rest-of-the-ladder); that
is when this idea gets pulled.

Extends the deliberate hedge in [[dcr-0f5d9b1]]
([dcr-0f5d9b1-cue-config-host-module.md](../decisions/dcr-0f5d9b1-cue-config-host-module.md)) — a
project _has_ stores; git is one, nested on purpose — and the schema comment on `#Project` in
[valley.cue](../../schema/valley.cue).
