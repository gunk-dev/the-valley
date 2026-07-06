---
type: idea
id: ida-b9f646c
status: adopted
title: Nix is a backend, not the substrate — schemas must stay portable to typical Linux
created: 2026-07-06
source: conversation, 2026-07-06
---

# Nix is a backend, not the substrate

Owner guidance: target non-Nix systems at a reasonable moment so no assumption gets baked in that is hard to port to typical Linux. The discipline that makes the moment cheap: **portable schemas from day 0, portable implementations on demand.**

Where Nix is actually load-bearing today:

- **Hosting** — not coupled. The host module's work (git user, git-shell, bare repos, hooks) is plain POSIX; the NixOS module is a *reference installer* consuming [valley.cue](../../schema/valley.cue). Another installer can consume the same file.
- **Events, knowledge** — not coupled.
- **Verification** — the junction. Checks-as-derivations is the reference implementation, not the contract: the attestation schema must record *what check ran, on what tree, what result*, with **purity as a claim tied to a runner kind** (`nix` claims purity strongly; a `plain`/`oci` runner claims less). If derivation-ness leaks into the schema, non-Nix support becomes a migration instead of an added backend.

Review heuristic: every schema must be implementable by a shell script on Debian. A field only Nix can produce is a leak.

The forced moments arrive on their own: a second runner kind when the Phase 2 attest helper is designed; a non-NixOS installer the first time a machine the owner doesn't control hosts a valley. Building either sooner is the premature-generality trap.

Follow-up (PR-class, after the requirements rewrite lands): the README constraint "Nix-native — builds, verification, and artifacts *are* derivations" states an identity; reframe it as reference-implementation-not-contract, and align [verification.md](../../design/verification.md).

## Related

- [[ida-1ec03b1]] — path-scoped verification policy; the runner-kind claim is what its per-class attestations are made of.
