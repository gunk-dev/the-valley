---
type: idea
id: ida-ad30302
status: exploring
title: "Attribute build evidence to the machine that produced it"
created: 2026-08-01
source: design conversation, 2026-08-01
---

# Attribute build evidence to the machine that produced it

The machine that executes a build should sign the statement describing its inputs, environment, and
result. This identifies the party asserting that the computation took place.

[[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) adopts host signing for
attestations. [[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) places
the builder's keys in a machine or service principal's registry entry. The agent run is attributed
through provenance, as described in [[ida-45178f6]](ida-45178f6-agent-identity-is-provenance.md).

Builder identity and recorded conditions allow policy to require particular execution hosts,
architectures, or compiler versions. The verifier must still evaluate the evidence and the signer's
standing; a signature alone does not establish a successful build.

The pure-versus-effectful distinction in [verification.md](../../design/verification.md) determines
what can later be reproduced.
