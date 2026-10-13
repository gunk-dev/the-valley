# Verification

This document specifies the verification architecture for `the-valley`: check classifications,
content-addressed tree digests, canonical statement serialization, and cryptographic note envelopes.

Instead of relying on slow, synchronous CI gates on remote servers, `the-valley` uses **signed
attestations with asynchronous re-verification**. Contributors run checks in hermetic local
sandboxes and sign cryptographic statements attesting to the results.

---

## Check Classifications

A single generic "tests passed" badge is inadequate for robust verification. The system explicitly
distinguishes between two types of checks:

1. **Pure Checks (Deterministic Derivations):**
   - Examples: `nix build`, `nix flake check`, unit tests, linters, type checks, and VM integration
     tests with mocked networks.
   - Properties: Inputs are content-addressed and hermetic; outputs are bit-for-bit reproducible.
   - Attestation Content: Records input closure digests, derivation hashes, and output hashes.
   - Verification Guarantee: **Any verifier can re-run the derivation and confirm identical
     outputs.**
2. **Effectful Checks (Environment-Dependent Notarizations):**
   - Examples: Live network integration, external API testing, and performance benchmarks.
   - Properties: Outputs may vary across runs and depend on external conditions.
   - Attestation Content: Records execution timestamps, environment descriptions, and observed
     outputs.
   - Verification Guarantee: Relies on the cryptographic reputation and authority of the signer.

---

## The Subject: Content-Addressed Tree Digest (`valley-tree-v1`)

Attestations bind to git tree objects rather than commit hashes. Because the tree digest depends
solely on the directory contents—and not on author names, timestamps, or parent commit hashes—an
attestation remains valid even if a commit is rebased, provided the resulting file tree is
identical.

### Tree Digest Algorithm

To compute the `valley-tree-v1` digest of a repository tree:

1. Traverse every file entry in the tree (blobs and symlinks).
2. For each entry, format a line containing:

   ```
   <mode> <sha256-of-content> <path-byte-length> <path>
   ```

   - `<mode>`: Standard git mode (`100644` for regular files, `100755` for executables, `120000` for
     symlinks). For symlinks, the content is the target path.
   - `<sha256-of-content>`: Lowercase hex SHA-256 hash of the file contents.
   - `<path-byte-length>`: Length of the relative path in bytes (decimal). Including the path length
     prevents newline-injection attacks where a maliciously crafted filename could inject fraudulent
     manifest lines.
   - `<path>`: Relative path from repository root.

3. Sort all lines in ascending lexicographical byte order.
4. Prepend the header `valley-tree-v1\n`.
5. Compute the lowercase hex SHA-256 hash over the combined bytes.

Submodules are disallowed in verified trees because their contents point to external repositories
outside the tree's deterministic boundary.

---

## A statement is written down as lines

Digital signatures cover exact byte sequences. To guarantee that independent implementations produce
identical signatures for identical data, statements follow a strict line-based format.

### Example Attestation Statement

```
the-valley/attestation/v1
subject.primary valley-tree-v1
subject.digest.git-sha1 99b5f2f7...
subject.digest.valley-tree-v1 73847e0b...
predicateType the-valley/check/pure/v1
predicate.check.name prose-format
predicate.check.runner nix
predicate.check.attribute prose-format
predicate.result passed
predicate.inputs.valley-inputs-v1 8df8c199...
predicate.derivation.sha256 ef69ed0c...
predicate.output.valley-tree-v1 cf1f5a9c...
provenance.harness klaus
provenance.model claude-sonnet-4-5
provenance.delegation.0.principal human:operator
provenance.delegation.0.grant land-changes
```

### Line order

- **Header Line:** The first line must declare the statement type (e.g.,
  `the-valley/attestation/v1`).
- **Key-Value Pairs:** Each subsequent line contains a dot-delimited key, a single space, and the
  string value.
- **Strict Key Ordering:** Keys must appear in standard order: `subject` fields, followed by
  `predicateType`, `predicate` attributes, and `provenance`.
- **No Escapes or Ambiguity:** Values are written as raw bytes without escape characters. Values
  cannot contain embedded newlines.
- **Dense Array Indices:** Array elements use sequential decimal indices starting from zero (`.0`,
  `.1`, `.2`). Empty objects and empty arrays are omitted.

---

## The envelope is a signed note

Attestations use the signed note envelope format specified by Go's `golang.org/x/mod/sumdb/note`:

```
the-valley/attestation/v1
subject.primary valley-tree-v1
...

— laddie.gunk.dev/attestations sEDvC7HO4DfL5ZxbZPFvoVWq2Wt5eGTqZx2h...
— witness.gunk.dev/attestations lDb9C3YrmxnO0/YWsmWjiu2kteNvGqfQstCy...
```

- The note body contains the canonical statement text, followed by an empty line.
- Each signature begins with an em-dash (`—`, Unicode `U+2014`), a space, the signer's identity
  string, a space, and a base64-encoded payload (a 4-byte key hash prefix followed by the raw
  Ed25519 signature).
- Multiple signers (such as independent witnesses or re-verifiers) add sibling signature lines below
  the same statement body.

### Signer Identity and Verifier Keys

Signer identities use the format: `<hostname>/<purpose>` (e.g., `laddie.gunk.dev/attestations`).
Verifiers validate signatures against a known key list:

```
laddie.gunk.dev/attestations+1f9c40b2+AbUmzL2tCH0nR9...
```

---

## The helper

The `attest` CLI helper (`nix run .#attest`) automates statement generation and verification:

- `attest run`: Computes the current tree digest, runs repository checks in an isolated export,
  generates statements, signs them, and stores them under
  `refs/the-valley/attestations/<tree-digest>/<signer-key-hash>`.
- `attest verify`: Reads a signed note, verifies statement formatting against the CUE schema, checks
  Ed25519 signatures against known public keys, and confirms that the repository tree matches the
  attested digest.
- `attest inputs`: Evaluates and outputs the input-closure digest for pure checks without running
  full builds, enabling the integrator to quickly test for staleness.
- `attest render` & `attest sign`: Helper commands for low-level statement formatting and note
  signing.

---

## Defense-in-Depth Verification Stack

The verification architecture combines multiple layers of security:

| Mechanism                      | Protection Provided                                                                      | Residual Risk                                                      |
| :----------------------------- | :--------------------------------------------------------------------------------------- | :----------------------------------------------------------------- |
| **Ed25519 Signing Keys**       | Verifies that a trusted host or agent produced the attestation.                          | Host compromise allows forged claims.                              |
| **Hermetic Nix Sandboxes**     | Prevents tests from capturing undeclared local state or network dependencies.            | Local root can bypass sandbox, but leaves evidence.                |
| **Re-Derivation Audits**       | Pure checks encode all input hashes; verifiers can re-run and confirm identical outputs. | Not applicable to effectful checks.                                |
| **Witness Sampling**           | Background nodes sample and re-verify pure attestations, maintaining trust scores.       | Small window before divergence is detected.                        |
| **Transparency Log (Tessera)** | Attestations are appended to an immutable append-only log with inclusion proofs.         | Provides tamper-evident non-repudiation, not semantic correctness. |

Combined, these mechanisms ensure that dishonest attestations are immediately detectable and result
in automatic revocation of the offending signer's credentials.
