# the-valley

> 谷神不死，是謂玄牝。玄牝之門，是謂天地根。
> *The valley spirit never dies. It is called the mysterious female. The gateway of the mysterious female is the root of heaven and earth.*
> — Tao Te Ching, ch. 6

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
| Observability & feedback | PR-shaped, conflates four concerns | Continuous feedback as events; threads as derived views; priority/routing as a first-class subsystem |
| Issues / discussion | Bolted on                      | Subsumed into feedback; threads can scope to any change, chain, or topic |

## Shape of the system

```
  bare git (Tailscale)  ──┐
  build outputs        ───┤
  agent runs (klaus)   ───┼──►  event log  ──►  subscribers
  deploy state         ───┤      (NATS JS)        │
  external signals     ──┘                        ├──► nix build  (pure reactions)
                                                  └──► armstrong  (effectful reactions)
```

- **Hosting**: bare git over SSH. Each repo is just a directory. `cgit` or similar for browsing if needed.
- **Bus**: NATS JetStream. Single binary, persistent streams, runs on the same Tailscale box. This is the one piece of unavoidable centralization — append-only ordering needs an authority. Replicate later if the box becomes a constraint.
- **Pure reactions**: Nix derivations. Inputs are events (refs, flake locks), outputs are content-addressed artifacts. Replayable from the log.
- **Effectful reactions**: [armstrong](https://github.com/gunk-dev/armstrong) as a Go controller. Subscribes to events and emits side-effects: deploys, notifications, agent dispatches, review-state changes. This is the controller-shaped successor to the current Actions-based armstrong.
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

1. **Attention routing.** Once feedback is continuous and event-shaped, the hardest job becomes deciding what reaches a human and when. The priority layer is load-bearing in a way no piece of GitHub is today.
2. **Discovery.** Without GitHub-the-social-graph, how do humans find each other's repos? Probably out of scope, but worth naming.
3. **The log is a single point of failure.** Accepted for now. Replication is cheap when it's needed.
4. **Schema evolution.** CUE handles validation, but event schemas will change. Migration story?

## Design notes

Longer thinking on specific subproblems lives in [`design/`](./design):

- [`verification.md`](./design/verification.md) — replacing CI-as-gate with local attestations and async re-verification.
- [`feedback.md`](./design/feedback.md) — reframing review as continuous observability and feedback; threads as derived views; attention routing as a first-class subsystem.

## Status

Sketch. Nothing built yet. This document is the design space, not a plan.
