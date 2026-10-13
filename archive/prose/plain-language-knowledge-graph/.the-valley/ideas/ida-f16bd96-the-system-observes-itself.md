---
type: idea
id: ida-f16bd96
status: exploring
title: "Detect operational problems and use the results to improve the system"
created: 2026-08-05
source: design conversation, 2026-08-05
---

# Detect operational problems and use the results to improve the system

The-valley should detect and report operational failures without waiting for someone to inspect
logs. Relevant observations include convergence latency, mirror failures, backup results, bus
health, and the results of agent work.

The system should retain enough evidence to explain what happened and support an appropriate
response.

## Detection and records

[S4](../../design/user-scenarios.md#the-rest-of-the-ladder) requires consequences to be traceable
through a shared history. Detected incidents can contribute to that history as durable events and,
where useful, bug nodes.

[[dcr-62ecc36]](../decisions/dcr-62ecc36-signal-contracts.md) defines separate contracts for events,
metrics, and logs. Metrics may lose samples; durable findings and decisions have stronger retention
requirements.

[[ida-b48bded]](ida-b48bded-production-dags-and-events.md) describes an incident as evidence that an
outcome believed to be satisfied may need more work.

## Response

A detector can report a problem or initiate a permitted response. The policy must distinguish those
cases.

[[ida-a006b02]](ida-a006b02-ooda-over-neural-nodes.md) applies the same observation-and-adjustment
pattern to agent dispatch. The broader operational loop also covers deterministic services.

Reusable collectors, views, and controllers should support the
[minimality constraint](../../design/requirements.md). Project findings must remain connected to the
authoritative history, with the interface and storage choices serving that requirement.

## Constraints

Automated consumers acting on bus events require authenticated events and suitable permissions.
[[bd-d853d9c]](../bugs/bd-d853d9c-bus-unauthenticated.md) tracks that dependency.

Detection rules and responding controllers are system configuration. Changes to them must be
versioned and governed under [self-transparency.md](../../design/self-transparency.md).

## Open questions

Which failures should be detected first, and within what delay? What makes a detected incident worth
a graph node? How should noise and repeated reports be handled? Which responses can run
autonomously, and which require a person?

The [effectful-reactions work](../../design/roadmap.md#phase-5--effectful-reactions) and
[feedback work](../../design/roadmap.md#phase-7--feedback-and-incident-memory) provide the broader
implementation context.
