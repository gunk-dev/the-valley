# Open questions

Consolidated across the design docs, tagged by layer: `[requirements]`, `[architecture]`,
`[design]`. Design-level questions survive here only when a current or near-term roadmap phase (0–2)
needs them; questions attached to pruned detail get re-asked when their phase starts — git history
has the originals.

## Identity & trust bootstrapping

- `[architecture]` **Agent identity.** When an agent is the contributor, what key signs the commit
  and attestation? Ephemeral per-run, long-lived per-agent, or delegated from a human signer? The
  architecture supports any; a choice has to be made. _Origin: [scenarios.md](./scenarios.md),
  [contribute.md](./contribute.md)._
- `[architecture]` **Bootstrapping trust for new contributors.** A new contributor has no trust
  score. Do they get one by default, or do they require gating until $N attestations land cleanly?
  Probably the latter; the state machine needs concrete rules. _Origin:
  [verification.md](./verification.md), [scenarios.md](./scenarios.md)._
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
  driven by the trust backstop. _Origin: [roadmap.md](./roadmap.md)._

## Cross-repo coordination

The [federation frame](./architecture.md#federation-the-group-is-the-unit) splits each of these into
an **intra-group** case (within one instance — one bus, one integrator; the tractable near-term
problem) and an **inter-group** case (across instances — genuine federation, harder and later).

- `[architecture]` **Cross-repo integration.** Two requests in two repos that must succeed together
  (schema producer + consumer). _Intra-group:_ one integrator, shared bus — a wrapper controller can
  condition B on A; design deferred to v2. _Inter-group:_ events must cross an instance boundary;
  deferred further still. _Origin: [architecture.md](./architecture.md)._
- `[architecture]` **Cross-repo feedback.** A change in repo A breaks a consumer in repo B; the
  consumer's event needs to land somewhere visible to A's thread. _Intra-group:_ both on the same
  bus, but routing is still non-trivial. _Inter-group:_ the breakage event must federate to A's
  instance. Deferred. _Origin: [architecture.md](./architecture.md)._

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

## Verification specifics

- `[design]` **Effectful test catalogue.** Which classes of effectful test can be lifted to
  `nixosTest` or microVM-sealed environments? Maintaining a list — moving a test from effectful to
  pure is a meaningful security improvement. _Origin: [verification.md](./verification.md)._
- `[design]` **One attestation per commit, or multiple.** The current shape enforces one (refs are
  create-only). Multi-signature attestations could change this; a namespace like
  `refs/the-valley/attestations/<commit-sha>/<signer-id>` would allow multiple, at the cost of more
  complex lookup. _Origin: [contribute.md](./contribute.md)._

## Discovery

- `[requirements]` **Discovery.** Without GitHub-the-social-graph, how do humans find each other's
  repos? Probably out of scope, but worth naming. _Origin: [requirements.md](./requirements.md)._

## Resolved (kept for the record)

- ~~**Hetzner backup mechanism.**~~ _Decided 2026-07-04
  ([dcr-db1acbb](../.the-valley/decisions/dcr-db1acbb-hetzner-replication-mechanism.md)):_
  push-triggered git-native mirror plus nightly restic offsite backup; ZFS send rejected for now.
- ~~**Signaling: bus event vs. request ref vs. branch convention.**~~ _Resolved in
  [contribute.md](./contribute.md):_ the contributor pushes
  `refs/the-valley/integration-requests/<name>` atomically with the topic branch and attestation;
  the bare repo's post-receive hook is the canonical projection of those ref updates into bus
  events.
- ~~**The log is a single point of failure.**~~ _Reframed:_ per-repo events (refs, attestations,
  integration requests) are durable in git itself and externally tamper-evident via the
  Tessera-backed tlog. The bus is only the source of truth for ephemeral cross-system events
  (deploys, metrics) and can be replicated lazily when needed.
- ~~**Priority layer architecture (work scheduling).**~~ _Split, and addressed as a bet in
  [architecture.md](./architecture.md):_ the knowledge graph read generatively — root-outcome
  priority propagating down the dependency DAG, frontier dispatch toward the highest-priority root —
  answers "what should the system work on next." The attention-routing half of the original question
  stays open above.
