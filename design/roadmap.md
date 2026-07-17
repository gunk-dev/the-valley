# Roadmap

The other design docs describe the design space. This is the plan. It exists to **incrementally
validate the design** — not to commit to it. It sits downstream of the
[scenario ladder](./user-scenarios.md): the ladder says what must become true for a user, and in
what order; this document says what gets built to make each rung true, and how each claim gets
checked. Where phases and rungs disagree, the rungs win.

## The shape of the plan

The design is a stack of claims: git-as-event-source, signed-local-checks-instead-of-CI, pull-based
integration, reactive controllers, transparency-backed trust, a knowledge graph. Each is
falsifiable. The roadmap turns each into a phase you can actually run and check.

Four rules govern every phase:

1. **Independently valuable.** The phase ships something worth having even if the next phase never
   happens. Phase 0 is durable self-hosting; you'd want that regardless of whether attestations ever
   exist.
2. **Independently stoppable.** You can stop after any phase and be left with a coherent system, not
   a half-wired one.
3. **Validates exactly one distinct claim.** A phase that validates two claims can't tell you
   _which_ one broke.
4. **Serves a rung.** Every phase names the rung(s) of the [ladder](./user-scenarios.md#the-ladder)
   it builds toward; a phase no rung demands has no business on this page. One rung may take several
   phases — each still validates its own claim, and the rung is established at the last of their
   gates.

Heavy iteration on the later phases is expected, and that's a **feature, not a risk**. The point of
validating Phase 1 before building Phase 3 is that Phase 3 gets designed against a spine that has
actually run. This is a living document, re-derived against the ladder as rungs and reality move. It
will be wrong in places, and the plan is structured so that being wrong is cheap. The ladder's own
discipline applies here too: a phase serving a far rung stays thin until that rung is the top
priority.

**Sequencing is by validation gate, not calendar date.** There are no time estimates here on
purpose. Each phase is done when its exit criteria are checkable and checked — "validated when …",
not "shipped by …".

## Phases at a glance

| Phase | Ships                                   | Rung                    | Validates                                               | Gate                                                                  |
| ----- | --------------------------------------- | ----------------------- | ------------------------------------------------------- | --------------------------------------------------------------------- |
| 0     | Repos off GitHub, durably; knowledge v0 | S1                      | Self-hosting is durable enough to trust                 | Verified restore + a week off GitHub for the pilot                    |
| 1     | The event log                           | S2 (1 of 3)             | git-as-event-source; "a log, not a workflow engine"     | `valley tail` shows real ref updates as events                        |
| 2     | Signed local attestations               | S2 (2 of 3)             | The contributor protocol's ergonomics                   | `nix run .#attest` replaces "wait for CI" for real work               |
| 3     | The integrator                          | S2 established          | Pull-based integration; staleness-as-failure-mode       | You stop pushing to `main` directly, even solo                        |
| 4     | Agents as first-class authors           | S3 established          | The pipeline holds with no human in it                  | A dispatched change lands unsupervised, attributed to the agent's key |
| 5     | Effectful reactions                     | S4 established          | Reactive controllers replace push-based CI/CD           | commit→build→deploy→notify is one queryable history                   |
| 6     | Trust backstop                          | S3 hardened; S5 enabled | The security model (~SLSA-3 for purity-claiming checks) | An untrusted-signer change lands only via the trust flow              |
| 7     | Feedback & incident memory              | S6 established          | Review is feedback; incidents are memory                | An incident files its own node; review happens with no PR object      |

S7 (strangers) has no phase, deliberately: it is
[explicitly deferred, maybe never](./user-scenarios.md#the-ladder) — the trust model should degrade
toward it gracefully, not build for it. Requirements need 7 (_Demand-shaped work_,
[requirements.md](./requirements.md)) is likewise phase-less, deliberately: phases serve rungs (rule
4), and need 7 derives from the README's outcome-engine framing rather than a rung — though Phase 4
(dispatch targets an outcome node) and Phase 5 (a landed change closes the outcome it serves)
partially serve it.

The named systems, all pilot instances: **klaus** (agent dispatch, run budget), **armstrong** (the
Go controller that becomes the effectful-reactions successor), **tesseract**
([transparency-dev/tesseract](https://github.com/transparency-dev/tesseract), the Tessera-backed
tlog), **cosmo** (the operator's NixOS infra — the consumer that installs this repo's host module),
and **classic-laddie** (the pilot host: a box on the operator's tailnet, already running the klaus
webhook relay; it hosts every server-side phase below first). Hostnames are pilot detail, not
design: the durable content of this plan is portable, and what any host serves is declared in the
host schema this repo ships ([schema/valley.cue](../schema/valley.cue)).

---

## Phase 0 — MVP: repos off GitHub

**This is the priority.** Everything downstream is developed against it.

**Rung:
[S1 — my repos live on my infrastructure and I can never lose them](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them).**
The rung's acceptance checklist is this phase's gate.

**Goal.** Get the crown-jewel git data off GitHub and onto infrastructure you control, durably
enough that you'd trust it with the only copy — before there's ever only one copy.

**What gets built — and what already is.** Hosting only. Bare git over SSH on a host the operator
controls (the pilot host: classic-laddie). The first artifacts are shipped: a CUE host schema
([schema/valley.cue](../schema/valley.cue)) declaring what a valley host serves — projects, each
with a git store and push mirrors — and a `valley-host` NixOS module ([flake.nix](../flake.nix))
that installs a host from that declaration; cosmo consumes the module for the pilot host (in
flight). The schema is the domain model and is deliberately not Nix: the NixOS module is one
installer consuming it, and any other installer can consume the same file. Identity is Tailscale
ACLs + SSH keys ([architecture.md](./architecture.md)'s _Hosting_ and _Identity / access_ rows,
nothing more). **No bus, no attestations, no integrator.** The contributor — one solo dev — pushes
directly to `main`. `cgit` or similar for browsing if wanted. The integrator and the protected-ref
invariant arrive in [Phase 3](#phase-3--the-integrator); pushing straight to `main` now is correct,
not a shortcut.

The Tailscale identity layer is deliberately thin — thin enough to swap later, so it is an
acceptable v1 choice, not a lock-in. When identity needs to grow (untrusted contributors, agent
keys), it grows in [Phase 6](#phase-6--trust-backstop).

**Knowledge v0 ships in this phase.** S1's knowledge increment: issues, outcomes, ideas, and
decisions live with the repo as plain markdown files — a directory convention, not a system. No
indexer, no events; the schemas are documentation until there's an integrator to enforce them.
Instantiated at [.the-valley/](../.the-valley/README.md). The graph grows one increment per rung
from here — agents write it ([Phase 4](#phase-4--agents-as-first-class-authors)), it becomes
observable ([Phase 5](#phase-5--effectful-reactions-armstrong-as-controller)), incidents file into
it ([Phase 7](#phase-7--feedback--incident-memory)) — so no later phase "builds the knowledge
graph".

**Durability is part of the MVP, not an afterthought.** The git data is the crown jewel; losing it
is the one unrecoverable failure. The target is 3-2-1: primary bare repos on the valley host,
replicated offsite, with **GitHub retained as a transitional mirror** during migration — three
copies early on. The mechanism is decided
([dcr-d7952bc](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md) — it
was this document's open question): two complementary layers.

| Layer            | Mechanism                                                                        | What it gets you                                                                                             |
| ---------------- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| Live replication | Push-triggered git-native mirror to an independent live remote                   | Pushed = replicated: a hot second remote you can `git fetch` from directly; git-native, trivially verifiable |
| Offsite depth    | Periodic encrypted backup of the bare-repo dir (restic-style) to offsite storage | Encrypted, deduplicated point-in-time history, independent of the live remotes                               |

Block-level replication (ZFS send) was considered and rejected for now: it ties restore to a
matching filesystem on the far end, and it is not a live git remote. For the pilot, the live second
remote is GitHub — the transitional mirror — and offsite depth is a Hetzner Storage Box; a dedicated
sovereign live remote is deferred until GitHub exit. Light RPO/RTO framing, per the rung: pushed
work is in at least two independent places within minutes, one offsite; restore from the offsite
copy within a day — and no copy counts until a restore from it has been _performed and verified_.

**Migration strategy: mirror-first, then cut over.** Mirrors are declared config, not a manual
dual-push: the host declaration's per-project `mirrors` field replicates every push to the primary
out to each mirror URL — deletions propagated, best-effort, a dead mirror never rejects the primary
push. GitHub stays declared as one such mirror while confidence builds; the canonical `origin` flips
per-repo once it's earned. The same field is the public-exposure mechanism later — migration
dual-push and publishing are one mechanism. Reversible at every step; supports iteration.

**Pilot: the-valley itself.** the-valley's own repo is the first off GitHub — dogfooding, low
stakes, and every later phase is developed against it. Later repos roll out after the pilot proves
the workflow.

**The design claim it validates.** Self-hosted bare git on the operator's existing infrastructure is
durable and ergonomic enough to be the canonical home for real work — the premise
([requirements.md](./requirements.md)) that hosting lock-in is the only thing GitHub does well, and
it's swappable.

**Exit criteria.** The rung owns them:
[S1's acceptance checklist](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them)
— canonical origin flipped; every push verifiably in two independent places; one restore performed
and verified; a week of real human _and_ agent work without GitHub, including an agent change
landing end to end in direct-push mode; a real issue worked and closed as an in-repo node; a
repeatable migration-plus-restore runbook.

**Links.** [architecture.md](./architecture.md) (_Hosting_, _Identity / access_ rows),
[schema/valley.cue](../schema/valley.cue), [examples/host.cue](../examples/host.cue).

**Open questions.** One remains from this phase: Phase-0 identity being Tailscale-ACL-based (thin,
swappable) — see [Open questions](#open-questions). The replication mechanism was the other and is
decided above.

---

## Phase 1 — The event log (the spine)

**Rung: [S2](./user-scenarios.md#the-ladder), first of three phases.** No rung demands a bus for its
own sake; this phase exists because everything S2 needs — attested checks, an integrator — runs on
events, and the projection claim is worth isolating (rule 3: if it's awkward here, everything
downstream inherits the problem).

**Goal.** Make git a source of events. Nothing reacts yet — this phase only proves the events are
real and legible.

**What gets built.** NATS JetStream on the valley host. A `post-receive` hook projecting ref updates
into bus events: `ref-updated` now, `attestation-published` / `integration-requested` later (per
[contribute.md](./contribute.md)). A dumb `valley tail` inspector to watch events scroll by. That's
it. No subscriber acts on anything.

**The design claim it validates.** git-as-event-source — that ref updates project cleanly onto a
log, and that a log is the right substrate ("a log, not a workflow engine" —
[architecture.md](./architecture.md)).

**A note on durability.** The bus is the source of truth only for _ephemeral cross-system events_.
Per-repo events (refs, attestations, integration requests) are durable in git itself; the bus is a
projection that can be rebuilt. This is the resolution of the old "the log is a single point of
failure" question ([openquestions.md](./openquestions.md), _Resolved_).

**Exit criteria.**

- A push to the valley host produces a `ref-updated` event visible in `valley tail` within seconds.
- Rebuilding the stream from scratch (replay the repo's refs) reproduces the same events — the
  projection is deterministic.

**Links.** [architecture.md](./architecture.md) (_a log, not a workflow engine_),
[contribute.md](./contribute.md).

---

## Phase 2 — Attestations (verification MVP)

**Rung: [S2](./user-scenarios.md#the-ladder), second of three.** The rung's hardest demand — checks
run where the work was written, trustworthy enough to integrate against — enters here as ergonomics;
the adversarial half of "trustworthy" is [Phase 6](#phase-6--trust-backstop)'s.

**Goal.** Replace "wait for CI" with "signed local check" for real day-to-day work.

**What gets built.** A `nix run .#attest` helper that: runs the repo's canonical checks (Nix
derivations, in the reference implementation); composes the attestation
([contribute.md](./contribute.md) / [verification.md](./verification.md)), recording _what check
ran, on what tree, with what result_; SSH-signs it with the same key as the commit signature; stores
it as `refs/the-valley/attestations/<sha>`; pushes atomically ([contribute.md](./contribute.md)).
Purity is a claim tied to a runner kind — the `nix` runner claims it strongly — not a property the
schema assumes.

Checks-as-derivations is the reference implementation, not the contract. The attestation schema must
stay implementable by other runner kinds, so a non-Nix runner is an added backend later, never a
migration.

**Deferred to [Phase 6](#phase-6--trust-backstop):** the Tessera tlog and witness re-derivation.
Phase 2 is _just local signed attestations_ — the ergonomics of the protocol, without the trust
backstop. This is the right cut: the protocol has to feel good before the security layer is worth
building on top of it.

**The design claim it validates.** The contributor protocol's ergonomics — that a signed local check
is a pleasant, fast substitute for a CI gate, produced by native git plus one helper
([contribute.md](./contribute.md)).

**Exit criteria.**

- Real changes to the-valley land with an attestation ref alongside every commit.
- The push is one atomic native-git command; no wrapper.
- The user prefers this to CI for the pilot repo — the ergonomic claim holds in practice, not just
  in theory.

**Links.** [contribute.md](./contribute.md), [verification.md](./verification.md).

---

## Phase 3 — The integrator

**Rung: [S2](./user-scenarios.md#the-ladder) — established at this gate.** Push, and seconds later
it's integrated; the operator never waits for CI, even solo.

**Goal.** Stop writing `main` by hand. Route every change through a request-and-react flow, even
solo.

**What gets built.** Two things:

- **The structural invariant.** The one-line `pre-receive` hook: only the integrator key writes
  `refs/heads/<protected>`; attestation refs are create-only; everything else is open
  ([contribute.md](./contribute.md), _The one invariant_; [architecture.md](./architecture.md), _The
  one structural git invariant_).
- **The integrator controller.** Pull-based, subscribing to `integration-requested`. Verifies
  signature + attestation + (for now, self-) trust, does FF/rebase into `main`, emits outcome events
  ([architecture.md](./architecture.md), _a pull-based integrator_). Merge-queue semantics fall out
  for free. The required attestation set is a function of the diff, not of the contributor's claim
  about it: policy maps path classes to required checks — code takes the full suite, knowledge-only
  changes (`.the-valley/**`) take signature plus knowledge lint, mixed commits take the max of
  everything touched. That is also what keeps knowledge-node changes cheap through the same
  protected path: one invariant, proportionate checks.

The integrator is designed around **change objects** — a diff targeting a stream, with identity
stable across rebases — rather than branches, per the adopted direction in
[ida-93e4f91](../.the-valley/ideas/ida-93e4f91-changes-not-branches.md).

This is where the user **stops pushing directly to `main`** and goes through the request flow, even
as the only contributor. That's the whole point — the flow has to be tolerable at N=1 before it's
asked to hold at N>1.

**The design claim it validates.** The controller pattern and the core integration claim — that a
pull-based integrator is a better shape than a `pre-receive` gate, and that staleness is the right
unified failure mode ([architecture.md](./architecture.md)).

**Exit criteria.**

- Direct pushes to `refs/heads/main` are rejected by the hook; only the integrator key succeeds.
- A change lands via `integration-requested` → `integration-succeeded` with no manual ref write.
- A stale case (rebase would change the tree) surfaces as one `request-stale` event, not a rejection
  or a retry storm.

**Links.** [architecture.md](./architecture.md) (_a pull-based integrator_, _The one structural git
invariant_).

**New open questions.** Integrator self-integration — the integrator is code in a repo; how does
_its_ changes get integrated? Already tracked in [openquestions.md](./openquestions.md) (_Identity &
trust bootstrapping_). Phase 3 is where the chicken-and-egg becomes concrete.

---

## Phase 4 — Agents as first-class authors

**Rung: [S3](./user-scenarios.md#the-ladder) — established here; its attribution claim is hardened
by [Phase 6](#phase-6--trust-backstop).**

**Goal.** An agent's change lands with the same guarantees as the operator's, attributed to the
agent that made it, with no human babysitting the pipeline.

**What gets built.** Deliberately thin — the integration flow is unchanged from Phase 3, and that's
the point:

- **Per-agent identity.** Each agent signs commits and attestations with its own key. Attribution
  lives in the git objects and the log, not in a platform sidecar; the landed history distinguishes
  the agent's work from the operator's.
- **Dispatch against the graph.** klaus dispatch targets an outcome node, not a GitHub issue, and
  agents read and write knowledge nodes as part of the work — S3's knowledge increment. The
  convention is already live in [.the-valley/](../.the-valley/README.md); this phase makes it the
  pipeline's shape instead of a habit.

This phase retires S1's interim mode — agents pushing a branch for the operator to review and merge
by hand
([user-scenarios.md § S1](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them)).
Feeling that mode's pain was this phase's motivation, by design.

**The design claim it validates.** The pipeline holds with no human in it: a non-human author lands
with the same guarantees, and attribution is recorded, not inferred. What this phase deliberately
does not claim: that attribution can't be forged — signature-based attribution is only as strong as
key custody until the tlog lands ([Phase 6](#phase-6--trust-backstop)). For one operator and their
own agents, that is the honest cut.

**Exit criteria.**

- A klaus-dispatched change lands via `integration-requested` → `integration-succeeded` with no
  human action between dispatch and landing.
- The landed history attributes the change to the agent's own key, distinguishable from the
  operator's.
- The dispatch's target is an outcome node in the repo, and the work reads and writes knowledge
  nodes end to end.

- A klaus-dispatched change lands via `integration-requested` → `integration-succeeded` with no
  human action between dispatch and landing.
- The landed history attributes the change to the agent's own key, distinguishable from the
  operator's.
- The dispatch's target is an outcome node in the repo, and the work reads and writes knowledge
  nodes end to end.

**Links.** [architecture.md](./architecture.md) (_Identity / access_ row),
[scenarios.md #2](./scenarios.md) (klaus-style agent change),
[user-scenarios.md](./user-scenarios.md).

---

## Phase 5 — Effectful reactions (armstrong-as-controller)

**Rung: [S4](./user-scenarios.md#the-ladder) — established here**, including its knowledge
increment: knowledge changes become observable events, and a landed change can close the outcome it
serves.

**Goal.** Prove the causality chain end to end: a commit becomes a build becomes a deploy becomes a
notification, all as reactions on the log.

**What gets built.** armstrong subscribes to `integration-succeeded` → `nix build` the artifact
derivation → deploy / notify ([scenarios.md #1](./scenarios.md)). This is the controller-shaped
successor to the current Actions-based armstrong — the same tool, inverted from push-based pipeline
to reactive subscriber. Alongside it, the knowledge graph joins the causal record: node changes
surface on the bus like any ref update, and a landing can flip the outcome node it serves to done —
the graph stops being write-only.

**The design claim it validates.** Reactive controllers replace push-based CI/CD, and the causality
chain (commit → build → deploy → notify) is _one queryable history_ rather than seven disconnected
job UIs ([architecture.md](./architecture.md), _Components_ and _a log, not a workflow engine_).

**Exit criteria.**

- An integration into `main` triggers a build and a deploy with no workflow file — only a
  subscriber.
- The full chain for a given commit is reconstructable from the log alone.
- A landed change closes the outcome node it serves, and that closure is visible in the log like any
  other event.

**Links.** [architecture.md](./architecture.md) (_Components_),
[scenarios.md #1 and #6](./scenarios.md).

---

## Phase 6 — Trust backstop

**Rungs: [S3](./user-scenarios.md#the-ladder) hardened, [S5](./user-scenarios.md#the-ladder)
enabled.** Attribution gains non-repudiation — "can't be waved through or forged" now holds
adversarially — and trust becomes grantable, bounded, and revocable, which is what lets a second
human land a change without anything platform-shaped. S5 counts as established when a real
collaborator lands a real change through this machinery.

**Goal.** Make attestations trustworthy under adversarial or multi-party conditions, not just
convenient.

**What gets built.**

- **Transparency log.** Tessera-backed tlog publication of attestations via tesseract, with
  inclusion proofs appended as a sidecar ([contribute.md](./contribute.md)).
- **Witness re-derivation.** A re-verifier that re-derives any attestation whose runner kind claims
  purity and emits confirm/deny events.
- **Trust controller.** Scores per signer from confirm rates, with revocation
  ([architecture.md](./architecture.md), _attestation with revocation_).

Together these enable the untrusted-contributor and agent-identity scenarios that Phase 2's
local-only attestations can't.

**The design claim it validates.** The security model — roughly SLSA Level 3 for purity-claiming
checks, plus non-repudiation from the tlog ([verification.md](./verification.md), _The mechanism
stack_). Purity is the runner kind's claim, carried in the attestation schema — the `nix` runner is
the reference implementation of a strong claim, and the model degrades honestly for runners that
claim less.

**Exit criteria.**

- Every attestation lands in the tlog with a verifiable inclusion proof.
- A deliberately-wrong purity-claiming attestation is caught by the witness and lowers the signer's
  trust score.
- An untrusted signer's change integrates _only_ via the trust flow
  ([scenarios.md #4](./scenarios.md)), never by default.

**Links.** [verification.md](./verification.md), [contribute.md](./contribute.md),
[scenarios.md #2 and #4](./scenarios.md).

---

## Phase 7 — Feedback & incident memory

**Rung: [S6](./user-scenarios.md#the-ladder) — established here.** It also completes S4's feedback
story: who needs to know about which event, without a firehose.

**Goal.** Unbundle review — the last GitHub-shaped interaction — from the vendor UI, and make
incidents part of the project's durable memory.

**What gets built.**

- **Threads.** Derived views over events, scoped to a change or chain
  ([architecture.md](./architecture.md), _review is observability + feedback_). PR-as-thread — the
  "PR" becomes a named query, not a stored object.
- **Priority/attention router.** The routing subsystem that decides who needs to know about which
  event ([architecture.md](./architecture.md), same section).
- **Incident memory.** S6's knowledge increment: an incident files its own node, attributing the
  regression to the change that caused it — with the attribution carrying its uncertainty honestly,
  because a confidently wrong answer is worse than none — and linking the revert. The war story
  becomes a queryable record.

There is no "build the knowledge graph" item here: the graph arrived one increment per rung — plain
files ([Phase 0](#phase-0--mvp-repos-off-github)), agent-written
([Phase 4](#phase-4--agents-as-first-class-authors)), observable
([Phase 5](#phase-5--effectful-reactions-armstrong-as-controller)), incident-filing (here) — exactly
as the ladder grows it.

**The design claim it validates.** Observability + project-knowledge unbundling — that review is a
special case of feedback, incidents are a special case of knowledge, and both belong to one history
rather than scattered across issues, wikis, and heads ([architecture.md](./architecture.md),
_Observability & feedback_, _Project knowledge_ rows).

**Exit criteria.**

- A change accrues discussion, an approval, and an outcome as one chronology with no PR object
  anywhere.
- The router surfaces one genuinely high-priority event to a human without a firehose.
- A bad deploy — real or gamedayed — ends as an incident node with attribution, stated uncertainty,
  and a link to the revert.

**Links.** [architecture.md](./architecture.md) (_review is observability + feedback_, _project
knowledge is a typed-node graph_), [scenarios.md #3 and #4](./scenarios.md).

**New open questions.** None new here — but note the _priority-layer architecture_ question
([openquestions.md](./openquestions.md), _Attention, routing, and threads_) is the hardest new
bottleneck the whole design creates, and Phase 7 is where it stops being hypothetical.

---

## Cross-cutting threads

Some things aren't a phase; they run through all of them.

- **CUE schemas.** One schema language across the system. Event schemas are shared across producers
  and consumers, reused from armstrong — minimal in Phase 1, growing a field or a type each phase.
  Host config already speaks it ([schema/valley.cue](../schema/valley.cue), shipped in Phase 0);
  knowledge-node frontmatter and verification policy are candidates to follow. Schema evolution is a
  standing concern, not a phase — tracked in [openquestions.md](./openquestions.md) (_Storage,
  retention, and evolution_).
- **The `valley` CLI.** A thin tool that accretes one verb per phase — `valley migrate` (0),
  `valley tail` (1), `valley attest` or the `nix run .#attest` helper (2), `valley browse` (7). It
  stays thin on purpose: the contributor protocol is native-git-first
  ([contribute.md](./contribute.md)), so the CLI is convenience, never the critical path. Anything
  `valley` does, plain git and `nix` can do.
- **Durability as a standing priority.** Phase 0 makes it explicit, but it never stops mattering.
  The durable substrate is git objects + attestation refs + the tlog — all replicable, all
  externally witnessable. The bus is the one replaceable component: lose it and rebuild it from git.
  Every phase should preserve that property — if a phase makes the bus load-bearing for durable
  state, that's a design smell to catch.
- **Portability as a standing constraint.** Nix is a backend, not the substrate: portable schemas
  from day 0, portable implementations on demand. Every schema this plan ships — host config,
  events, attestations, knowledge nodes — must be implementable without Nix; the NixOS module and
  checks-as-derivations are reference implementations, never the contract. The review heuristic: a
  field only Nix can produce is a leak.

## Open questions

This document originally raised two; one is now decided, one remains open:

- **Offsite replication mechanism — decided**
  ([dcr-d7952bc](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md)).
  Push-triggered git-native mirroring to an independent live remote (pushed = replicated) — during
  migration that remote is GitHub, with the dedicated sovereign live remote deferred until GitHub
  exit — plus periodic encrypted restic-style backup for offsite depth; block-level replication (ZFS
  send) rejected for now. Details in [Phase 0](#phase-0--mvp-repos-off-github).
- **Phase-0 identity is Tailscale-ACL-based** (_Identity & trust bootstrapping_). Thin by design and
  swappable; the open question is _when_ it has to grow and into what — likely driven by
  [Phase 6](#phase-6--trust-backstop). _Origin: roadmap.md._

Everything else this roadmap touches is already tracked in [openquestions.md](./openquestions.md) —
integrator self-integration, the priority-layer architecture, attestation expiry vs. cache
retention, agent identity, and schema evolution all surface at specific phases above.
