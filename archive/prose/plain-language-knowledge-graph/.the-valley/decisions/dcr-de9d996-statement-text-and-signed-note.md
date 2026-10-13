---
type: decision
id: dcr-de9d996
status: decided
title: "Serialize attestation statements as canonical text and sign them as notes"
created: 2026-08-03
source: design conversation, 2026-08-03
---

# Serialize attestation statements as canonical text and sign them as notes

An attestation is stored as readable statement text followed by a blank line and one signature line
per signer. The serialization rules determine the exact bytes covered by each signature.

[verification.md](../../design/verification.md#statement-text) specifies the text format and
[signed-note envelope](../../design/verification.md#signed-notes).
[schema/attestation.cue](../../schema/attestation.cue) defines the statement shape. The reference
implementation is in [attest/text.go](../../attest/text.go) and
[attest/sign.go](../../attest/sign.go).

## Text format

The first line identifies the statement type and version. Each later line contains a key, one space,
and a value. Keys are dotted paths to fields; numeric path segments identify array positions.

The type appears only on the first line. Because signatures cover the text, that line also
distinguishes an attestation from another kind of document signed with the same key.

The order is fixed: subject, predicate type, predicate content, then provenance. Each predicate type
defines the order of its own fields. A pure-check claim gives the check, result, and reproducibility
digests. An effectful claim gives the check, environment, time, and observation. The complete order
is in [verification.md](../../design/verification.md#line-order).

Maps whose names are supplied by a caller, such as digest sets, sort by key. Arrays retain index
order. A field without a defined position is rejected. Adding a field therefore requires defining
its place in the versioned format.

## Values and empty containers

Values are written directly, without escapes. The format rejects values containing newlines, empty
values, numeric leaves, empty containers, and field names containing separators such as dots or
spaces.

Rejection ensures that the validated statement and signed statement contain the same information. A
renderer must not silently omit a value it cannot represent.

An empty list or object would produce no lines, so it cannot be serialized. A statement that needs
to express a zero case must use an explicit value and omit the empty container. The schema must
define what that value and the omitted field mean. Otherwise it could accept statements that cannot
be signed.

## Verification uses the stored bytes

The stored note contains the exact text that was signed. A reader can inspect it with `git cat-file`
or search it as plain text.

Parsing alone is insufficient. The verifier must render the parsed statement again and require
byte-for-byte equality with the input. It rejects noncanonical text even when a signature over that
text is valid. This prevents different implementations from interpreting equivalent-looking
encodings differently.

## Multiple signatures

Each signer signs the same statement text independently. Signature lines appear as siblings beneath
that text. Moving a signature to different text causes verification to fail.

The envelope follows the signed-note format used by `golang.org/x/mod/sumdb/note` and
transparency-log checkpoints. The
[Phase 6 transparency work](../../design/roadmap.md#phase-6--trust-backstop) can therefore use the
same envelope family; see
[[ida-d2dc957]](../ideas/ida-d2dc957-attestations-ride-on-a-transparent-leaf.md).

A signature covers the text, including its type line. It does not cover a Git object or separately
sign the displayed signer name.
[[ida-51605e8]](../ideas/ida-51605e8-authenticity-not-git-coupled.md) explains the
storage-independent authentication requirement.

## Signer names and verifier keys

A signature line identifies a key by name and hash. A host signing name combines its fully qualified
host name and purpose, for example `laddie.gunk.dev/attestations`.

The verifier-key hash includes the name and public key. Different purpose names therefore identify
different verifier keys even if the underlying public key is the same.

The verifier receives an explicit set of known keys. A key outside that set does not contribute an
accepted signature. A signature that claims a known key but fails verification rejects the note.

The implemented note format supports raw Ed25519 signatures. The signing key is a deployment
parameter. A host's `/etc/ssh/ssh_host_ed25519_key` is one possible source; this decision does not
require every deployment to use it.

## Implementation and conformance

The helper implements the note format with `crypto/ed25519` and the Go standard library. This keeps
a small executable free of external module dependencies. Compatibility depends on the specified
bytes and conformance examples.

[attest/conformance/](../../attest/conformance/) contains statements paired with exact serialized
text, valid notes including a note with two signatures, and inputs that must be rejected. The
`attest-conformance` check runs these fixtures. Unit tests also reproduce the published
sum.golang.org verifier-key hash from its name and public key.

Fixtures describe the contract. They must not be changed merely to match a different implementation
output. Changing the serialization rules can invalidate signatures created under those rules and
therefore requires a format change.

This decision supplies the serialization and envelope details for
[[dcr-0de694f]](dcr-0de694f-phase2-attestation-shape.md). The
[helper documentation](../../design/verification.md#the-helper) describes how they are used.
