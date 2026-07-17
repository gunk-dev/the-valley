---
type: idea
id: ida-eac723e
status: adopted
title: The outcome DAG as a generative scheduler — the full sketch
created: 2026-07-02
source: PR #1
---

# The outcome DAG as a generative scheduler

> Thin form:
> [architecture.md § the knowledge graph read generatively — an outcome DAG](../../design/architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag).
> This node carries the full sketch from PR #1's `design/outcomes.md` (plus the outcome-vs-bug
> material from its `design/knowledge.md` changes), preserved at full fidelity; the mechanics below
> return as a design doc when rung S3+ of the [scenario ladder](../../design/user-scenarios.md) is
> the top priority.

the-valley is a **recursive, transparent outcome-production engine**. The unit it works in is the
_outcome_: a thing someone wants to exist that does not yet. Outcomes chain and recurse — "add this
code to the VCS" is an outcome that is part of "deliver a feature users love to prod," which is part
of something larger still. The substrate is general; the software development lifecycle is the v1
reference implementation, not the definition.

This sketch works out the scheduling consequence of taking that seriously: if outcomes are the unit
of work, the graph of outcomes is a _production graph the system has pressure to complete_, and
reading it generatively gives the system its scheduler for free.

## The DAG is already latent in the knowledge graph

