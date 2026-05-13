# Observability & feedback

The thing usually called "review" is one slice of a larger problem: how does the system give feedback to anyone or anything that needs it about the state of changes flowing through?

This document reframes review as a special case of *observability + feedback*, and works out what changes when you stop privileging pre-merge human comments as a distinct mechanism.

## What "review" actually bundles

The PR-as-page model conflates several distinct things:

| Concern | What it does | Failure mode today |
| --- | --- | --- |
| Coordination | A place humans converge to discuss a change | Discussion thread is locked to the PR; orphaned at merge |
| Gating | Correctness checks must pass before merge | Slow, brittle, false negatives |
| Record | Persistent trace of why a change happened | Lives inside one vendor's database |
| Notification | Someone needs to look at this | Firehose; no useful prioritization |

Once these are unbundled:

- **Gating** is handled by [attestations](./verification.md), not human review.
- **Record** is the event log itself.
- **Coordination** still needs a focal point — but a derived one, not a stored object (see below).
- **Notification** becomes a routing/priority problem, which is the new hard bottleneck.

## Feedback as events about events

Everything that today shows up in disparate UIs — build results, lint output, deploy state, runtime metrics, downstream-consumer complaints, security scans, trust-score updates, human comments, agent suggestions — has the same shape: an event referencing another event (or a change, which is itself an event).

```
commit C
  ├── attestation A1 (pure check passed)
  ├── attestation A2 (effectful check passed)
  ├── derivation D produced from C
  │     ├── deploy event "D deployed to staging"
  │     │     ├── metric event "latency regression in /api/x"
  │     │     └── user-reported event "feature Y broke"
  │     └── trust event "attester P confirmed by re-verifier"
  ├── comment event "this collides with refactor on branch B"
  ├── agent suggestion event "consider extracting helper from L123"
  └── approval event "looks good for staging deploy"
```

All the same substrate. Subscribers — humans, controllers, agents — consume whichever subset they care about.

## What disappears

- **The privileged "merge" moment.** Refs update because a controller decided to update them based on the events it saw. There's no special event called "merged"; there's a `ref-updated` event with prior attestations and approvals in its causal history. Pre-merge and post-merge feedback share the same machinery, and a regression observed three weeks later is mechanically indistinguishable from a pre-merge lint warning — only the latency differs.
- **The PR object.** What people call "a PR" becomes a *view* over events scoped to a change (or a chain of changes). Views are derived, queryable, subscribable. They have no canonical storage of their own.
- **The "review/no-review" binary.** Feedback accrues continuously. A change has *as much* review as the events that have accumulated against it — possibly zero, possibly a lot.

## What appears

### Threads (the coordination focal point)

Humans still need a *place* to converge on a change. A thread is a named, persistent view over events scoped to a change or chain of changes. It is:

- **Referenceable** — has a stable identifier, linkable, embeddable.
- **Subscribable** — you can subscribe to new events on a thread.
- **Closeable, but not deletable** — closure is itself an event; the thread's events remain.
- **Multi-change** — a thread can scope to one commit, a chain, a feature, an incident, anything queryable.

A thread is not stored separately from the log. It's a *query* with a name.

### The priority/routing layer

The hard new subsystem. Every event has potential audiences:

- Humans, with finite attention spans and varying interest profiles.
- Agents, that may want to react autonomously to specific patterns.
- Controllers, that act on event types they're configured for.

Routing must answer: for each event, who needs to know, how urgently? This is the part of GitHub's UX that fails most loudly today (notification firehose) and the part most worth getting right.

Possible shapes — none decided:

- Per-subscriber rule sets (CUE schemas declaring "I care about events matching X").
- Learned priorities — a model observing what events a human actually opens vs ignores.
- Hand-curated digests at varying cadences.
- Escalation chains — an event with no acknowledgment after $T promotes itself.

This subsystem deserves its own design document once the shape gets clearer.

### Trust as a feedback signal

The attestation trust controller from [verification](./verification.md) is itself a feedback subsystem: it consumes confirm/deny events from re-verifiers and emits trust-update events. Other controllers react to trust changes by tightening or loosening gating. Trust is feedback to the system about the system's own past decisions.

## How this dissolves the agent-review question

"How do humans review agent-authored code" was the wrong question. The right questions:

- What filter and priority logic decides which agent-emitted events reach a human?
- What's the human's verb when an event reaches them — comment, approve, reject, dispatch a follow-up agent, demote the attester's trust?
- For agent-authored changes whose attestations are strong, why would a human be involved by default at all?

The default flow for a well-attested agent change becomes: events flow, controllers react, no human attention required. Humans engage when something is *flagged for them* by the priority layer — high-impact change, low-trust attester, attestations conflicting with later signals, explicit subscription. Pre-merge human review becomes the exception, not the rule.

## Honest tensions

- **Attention is now the gatekeeper.** "Don't drown the human" replaces "don't merge bad code" as the system's hardest job. The priority layer is load-bearing in a way no piece of GitHub is today.
- **Threads-as-queries are unusual UX.** Most people expect URLs that point to stored objects, not query results. The system must make this feel solid — stable IDs, snapshot semantics, predictable refresh.
- **Late feedback has a half-life.** In principle a change accrues feedback forever. In practice attention has to age out. Probably: events stay, but views can mark themselves dormant after some criterion (deployed and stable for $T, no recent activity, explicit closure).
- **Revert and roll-forward semantics.** When post-merge feedback says "this is bad," what should happen? Probably: emit a `regression-suspected` event, controllers decide (auto-rollback for high-confidence runtime signals, agent-dispatched fix for code-level issues, human escalation for ambiguous cases). The mechanism exists; the policies are still to be designed.

## Open questions

- **Priority layer architecture.** Per-subscriber, learned, both? Probably starts hand-configured and grows.
- **Thread identity and naming.** UUID? Human-readable slug? Tied to a commit, a chain, a topic?
- **When does a thread close?** Auto-close on deploy-stable? Explicit close events? Both?
- **How does feedback reach the author when the author is an agent that has finished its run?** Probably: events spawn new agent runs scoped to acting on them. klaus-shaped.
- **Cross-repo feedback.** A change in repo A breaks a consumer in repo B. The consumer's event needs to land somewhere visible to A's thread. Cross-repo routing is non-trivial; deferred.
