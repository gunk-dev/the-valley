---
type: outcome
id: oc-9949561
status: done
title: Every push replicated — classic-laddie primary + Hetzner offsite + verified restore
created: 2026-07-02
blocked_by: [oc-fc348f0]
---

# Every push replicated

The S1 infrastructure work: bare repos on classic-laddie as primary, offsite replication to Hetzner,
and one full restore _performed and verified_ — configured is not done
([user-scenarios.md § S1](../../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them),
[roadmap.md Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github)). The host module lives
in the-valley itself (`flake.nix` + `schema/`, per [[dcr-0f5d9b1]]); consumers — cosmo's hosts —
install and configure it. Blocked by the mechanism decision [[oc-fc348f0]].

Done 2026-07-13 — the-valley's primary populated on classic-laddie and canonical origin flipped
(GitHub demoted to a push mirror via write deploy key; hook pushes explicit head/tag refspecs per
the [[dcr-d7952bc]]-adjacent module fix
([dcr-d7952bc-phase0-replication-github-transitional.md](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md)));
replication verified by checking both sides (marker tag `s1-migration` on both within seconds);
offsite depth live (nightly restic to the Hetzner Storage Box + box auto-snapshots); and a full
restore performed and verified against real data (snapshot `28ce9e00` restored, fsck clean,
`refs/heads/main` hash-identical to the live primary).