Nothing new needs to be stored. The dependency structure is already in the typed-node knowledge
graph (PR #1's `knowledge.md`; thin form in
[architecture.md § project knowledge is a typed-node graph](../../design/architecture.md#bet-project-knowledge-is-a-typed-node-graph)):

- `blocked_by` / `blocks` edges between outcome nodes **are** a dependency DAG. An outcome is
  `blocked_by` the sub-outcomes that must complete before it can.
- `closes` + the attestation-success → `node-status-changed` event is exactly how an outcome
  **finalizes** a node: a change lands carrying `closes: [oc-…]`, the integrator's success event
  flips the node's status, and the edge that blocked its parent is gone.

The descriptive reading — "here is what blocks what" — is already supported. The new move is to read
the same graph **generatively**: the open outcomes are not a record of intent, they are a worklist
the system is under pressure to discharge. Every unfinished outcome is latent demand for an agent
run.

```
            oc-feature-loved        (root, priority: high)
             │            │
       blocked_by      blocked_by
             ▼            ▼
       oc-code-in-vcs   oc-docs-shipped
             │
        blocked_by
             ▼
       oc-auth-bug-fixed   ← frontier (no open blockers)
```

## Priority propagates; the frontier gets scheduled

Root outcomes — the things a human or another system actually asked for — carry a **priority**.
Priority is not a property of every node; it is set at the roots and **propagates down the DAG** to
the outcomes that must complete for that root to complete (its transitive `blocked_by` closure — the
ancestors-of-completion). A sub-outcome inherits the highest priority among the roots that depend on
it.

Under contention, the scheduler dispatches the **frontier**: the unblocked outcomes (no open
blockers remaining) that lie on the critical path to the highest-priority root. Producing the nodes
that finalize the DAG for a high-priority root _is_ the scheduling mechanism — there is no separate
queue of "work to do," only the graph and the pressure to complete it.

This is **dependency-aware, critical-path scheduling**, not an attention router. It answers "what
should the system work on next," not "what should a human look at." Those are different problems,
and conflating them was the mistake the feedback reframe
([architecture.md § review is observability + feedback](../../design/architecture.md#bet-review-is-observability--feedback))
left half-made — see _What this splits_ below.

| Concept              | Meaning                                                                         |
| -------------------- | ------------------------------------------------------------------------------- |
| Root outcome         | An outcome with no parent in the DAG; carries an explicit priority              |
| Priority propagation | A root's priority flows along `blocked_by` to every ancestor-of-completion      |
| Frontier             | Unblocked outcomes whose blockers are all closed; eligible to dispatch          |
| Critical path        | The chain of frontier→…→root that gates a given root's completion               |
| Finalization         | `closes` + attestation-success → `node-status-changed`; removes a blocking edge |

## Dispatch is klaus-shaped

A **scheduler controller** subscribes to the same node-mutation events the rest of the system emits
(`node-created`, `node-status-changed`, `node-linked`). On each change it recomputes the frontier
and the propagated priorities, then dispatches agent runs against frontier nodes — highest effective
priority first. When a dispatched run lands a change that `closes` its outcome, the resulting
`node-status-changed` event reopens the loop: the parent may now be on the frontier, and the
scheduler dispatches again.

Because recomputation is event-driven, bursts of node mutations can recompute the frontier many
times in quick succession — so dispatch must be **idempotent per outcome**, never spawning a second
agent for an outcome that already has one in flight. The controller tracks in-flight runs as state
on the outcome itself: a dispatch flips the node to `status: in_progress` (carrying the run's id)
before the agent starts, and only `open` frontier outcomes are eligible to dispatch. That status
transition is the lock — it is itself a `node-status-changed` event, so the in-flight set is a
derived query over the graph like every other projection, with no separate scheduler-private store
to keep consistent. A run that dies without closing or releasing its outcome is caught by the same
run-budget and loop-cap mechanisms below (an outcome stuck `in_progress` past a threshold is a
staleness signal that returns it to the frontier).

This is consistent with the note that _"events spawn new agent runs scoped to acting on them —
klaus-shaped"_ (PR #1's `openquestions.md`, under work scheduling). The scheduler is one more
controller reading the log; the frontier is a derived query over the graph, rebuildable from source
like every other projection.

The same run-budget and loop-cap mechanisms named in [scenarios.md](../../design/scenarios.md)
(_Agent loops_) bound runaway dispatch: an outcome that keeps respawning agents without closing is
itself a signal.

## What this splits

The single _"Priority layer architecture"_ open question conflated two problems. The generative DAG
resolves one of them:

- **(a) Work scheduling** — _which outcome the system should produce next._ Answered here: DAG
  priority-propagation + frontier dispatch.
- **(b) Attention routing** — _which finished or blocked outcomes a human must see._ Still open.
  This is the firehose problem from the feedback reframe, and the generative DAG says nothing about
  it.

[openquestions.md](../../design/openquestions.md) reflects the split: the work-scheduling half is
resolved by this sketch; the attention-routing half stays open.

## Outcome is the central node type — it does not collapse into bug

An earlier open question floated collapsing `task` into `bug`. The generative reading flips that
decision: the outcome/task node is the **central generative node type** — the unit of work
production the whole scheduler runs on. A `bug` is merely one _kind of problem_, one reason an
outcome exists. Collapsing the unit-of-work into one of its causes would erase the DAG.

Accordingly the `task` node type is reframed as **`outcome`** (`oc-*`), which reads truer to its
role.

### Bugs motivate outcomes; the lifecycles stay separate

The two link explicitly via the `motivates` / `motivated_by` edge: a `bug` `motivates` an `oc-*`,
and the outcome is `motivated_by` the bug. They keep **separate lifecycles**, and `closes` always
targets the outcome, never the bug directly. When an attestation lands carrying `closes: [oc-…]`,
the integrator's success event flips _only_ the outcome's status; a controller subscribed to that
`node-status-changed` then resolves the motivating bug — auto-closing it when the closed outcome was
its sole motivated-by, or leaving it open when other outcomes still reference it (one bug can
motivate several fixes). This keeps the work unit (the outcome the DAG schedules on) and the problem
record (the bug) from being conflated: a bug is not "done" because one of the outcomes it spawned
landed, only when nothing it motivates remains open.

## Generality

The DAG mechanism knows nothing about code. An outcome is "a thing to be produced," a sub-outcome is
"a thing that must be produced first," and finalization is "the producing happened, attestably." The
SDLC reference implementation populates the graph with code-shaped outcomes (a bug fixed, a feature
shipped, a dependency bumped). A different reference implementation could populate it with anything
attestable — `oc-concert-tickets-booked` blocked by `oc-budget-approved` is structurally identical.
The scheduler does not care; it only completes graphs.

## Related

- Disposition of this material: [[dcr-2113c52]]
  ([decisions/dcr-2113c52-pr1-relanding.md](../decisions/dcr-2113c52-pr1-relanding.md))
- The organizational layer the scheduler runs inside: [[ida-8482624]]
  ([ida-8482624-federation-groups.md](./ida-8482624-federation-groups.md))
