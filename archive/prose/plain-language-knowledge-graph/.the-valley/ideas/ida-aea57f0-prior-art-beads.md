---
type: idea
id: ida-aea57f0
status: exploring
title: "Study Beads for graph workflow and storage tradeoffs"
created: 2026-08-04
source: prior-art review, 2026-08-04
---

# Study Beads for graph workflow and storage tradeoffs

[Beads](https://github.com/gastownhall/beads) is relevant prior art for persistent, structured
memory used by coding agents. It represents work as a dependency graph and provides commands for
finding ready work. Its documented storage backend is Dolt.

The overlap with the-valley is useful evidence that dependency-aware records address a practical
need. It does not establish that either design has found the only suitable representation.

## What to examine

Beads offers examples of short identifiers, dependency relationships, and ready-work queries. Its
related [Gas Town](https://github.com/gastownhall/gastown) system connects work records to agent
coordination.

Other ideas from the prior-art review remain worth examining: explicit duplicate and supersession
relationships, hierarchical identifiers for subtasks, and threaded messages represented as records.

A simple command to list unblocked outcomes would be immediately useful to the-valley's graph.
[[ida-3145b7a]](ida-3145b7a-demand-pressure.md) describes the scheduling behavior that would use it.

## Storage and integration requirements

Dolt supplies database queries and versioned data. The-valley's current Markdown convention instead
uses ordinary files and Git history.

The important requirements are how authoritative project state is kept consistent and how changes
are admitted. A future database backend could satisfy those requirements, but it would need a
defined relationship to code history and the integration path.

Knowledge changes must receive the required verification and approval. A code change and its outcome
update must be able to land together, as described in
[[ida-1ec03b1]](ida-1ec03b1-path-scoped-verification-policy.md). A separately mutable graph would
need to account for those guarantees.

[[ida-48c8868]](ida-48c8868-stores-beyond-git.md),
[[ida-b9f646c]](ida-b9f646c-nix-backend-not-substrate.md), and
[[ida-4ac1125]](ida-4ac1125-identity-is-derived-not-assigned.md) leave room for different storage
and implementation choices.

## Open question

What workload would make richer query or storage machinery worthwhile? Experience with a
database-backed graph can help answer that question. It should be evaluated against measured
editing, merging, and query needs rather than a general preference for files or databases.
