# Architecture

The bets, each traced to a requirement ([requirements.md](./requirements.md)) and taken no deeper than rationale. Detailed design lives in the rest of `design/`, and only for roadmap phases that are current or next — later phases get their detail re-fleshed when they start, informed by what validation has taught us.

## Shape of the system

```
  bare git (Tailscale)  ──┐                       ┌──► nix build       (pure reactions)
  build outputs        ───┤                       │
  agent runs (klaus)   ───┼──►  event bus  ──►  subscribers ──► armstrong  (effectful reactions)
  deploy state         ───┤    (NATS JS)          │
  external signals     ──┘                        └──► integrator     (per-repo integration)

  attestations published to a Tessera-backed transparency log on every push
```

## The concerns, unbundled

| Concern             | Current state                  | Direction                                              |
| ------------------- | ------------------------------ | ------------------------------------------------------ |
| Hosting             | Works fine, lock-in is the cost | Bare git over SSH on a Tailscale-reachable box         |
| Identity / access   | Works fine                     | Tailscale ACLs + SSH keys                              |
| Verification        | Slow, YAML-shaped              | `nix flake check` triggered by events                  |
| Artifacts           | Opaque, ephemeral              | Nix derivations, content-addressed, pushed to a cache  |
| Automation          | Actions: push-based, brittle   | Reactive controllers subscribing to an event log       |
| Integration / merge | Branch protection in vendor UI | Pull-based integrator controllers reacting to integration-requested events |
| Observability & feedback | PR-shaped, conflates four concerns | Continuous feedback as events; threads as derived views; priority/routing as a first-class subsystem |
| Project knowledge   | Scattered: issues, wikis, docs, scratch files, agent histories, heads | Unified typed-node graph in markdown, equally navigable by humans and agents; discussion threads are themselves nodes |

## Bet: git as event source — a log, not a workflow engine

*Serves: automation, observability.*

GitHub Actions, GitLab CI, Jenkins — all push-based pipelines. An event happens, a workflow runs, it exits, the trail is a log file.

A reactive log inverts this:

- **Causality is preserved.** The chain of "commit → build → deploy → notification" is one queryable history, not seven disconnected job UIs.
- **Reactions are independent.** Adding a new subscriber doesn't touch any other subscriber or any workflow file.
- **Replay is free.** Want to know what would have happened if a reaction had been different? Replay the log against a new subscriber.
- **No central workflow file.** Each subscriber owns its own logic and lifecycle.

Per-repo events (refs, attestations, integration requests) are durable in git itself; the bus is a projection that can be rebuilt. Only ephemeral cross-system events (deploys, metrics, agent runs) have the bus as their source of truth — so the log is never load-bearing for the crown-jewel data.

Prior art: Atomist (defunct, but had this model). Kubernetes controllers. Datomic / event sourcing generally. `git` itself — commits are events, refs are views.

## Bet: attestation with revocation, not CI as gate

*Serves: verification (feedback in seconds), agents as first-class authors.*

The contributor's own hermetic environment runs the canonical checks and produces a signed attestation of what ran, against what inputs, with what result. Subscribers act on the attestation immediately; re-verifiers cross-check asynchronously; trust is *measured* per attester from re-verification confirm rates, and divergence revokes it.

This trades two failure modes:

| Model | Failure mode |
| --- | --- |
| CI-as-gate | False negatives — slow feedback, flakes, infra outages block correct changes |
| Attestation | False positives — a bad attestation can land before the re-verifier catches it |

For most environments — personal projects, small teams, trusted contributor sets — false positives that are *detected and revocable* are a much better trade than a slow, brittle gate. The primary win is latency; the security properties are a side effect of doing it well. See [verification.md](./verification.md) for what makes an attestation hard to forge.

## Bet: a pull-based integrator, not a pre-receive gate

*Serves: integration (observable, policy-driven), architectural minimalism.*

