# the-valley

> "The valley spirit never dies; it is named the mysterious female. The gateway of the mysterious
> female is called the root of heaven and earth." — _Tao Te Ching, Chapter 6_

A clean-slate design for source management, verification, and developer workflow.

The question behind this project is simple: **What would source management look like if designed
today from first principles?**

If we assume AI agents are first-class authors and no single vendor should own hosting, identity,
CI, artifacts, review, and automation, how would we build the substrate?

## The Problem with the Monolith

Modern software development relies heavily on centralized platforms like GitHub. While convenient,
bundling every concern into a single hosted platform creates friction that compounds as software
development shifts toward autonomous agents:

- **CI is slow, brittle, and YAML-shaped.** The core verification loop takes minutes on third-party
  infrastructure, configured in a proprietary dialect decoupled from local development.
- **The pull request conflates four distinct concerns.** Coordination, gating, historical record,
  and notifications are forced into a single web page. Each concern is served worse than a
  purpose-built mechanism would serve it.
- **Agents are treated as second-class citizens.** Centralized platforms are built around human
  operators clicking in a web browser. Agents that author code, inspect documentation, or react to
  events must navigate APIs designed for external bots.
- **Project knowledge is fragmented.** Issues, pull request discussions, wikis, and design documents
  are scattered across proprietary databases, invisible to the tools and agents doing the daily
  work.

## The Valley Model

In Daoist philosophy, the valley is powerful because it stays low, open, and receives all streams
without grasping or imposing artificial shape.

`the-valley` applies this principle to software development: **compose small, focused tools over a
durable substrate you own.** Instead of forcing all development through a monolithic cloud platform,
each concern receives an unbundled, purpose-built mechanism:

- **Hosting:** Sovereign bare git repositories over Tailscale, backed up offsite.
- **Verification:** Hermetic Nix derivations producing cryptographic attestations locally in
  seconds, replacing slow remote CI gates.
- **Integration:** A pull-based integrator controller that validates attestations and merges changes
  without synchronous pre-receive blocking.
- **Automation:** An event log (NATS JetStream) where repository ref updates, builds, and deploys
  are projected as replayable events.
- **Project Knowledge:** A typed markdown graph (`.the-valley/`) stored directly in the repository
  alongside the code, navigable by humans and agents alike.

## The Ultimate Destination: An Outcome Engine

At its fullest expression, `the-valley` is a **recursive, transparent outcome-production engine**.

The fundamental unit of work is an **outcome**—a clear statement of a desired state that does not
yet exist. Outcomes form a directed acyclic graph (DAG) of dependencies. Schedulers inspect this
graph, identify the unblocked frontier of work, and dispatch agents or human operators to complete
it.

Because the system is recursive, **the valley builds the valley**: its own configurations, policies,
and controller improvements are tracked as outcomes and integrated through the same verified
pipeline as user code.

## Core Constraints

- **Open Source:** The substrate must remain fully inspectable, portable, and forkable.
- **Minimal:** Prefer composing small, single-purpose tools over constructing a complex monolithic
  platform.
- **Nix-Native:** Builds, verification, and artifacts run as hermetic Nix derivations. Nix serves as
  the reference backend, while schemas remain portable.
- **Decentralized by Default:** Centralization is accepted only where strict ordering or
  coordination genuinely requires it.

## Current Status

`the-valley` is currently in **Phase 0 (Durable Self-Hosting & Knowledge v0)**:

- **Host Declarations:** Declarative CUE schemas ([`schema/valley.cue`](./schema/valley.cue)) define
  valley hosts, projects, push mirrors, and backup policies.
- **NixOS Module:** A NixOS module ([`flake.nix`](./flake.nix)) installs and manages a sovereign
  valley host from that declaration.
- **Event Bus:** An initial event bus projects git ref updates as structured events
  ([`schema/events.cue`](./schema/events.cue)), observable via `valley tail`.
- **Knowledge Graph Conventions:** A frontmatter schema ([`schema/node.cue`](./schema/node.cue)) and
  flake lint (`lib.knowledgeLint`) validate typed markdown nodes across repositories.

## Documentation Roadmap

- [**design/user-scenarios.md**](./design/user-scenarios.md) — The escalating ladder of user
  scenarios (S1 through S7) that ground all system requirements.
- [**design/requirements.md**](./design/requirements.md) — The functional requirements, constraints,
  and non-goals derived from user scenarios.
- [**design/architecture.md**](./design/architecture.md) — Architectural choices, component
  responsibilities, and technical rationale.
- [**design/integration.md**](./design/integration.md) — Specification for integration requests,
  optimistic concurrency control, and verdict evaluation.
- [**design/verification.md**](./design/verification.md) — Specifications for pure vs. effectful
  checks, tree digests, and cryptographic attestations.
- [**design/contribute.md**](./design/contribute.md) — Step-by-step protocol for human and agent
  contributors.
- [**design/self-transparency.md**](./design/self-transparency.md) — Invariant analysis: ensuring no
  actor can durably change the system without transparent record.
- [**design/openquestions.md**](./design/openquestions.md) — Open technical and architectural
  questions.
- [**design/roadmap.md**](./design/roadmap.md) — The phased validation plan.
- [**bin/valley**](./bin/valley) — The valley CLI utility for inspecting pending requests, reviews,
  and verification policies.
