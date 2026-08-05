---
type: idea
id: ida-f16bd96
status: exploring
title: The system observes itself
created: 2026-08-05
source: design conversation, 2026-08-05
---

# The system observes itself

The system has state-of-the-art observability as a goal: metrics, error detection, and runtime
insights — detected by the system itself, reported, and acted on. Not best-effort logging a human
reads after the fact; the bar is that the system notices before a human does.

## The skeleton already exists

The design holds the pieces this goal raises the bar on:

- **The scenario.** [S4](../../design/user-scenarios.md#the-rest-of-the-ladder): consequences just
  happen, and "why did X occur" is answerable from one history.
- **The acting half.** Reactive controllers
  ([roadmap Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller))
  act on events; feedback and incident memory
  ([roadmap Phase 7](../../design/roadmap.md#phase-7--feedback--incident-memory)) remembers them.
- **The remembering half, already in practice.** Incidents become bug nodes in the graph —
  operational friction has produced `bd-` nodes directly — and [[ida-b48bded]]
  ([ida-b48bded-production-dags-and-events.md](./ida-b48bded-production-dags-and-events.md)) frames
  an incident as an event invalidating the observed satisfaction of a root.

## What the goal adds

1. **Detection, not discovery.** Failures currently surface when a human looks. The goal inverts
   that: errors and anomalies are detected by the system and reported unprompted. Operational
   history shows the cost of the current state — a broken build can sit for hours before anyone
   notices.
2. **Metrics are a named signal class.** Rates, latencies, error counts, resource use. Their
   placement follows an existing carve-out: [architecture.md](../../design/architecture.md)
   designates an ephemeral cross-system event class for which the bus is the source of truth, and
   metrics belong to it — so metrics arrive without violating the rule that durable events are a
   projection of git.
3. **The loop closes.** Detected errors and insights feed decisions — the same observe, orient,
   decide, act shape as the neural-node performance loop ([[ida-a006b02]]
   ([ida-a006b02-ooda-over-neural-nodes.md](./ida-a006b02-ooda-over-neural-nodes.md))). That loop is
   one instance of this goal; this node states the general one, which covers deterministic machinery
   as well: convergence latency, mirror pushes, backup runs, bus health.

## Unification, not a platform

State-of-the-art does not mean adopting a monitoring platform. The Minimal constraint
([requirements.md](../../design/requirements.md)) holds that small composed tools stay
understandable while platforms accrete, and the differentiating property here is unification:
insights are derived views over the one history, not a parallel telemetry silo with its own truth.
An observability stack imported wholesale would be a second platform inside a system built to escape
platforms.

## Constraints

- Anything that acts on a detection is an automated bus consumer, and [[bd-d853d9c]]
  ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md)) gates automated
  consumers on bus authentication. The acting half waits on that fix.
- The observability machinery is itself system output. The candidate invariant of
  [self-transparency.md](../../design/self-transparency.md) applies: the thing that watches the
  system is governed, versioned, and transparent like the system.

## Open

- What state-of-the-art means concretely — which signals, what latency of detection, whether
  anything SLO-shaped exists here — without importing a platform's vocabulary wholesale.
- Whether detected incidents open graph nodes automatically, and what distinguishes an incident
  worth a node from noise.
- What the system may act on autonomously versus what it may only report — actions with blast radius
  meet the same gates as any effect.
