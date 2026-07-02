# Roadmap

The other design docs describe the design space. This is the plan. It exists to **incrementally validate the design** — not to commit to it.

## The shape of the plan

The design is a stack of claims: git-as-event-source, signed-local-checks-instead-of-CI, pull-based integration, reactive controllers, transparency-backed trust, a knowledge graph. Each is falsifiable. The roadmap turns each into a phase you can actually run and check.

Three rules govern every phase:

1. **Independently valuable.** The phase ships something worth having even if the next phase never happens. Phase 0 is durable self-hosting; you'd want that regardless of whether attestations ever exist.
2. **Independently stoppable.** You can stop after any phase and be left with a coherent system, not a half-wired one.
3. **Validates exactly one distinct claim.** A phase that validates two claims can't tell you *which* one broke.

Heavy iteration on the later phases is expected, and that's a **feature, not a risk**. The point of validating Phase 1 before building Phase 3 is that Phase 3 gets designed against a spine that has actually run. This is a living first cut. It will be wrong in places, and the plan is structured so that being wrong is cheap.

**Sequencing is by validation gate, not calendar date.** There are no time estimates here on purpose. Each phase is done when its exit criteria are checkable and checked — "validated when …", not "shipped by …".

## Phases at a glance

| Phase | Ships | Validates | Gate |
| --- | --- | --- | --- |
| 0 | Repos off GitHub, durably | Self-hosting is durable enough to trust | Verified restore + a week off GitHub for the pilot |
| 1 | The event log | git-as-event-source; "a log, not a workflow engine" | `valley tail` shows real ref updates as events |
| 2 | Signed local attestations | The contributor protocol's ergonomics | `nix run .#attest` replaces "wait for CI" for real work |
| 3 | The integrator | Pull-based integration; staleness-as-failure-mode | You stop pushing to `main` directly, even solo |
| 4 | Effectful reactions | Reactive controllers replace push-based CI/CD | commit→build→deploy→notify is one queryable history |
| 5 | Trust backstop | The security model (~SLSA-3 for pure checks) | An untrusted-signer change lands only via the trust flow |
| 6 | Knowledge & feedback | Observability + project-knowledge unbundling | A PR-shaped interaction happens with no PR object |

