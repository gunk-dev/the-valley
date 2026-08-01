---
type: idea
id: ida-ad30302
status: exploring
title: Build provenance is signed by a builder identity
created: 2026-08-01
source: design conversation, 2026-08-01
---

# Build provenance is signed by a builder identity

The original attestation use case, alongside the agent-run one, is an attestation that a build and
its tests complete successfully over the build inputs and the build environment. The integrator
takes such an attestation as an article of faith about the change it covers.

That implies a **builder identity** does the signing: the machine that performed the build, a host
such as classic-laddie. It is a third identity class beside the human signer and the agent run.

A builder identity makes a class of policy expressible. That builds must run on a set of trusted
machines. That a named compiler or compiler version is banned. That an architecture is required or
forbidden.

The pure-versus-effectful distinction in [verification.md](../../design/verification.md) already
governs what such an attestation can claim.

## Related

- [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) —
  the sibling identity class.
- [verification.md](../../design/verification.md) — the pure-versus-effectful distinction.
