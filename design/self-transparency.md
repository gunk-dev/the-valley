# Self-transparency (DRAFT — unresolved, needs design)

> **Status: DRAFT.** This document names a candidate invariant and collects the scattered facets that look like instances of it. It does **not** resolve it. The mechanism, the precise statement, and the bootstrap story are all open. Do not treat anything here as decided.

## Candidate invariant

A single property keeps surfacing, unnamed, across the other docs:

> **No actor can durably change the system, or an output of the system, without transparency.**

Recursive all the way down. The system's own policy, controllers, and configuration are not a privileged layer exempt from the rules — they are **themselves outcomes**, produced and integrated exactly like code. the-valley builds the-valley. A change to the integrator's policy goes through integration; a change to a controller goes through attestation; a change to the config that governs integration is itself a knowledge-graph node under the same governance. There is no back door, no out-of-band edit that the log does not see.

"Durably" is load-bearing and underspecified: a transient, self-correcting change may not need the same ceremony as one that persists. Where that line sits is part of what is unresolved.

## Scattered facets

The invariant is not new design — it is the thing several existing open questions are each a piece of. Collected:

| Facet | Where it currently lives | What it is an instance of |
| --- | --- | --- |
| **Integrator self-integration** — the integrator is code in a repo; how does *its* code get integrated? | [openquestions.md](./openquestions.md) (*Identity & trust*), [integration.md](./integration.md) | The system changing itself must go through the same transparency as any change |
| **Policy bootstrap** — someone must land the first principle before policy exists to govern principle changes | [openquestions.md](./openquestions.md) (*Policy & configuration*), [integrator-internals.md](./integrator-internals.md) | The recursion needs a base case; transparency must hold even at t=0 |
| **Per-repo integrator config as a `config` node** — the integrator's own configuration is a knowledge-graph node, queried alongside principles | [openquestions.md](./openquestions.md) (*Policy & configuration*), [integrator-internals.md](./integrator-internals.md) | The system's configuration is an output governed like any output |
| **Principles load-bearing on integration** — active principles with `enforced_by` constrain what the integrator accepts | [knowledge.md](./knowledge.md), [integrator-internals.md](./integrator-internals.md) | The rules the system runs by are themselves transparent, versioned nodes |

Each was raised locally, as a wrinkle in one subsystem. Seen together they look like one property the design has been circling without stating.

## Why it is unresolved

- **Bootstrap / base case.** A recursive invariant needs a grounding step that does not itself recurse. v1 hand-waves this (integrator-key-holders land the first policy with relaxed rules — see [openquestions.md](./openquestions.md)). That is a gap, not an answer.
- **Statement.** "Durably change" and "an output" need precise definitions before the invariant can be enforced rather than admired.
- **Mechanism.** If config and policy are outcomes (see [outcomes.md](./outcomes.md)) and changes to them are integrated like code, what enforces that there is *no* other path? Today nothing names the closure.
- **Cost.** Full recursion implies every config tweak carries integration ceremony. Where the system relaxes that — and stays honest about relaxing it — is undesigned.

## Relationship to the rest of the design

If it holds, this invariant is the property that makes the [outcome engine](./outcomes.md) trustworthy when pointed at itself, and the property that [federation](./federation.md) must preserve across group boundaries (a federated change is still a transparent one). It is plausibly the *top-level* invariant the whole design is an implementation of — which is exactly why it should not be declared settled on a stub.

## Open questions

See [openquestions.md](./openquestions.md) — the scattered facets above are consolidated there as a single unresolved item under *Policy & configuration*, noting they are facets of one candidate invariant.
