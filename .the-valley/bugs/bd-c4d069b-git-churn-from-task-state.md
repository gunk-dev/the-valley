---
type: bug
id: bd-c4d069b
status: open
title: Storing high-frequency task and lease state in git produces merge churn and packfile bloat
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Storing task and lease state in git produces merge churn and bloat

The knowledge graph architecture places all project knowledge—outcomes, decisions, bugs, ideas, and
discussion threads—into markdown files versioned in git ([architecture.md](../../design/architecture.md)).
Sketches for autonomous agent execution ([[ida-eac723e]], [[ida-3145b7a]]) propose using frontmatter
status mutations (such as transitioning an outcome to `status: in-progress` with run leases and
heartbeat timestamps) as the concurrency lock for dispatching work. Storing high-frequency task
execution state, leases, and discussion comments directly in git commits produces severe merge
conflicts and rapid packfile bloat as agent concurrency increases.

## Why git is poorly suited for fine-grained task leases

Git is designed for durable, content-addressed trees of source code committed at human or
milestone granularity. It is not designed to serve as an in-flight job queue or distributed lock
manager.

Using git commits to manage agent scheduling encounters three concrete failure modes:

1. **Frontmatter merge conflicts.** If two agents concurrently update different fields on an outcome
   node (for instance, one updating a lease expiration while another appends a blocking edge or
   status note), standard three-way git merges frequently conflict on adjacent frontmatter lines.
   These conflicts reject integration requests, stalling automated progress.
2. **Packfile and tree bloat.** High-frequency state transitions (e.g., `open` $\rightarrow$
   `in-progress` $\rightarrow$ heartbeat lease renewal $\rightarrow$ `done`) commit hundreds of
   transient tree objects into the repository. This inflates `.git` storage, increases clone
   times, and degrades the performance of every local `git log` and `git status` command.
3. **Query latency at scale.** Finding the unblocked frontier requires reading frontmatter across
   all files in `outcomes/`. While scanning files with `grep` or filesystem traversal works at
   fifty nodes, evaluating complex DAG dependencies across thousands of historical task nodes
   imposes substantial I/O overhead on every scheduler tick.

## Why this is acceptable today

The live outcome graph currently contains fewer than ten nodes, and all outcome state transitions
are authored manually by the operator. Automated dispatchers (klaus) do not yet write leases or
execution state to git. The issue is structural to the scheduling design and will emerge as soon as
the Phase 4 agent dispatch loop is automated.

## Directions, not decisions

- **Separate ephemeral leases from durable milestones.** In-flight dispatch leases, heartbeat
  tokens, and agent execution logs belong on an ephemeral, low-latency store—such as NATS JetStream
  KV or memory—rather than in git commits.
- **Git records only terminal milestones.** An outcome is updated in git only upon creation, terminal
  completion (`done`), or abandonment (`abandoned`), eliminating high-frequency operational churn
  from git history.
- **Out-of-band discussion and thread storage.** Consider storing rapid discussion threads and
  agent reasoning traces in a dedicated append-only log or thread namespace, rather than embedding
  every reply as a separate revision of a root markdown file.

## Related

- Outcome DAG scheduler: [[ida-eac723e]]
  ([ida-eac723e-outcome-dag.md](../ideas/ida-eac723e-outcome-dag.md))
- Demand pressure and leases: [[ida-3145b7a]]
  ([ida-3145b7a-demand-pressure.md](../ideas/ida-3145b7a-demand-pressure.md))
- Project knowledge architecture: [architecture.md](../../design/architecture.md)
