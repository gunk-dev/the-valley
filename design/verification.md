# Verification

The reframe — attestation-with-revocation instead of CI-as-gate, and the failure-mode trade it makes
— is an architecture bet; see [architecture.md](./architecture.md). This document holds what the
attestation phase actually needs: the two kinds of checks, what an attestation is about, and what
makes one hard to forge.

The shape of an attestation — its statement, its signer, its envelope, where it is stored, and how
several of them compose — is fixed by
[dcr-0de694f](../.the-valley/decisions/dcr-0de694f-phase2-attestation-shape.md), and the statement's
fields are [schema/attestation.cue](../schema/attestation.cue). How a statement is written down and
signed is [dcr-de9d996](../.the-valley/decisions/dcr-de9d996-statement-text-and-signed-note.md), and
the sections below are that decision's detail.

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

## A statement is written down as lines

A signature covers bytes, so a statement has one written form and every implementation must produce
it exactly. That form is lines.

The first line is the statement type, which says what the document is and which revision of it this
is. Every line after it is a key, one space, and a value. The text ends with a newline.

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
    provenance.harness a harness
    provenance.model a model
    provenance.delegation.0.principal human:integrator
    provenance.delegation.0.grant land changes

The statement type is on the first line and nowhere else. It is the one thing a reader needs before
anything else — what this document is, and which revision of it — and a signature covers the text
and nothing else, so the first line is also what separates an attestation from any other note a key
signs. One line answers both.

A key is the path from the document's root to the value, with a dot between segments. A segment that
is a decimal number is an index into an array, so `provenance.delegation.0.principal` is the
principal of the first recorded delegation.

### Line order

A statement is a document a reader reads in order to decide something, so the reader's first
question is answered first. Lines are written in the order below, and that order is fixed.

1. The statement type, as the first line.
2. `subject` — what the statement is about: `primary`, then the digest set.
3. `predicateType` — what kind of claim this is, which says how to read what follows.
4. `predicate` — the claim, in the order the claim reads. Per predicate type:

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

   A pure claim reads as the check, its result, and then the three digests a verifier re-derives
   against. A notarization reads as the check, the environment it ran in, when that was, and what
   was observed.

   The integrator's commit-point claim is the third predicate type, and it is not about a check at
   all: it says that evidence produced over one tree stands for another
   ([integration.md](./integration.md)). It reads as which change, onto what, from which base, under
   which policy, and then the per-check verdicts.

   | `the-valley/integration/transfer/v1`                         |
   | ------------------------------------------------------------ |
   | `predicate.change`                                           |
   | `predicate.target`                                           |
   | `predicate.base`                                             |
   | `predicate.policy`                                           |
   | `predicate.required`                                         |
   | `predicate.checks.<i>.name`, `.rule`, `.evidence`, `.inputs` |

   A transfer statement writes the last of those two lines exactly one way. When the policy required
   checks, `predicate.checks` carries one entry per check and `predicate.required` is absent. When it
   required none, `predicate.required none` is the line and `predicate.checks` is absent. There is no
   third way, and in particular no empty list: an array with no elements has no lines, so it has no
   written form, and a landing may not depend on a container the form cannot write down. A policy
   that asks nothing of a diff is an ordinary outcome, so the zero case is said in a line rather than
   left to be inferred from a missing one.

5. `provenance` — what produced the change, last, because it is context for the claim rather than
   part of it: `harness`, `model`, `prompt`, `context`, then `delegation`.

**Within a set keyed by caller-varying names — a digest set, and any map added later — entries sort
by key.** Fixed order where position carries meaning, sorted order where it does not. An array is
positional, so its elements are written in index order: `provenance.delegation.10` follows
`provenance.delegation.9`.

A field with no position in that order has no written form and is refused. So a new field's position
arrives with the field, and the predicate type is versioned, which is what lets a claim shape grow
without moving what came before it. It also means only a whole statement has a written form: order
is defined per predicate type, so a fragment has no order at all.

### What a line can carry

The form has no escapes, and that is why it was chosen. A value is written as its own bytes, so
there is exactly one way to write it and nothing for two implementations to disagree about. What
follows from that is a short list of things a statement may not carry, each refused rather than
escaped or dropped:

- **Only strings at the leaves.** A number, a boolean and a null have no written form, because
  nobody has decided one and a guess would be a guess about signed bytes.
- **No empty value.** A line's value is what follows its first space, so an empty one would be an
  invisible trailing space. A field a statement does not carry is left out.
- **No control character in a value.** A newline is where a line ends, so there is no way to write
  one inside a value.
- **No empty object and no empty array.** They have no lines, so they cannot be written down. They
  are refused rather than dropped, which is what keeps what was validated and what was signed the
  same document.
- **Field names are a letter, then letters, digits and dashes.** That keeps a name clear of the dot
  that separates segments, the space that separates key from value, and the digits that mark an
  index.

Arrays are dense: indices run from 0 with no gaps, in decimal without leading zeros.

