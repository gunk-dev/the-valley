# Open questions

Consolidated from across the design docs. Grouped by theme. Each item retains an origin pointer to the doc where it was raised.

## Identity & trust bootstrapping

- **Agent identity.** When an agent (klaus-dispatched or otherwise) is the contributor, what key signs the commit and attestation? Ephemeral per-run, long-lived per-agent, or delegated from a human signer? The architecture supports any; a choice has to be made. *Origin: [scenarios.md](./scenarios.md), [contribute.md](./contribute.md).*
- **Bootstrapping trust for new contributors.** A new contributor has no trust score. Do they get one by default, or do they require gating until $N attestations land cleanly? Probably the latter; the state machine needs concrete rules. *Origin: [verification.md](./verification.md), [scenarios.md](./scenarios.md).*
- **Integrator self-integration.** The integrator is itself code in a repo. How does that code get integrated? Likely with a stricter policy on the integrator's own repo (always require human approval), but the chicken-and-egg deserves explicit handling. *Origin: [integration.md](./integration.md).*
- **Attestation tool distribution.** The attestation tool is a Nix derivation. How is its canonical hash published and pinned? Probably as part of an `armstrong`-shaped flake, but bootstrapping is worth thinking through. *Origin: [verification.md](./verification.md).*
- **Phase-0 identity is Tailscale-ACL-based.** The MVP uses Tailscale ACLs + SSH keys for identity — thin by design and swappable. The open question is *when* it has to grow (untrusted contributors, agent keys) and into what. Likely driven by the trust backstop. *Origin: [roadmap.md](./roadmap.md).*

## Policy & configuration

- **Policy bootstrap.** Someone has to be able to land the first principle / first policy change before the policy exists to govern principle changes. v1: integrator-key-holders land changes to `.the-valley/principles/` directly with relaxed policy. Worked out in a follow-up. *Origin: [integrator-internals.md](./integrator-internals.md).*
- **Per-repo integrator configuration.** Where does the integrator's per-repo configuration live — which protected refs, which trust thresholds per ref, which witnesses to wait for? Probably as a `config` node in the knowledge graph, queried alongside principles. *Origin: [integrator-internals.md](./integrator-internals.md).*
- **Per-repo check-set evolution.** When `flake.nix` adds a new required check, do existing attestations become invalid? Answer per [integrator-internals.md](./integrator-internals.md): yes, stale via policy-required-check. But what about graceful introduction periods? *Origin: [contribute.md](./contribute.md).*

## Cross-repo coordination

- **Cross-repo integration.** Two requests in two repos that must succeed together (schema producer + consumer). The integrator pattern can support this — a wrapper controller conditions B on A — but the design is deferred to v2. *Origin: [integration.md](./integration.md), [integrator-internals.md](./integrator-internals.md).*
- **Cross-repo feedback.** A change in repo A breaks a consumer in repo B. The consumer's event needs to land somewhere visible to A's thread. Cross-repo routing is non-trivial; deferred. *Origin: [feedback.md](./feedback.md).*
- **Cross-repo nodes in the knowledge graph.** A bug in repo A blocking work in repo B. v1 is per-repo; cross-repo linking needs stable IDs that include a repo namespace. Deferred. *Origin: [knowledge.md](./knowledge.md).*

## Attention, routing, and threads

- **Priority layer architecture.** Per-subscriber rule sets, learned priorities, hand-curated digests, escalation chains? The hard new bottleneck created by the feedback reframe. Probably starts hand-configured and grows. Deserves its own design doc once shape clarifies. *Origin: [feedback.md](./feedback.md), README.*
- **Thread identity and naming.** UUID? Human-readable slug? Tied to a commit, a chain, a topic? *Origin: [feedback.md](./feedback.md).*
- **When does a thread close?** Auto-close on deploy-stable? Explicit close events? Both? *Origin: [feedback.md](./feedback.md).*
- **Feedback back to an agent that finished its run.** Probably: events spawn new agent runs scoped to acting on them. klaus-shaped. *Origin: [feedback.md](./feedback.md).*
- **Backpressure visibility on the integrator.** Contributors should see queue depth and estimated wait. Easy to expose via the same `request-state` query; not strictly v1. *Origin: [integrator-internals.md](./integrator-internals.md).*

## Storage, retention, and evolution

- **Attestation expiry.** If inputs referenced by an attestation get garbage-collected from a Nix cache, re-derivation breaks. What's the retention promise on the binary cache? *Origin: [contribute.md](./contribute.md), [verification.md](./verification.md) (as "replay timeline").*
- **Stale-request expiry.** A request that sits stale forever clutters the namespace. Probably a periodic controller emits `request-abandoned` after $T of staleness with no owner action. *Origin: [integrator-internals.md](./integrator-internals.md).*
- **Schema evolution (events).** CUE handles validation, but event schemas will change. Migration story? *Origin: README.*
- **Schema evolution (knowledge graph).** Per-type schemas will change. Old nodes remain valid against their original version; agents produce against the latest. Migration tooling deferred. *Origin: [knowledge.md](./knowledge.md).*
- **Hetzner backup mechanism.** For the offsite copy of the bare repos: git-native mirror (`git push --mirror`), ZFS `zfs send` (classic-laddie is already ZFS), or restic/borg encrypted backup — or a combination. git-native gives a live fetchable remote; ZFS send is cheap block-level but needs ZFS to restore; restic is encrypted + dedup but not a live remote. *Origin: [roadmap.md](./roadmap.md).*

## Knowledge graph specifics

- **Initial node-type set.** Probably `bug`, `principle`, `decision`, `idea`, `thread` for v1. `task` may collapse into `bug`. `retrospective` may not be needed yet. *Origin: [knowledge.md](./knowledge.md).*
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

- **Discovery.** Without GitHub-the-social-graph, how do humans find each other's repos? Probably out of scope, but worth naming. *Origin: README.*

## Resolved (kept for the record)

- ~~**Signaling: bus event vs. request ref vs. branch convention.**~~ *Resolved in [contribute.md](./contribute.md):* the contributor pushes `refs/the-valley/integration-requests/<name>` atomically with the topic branch and attestation; the bare repo's post-receive hook is the canonical projection of those ref updates into bus events.
- ~~**The log is a single point of failure.**~~ *Reframed:* per-repo events (refs, attestations, integration requests) are durable in git itself and externally tamper-evident via the Tessera-backed tlog. The bus is only the source of truth for ephemeral cross-system events (deploys, metrics) and can be replicated lazily when needed.
