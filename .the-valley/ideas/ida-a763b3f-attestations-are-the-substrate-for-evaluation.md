---
type: idea
id: ida-a763b3f
status: exploring
title: Attestations are the substrate for evaluation
created: 2026-08-01
source: design conversation, 2026-08-01
---

# Attestations are the substrate for evaluation

Because an attestation records an agent run's inputs, the run can be replayed afterwards with those
inputs deliberately varied — a different model, a different prompt, a different tool environment —
and the resulting trajectories compared. The record is a reproducible definition of an experiment as
much as it is evidence about what happened, and that is what makes systematic evaluation of agent
runs possible against real work rather than against synthetic benchmarks.

The inputs this depends on are the ones enumerated in [[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)). The
carrier is the transparent leaf of [[ida-d2dc957]]
([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md)),
whose double hashing is what lets a large or private input be committed to while being withheld.

## Related

- [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) —
  the record of inputs this reads as an experiment definition.
- [[ida-d2dc957]]
  ([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
  — the carrier the record rides in.
