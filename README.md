# the-valley

> "Whatever."

A space to rethink developer workflow and source management from scratch.

This is not a project to "move off GitHub." That framing is too narrow. The question is: **what would source management look like if designed today**, with AI agents as first-class authors, Nix as a substrate, and no assumption that a single platform should own hosting, identity, CI, artifacts, review, and automation?

## Constraints

- **Open source.**
- **Minimal.** Prefer composing small tools over building a platform.
- **Nix-native.** Builds, verification, and artifacts are derivations.
- **Decentralized where possible.** Accept centralization only where ordering or coordination genuinely require it, and be explicit about it.

## What GitHub does, broken out

GitHub bundles seven things. Most of them it does poorly enough that unbundling looks attractive:

| Concern             | Current state                  | Direction                                              |
| ------------------- | ------------------------------ | ------------------------------------------------------ |
| Hosting             | Works fine, lock-in is the cost | Bare git over SSH on a Tailscale-reachable box         |
| Identity / access   | Works fine                     | Tailscale ACLs + SSH keys                              |
| Verification        | Slow, YAML-shaped              | `nix flake check` triggered by events                  |
| Artifacts           | Opaque, ephemeral              | Nix derivations, content-addressed, pushed to a cache  |
| Automation          | Actions: push-based, brittle   | Reactive controllers subscribing to an event log       |
| Integration / merge | Branch protection in vendor UI | Pull-based integrator controllers reacting to integration-requested events |
| Observability & feedback | PR-shaped, conflates four concerns | Continuous feedback as events; threads as derived views; priority/routing as a first-class subsystem |
| Project knowledge   | Scattered: issues, wikis, docs, scratch files, agent histories, heads | Unified typed-node graph in markdown — bugs, principles, decisions, ideas, threads — equally navigable by humans and agents |
| Discussion          | Bundled with issues            | Threads are themselves nodes in the knowledge graph; scope to any change, chain, node, or topic |

## Shape of the system

```
  bare git (Tailscale)  ──┐                       ┌──► nix build       (pure reactions)
  build outputs        ───┤                       │
  agent runs (klaus)   ───┼──►  event bus  ──►  subscribers ──► armstrong  (effectful reactions)
  deploy state         ───┤    (NATS JS)          │
  external signals     ──┘                        └──► integrator     (per-repo integration)

  attestations published to a Tessera-backed transparency log on every push
```

- **Hosting**: bare git over SSH. Each repo is just a directory. `cgit` or similar for browsing if needed.
- **Bus**: NATS JetStream. Single binary, persistent streams, runs on the same Tailscale box. Carries cross-system events — events whose source isn't a single repo (deploys, metrics, agent runs) and projections of per-repo git events (ref updates, attestations, integration requests) for cross-cutting subscribers. Replicate later if needed.
- **Transparency log**: Tessera-backed tlog. Every published attestation lands here with an inclusion proof. Independently witnessable; gives non-repudiation and external auditability without depending on any one host.
- **Pure reactions**: Nix derivations. Inputs are events (refs, flake locks), outputs are content-addressed artifacts. Replayable from the log.
- **Effectful reactions**: [armstrong](https://github.com/gunk-dev/armstrong) as a Go controller. Subscribes to events and emits side-effects: deploys, notifications, agent dispatches, integrator dispatches. The controller-shaped successor to the current Actions-based armstrong.
- **Schemas**: CUE, already used by armstrong. Shared across event producers and consumers.

## Why a log, not a workflow engine

GitHub Actions, GitLab CI, Jenkins — all push-based pipelines. An event happens, a workflow runs, it exits, the trail is a log file.

A reactive log inverts this:

- **Causality is preserved.** The chain of "commit → build → deploy → notification" is one queryable history, not seven disconnected job UIs.
- **Reactions are independent.** Adding a new subscriber doesn't touch any other subscriber or any workflow file.
- **Replay is free.** Want to know what would have happened if a reaction had been different? Replay the log against a new subscriber.
- **No central workflow file.** Each subscriber owns its own logic and lifecycle.

Prior art: Atomist (defunct, but had this model). Kubernetes controllers. Datomic / event sourcing generally. `git` itself — commits are events, refs are views.

## Open questions

Consolidated across the design docs in [`design/openquestions.md`](./design/openquestions.md). Themes: identity & trust bootstrapping, policy & configuration, cross-repo coordination, attention/routing/threads, storage/retention/evolution, knowledge graph specifics, verification specifics, workflow patterns, discovery.

## Design notes

Longer thinking on specific subproblems lives in [`design/`](./design):

- [`contribute.md`](./design/contribute.md) — the tight contributor protocol: signed commit, local hermetic checks, signed attestation, tlog publication, atomic push of branch + attestation + integration request.
- [`verification.md`](./design/verification.md) — replacing CI-as-gate with local attestations and async re-verification.
- [`integration.md`](./design/integration.md) — pull-based integrator controllers in place of branch-protection gates; merge-queue semantics for free.
- [`integrator-internals.md`](./design/integrator-internals.md) — the integrator's loop: attestation-invariance under trivial rebase, staleness as the unified failure mode, policy as a derived query over active principles, strict FIFO per protected ref, crash recovery.
- [`feedback.md`](./design/feedback.md) — reframing review as continuous observability and feedback; threads as derived views; attention routing as a first-class subsystem.
- [`knowledge.md`](./design/knowledge.md) — typed-node graph in markdown for everything that isn't code or user docs: bugs, principles, decisions, ideas, threads. Frontmatter is the structured layer; body is freeform. Composes with attestations, integration, and threads.
- [`scenarios.md`](./design/scenarios.md) — end-to-end walk-throughs (solo dev, agent change, post-deploy regression, untrusted contributor, cross-repo, scheduled task) and what each tests or stresses.
- [`openquestions.md`](./design/openquestions.md) — consolidated open questions across all docs, grouped by theme with origin pointers.

## Status

Sketch. Nothing built yet. This document is the design space, not a plan.
