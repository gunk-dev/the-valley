# Conformance vectors for attestation serialization and signatures

This directory defines the conformance test suite and interoperability contract for attestation
serialization and note verification.

Any independent implementation of `attest` must serialize every `input.json` in this directory to
the exact byte sequence of the corresponding `statement.txt`, and must successfully verify every
`statement.note`. If implementations disagree by even a single byte, cryptographic signatures
produced by one tool will fail verification in another.

The canonical written format and signed note envelope specification are detailed in
[design/verification.md](../../design/verification.md).

## Test suite structure

Each directory under `vectors/` represents a single conformance test case:

- `input.json`: A statement represented as arbitrary JSON (with unnormalized key ordering,
  whitespace, or escape sequences).
- `statement.txt`: The exact, canonical byte sequence produced by the written form. Signatures cover
  these bytes and nothing else.
- `statement.note`: The canonical statement text followed by a blank line and signed note lines.
  Test verification keys are defined in [`known_keys`](./known_keys).

### Coverage of edge cases

The vectors validate crucial serialization rules:

- **All three predicate types:** Verification, transfer, and denial predicates formatted in their
  required field orders.
- **Transfer predicate zero-requirement:** Proper handling of `predicate.required none` when no
  checks were required for a path.
- **Digest sets:** Sorted alphabetically by algorithm name.
- **Array indices:** Numeric sorting for delegation chains so index `10` follows index `9`.
- **Exact whitespace and Unicode:** Preserving literal whitespace and Unicode code points above the
  Basic Multilingual Plane without unneeded JSON escapes.
- **Multi-party notes:** Validating envelopes signed by multiple parties on consecutive lines.

### Negative test cases (`refused/`)

The `refused/` directory contains malformed inputs that every compliant implementation must reject:

- `.json` files containing unrenderable data (e.g., raw numbers, empty objects, newlines within
  values).
- `.txt` files containing invalid statement texts (e.g., misordered sections, duplicate keys,
  non-sequential array indices).

## Running conformance checks

The conformance test suite executes automatically as part of repository checks:

```bash
nix flake check
```

To run individual checks manually:

```bash
# Verify byte-for-byte canonical rendering:
nix run .#attest -- render attest/conformance/vectors/01-statement-minimal/input.json \
    | cmp - attest/conformance/vectors/01-statement-minimal/statement.txt

# Verify cryptographic note signatures:
nix run .#attest -- verify \
    --note attest/conformance/vectors/04-two-signers/statement.note \
    --known-keys attest/conformance/known_keys
```

## Modifying vectors

Recorded vector bytes must only change if the underlying canonical specification in
`design/verification.md` changes. Modifying the written format breaks backward compatibility with
all previously signed attestations.

To add new test cases, create a new subfolder under `vectors/` containing the test files. The test
runner will discover and run them automatically.
