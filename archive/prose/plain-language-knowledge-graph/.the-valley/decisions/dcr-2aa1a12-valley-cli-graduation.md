---
type: decision
id: dcr-2aa1a12
status: proposed
title: "Decide when to replace the valley CLI prototype"
created: 2026-07-17
source: Phase 1 landing and design conversation, 2026-07-17
---

# Decide when to replace the valley CLI prototype

The CLI has exceeded the dependency limit set by
[[dcr-74c3158]](dcr-74c3158-valley-cli-lifecycle.md): its `tail` and `replay` commands require the
NATS CLI. A decision is needed on whether to move it to a separate repository and implementation
language, or continue prototyping while developing a durable specification.

This decision remains proposed. The existing lifecycle policy has not been superseded.

## Options under consideration

Moving the CLI now would make structured parsing and testing easier. Go is the established
implementation language elsewhere in the project. The cost is maintaining a separate project while
the command interface is still developing.

Continuing the prototype would allow the interface and specification to develop together. The
argument for this approach is that an implementation can be replaced from a sufficiently complete
specification and conformance suite, as explored in
[[ida-4557af7]](../ideas/ida-4557af7-spec-driven-iteration.md). Klaus provides a related example:
its GitHub-based prototype is intended to inform a later implementation against the valley design.

Under the second option, specification coverage would become the limit on prototype growth. New
behavior would need a schema, conformance check, or design description sufficient to reproduce it.
Size and dependency count would no longer independently require a rewrite.

The event schema and bus checks provide examples of that coverage. The CLI's review behavior needs
the same assessment: behavior present only in `bin/valley` would prevent the prototype from being
safely replaced from its specification.

## Decision needed

Choose between immediate separation and continued prototyping under a specification-coverage rule.
Adopting the latter requires explicitly superseding the size and dependency rules in
[[dcr-74c3158]](dcr-74c3158-valley-cli-lifecycle.md).
