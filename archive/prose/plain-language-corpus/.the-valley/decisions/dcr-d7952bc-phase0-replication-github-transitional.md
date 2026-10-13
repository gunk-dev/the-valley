---
type: decision
id: dcr-d7952bc
status: decided
title: "Use GitHub for transitional live replication and restic for independent backup"
created: 2026-07-11
supersedes: [dcr-db1acbb]
---

# Use GitHub for transitional live replication and restic for independent backup

During the GitHub migration, the declared GitHub mirror provides a live offsite copy of integrated
history. Nightly encrypted restic backups to a Hetzner Storage Box provide independent,
point-in-time recovery.

The publication scope is `main` and tags, as defined in
[[dcr-24d62f7]](dcr-24d62f7-publication-mirror-not-review-queue.md). Integrated work therefore has
copies on classic-laddie and GitHub within the live replication window. Pending branches rely on the
primary and the next backup snapshot; the two-copies-within-minutes target does not cover all of
that pending work.

The verified restore required by S1 uses the restic backup. Keeping that backup independently of
GitHub also protects the migration from dependence on the publication mirror.

A dedicated live Git remote on a Hetzner VPS is deferred until GitHub ceases to be retained as a
mirror. Provision and verify that replacement before removing GitHub's live copy. This avoids
operating an additional live host during the transitional period.

[[oc-9949561]](../outcomes/oc-9949561-push-replication.md) tracks the replication outcome.
[[dcr-db1acbb]](dcr-db1acbb-hetzner-replication-mechanism.md) is the superseded mechanism decision.
