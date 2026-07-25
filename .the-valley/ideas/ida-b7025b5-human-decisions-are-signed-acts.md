---
type: idea
id: ida-b7025b5
status: exploring
title: Human decisions are signed acts
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Human decisions are signed acts

A human decision or approval is associated with a cryptographic signature — ideally backed by a
hardware key (YubiKey / FIDO). When a human, not an agent, authorizes a change — integrating a
request, landing a principle, editing a policy — that authorization is a signed act: verifiable,
and distinguishable from agent action, rather than an unattested state change.

## Related

- The attestation the self-transparency invariant already requires:
  [self-transparency.md](../../design/self-transparency.md)
- The human counterpart to the open agent-signing question:
  [openquestions.md § Identity & trust bootstrapping](../../design/openquestions.md#identity--trust-bootstrapping)
- The participant whose outputs carry this provenance: [[ida-3145b7a]]
  ([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md))
