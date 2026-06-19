# Open questions

Consolidated from across the design docs. Grouped by theme. Each item retains an origin pointer to the doc where it was raised.

## Identity & trust bootstrapping

- **Agent identity.** When an agent (klaus-dispatched or otherwise) is the contributor, what key signs the commit and attestation? Ephemeral per-run, long-lived per-agent, or delegated from a human signer? The architecture supports any; a choice has to be made. *Origin: [scenarios.md](./scenarios.md), [contribute.md](./contribute.md).*
- **Bootstrapping trust for new contributors.** A new contributor has no trust score. Do they get one by default, or do they require gating until $N attestations land cleanly? Probably the latter; the state machine needs concrete rules. *Origin: [verification.md](./verification.md), [scenarios.md](./scenarios.md).*
- **Integrator self-integration.** The integrator is itself code in a repo. How does that code get integrated? Likely with a stricter policy on the integrator's own repo (always require human approval), but the chicken-and-egg deserves explicit handling. *Origin: [integration.md](./integration.md).*
- **Attestation tool distribution.** The attestation tool is a Nix derivation. How is its canonical hash published and pinned? Probably as part of an `armstrong`-shaped flake, but bootstrapping is worth thinking through. *Origin: [verification.md](./verification.md).*

## Policy & configuration

- **Policy bootstrap.** Someone has to be able to land the first principle / first policy change before the policy exists to govern principle changes. v1: integrator-key-holders land changes to `.the-valley/principles/` directly with relaxed policy. Worked out in a follow-up. *Origin: [integrator-internals.md](./integrator-internals.md).*
- **Per-repo integrator configuration.** Where does the integrator's per-repo configuration live — which protected refs, which trust thresholds per ref, which witnesses to wait for? Probably as a `config` node in the knowledge graph, queried alongside principles. *Origin: [integrator-internals.md](./integrator-internals.md).*
- **Per-repo check-set evolution.** When `flake.nix` adds a new required check, do existing attestations become invalid? Answer per [integrator-internals.md](./integrator-internals.md): yes, stale via policy-required-check. But what about graceful introduction periods? *Origin: [contribute.md](./contribute.md).*
- **Recursive self-transparency invariant (unresolved, consolidated).** Several items here and elsewhere look like facets of one candidate top-level invariant: *no actor can durably change the system, or an output of the system, without transparency* — recursive all the way down, so the system's own policy, controllers, and config are themselves outcomes governed exactly like code (the-valley builds the-valley). The facets: integrator self-integration (*Identity & trust*), policy bootstrap and per-repo integrator config as a `config` node (both above), and principles being load-bearing on integration ([knowledge.md](./knowledge.md)). [self-transparency.md](./self-transparency.md) names the invariant and collects them, but it is explicitly a **DRAFT** — statement, mechanism, and base case are all undesigned. *Origin: [self-transparency.md](./self-transparency.md); facets from [integration.md](./integration.md), [integrator-internals.md](./integrator-internals.md), [knowledge.md](./knowledge.md).*

## Cross-repo coordination

[federation.md](./federation.md) reframes these: the group/instance boundary splits each into an **intra-group** case (within one instance — one bus, one integrator, one knowledge graph; the tractable near-term problem) and an **inter-group** case (federation across instances; the harder, later problem).

- **Cross-repo integration.** Two requests in two repos that must succeed together (schema producer + consumer). *Intra-group:* one integrator, shared bus, a wrapper controller conditions B on A. *Inter-group:* events must cross an instance boundary. *Origin: [integration.md](./integration.md), [integrator-internals.md](./integrator-internals.md); framed in [federation.md](./federation.md).*
- **Cross-repo feedback.** A change in repo A breaks a consumer in repo B. *Intra-group:* both on the same bus, routing is local. *Inter-group:* the breakage event must federate to A's instance. *Origin: [feedback.md](./feedback.md); framed in [federation.md](./federation.md).*
- **Cross-repo nodes in the knowledge graph.** A bug in repo A blocking work in repo B. *Intra-group:* same graph, IDs namespaced by repo. *Inter-group:* the blocking node lives in another group's graph. *Origin: [knowledge.md](./knowledge.md); framed in [federation.md](./federation.md).*
- **Federated identity mapping & cross-group trust translation.** Trust scores are strictly group-scoped, but a group may import another group's attestations. An imported attestation carries a foreign signer fingerprint and a foreign confirm-rate that mean nothing under local policy. How are external identities verified and mapped to local trust — a translation function, a per-source discount factor, an explicit identity-linking node, or no automatic credit at all? *Inter-group only.* *Origin: [federation.md](./federation.md).*

## Attention, routing, and threads

