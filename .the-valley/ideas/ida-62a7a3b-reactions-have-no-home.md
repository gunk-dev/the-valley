---
type: idea
id: ida-62a7a3b
status: raw
title: A project's automation has no home on the host — and leaving it on the mirror destroys work
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# A project's automation has no home on the host

The bus publishes `ref-updated` and nothing subscribes. A valley host today has no place to put a
program that reacts to a project's events, and no place to put a program that runs on a schedule.
Automation a project depends on therefore has nowhere to land at migration, and — the sharper half
of this — the automation it leaves behind on the GitHub mirror does not merely stop working. It
silently destroys its own output.

## Two shapes, not one

cosmo carries three bots, and they are two different shapes:

- **Reactive to a ref update.** `update-klaus.yml` and `update-the-valley.yml` fire on
  `repository_dispatch` and bump exactly one flake input each. Their input is an upstream push. That
  is precisely what `ref-updated` already carries ([schema/events.cue](../../schema/events.cue)) —
  the event exists, the subscriber does not.
- **Scheduled.** `update-flake-lock.yml` runs nightly on cron, does a full `nix flake update`, opens
  a pull request, and enables auto-merge on it. Its input is a clock. Nothing on the host owns a
  clock that can start work inside a project.

The reactive shape currently runs *through* GitHub even for a repo whose origin is already
sovereign. the-valley's mirror push triggers its own `.github/workflows/notify-cosmo.yml`, which
sends a `repository_dispatch` to cosmo, which opens the bump PR. Migrating cosmo moves the sink of
that chain onto the valley host while the relay in the middle is still a GitHub Action reacting to a
mirror push. The chain does not survive being cut in one place.

## Leaving them running is worse than losing them

A publication mirror receives `main` and the tags and nothing else; every other head found there is
deleted on the next push ([[dcr-24d62f7]]
([dcr-24d62f7-publication-mirror-not-review-queue.md](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md))).
Applied to a bot that writes to the mirror, that invariant has two consequences:

- A branch the bot opens on the mirror is removed by the next unpublish sweep, so the pull request
  it opened points at a deleted branch.
- `main` is replicated with a forced refspec, so a bot pull request that does auto-merge on the
  mirror is overwritten by the next push from the primary. The bump is reported as merged and then
  is not there.

Both failures are quiet. The bot's own run succeeds. Removing these workflows is therefore part of
the migration itself, not cleanup that can follow it. The general form: a publication mirror is a
strictly downstream surface, and anything that writes to one loses its work without being told.

## What would close it

Something on the host that holds credentials, acts, and records what it did — the actuator shape in
[[ida-f1b39e8]]
([ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](./ida-f1b39e8-outbound-effects-pass-through-an-actuator.md)).
The effects these bots need are small and enumerable: edit `flake.lock`, and request integration of
the result. That is a good first vocabulary precisely because it is short.

This is [roadmap Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller)
territory — the first phase in which anything subscribes to an event and acts on it.

## Open

- Whether a scheduled trigger belongs on the host at all. A nightly dependency bump is a standing
  intention, and standing intentions may be better expressed as demand on the outcome graph
  ([[ida-3145b7a]] ([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md))) than as a
  cron entry the host owns.
- Whether a reaction that opens work for review needs a change object to open it against
  ([[ida-93e4f91]] ([ida-93e4f91-changes-not-branches.md](./ida-93e4f91-changes-not-branches.md))),
  which would make these bots depend on the integrator rung rather than only on the reaction rung.
