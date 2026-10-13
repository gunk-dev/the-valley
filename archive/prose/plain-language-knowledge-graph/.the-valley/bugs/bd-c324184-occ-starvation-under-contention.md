---
type: bug
id: bd-c324184
status: open
title: "Repeated changes to shared check inputs can starve pending work"
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Repeated changes to shared check inputs can starve pending work

A pending change can repeatedly lose usable check evidence if other landings keep changing the
inputs its checks depend on. When producing fresh evidence takes longer than the interval between
those invalidations, the change may make no progress toward integration.

This is a possible starvation condition in the optimistic integration rules of
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md). It
depends on overlap between check inputs, landing order, and verification time. Concurrent
contributions alone do not establish starvation.

## How it occurs

The integrator compares each pure check's attested input-closure digest with the digest for the
candidate tree.

A change to a shared dependency, root schema, or build helper can alter that digest for several
pending changes. A broad source closure may make even a small edit relevant to many checks. Only
checks whose inputs change become stale.

For example, A lands while B and C are being checked. Both need fresh evidence because A changed
shared inputs. B finishes and lands first. If B also changes inputs used by C, C must repeat the
work again. Continued invalidation of that kind can prevent C from landing.

A clean textual merge does not avoid the problem. The check may depend on files that do not overlap
the proposed edit.

## Evidence needed

The graph identifies the failure condition but does not establish its frequency in the deployed
system. Useful measurements include invalidations per request, repeated invalidations of the same
request, time spent producing fresh evidence, and which inputs cause the churn.

## Candidate responses

Batch pending changes into a candidate tree that can be checked together. This needs an execution
arrangement outside the integrator's serialized decision step.

Narrow input closures where the true dependencies permit it. Omitting a real dependency would make
evidence reuse unsound.

Give repeatedly invalidated work a bounded reservation or other fairness rule. This can improve its
chance of landing but delays competing work.

Share builders and caches to reduce the cost of producing fresh evidence. That can shorten the
vulnerable interval without guaranteeing fairness on its own.

The queue policy remains undecided. [integration.md](../../design/integration.md) and
[requirements.md](../../design/requirements.md) provide the protocol and latency requirements
against which to evaluate it.
