# Open questions

Consolidated across the design docs, tagged by layer: `[requirements]`, `[architecture]`, `[design]`. Design-level questions survive here only when a current or near-term roadmap phase (0–2) needs them; questions attached to pruned detail get re-asked when their phase starts — git history has the originals.

## Identity & trust bootstrapping

- `[architecture]` **Agent identity.** When an agent is the contributor, what key signs the commit and attestation? Ephemeral per-run, long-lived per-agent, or delegated from a human signer? The architecture supports any; a choice has to be made. *Origin: [scenarios.md](./scenarios.md), [contribute.md](./contribute.md).*
- `[architecture]` **Bootstrapping trust for new contributors.** A new contributor has no trust score. Do they get one by default, or do they require gating until $N attestations land cleanly? Probably the latter; the state machine needs concrete rules. *Origin: [verification.md](./verification.md), [scenarios.md](./scenarios.md).*
- `[architecture]` **Integrator self-integration.** The integrator is itself code in a repo. How does that code get integrated? Likely with a stricter policy on the integrator's own repo (always require human approval), but the chicken-and-egg deserves explicit handling. *Origin: [architecture.md](./architecture.md).*
- `[design]` **Attestation tool distribution.** The attestation tool is a Nix derivation. How is its canonical hash published and pinned? Probably as part of an `armstrong`-shaped flake, but bootstrapping is worth thinking through. *Origin: [verification.md](./verification.md).*
- `[architecture]` **Phase-0 identity is Tailscale-ACL-based.** Thin by design and swappable. The open question is *when* it has to grow (untrusted contributors, agent keys) and into what. Likely driven by the trust backstop. *Origin: [roadmap.md](./roadmap.md).*

## Cross-repo coordination

- `[architecture]` **Cross-repo integration.** Two requests in two repos that must succeed together (schema producer + consumer). A wrapper controller can condition B on A; design deferred to v2. *Origin: [architecture.md](./architecture.md).*
- `[architecture]` **Cross-repo feedback.** A change in repo A breaks a consumer in repo B; the consumer's event needs to land somewhere visible to A's thread. Cross-repo routing is non-trivial; deferred. *Origin: [architecture.md](./architecture.md).*

## Attention, routing, and threads

- `[architecture]` **Priority layer architecture.** Per-subscriber rule sets, learned priorities, hand-curated digests, escalation chains? The hard new bottleneck created by the feedback reframe. Probably starts hand-configured and grows. Deserves its own design doc once shape clarifies. *Origin: [architecture.md](./architecture.md).*

## Storage, retention, and evolution

- `[design]` **Hetzner backup mechanism.** For the offsite copy of the bare repos: git-native mirror (`git push --mirror`), ZFS `zfs send` (classic-laddie is already ZFS), or restic/borg encrypted backup — or a combination. git-native gives a live fetchable remote; ZFS send is cheap block-level but needs ZFS to restore; restic is encrypted + dedup but not a live remote. *Origin: [roadmap.md](./roadmap.md).*
- `[design]` **Attestation expiry.** If inputs referenced by an attestation get garbage-collected from a Nix cache, re-derivation breaks. What's the retention promise on the binary cache? *Origin: [contribute.md](./contribute.md), [verification.md](./verification.md).*
- `[architecture]` **Schema evolution (events).** CUE handles validation, but event schemas will change. Migration story? *Origin: [architecture.md](./architecture.md).*

## Verification specifics

- `[design]` **Effectful test catalogue.** Which classes of effectful test can be lifted to `nixosTest` or microVM-sealed environments? Maintaining a list — moving a test from effectful to pure is a meaningful security improvement. *Origin: [verification.md](./verification.md).*
- `[design]` **One attestation per commit, or multiple.** The current shape enforces one (refs are create-only). Multi-signature attestations could change this; a namespace like `refs/the-valley/attestations/<commit-sha>/<signer-id>` would allow multiple, at the cost of more complex lookup. *Origin: [contribute.md](./contribute.md).*

## Discovery

- `[requirements]` **Discovery.** Without GitHub-the-social-graph, how do humans find each other's repos? Probably out of scope, but worth naming. *Origin: [requirements.md](./requirements.md).*

## Resolved (kept for the record)

- ~~**Signaling: bus event vs. request ref vs. branch convention.**~~ *Resolved in [contribute.md](./contribute.md):* the contributor pushes `refs/the-valley/integration-requests/<name>` atomically with the topic branch and attestation; the bare repo's post-receive hook is the canonical projection of those ref updates into bus events.
- ~~**The log is a single point of failure.**~~ *Reframed:* per-repo events (refs, attestations, integration requests) are durable in git itself and externally tamper-evident via the Tessera-backed tlog. The bus is only the source of truth for ephemeral cross-system events (deploys, metrics) and can be replicated lazily when needed.
