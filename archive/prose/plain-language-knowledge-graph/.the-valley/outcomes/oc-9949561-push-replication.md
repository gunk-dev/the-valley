---
type: outcome
id: oc-9949561
status: done
title: "The primary repository has offsite copies and a verified restore"
created: 2026-07-02
blocked_by: [oc-fc348f0]
---

# The primary repository has offsite copies and a verified restore

The-valley's primary repository is hosted on classic-laddie, with a transitional GitHub publication
mirror and encrypted restic backups. A full restore from the offsite backup has been performed and
checked.

The verification record includes the `s1-migration` marker tag appearing on both live copies within
seconds. Restic snapshot `28ce9e00` was restored, passed `git fsck`, and had a `refs/heads/main`
hash identical to the live primary.

[[dcr-d7952bc]](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md) defines the
replication layers. [[dcr-24d62f7]](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md)
limits the publication mirror to integrated history and tags; pending branches rely on periodic
backup until integration.

The host module is supplied by the-valley and configured by consumers such as cosmo, as decided in
[[dcr-0f5d9b1]](../decisions/dcr-0f5d9b1-cue-config-host-module.md).
[[oc-fc348f0]](oc-fc348f0-hetzner-mechanism.md) records the mechanism decision.

This completes the initial infrastructure and restore work for
[S1](../../design/user-scenarios.md#s1--repositories-under-my-control-and-recoverable).
