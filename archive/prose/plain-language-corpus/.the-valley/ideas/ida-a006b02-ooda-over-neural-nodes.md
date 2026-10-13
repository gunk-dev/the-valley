---
type: idea
id: ida-a006b02
status: exploring
title: "Use run results to improve task sizing and dispatch"
created: 2026-08-05
source: design conversation, 2026-08-05
---

# Use run results to improve task sizing and dispatch

The system should record the cost and results of work, compare similar runs, and use that evidence
to improve later dispatch. This applies to model runs and to human work that affects whether an
outcome can progress.

The proposed loop follows observation, interpretation, decision, and action: collect results,
compare them, choose dispatch conditions, and run the next task under those conditions.

## Compare conditions

A run has provenance rather than a persistent agent identity, as described in
[[ida-45178f6]](ida-45178f6-agent-identity-is-provenance.md). Comparisons should therefore group
runs by relevant conditions: model, effort, brief structure, and task kind.

Human review delay is another input to completion time.
[[ida-3145b7a]](ida-3145b7a-demand-pressure.md) describes how blocked human work affects scheduling.

## Connect records to outcomes

Run records can supply cost and execution settings. Integration events can report whether a change
landed or was rejected. The missing complete loop is the relationship among those records, the task
they served, and a meaningful measure of success.

The integrator's events provide useful inputs, but an integration success alone may not mean the
intended outcome was achieved.

Recorded provenance also supports offline comparisons in which selected inputs are varied.
[[ida-febcd97]](ida-febcd97-neural-node-conditions.md) identifies dispatch conditions that may
affect the result.

## Task sizing

An initial use would map a kind of task to a model, effort setting, and budget.

Two pieces of knowledge need distinct representations. A size estimate for one task is a prediction
to compare with its actual result. A vocabulary of task kinds supports learning a reusable sizing
rule across many tasks.

Dispatch against outcome nodes would provide a durable task identity for joining these records.
Existing coordinator habits, such as assigning smaller budgets to capture work and larger budgets to
builds, could supply an initial declared strategy.

## Governing the loop

The sizing policy is system configuration. Changes to it must use the same governed path as other
configuration, as described in [self-transparency.md](../../design/self-transparency.md).

The verification trust loop has a related structure: observations affect standing, which affects a
later decision. [[ida-5a59fef]](ida-5a59fef-one-plumbing-for-every-loop.md) explores sharing
machinery between these loops.

## Open questions

What counts as success: integration without rework, a change remaining useful over time, or
completion of the outcome? How can the system avoid optimizing a convenient measure while making the
actual work worse?

The location of sizing policy, task-kind vocabulary, and per-task estimates also remains undecided.
