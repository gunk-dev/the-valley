# Self-transparency (DRAFT — candidate invariant, unresolved)

> **Status: DRAFT.** This document articulates an emerging candidate invariant and gathers the
> design facets connected to it. It does not declare the problem solved. The precise formulation,
> enforcement mechanisms, and base case remain active open questions.

## The candidate invariant

A recurring principle underlies many distinct parts of the valley design:

> **No actor can durably change the system, or an output of the system, without transparency.**

This principle applies recursively to the system itself. The policies, controllers, and
configurations that govern the valley are not exempt from the rules they enforce. They are outcomes
produced, verified, and integrated through the exact same mechanisms as application code.

The valley builds the valley. A modification to the integrator's verification policy must pass
through integration. A change to a deployment controller requires signed attestations. There are no
out-of-band administrative edits that bypass the event log.

The term "durably" is critical: transient, self-correcting cache states do not require the same
ceremonial overhead as permanent changes. Defining the exact boundary between transient state and
durable mutation is part of the remaining work.

## Facets unified by this invariant

Several subsystem problems reflect this single core property:

- **Integrator self-integration:** The integrator is software stored in a git repository. Changes to
  the integrator must be integrated by the integrator itself without circular deadlocks
  ([openquestions.md](./openquestions.md#identity--trust-bootstrapping)).
- **Policy bootstrapping:** The first principle or verification policy must land before any
  automated policy controller exists to evaluate it. The recursive chain requires a well-defined
  base case.
- **Governing principles as knowledge nodes:** Active engineering principles constrain what the
  integrator accepts
  ([architecture.md](./architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)).
  The rules governing the system are transparent, versioned graph nodes.
- **Declarative integrator configuration:** Repository verification thresholds, protected ref
  patterns, and trusted witness lists live as in-repo configuration. Modifying them follows the
  standard attested change protocol.

## Why it remains open

1. **Precise definitions:** "Durably change" and "system output" must be formally defined to enable
   automated enforcement.
2. **The base case:** Grounding the recursive loop without granting arbitrary, untracked root
   authority requires a rigorous bootstrap protocol.
3. **Closure mechanisms:** Ensuring that no alternative write paths exist requires verification
   across the entire hosting and controller surface.
4. **Ceremony and ergonomics:** Applying full verification ceremony to minor configuration
   adjustments risks friction. We must balance rigorous transparency with developer convenience
   without compromising integrity.

## Relationship to the broader architecture

If formally proven and enforced, self-transparency makes the
[outcome engine](./architecture.md#bet-the-knowledge-graph-read-generatively--an-outcome-dag)
trustworthy when modifying its own infrastructure. It also defines the fundamental invariant that
[federation](./architecture.md#federation-the-valley-is-the-unit) must uphold across autonomous
instances.

Self-transparency is tracked as an active design exploration in
[openquestions.md](./openquestions.md#identity--trust-bootstrapping).
