# Self-transparency (DRAFT — candidate invariant, unresolved)

> **Status: DRAFT.** This document names a candidate invariant and collects the facets that look like instances of it. It does **not** resolve it. The statement, the mechanism, and the base case are all open. Treat nothing here as decided.

## The candidate invariant

A single property keeps surfacing, unnamed, across the design:

> **No actor can durably change the system, or an output of the system, without transparency.**

Recursive all the way down. The system's own policy, controllers, and configuration are not a privileged layer exempt from the rules — they are themselves outcomes, produced and integrated exactly like code. the-valley builds the-valley. A change to the integrator's policy goes through integration; a change to a controller goes through attestation. No back door, no out-of-band edit the log does not see.

"Durably" is load-bearing and underspecified: a transient, self-correcting change may not need the same ceremony as one that persists. Where that line sits is part of what is unresolved.

## Facets it unifies

Each was raised locally, as a wrinkle in one subsystem; together they look like one property the design has been circling without stating:

- **Integrator self-integration.** The integrator is code in a repo; how does *its* code get integrated? ([openquestions.md](./openquestions.md), *Identity & trust bootstrapping*.)
- **Policy bootstrap.** Someone must land the first principle before any policy exists to govern principle changes — the recursion needs a base case that does not itself recurse.
- **Load-bearing principles.** Active principles can constrain what the integrator accepts ([architecture.md](./architecture.md), *project knowledge is a typed-node graph*) — the rules the system runs by are themselves transparent, versioned nodes.

## Why it stays unresolved

- **Statement.** "Durably change" and "an output" need precise definitions before the invariant can be enforced rather than admired.
- **Base case.** A recursive invariant needs a grounding step that does not itself recurse; "key-holders land the first policy under relaxed rules" is a gap, not an answer.
- **Mechanism.** If policy and config are outcomes integrated like code, what enforces that there is *no* other path? Nothing yet names the closure.
- **Cost.** Full recursion implies every config tweak carries integration ceremony; where the system relaxes that — and stays honest about relaxing it — is undesigned.

If it holds, this is plausibly the top-level invariant the whole design is an implementation of — which is exactly why it should not be declared settled on a stub. Tracked in [openquestions.md](./openquestions.md).
