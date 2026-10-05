# Open questions

Consolidated across the design docs, tagged by layer: `[requirements]`, `[architecture]`,
`[design]`. Design-level questions survive here only when a current or near-term roadmap phase (0–2)
needs them; questions attached to pruned detail get re-asked when their phase starts — git history
has the originals. The exceptions sit under [Held for later phases](#held-for-later-phases), each
with the phase it waits for.

## Identity & trust bootstrapping

- `[architecture]` **Delegation-chain verification.** Agent identity itself is decided: an agent run
  holds no key of its own; the host signs, and the change is attributed inside the host-signed
  statement, as provenance — harness, model, digests of the prompt and of the context, and the
  delegation chain as recorded
  ([dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md)). What remains
  open is verifying that chain rather than recording it: the check belongs to the enforcement point,
  the Phase 3 integrator, and its rules are undesigned. _Origin: [contribute.md](./contribute.md)._
- `[architecture]` **Bootstrapping trust for new contributors.** A new contributor has no trust
  score. Do they get one by default, or do they require gating until $N attestations land cleanly?
  Probably the latter; the state machine needs concrete rules. _Origin:
  [verification.md](./verification.md)._
- `[architecture]` **Integrator self-integration.** The integrator is itself code in a repo. How
  does that code get integrated? Likely with a stricter policy on the integrator's own repo (always
  require human approval), but the chicken-and-egg deserves explicit handling. _Origin:
  [architecture.md](./architecture.md)._
- `[requirements]` **Recursive self-transparency (candidate invariant).** Several questions here
  look like facets of one property: _no actor can durably change the system, or an output of it,
  without transparency_ — recursive all the way down (the-valley builds the-valley). Integrator
  self-integration (above), the policy bootstrap, and load-bearing principles are the instances so
  far. Statement, mechanism, and base case are all undesigned;
  [self-transparency.md](./self-transparency.md) is a deliberate DRAFT stub that names the invariant
  and decides nothing. _Origin: [self-transparency.md](./self-transparency.md)._
- `[design]` **Attestation tool distribution.** The attestation tool is a Nix derivation. How is its
  canonical hash published and pinned? Probably as part of an `armstrong`-shaped flake, but
  bootstrapping is worth thinking through. _Origin: [verification.md](./verification.md)._
- `[architecture]` **Phase-0 identity is Tailscale-ACL-based.** Thin by design and swappable. The
  open question is _when_ it has to grow (untrusted contributors, agent keys) and into what. Likely
  driven by the trust backstop. One concrete forcing function is already recorded:
  [bd-8a591dc](../.the-valley/bugs/bd-8a591dc-machine-credentials-never-expire.md) — machine grants
  never expire, so disposable machines make provisioning O(machines) and revocation optional.
  _Origin: [roadmap.md](./roadmap.md)._

## Cross-repo coordination

