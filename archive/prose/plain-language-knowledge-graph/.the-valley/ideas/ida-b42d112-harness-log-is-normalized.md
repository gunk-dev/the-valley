---
type: idea
id: ida-b42d112
status: exploring
title: "Record all agent-run inputs and outputs in a common format"
created: 2026-08-02
source: design conversation, 2026-08-02
---

# Record all agent-run inputs and outputs in a common format

A harness should produce one normalized record of everything that enters and leaves an agent run,
including messages delivered through different channels. Provenance can then be derived from a
common representation across harnesses.

A run record that omits an injected message can misrepresent the conditions that produced the
result. Keeping that message in a second, unrelated log does not make the first log sufficient for
reconstruction.

[[ida-45178f6]](ida-45178f6-agent-identity-is-provenance.md) identifies the inputs needed for
provenance. The logging contract must account for each of them and make unsupported channels
visible.

## Across harnesses

A common record format would let analysis and provenance tools consume runs from different
harnesses. Each harness would need an adapter that meets the same completeness requirements.

This supports the provider and harness independence proposed in
[[ida-1bda403]](ida-1bda403-executor-bundles.md). It also makes incomplete logging a concrete
conformance problem rather than an assumption hidden in each integration.

## Trust and open questions

Normalization does not prove that the harness recorded everything accurately.
[[ida-a8243d2]](ida-a8243d2-agent-runs-act-under-delegated-authority.md) asks how to trust a
delegation record written by the system that executed the run.

The canonical format remains open. So does the evidence needed to check completeness: the log alone
may not reveal an input that was never recorded.

The system also needs a policy for work produced by a harness that cannot supply a complete
normalized record.
