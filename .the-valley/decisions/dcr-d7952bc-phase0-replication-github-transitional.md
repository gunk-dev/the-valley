---
type: decision
id: dcr-d7952bc
status: decided
title: Phase 0 live replication is GitHub (transitional mirror); sovereign live remote deferred to GitHub exit
created: 2026-07-11
---

# Phase 0 replication — GitHub as the transitional live layer

**Decided 2026-07-11.** Supersedes [[dcr-db1acbb]]
([dcr-db1acbb-hetzner-replication-mechanism.md](./dcr-db1acbb-hetzner-replication-mechanism.md)).

- **Live replication (revised).** For Phase 0 the live layer is **GitHub** — the transitional push
  mirror already declared in the host config — rather than a new Hetzner VPS. The dedicated
  sovereign live remote (the VPS) is **deferred until GitHub exit**, i.e. the point where GitHub
  stops being retained as a mirror.
- **Offsite depth (unchanged).** Nightly encrypted restic backups of the bare-repo directory to a
  Hetzner Storage Box.
- **ZFS send — still rejected for now.**

**Rationale.** During the migration window GitHub already provides a hot, independent, offsite
second copy within seconds of every push, so a Hetzner VPS mirror would duplicate it while adding a
live host to provision and operate. The Storage Box restic layer supplies the sovereign, encrypted,
point-in-time copy — and is the copy the S1 "restore performed and verified" criterion runs against.
Net: one Hetzner resource instead of two until GitHub exit.

S1 stays satisfied as written: "two independent places within minutes" = classic-laddie + GitHub;
the verified restore = from restic.

**Revisit trigger.** GitHub exit brings back the sovereign live remote: when GitHub is no longer
retained as a mirror, provision the dedicated live remote before dropping it.

Relates to [[oc-9949561]]
([oc-9949561-push-replication.md](../outcomes/oc-9949561-push-replication.md)).
