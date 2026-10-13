---
type: idea
id: ida-62a7a3b
status: raw
title: "Move project automation before making GitHub a publication mirror"
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# Move project automation before making GitHub a publication mirror

A migrating project needs replacements for automation triggered by upstream changes and by
schedules. Automation that continues writing to a GitHub publication mirror can lose its output when
the primary next republishes.

This concern covers job execution and triggers. It is separate from the integrator's existing role
as a consumer of integration requests.

## Cosmo examples

Cosmo's `update-klaus.yml` and `update-the-valley.yml` react to upstream notifications and update a
flake input. Its `update-flake-lock.yml` runs nightly, updates dependencies, and opens a request for
review.

The reactive jobs need an event subscription and an executor. The scheduled job also needs a source
of time-based demand.

The-valley's current notification path uses a GitHub Action triggered by its mirror push to send a
repository-dispatch event to cosmo. Moving cosmo's primary repository requires replacing or
deliberately adapting this entire chain.

## Writes to a publication mirror are temporary

[[dcr-24d62f7]](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md) limits the mirror
to `main` and tags. Its cleanup sweep attempts to delete other branches. A bot-created branch on the
mirror can therefore disappear on the next successful sweep.

The primary also force-updates the mirrored `main`. A change merged only on the mirror can be
overwritten by the next publication push.

The bot may report success before either happens. Workflows that author changes on the mirror must
therefore be retired or redirected as part of migration.

## Replacement work

The dependency bots need a small set of actions: update `flake.lock`, preserve the result, and
request integration. They are possible early uses of the actuator in
[[ida-f1b39e8]](ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) and the
[effectful-reactions work](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller).

Two design questions remain. A scheduled dependency update might be expressed as a recurring outcome
instead of a host-owned cron job, following [[ida-3145b7a]](ida-3145b7a-demand-pressure.md). A bot
also needs a durable change identity for review and repeated attempts, as explored in
[[ida-93e4f91]](ida-93e4f91-changes-not-branches.md).
