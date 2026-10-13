# Integration

This document specifies the integration protocol: how proposed changes are evaluated and merged into
protected branches.

Integration in `the-valley` uses **optimistic concurrency control over content-addressed evidence**.
The integrator runs no tests itself. Verification occurs hermetically where code is authored,
producing cryptographic attestations. At the commit point, the integrator evaluates the change's
delta against the target branch tip, validates the submitted attestations, and updates the branch
reference.

## Integration Requests as Git References

A contributor (whether human or autonomous agent) requests integration by pushing a single git
reference:

```
refs/the-valley/integration-requests/<target>/<change> -> <commit-head>
```

- `<target>` identifies the protected branch (e.g., `main`).
- `<change>` names the proposed change or feature branch.
- `<commit-head>` is the commit hash of the change.

The git commit already contains its full history, parent tree, and merge base. Attestations are
pushed alongside the request at:

```
refs/the-valley/attestations/<tree-digest>/<signer-key-hash>
```

The integrator identifies the relevant attestations by computing the content-addressed tree digest
of the requested commit.

## The Reconciliation Loop

The integrator runs as a continuous reconciliation daemon over repository refs.

On each evaluation pass, the integrator:

1. Enumerates all pending integration request refs.
2. Evaluates each request against the current tip of its target branch.
3. Applies state updates whenever the verdict has changed since the prior pass.

### Ground-Truth Polling vs. Event Subscriptions

The integrator reads ground-truth refs directly from git rather than subscribing exclusively to the
event bus. Because event projections can drop messages or restart, level-triggered polling over
durable git refs ensures reliable eventual consistency.

The integrator caches its last evaluation verdict at:

```
refs/the-valley/integration-outcomes/<target>/<change>
```

This memo stores the evaluated commit head, the target branch tip, and the attestation namespace
digest. If none of these three inputs have changed, the integrator skips re-evaluating the request,
preventing unnecessary CPU churn.

---

## Verdict Evaluation Rules

When evaluating an integration request, the integrator applies the following rules in strict
sequence:

### 1. Clean Delta Application

The integrator attempts a three-way merge between the target branch tip and the request commit
against their common merge base.

- If merge conflicts occur, the change cannot be applied automatically. The request is marked
  **stale for all required checks**, because resolving conflicts requires new authorship and new
  test evidence.

### 2. Policy Derivation from Target Tip

The list of required verification checks is derived from the policy declared at the **target branch
tip**, never from the incoming untrusted branch.

- Each check is classified by policy as **pure** (deterministic derivation) or **effectful**
  (environment-dependent).
- An incoming attestation must match the runner type specified by the target policy; an attestation
  attempting to claim a different check classification is rejected.

### 3. Pure Check Transfer (Closure-Digest Equality)

For deterministic derivations (e.g., `nix flake check`, unit tests, linters):

- The integrator recomputes the input closure digest (`attest inputs`) over the potential merged
  tree.
- If the recomputed input closure digest matches the digest recorded in the attestation, the test
  results remain valid on the new base and are reused.
- If the input closure digest differs, only that specific check is marked **stale**.

### 4. Effectful Check Transfer (Untouched Scopes and Freshness)

For non-deterministic checks (e.g., live network tests, benchmarks, external integration suites),
the evidence transfers only if:

1. No intervening commits on the target branch modified files covered by that check's path policy.
2. The attestation timestamp falls within the validity window ($\Delta$) declared by the project
   policy.

### 5. Atomic Commit Point

If all required checks remain valid on the merged tree:

1. The integrator constructs a **transfer statement** recording the change, target stream, attested
   tree, and verification results.
2. The integrator performs an atomic compare-and-swap (CAS) fast-forward update on the protected
   target branch (`refs/heads/<target>`).
3. The integrator signs the attestation notes, writes the transfer statement to the repository, and
   deletes the consumed integration request ref.
4. The integrator emits an `integration-succeeded` event to the NATS event bus.

### 6. Staleness Reporting

If any required check is invalidated by intervening changes on the target branch:

- The request ref remains in place.
- The integrator emits a `request-stale` event naming precisely which checks require fresh evidence.
- The author (or autonomous agent) re-runs only the invalidated checks against the updated tip and
  pushes fresh attestations to resume integration.

---

## What the Integrator Publishes

The integrator emits structured events onto the NATS JetStream event bus under:

- `valley.git.<repo>.integration-succeeded`: Emitted when a change cleanly lands on a protected
  branch.
- `valley.git.<repo>.request-stale`: Emitted when a change requires re-attestation of specific
  invalid checks.

Publishing is best-effort and non-blocking. The repository itself remains the sole source of truth.
