---
type: idea
id: ida-eac723e
status: adopted
title: "Schedule work from outcomes and their dependencies"
created: 2026-07-02
source: PR #1
---

# Schedule work from outcomes and their dependencies

An outcome states something the project wants to achieve. Outcomes can depend on other outcomes. A
scheduler could use that graph to identify ready work, prioritize it, and dispatch the people or
agents able to complete it.

This adopted sketch connects the knowledge graph to execution. Software development is the first
application; the proposed scheduling structure can also represent other work with explicit
dependencies and completion evidence.

The
[architecture](../../design/architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)
carries the high-level commitment. This node supplies the reasoning and candidate mechanics.

## Dependencies and completion

The graph already represents blockers through `blocked_by`. The scheduler would read active outcomes
as requested work, rather than requiring a separate manually maintained task queue.

An outcome such as shipping a feature might depend on implementation and documentation.
Implementation might in turn depend on resolving an authentication defect. Work begins with the
dependencies that can proceed.

[[dcr-593d3d1]](../decisions/dcr-593d3d1-ideas-are-transitional.md) defines which node states clear
a blocker. Decisions, ideas, bugs, and other outcomes can all block work. A blocker becoming
terminal removes the mechanical block; the parent may still need reassessment when a prerequisite
was abandoned rather than fulfilled.

The current graph stores status changes in commits. Completion of code work and its outcome update
can therefore be integrated together. Candidate conveniences such as `closes`, reverse `blocks`
edges, and completion events are not all part of the current schema. Any automation they enable must
preserve the same integration path.

## Priorities and ready work

Active root outcomes carry the requested priority. A dependency inherits the highest priority of the
roots that need it.

The **frontier** is the set of unfinished outcomes whose blockers have cleared. The proposed
scheduler selects from that set, favoring work that advances the highest-priority roots.

Critical-path scheduling would also need enough information about dependencies and task duration to
identify which work delays completion. Priority propagation alone does not provide a duration
estimate.

| Concept            | Meaning                                                                |
| ------------------ | ---------------------------------------------------------------------- |
| Root outcome       | A requested result from which demand and priority begin                |
| Effective priority | The highest priority inherited from a root that needs the outcome      |
| Frontier           | Unfinished outcomes with no remaining mechanical blockers              |
| Critical path      | A dependency chain determining the earliest completion of a root       |
| Completion         | An accepted state change recording that the outcome's criteria are met |

## Dispatch and reconciliation

A scheduler controller observes node changes and recomputes readiness and priority.
[[ida-b48bded]](ida-b48bded-production-dags-and-events.md) refines this into continuous
reconciliation: events prompt a refresh, while current graph and run state determine what work
remains.

Dispatch must be idempotent per outcome. Repeated observations must not create several runs for the
same claimed work.

The sketch proposes recording the claim and run association on the outcome before execution begins.
Only unclaimed, open frontier work would be eligible. The current status spelling is `in-progress`;
run association, leases, and an atomic claim protocol still need a concrete schema and
implementation.

A dead run must eventually release its claim or be detected as stale.
[[ida-3145b7a]](ida-3145b7a-demand-pressure.md) explores leases, redispatch, and escalation.

Budgets and loop limits must bound repeated attempts.
[[bd-5cb2034]](../bugs/bd-5cb2034-unbounded-loops-under-demand-pressure.md) examines cumulative
limits, and [[bd-c4d069b]](../bugs/bd-c4d069b-git-churn-from-task-state.md) examines the cost of
frequent coordination writes to Git. The graph makes readiness queryable; it does not by itself
solve those operational problems.

## Human attention

Scheduling answers what work should proceed next. Attention routing also asks which completed work,
failures, and reports a person should see.

A human decision that blocks an outcome can be scheduled as a request for attention, as proposed in
[[ida-3145b7a]](ida-3145b7a-demand-pressure.md). The broader notification and review problem remains
open in [openquestions.md](../../design/openquestions.md).

## Outcomes and bugs

An outcome is a unit of work with completion criteria. A bug records a problem that may motivate one
or several outcomes.

Their lifecycles remain distinct. Completing one repair outcome should not automatically close a bug
if other required repair work remains.

The sketch proposes explicit relationships such as `motivates` and `motivated_by`. A controller
could use them to propose closing a bug when all required outcomes are complete. Those edges and
that behavior still need implementation; status edits must pass through integration.

This preserves the distinction between explaining a problem and scheduling the work needed to
address it.

## Broader use

The scheduler's dependency traversal does not require a code artifact. Booking tickets after a
budget is approved has a similar dependency structure to implementing a feature after a design
decision.

Each domain still needs suitable permissions, completion evidence, and handling of changing
circumstances. The structure can be reused without assuming those details are identical.

[[ida-8482624]](ida-8482624-federation-groups.md) describes the valley and federation context.
[[dcr-2113c52]](../decisions/dcr-2113c52-pr1-relanding.md) records this sketch's place in the
documentation, and [user-scenarios.md](../../design/user-scenarios.md) determines when detailed
scheduling work becomes a priority.
