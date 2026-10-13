---
type: decision
id: dcr-24d62f7
status: decided
title: "Publication mirrors receive main and tags"
created: 2026-07-25
---

# Publication mirrors receive main and tags

A declared publication mirror receives `main` and tags. Topic branches stay on the primary while
they await integration. The next mirror push attempts to remove any other branches found on the
mirror.

Consumers use a publication mirror to fetch integrated work. This also supports consumers whose
tooling fetches from GitHub. A pending contribution becomes public through this mirror when it is
integrated.

## Durability

Restic provides the independent backup described in
[[dcr-d7952bc]](dcr-d7952bc-phase0-replication-github-transitional.md). The publication mirror also
provides a live offsite copy of integrated history.

Pending topic branches have a different recovery window. Before integration, their offsite
protection comes from the nightly restic snapshot. The requirement for two independent copies within
minutes therefore holds for integrated work, but does not hold for every pending branch. This
limitation is accepted for the publication mirror.

## Mechanism

The push refspec names `main` explicitly. Because Git's `--prune` only prunes heads covered by a
glob refspec, a separate sweep lists the mirror's heads and attempts to delete every head except
`main`.

The sweep is best-effort and independent of the replication push. A refused deletion is logged
without changing the reported result of that push. The sweep only reads `refs/heads`; it does not
touch mirror-owned namespaces such as GitHub's read-only `refs/pull/*`.

## Revisit condition

A consumer may eventually need mirrored access to work before integration. That would justify a
separately declared mirror with a different publication scope.
