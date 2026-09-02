---
type: bug
id: bd-c324184
status: open
title: Optimistic concurrency control starves under contention and foundational changes
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# OCC starves under contention and foundational changes

Integration uses optimistic concurrency control (OCC) over content-addressed evidence
([[dcr-439b771]]). A change lands without running checks on the server if the change merges cleanly
and its required pure checks transfer by input-closure digest equality. When a landed change
modifies a foundational dependency—such as `flake.lock`, root schemas, or shared libraries—it
invalidates the input closures of all concurrently pending changes. Under high change frequency or
multiple concurrent agents, contributors repeatedly experience check staleness and are forced to
re-attest locally, leading to optimistic concurrency starvation (livelock).

## The mechanism of starvation

The integrator evaluates integration requests sequentially at the commit point
([integration.md](../../design/integration.md)). For each pure check, it compares the input-closure
digest recorded in the author's attestation against the input-closure digest recomputed over the
candidate merge tree (`attest inputs`).

In a Nix-native repository, input closures are deep and sensitive. When a foundational file like
`flake.lock` or a common build helper is modified:

1. The input closure for virtually every downstream derivation changes.
2. Even if a pending change has zero syntactic merge conflict with the new tip, its recomputed
   closure digest diverges from the attested closure digest.
3. The integrator marks the check as stale (`request-stale`) and refuses to advance the ref.
4. The contributor or agent must re-run the check locally over the new tip, sign a new attestation,
   and file a new integration request.

If several contributors or automated agents submit changes during an active development window:

- Agent A's change lands, advancing `main`.
- Agents B, C, and D are marked stale.
- Agents B, C, and D pull the new tip and begin local re-attestation.
- Agent B finishes first and lands.
- Agents C and D have their newly computed attestations invalidated again before they can land.

Because re-attestation happens decentralized on contributor nodes rather than in a centralized
queue, the system exhibits classic OCC livelock under contention. If re-attestation latency exceeds
the inter-arrival time of changes to shared dependencies, pending changes starve indefinitely.

## Why this is acceptable today

Today the project operates at $N=1$ human developer plus sequential agent dispatches. Changes are
serialized before integration requests are pushed, and concurrent submissions against the same ref
are rare. The defect is structural to the OCC design and becomes acute as soon as multiple agents
work concurrently or external contributors submit changes.

## Directions, not decisions

- **Integrator-side batching.** The integrator merges multiple pending changes into a single
  speculative candidate tree and evaluates whether their combined closures can be attested once.
- **Closure granularity and path isolation.** Split monolithic flakes and shared policy definitions
  so that documentation, tools, and sub-projects do not include root repository locks in their input
  closures.
- **Staleness reservation windows.** A request that has been invalidated repeatedly acquires an
  exclusive reservation window on the target ref, blocking other requests from landing until its
  re-attestation completes.
- **Pipelined builder backends.** Provide a shared builder cache so that author nodes re-evaluating
  stale closures do not duplicate compilation work from scratch.

## Related

- The OCC integration decision: [[dcr-439b771]]
  ([dcr-439b771-integration-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md))
- Integration protocol: [integration.md](../../design/integration.md)
- Latency and throughput requirements: [requirements.md](../../design/requirements.md)
