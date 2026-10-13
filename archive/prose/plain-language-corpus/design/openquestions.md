# Open questions

This document consolidates unresolved architectural and design questions across the valley project.
Questions are tagged by altitude: `[requirements]`, `[architecture]`, or `[design]`.

Near-term questions (Phases 0–3) are actively investigated. Questions relevant only to later phases
are parked in [Held for later phases](#held-for-later-phases).

---

## Identity & trust bootstrapping

- `[architecture]` **Delegation-chain verification.** Agent identity is decided: agent runs do not
  hold sovereign keys; the host signs the attestation, and the change is attributed through
  provenance embedded in the statement (harness, model, prompt digest, context hash, and delegation
  chain per [dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md)). What
  remains open is how the Phase 3 integrator verifies this delegation chain mechanically rather than
  merely recording it. _(Origin: [contribute.md](./contribute.md))_
- `[architecture]` **Bootstrapping trust for new contributors.** A new contributor has no prior
  verification track record. Should the system grant a minimal trust level by default, or require
  all initial changes to pass full witness re-derivation until $N$ attestations land cleanly?
  _(Origin: [verification.md](./verification.md))_
- `[architecture]` **Integrator self-integration.** The integrator controller is itself software
  maintained in a repository. How are changes to the integrator tested and integrated without
  circular dependencies? _(Origin: [architecture.md](./architecture.md))_
- `[requirements]` **Recursive self-transparency.** Several open problems point to a single
  candidate invariant: _No actor can durably change the system, or an output of the system, without
  transparency_. Defining the exact mechanism, scope of durability, and non-recursive base case
  remains an active design effort. _(Origin: [self-transparency.md](./self-transparency.md))_
- `[design]` **Attestation tool distribution.** The verification runner (`attest`) is a Nix
  derivation. How is its canonical hash published, pinned, and bootstrapped across contributor
  machines? _(Origin: [verification.md](./verification.md))_
- `[architecture]` **Phase 0 identity and machine credentials.** Phase 0 relies on Tailscale ACLs
  and SSH keys for simplicity. As recorded in
  [bd-8a591dc](../.the-valley/bugs/bd-8a591dc-machine-credentials-never-expire.md), machine
  credentials currently do not expire. When and how should identity scale to handle ephemeral agent
  runs and untrusted external signers? _(Origin: [roadmap.md](./roadmap.md))_

---

## Cross-repo coordination & federation

The [federation model](./architecture.md#federation-the-valley-is-the-unit) separates coordination
into **intra-valley** (shared bus and integrator within one boundary) and **inter-valley**
(cross-boundary federation).

- `[architecture]` **Cross-repository atomic integration.** When changes across two repositories
  depend on each other (e.g., an API schema producer and consumer):
  - _Intra-valley:_ Handled via coordinated controller wrappers on a single bus.
  - _Inter-valley:_ Requires multi-party transaction protocols across distinct event streams.
    _(Origin: [architecture.md](./architecture.md))_
- `[architecture]` **Cross-repository feedback and breakage.** If a change in repository A breaks a
  consumer in repository B, how does the failure event route back to the active thread for change A?
  _(Origin: [architecture.md](./architecture.md))_
- `[architecture]` **Cross-valley contributions to shared projects.** When an external valley
  contributes to a shared foundational tool, how are changes, attestations, and approvals
  transported and evaluated against the owning valley's policy
  ([ida-8482624](../.the-valley/ideas/ida-8482624-federation-groups.md))? _(Origin:
  [architecture.md](./architecture.md#system-shape-and-cardinalities))_

---

## Valleys and hosts

- `[architecture]` **Bus scoping and isolation.** Should an instance run one NATS bus per valley, or
  a single host-wide bus with authenticated, valley-scoped subject namespaces
  ([bd-d853d9c](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md),
  [dcr-62ecc36](../.the-valley/decisions/dcr-62ecc36-signal-contracts.md))? _(Origin:
  [architecture.md](./architecture.md#system-shape-and-cardinalities))_
- `[design]` **Mirror authentication credentials.** Upstream mirror hosts often restrict SSH deploy
  keys to one repository per key. Mirrored setups must choose between managing per-repository SSH
  aliases or using organization-wide machine accounts
  ([ida-a0e5d03](../.the-valley/ideas/ida-a0e5d03-second-mirror-identity.md)). _(Origin:
  [roadmap.md](./roadmap.md))_

---

## Attention routing & notifications

- `[architecture]` **Priority and attention layer.** When thousands of events occur across
  repositories, how does the system route notifications to humans without creating an overwhelming
  firehose? Work scheduling is solved by traversing the outcome DAG
  ([architecture.md](./architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)),
  but human attention routing requires dedicated heuristics and escalation models. _(Origin:
  [architecture.md](./architecture.md))_

---

## Storage, retention, and evolution

- `[design]` **Binary cache retention and attestation reproducibility.** If input artifacts
  referenced by an attestation are garbage-collected from upstream binary caches, subsequent witness
  re-derivation will fail. What retention policy applies to binary caches supporting attestations?
  _(Origin: [contribute.md](./contribute.md), [verification.md](./verification.md))_
- `[architecture]` **Event schema migration.** CUE validates event payloads at runtime, but schemas
  will evolve over time. How are event versions and backward compatibility managed across long-lived
  streams? _(Origin: [architecture.md](./architecture.md))_
- `[architecture]` **Durable storage for lifecycle events.** The NATS event stream is treated as a
  rebuildable projection of git state. However, post-integration events (deployments, notifications)
  do not originate in git. In later phases, should these lifecycle events be projected into a
  durable store or should the bus itself be replicated? _(Origin: Phase 1 design review)_

---

## Discovery

- `[requirements]` **Decentralized project discovery.** In the absence of a centralized social
  forge, how do developers discover other valleys and public repositories? _(Origin:
  [requirements.md](./requirements.md))_

---

## Held for later phases

- `[design]` **Preserving destructive ref updates in event history (Phase 7).** Replaying git refs
  reproduces current state but loses records of force-pushes or deleted branches. For complete
  operational incident memory (Scenario S6), bare repositories may require server-side reflogs
  (`core.logAllRefUpdates`) to preserve destructive history
  ([ida-48c8868](../.the-valley/ideas/ida-48c8868-stores-beyond-git.md)).
- `[design]` **Cataloging effectful tests for hermetic isolation (Phase 6).** Identifying which
  effectful integration tests can be safely converted to hermetic `nixosTest` or VM-isolated
  executions to improve verification purity. _(Origin: [verification.md](./verification.md))_

---

## Resolved decisions (retained for reference)

- ~~**Phase 0 offsite replication:**~~ _Decided 2026-07-11
  ([dcr-d7952bc](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md))._
  Implemented via git-native live push mirroring combined with periodic encrypted offsite backups.
- ~~**Attestation granularity and identity:**~~ _Decided 2026-08-02
  ([dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md))._ Attestation
  subjects are tree digests rather than commits. Storage refs are keyed by tree digest and signer
  key hash.
- ~~**Bus durability single point of failure:**~~ _Decided in
  [architecture.md](./architecture.md#bet-git-as-event-source--a-log-not-a-workflow-engine)._ The
  event bus is an ephemeral, rebuildable projection of durable git repository state.
- ~~**Work prioritization:**~~ _Decided in
  [architecture.md](./architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)._
  Autonomous work priority is determined by propagating outcome weights down the knowledge graph
  dependency DAG.
