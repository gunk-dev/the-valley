---
type: idea
id: ida-aea57f0
status: exploring
title: Prior art — Beads arrives at the same graph with the opposite substrate bet
created: 2026-08-04
source: prior-art review, 2026-08-04
---

# Prior art: Beads — the same graph, the opposite substrate bet

This node records [Beads](https://github.com/gastownhall/beads) as prior art for the knowledge
graph: an independent arrival at the same problem, resolved with the opposite substrate bet. Beads
is a distributed graph issue tracker built for AI coding agents, by the author of
[Gas Town](https://github.com/gastownhall/gastown), a multi-agent workspace manager that dispatches
against it. Its stated problem is the one this graph exists for: persistent, structured memory for
coding agents — a dependency-aware graph replacing markdown plans, so agents handle long-horizon
work without losing context across context resets.

## The convergence

Independent design arrived at the same elements: typed nodes with dependency edges, living with the
code rather than in a hosted platform; hash-style short ids; edge types including supersedes and
duplicates; a message node type with threading. Two designs hitting the same shape independently is
evidence the shape is real: agents need a durable typed graph that travels with the repository.

## The divergence

Beads stores in Dolt, a version-controlled SQL database, embedded under the repository in a
dot-directory; its own JSONL export is documented as not the source of truth. Its rationale for the
database is cell-level merge, multi-writer concurrency for fleets of simultaneous agents, built-in
history, and branching independent of git branches.

the-valley's bet is the inverse — a directory convention, not a system; listing is `ls`, search is
`grep`, history is `git log` — and each of the database's four justifications dissolves against this
design rather than transferring:

- Built-in history: git already provides it when the store is files.
- Cell-level merge: one node per file with hash-derived ids makes collisions rare, and integration
  is serialized through the integrator.
- Multi-writer concurrency: this design has no shared mutable state to write concurrently — every
  change travels branch, review, integration.
- Version-control branching independent of git: from this design's side a defect, because knowledge
  state diverging from code state breaks the property that a landed change and its node flip are one
  commit ([[ida-1ec03b1]]
  ([ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md))).

The decisive incompatibility: a write to an embedded database bypasses the integration path entirely
— no signature, no attestation, no path-scoped checks, invisible to the protected-ref invariant. A
second source of truth for project knowledge is the thing this design's durability and verification
story exists to preclude.

## Worth taking — ideas, not code

- The edge vocabulary: supersedes is already arriving; duplicates is the edge the
  corpus-normalization work will want.
- Hierarchical child ids for subdividing a node.
- Messages as nodes with threading — the concrete form of threads-as-derived-views
  ([architecture.md](../../design/architecture.md)).
- A ready verb: compute the unblocked frontier of the outcome DAG from `blocked_by` edges. This is
  [[ida-3145b7a]]'s ([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md))
  demand-pressure surface as a one-line query, and it is ergonomics this graph currently lacks. At
  this graph's scale it is a script over files, not a database.

## Open

- Beads is a live natural experiment: the same problem with the opposite substrate bet. What its
  trajectory shows about file-convention graphs at larger scale — and whether the answer is more
  small query verbs over files — is worth revisiting.
