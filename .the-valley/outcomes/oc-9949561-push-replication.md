---
type: outcome
id: oc-9949561
status: done
title: Every push replicated — classic-laddie primary + Hetzner offsite + verified restore
created: 2026-07-02
blocked_by: [oc-fc348f0]
---

# Every push replicated

Done 2026-07-13: the S1 infrastructure work — bare repos on classic-laddie as primary, offsite
replication to Hetzner, and one full restore _performed and verified_, because configured is not
done
([user-scenarios.md § S1](../../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them),
[roadmap.md Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github)). The host module lives
in the-valley itself (`flake.nix` + `schema/`, per [[dcr-0f5d9b1]]); consumers — cosmo's hosts —
install and configure it. The work waited on the mechanism decision [[oc-fc348f0]]
([oc-fc348f0-hetzner-mechanism.md](./oc-fc348f0-hetzner-mechanism.md)).

What was done: the-valley's primary populated on classic-laddie and canonical origin flipped —
GitHub demoted to a push mirror via write deploy key, the hook pushing explicit head/tag refspecs
per the [[dcr-d7952bc]]-adjacent module fix
([dcr-d7952bc-phase0-replication-github-transitional.md](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md)).
Replication was verified by checking both sides: marker tag `s1-migration` on both within seconds.
Offsite depth is live — nightly restic to the Hetzner Storage Box plus the box's auto-snapshots —
and a full restore was performed and verified against real data: snapshot `28ce9e00` restored, fsck
clean, `refs/heads/main` hash-identical to the live primary.
