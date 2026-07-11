---
type: outcome
id: oc-fc348f0
status: done
title: Hetzner replication mechanism decided (git mirror vs ZFS send vs restic — or combination)
created: 2026-07-02
blocked_by: []
---

# Hetzner replication mechanism decided

**Actor: patflynn (human).** The options table — (a) git-native mirror, (b) ZFS send, (c) restic/borg, not mutually exclusive — is in [roadmap.md Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github). This node is deliberately the experiment's first human-blocked frontier item ([[ida-3145b7a]]): unblocked in the graph, waiting only on the owner's decision.

Done 2026-07-04 — decided as (a) + (c) in [[dcr-db1acbb]] ([dcr-db1acbb-hetzner-replication-mechanism.md](../decisions/dcr-db1acbb-hetzner-replication-mechanism.md)).