Text is accepted only if writing the document it says back out reproduces it byte for byte. That is
the whole acceptance rule, and it makes "these bytes are the written form of what they say"
something a verifier settles by reading rather than a claim it has to take on trust. Line order, a
duplicated key, a set out of order and a value no line can carry all fail the same way, because none
of them is what a renderer would have produced.

[schema/attestation.cue](../schema/attestation.cue) is the gate, and it holds every value and every
field name to what a line can carry. So a statement with no written form fails validation before a
run has built anything, rather than in a renderer after a check has already passed.

## The envelope is a signed note

A statement is signed as a **note**: the statement text, a blank line, and one or more signature
lines. Each signature line is an em-dash (U+2014), a space, the signer's name, a space, and base64
of a four-byte key hash followed by the raw Ed25519 signature over the text.

    the-valley/attestation/v1
    subject.primary valley-tree-v1
    …

    — laddie.gunk.dev/attestations sEDvC7HO4DfL5ZxbZPFvoVWq2Wt5eGTqZx2h…
    — witness.gunk.dev/attestations lDb9C3YrmxnO0/YWsmWjiu2kteNvGqfQstCy…

This is the format `golang.org/x/mod/sumdb/note` defines and transparency log checkpoints are
published in. It is the envelope for what it does to composition: several parties attesting to one
subject are sibling signature lines under one text, so a signature cannot be lifted onto a different
statement — it sits under the text it covers, and moving it means moving that text with it. It is
also the envelope a [Phase 6](./roadmap.md#phase-6--trust-backstop) checkpoint arrives in, so the
same envelope carries an attestation now and a checkpoint later rather than changing at the
boundary.

A signature covers the text and nothing else: not the signer's name, not a namespace, and no git
object ([ida-51605e8](../.the-valley/ideas/ida-51605e8-authenticity-not-git-coupled.md)). So what
separates an attestation from any other note a key signs is the text's own first line, which is the
statement type.

**The signer's name** is the host's fully qualified name, a slash, and what it is signing:
`laddie.gunk.dev/attestations`. The host part says which machine to go and ask about a key. The
suffix keeps a key signing attestations distinct, as a verifier key, from the same key signing
anything else — the name is inside the key hash, so the same public key published under two names is
two verifier keys and cannot be confused.

**What a verifier is given** is a set of known keys, one per line: the name, the key hash its name
and public key produce, and the public key itself.

    laddie.gunk.dev/attestations+1f9c40b2+AbUmzL2tCH0nR9…

That is the whole of it. There is no allowed-signers file, no certificate and no directory lookup. A
signature by a key not among them is a signature by nobody the verifier accepts, and a signature by
one of them that does not check out refuses the whole note — one good signature standing beside a
forgery is not a note to read past.

The host signs with a raw Ed25519 key. `/etc/ssh/ssh_host_ed25519_key` is the natural production
identity, because the host already has it and it already says which host this is, but the key is a
parameter and provisioning one is deployment.

## The helper

`nix run .#attest` is the Phase 2 helper. `attest run` digests the tree, runs the checks over an
export of that tree rather than over the working directory, composes one statement per check, vets
each against [schema/attestation.cue](../schema/attestation.cue), writes each out as statement text,
signs each as a note, and stores them at
`refs/the-valley/attestations/<tree digest>/<signer key hash>`. With `--push` it publishes the topic
branch and that ref in one atomic native-git push. A failing check publishes nothing, and neither
does a statement the schema rejects or a statement with no written form.

The signing key is a parameter (`--key`), as is the name it signs under (`--name`, defaulting to
this host's). There is no unsigned mode: a run with no key available fails rather than emitting a
statement nobody signed.

`attest verify` takes a note and a known-keys file, and checks four things: that the note's text is
the written form of what it says, that the statement satisfies the schema, that every signature by a
key the verifier holds checks out over exactly those bytes, and — given `--repo` — that the tree in
front of the verifier is the tree the statement names. `--signer` names a signer whose signature
must be present. Re-running a pure check and confirming its output digest is witness
re-verification, and belongs to [Phase 6](./roadmap.md#phase-6--trust-backstop) rather than here.

`attest inputs` prints a pure check's input-closure digest and its derivation digest, evaluating the
check over an export of the revision's tree and building nothing. It exists for the integrator: the
pure transfer rule is closure-digest equality ([integration.md](./integration.md)), and check
latency has no business on the commit point's path.

`attest render` writes a JSON document as statement text, `attest sign` turns statement text into a
note or adds a signature line to one, and `attest key` prints the verifier key for a signing key.
Those bytes are an interop contract rather than an internal step: a second implementation that
writes them differently signs different bytes over the same statement, and every signature already
made stops verifying against it. So [attest/conformance/](../attest/conformance/) holds fixed
documents paired with their exact text and notes, along with the documents and texts that must be
refused, and the flake's `attest-conformance` check runs the whole set.

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
