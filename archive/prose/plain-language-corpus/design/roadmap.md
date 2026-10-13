# Roadmap

This roadmap turns the architectural bets of the valley into an incremental validation plan. It sits
downstream of the [scenario ladder](./user-scenarios.md). The ladder defines what capabilities must
become true for a user and in what order. This document describes what we build to satisfy each
scenario, which design claims each phase tests, and the concrete gates that prove a phase is
complete.

Where a phase description and a scenario rung disagree, the scenario rung wins.

## Principles of the plan

The valley design relies on a chain of falsifiable bets: git as an event source, signed local
verification instead of push-based CI, pull-based integration, reactive controllers,
transparency-backed trust, and an in-repo knowledge graph.

Like water finding the natural contours of the terrain, our development moves from solid ground to
solid ground rather than erecting elaborate scaffolding ahead of reality. Four rules govern every
phase:

1. **Independently valuable.** Each phase delivers immediate utility even if subsequent phases never
   happen. Phase 0 delivers durable, sovereign git hosting with zero dependency on attestations or
   controllers.
2. **Independently stoppable.** Work can halt after any phase and leave behind a coherent,
   operational system rather than a half-wired prototype.
3. **Validates exactly one distinct claim.** Testing multiple unproven architectural bets in a
   single phase makes it impossible to isolate why something failed.
