---
type: idea
id: ida-51605e8
status: exploring
title: Authenticity must not be coupled to git
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Authenticity must not be coupled to git

the-valley does not intend to be tightly coupled to git, so git commit signing is a poor carrier for
authenticity.

The signing primitive is not the problem. SSHSIG detached signatures cover an arbitrary payload
under a caller-chosen namespace and verify against an allowed-signers list, with no git object
involved anywhere. Git merely adopts that same primitive for commit signing. The primitive is
portable; it is the framing around it that binds.

What is coupled is the subject. Signing a commit object, and keying an attestation by commit hash,
are both statements about git's data model rather than about the change being approved.

That coupling is not merely inelegant — it is defective for approvals. A commit hash does not
survive a rebase, the integrator rebases as it integrates, and the integration primitive is already
defined as a change whose identity is stable across rebases ([[ida-93e4f91]],
[ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)). An approval bound to
a commit hash therefore stops referring to what actually lands.

The direction, not yet designed: a self-contained signed statement that identifies its subject by
content, so that git storage becomes a backend rather than the contract. This repeats a move the
design has already made twice. Checks-as-derivations is the reference runner and not the contract
([[ida-b9f646c]],
[ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)), and
durability attaches to project state rather than to git.

## Open

- What identifies the subject.
- Where signed statements live when the store is not git.

## Related

- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — the act being carried.
- [contribute.md](../../design/contribute.md) and
  [roadmap.md, Phase 2](../../design/roadmap.md#phase-2--attestations-verification-mvp) — the places
  currently carrying the git-shaped framing.
