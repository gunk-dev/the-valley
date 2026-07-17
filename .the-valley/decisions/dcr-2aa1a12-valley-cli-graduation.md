---
type: decision
id: dcr-2aa1a12
status: proposed
title: valley crossed its first graduation tripwire — graduate, or prototype mode until the spec hardens
created: 2026-07-17
source: Phase 1 landing and design conversation, 2026-07-17
---

# valley crossed its first graduation tripwire

The dependency tripwire in [[dcr-74c3158]]
([dcr-74c3158-valley-cli-lifecycle.md](./dcr-74c3158-valley-cli-lifecycle.md)) is crossed: the
`tail` and `replay` verbs need the nats CLI, beyond git and coreutils. The line-count tripwire
(~300) is close behind. Per the lifecycle policy, crossing triggers a deliberate decision, recorded
here.

Two real options:

1. **Graduate now** — own repo, a real language (Go is the house pattern). Buys tests and robust
   parsing; costs ceremony while the verb surface is six verbs in one file.
2. **Prototype mode until the spec hardens.** Everything built along the roadmap is a prototype,
   deliberately, all the way through; re-engineering happens once against a robust and durable spec
   rather than piecemeal at code-size tripwires. The lifecycle policy's dependency and line-count
   tripwires encode the assumption that rewrites are expensive and code must therefore stay
   continuously maintainable — but re-engineering from a hard spec is what the agent toolbox makes
   cheap ([[ida-4557af7]],
   [ida-4557af7-spec-driven-iteration.md](../ideas/ida-4557af7-spec-driven-iteration.md):
   implementations become derived artifacts). Precedent: klaus already lives this way — a frozen
   GitHub-shaped prototype, fix only what bleeds, re-engineered against the valley spec at Phase 4.

Under option 2, one tripwire survives, transformed: **behavior that exists only in the prototype**.
A prototype may grow only as fast as its spec — schemas, conformance checks, design docs — so the
prototype is never the sole description of the system and regeneration stays possible at any moment.
Phase 1 met that bar (schema/events.cue, the closedness checks, the bus-e2e check); the review verbs
currently do not — `valley review`'s behavior lives only in bin/valley. Adopting option 2 supersedes
[[dcr-74c3158]]'s size-and-dependency tripwires with that single spec-coverage tripwire.

Status: proposed — undecided until reviewed.