- ~~**Priority layer architecture (work scheduling).**~~ *Resolved in [outcomes.md](./outcomes.md):* the dependency DAG already latent in the knowledge graph (`blocked_by`/`blocks` + `closes`), read generatively, is the scheduler. Root outcomes carry priority that propagates to their ancestors-of-completion; a klaus-shaped scheduler controller dispatches agents against the unblocked frontier on the critical path to the highest-priority root. This was one half of the original single "priority layer" question; the other half (attention routing, below) remains open.
- **Priority layer architecture (attention routing).** Which finished or blocked outcomes a human must *see*, and how urgently — per-subscriber rule sets, learned priorities, hand-curated digests, escalation chains? The firehose problem; distinct from work scheduling, which the outcome DAG now answers. Probably starts hand-configured and grows. *Origin: [feedback.md](./feedback.md), [outcomes.md](./outcomes.md), README.*
- **Thread identity and naming.** UUID? Human-readable slug? Tied to a commit, a chain, a topic? *Origin: [feedback.md](./feedback.md).*
- **When does a thread close?** Auto-close on deploy-stable? Explicit close events? Both? *Origin: [feedback.md](./feedback.md).*
- **Feedback back to an agent that finished its run.** Probably: events spawn new agent runs scoped to acting on them. klaus-shaped — the same shape the [outcomes.md](./outcomes.md) scheduler controller uses to dispatch against the frontier. *Origin: [feedback.md](./feedback.md), [outcomes.md](./outcomes.md).*
- **Backpressure visibility on the integrator.** Contributors should see queue depth and estimated wait. Easy to expose via the same `request-state` query; not strictly v1. *Origin: [integrator-internals.md](./integrator-internals.md).*

## Storage, retention, and evolution

- **Attestation expiry.** If inputs referenced by an attestation get garbage-collected from a Nix cache, re-derivation breaks. What's the retention promise on the binary cache? *Origin: [contribute.md](./contribute.md), [verification.md](./verification.md) (as "replay timeline").*
- **Stale-request expiry.** A request that sits stale forever clutters the namespace. Probably a periodic controller emits `request-abandoned` after $T of staleness with no owner action. *Origin: [integrator-internals.md](./integrator-internals.md).*
- **Schema evolution (events).** CUE handles validation, but event schemas will change. Migration story? *Origin: README.*
- **Schema evolution (knowledge graph).** Per-type schemas will change. Old nodes remain valid against their original version; agents produce against the latest. Migration tooling deferred. *Origin: [knowledge.md](./knowledge.md).*

## Knowledge graph specifics

- **Initial node-type set.** Probably `outcome`, `bug`, `principle`, `decision`, `idea`, `thread` for v1. *Decision flipped:* `outcome` (formerly `task`) does **not** collapse into `bug` — the outcome is the central generative unit of work that the [outcomes.md](./outcomes.md) scheduler runs on, and `bug` is merely one kind of problem that motivates an outcome. Keeping them distinct is load-bearing for the production DAG. `retrospective` may not be needed yet. *Origin: [knowledge.md](./knowledge.md), [outcomes.md](./outcomes.md).*
- **Hash-based vs incrementing IDs.** Hash-based is coordination-free but uglier; incrementing is human-friendly but needs coordination. Probably hash-based for v1, with an alias system for human-readable names later. *Origin: [knowledge.md](./knowledge.md).*
- **Bus event granularity for node mutations.** One event per node mutation, or batched per commit? Affects subscriber complexity. *Origin: [knowledge.md](./knowledge.md).*
- **Indexer cadence.** On every push, on-demand, subscriber-driven? The cost is small; freshness expectations drive the choice. *Origin: [knowledge.md](./knowledge.md).*

## Verification specifics

- **Effectful test catalogue.** Which classes of effectful test can be lifted to `nixosTest` or microVM-sealed environments? Maintaining a list — moving a test from effectful to pure is a meaningful security improvement. *Origin: [verification.md](./verification.md).*
- **One attestation per commit, or multiple.** The current shape enforces one (refs are create-only). Multi-signature attestations could change this. A namespace like `refs/the-valley/attestations/<commit-sha>/<signer-id>` would allow multiple, at the cost of more complex lookup. *Origin: [contribute.md](./contribute.md).*

## Workflow patterns

- **Dependent changes / stacks.** When B depends on A, the integrator needs to know to integrate them as a unit or in order. Stacked-diff tooling exists; how does it surface in the request event? *Origin: [integration.md](./integration.md).*
- **Long-running topic branches.** A topic that integrates incrementally over time. Does each request consume one commit, a range, the whole branch? Probably the request specifies a commit range and the topic drifts naturally. *Origin: [integration.md](./integration.md).*

## Discovery

- **Discovery.** Without GitHub-the-social-graph, how do humans find each other's repos? Scoped by group/federation per [federation.md](./federation.md): *within* a group, discovery is trivial (the instance knows its own repos); *across* groups it is the inter-group problem — how one instance learns another exists and what it federates. Shape open; the framing is that discovery lives at the federation layer, not the repo layer. *Origin: README; framed in [federation.md](./federation.md).*

## Resolved (kept for the record)

- ~~**Signaling: bus event vs. request ref vs. branch convention.**~~ *Resolved in [contribute.md](./contribute.md):* the contributor pushes `refs/the-valley/integration-requests/<name>` atomically with the topic branch and attestation; the bare repo's post-receive hook is the canonical projection of those ref updates into bus events.
- ~~**The log is a single point of failure.**~~ *Reframed:* per-repo events (refs, attestations, integration requests) are durable in git itself and externally tamper-evident via the Tessera-backed tlog. The bus is only the source of truth for ephemeral cross-system events (deploys, metrics) and can be replicated lazily when needed.