The simplest option is a `pre-receive` hook that checks attestations on every push to a protected branch. It works — but it makes integration a different *shape* from the rest of the system, a synchronous special case whose failure mode is a terse line of stderr. The design instead adopts a **pull-based integrator**: a controller subscribing to integration-request events, performing integration as a reaction, emitting outcome events back into the log.

| | Pre-receive gate | Pull-based integrator |
| --- | --- | --- |
| Where policy lives | Hook script on git server | Controller subscribing to events |
| Failure mode | Push error to stderr | Outcome event in log, threadable |
| Multiple policies | Hard (one hook per repo) | Trivial (multiple subscribers) |
| Async validation | No — must answer before push completes | Yes — controller takes its time |
| Re-running on transient failure | Re-push | Re-fire the event |
| Architectural consistency | Special case | Same pattern as everything else |

Merge-queue semantics fall out for free: requests targeting the same ref serialize in the integrator. And the failure model is deliberately unified — there is no rejection, only **staleness**: a request that can't progress against current policy is marked stale, once, and refreshing it is the branch owner's problem, not the integrator's.

### The one structural git invariant

The bare repo enforces exactly one thing: **only the integrator's key may write protected refs**. Everything else — topic branches, attestation refs, integration requests — is wide open to anyone with push access. That's a one-line `pre-receive` hook; all complex policy lives in the integrator.

## Bet: review is observability + feedback

*Serves: observability & feedback; dissolves the "how do humans review agent code" question.*

"Review" is one slice of a larger problem: how the system gives feedback to anyone or anything that needs it. The PR-as-page model conflates:

| Concern | What it does | Failure mode today |
| --- | --- | --- |
| Coordination | A place humans converge to discuss a change | Discussion thread is locked to the PR; orphaned at merge |
| Gating | Correctness checks must pass before merge | Slow, brittle, false negatives |
| Record | Persistent trace of why a change happened | Lives inside one vendor's database |
| Notification | Someone needs to look at this | Firehose; no useful prioritization |

Unbundled: **gating** is handled by attestations; the **record** is the event log itself; **coordination** happens in threads — named, persistent *views* over events scoped to a change, derived rather than stored; **notification** becomes a priority/routing subsystem, which is the hard new bottleneck the design creates. The PR object disappears — what people call "a PR" becomes a query with a name. Pre-merge human review becomes the exception: humans engage when the priority layer flags something for them, and feedback accrues continuously before and after a change lands, on the same machinery.

## Bet: project knowledge is a typed-node graph

*Serves: project knowledge & discussion, agents as first-class authors.*

Everything in a project that isn't executable code or user-facing docs — bugs, principles, decisions, ideas, threads — is a **typed node**: a markdown file whose YAML frontmatter carries the structured layer (type, id, status, typed edges) and whose body carries prose. The graph lives at the repo root, clones with the code, and is equally navigable by a human with `grep` and an agent parsing frontmatter. Nodes are signed by the same keys as commits; discussion threads are themselves nodes; and active principles can become load-bearing on integration policy. Not a wiki, not Obsidian, not Jira — a project substrate that exposes a knowledge-tool-shaped surface.

## Components

One line each; these are the current picks, not commitments:

- **Hosting** — bare git over SSH; each repo is just a directory; `cgit` or similar for browsing if needed.
- **Bus** — NATS JetStream; single binary, persistent streams, runs on the same Tailscale box; replicate later if needed.
- **Transparency log** — Tessera-backed tlog via [tesseract](https://github.com/transparency-dev/tesseract); every attestation lands with an inclusion proof, giving non-repudiation and external auditability without depending on any one host.
- **Pure reactions** — Nix derivations; inputs are events, outputs are content-addressed artifacts, replayable from the log.
- **Effectful reactions** — [armstrong](https://github.com/gunk-dev/armstrong) as a Go controller; the controller-shaped successor to the current Actions-based armstrong.
- **Agent dispatch** — klaus, with its existing run-budget mechanism.
- **Schemas** — CUE, already used by armstrong; shared across event producers and consumers.
