# the-valley

> "Whatever."

A space to rethink developer workflow and source management from scratch.

This is not a project to "move off GitHub." That framing is too narrow. The question is: **what
would source management look like if designed today**, with AI agents as first-class authors and no
assumption that a single platform should own hosting, identity, CI, artifacts, review, and
automation?

## The pain

GitHub bundles seven concerns — hosting, identity and access, verification and artifacts,
automation, integration, observability and feedback, and project knowledge — and does several of
them poorly enough that unbundling looks attractive:

- **CI is slow and YAML-shaped.** The feedback loop that matters most runs in minutes on someone
  else's infrastructure, configured in a language nobody chose.
- **The pull request conflates four things** — coordination, gating, record, and notification — and
  the bundle serves each worse than a mechanism designed for it would.
- **Agents are second-class.** The platform is built around a human clicking through a web UI. An AI
  agent authoring changes, reading project knowledge, or reacting to events is an afterthought
  bolted on through APIs designed for something else.
- **Project knowledge has no home.** Issues, wikis, docs, scratch files, and heads — scattered,
  unstructured, and invisible to the agents doing a growing share of the work.

## The promise

Compose small tools over a durable substrate you own. Each concern gets the mechanism it deserves
instead of the bundle's compromise, and everything that matters survives the loss of any one host.

## Where this goes

At its most general, the-valley is a **recursive, transparent outcome-production engine**. Outcomes
chain and recurse on a dependency graph — "add this code to a VCS" serves "deliver a feature users
love," which serves something larger still — and the system's job is to complete that graph,
transparently. The software development lifecycle is the v1 reference implementation, not the
boundary: everything above is how the engine gets pointed at code first. Recursive includes the
engine itself — the-valley builds the-valley.

## Constraints

- **Open source.**
- **Minimal.** Prefer composing small tools over building a platform.
- **Nix-native.** Builds, verification, and artifacts are derivations.
- **Decentralized where possible.** Accept centralization only where ordering or coordination
  genuinely require it, and be explicit about it.

## Status

Phase 0 in progress. The repo ships its first artifacts: a CUE schema declaring what a valley host
serves ([`schema/valley.cue`](./schema/valley.cue), example in
[`examples/host.cue`](./examples/host.cue)) and a NixOS module ([`flake.nix`](./flake.nix)) that
installs a host from that declaration. The plan-of-record — incremental, MVP-first, validation-gated
— is [`design/roadmap.md`](./design/roadmap.md).

## The docs

- [`design/user-scenarios.md`](./design/user-scenarios.md) — the escalating ladder of problem-space
  user scenarios the requirements derive from; only the top-priority rung carries acceptance
  criteria.
- [`design/requirements.md`](./design/requirements.md) — what the system must be: who it's for, the
  unbundled needs, the constraints, non-goals.
- [`design/architecture.md`](./design/architecture.md) — the bets and their rationale: the event
  log, attestations instead of CI gates, the pull-based integrator, review as feedback, knowledge as
  a graph.
- [`design/contribute.md`](./design/contribute.md) — the contributor protocol: what a human or agent
  does to push a change and request integration.
- [`design/verification.md`](./design/verification.md) — pure vs. effectful checks and what makes an
  attestation hard to forge.
- [`design/scenarios.md`](./design/scenarios.md) — end-to-end walk-throughs and what each stresses.
- [`design/self-transparency.md`](./design/self-transparency.md) — **DRAFT.** A candidate invariant,
  deliberately unresolved: no actor can durably change the system or an output without transparency,
  recursively. Names the facets; decides nothing.
- [`design/openquestions.md`](./design/openquestions.md) — consolidated open questions, tagged by
  layer.
- [`design/roadmap.md`](./design/roadmap.md) — the incremental validation plan.
- [`bin/valley`](./bin/valley) — the integrator's CLI: the Phase 0 verbs (`pending`, `review`) as a
  plain shell script, accreting one verb per phase per the roadmap's CLI thread.
