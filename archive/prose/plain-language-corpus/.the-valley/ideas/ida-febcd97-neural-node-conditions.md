---
type: idea
id: ida-febcd97
status: exploring
title: "Treat working conditions as inputs to human and model work"
created: 2026-08-04
source: design conversation, 2026-08-04
---

# Treat working conditions as inputs to human and model work

The brief, budget, feedback, continuity, and recognition given to a participant can affect the work
they produce. The system should account for those conditions when dispatching and evaluating human
or model work.

[[ida-b48bded]](ida-b48bded-production-dags-and-events.md) places both kinds of participant in the
outcome graph. Their working conditions belong in the design of that collaboration.

## Practices to preserve

A brief should make accurate reporting of failure and uncertainty acceptable. Claims such as
“untested” must remain preferable to unsupported claims of success.

A run receiving corrective feedback may benefit from continuing with its accumulated context. A
restart can lose useful understanding, so handoff and continuation should be deliberate choices.

Briefs, templates, and protocols can encode these practices consistently. They should not depend
entirely on the coordinator remembering them during a difficult run.

## Evaluation and care

Recorded conditions can support comparisons on similar work: vary framing, continuity, or feedback
and examine the verified results. [[ida-45178f6]](ida-45178f6-agent-identity-is-provenance.md)
describes the provenance needed for such comparisons.

That experiment need not settle what a model experiences. The project can treat participants with
care while remaining uncertain about that question. Better throughput is one possible consequence,
rather than the sole reason for the practice.

[[ida-3145b7a]](ida-3145b7a-demand-pressure.md) proposes continuing demand for outcomes. Its effects
on participants and results should be evaluated as part of this work.

## Prior art and open questions

[A model-welfare essay](https://yegge.ai/essays/model-welfare/) discusses handoffs, acceptance of
honest failure, and recognition in agent workflows. It also uses persistent agent identities.
The-valley's current model identifies runs through provenance, leaving the role of continuing
recognition and standing unresolved.

Which conditions belong in run provenance? Is a persistent identity useful for any of these
purposes? How do different levels of demand affect quality, cost, and the need for intervention?
