# Requirements

This document defines what `the-valley` must accomplish. It focuses strictly on requirements and
constraints without prescribing specific implementation mechanisms.

Every requirement derives directly from a rung on the
[scenario ladder](./user-scenarios.md#the-ladder). If a requirement does not serve a scenario on
that ladder, it does not belong in this specification.

## Target Audience and Scale

The system is designed for a specific progression of trust and scale:

1. **Version 1 (Solo Developer and Autonomous AI Agents):** Covering scenarios S1 through S4. The
   system must provide a fast, seamless experience for a single human operator collaborating with
   autonomous AI agents before expanding further.
2. **Later Phases (Small, Trusted Teams):** Covering scenarios S5 and S6. Designed for invite-only
   teams where access and trust are explicitly granted, scoped, and revocable without complex
   administrative overhead.
3. **Explicit Non-Goal (Public Social Scale):** Scenario S7 (untrusted public contribution and spam
   resistance) is deferred. The trust model is designed to degrade safely, but building a public
   social platform is out of scope.

## Core System Requirements

1. **Sovereignty and Durability (S1):** Repositories and project knowledge must live on
   infrastructure controlled by the operator. Daily development continues using standard git
   commands (`clone`, `commit`, `push`), while external platforms serve only as downstream mirrors.
   State is never lost: integrated work is replicated across independent locations within minutes.
2. **Instant Integration via Trustworthy Verification (S2):** Pushing code to integration must
   complete in seconds. Verification happens hermetically where the work is authored (on the
   contributor's machine or agent sandbox), producing verifiable cryptographic attestations.
   Integration simply checks attestations rather than repeating slow build jobs.
3. **Autonomous Agents as First-Class Authors (S3):** Changes authored by AI agents land under the
   exact same verification guarantees as human changes. Every agent run produces cryptographic
   provenance so authorship is strictly accountable and cannot be forged.
4. **Unified, Queryable Causality (S4):** Automated consequences—derivations, builds, deployments,
   notifications—react to repository events over a persistent event log. The causal chain ("why was
   version X deployed to host Y?") is queryable from a single durable history rather than stitched
   together from disparate logs.
5. **Scoped, Revocable Trust (S5):** Collaborators can contribute without requiring the operator to
   administer accounts, roles, or web dashboards. Access is granted, constrained, and revoked using
   cryptographic keys and simple policies.
6. **Attributed Incident Memory (S6):** When a deployment causes a regression, the system identifies
   the responsible change, assists in reversion, and records the incident as a permanent node in the
   project's knowledge graph. Blame and uncertainty are tracked honestly.
7. **Demand-Driven Outcome DAGs:** Work is represented as an executable dependency graph of
   **outcomes**—declarations of desired results. The system automatically tracks unblocked work and
   dispatches agents to complete it.

## Constraints

These constraints govern all solutions in `the-valley`:

- **Open Source:** The substrate must be open, transparent, and forkable to prevent vendor lock-in.
- **Minimalist Composition:** Prefer small, focused tools communicating over clean interfaces rather
  than monolithic platform engines.
- **Nix-Native Execution:** The reference implementation uses hermetic Nix derivations for
  reproducible builds and checks. The underlying schema contracts remain portable to other
  deterministic builders.
- **Single Execution Runtime:** Verification uses the same runtime locally, in agent sandboxes, and
  at integration. There is no separate "CI YAML dialect" or specialized test runner.
- **Off-Path Verification Latency:** The time required to run checks must not serialize the
  integration stream. Long-running tests cannot bottleneck independent incoming changes.
- **Auditable Supply Chains:** All build dependencies must be content-addressed and pinned so that
  build provenance is fully verifiable.

## Durability Invariant

The project's repository state—both code and knowledge graph—is the core asset. Infrastructure,
caches, and event projections can be rebuilt, but state cannot.

Therefore, durability follows a strict invariant: **integrated means replicated**.

- Every change merged into a primary branch must be replicated to at least two independent physical
  locations (including one offsite backup) within minutes.
- Durability is not assumed from configuration; it is verified by conducting and recording actual
  restore drills.

## Explicit Non-Goals

- **Public Social Forge Features:** Star counts, pull request discovery feeds, and social networking
  features are out of scope.
- **Administrative Platform Overhead:** The system avoids user account databases, web admin
  consoles, and complex permission hierarchies.
- **Compromised Host Defense:** Defending against a compromised local machine with stolen root keys
  signing malicious attestations requires hardware enclave attestation, which is outside the current
  scope.
- **Universal Migration Tooling:** The project does not attempt to provide turn-key migration
  scripts for every third-party issue tracker or git host.
