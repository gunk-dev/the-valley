# Requirements

What the system must be. No solutions here — those are [architecture.md](./architecture.md). Each requirement derives from a rung of the scenario ladder in [user-scenarios.md](./user-scenarios.md): the rung says what a user experiences; the requirement is what must hold for the rung to be true.

## Who it's for

- **v1: a solo developer plus AI agents as first-class authors** (S1–S4). The system must be pleasant at N=1 before it's asked to hold at N>1.
- **Later: small, trusted teams** (S5–S6). Invite-only contributor sets where trust is granted, measured, and revocable.
- **Explicitly not: public-social scale.** S7 is deferred, maybe never; it stays on the ladder only as the limit the trust model should degrade toward gracefully.

## The needs

1. **Never losable** (S1). Repos live on infrastructure the owner controls, no vendor lock-in — and project knowledge (outcomes, ideas, decisions, threads) travels with the repo, protected by the same motion that protects the code. Precisely what this means: [durability](#durability), below.
2. **Integrated in seconds** (S2). Push to integrated completes in seconds, resting on checks that are **trustworthy where the work was written** — a verifiable claim, not a hope — with reproducible build outputs so the claim can be checked.
3. **Agents as first-class authors** (S3). An agent's change lands with the same guarantees as the owner's own, unsupervised, attributable to exactly the agent that made it — attribution that can be neither waved through nor forged.
4. **Causality queryable from one history** (S4). Consequences — builds, deploys, notifications — follow without anyone kicking anything, each reaction independently addable and removable; "why did X occur" is answerable from one durable history, not reconstructed across tools.
5. **Trust grantable, bounded, revocable** (S5). A second, semi-trusted human lands a change without the owner administering accounts, roles, or a platform: exactly enough access — easy to grant, limit, and take back — without a vendor.
6. **Attributed incident memory** (S6). A landed change that goes bad gets attributed, reverted, and remembered: the incident becomes durable project knowledge, not a war story.
7. **Demand-shaped work** ([README: where this goes](../README.md#where-this-goes)). Work to be done is itself knowledge — outcomes on a dependency graph the system is under pressure to complete toward what someone actually asked for, not merely to record.

**The unbundling note.** GitHub's bundle — hosting, identity & access, verification & artifacts, automation, integration, observability & feedback, project knowledge & discussion — cuts across these rungs: hosting is need 1; verification, artifacts, and integration are 2; identity & access is 3 and 5; automation and observability & feedback are 4; project knowledge & discussion runs through 1, 6, and 7. The concerns remain the vocabulary the [architecture](./architecture.md) unbundles by; the rungs are why each is needed.

## Constraints

- **Open source.** The substrate must be inspectable and forkable; a closed dependency reintroduces the lock-in being escaped.
- **Minimal.** Small composed tools stay understandable and replaceable; platforms accrete.
- **Nix-native.** Hermetic, content-addressed builds are what make verification trustworthy and artifacts reproducible.
- **Decentralized where possible.** Centralization is accepted only where ordering or coordination genuinely require it, and must be explicit.

## Durability

The git data is the crown jewel; losing it is the one unrecoverable failure. Everything else — infrastructure, tooling, even the event history's ephemeral parts — is rebuildable. Durability is therefore a first-class requirement, not an operational afterthought — it is the whole of S1, the rung everything else stands on: multiple copies, offsite, with restores actually performed and verified before any copy becomes the only one.

## Non-goals

- Public-social scale, discovery, or spam resistance (S7: deferred, maybe never).
- Building a platform. This is a set of small tools over a substrate.
- Defense against a compromised developer machine signing genuine attestations of malicious code — hardware attestation is the only fix and is out of scope.
- Migration tooling for the world's existing repos and trackers. The pilot is our own.
