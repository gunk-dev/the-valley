---
type: decision
id: dcr-62ecc36
status: decided
title: "Telemetry rides three signal contracts: durable events, lossy metrics, logs retained under declared policy"
created: 2026-08-05
source: design conversation, 2026-08-05
---

# Three signal contracts

Telemetry on the instance's infrastructure is three signal classes, each with its own durability
contract: domain events are durable and replayable, metrics are lossy by design, and logs are
retained under declared policy. All three ride the instance's existing infrastructure; the classes
differ in what each is allowed to lose.
[architecture.md's event-log bet](../../design/architecture.md#bet-git-as-event-source--a-log-not-a-workflow-engine)
already holds that per-repo events are durable in git with the bus as a rebuildable projection, and
that only ephemeral cross-system events — naming metrics in passing — have the bus as their source
of truth. This node designs what that parenthetical left open, and adds a third class the
architecture does not name.

## Domain events — durable

Domain events are the class Phase 1 built: schema'd one event type at a time in
[schema/events.cue](../../schema/events.cue), carried on the bus's durable stream (JetStream),
deterministically replayable, and a projection of git. This decision leaves them unchanged. The
properties that make the bus trustworthy — durability, schemas, replay — belong to this class alone.

## Metrics — lossy by design, decided as a trial

Metrics are rates, latencies, error counts, and resource use. They ride plain subjects on the same
server, deliberately unpersisted: fire-and-forget, no stream, no pressure on the bus's bounded
store. Loss of a sample is acceptable, and that acceptance is what distinguishes the class.

This element is decided as a trial. The revisit condition: if the separation fails in practice —
metrics leaking into durable streams, the vocabulary discipline eroding, or the transport proving
wrong — the transport is revisited. The class distinctions stand regardless of the transport's fate.

## Logs — retention is a capability, governed by declared policy

Retention is a capability of the system: ingestion and warehousing exist as machinery, and what is
retained, at what fidelity, for how long, is declared policy — never an implicit property of
whatever a collector happens to keep. Resources are bounded, and observability data's value decays
for the loop that consumes it — the observe-orient-decide-act loop the system runs over its own
signals ([[ida-a006b02]]
([ida-a006b02-ooda-over-neural-nodes.md](../ideas/ida-a006b02-ooda-over-neural-nodes.md))). A
default retention strategy ships, and it evolves as use of the data drives efficiencies: the
retention policy is itself an output the loop tunes, governed like any configuration
([self-transparency.md](../../design/self-transparency.md)).

The declared-tiers shape already exists in this schema: `#Backup.retention` in
[schema/valley.cue](../../schema/valley.cue) declares keep-counts as policy with defaults. The same
move extends to signals.

Species differ and carry distinct defaults: some signals are already kept indefinitely at
negligible cost, others are bulky with fast-decaying value. Policy is per species, not one number.

The warehouse technology, the query interface, and whether logs transit the bus at all or ship by
other means are deliberately open: the decision is that retention is declared, not which machinery
enforces it. That openness is the design's storage-agnosticism applied to logs — durability
attaches to project state, not to any one store ([[ida-48c8868]]
([ida-48c8868-stores-beyond-git.md](../ideas/ida-48c8868-stores-beyond-git.md))), and an
implementation is a backend ([[ida-b9f646c]]
([ida-b9f646c-nix-backend-not-substrate.md](../ideas/ida-b9f646c-nix-backend-not-substrate.md))).

No declared retention policy exists today, so what is kept is an accident of each collector's
machine configuration rather than a decision. Journals on the pilot host rotate under default caps;
run trajectories persist indefinitely; neither is the outcome of declared policy.
Retention-by-accident is the gap this decision closes.

## The promotion ladder

Promotion is how value escapes decay: raw signal is retained under the default policy and expires;
what a detection distills is promoted into a durable, schema'd insight event; what warrants
permanent memory becomes a graph node. The decay gradient and the promotion ladder are the same
structure read in two directions. Raw metrics and logs are sensing, not history. The one-history
property (S4, [user-scenarios.md](../../design/user-scenarios.md#the-rest-of-the-ladder)) is
carried by promoted insights and the durable event log, never by the firehose.

## The split of concerns

The design states the properties; the instance supplies the machinery. This is the division the
backup policy already draws: `#Backup` in the host schema declares the durability policy, and the
installer supplies machine integration. Which warehouse, which collector, which retention storage —
instance and machine concerns.

## The authentication consequence

[[bd-d853d9c]] ([bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md))
gates automated consumers on bus authentication. That fix should carve the subject namespace with
the class split in mind from the start: domain subjects writable only by the hook and integrator
identities, metrics subjects writable by machine services. Encoding the two write-authorities in
subject-level authorization is cheaper now than re-cutting the namespace later.

## Related

- [[ida-b48bded]]
  ([ida-b48bded-production-dags-and-events.md](../ideas/ida-b48bded-production-dags-and-events.md))
  — the event frame these contracts ride on.
- [[ida-b42d112]]
  ([ida-b42d112-harness-log-is-normalized.md](../ideas/ida-b42d112-harness-log-is-normalized.md)) —
  a harness's log of an agent run is normalized; normalization is what makes cross-node log
  retention usable.
- [requirements.md's Minimal constraint](../../design/requirements.md#constraints) — one server
  carrying three contracts instead of three systems.
- The pending idea "The system observes itself" on branch `idea/the-system-observes-itself` — the
  goal these contracts serve.

## Open

- The default retention strategy's actual tiers, per species.
- What usage signal drives the retention policy's evolution.
- Whether the offsite depth that protects git data extends to retained signals, and for which
  species.
- The warehouse: technology and query interface.
- Whether logs transit the bus or ship journald-native.
- What volume the trial's plain subjects sustain on the pilot host before the transport question
  reopens.
