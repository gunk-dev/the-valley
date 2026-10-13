---
type: bug
id: bd-c4d069b
status: open
title: "Frequent coordination updates may make the Git-backed graph expensive to use"
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Frequent coordination updates may make the Git-backed graph expensive to use

Recording every lease renewal, heartbeat, and discussion update as a Git commit may create editing
conflicts and unnecessary repository growth. The scheduler sketches in
[[ida-eac723e]](../ideas/ida-eac723e-outcome-dag.md) and
[[ida-3145b7a]](../ideas/ida-3145b7a-demand-pressure.md) need to establish which execution state
belongs in durable project history.

This is a projected workload concern. The current small graph is edited without an automated lease
or heartbeat writer, so it does not demonstrate the proposed high-frequency workload.

## Possible costs

Concurrent edits to nearby frontmatter fields can conflict. A lease update and a dependency edit may
then require resolution even though their intended changes are compatible.

Frequent commits create more history and Git objects. The effect on storage, clone time, and history
queries depends on record size, compression, and update rate. It needs measurement; the number of
commits alone does not establish that every Git operation will become slow.

A scheduler that rescans all outcome files on every tick may also spend increasing time parsing and
traversing the graph. Derived indexes or incremental queries could reduce that cost without changing
the authoritative store.

## Candidate approaches

Keep short-lived coordination state in a store suited to atomic claims and frequent renewal, while
integrating durable outcome milestones through Git.

Reduce the number of persisted transitions if some are only operational details. Which milestones
belong in history remains a design question; creation and completion are clear examples.

Store rapid discussion or run traces in an append-only record with stable references from the graph,
rather than repeatedly rewriting a large node.

## Constraint and investigation

Any additional store needs explicit recovery and authority rules. A disposable lease index and a
second authoritative outcome database have different consequences. The proposal must preserve the
[architecture's](../../design/architecture.md) integration and history guarantees.

Measure plausible concurrent editing and scheduler workloads before selecting a backend. The tests
should cover conflicts, restart recovery, repository growth, and the cost of finding ready work.
