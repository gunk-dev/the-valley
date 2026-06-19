# Outcomes — the production DAG

the-valley is a **recursive, transparent outcome-production engine**. The unit it works in is the *outcome*: a thing someone wants to exist that does not yet. Outcomes chain and recurse — "add this code to the VCS" is an outcome that is part of "deliver a feature users love to prod," which is part of something larger still. The substrate is general; the software development lifecycle is the v1 reference implementation, not the definition (see [README](../README.md)).

This document works out the scheduling consequence of taking that seriously: if outcomes are the unit of work, the graph of outcomes is a *production graph the system has pressure to complete*, and reading it generatively gives the system its scheduler for free.

## The DAG is already latent in the knowledge graph

Nothing new needs to be stored. The dependency structure is already in the [knowledge graph](./knowledge.md):

- `blocked_by` / `blocks` edges between outcome nodes **are** a dependency DAG. An outcome is `blocked_by` the sub-outcomes that must complete before it can.
- `closes` + the attestation-success → `node-status-changed` event (see [knowledge.md](./knowledge.md), *Composition with the rest of the architecture*) is exactly how an outcome **finalizes** a node: a change lands carrying `closes: [oc-…]`, the integrator's success event flips the node's status, and the edge that blocked its parent is gone.

The descriptive reading — "here is what blocks what" — is already supported. The new move is to read the same graph **generatively**: the open outcomes are not a record of intent, they are a worklist the system is under pressure to discharge. Every unfinished outcome is latent demand for an agent run.

```
            oc-feature-loved        (root, priority: high)
             ▲            ▲
       blocked_by      blocked_by
             │            │
       oc-code-in-vcs   oc-docs-shipped
             ▲
        blocked_by
             │
       oc-auth-bug-fixed   ← frontier (no open blockers)
```

## Priority propagates; the frontier gets scheduled

Root outcomes — the things a human or another system actually asked for — carry a **priority**. Priority is not a property of every node; it is set at the roots and **propagates down the DAG** to the outcomes that must complete for that root to complete (its transitive `blocked_by` closure — the ancestors-of-completion). A sub-outcome inherits the highest priority among the roots that depend on it.

Under contention, the scheduler dispatches the **frontier**: the unblocked outcomes (no open blockers remaining) that lie on the critical path to the highest-priority root. Producing the nodes that finalize the DAG for a high-priority root *is* the scheduling mechanism — there is no separate queue of "work to do," only the graph and the pressure to complete it.

This is **dependency-aware, critical-path scheduling**, not an attention router. It answers "what should the system work on next," not "what should a human look at." Those are different problems, and conflating them was the mistake the feedback reframe ([feedback.md](./feedback.md)) left half-made — see *What this splits* below.

| Concept | Meaning |
| --- | --- |
| Root outcome | An outcome with no parent in the DAG; carries an explicit priority |
| Priority propagation | A root's priority flows along `blocked_by` to every ancestor-of-completion |
| Frontier | Unblocked outcomes whose blockers are all closed; eligible to dispatch |
| Critical path | The chain of frontier→…→root that gates a given root's completion |
| Finalization | `closes` + attestation-success → `node-status-changed`; removes a blocking edge |

## Dispatch is klaus-shaped

A **scheduler controller** subscribes to the same node-mutation events the rest of the system emits (`node-created`, `node-status-changed`, `node-linked`). On each change it recomputes the frontier and the propagated priorities, then dispatches agent runs against frontier nodes — highest effective priority first. When a dispatched run lands a change that `closes` its outcome, the resulting `node-status-changed` event reopens the loop: the parent may now be on the frontier, and the scheduler dispatches again.

This is consistent with the existing open-question note that *"events spawn new agent runs scoped to acting on them — klaus-shaped"* ([openquestions.md](./openquestions.md), now under work scheduling). The scheduler is one more controller reading the log; the frontier is a derived query over the graph, rebuildable from source like every other projection.

The same run-budget and loop-cap mechanisms named in [scenarios.md](./scenarios.md) (Scenario 2) bound runaway dispatch: an outcome that keeps respawning agents without closing is itself a signal.

## What this splits

The single *"Priority layer architecture"* open question conflated two problems. The generative DAG resolves one of them:

- **(a) Work scheduling** — *which outcome the system should produce next.* Answered here: DAG priority-propagation + frontier dispatch.
- **(b) Attention routing** — *which finished or blocked outcomes a human must see.* Still open. This is the firehose problem from [feedback.md](./feedback.md), and the generative DAG says nothing about it.

[openquestions.md](./openquestions.md) is updated accordingly: the work-scheduling half points here as resolved; the attention-routing half stays open.

## Outcome is the central node type — it does not collapse into bug

An earlier open question floated collapsing `task` into `bug`. The generative reading flips that decision: the outcome/task node is the **central generative node type** — the unit of work production the whole scheduler runs on. A `bug` is merely one *kind of problem*, one reason an outcome exists. Collapsing the unit-of-work into one of its causes would erase the DAG.

Accordingly the `task` node type is reframed as **`outcome`** (`oc-*`), which reads truer to its role. See [knowledge.md](./knowledge.md) for the node-type change and the generative edge reading.

## Generality

The DAG mechanism knows nothing about code. An outcome is "a thing to be produced," a sub-outcome is "a thing that must be produced first," and finalization is "the producing happened, attestably." The SDLC reference implementation populates the graph with code-shaped outcomes (a bug fixed, a feature shipped, a dependency bumped). A different reference implementation could populate it with anything attestable — `oc-concert-tickets-booked` blocked by `oc-budget-approved` is structurally identical. The scheduler does not care; it only completes graphs.

## Open questions

See [openquestions.md](./openquestions.md) — items raised here live under *Attention, routing, and threads* (the work-scheduling/attention-routing split) and *Knowledge graph specifics* (the outcome-vs-bug type decision).
