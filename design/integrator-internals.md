# Integrator internals

**Status: placeholder.** Initial framing and open questions; not yet designed in detail.

This document will define how the integration automation actually performs its job once an `integration-requested` event arrives. The high-level controller pattern is established in [integration.md](./integration.md); this is the missing operational detail.

## In scope

- **Merge queue mechanics.** Serialization per protected ref, fairness, request ordering.
- **Conflict detection.** When a topic doesn't fast-forward cleanly, how does the integrator notice, what does it try, and when does it give up?
- **Agentic conflict resolution.** When auto-rebase or three-way merge fails, the integrator may dispatch an agent (klaus-shaped) to attempt a more intelligent resolution. The output is a new commit + new attestation; the contributor or the integrator on their behalf resubmits.
- **Failure semantics.** How rejections, retries, and partial successes surface as events.

## Initial framing

- One **queue per protected ref** (typically `refs/heads/main` per repo). Strictly serial inside the queue; concurrent integration across different protected refs is fine.
- For each `integration-requested` event the integrator dequeues:
  1. Verify attestation signatures, contents, signer trust score (per [verification](./verification.md)).
  2. Optionally wait for or fetch tlog inclusion proof and witness re-derivation confirmation, depending on the policy for this signer/path.
  3. Try fast-forward integration into the target's current tip.
  4. If non-FF: attempt automatic rebase. **But:** the rebased commits have different SHAs; the original attestation no longer applies. So either:
     - Reject with `requires-rebase`; the contributor re-runs locally and re-attests. (Simpler, slower.)
     - Or: integrator dispatches an agent to perform the rebase + re-attest in the contributor's identity context. (Faster, opens a trust question.)
  5. If still conflicting: dispatch the conflict-resolution agent (optional, opt-in).
  6. If all paths fail: emit `integration-failed` with structured reasoning.
- On success: update the protected ref, emit `integration-succeeded`.

## Open questions

- **Rebase ownership.** Who signs the rebased commits? Possibilities:
  - Only the original contributor can. Integrator rejects on non-FF and the contributor resubmits. (Architecturally pure.)
  - The integrator's identity is acceptable for trivial rebases (no conflict). (Pragmatic.)
  - A dispatched agent running under the contributor's delegated authority. (Touches agent identity.)
- **Conflict resolution agent trust.** When an agent produces a conflict resolution, whose key signs the new commit and attestation? Inherits from [scenarios.md](./scenarios.md)'s agent identity question.
- **Fairness.** Strict FIFO per queue is the obvious default. Are there cases where a queue should be priority-ordered (e.g., security fixes ahead of feature work)?
- **Backpressure visibility.** Contributors should see queue depth and an estimate of when their request will be processed.
- **Multiple integrators per protected ref.** Probably not — competing integrators racing for the same ref creates chaos. One integrator per protected ref.
- **Recovery from a crashed integrator.** Integrator state should be derivable from the bus + git refs (the integration-request refs are durable; the bus carries the pending events). Restart should be safe.
- **Cross-repo coordinated integration.** Two requests in two repos that must succeed together (e.g., a schema producer and consumer). Deferred to v2.