The named systems: **klaus** (agent dispatch, run budget), **armstrong** (the Go controller that becomes the effectful-reactions successor), **tesseract** ([transparency-dev/tesseract](https://github.com/transparency-dev/tesseract), the Tessera-backed tlog), **cosmo** (the Nix/NixOS infra), and **classic-laddie** (a ZFS-based NixOS box on the tailnet, already running the klaus webhook relay). classic-laddie is the host for every server-side phase below.

---

## Phase 0 — MVP: repos off GitHub

**This is the priority.** Everything downstream is developed against it.

**Goal.** Get the crown-jewel git data off GitHub and onto infrastructure you control, durably enough that you'd trust it with the only copy — before there's ever only one copy.

**What gets built.** Hosting only. Bare git over SSH on classic-laddie. Identity is Tailscale ACLs + SSH keys ([architecture.md](./architecture.md)'s *Hosting* and *Identity* rows, nothing more). **No bus, no attestations, no integrator.** The contributor — one solo dev — pushes directly to `main`. `cgit` or similar for browsing if wanted. The integrator and the protected-ref invariant arrive in [Phase 3](#phase-3--the-integrator); pushing straight to `main` now is correct, not a shortcut.

The Tailscale identity layer is deliberately thin. The user has noted it's thin enough to swap later — so it's an acceptable v1 choice, not a lock-in. When identity needs to grow (untrusted contributors, agent keys), it grows in [Phase 5](#phase-5--trust-backstop).

**Durability is part of the MVP, not an afterthought.** The git data is the crown jewel; losing it is the one unrecoverable failure. The target is 3-2-1: primary bare repos on classic-laddie, replicated offsite to **Hetzner**, with **GitHub retained as a transitional mirror** during migration — three copies early on. Three concrete mechanisms to iterate on (not hard-picked):

| Mechanism | What it gets you | Cost |
| --- | --- | --- |
| (a) periodic `git push --mirror` to a Hetzner Storage Box or small VPS running bare git | A usable second remote you can `git fetch` from directly; git-native, trivially verifiable | Not encrypted at rest by default; per-repo push config |
| (b) ZFS snapshot + `zfs send` to Hetzner (classic-laddie is already ZFS, from cosmo) | Block-level, cheap, incremental, snapshots the whole repo dir atomically | Restore needs ZFS on the far end; not a live git remote |
| (c) restic/borg encrypted backup of the bare-repo dir to Hetzner object storage | Encrypted + deduplicated offsite | Not a live remote; restore is a two-step (restic → git) |

They're not exclusive — (a) gives a hot second remote, (b) or (c) gives cheap offsite depth. The mechanism choice is an [open question](#open-questions). Light RPO/RTO framing: for a solo dev, an RPO of "since last hourly mirror" and an RTO of "clone from the second remote" is plenty; don't over-engineer past that.

**Migration strategy: mirror-first, then cut over.** Add classic-laddie as an additional remote and dual-push. Keep GitHub live as backup. Flip the canonical `origin` per-repo once confident. Reversible at every step; supports iteration.

**Pilot: the-valley itself.** the-valley's own repo is the first off GitHub — dogfooding, low stakes, and every later phase is developed against it. Later repos roll out after the pilot proves the workflow.

**The design claim it validates.** Self-hosted bare git on the existing tailnet is durable and ergonomic enough to be the canonical home for real work — the premise ([requirements.md](./requirements.md)) that hosting lock-in is the only thing GitHub does well, and it's swappable.

**Exit criteria.**
- the-valley's canonical `origin` is classic-laddie.
- A restore from the Hetzner backup has actually been *performed and verified* — not merely configured.
- The user works ~a week without touching GitHub for the pilot repo.
- The migration runbook is written and repeatable for the next repo.

**Links.** [architecture.md](./architecture.md) (*Hosting*, *Identity* rows).

**New open questions.** The Hetzner backup mechanism (a/b/c above). Phase-0 identity being Tailscale-ACL-based for now (thin, swappable). Both belong in [openquestions.md](./openquestions.md); see [Open questions](#open-questions).

---

## Phase 1 — The event log (the spine)

**Goal.** Make git a source of events. Nothing reacts yet — this phase only proves the events are real and legible.

**What gets built.** NATS JetStream on classic-laddie. A `post-receive` hook projecting ref updates into bus events: `ref-updated` now, `attestation-published` / `integration-requested` later (per [contribute.md](./contribute.md)). A dumb `valley tail` inspector to watch events scroll by. That's it. No subscriber acts on anything.

**The design claim it validates.** git-as-event-source — that ref updates project cleanly onto a log, and that a log is the right substrate ("a log, not a workflow engine" — [architecture.md](./architecture.md)). If the projection is awkward or lossy here, everything downstream inherits the problem, so it's worth isolating.

**A note on durability.** The bus is the source of truth only for *ephemeral cross-system events*. Per-repo events (refs, attestations, integration requests) are durable in git itself; the bus is a projection that can be rebuilt. This is the resolution of the old "the log is a single point of failure" question ([openquestions.md](./openquestions.md), *Resolved*).

**Exit criteria.**
- A push to classic-laddie produces a `ref-updated` event visible in `valley tail` within seconds.
- Rebuilding the stream from scratch (replay the repo's refs) reproduces the same events — the projection is deterministic.

**Links.** [architecture.md](./architecture.md) (*a log, not a workflow engine*), [contribute.md](./contribute.md).

---

## Phase 2 — Attestations (verification MVP)

**Goal.** Replace "wait for CI" with "signed local check" for real day-to-day work.

**What gets built.** A `nix run .#attest` helper that: runs the repo's canonical `nix flake check` derivations; composes the attestation ([contribute.md](./contribute.md) / [verification.md](./verification.md)); SSH-signs it with the same key as the commit signature; stores it as `refs/the-valley/attestations/<sha>`; pushes atomically ([contribute.md](./contribute.md)).

**Deferred to [Phase 5](#phase-5--trust-backstop):** the Tessera tlog and witness re-derivation. Phase 2 is *just local signed attestations* — the ergonomics of the protocol, without the trust backstop. This is the right cut: the protocol has to feel good before the security layer is worth building on top of it.

**The design claim it validates.** The contributor protocol's ergonomics — that a signed local check is a pleasant, fast substitute for a CI gate, produced by native git plus one helper ([contribute.md](./contribute.md)).

**Exit criteria.**
- Real changes to the-valley land with an attestation ref alongside every commit.
- The push is one atomic native-git command; no wrapper.
- The user prefers this to CI for the pilot repo — the ergonomic claim holds in practice, not just in theory.

**Links.** [contribute.md](./contribute.md), [verification.md](./verification.md).

---

## Phase 3 — The integrator

**Goal.** Stop writing `main` by hand. Route every change through a request-and-react flow, even solo.

**What gets built.** Two things:

- **The structural invariant.** The one-line `pre-receive` hook: only the integrator key writes `refs/heads/<protected>`; attestation refs are create-only; everything else is open ([contribute.md](./contribute.md), *The one invariant*; [architecture.md](./architecture.md), *The one structural git invariant*).
- **The integrator controller.** Pull-based, subscribing to `integration-requested`. Verifies signature + attestation + (for now, self-) trust, does FF/rebase into `main`, emits outcome events ([architecture.md](./architecture.md), *a pull-based integrator*). Merge-queue semantics fall out for free.

This is where the user **stops pushing directly to `main`** and goes through the request flow, even as the only contributor. That's the whole point — the flow has to be tolerable at N=1 before it's asked to hold at N>1.

**The design claim it validates.** The controller pattern and the core integration claim — that a pull-based integrator is a better shape than a `pre-receive` gate, and that staleness is the right unified failure mode ([architecture.md](./architecture.md)).

**Exit criteria.**
- Direct pushes to `refs/heads/main` are rejected by the hook; only the integrator key succeeds.
- A change lands via `integration-requested` → `integration-succeeded` with no manual ref write.
- A stale case (rebase would change the tree) surfaces as one `request-stale` event, not a rejection or a retry storm.

**Links.** [architecture.md](./architecture.md) (*a pull-based integrator*, *The one structural git invariant*).

**New open questions.** Integrator self-integration — the integrator is code in a repo; how does *its* changes get integrated? Already tracked in [openquestions.md](./openquestions.md) (*Identity & trust bootstrapping*). Phase 3 is where the chicken-and-egg becomes concrete.

---

## Phase 4 — Effectful reactions (armstrong-as-controller)

**Goal.** Prove the causality chain end to end: a commit becomes a build becomes a deploy becomes a notification, all as reactions on the log.

**What gets built.** armstrong subscribes to `integration-succeeded` → `nix build` the artifact derivation → deploy / notify ([scenarios.md #1](./scenarios.md)). This is the controller-shaped successor to the current Actions-based armstrong — the same tool, inverted from push-based pipeline to reactive subscriber.

**The design claim it validates.** Reactive controllers replace push-based CI/CD, and the causality chain (commit → build → deploy → notify) is *one queryable history* rather than seven disconnected job UIs ([architecture.md](./architecture.md), *Components* and *a log, not a workflow engine*).

**Exit criteria.**
- An integration into `main` triggers a build and a deploy with no workflow file — only a subscriber.
- The full chain for a given commit is reconstructable from the log alone.

**Links.** [architecture.md](./architecture.md) (*Components*), [scenarios.md #1 and #6](./scenarios.md).

---

## Phase 5 — Trust backstop

**Goal.** Make attestations trustworthy under adversarial or multi-party conditions, not just convenient.

**What gets built.**

- **Transparency log.** Tessera-backed tlog publication of attestations via tesseract, with inclusion proofs appended as a sidecar ([contribute.md](./contribute.md)).
- **Witness re-derivation.** A pure-check re-verifier that re-derives any pure attestation and emits confirm/deny events.
- **Trust controller.** Scores per signer from confirm rates, with revocation ([architecture.md](./architecture.md), *attestation with revocation*).

Together these enable the untrusted-contributor and agent-identity scenarios that Phase 2's local-only attestations can't.

**The design claim it validates.** The security model — roughly SLSA Level 3 for pure-derivation checks, plus non-repudiation from the tlog ([verification.md](./verification.md), *The mechanism stack*).

**Exit criteria.**
- Every attestation lands in the tlog with a verifiable inclusion proof.
- A deliberately-wrong pure attestation is caught by the witness and lowers the signer's trust score.
- An untrusted signer's change integrates *only* via the trust flow ([scenarios.md #4](./scenarios.md)), never by default.

**Links.** [verification.md](./verification.md), [contribute.md](./contribute.md), [scenarios.md #2 and #4](./scenarios.md).

---

## Phase 6 — Knowledge & feedback

**Goal.** Unbundle the last two things GitHub does — project knowledge and review — from the vendor UI.

**What gets built.**

- **Knowledge graph.** The typed-node markdown graph (`bug` / `principle` / `decision` / `idea` / `thread`) at each repo root ([architecture.md](./architecture.md), *project knowledge is a typed-node graph*).
- **Threads.** Derived views over events, scoped to a change or chain ([architecture.md](./architecture.md), *review is observability + feedback*). PR-as-thread — the "PR" becomes a named query, not a stored object.
- **Priority/attention router.** The routing subsystem that decides who needs to know about which event ([architecture.md](./architecture.md), same section).

**The design claim it validates.** Observability + project-knowledge unbundling — that review is a special case of feedback, and that institutional knowledge belongs in a signed, agent-legible graph rather than scattered across issues, wikis, and heads ([architecture.md](./architecture.md), *Observability & feedback*, *Project knowledge* rows).

**Exit criteria.**
- A change accrues discussion, an approval, and an outcome as one chronology with no PR object anywhere.
- An agent reads and writes knowledge nodes as structured frontmatter.
- The router surfaces one genuinely high-priority event to a human without a firehose.

**Links.** [architecture.md](./architecture.md) (*review is observability + feedback*, *project knowledge is a typed-node graph*), [scenarios.md #3 and #4](./scenarios.md).

**New open questions.** None new here — but note the *priority-layer architecture* question ([openquestions.md](./openquestions.md), *Attention, routing, and threads*) is the hardest new bottleneck the whole design creates, and Phase 6 is where it stops being hypothetical.

---

## Cross-cutting threads

Some things aren't a phase; they run through all of them.

- **CUE event schemas.** Shared across producers and consumers, reused from armstrong. They start minimal in Phase 1 and grow a field or a type each phase. Schema evolution is a standing concern, not a phase — tracked in [openquestions.md](./openquestions.md) (*Storage, retention, and evolution*).
- **The `valley` CLI.** A thin tool that accretes one verb per phase — `valley migrate` (0), `valley tail` (1), `valley attest` or the `nix run .#attest` helper (2), `valley browse` (6). It stays thin on purpose: the contributor protocol is native-git-first ([contribute.md](./contribute.md)), so the CLI is convenience, never the critical path. Anything `valley` does, plain git and `nix` can do.
- **Durability as a standing priority.** Phase 0 makes it explicit, but it never stops mattering. The durable substrate is git objects + attestation refs + the tlog — all replicable, all externally witnessable. The bus is the one replaceable component: lose it and rebuild it from git. Every phase should preserve that property — if a phase makes the bus load-bearing for durable state, that's a design smell to catch.

## Open questions

Two questions are genuinely new to this document and should migrate to [openquestions.md](./openquestions.md) under the noted themes:

- **Hetzner backup mechanism** (*Storage, retention, and evolution*). git-native mirror (a), ZFS send (b), or restic/borg (c) for the offsite copy — or a combination. Tradeoffs in [Phase 0](#phase-0--mvp-repos-off-github). *Origin: roadmap.md.*
- **Phase-0 identity is Tailscale-ACL-based** (*Identity & trust bootstrapping*). Thin by design and swappable; the open question is *when* it has to grow and into what — likely driven by [Phase 5](#phase-5--trust-backstop). *Origin: roadmap.md.*

Everything else this roadmap touches is already tracked in [openquestions.md](./openquestions.md) — integrator self-integration, the priority-layer architecture, attestation expiry vs. cache retention, agent identity, and schema evolution all surface at specific phases above.
