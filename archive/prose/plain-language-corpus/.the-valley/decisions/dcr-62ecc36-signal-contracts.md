---
type: decision
id: dcr-62ecc36
status: decided
title: "Events, metrics, and logs have different retention rules"
created: 2026-08-05
source: design conversation, 2026-08-05
---

# Events, metrics, and logs have different retention rules

Domain events must be durable and replayable. Metrics may lose samples. Logs must be retained
according to declared policy. These three kinds of signal use the valley's infrastructure, with
different guarantees about what may be lost.

## Domain events

Domain events use the durable JetStream stream and the contracts in
[schema/events.cue](../../schema/events.cue). Repository events can be reconstructed from Git and
replayed deterministically. The bus is a projection of that history, as described in the
[architecture](../../design/architecture.md#bet-git-as-event-source--a-log-not-a-workflow-engine).

Durability, schema validation, and replay are requirements for this class.

## Metrics

Metrics include rates, latency, error counts, and resource use. The decided trial sends them on
ordinary NATS subjects without persistence. Losing a sample is acceptable. Metrics must not consume
the durable event stream's bounded storage.

Revisit the transport if metrics enter durable streams accidentally, the subject conventions become
difficult to maintain, or the transport performs poorly. The distinction between durable events and
lossy metrics remains useful regardless of transport.

## Logs

Policy must declare which logs are retained, their fidelity, and their retention period. Different
kinds of log can have different defaults. Small records with continuing value may be kept
indefinitely; large records may need shorter retention.

This requires retention machinery as well as a policy. Existing journal rotation settings and
indefinitely stored run trajectories do not by themselves provide a common declared policy.

The default policy should evolve from observed use of the data. Changes to that policy are governed
configuration changes, including when the system proposes them through the observation loop in
[[ida-a006b02]](../ideas/ida-a006b02-ooda-over-neural-nodes.md).
[self-transparency.md](../../design/self-transparency.md) describes the purpose of that loop.

The existing `#Backup.retention` declaration in [schema/valley.cue](../../schema/valley.cue)
provides a model for expressing retention rules and defaults. The warehouse, query interface, and
log transport remain open. [[ida-48c8868]](../ideas/ida-48c8868-stores-beyond-git.md) and
[[ida-b9f646c]](../ideas/ida-b9f646c-nix-backend-not-substrate.md) explain why these requirements
need not select one storage or deployment backend.

## Keeping useful findings

Raw signals can expire while findings derived from them remain available. A detector can record a
useful finding as a durable event. A finding that should inform future work can become a
knowledge-graph node.

This is how the system retains operational knowledge without retaining every sample forever. The
shared history required by [S4](../../design/user-scenarios.md#the-rest-of-the-ladder) depends on
durable events and recorded findings.

## Deployment and authorization

The design defines the guarantees. The valley and host configuration select collectors, storage, and
other implementation details.

Bus authentication must distinguish the writers of domain events from the writers of metrics. Hook
and integrator identities write domain subjects; machine services write metric subjects. This needs
to be considered when resolving [[bd-d853d9c]](../bugs/bd-d853d9c-bus-unauthenticated.md).

## Open questions

- What default retention periods and fidelity levels suit each kind of log?
- What evidence should drive changes to retention policy?
- Which retained signals need offsite backup?
- Which warehouse and query interface should be used?
- Should logs pass through NATS or use another transport, such as a journal collector?
- What metric volume can the trial transport sustain on the pilot host?

[[ida-b48bded]](../ideas/ida-b48bded-production-dags-and-events.md) describes the event model.
[[ida-b42d112]](../ideas/ida-b42d112-harness-log-is-normalized.md) covers normalized agent-run logs,
and [[ida-f16bd96]](../ideas/ida-f16bd96-the-system-observes-itself.md) describes using these
signals to improve the system itself.
