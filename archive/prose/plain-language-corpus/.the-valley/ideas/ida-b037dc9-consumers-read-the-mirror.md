---
type: idea
id: ida-b037dc9
status: raw
title: "Move consumer fetches as well as repository writes off GitHub"
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# Move consumer fetches as well as repository writes off GitHub

Changing a repository's canonical origin does not change where its consumers fetch code. A consumer
still reading the GitHub mirror remains dependent on GitHub's availability and on successful mirror
publication.

Migration therefore needs an inventory of deployment and dependency reads as well as writes.

## Cosmo examples

The common system module configures automatic upgrades from `github:patflynn/cosmo`. The
classic-laddie convergence configuration also names cosmo on GitHub, using its SSH Git URL. These
paths must be considered separately when moving deployment reads.

Cosmo's flake also consumes the-valley through `github:gunk-dev/the-valley`, even though
the-valley's canonical origin is already hosted independently.

A publication mirror can legitimately serve consumers, as decided in
[[dcr-24d62f7]](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md). Continuing to use
it may be a deliberate choice. It must not be mistaken for having removed that dependency.

## Ownership of convergence

Two approaches remain possible.

Cosmo can own the migration of its upgrade, convergence, and dependency URLs. The-valley would
provide hosting and integration without adding a deployment controller for this purpose.

Alternatively, deployment after integration can become a reaction managed through the-valley's event
and controller model. Cosmo would provide an early use of the
[Phase 5 work](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller).

This is a scope decision to make before building the replacement.

## Reachability

Every machine or runner evaluating a flake input must be able to fetch it. A tailnet-only canonical
host does not serve outside consumers automatically.

Possible arrangements include placing all relevant consumers on the tailnet, providing another
reachable remote under the project's control, or intentionally retaining mirror reads for selected
consumers.

[[oc-87deec8]](../outcomes/oc-87deec8-valley-can-host-cosmo.md) tracks the migration outcome. The
chosen arrangement must cover both revision discovery and fetching the selected revision.
