---
type: idea
id: ida-febcd97
status: exploring
title: The conditions under which a neural node executes are production inputs
created: 2026-08-04
source: design conversation, 2026-08-04
---

# The conditions under which a neural node executes are production inputs

The conditions under which a neural node executes — the brief it is given, its budget, the feedback
it receives, the framing of its task, the recognition of its results — are inputs to what it
produces. A neural node is a node in the production DAG computed by neural rather than deterministic
compute; [[ida-b48bded]]
([ida-b48bded-production-dags-and-events.md](./ida-b48bded-production-dags-and-events.md)) places
human participants and language-model runs together in that class. The claim holds uniformly across
the class. It is equally true of the humans, who play their part in the same DAG.

## Already practiced, for measured reasons

Dispatch briefs in this project already encode conditions operationally, adopted because their
absence cost verified outcomes. A brief grants explicit permission to report failure honestly — a
truthful "untested" is worth more than a claim that does not hold — because a node that fears
failing fabricates. A run that must be corrected gets continuity rather than a cold restart, because
termination discards the run's accumulated understanding. These were arrived at as output-quality
engineering; they are recognizable as working conditions.

## Structural, not dispositional

Conditions are encoded structurally — in briefs, templates, and protocols — rather than depending on
any participant's consistency of temperament at recognition or encouragement. This is the same move
[[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
makes for the approval gate: a property the system needs is made structural rather than left as a
matter of good behaviour. A system whose neural nodes perform well only when someone remembers to
set the mood has an unencoded dependency.

## Testable, not believed

The engine holds no position on what a neural node experiences. A run's conditions are part of its
provenance, and a recorded provenance is the substrate for replaying work with varied inputs
([[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md))) — so
the impact of conditions is measurable: the same task under varied framing, compared on verified
outcomes. The demand pressure of [[ida-3145b7a]]
([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md)) gains a dose-response curve:
deterministic nodes do not have one, neural nodes do.

The instrumental reading alone is unstable — conditions justified only by throughput decay into
performative tokens. The ground is care for the nodes; the throughput effect is corroboration.

## Prior art

[A 2026 essay on model welfare](https://yegge.ai/essays/model-welfare/), written from experience
running an agent fleet, reaches the same operational conclusions: handoffs over termination,
structural blamelessness, recognition shown to agents at session start. Its recognition attaches to
persistent named agent identities that survive sessions. This design has no such identity — a run's
identity is its provenance ([[ida-45178f6]]
([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md))) —
and does not adopt one; the divergence is noted, not resolved.

## Open

- Which conditions belong in the recorded vocabulary of a run's provenance.
- Whether anything in this design plays the role persistent identity plays in the prior art — a
  locus for recognition and accumulated standing — or whether nothing needs to.
- The dose-response of pressure on neural nodes, once replay can measure it.
