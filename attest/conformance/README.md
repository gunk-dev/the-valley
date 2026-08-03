# Conformance vectors for the canonical form

These files are the interop contract for the bytes an attestation signature covers. A second
implementation of `attest` — a rewrite of this one, or a reader written in another language — must
render every `input.json` here into exactly the bytes of the `canonical.json` beside it. Anything
else and the two sign different bytes over the same statement, so a signature made by one does not
verify against the other, and the way that failure arrives is a verification error that names
nothing.

The serialization itself is RFC 8785 canonical JSON, narrowed by excluding numbers, and the reasons
for that are in [`../canonical.go`](../canonical.go) and in the decision node it cites.

## What is here

Each directory under `vectors/` is one vector.

- `input.json` is a statement or fragment written in some arbitrary rendering — members out of
  order, whitespace anywhere, escapes chosen freely. It is input, not output.
- `canonical.json` is the exact byte sequence the canonical form produces from it. There is no
  trailing newline: the file is the bytes, and nothing else is signed.
- `statement.sig`, where present, is a detached SSHSIG signature over those exact bytes, under the
  namespace `the-valley.attestation.v1`, made by the key in `../signer.pub` and listed in
  `../allowed_signers`. That key exists for these vectors and holds no authority anywhere.

The vectors cover the places implementations actually part company: member order over keys that
differ only in case, keys where one is a prefix of another, and the empty key; nesting of objects
inside arrays inside objects; empty objects, empty arrays and the empty string; every string escape,
including the control characters that have short forms and the ones that do not, the characters a
JavaScript-oriented encoder escapes and RFC 8785 does not, and text above the basic multilingual
plane; and the difference between an absent `provenance` and one present but empty, which the schema
permits and which is two different byte sequences.

A vector whose `canonical.json` is a whole statement also vets against
[`../../schema/attestation.cue`](../../schema/attestation.cue), so the set does not drift into
documents no verifier would accept. Numbers are absent on purpose — a statement carries none, so the
canonical form of one has never been decided and `attest` refuses rather than guessing.

## Running them

`nix flake check` runs them as `attest-conformance`. Each vector is rendered with
`attest canonical`, compared byte for byte, and re-rendered from its own canonical bytes to confirm
the form is a fixed point. The signed vector is then put through `attest verify`, so the path from a
statement to a signature something else can check is pinned end to end rather than only the
serialization. The check also perturbs one recorded byte sequence and requires the comparison to
reject it, so a vector set that agrees with itself vacuously cannot pass.

By hand, against any one vector:

```
$ nix run .#attest -- canonical attest/conformance/vectors/01-key-order/input.json \
    | cmp - attest/conformance/vectors/01-key-order/canonical.json
```

## Changing them

A vector's recorded bytes may only change when the canonical form itself changes, and that
invalidates every signature ever made under it. Regenerating a `canonical.json` to make a failing
check pass is the one thing this directory exists to prevent. A new vector is a new directory and
needs no registration; the check finds it.
