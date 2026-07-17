---
type: idea
id: ida-b48bded
status: exploring
title: "Outcomes as production: can DAGs and events weave?"
created: 2026-07-16
source: design conversations, 2026-07-16/17
---

# Outcomes as production: can DAGs and events weave?

A central pillar of the architecture is a bet to be tested: most valuable outcomes can be modeled as
_production_ — the result of computing and satisfying a DAG. But DAGs and event-based systems have
very different topologies and control flow. The question: can the two be woven naturally, or not?

## What about the SDLC is DAG-shaped

An existing build target has an a priori DAG: a transitive closure of inputs that produce the thing
that meets some behavior requirement. Development is the process of manifesting the DAG of a root
node in the future whose spec is yet to be satisfied. A build is a completed development — the same
DAG seen after the fact. The development process does not look like a DAG while it runs because the
missing structure is not known yet, not because the work has a different shape; the engine must
suspend and resume as structure appears rather than plan-then-execute ("Build Systems à la Carte"
studies exactly this axis).

Two kinds of compute do the manifesting. Deterministic compute executes code that exists. Neural
compute — human or LLM agents — manifests the missing deterministic instructions in the stack. While
the root node does not satisfy its requirements, neural nodes produce the elements needed to
deterministically produce a satisfying artifact. The formal signature: deterministic nodes are
graph-preserving (they execute known edges); neural nodes are graph-extending (their output is new
graph structure). A neural node's end goal is to put itself out of business, automating as much as
it can with deterministic nodes — the graduation policy ([[dcr-74c3158]],
[dcr-74c3158-valley-cli-lifecycle.md](../decisions/dcr-74c3158-valley-cli-lifecycle.md)) generalized
to all neural work.

Pressure ([[ida-3145b7a]], [ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md)) is
the engine: the system always trying to unblock stalls in the realization of the future DAG. The
root's behavior requirement is what makes "not yet satisfied" decidable, so pressure has a gradient;
the conformance-suite move ([[ida-4557af7]],
[ida-4557af7-spec-driven-iteration.md](./ida-4557af7-spec-driven-iteration.md)) is the same
requirement seen from the spec side.

## The weave

Events at the boundary, the DAG explicit in the middle, levels underneath — the shape k8s,
incremental build systems, and Excel share. Events never drive work; they _invalidate_ observed
state. The knowledge graph holds the dependency structure, queryable at rest, and a level-triggered
reconciler compares demand against observation and pulls the frontier. The anti-pattern is reaction
chains that encode the DAG implicitly in event wiring — the failure "a log, not a workflow engine"
([architecture.md](../../design/architecture.md)) guards against.

Incidents fit inside the model: an incident is an event invalidating the observed satisfaction of an
already-satisfied root, and pressure resumes. Cycles fit too: review→revise→review is cyclic, but
the artifact dependency structure is acyclic at any instant — versioning turns cycles into spirals,
the same move changes-not-branches makes ([[ida-93e4f91]],
[ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)).

## The residue, undecided

Standing obligations — infrastructure, long-lived services, timers — have no terminal "satisfied"
state; their specs quantify over time. Production claims demanded, terminating work; the standing
layer is delegated to level-triggered convergence, which this stack already runs (armstrong, cosmo).
Perhaps no innovation is needed there — undecided. The Terraform ban does not settle it: the ban
indicts statefulness and the configuration language, not DAGishness, and a reconciler might still
need to compute all the dependencies of, for example, a service to cleanly instantiate it.

Phase 5 (effectful reactions, [roadmap.md](../../design/roadmap.md)) is where this resolves in
practice: reconciler-against-the-graph, or reaction chains. Deciding early is cheap; refactoring
Phase 5 later is not.

Extends the outcome-DAG bet ([[ida-eac723e]],
[ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md)).
