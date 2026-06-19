# Federation — group, instance, federation

The design docs so far stop at the repo. But a repo is not the largest unit that matters: a team has many repos, a company has many teams, and changes, trust, and feedback cross those boundaries. This document introduces the organizational layer **above** the repo and the distribution model that goes with it.

## The basic unit is the group

A **group** is whatever organization owns a set of repos and shares one trust domain — a team, a company, a personal namespace, a working group. The group, not the repo, is the basic unit of federation.

Each group has exactly one **instance**: a 1-to-1 binding. A group's instance runs, for that group:

- the **bus** (NATS JetStream) carrying its cross-system events,
- the **integrator** (one controller, many per-repo policies),
- **git hosting** for the group's repos (bare git over SSH, as in [contribute.md](./contribute.md)).

Everything in the existing docs — the event log, the integrator's queues, the knowledge graph, trust scores — lives *inside* one instance. "The bus" and "the integrator" in the other docs mean *this group's* bus and integrator.

```
  group: acme-platform
  └── instance  (1-to-1)
        ├── bus            (NATS JetStream)
        ├── integrator     (per-repo policies)
        ├── git hosting    (bare repos over SSH)
        └── knowledge graph + trust scores   ← scoped to this group
```

## One machine or many — same design

An instance is **instantiable on a single machine**: NATS, the integrator, and a handful of bare repos fit on one Tailscale-reachable box, which is exactly the solo-dev and small-team case the rest of the design targets.

The same instance is **runnable as a distributed system**: NATS JetStream clusters, the integrator scales to one controller per protected ref (queues are already independent per ref — see [integrator-internals.md](./integrator-internals.md)), git hosting shards across hosts. Nothing in the architecture assumes co-location; the controller-and-log pattern was distributed-friendly from the start. The design scales **down and up** without changing shape — the difference between a hobby instance and a company instance is deployment, not design.

## Intra-group vs inter-group

The "cross-repo" open questions were really two questions wearing one name. The group/instance boundary separates them:

| Question | Intra-group (within one instance) | Inter-group (federation across instances) |
| --- | --- | --- |
| Integration | Two requests in two repos that must succeed together — one integrator, shared bus, a wrapper controller conditions one on the other | Producer and consumer in *different* groups — needs events to cross an instance boundary |
| Feedback | A change in repo A breaks consumer B; both on the same bus, routing is local | B is in another group; the breakage event must federate to A's instance |
| Knowledge nodes | A bug in repo A blocks work in repo B; same graph, IDs need a repo namespace | The blocking node lives in another group's graph entirely |

**Intra-group** is the tractable near-term case: one bus, one integrator, one knowledge graph, IDs namespaced by repo. Most of what the existing docs defer as "cross-repo, v2" is really intra-group and becomes tractable once the group is the frame.

**Inter-group** is genuine federation: events, trust, and node references crossing between independent instances. This is the harder, later problem, and federation is the layer that governs it.

## Trust is group-scoped; federation governs the boundary

Trust scores ([verification.md](./verification.md)) are **scoped within a group**. A signer's confirm rate is meaningful relative to one group's re-verifiers and one group's policy; it does not automatically mean anything in another group.

Federation governs how trust and events cross group boundaries — explicitly, never implicitly:

- A group may **import** another group's attestations and decide, by its own policy, how much they count.
- A group may **subscribe** to a subset of another group's events (e.g. "schema-updated on acme/contracts") rather than its whole bus.
- Crossing the boundary is a policy decision on *both* sides, expressed the same way every other policy is — as nodes the integrator and routers query.

This keeps the permissive, low-ceremony intra-group model (anyone with push access can request integration — see [scenarios.md](./scenarios.md), Scenario 5) without leaking that permissiveness across organizational lines.

## Discovery is scoped by group and federation

"Without GitHub-the-social-graph, how do humans find each other's repos?" is a **federation** question. Within a group, discovery is trivial — the instance knows its own repos. Across groups, discovery is the inter-group problem: how one instance learns another exists and what it federates. The shape is open; the framing is that discovery operates at the federation layer, not the repo layer. See [openquestions.md](./openquestions.md).

## Open questions

See [openquestions.md](./openquestions.md) — items raised here live under *Cross-repo coordination* (reframed as intra-group vs inter-group) and *Discovery* (scoped by group/federation).
