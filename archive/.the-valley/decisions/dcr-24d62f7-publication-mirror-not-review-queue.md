---
type: decision
id: dcr-24d62f7
status: decided
title: A mirror publishes main and tags — the review queue is not published
created: 2026-07-25
---

# The mirror is a publication mirror, not a copy of the primary

**Decided 2026-07-25.** A declared mirror receives `main` and the tags, and nothing else. Any other
branch found on a mirror is removed on the next push. Topic branches live only on the primary until
they are integrated.

Until now the mirror hook pushed `+refs/heads/*:refs/heads/*`, so every branch reached the mirror
the instant it reached the primary. On a public mirror that makes _pending review_ and _published_
the same state: work that has not been read yet is already the project's public face, and
withdrawing it means deleting a branch from a repo other people may have fetched.

The mirror exists so that consumers can fetch what the project has integrated, and so that
GitHub-hosted consumers can depend on it at all. It is not the durability layer — restic is
([dcr-d7952bc](./dcr-d7952bc-phase0-replication-github-transitional.md)) — and it is not the review
queue. Narrowing it costs nothing it was actually for.

**What this changes about [[dcr-d7952bc]].** That decision counts GitHub as Phase 0's live
replication layer, "a hot, independent, offsite second copy within seconds of every push". That
remains true for integrated history, which is what a restore has to recover. It is no longer true
for a topic branch: between its push to the primary and its integration, the only offsite copy is
the nightly restic snapshot. S1's "two independent places within minutes" therefore holds for
integrated work and not for in-flight branches. This is accepted deliberately — the exposure window
is a branch's review latency, and the alternative is publishing everything unread.

**Mechanism.** The heads refspec names one ref, so `git push --prune` no longer prunes heads (prune
only considers glob refspecs). A separate best-effort sweep does it: list the mirror's heads, delete
every one that is not `main`. The sweep is deliberately independent of the replication push — it
never changes whether that push is reported as succeeding, and a mirror that refuses the deletion
costs a log line. It reads only `refs/heads`, so a namespace the mirror owns and the primary cannot
write — GitHub's read-only `refs/pull/*` — is never touched.

**Revisit trigger.** A consumer that needs to fetch pre-integration work from a mirror. Today nobody
does; that would be a request for a second, differently-scoped mirror rather than a reason to widen
this one.
