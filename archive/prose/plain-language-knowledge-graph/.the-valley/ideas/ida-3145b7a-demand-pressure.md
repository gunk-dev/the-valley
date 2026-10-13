---
type: idea
id: ida-3145b7a
status: exploring
title: "Keep requested outcomes moving and surface stalled work"
created: 2026-07-02
source: design conversation, 2026-07-02
---

# Keep requested outcomes moving and surface stalled work

A scheduler could keep working toward requested outcomes until they are completed, abandoned, or
blocked on a decision it cannot make. It would dispatch capable AI agents and direct human attention
to work that requires a person. This continuing reconciliation is the idea called **demand
pressure**.

The proposal extends the adopted outcome-DAG sketch in [[ida-eac723e]](ida-eac723e-outcome-dag.md).
It remains exploratory. The [outcomes directory](../outcomes/) provides a small dependency graph for
examining it; that graph is not evidence that an automated scheduler exists.

## Humans and agents

Both humans and AI agents can be producers of work in the graph. Dispatch differs: an AI run can be
started, while a human receives a request for attention.

This lets scheduling cover a human decision that blocks an outcome. It does not settle the broader
attention problem of which completed work, incidents, and reports a person should see. That remains
a separate question.

## Reconcile outstanding work

Demand follows dependencies from active root outcomes. The controller repeatedly compares that
demand with current progress.

Events tell the controller that its view may be stale. It then reads current state and decides what
work is needed, following [[ida-b48bded]](ida-b48bded-production-dags-and-events.md). A missed or
repeated event should not determine whether an outcome eventually receives attention.

## Recognizing stalls

| Condition                                       | Evidence to look for                                      |
| ----------------------------------------------- | --------------------------------------------------------- |
| A run died                                      | An outcome is in progress without a live run              |
| A human has not responded                       | A human-assigned item remains ready past a threshold      |
| A dependency no longer supplies what was needed | A blocker was abandoned or its premise changed            |
| Demand disappeared                              | No active root still depends on the outcome               |
| A proposal repeatedly fails to land             | Successive attempts for the same change fail verification |

Repeated proposals need a stable identity. For example, four nightly attempts at the same dependency
update should be recognizable as four failures of one intended change. Treating each generated
branch as unrelated work would hide the recurrence.
[[ida-93e4f91]](ida-93e4f91-changes-not-branches.md) explores the required change identity.

## Possible responses

A lease could associate an in-progress outcome with a live run. If the lease expires, the controller
could make the outcome available again.

Repeated failure could trigger escalation to another actor, including a human. An unanswered human
request might be redirected where policy permits. A counter and threshold for one repeatedly failing
proposal can be useful before the broader attention-routing design is settled.

Outcomes no longer needed by an active root would stop receiving automatic work. They could then be
reviewed for archival or removal from the active graph.

These responses need limits.
[[bd-5cb2034]](../bugs/bd-5cb2034-unbounded-loops-under-demand-pressure.md) examines retry and
decomposition budgets, and [[bd-c4d069b]](../bugs/bd-c4d069b-git-churn-from-task-state.md) examines
the cost of storing frequent coordination updates in Git.

## Relationship to build scheduling

The dependency traversal resembles a build system: request a result, reuse completed prerequisites,
and schedule the remaining work. Completed outcomes play a role similar to cached build results.

The analogy helps explain scheduling, but an outcome may depend on judgment, changed circumstances,
or mutable state. The controller needs explicit completion and invalidation rules for those cases.

The [architecture](../../design/architecture.md#work-from-outcomes-and-dependencies) records the
high-level commitment. [[oc-f3bcfd0]](../outcomes/oc-f3bcfd0-s1-holds.md) and
[[oc-fc348f0]](../outcomes/oc-fc348f0-hetzner-mechanism.md) provide the initial examples, including
work deliberately blocked on a human choice.
