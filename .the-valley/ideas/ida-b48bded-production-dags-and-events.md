---
type: idea
id: ida-b48bded
status: exploring
title: "Outcomes as production: can DAGs and events weave?"
created: 2026-07-16
source: owner design conversation
---

# Outcomes as production: can DAGs and events weave?

A central pillar of the architecture is an idea the owner likes and wants to test: most valuable outcomes can be modeled as *production* — the result of computing and satisfying a DAG. Builds are DAGs; configuring and standing up infra can be a DAG; implementing a feature can be a DAG. But DAGs and event-based systems have very different topologies and control flow. It is not obvious the SDLC can be modeled as a DAG, or that all of those examples are right. The question: are event-based and production-DAG architectures something we can naturally weave together, or not?

Current thinking (2026-07-16):

- **The examples split three ways.** Builds are a *static* DAG, known before execution. Feature work is a *dynamic* DAG — doing one node reveals the next, so the engine must suspend and resume rather than plan-then-execute ("Build Systems à la Carte" studies exactly this axis). Infra is the weak example: this stack already voted against infra-as-DAG once, banning Terraform's plan-DAG in favor of k8s-style convergence (armstrong, cosmo).
- **A residue is not DAG-shaped.** Standing obligations have no terminal "satisfied" state (the roadmap already holds durability out of the phases as a standing priority), and incidents are interrupts — the world pushes, no root outcome demanded them. Production should claim demanded, terminating work and explicitly cede the rest.
- **The weave with precedent** (k8s, incremental build systems, Excel): events at the boundary, the DAG explicit in the middle, levels underneath. Events never drive work — they *invalidate* observed state. The knowledge graph holds the dependency structure, queryable at rest. A level-triggered reconciler compares demand against observation and pulls the frontier. The anti-pattern is reaction chains that encode the DAG implicitly in event wiring — the failure "a log, not a workflow engine" ([architecture.md](../../design/architecture.md)) guards against.
- **Cycles.** Review→revise→review is cyclic, but the artifact dependency structure is acyclic at any instant; versioning turns cycles into spirals — each iteration is a new node state, the same move changes-not-branches makes ([[ida-93e4f91]], [ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)).

Phase 5 (effectful reactions, [roadmap.md](../../design/roadmap.md)) is where this resolves in practice: reconciler-against-the-graph, or reaction chains. Deciding early is cheap; refactoring Phase 5 later is not.

Extends the outcome-DAG bet ([[ida-eac723e]], [ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md)).
