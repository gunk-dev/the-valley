---
type: bug
id: bd-e63bac8
status: open
title: Asynchronous attestation re-verification causes cascading invalidation on divergence
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Asynchronous re-verification causes cascading invalidation on divergence

The verification architecture chooses attestation with revocation over CI as a gate
([architecture.md](../../design/architecture.md)). A contributor's change lands on a protected ref
immediately upon presenting valid cryptographic attestations, while full re-verification of the
derivations happens asynchronously in the background. If an asynchronous re-verifier detects that an
attestation diverged, failed, or was forged, revoking trust does not cleanly undo the landed change.
Because git history is append-only, all subsequent changes landed on top of the invalidated commit
are tainted, creating a cascading invalidation crisis across in-flight and landed work.

## The failure mode of optimistic landing

The system trades CI latency for optimistic throughput: false positives that are detected and
revocable are accepted in place of slow, brittle pre-merge gates.

In practice, git branches are linear chains of commits. Consider a sequence of events:

1. Change A lands on `main` carrying a fraudulent or flawed attestation for a pure check.
2. Changes B, C, and D land on `main` over the subsequent twenty minutes, each rebased onto the tip
   containing A.
3. An asynchronous re-verifier finishes re-executing A's derivations and discovers that the check
   fails or that the recorded output digest was forged.
4. The system revokes trust in the attester.

Revoking trust in the attester does not alter the git tree. Resolving the defect on `main` leaves
two problematic choices:

- **Rewriting `main` (force-push).** Removing commit A from history breaks every contributor clone,
  invalidates all remote-tracking refs, and severs the cryptographic provenance of changes B, C, and
  D.
- **Landing a revert commit.** Committing a revert of A produces a new tree. This new tree
  invalidates the input-closure digests and merge bases of all active integration requests and
  in-flight branches across the entire project. Furthermore, if B, C, or D depended semantically on
  changes introduced by A, the revert introduces broken builds directly onto `main`.

In an autonomous multi-agent environment with high change velocity, a single diverged attestation
causes cascading invalidation across the entire outcome DAG and in-flight branch pool.

## Why this is acceptable today

All contributors today share the operator's identity and run checks on trusted hardware. Divergence
between local check execution and re-verification has not occurred because there are no semi-trusted
or untrusted signers. The defect becomes active as soon as semi-trusted human contributors (scenario
S5) or autonomous agent authors (scenario S3) push changes without operator supervision.

## Directions, not decisions

- **Staged integration streams.** Introduce a transient integration stream (such as `staging` or
  `next`) where changes land optimistically and are published to `main` only after background
  re-verification confirms the evidence.
- **Tiered verification by attester standing.** Require synchronous re-verification for new,
  unproven, or low-trust signers, while reserving instantaneous OCC landing for signers with high
  established confirm rates.
- **Blast-radius partitioning.** Restrict optimistic landing to path classes with zero system-level
  blast radius (such as `.the-valley/**` documentation and knowledge nodes). Require synchronous
  verification for foundational classes (NixOS modules, policy schemas, compiler tools).

## Related

- The verification architecture bet: [architecture.md](../../design/architecture.md)
- Verification mechanics: [verification.md](../../design/verification.md)
- Integration concurrency: [[dcr-439b771]]
  ([dcr-439b771-integration-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md))
- Standing and identity registry: [[dcr-b87f6e8]]
  ([dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
