# Contributor protocol

Defines exactly what a contributor (human or agent) does to push a change and request its integration into a protected ref.

This is the *protocol* that the trust model ([verification](./verification.md)) and the integrator ([integration](./integration.md)) both consume. It is intentionally tight; mechanisms for the integrator's queue, conflict resolution, and issue tracking are out of scope and have their own placeholder documents.

## Goal

A push that is:

1. **Cryptographically attributable** to the contributor.
2. **Accompanied by a signed promise** that the repo's canonical check set passed locally in a hermetic environment.
3. **Externally tamper-evident** via inclusion in a transparency log.
4. **Explicitly marked** as ready for integration into a target branch.

These four properties are produced by one atomic `git push`, with no `git` wrapper command. The contributor uses native git verbs and one helper for attestation composition.

## Steps

### 1. Commit, signed natively

The contributor commits as normal. The commit is SSH-signed using modern git's native commit signing — `git config gpg.format ssh` and a `commit.gpgsign = true` setting. No GPG. The signature establishes *who made this commit*.

### 2. Run the canonical check set, locally and hermetically

The repo's `flake.nix` declares a set of derivations as the canonical checks — `checks.<system>.unit`, `.lint`, `.integration`, whatever the repo wants. Each runs in the Nix sandbox; effectful checks are wrapped as `nixosTest` VMs so they are pure derivations from the outside.

The contributor runs them via a single helper (e.g. `nix run .#attest`). All must succeed. If any fails, the helper exits non-zero and nothing is published.

### 3. Compose the attestation

The helper assembles a structured claim about what ran:

```json
{
  "subject_commit": "<commit-sha>",
  "issued_at":      "<rfc3339>",
  "checks": [
    {
      "kind":       "pure",
      "tool":       "sha256:<attestation-tool-derivation>",
      "inputs":     ["sha256:..."],
      "derivation": "sha256:...",
      "output":     "sha256:...",
      "result":     "success"
    },
    ...
  ]
}
```

The attestation makes no claim about *intent to integrate*. That is signaled by the ref namespace in step 6.

### 4. Sign and publish to a transparency log

The helper signs the attestation with the contributor's SSH key (same key as the commit signature). The signed attestation is submitted to a Tessera-backed transparency log; the log returns an inclusion proof. The proof is appended to the signed attestation as a sidecar field.

The tlog gives the system *non-repudiation and external auditability*: anyone can verify the attestation was logged, when, with what content, and that the log itself is internally consistent. This is independent of and complementary to re-derivation witnessing (see [verification](./verification.md)) — the tlog proves *the attestation exists and is unchanged*; re-derivation proves *the computation it describes actually produces the claimed result*.

### 5. Store the attestation in the repo's object store

The signed attestation (with inclusion proof) is added as a git blob, and a ref is created:

```
refs/the-valley/attestations/<commit-sha>  →  attestation blob
```

This makes the attestation distributable by standard `git fetch` and trivially lookupable by commit SHA. The naming pattern mirrors gittuf's convention without adopting gittuf as a dependency.

### 6. Signal integration intent

A second ref is set, pointing at the topic commit:

```
refs/the-valley/integration-requests/<request-name>  →  topic-commit-sha
```

Pushing to this namespace is the contributor's signal: *please integrate this commit into the target branch declared by repository policy*. Without this ref push, the topic branch is just a backup; no integrator reacts.

### 7. Atomic push

```
git push --atomic origin \
  refs/heads/topic/<name> \
  refs/the-valley/attestations/<commit-sha> \
  refs/the-valley/integration-requests/<request-name>
```

Either all three refs land or none do. No wrapper command — this is native git.

### 8. Server-side projection

The bare repo's `post-receive` hook fires once. For each updated ref, it emits a corresponding event onto the bus:

- `refs/heads/*` → `ref-updated`
- `refs/the-valley/attestations/*` → `attestation-published`, carrying the attestation blob
- `refs/the-valley/integration-requests/*` → `integration-requested`

The hook is a pure projection from git to bus. It performs no policy and no verification — those are the integrator's job.

## What protects the canonical refs

The bare repo enforces *one* structural invariant via a `pre-receive` hook:

- Only the integrator's SSH key may write `refs/heads/<protected>`.
- Attestation refs are create-only (no updates) to keep attestations append-only-per-commit.
- All other namespaces — topic branches, integration requests — are wide open to anyone with push access.

The complex policy lives in the integrator. The git boundary enforces only the minimum needed for the controller pattern to work.

## What ties this together

| Property | Source |
| --- | --- |
| Who made the commit | SSH commit signature |
| Who made the attestation | SSH signature on the attestation blob (same key) |
| What checks ran and what they produced | Attestation content (derivation hashes) |
| External tamper evidence | Tessera tlog inclusion proof |
| Lookup by commit | `refs/the-valley/attestations/<commit-sha>` ref |
| Intent to integrate | `refs/the-valley/integration-requests/*` ref namespace |
| Atomicity | `git push --atomic` |

## Out of scope

- **Integrator internals** — how the merge queue works, conflict resolution, agentic fallback. See [integrator-internals.md](./integrator-internals.md).
- **Issue tracking** — how bugs, feature ideas, and tasks are recorded and worked. See [issues.md](./issues.md).
- **Per-repo policy file** — where the trusted-signer list and the canonical-checks declaration live, and how they are themselves authenticated and updated. To be designed; likely a `refs/the-valley/policy` ref carrying a signed configuration.
- **Multi-signature attestations** — for higher-trust paths, requiring N signers on a single attestation. Gittuf demonstrates this is tractable; not in v1.
- **Tlog deployment** — self-hosted Tessera on the same Tailscale box vs. a shared instance. An operational choice that doesn't affect the protocol.

## Open questions

See [openquestions.md](./openquestions.md) — items raised here live under *Identity & trust bootstrapping*, *Storage, retention, and evolution*, and *Verification specifics*.
