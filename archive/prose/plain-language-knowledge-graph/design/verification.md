# Verification

An attestation is a signed statement about a check and its result. It identifies the tree checked,
the computation or observation performed, and any supplied provenance. The integrator uses that
statement as evidence under the project's governing policy.

This document defines the current formats and helper behavior. Independent re-verification and
transparency publication remain [Phase 6 work](./roadmap.md#phase-6--trust-backstop). The
[attestation decision](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md) defines the
overall shape; the
[text and signature decision](../.the-valley/decisions/dcr-de9d996-statement-text-and-signed-note.md)
fixes the signed representation.

## Pure and effectful checks

A **pure check** claims a reproducible computation over identified inputs. Examples include builds,
lint, and tests packaged as Nix derivations. Its statement records the input digest, derivation
digest, output digest, and result. Another verifier can attempt the same computation and compare
those values.

An **effectful check** records an observation of a changing environment. Examples include a test
against a live API or a measurement whose result is not reproducible from fixed inputs. Its
statement records the check, environment, time, and observed result. Repeating it later produces a
new observation, not necessarily confirmation of the old one.

A test involving services may still be packaged as a pure check if the relevant environment is
controlled, for example in a `nixosTest` VM. Doing that reduces dependence on mutable external
state. Classifying a runner as pure is still a claim about its execution conditions and
reproducibility.

The policy chooses the required runner. An attestation cannot select a weaker transfer rule for
itself by claiming a different check kind. [Integration](./integration.md#evaluating-a-request)
defines how the two kinds of evidence transfer to a new base.

## Tree identity

The primary subject of an attestation is a digest of the checked tree. A Git commit ID may accompany
it as an advisory identifier, but does not define the subject.

The `valley-tree-v1` digest is SHA-256 over a canonical manifest:

1. Begin with `valley-tree-v1` followed by a newline.
2. Enumerate every regular file, executable file, and symlink at its path relative to the tree root.
3. Sort entries by path bytes in ascending order.
4. Append each entry in this form, followed by a newline:

   ```text
   <mode> <SHA-256 of content in lowercase hex> <path byte length in decimal> <path>
   ```

Modes are Git's `100644`, `100755`, and `120000`. A symlink's content is its target. Directories are
implied by the paths beneath them and have no entries of their own. Submodules are rejected because
their content is not contained in the tree being digested.

The path length prevents an embedded newline in a path from being confused with an entry boundary.
The final digest is rendered as lowercase hexadecimal.

Commit metadata, timestamps, and store locations do not enter the manifest. Two commits containing
the same tree therefore have the same subject digest. If rebasing changes the tree, the original
attestation still names the original tree; the integrator must establish whether its evidence
transfers. [Base binding](../.the-valley/ideas/ida-cbcbb3c-attestations-bind-to-a-base.md) explains
that distinction.

The helper uses the same manifest for a pure check's output. A single-file output is represented as
an entry at the empty path.

### Input and derivation digests

`valley-inputs-v1` identifies the set of input names reported by a runner. Its manifest begins with
`valley-inputs-v1` and a newline. Names are sorted by bytes and deduplicated. Each is written as its
byte length in decimal, a space, the name, and a newline. SHA-256 over that manifest, rendered in
lowercase hex, is the digest.

The Nix runner obtains these names from the derivation's referenced store paths. It separately
hashes the derivation file's bytes with SHA-256. These are the two values recomputed for pure
evidence transfer. The definitions and reference implementation are in
[attest/digest.go](../attest/digest.go) and [attest/check.go](../attest/check.go).

## Statement text

A signature covers bytes, so each statement has one canonical text representation. The first line is
the statement type. Each remaining line contains a field path, one space, and a value. The text ends
with a newline.

This schematic example abbreviates digests and is not a valid statement to submit:

```text
the-valley/attestation/v1
subject.primary valley-tree-v1
subject.digest.git-sha1 99b5f2f7…
subject.digest.valley-tree-v1 73847e0b…
predicateType the-valley/check/pure/v1
predicate.check.name prose-format
predicate.check.runner nix
predicate.check.attribute prose-format
predicate.result passed
predicate.inputs.valley-inputs-v1 8df8c199…
predicate.derivation.sha256 ef69ed0c…
predicate.output.valley-tree-v1 cf1f5a9c…
provenance.harness example-harness
provenance.model example-model
provenance.delegation.0.principal human:operator
provenance.delegation.0.grant authorized project work
```

The first line identifies both the kind of document and its version. It also distinguishes these
signed bytes from other kinds of statement the same key might sign.

Dots separate field-path segments. A decimal segment denotes an array index:
`provenance.delegation.0.principal` names the principal in the first recorded delegation.

### Line order

Render fields in this order, omitting only fields the statement does not carry:

1. Statement type, as the first line.
2. `subject.primary`, then `subject.digest` entries.
3. `predicateType`.
4. `predicate` fields in the order defined for that predicate type.
5. `provenance`: `harness`, `model`, `prompt`, `context`, then `delegation`.

The check predicate orders are:

| `the-valley/check/pure/v1`  | `the-valley/check/effectful/v1` |
| --------------------------- | ------------------------------- |
| `predicate.check.name`      | `predicate.check.name`          |
| `predicate.check.runner`    | `predicate.check.runner`        |
| `predicate.check.attribute` | `predicate.check.attribute`     |
| `predicate.check.command`   | `predicate.check.command`       |
| `predicate.result`          | `predicate.environment`         |
| `predicate.inputs`          | `predicate.observedAt`          |
| `predicate.derivation`      | `predicate.result`              |
| `predicate.output`          |                                 |

The third predicate, `the-valley/integration/transfer/v1`, records the integrator's decision that
evidence applies to the landed tree. Its field order is:

| Field or group                                               | Meaning                                             |
| ------------------------------------------------------------ | --------------------------------------------------- |
| `predicate.change`                                           | Change identifier                                   |
| `predicate.target`                                           | Target ref                                          |
| `predicate.base`                                             | Digest set identifying the originally attested tree |
| `predicate.policy`                                           | Digest set identifying the governing policy         |
| `predicate.required`                                         | Explicit zero-check case, when applicable           |
| `predicate.checks.<i>.name`, `.rule`, `.evidence`, `.inputs` | Per-check transfer verdicts                         |

A transfer statement must use exactly one of two forms:

- When checks were required, `predicate.checks` contains an entry for each, and `predicate.required`
  is absent.
- When none were required, it contains `predicate.required none`, and `predicate.checks` is absent.

An empty array cannot represent the zero-check case because it produces no lines.

Digest sets and other maps whose keys vary by caller sort by key. Arrays use numeric index order, so
element 10 follows element 9. Each recorded delegation writes `principal` before `grant`. A field
without a defined position is rejected. New fields need a defined order and appropriate versioning;
arbitrary document fragments have no independent canonical form.

### Allowed values

The text format has no escaping. Its constraints are part of the signing contract:

- Leaves must be nonempty strings. Numbers, booleans, and null are rejected.
- Values cannot contain ASCII control characters, including newlines.
- Empty objects and arrays are rejected, rather than silently dropped.
- Field names begin with an ASCII letter and continue with letters, digits, or dashes.
- Array indices start at zero, have no gaps, and use decimal without leading zeros.

A verifier accepts text only if parsing it and rendering the resulting document reproduces the same
bytes. Duplicate keys, incorrect ordering, unsupported fields, and values that cannot be represented
therefore fail verification.

[schema/attestation.cue](../schema/attestation.cue) validates statement fields and values. Schema
validation and canonical rendering must agree on the document being signed.

## Signed notes

A signed note contains the statement text, a blank line, and one or more signature lines. Each
signature line has this form and ends with a newline:

```text
— <signer name> <base64 of key hash followed by signature>
```

The leading character is an em dash, U+2014. The base64 payload contains the four-byte key hash in
big-endian order followed by the raw Ed25519 signature over the statement text, including its final
newline.

This is the signed-note format used by `golang.org/x/mod/sumdb/note`. Several signers add sibling
signature lines under the same statement text. Adding a signature does not change the claim or the
bytes earlier signers covered.

The signature covers the statement text. It does not cover Git objects, envelope fields, or the
signer's displayed name. The statement type on the first line provides the distinction between kinds
of signed claim.

### Signing names and verifier keys

The naming convention is a host's fully qualified name followed by the signing purpose, for example
`laddie.gunk.dev/attestations`.

A verifier receives known keys as one line per key:

```text
<name>+<eight hexadecimal digits of key hash>+<base64 public-key encoding>
```

The public-key encoding contains the algorithm byte followed by the public key. The hash is the
first four bytes of SHA-256 over the name, a newline, the algorithm byte, and the public key.
Publishing one raw key under two names therefore produces two verifier identities.

Unknown signatures do not establish trust. At least one recognized signature must verify, and an
invalid signature claiming a recognized key causes the whole note to fail. A valid sibling signature
does not excuse that failure.

The helper signs with a raw Ed25519 key supplied by the host. A host SSH key can serve this role;
provisioning and custody are deployment concerns. Native SSH commit signing and signed-note
verification are separate formats with separate verifier configuration.

The planned physical human approval in the
[core decision](../.the-valley/decisions/dcr-9c75283-substrate-adoption-and-the-core.md) needs its
own usable signing mechanism. The current host-signed note does not establish that a person
physically approved the change.

## The helper

`nix run .#attest -- <command>` runs the reference helper. Its commands are:

| Command  | Behavior                                                                                                                |
| -------- | ----------------------------------------------------------------------------------------------------------------------- |
| `run`    | Run checks over an export of a committed tree, compose and sign one statement per check, and store the evidence locally |
| `verify` | Validate the note, statement, and recognized signatures; optionally compare the subject with a repository tree          |
| `digest` | Compute the tree digest for a revision                                                                                  |
| `inputs` | Evaluate pure checks and print input and derivation digests without building them                                       |
| `render` | Render a statement supplied as JSON into canonical text                                                                 |
| `sign`   | Sign statement text or add a signature to an existing note                                                              |
| `key`    | Print the verifier key corresponding to a signing key                                                                   |

`attest run` uses committed content rather than uncommitted files in the working directory. The key
is supplied with `--key`; `--name` selects the signing name, defaulting to the host's. There is no
unsigned mode. A failing check, invalid statement, or signing failure prevents publication.

Evidence is stored beneath:

```text
refs/the-valley/attestations/<tree digest>/<signer key hash>
```

With `--push`, the helper publishes the topic branch and its attestation ref in one atomic native
Git push. This does not submit an integration request. The
[contributor workflow](./contribute.md#publishing-and-requesting-integration) explains how
`valley review` publishes evidence and a request together.

`attest verify` checks canonical text, schema validity, and signatures by recognized keys. With
`--repo`, it also checks the named tree; `--signer` requires a particular signer's signature. It
does not rerun the check. Witness re-verification is a separate planned operation.

Run provenance is optional caller input through `--provenance`. It can record the harness, model,
prompt and context digests, and delegation chain. The helper does not collect those values
automatically or establish that a recorded delegation was authorized.

The files in [attest/conformance/](../attest/conformance/) pair documents with exact text and signed
notes, and include inputs that must be rejected. The `attest-conformance` check tests this
interoperability contract.

## Trust and limits

The mechanisms establish different things:

| Mechanism                         | What it establishes                                                            | What it does not establish                                             |
| --------------------------------- | ------------------------------------------------------------------------------ | ---------------------------------------------------------------------- |
| Signature                         | A recognized key signed these bytes                                            | That the stated check actually ran or the signer described it honestly |
| Tree and computation digests      | Which content and computation the claim identifies                             | That the check itself enforces the intended requirement                |
| Reproducible execution            | A basis for independently checking a result                                    | Proof from the author's report alone                                   |
| Pinned tool and check definitions | Which implementations are intended to run                                      | That a compromised host actually used them                             |
| Witness re-verification, planned  | Independent comparison for the checks sampled                                  | Immediate detection or coverage of every accepted claim                |
| Transparency log, planned         | Evidence of publication and later tampering under the log's verification model | Correctness of execution or automatic recovery from a bad change       |

A host with access to a signing key can make a false but correctly signed statement, including a
false claim about the tool it used. A sandbox helps control normal execution but cannot make the
host's own report independent of that host.

There is also a current
[check-definition gap](../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md):
the policy comes from governing state, but check derivations still come from the candidate tree.
Rerunning a weakened check can reproduce its result without detecting the weakened requirement.

The proposed trust system still needs rules for sampling, new signers, revocation, retention, and
recovery after a false claim has already been accepted. These limits do not establish a supply-chain
assurance level or a guaranteed detection window.
