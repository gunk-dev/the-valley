# Verification & trust

How the system decides a change is safe to integrate, without a central CI gate.

## The reframe

CI-as-gate ("the change is blocked until our infra confirms the checks pass") is replaced by **attestation-with-revocation**:

1. The developer's local environment runs the checks in a hermetic sandbox.
2. It produces a signed attestation describing what ran, against what inputs, and what the result was.
3. The commit and its attestations land in the event log together.
4. Subscribers act immediately on the attestation. Re-verifiers cross-check asynchronously.
5. Trust is *measured* per attester, based on re-verification confirm rates. Divergence revokes trust.

This trades two failure modes:

| Model | Failure mode |
| --- | --- |
| CI-as-gate | False negatives — slow feedback, flakes, infra outages block correct changes |
| Attestation | False positives — a bad attestation can land before the re-verifier catches it |

For most environments — personal projects, small teams, trusted contributor sets — false positives that are *detected and revocable* are a much better trade than a slow, brittle gate.

## What "unforgeable" actually means

Without a TEE, attestations are exactly as trustworthy as the signing key. That's the same trust model as code signing, and it's plenty for non-adversarial settings. But you can stack mechanisms to raise the bar significantly above "trust the human":

| Mechanism | What it gets you | Residual attack |
| --- | --- | --- |
| Signing key | "Patrick says these checks passed" | Anyone with the key can lie |
| Content-addressed attestation tool | The tool itself is a known Nix derivation; the attestation names its hash | Malicious dev can ship a patched tool, but it will not match the canonical hash |
| Hermetic sandbox | `nix build` sandbox; `nixosTest` VMs; microVMs for non-Nix checks | Local root can still bypass — but bypass leaves evidence |
| Re-derivation audit | Pure attestations encode every input hash; verifier re-runs and confirms bit-identical | None for pure checks; effectful checks not re-verifiable |
| Witness sampling | Random fraction of attestations re-run on another node; trust score per attester | None — this is the backstop |
| Transparency log (Tessera-backed) | Every attestation appended to an external append-only log with an inclusion proof; anyone can audit existence and content over time | Orthogonal to the others — gives non-repudiation and tamper evidence, not correctness of the computation. Independent and complementary. |

Stack 2–6 and a determined local-root actor *can* still forge an attestation, but only by:

- Shipping a tool whose hash doesn't match the canonical (visible).
- Producing a derivation hash that won't match on re-derivation (eventually detected).
- Doing so before their trust score is recalculated (bounded window).

For pure-derivation checks this lands roughly at SLSA Level 3. For effectful checks, lower — but the system knows which kind each attestation is.

## Two kinds of checks, two kinds of attestation

The system must distinguish what an attestation is claiming. A single "checks passed" signature is the SLSA mistake worth avoiding.

### Pure checks

- `nix build` of the project
- `nix flake check`
- Type-checking, linting, formatting (when wrapped as derivations)
- `nixosTest`-style integration tests (effectful from inside, pure from outside)

Inputs are content-addressed, outputs are deterministic. The attestation includes input hashes, derivation hashes, output hashes. **Any verifier can re-derive and confirm.** These are the strong attestations.

### Effectful checks

- Real-network integration tests
- Tests against external APIs or stateful services
- Performance benchmarks
- Anything not bit-reproducible

The attestation is a notarization: "sealed environment $E ran $T at time $t and observed $result." Verifiable only inasmuch as $E is a derivation. Trust here is closer to "I trust the signer."

Wherever a check can be moved from effectful to pure (via `nixosTest` or microVM sandboxing), it should be.

## Attestation shape

Sketch, not a spec:

```
{
  "subject": {
    "repo": "...",
    "commit": "sha256:..."
  },
  "checks": [
    {
      "kind": "pure",
      "tool":   "sha256:...",   // attestation tool derivation hash
      "inputs": ["sha256:..."], // input derivation hashes
      "derivation": "sha256:...",
      "output":     "sha256:...",
      "result": "success"
    },
    {
      "kind": "effectful",
      "tool":   "sha256:...",
      "env":    "sha256:...",   // sealed-environment derivation hash
      "test":   "sha256:...",
      "result": "success",
      "observed_at": "..."
    }
  ],
  "signatures": [
    {"key_id": "...", "sig": "..."}
  ]
}
```

The attestation is itself an event published into the log.

## Reactions on the event

A few subscribers worth naming:

- **Deploy controller** — `attestation valid + signer trusted + ref matches main → deploy`.
- **Re-verifier (pure)** — re-derive any pure attestation; emit confirm/deny event.
- **Re-verifier (effectful sample)** — re-run a sampled fraction of effectful attestations on a witness node.
- **Trust controller** — track per-attester confirm rate. Below threshold → revoke trust → future attestations from that signer require gating before reactions.
- **Review controller** — open a human review request regardless. Attestations replace the *correctness gate*, not the *human review*.

## What this is not

- Not a replacement for review. Attestations say "the checks ran and passed." They say nothing about whether the change is *correct* or *desirable*. That's a separate problem with a separate document.
- Not a guarantee against compromised developer machines. A compromised machine signing real attestations of malicious code is the unsolved problem; hardware attestation is the only fix and is out of scope.
- Not a security-only mechanism. The primary win is *latency* — closing the feedback loop from minutes to seconds. The security properties are a side effect of doing it well.

## Open questions

See [openquestions.md](./openquestions.md) — items raised here live under *Identity & trust bootstrapping*, *Storage, retention, and evolution*, and *Verification specifics*.
