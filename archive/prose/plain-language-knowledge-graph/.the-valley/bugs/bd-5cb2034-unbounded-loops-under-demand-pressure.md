---
type: bug
id: bd-5cb2034
status: open
title: "Automatic redispatch needs cumulative budgets and limits on decomposition"
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Automatic redispatch needs cumulative budgets and limits on decomposition

A scheduler that keeps redispatching unfinished work can spend resources indefinitely when a task
cannot succeed. A scheduler that also lets runs create further dependencies can keep expanding the
work beyond the original request.

This is a design risk in the continuing reconciliation proposed by
[[ida-3145b7a]](../ideas/ida-3145b7a-demand-pressure.md) and
[[ida-eac723e]](../ideas/ida-eac723e-outcome-dag.md). The autonomous outcome scheduler is not yet
implemented. The existing sketches mention budgets and loop limits, but do not define a complete
cumulative enforcement mechanism.

## Failure conditions

A run may fail because its task is unsatisfiable, its environment is broken, or its output
repeatedly fails verification. If lease expiry simply makes the same outcome eligible again, each
individually budgeted run can start another individually budgeted run. Per-run limits do not bound
their total cost.

Decomposition can create a similar problem. A run adds prerequisite outcomes, those outcomes become
eligible for dispatch, and later runs add more. Without limits or review, the graph can accumulate
redundant work or drift from the intended result.

These failures depend on the scheduler's retry and authorization rules. They are not inevitable
consequences of representing dependencies as a graph.

## Candidate controls

- Enforce a shared spending or execution budget across runs, with a defined stop and reset
  condition.
- Count attempts per outcome and use backoff. Repeated failure should stop automatic retries and
  request intervention.
- Limit or require approval for decomposition, using limits on depth, breadth, or cumulative work.
- Give persistently failing outcomes an explicit state that excludes them from automatic redispatch
  until reviewed.

Specific thresholds remain undecided. The limit must be enforced where dispatch or expenditure can
actually be refused;
[[ida-f1b39e8]](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) is a possible
part of that boundary.

## Work needed

Define how budgets apply across child outcomes, concurrent runs, retries, and controller restarts.
Define how stopped work becomes eligible again.

Then exercise repeated failure and recursive decomposition to show that the controller stops at the
declared limits and reports the reason.
[Feedback and incident memory](../../design/roadmap.md#phase-7--feedback-and-incident-memory) should
preserve what was learned from those failures.
