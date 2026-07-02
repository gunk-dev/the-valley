# Verification

The reframe — attestation-with-revocation instead of CI-as-gate, and the failure-mode trade it makes — is an architecture bet; see [architecture.md](./architecture.md). This document holds what the attestation phase actually needs: the two kinds of checks, and what makes an attestation hard to forge.

## Two kinds of checks, two kinds of attestation

The system must distinguish what an attestation is claiming. A single "checks passed" signature is the SLSA mistake worth avoiding.

- **Pure checks** — `nix build`, `nix flake check`, lint/type-check as derivations, `nixosTest`-style integration tests (effectful inside, pure outside). Inputs content-addressed, outputs deterministic; the attestation carries input, derivation, and output hashes, so **any verifier can re-derive and confirm**. These are the strong attestations.
- **Effectful checks** — real-network tests, external APIs, benchmarks, anything not bit-reproducible. The attestation is a notarization: "sealed environment $E ran $T at time $t and observed $result." Trust here is closer to "I trust the signer."

Wherever a check can be moved from effectful to pure (via `nixosTest` or microVM sandboxing), it should be.

## The mechanism stack

Without a TEE, attestations are exactly as trustworthy as the signing key — the same trust model as code signing, plenty for non-adversarial settings. But the mechanisms stack:

| Mechanism | What it gets you | Residual attack |
| --- | --- | --- |
| Signing key | "Patrick says these checks passed" | Anyone with the key can lie |
| Content-addressed attestation tool | The tool itself is a known Nix derivation; the attestation names its hash | Malicious dev can ship a patched tool, but it will not match the canonical hash |
| Hermetic sandbox | `nix build` sandbox; `nixosTest` VMs; microVMs for non-Nix checks | Local root can still bypass — but bypass leaves evidence |
| Re-derivation audit | Pure attestations encode every input hash; verifier re-runs and confirms bit-identical | None for pure checks; effectful checks not re-verifiable |
| Witness sampling | Random fraction of attestations re-run on another node; trust score per attester | None — this is the backstop |
| Transparency log (Tessera-backed) | Every attestation appended to an external append-only log with an inclusion proof; anyone can audit existence and content over time | Orthogonal to the others — gives non-repudiation and tamper evidence, not correctness of the computation. Independent and complementary. |

Stacked, a determined local-root actor can still forge an attestation — but only visibly (tool hash mismatch), temporarily (re-derivation eventually detects), and within a bounded window (before their trust score is recalculated). For pure-derivation checks this lands roughly at SLSA Level 3; for effectful checks, lower — but the system knows which kind each attestation is.
