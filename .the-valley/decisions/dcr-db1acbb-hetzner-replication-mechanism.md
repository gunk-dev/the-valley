---
type: decision
id: dcr-db1acbb
status: superseded
title: Phase 0 offsite replication — git-native mirror (a) + restic (c), ZFS send rejected for now
created: 2026-07-04
---

# Phase 0 offsite replication mechanism

**Decided by patflynn, 2026-07-04.** From the options table in
[roadmap.md Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github): a combination of **(a)
git-native mirror** and **(c) restic**.

- **(a) — hot, git-native second remote.** A post-receive hook on classic-laddie pushes every ref
  update to a bare-git remote on a small Hetzner VPS. Push-triggered, so S1's "pushed = replicated
  within minutes" durability target holds.
- **(c) — encrypted offsite depth.** Nightly restic backups of the bare-repo directory to a Hetzner
  Storage Box: encrypted and deduplicated.
- **(b) ZFS send — rejected for now.** It needs a ZFS-capable receive target (a Storage Box cannot
  receive ZFS streams), and the far copy is not a usable git remote.

Closes the mechanism question tracked by [[oc-fc348f0]]. Immediate follow-up: the owner must
provision the Hetzner VPS and Storage Box before the implementation outcome [[oc-9949561]] can
complete; the implementation itself lives in cosmo.

**2026-07-11:** superseded by [[dcr-d7952bc]]
([dcr-d7952bc-phase0-replication-github-transitional.md](./dcr-d7952bc-phase0-replication-github-transitional.md))
— VPS mirror deferred until GitHub exit; restic layer carried forward.