The [federation frame](./architecture.md#federation-the-valley-is-the-unit) splits each of these
into an **intra-valley** case (within one valley — one bus, one integrator; the tractable near-term
problem) and an **inter-valley** case (across valleys — genuine federation, harder and later).

- `[architecture]` **Cross-repo integration.** Two requests in two repos that must succeed together
  (schema producer + consumer). _Intra-valley:_ one integrator, shared bus — a wrapper controller
  can condition B on A; design deferred to v2. _Inter-valley:_ events must cross a valley boundary;
  deferred further still. _Origin: [architecture.md](./architecture.md)._
- `[architecture]` **Cross-repo feedback.** A change in repo A breaks a consumer in repo B; the
  consumer's event needs to land somewhere visible to A's thread. _Intra-valley:_ both on the same
  bus, but routing is still non-trivial. _Inter-valley:_ the breakage event must federate to A's
  valley. Deferred. _Origin: [architecture.md](./architecture.md)._

- `[architecture]` **Cross-valley contribution to a shared project.** The substrate is consumed by
  every valley and is at the same time a project owned by one valley, landed by that valley's
  integrator. How a valley that does not own such a project contributes to it — carrying a change,
  its attestations and its approvals across the boundary, and getting them weighed by the owning
  valley's policy — is the federation edge
  ([ida-8482624](../.the-valley/ideas/ida-8482624-federation-groups.md)). _Inter-valley only._
  _Origin: [architecture.md](./architecture.md#system-shape-and-cardinalities)._

## Valleys and hosts

The [system-shape section](./architecture.md#system-shape-and-cardinalities) settles the
cardinalities and the isolation between valleys sharing a host
([dcr-9b5da04](../.the-valley/decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md)). Two questions
survive it.

- `[architecture]` **The bus's valley scoping.** One bus per valley, or one bus whose subjects are
  valley-scoped and authenticated? The bus is host plumbing today: localhost-only and
  unauthenticated ([bd-d853d9c](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md)), with
  subjects namespaced per project
  ([dcr-62ecc36](../.the-valley/decisions/dcr-62ecc36-signal-contracts.md)). When authentication
  ships, credentials compile from valley registries, which makes the bus a valley surface — and
  cross-valley event visibility is not a default, so the two shapes are the whole choice. The
  question is gated on that authentication and answers with it. _Origin:
  [architecture.md](./architecture.md#system-shape-and-cardinalities)._
- `[design]` **The mirror credential mechanism.** A mirror host allows one deploy key per
  repository, so a second mirrored repository authenticates only with per-repository keys behind ssh
  host aliases, or with a machine account whose one credential reaches every mirror. The fork, and
  what each way costs the host schema, is stated at
  [ida-a0e5d03](../.the-valley/ideas/ida-a0e5d03-second-mirror-identity.md); the missing mirrors are
  declared once it is taken. _Origin: [roadmap.md](./roadmap.md)._

## Attention, routing, and threads

- `[architecture]` **Priority layer architecture (attention routing).** Which events a human must
  _see_, and how urgently — per-subscriber rule sets, learned priorities, hand-curated digests,
  escalation chains? The firehose problem; the half of the old priority-layer question the
  [outcome-DAG bet](./architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)
  does not answer. Probably starts hand-configured and grows. Deserves its own design doc once shape
  clarifies. _Origin: [architecture.md](./architecture.md)._

## Storage, retention, and evolution

- `[design]` **Attestation expiry.** If inputs referenced by an attestation get garbage-collected
  from a Nix cache, re-derivation breaks. What's the retention promise on the binary cache? _Origin:
  [contribute.md](./contribute.md), [verification.md](./verification.md)._
- `[architecture]` **Schema evolution (events).** CUE handles validation, but event schemas will
  change. Migration story? _Origin: [architecture.md](./architecture.md)._
- `[architecture]` **Durable home for cross-system events.** The resolved single-point-of-failure
  answer calls deploys and notifications ephemeral. S4 makes those events the substance of "one
  durable history" ([requirements.md](./requirements.md)), so by Phase 5 that answer expires: either
  the bus is genuinely replicated — contradicting the bus-as-rebuildable-projection position
  ([architecture.md](./architecture.md#bet-git-as-event-source--a-log-not-a-workflow-engine)) — or
  its events get projected into a durable store. Which, and when? _Origin: owner review of Phase 1,
  2026-07-16._

## Discovery

- `[requirements]` **Discovery.** Without GitHub-the-social-graph, how do humans find each other's
  repos? Probably out of scope, but worth naming. _Origin: [requirements.md](./requirements.md)._

## Held for later phases

Two design-level questions belong to phases beyond the near-term horizon. They are parked whole
instead of pruned because each carries analysis that re-asking from git history would bury:

- `[design]` **The lived event log is the only witness to destructive ref updates** (S6, Phase 7).
  "Per-repo events are durable in git" fails exactly when git state is destroyed: after a force-push
  or a branch deletion, the event's `old` id may reference objects the repository no longer reaches,
  and replay — a current-state projection — reproduces strictly less than what happened. S6's
  incident memory wants the lived record, not the projection. A cheap, git-native mitigation is
  decidable now: server-side reflogs (`core.logAllRefUpdates`) on the bare repos keep prior ref
  values inside git through rewrites and deletions. Today the lived stream survives only by accident
  — the JetStream store sits under the valley dataDir, so the nightly restic pass snapshots it,
  crash-consistent at best: the volatile-store wrinkle in
  [stores beyond git](../.the-valley/ideas/ida-48c8868-stores-beyond-git.md). _Origin: owner review
  of Phase 1, 2026-07-16._
- `[design]` **Effectful test catalogue** (Phase 6). Which classes of effectful test can be lifted
  to `nixosTest` or microVM-sealed environments? Maintaining a list — moving a test from effectful
  to pure is a meaningful security improvement. _Origin: [verification.md](./verification.md)._

## Resolved (kept for the record)

- ~~**Phase 0 replication mechanism.**~~ _Decided 2026-07-11
  ([dcr-d7952bc](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md)):_
  a git-native live mirror plus periodic offsite backup, applied in
  [roadmap Phase 0](./roadmap.md#phase-0--mvp-repos-off-github).
- ~~**One attestation per commit, or multiple.**~~ _Decided 2026-08-02
  ([dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md)):_ an
  attestation's subject is a tree digest, not a commit, and storage is a ref keyed by subject digest
  and signer, so attestations by several signers coexist; several parties attesting to one statement
  sign it as sibling lines under one text.
- ~~**The log is a single point of failure.**~~ _Reframed:_ the bus is a rebuildable projection of
  state that is durable in git; the position is stated at
  [architecture.md](./architecture.md#bet-git-as-event-source--a-log-not-a-workflow-engine).
- ~~**Priority layer architecture (work scheduling).**~~ _Split, and addressed as a bet in
  [architecture.md](./architecture.md):_ the knowledge graph read generatively — root-outcome
  priority propagating down the dependency DAG, frontier dispatch toward the highest-priority root —
  answers "what should the system work on next." The attention-routing half of the original question
  stays open above.