4. **Directly serves a scenario rung.** Every phase builds toward one or more rungs of the
   [scenario ladder](./user-scenarios.md#the-ladder). Work that does not serve a scenario rung does
   not belong on this roadmap.

We sequence milestones by validation gates rather than calendar dates. A phase is complete only when
its exit criteria are verified in practice.

## Phases at a glance

| Phase | Ships                                     | Scenario Rung           | Validates                                                            | Exit Gate                                                                           |
| ----- | ----------------------------------------- | ----------------------- | -------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| 0     | Sovereign git hosting; knowledge graph v0 | S1                      | Self-hosting is durable and reliable enough to trust as primary      | Verified backup restore + one week off GitHub for the pilot repo                    |
| 1     | The event log                             | S2 (1 of 3)             | Git ref updates cleanly project onto a log without a workflow engine | `valley tail` displays real-time ref updates from git pushes                        |
| 2     | Signed local attestations                 | S2 (2 of 3)             | Local verification ergonomics can replace waiting for CI             | `nix run .#attest` replaces remote CI runs for daily work                           |
| 3     | Pull-based integrator                     | S2 established          | Pull-based integration and staleness handling replace push-to-main   | Direct pushes to `main` cease entirely, even for solo work                          |
| 4     | Autonomous agent authors                  | S3 established          | Non-human authors land changes unsupervised with provenance          | A dispatched agent change integrates cleanly without human intervention             |
| 5     | Reactive controllers                      | S4 established          | Log-driven controllers replace monolithic CI/CD pipelines            | Commit, build, deploy, and notification form one unified, queryable event history   |
| 6     | Adversarial trust backstop                | S3 hardened; S5 enabled | Transparency logs and witness re-derivation backstop local claims    | An untrusted signer's change integrates only through verified trust evaluation      |
| 7     | Feedback & incident memory                | S6 established          | Code review is feedback; operational incidents become repo memory    | Incidents file structured graph nodes; review proceeds without pull request objects |

Scenario S7 (untrusted public strangers) has no dedicated phase because it is
[explicitly deferred](./user-scenarios.md#the-ladder). The trust architecture degrades gracefully
toward it without prematurely optimizing for it.

### Named pilot components

- **klaus**: Agent dispatch and budget management daemon.
- **armstrong**: Controller daemon (the Go-based reactive successor to push CI scripts).
- **tesseract**: Transparency log server backed by
  [transparency-dev/tesseract](https://github.com/transparency-dev/tesseract).
- **cosmo**: The operator's NixOS infrastructure configuration.
- **classic-laddie**: The initial pilot host on the operator's tailnet.

Specific hostnames represent pilot infrastructure rather than design requirements. The portable
system model is declared in [schema/valley.cue](../schema/valley.cue).

---

## Phase 0 — MVP: repos off GitHub

**Scenario Rung:**
[S1 — my repos live on my infrastructure and I can never lose them](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them).

### Goal

Move canonical git repositories off third-party platforms onto operator-controlled infrastructure
with high durability guarantees.

### What gets built

1. **Bare git hosting over SSH:** Installed via a declarative NixOS module
   ([flake.nix](../flake.nix)) consuming the CUE host schema
   ([schema/valley.cue](../schema/valley.cue)).
2. **Access control:** Enforced strictly via Tailscale ACLs and standard SSH keys
   ([architecture.md](./architecture.md)).
3. **Knowledge graph v0:** Markdown convention in [.the-valley/](../.the-valley/README.md) for
   issues, outcomes, ideas, and decisions. Validated via `nix flake check`.
4. **Replication & backup:** Push-triggered git-native replication to an independent remote (using
   GitHub as a transitional push mirror per
   [dcr-d7952bc](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md)),
   combined with periodic encrypted offsite backups for point-in-time recovery.

### What is deferred

No event bus, no attestations, and no integrator controller. Direct push to `main` is permitted
during this phase.

### Claim validated

Self-hosted bare git on existing infrastructure provides sufficient durability and ergonomics to
serve as the sole primary home for active projects.

### Exit gate

All criteria in
[S1's acceptance checklist](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them)
are satisfied, including verified backup restore drills.

---

## Phase 1 — The event log (the spine)

**Scenario Rung:** [S2](./user-scenarios.md#the-ladder) (Part 1 of 3).

### Goal

Turn repository ref updates into a structured, replayable event stream without introducing a complex
workflow orchestrator.

### What gets built

1. **NATS JetStream instance:** Running on the valley host.
2. **Git post-receive hook:** Projects ref updates into discrete bus events (`ref-updated`).
3. **CLI inspector:** A lightweight `valley tail` command to stream live events.

### What is deferred

No consumers or controllers act on events yet. The stream is strictly observational.

### Claim validated

Git ref updates project deterministically onto an append-only event log, establishing the log as the
foundational nervous system for all automation.

### Exit gate

- Pushing to a repository emits a corresponding `ref-updated` event visible via `valley tail` in
  under one second.
- Replaying the repository's ref log from scratch deterministically reconstructs the identical event
  stream.

_(Status: Validated on the pilot host on 2026-07-17. Follow-up items tracked in
[bd-d853d9c](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md) and
[bd-500adf7](../.the-valley/bugs/bd-500adf7-bus-shares-git-user.md).)_

---

## Phase 2 — Attestations (verification MVP)

**Scenario Rung:** [S2](./user-scenarios.md#the-ladder) (Part 2 of 3).

### Goal

Replace slow, asynchronous CI runs with instant, signed, locally produced test verifications.

### What gets built

1. **Attestation runner (`nix run .#attest`):** Executes declared derivations on the exact source
   tree, produces structured verification statements ([verification.md](./verification.md)), and
   signs them with the local host key.
2. **Git notes storage:** Attestation notes are pushed atomically to
   `refs/the-valley/attestations/<tree-digest>/<signer-key-hash>` alongside commit objects
   ([dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md)).

### What is deferred

Public transparency logging and witness re-verification are deferred to Phase 6. Phase 2 validates
the daily ergonomic workflow for trusted operators.

### Claim validated

Local verification combined with cryptographic signing provides a faster, more dependable feedback
loop than centralized CI.

### Exit gate

- Everyday changes to the pilot repository land with attached attestation refs.
- Pushes are executed in a single atomic git command without complex wrappers.
- The operator prefers this flow over waiting for GitHub Actions.

---

## Phase 3 — The integrator

**Scenario Rung:** [S2 established](./user-scenarios.md#the-ladder).

### Goal

Eliminate direct manual pushes to protected branches in favor of an automated, pull-based
integration controller.

### What gets built

1. **Structural git invariant:** A simple `pre-receive` hook that permits only the integrator's
   cryptographic key to update protected branch refs (`refs/heads/main`).
2. **Integrator daemon:** Subscribes to `integration-requested` events on the bus, verifies required
   attestations against the repository verification policy
   ([dcr-f41f718](../.the-valley/decisions/dcr-f41f718-declared-verification-policy.md)),
   fast-forwards or rebases valid changes, updates `main`, and emits outcome events
   ([integration.md](./integration.md)).
3. **Staleness detection:** Cleanly rejects stale requests when the target branch moves, prompting
   an automatic rebase rather than crashing or looping.

### Claim validated

Pull-based integration controllers with unified staleness handling eliminate merge queues,
pre-receive latency, and manual branch maintenance.

### Exit gate

- Direct pushes to `main` are rejected by the git hook.
- Changes land reliably via the integration request flow.
- A concurrent merge produces an orderly stale notification without thrashing.

---

## Phase 4 — Autonomous agent authors

**Scenario Rung:** [S3 established](./user-scenarios.md#the-ladder).

### Goal

Allow autonomous AI agents to propose and land changes with full provenance, using the exact same
integration path as human engineers.

### What gets built

1. **Provenance-based attribution:** Agent runs operate under host keys with structured metadata
   detailing the runner, model, prompt digest, and context hash embedded directly in the signed
   attestation ([ida-45178f6](../.the-valley/ideas/ida-45178f6-agent-identity-is-provenance.md),
   [ida-a8243d2](../.the-valley/ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)).
2. **Graph-driven dispatch:** Klaus dispatches tasks against structured outcome nodes in
   `.the-valley/`, reading context from the repository and updating the graph upon completion.

### Claim validated

The verification and integration pipeline operates autonomously without human supervision while
preserving clear, indelible attribution.

### Exit gate

- An agent-dispatched task verifies, requests integration, and lands on `main` without human
  intervention.
- The resulting commit and attestation record explicitly identify the agent run, distinguish it from
  human contributions, and preserve the prompt/model provenance.

---

## Phase 5 — Effectful reactions (armstrong-as-controller)

**Scenario Rung:** [S4 established](./user-scenarios.md#the-ladder).

### Goal

Orchestrate post-integration actions (builds, deployments, notifications) as decentralized,
event-driven controllers subscribing to the event bus.

### What gets built

1. **Armstrong controller:** Subscribes to `integration-succeeded` events, triggers Nix artifact
   builds, executes deployments, and emits lifecycle status events back to the bus.
2. **Knowledge graph reactivity:** Node modifications trigger event updates, allowing successful
   integrations to automatically transition linked outcome nodes to `closed`.

### Claim validated

Discrete event-driven controllers replace monolithic push pipelines, turning commit, build, deploy,
and operational status into a single cohesive, queryable history.

### Exit gate

- A merge to `main` automatically triggers deployment and notification solely through event
  subscriptions.
- The complete causal history from source commit to production deployment can be queried directly
  from the event log.
- Landed changes cleanly close linked outcome nodes.

---

## Phase 6 — Trust backstop

**Scenario Rung:** [S3 hardened; S5 enabled](./user-scenarios.md#the-ladder).

### Goal

Provide cryptographic non-repudiation and multi-party verification so external contributors and
distributed agents can interact safely without central platform gates.

### What gets built

1. **Transparency log:** Attestations are appended to a Tessera-backed transparency log with
   inclusion proofs.
2. **Witness re-verification:** Independent witness nodes re-evaluate deterministic checks and
   publish confirmation or denial statements.
3. **Trust & reputation controller:** Maintains dynamic trust scores for signer keys based on
   historical re-derivation accuracy ([verification.md](./verification.md)).

### Claim validated

Local attestations backstopped by transparency logs and witness re-derivation provide strong
security (comparable to SLSA Level 3) without requiring centralized build farms.

### Exit gate

- Attestations include verifiable transparency log inclusion proofs.
- An intentionally forged or failing attestation is flagged by witness nodes and reduces the
  signer's trust score.
- Contributions from untrusted keys are routed to witness verification before integration.

---

## Phase 7 — Feedback & incident memory

**Scenario Rung:** [S6 established](./user-scenarios.md#the-ladder).

### Goal

Unbundle code review, operational feedback, and incident analysis from centralized web UIs into
durable, decentralized data structures.

### What gets built

1. **Thread projections:** Code review discussions and feedback are structured as causal event
   threads anchored to change IDs rather than ephemeral platform PR objects.
2. **Attention router:** Intelligently routes high-priority notifications to relevant human
   maintainers based on context and ownership.
3. **Incident memory:** Post-incident investigations generate first-class nodes in `.the-valley/`,
   linking root causes, uncertainty bounds, and remediations directly to source commits.

### Claim validated

Code review and incident management are simply specialized projections of the universal event log
and knowledge graph, requiring no proprietary forge software.

### Exit gate

- A multi-turn code review, revision cycle, and approval conclude cleanly without creating a pull
  request object.
- The attention router surfaces critical alerts without noise or alert fatigue.
- A production regression generates an attributed incident node linked to the offending commit and
  its subsequent revert.

---

## Cross-cutting threads

Several foundational principles run through every phase of the roadmap:

- **Unified CUE schemas:** All configuration, events, and attestation formats use CUE as the
  canonical schema definition language ([schema/valley.cue](../schema/valley.cue)).
- **Minimal CLI footprint:** The `valley` CLI provides optional convenience verbs (`valley tail`,
  `valley attest`), but the underlying protocols remain standard git and Nix operations.
- **Git durability as primary:** The repository objects, attestation refs, and transparency log are
  the permanent sources of truth. The NATS event bus is always rebuildable from git state.
- **Portable implementations:** Nix is our reference execution environment, but all schemas and
  protocols are defined independently to allow alternative backends.

## Standing open questions

1. **Identity evolution:** Phase 0 relies on Tailscale ACLs and SSH keys. As the system moves toward
   Phase 6, we will determine how key management expands to support distributed and ephemeral keys
   ([openquestions.md](./openquestions.md#identity--trust-bootstrapping)).
2. **Integrator self-hosting:** How changes to the integrator itself are tested and integrated
   recursively ([openquestions.md](./openquestions.md#identity--trust-bootstrapping)).
3. **Attention routing architecture:** Balancing automated event routing against human cognitive
   limits ([openquestions.md](./openquestions.md#attention-routing-and-threads)).
