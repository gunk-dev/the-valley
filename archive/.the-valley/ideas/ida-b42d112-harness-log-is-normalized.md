---
type: idea
id: ida-b42d112
status: exploring
title: A harness's log of an agent run is always normalized
created: 2026-08-02
source: design conversation, 2026-08-02
---

# A harness's log of an agent run is always normalized

A harness's log of an agent run is always normalized: one canonical record, in one representation,
of everything that entered and left the run — regardless of which channel an input arrived through.

Provenance is derived from that record. [[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) holds
that an agent run's identity is its provenance, whose inputs are the context, the prompt, the
environment, the model, and the chain back to the initiating human. A record that omits inputs
arriving by some channels, or that represents them in a shape particular to how they arrived, yields
provenance that is incomplete. It is incomplete in a way that is invisible from the record itself,
since nothing in it indicates what is missing.

The failure is real rather than hypothetical. Verified 2026-07-31 against one harness at one CLI
version: a message injected into a running agent is recorded in one of the two logs that harness
stores and not in the other, in a form particular to the channel it arrived on. That is one harness,
one version, one channel — evidence, not a taxonomy.

Normalization also buys harness-agnosticism. [[ida-1bda403]]
([ida-1bda403-executor-bundles.md](./ida-1bda403-executor-bundles.md)) holds that for language-model
agent runs the-valley is vendor- and harness-agnostic. A normalized log is what makes that possible:
different harnesses, one log shape, one way to derive provenance from it. Without normalization,
every harness would need its own provenance derivation, and a run would mean something different
depending on what executed it.

It changes the character of a known problem too. [[ida-a8243d2]]
([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
asks how a delegation chain is verified when the harness that ran the agent is also the software
recording the citation. Normalization does not answer that. What it does is make the log's
completeness a property that can be required of a harness and checked against, rather than assumed —
a stated requirement in place of an implicit trust.

## Open

- What the canonical form is.
- Whether a log's completeness is checkable from the log alone, or only against something outside
  it.
- What follows for a run whose harness does not produce a normalized log.

## Related

- [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) —
  the provenance derived from this record.
- [[ida-1bda403]] ([ida-1bda403-executor-bundles.md](./ida-1bda403-executor-bundles.md)) — the
  harness-agnosticism a normalized log makes possible.
- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  — the verification question this turns from an assumption into a requirement.
