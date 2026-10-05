---
type: idea
id: ida-aea57f0
status: exploring
title: Prior art — Beads arrives at the same graph with a second history beside git
created: 2026-08-04
source: prior-art review, 2026-08-04
---

# Prior art: Beads — the same graph, a second history beside git

This node records [Beads](https://github.com/gastownhall/beads) as prior art for the knowledge
graph: an independent arrival at the same problem. Beads is a distributed graph issue tracker built
for AI coding agents, by the author of [Gas Town](https://github.com/gastownhall/gastown), a
multi-agent workspace manager that dispatches against it. Its stated problem is the one this graph
exists for: persistent, structured memory for coding agents — a dependency-aware graph replacing
markdown plans, so agents handle long-horizon work without losing context across context resets.

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

In this design, the directory convention — listing is `ls`, search is `grep`, history is `git log` —
is the current implementation, adequate at present scale. The design is deliberately uncommitted
about storage: durability attaches to project state rather than to git ([[ida-48c8868]]
([ida-48c8868-stores-beyond-git.md](./ida-48c8868-stores-beyond-git.md))), an implementation is a
backend and never the contract ([[ida-b9f646c]]
([ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md))), and where
bytes live is a swappable locator concern ([[ida-4ac1125]]
([ida-4ac1125-identity-is-derived-not-assigned.md](./ida-4ac1125-identity-is-derived-not-assigned.md))).
Tooling is expected to grow as scale demands — the ready verb below is the first instance — and
nothing in the design precludes a richer backend later.

The divergence is two commitments, and both hold under any storage technology:

- **One source of truth.** Beads runs a second version-control system beside git: two authoritative
  histories that can disagree. This design requires project state to have one history, whatever
  store holds it. A database that was the store would not offend this; a second history beside the
  code's does.
- **Writes travel the integration path.** Knowledge changes carry the same verification,
  attestation, and protected-ref story as code ([[ida-1ec03b1]]
  ([ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md))),
  and a landed change and its node flip are one integration. A store mutated directly, outside
  integration, is unacceptable whatever its technology. This, not file-versus-database, is the
  decisive incompatibility.

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

- Beads is a live natural experiment: the same graph shape over a database. Its trajectory is
  evidence about when a file convention needs tooling — a question of timing this design expects to
  face, and one it is free to answer with query verbs or a storage backend without violating either
  commitment above.
