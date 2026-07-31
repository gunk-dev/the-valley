---
type: idea
id: ida-3145b7a
status: exploring
title: Actors in the graph + demand pressure as anti-stall
created: 2026-07-02
source: design conversation, 2026-07-02
---

# Actors in the graph + demand pressure as anti-stall

> Extends the adopted outcome-DAG sketch: [[ida-eac723e]]
> ([ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md)); the thin bet is
> [architecture.md § the knowledge graph read generatively — an outcome DAG](../../design/architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag).
> Status is _exploring_, not adopted — this node is an experiment candidate, and its first test is
> already running: the seeded [outcomes/](../outcomes/) DAG rooted at [[oc-f3bcfd0]]
> ([oc-f3bcfd0-s1-holds.md](../outcomes/oc-f3bcfd0-s1-holds.md)).

**The core idea:** agents — human and AI — can play parts in the outcome graph and be scheduled to
unblock productions, so that there is a form of standing pressure toward producing the final node:
an anti-stall mechanism.

Everything below the next heading is development of that idea, not part of it.

## Heterogeneous actor pool

Humans and AI agents are the same kind of participant: schedulable actors with capabilities. What
differs is how dispatch reaches them — an AI agent is _spawned_ (klaus-shaped, as in the adopted
sketch's dispatch section), while a human's _attention is routed_ to the blocked node. Same
scheduler, two delivery mechanisms.

Stated plainly, the consequence: the priority-layer question that [[ida-eac723e]] split in two —
**work-scheduling** vs **attention-routing** — partially _reunifies_. Attention-routing is
work-scheduling for the actor class that cannot be spawned. A human-blocked outcome is not a
different kind of thing from an agent-dispatchable one; it is a frontier node whose assigned actor
happens to be reached by notification rather than by process creation. (The firehose half of
attention-routing — what a human must _see_ across all finished work — stays a separate open
question; only the "human as blocking producer" half folds back in.)

## Pressure is standing, not event-shaped

Demand propagates from live root outcomes down the `blocked_by` closure, exactly as priority does in
the adopted sketch — but the controller does not dispatch once and forget. It continuously
reconciles _what should be moving but isn't_: level-triggered, not edge-triggered, the same
reconcile-don't-just-react controller principle the rest of the architecture leans on. An event
kicks a recompute sooner; the absence of events never means the absence of pressure.

## Stall is the named failure mode

Naming the failure mode makes anti-stall designable. A small taxonomy:

| Stall                            | Signature                                                       |
| -------------------------------- | --------------------------------------------------------------- |
| Agent run died silently          | Outcome stuck `in-progress`, no live run behind it              |
| Blocked on an unresponsive human | Human-actor frontier node open past a threshold                 |
| Dependency went stale            | A blocker closed as `abandoned`, or its premise no longer holds |
| Orphaned outcome                 | No path from any live root — demand for it is gone              |
| A change that cannot land        | The same change failing verification across repeated cycles     |

A change that cannot land is distinctive because the retry loop conceals it: the failure is not
subtle, its recurrence is. An automated proposal that regenerates on a schedule presents each cycle
as a fresh attempt, so repeated failures accumulate nothing — a scheduled dependency-update proposal
can fail verification on four consecutive nights and be indistinguishable, at any single moment,
from one that failed once. What makes the recurrence countable is change identity: a change whose
identity is stable across regeneration turns several independent failures into one change that has
failed several times, a fact that can be thresholded. That is the property argued for in
[[ida-93e4f91]] ([ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)). The
response needs no new mechanism — the escalation below already covers it; what this class adds is
the signature. It is an instance of the attention-routing question
([openquestions.md](../../design/openquestions.md), _Priority layer architecture_), but a separable
and much earlier one: a counter and a threshold on a single fact, not a solution to the firehose
problem that [roadmap.md, Phase 7](../../design/roadmap.md#phase-7--feedback--incident-memory)
carries.

Candidate mechanisms: **leases** on `in-progress` with expiry (a run that dies loses its lease and
the node returns to the frontier — the adopted sketch's staleness threshold, made explicit);
**escalation** to a different actor class (an unresponsive human's node can be re-routed, or an
agent's repeated failure escalated to a human); **re-dispatch** for dead runs; and the inverse for
orphans — outcomes with no path to a live root _lose_ pressure and become garbage-collectible, an
anti-clutter mechanism for free.

## The Nix resonance

This is `nix build` for work: you demand the final artifact and the graph schedules everything
required to produce it, caching what is already done. Closed outcomes are cache hits; the frontier
is the build plan; pressure is the outstanding derivation. A Nix-native system whose work management
mirrors its build semantics is pleasingly self-similar.

## Why it matters

Without pressure the graph is a passive tracker — an issue list with extra steps. Pressure is what
makes it a production _engine_: the standing force that turns "here is what blocks what" into "this
is being produced." It is load-bearing for the **recursive, transparent outcome-production engine**
framing in the [README](../../README.md).

## Related

- The sketch this extends: [[ida-eac723e]]
  ([ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md))
- The thin bet:
  [architecture.md § the knowledge graph read generatively — an outcome DAG](../../design/architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)
- First test: the live [outcomes/](../outcomes/) DAG rooted at [[oc-f3bcfd0]], with [[oc-fc348f0]]
  as the first deliberately human-blocked frontier item
