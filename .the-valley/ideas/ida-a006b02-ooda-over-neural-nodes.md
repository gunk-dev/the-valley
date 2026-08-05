---
type: idea
id: ida-a006b02
status: exploring
title: An OODA loop over neural nodes
created: 2026-08-05
source: design conversation, 2026-08-05
---

# An OODA loop over neural nodes

It is a goal of the system to track the performance of its neural nodes and to optimize their
success and cost. A neural node is a node in the production DAG computed by neural rather than
deterministic compute — a model run or a human participant alike ([[ida-b48bded]]
([ida-b48bded-production-dags-and-events.md](./ida-b48bded-production-dags-and-events.md))). The
loop is observe, orient, decide, act (Boyd's OODA): observe what runs cost and whether their changes
succeed; orient by aggregating over like runs; decide the conditions of the next dispatch; act by
dispatching under them.

## The unit of tracking is the provenance class

A run holds no persistent identity — its identity is its provenance ([[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md))), so
performance cannot attach to a name. It attaches to the provenance class: the equivalence class of a
run's recorded conditions — the model, the effort, the shape of the brief, the kind of task. Human
participants are neural nodes in the same class system, and their performance dimension is already
visible as the stall input of [[ida-3145b7a]]
([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md)) — a change waiting on review is
a cost like any other.

## The pieces that exist, and the one that is missing

- **The record.** A run's provenance is recorded, and the harness's run state carries its cost and
  the model and effort it executed under.
- **The offline half.** A recorded provenance is the substrate for replaying work with varied
  inputs, which is evaluation ([[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md))).
- **The lever.** The conditions of a dispatch are production inputs ([[ida-febcd97]]
  ([ida-febcd97-neural-node-conditions.md](./ida-febcd97-neural-node-conditions.md))), and dispatch
  already accepts a per-run model and effort.
- **The missing instrument: a success signal.** Nothing today records whether a run's change
  integrated, needed rework, or was rejected. That arrives when the integrator emits integration
  outcome events ([roadmap Phase 3](../../design/roadmap.md#phase-3--the-integrator)); the loop as a
  whole sits naturally in
  [Phase 7, feedback and incident memory](../../design/roadmap.md#phase-7--feedback--incident-memory).

## The decide step, made concrete: task sizing

The first function for the loop to learn is task sizing: a mapping from a task's kind to the
conditions of its dispatch — the model, the effort, the budget. The lever and the record above
already suffice for it, except for the label that groups observations: nothing today records what
kind of task a run served. The knowledge graph supplies that label. When a dispatch targets an
outcome node ([roadmap Phase 4](../../design/roadmap.md#phase-4--agents-as-first-class-authors)),
the run's record joins to task identity, and sizing becomes learnable over real history.

Task sizing is knowledge that lives in the graph twice, as different artifacts. A size expectation
on a task node is a prediction, consumed once and corrected by the run that executes it. The
task-kind vocabulary is the reusable domain the sizing function is learned over. The two must not be
blurred.

A hand-rolled sizing function already operates: the coordinating agent's dispatch practice assigns
small budgets to capture tasks and large ones to builds, and that mapping lives in no durable place.
Recording it as the default strategy — the starting point the loop improves — is the same migration
the project already practices, moving coordinator knowledge into the graph.

## One pattern, not two systems

[verification.md](../../design/verification.md) already runs this shape for a different quantity:
trust is measured per attester from re-verification confirm rates, and divergence revokes it.
Measured standing, derived from outcomes, driving a decision. The neural-node loop and the trust
loop should be recognized as one pattern before they are built as two systems.

## Open

- What counts as a run's success: integrated without rework, integrated and not reverted within some
  horizon, or closing the outcome it serves. Undecided.
- Goodhart: a measured node optimizes the measure, and conditions tuned toward a success signal
  drift toward gaming it. This applies uniformly across human and model nodes.
- Where the decide step's policy lives, and what may change it — routing policy is itself system
  configuration, which self-transparency ([self-transparency.md](../../design/self-transparency.md))
  wants governed like any output.
- Where sizing knowledge lives in node mechanics — a frontmatter field on task nodes, a kind
  vocabulary, or both — is undecided.
