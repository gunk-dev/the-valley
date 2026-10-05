---
type: outcome
id: oc-fc348f0
status: done
title: Hetzner replication mechanism decided (git mirror vs ZFS send vs restic — or combination)
created: 2026-07-02
blocked_by: []
---

# Hetzner replication mechanism decided

Done 2026-07-04: the instance's human operator decided as (a) + (c) in [[dcr-db1acbb]]
([dcr-db1acbb-hetzner-replication-mechanism.md](../decisions/dcr-db1acbb-hetzner-replication-mechanism.md)),
from the options table — (a) git-native mirror, (b) ZFS send, (c) restic/borg, not mutually
exclusive — in [roadmap.md Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github). This
node was deliberately the experiment's first human-blocked frontier item ([[ida-3145b7a]]
([ida-3145b7a-demand-pressure.md](../ideas/ida-3145b7a-demand-pressure.md))): unblocked in the
graph, it waited only on the operator's decision.
