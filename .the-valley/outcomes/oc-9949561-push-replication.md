---
type: outcome
id: oc-9949561
status: open
title: Every push replicated — classic-laddie primary + Hetzner offsite + verified restore
created: 2026-07-02
blocked_by: [oc-fc348f0]
---

# Every push replicated

The S1 infrastructure work: bare repos on classic-laddie as primary, offsite replication to Hetzner, and one full restore *performed and verified* — configured is not done ([user-scenarios.md § S1](../../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them), [roadmap.md Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github)). The implementation lives in cosmo; it is tracked here. Blocked by the mechanism decision [[oc-fc348f0]].
