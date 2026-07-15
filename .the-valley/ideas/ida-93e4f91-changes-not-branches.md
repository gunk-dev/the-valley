---
type: idea
id: ida-93e4f91
status: adopted
title: "Changes, not branches: the integration primitive is a change, not a ref"
created: 2026-07-14
source: owner brainstorm during the S1 week
---

# Changes, not branches

**Thesis.** What matters about a pending branch is the *diff targeting a particular version stream* — the branch is transport, not substance. A change is (target stream, delta, provenance, attestation over the resulting tree). Every branch in the S1 pending queue was created, reviewed as a diff, integrated, deleted: single-use packaging around the object that was actually under review. The integration primitive should be a change object, not a ref.

**Demand signal.** The S1 week's staleness pain. A branch bakes its base into its identity, so every integration invalidated the rest of the queue — `valley review` grew a [b]ase verb (rebase onto main in a throwaway worktree) within a day of going sovereign. The queue's members were never "branches to merge"; they were diffs targeting main whose packaging kept expiring.

**Staleness reframed, not eliminated.** A patch that applies cleanly to a moved stream yields a tree *nobody tested*. The architecture already answers this: attestations bind to trees, so reapplication on a new base demands re-verification. That is "staleness as the unified failure mode" and witness re-derivation — patch-shaped all along. The change model doesn't dodge the failure mode; it names it precisely.

**Commutation.** Non-overlapping changes can land in any order (Darcs/Pijul patch theory). The pending queue stops being a race for the base and becomes a **partial order** — structurally rhyming with the outcome DAG ([[ida-eac723e]], [ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md)). Changes are to code what outcome nodes are to intent: units with dependencies, not positions in a line.

**Stable identity.** A change's identity is its intent + content, not its base. Prior art to study, all three: jj's change-ids surviving rebase (hardest study — git-compatible underneath), Gerrit's change/patchset model, the kernel's `format-patch` flow.

## Consequences by phase

- **Phase 3 (integrator).** The integrator accepts changes targeting streams. Candidate encoding: `refs/the-valley/changes/*`, iterations reviewed via `git range-diff`; [contribute.md](../../design/contribute.md)'s integration-request ref is already nine-tenths of a change object.
- **Phase 2 (attestations).** Attestations bind (base, delta, tree) explicitly — the re-verification demand above falls out of the schema instead of being policy.
- **Phase 4 (dispatch).** Dispatch becomes symmetric: an agent consumes an outcome node and emits a change. klaus's budget-pause persistence gap (patflynn/klaus#282; the gap row in [[ida-594df79]], [ida-594df79-klaus-s3-requirements-oracle.md](./ida-594df79-klaus-s3-requirements-oracle.md)) is trivially a stored change object.

## Open design questions

Unanswered on purpose:

- The identity function of a change — what is hashed, over what, to survive rebase.
- The iteration/patchset model — what "a new version of the same change" is.
- Conflict semantics — what imposes order between overlapping changes.
- Signature/attribution binding across re-application — whose signature covers the rebased tree.
