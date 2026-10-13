---
type: bug
id: bd-e63bac8
status: open
title: "Define recovery when later verification disproves accepted evidence"
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Define recovery when later verification disproves accepted evidence

If a trusted signer supplies false evidence and the problem is found after integration, revoking
that signer does not undo the landed change or its effects. The verification design needs a recovery
procedure for that case.

This concerns an execution claim that was accepted and later disproved. The integrator already
checks signatures and evidence structure before landing; an invalid signature should be rejected at
that point.

## Failure sequence

A change A lands with a correctly signed claim that a required check passed. B, C, and D may then
land before an independent verifier reruns A's check and finds a different result.

Revoking the signer limits future reliance on its evidence. The system must still determine which
landed work, deployed state, and pending checks depend on A.

That impact is not automatically every later commit. It depends on actual code, configuration, and
verification dependencies. A shared foundational change can nevertheless affect a large amount of
later work.

## Recovery choices

Rewriting published history would require coordinating references and the meaning of existing
evidence. It can disrupt consumers and the audit trail.

An ordinary revert preserves history, but creates another tree that needs verification. Pending
checks become stale where their input closures change. Later changes that depend on A may need
coordinated repair instead of a simple revert.

A signature or a successful integration verdict does not decide which recovery is correct.

## Scope

Independent background re-verification remains part of the planned trust backstop. This report
identifies an unresolved recovery risk; it does not report a demonstrated cascade in the current
deployment.

The question matters whenever evidence can be wrong, including a compromised trusted machine or a
faulty check implementation. It is not limited to unfamiliar contributors.

## Candidate responses

Use an intermediate stream and promote work only after additional verification. This changes where
the verification delay is paid.

Require more verification before accepting evidence from signers with little established standing.
The executor capacity and trust rules need definition.

Vary admission policy according to the consequences of a change. A documentation path is not
automatically harmless if it contains policy or instructions used by automation.

The design must define detection, affected-change analysis, deployment response, and recovery
authorization. [verification.md](../../design/verification.md),
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md), and
[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) provide the evidence,
integration, and identity context.
