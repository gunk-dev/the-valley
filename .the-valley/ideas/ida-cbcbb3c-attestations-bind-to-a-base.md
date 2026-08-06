---
type: idea
id: ida-cbcbb3c
status: raw
title: An attestation is bound to the base it was produced against
created: 2026-08-01
source: design conversation, 2026-08-01
---

# An attestation is bound to the base it was produced against

A change has stable identity across rebases; the evidence about that change does not. Approvals,
builds and checks are performed over a specific base and a specific resulting tree, so an
attestation is bound to the base it was produced against in a way the change's identity deliberately
is not. Rebasing a change onto a new base does not carry its attestations with it.

Both properties hold at once. The integration primitive is a change — a diff targeting a version
stream, with identity stable across rebases — because a branch bakes its base into its identity and
every integration invalidates the rest of the queue ([[ida-93e4f91]],
[ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)). That solves identity.
It does not solve validity.

Stated the other way round: a patch does not live independently of the codebase it applies to. It
expires unpredictably as that codebase churns beneath it. Check-churn over a shifting base is both
pragmatically and semantically difficult — that difficulty is real, and is what the change primitive
was chosen to address — and it does not remove the fact that the evidence about a change is only
evidence about it at one base.

The change definition already contains this. A change is (target stream, delta, provenance,
attestation over the resulting tree), and the resulting tree is a function of the base.

## Related

- [[dcr-439b771]]
  ([dcr-439b771-integration-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md))
  — what happens to a change's attestations when its base moves.
- [[ida-93e4f91]] ([ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md)) —
  the primitive whose identity is stable across rebases.
- [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](./ida-51605e8-authenticity-not-git-coupled.md)) —
  why the subject of an attestation is identified by content rather than by commit.
- [contribute.md](../../design/contribute.md) and
  [roadmap.md, Phase 3](../../design/roadmap.md#phase-3--the-integrator) — where the integrator
  rebases as it integrates.
