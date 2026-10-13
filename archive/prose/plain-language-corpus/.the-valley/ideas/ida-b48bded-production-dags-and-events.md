---
type: idea
id: ida-b48bded
status: exploring
title: "Combine explicit work dependencies with continuous reconciliation"
created: 2026-07-16
source: design conversations, 2026-07-16/17
---

# Combine explicit work dependencies with continuous reconciliation

The project is exploring whether useful outcomes can be represented as dependency graphs whose
missing parts are discovered during work. A controller would keep comparing requested outcomes with
current state and schedule the work that can proceed.

The graph describes dependencies. Events prompt the controller to refresh its observations.
Reconciliation determines what work is still needed.

## Discovering the graph

A build executes a known set of instructions and dependencies. Development often has to discover or
create those instructions before the desired artifact can be produced.

The proposal treats that discovery as part of the graph. A task may add a missing implementation,
introduce a prerequisite, or refine the conditions for completion. Scheduling must therefore pause
and resume as the graph develops, rather than requiring a complete plan at the start.

The distinction studied in _Build Systems à la Carte_ is useful prior art for dependencies
discovered during execution.

## Human, model, and deterministic work

Deterministic tasks execute established procedures. Humans and models can supply missing judgments,
designs, and procedures. The graph can represent both.

An intended direction is to replace repeated judgment with reliable deterministic work where
experience makes that possible. [[ida-4557af7]](ida-4557af7-spec-driven-iteration.md) describes
specifications and conformance checks that could support that transition.

This is a model to test, not a claim that every task fits neatly into one category or can eventually
be automated.

## Events and current state

Events invalidate the controller's observed state and make it reconcile sooner. The dependency graph
remains queryable even when no event arrives.

The controller compares active demand with current completion and blockers, then selects the ready
work. [[ida-3145b7a]](ida-3145b7a-demand-pressure.md) describes that continuing demand. Encoding the
dependencies only in chains of event handlers would make them harder to inspect and recover.

The [architecture](../../design/architecture.md) separates the event log from workflow control for
this reason.

## Incidents and repeated revision

An incident can invalidate the belief that an outcome is satisfied. Reconciliation then identifies
the work needed to restore it.

Review and revision can repeat while each artifact version has an acyclic dependency graph.
[[ida-93e4f91]](ida-93e4f91-changes-not-branches.md) explores how successive versions remain
associated with one intended change.

Completion needs explicit acceptance criteria. Some are mechanically checkable; others still require
judgment.

## Standing obligations

A long-running service has requirements over time, rather than one final completed state. Existing
convergence systems such as cosmo and Armstrong already maintain such obligations.

How that continuing layer relates to terminating outcome work remains open. It may need no new
abstraction, but the relationship must be tested in the
[effectful-reactions phase](../../design/roadmap.md).

[[ida-eac723e]](ida-eac723e-outcome-dag.md) contains the adopted outcome-DAG sketch that this
proposal extends.
