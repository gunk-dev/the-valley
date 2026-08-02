# Verification

The reframe — attestation-with-revocation instead of CI-as-gate, and the failure-mode trade it makes
— is an architecture bet; see [architecture.md](./architecture.md). This document holds what the
attestation phase actually needs: the two kinds of checks, what an attestation is about, and what
makes one hard to forge.

The shape of an attestation — its statement, its signer, its envelope, where it is stored, and how
several of them compose — is fixed by
[dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md), and the statement's
fields are [schema/attestation.cue](../schema/attestation.cue).

## Two kinds of checks, two kinds of attestation

The system must distinguish what an attestation is claiming. A single "checks passed" signature is
the SLSA mistake worth avoiding.

- **Pure checks** — `nix build`, `nix flake check`, lint/type-check as derivations,
  `nixosTest`-style integration tests (effectful inside, pure outside). Inputs content-addressed,
  outputs deterministic; the attestation carries input, derivation, and output hashes, so **any
  verifier can re-derive and confirm**. These are the strong attestations.
- **Effectful checks** — real-network tests, external APIs, benchmarks, anything not
  bit-reproducible. The attestation is a notarization: "sealed environment $E ran $T at time $t and
  observed $result." Trust here is closer to "I trust the signer."

Wherever a check can be moved from effectful to pure (via `nixosTest` or microVM sandboxing), it
should be.

## The subject is a tree digest

An attestation is about a tree, not about a commit. The digest that names that tree is
`valley-tree-v1`, and it is defined so that anyone holding the tree can recompute it from the tree
alone.

Take every entry of the tree: every blob and every symlink, at its path relative to the root. Render
each one as a line

    <mode> <sha256 of the content, in hex> <byte length of the path> <path>

where the mode is git's — `100644`, `100755`, or `120000` for a symlink, whose content is its target
— and the byte length is written out in decimal. Sort the lines by path, ascending, comparing bytes.
Put `valley-tree-v1` and a newline in front of them. The digest is the SHA-256 of the result, in
lowercase hex.

The path's byte length is written out because a path can contain a newline, and an entry boundary a
path could forge is a digest an attacker could steer. Directories are not entries: a directory is
implied by the paths beneath it, and git holds no empty ones. A submodule is rejected rather than
digested, because a tree that reaches outside itself is not a thing this digest can be a function
of.

Nothing else enters the manifest. Not the commit, not the author, not the time, not a store path.
That is what makes an attestation survive the integrator's rebase: the same tree under a rewritten
commit has the same digest, so the attestation still names it. It is also what binds an attestation
to a base, since a tree is the base and the change together
([ida-cbcbb3c](../.the-valley/ideas/ida-cbcbb3c-attestations-bind-to-a-base.md)).

The same manifest digests a pure check's output, so the digest a statement records for what a check
produced and the digest it records for what the check ran over are the same kind of thing.

## The helper

`nix run .#attest` is the Phase 2 helper. `attest run` digests the tree, runs the checks over an
export of that tree rather than over the working directory, composes one statement per check, vets
each against [schema/attestation.cue](../schema/attestation.cue), signs each with a detached SSHSIG
signature under the namespace `the-valley.attestation.v1`, and stores them at
`refs/the-valley/attestations/<tree digest>/<signer fingerprint>`. With `--push` it publishes the
topic branch and that ref in one atomic native-git push. A failing check publishes nothing, and
neither does a statement the schema rejects.

The signing key is a parameter (`--key`). There is no unsigned mode: a run with no key available
fails rather than emitting a statement nobody signed.

`attest verify` takes a statement, its signature and an allowed-signers file, and checks four
things: that the statement satisfies the schema, that its bytes are the canonical rendering of what
it says, that a signer the file allows signed exactly those bytes, and — given `--repo` — that the
tree in front of the verifier is the tree the statement names. Re-running a pure check and
confirming its output digest is witness re-verification, and belongs to
[Phase 6](./roadmap.md#phase-6--trust-backstop) rather than here.

The run provenance a statement carries — the harness, the model, digests of the prompt and the
context, and the delegation chain — is supplied by the caller (`--provenance`) or absent. Nothing
collects those values today.

## The mechanism stack

Without a TEE, attestations are exactly as trustworthy as the signing key — the same trust model as
code signing, plenty for non-adversarial settings. But the mechanisms stack:

| Mechanism                          | What it gets you                                                                                                                    | Residual attack                                                                                                                          |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Signing key                        | "The signer says these checks passed"                                                                                               | Anyone with the key can lie                                                                                                              |
| Content-addressed attestation tool | The tool itself is a known Nix derivation; the attestation names its hash                                                           | Malicious dev can ship a patched tool, but it will not match the canonical hash                                                          |
| Hermetic sandbox                   | `nix build` sandbox; `nixosTest` VMs; microVMs for non-Nix checks                                                                   | Local root can still bypass — but bypass leaves evidence                                                                                 |
| Re-derivation audit                | Pure attestations encode every input hash; verifier re-runs and confirms bit-identical                                              | None for pure checks; effectful checks not re-verifiable                                                                                 |
| Witness sampling                   | Random fraction of attestations re-run on another node; trust score per attester                                                    | None — this is the backstop                                                                                                              |
| Transparency log (Tessera-backed)  | Every attestation appended to an external append-only log with an inclusion proof; anyone can audit existence and content over time | Orthogonal to the others — gives non-repudiation and tamper evidence, not correctness of the computation. Independent and complementary. |

Stacked, a determined local-root actor can still forge an attestation — but only visibly (tool hash
mismatch), temporarily (re-derivation eventually detects), and within a bounded window (before their
trust score is recalculated). For pure-derivation checks this lands roughly at SLSA Level 3; for
effectful checks, lower — but the system knows which kind each attestation is.
