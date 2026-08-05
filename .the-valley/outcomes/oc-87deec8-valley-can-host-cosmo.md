---
type: outcome
id: oc-87deec8
status: open
title: The valley can host cosmo
created: 2026-07-31
blocked_by: [oc-f3bcfd0, ida-7638082, ida-62a7a3b, ida-b037dc9, ida-a0e5d03]
---

# The valley can host cosmo

cosmo is the NixOS configuration that builds five machines, one of which is the pilot host that
serves the valley. It is the obvious second project to migrate, and it is the first project whose
requirements exceed what a valley host serves. This outcome is done when cosmo's canonical origin is
the valley host, its changes are gated at least as well as they are gated today, the automation it
depends on still runs, and its machines deploy without reading GitHub.

## What the host serves today, and why it was enough once

A valley host serves exactly four things: a bare git repository per project, publication mirrors, a
host-level restic backup, and a `ref-updated` bus ([schema/valley.cue](../../schema/valley.cue),
[schema/events.cue](../../schema/events.cue), [nix/valley-host.nix](../../nix/valley-host.nix)).

That was enough for the pilot repo because the pilot repo's gate is a human reading a diff, its only
consumer is a flake input, and it is the only repository the host's single replication identity has
had to authenticate for. Each of those three facts stops being true with cosmo, and a fourth
requirement — automation — has no equivalent on the host at all.

## The four gaps

**No verification gate.** cosmo's gate is a `pull_request` workflow, and S1 direct-push mode has no
pull request for it to attach to, so the gate does not degrade at migration — it disappears.
[[ida-7638082]]
([ida-7638082-host-has-no-check-runner.md](../ideas/ida-7638082-host-has-no-check-runner.md)).

**Automation has nowhere to land, and its current home destroys work.** Three GitHub bots keep
cosmo's flake inputs current, in two distinct shapes — one scheduled, two reacting to an upstream
ref update. Nothing on the host consumes an event and acts, and nothing on the host owns a clock.
Worse, leaving the bots on the mirror is not neutral: the mirror's unpublish sweep deletes their
branches and the forced `main` refspec overwrites their merges, both silently. [[ida-62a7a3b]]
([ida-62a7a3b-reactions-have-no-home.md](../ideas/ida-62a7a3b-reactions-have-no-home.md)).

**Consumers read GitHub.** Every cosmo machine auto-upgrades nightly from `github:patflynn/cosmo`,
the pilot host's converge unit both resolves and fetches from GitHub, and cosmo pins the-valley from
its GitHub mirror. Sovereignty of the write path does not make the read path sovereign.
[[ida-b037dc9]]
([ida-b037dc9-consumers-read-the-mirror.md](../ideas/ida-b037dc9-consumers-read-the-mirror.md)).

**The second mirror has no identity.** The host's one deploy key is already spent on the-valley, and
GitHub allows a given deploy key on one repository. The migration runbook flagged this fork as
deferred until the second migration; cosmo is the second migration. [[ida-a0e5d03]]
([ida-a0e5d03-second-mirror-identity.md](../ideas/ida-a0e5d03-second-mirror-identity.md)).

## The phase mapping — what hosting cosmo pulls forward

**cosmo readiness is not an increment on Phase 1.** This is the decision-relevant content of this
node. Mapped against [roadmap.md](../../design/roadmap.md), the four gaps land like this:

| Gap                    | Where it closes                                                               |
| ---------------------- | ----------------------------------------------------------------------------- |
| No verification gate   | Phase 2 **and** Phase 3 — attestations, then an integrator that requires them |
| Automation has no home | Phase 5 — the first phase in which anything subscribes and acts               |
| Consumers read GitHub  | Phase 5 if convergence is the valley's job; today, if it is cosmo's own       |
| Second mirror identity | Phase 0 — the only gap that is present-phase work                             |

The verification gap needs both of Phases 2 and 3, not either. A policy without an attestation is a
document; an attestation without an integrator is a receipt nobody checks; and the integrator's
refusal only exists because of Phase 3's `pre-receive` invariant. The path-scoped policy that would
govern it is already designed ([[ida-1ec03b1]]
([ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md)))
and its CUE schema is settled in [[dcr-f41f718]]
([dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md))
— so what is missing is not the design but the two mechanisms that make it bind.

So hosting cosmo properly would pull Phases 2, 3, and 5 forward, ahead of S1's own last two
unchecked boxes: a week of real human and agent work without GitHub, and a migration-plus-restore
runbook that is repeatable for the next repo. The roadmap sequences by validation gate, and the
gates of Phases 2, 3, and 5 are about the contributor protocol's ergonomics, pull-based integration,
and reactive controllers — none of them is "a second project is hosted". Hosting cosmo is not a
reason to reorder them; it is a reason to know that it would.

The competing reading deserves stating, because it is the same argument S1 already accepted once.
S1's direct-push interim mode is deliberately degraded, on the theory that feeling a gate's absence
is the validation signal that motivates building it. Migrating cosmo now, gate and all, would be
that theory applied again. The difference is stakes, and only stakes: the interim mode's pain is a
human merging diffs by hand in the pilot repo, while cosmo's is an unchecked change to the
configuration of five machines that a timer deploys within the hour.

The cheap half is available regardless. Repointing cosmo's converge unit, `autoUpgrade`, and its
the-valley input at sovereign URLs is a change to one project's configuration and needs nothing new
from the valley — see the reachability question in [[ida-b037dc9]].

## A note that is not a blocker

cosmo configures classic-laddie, and classic-laddie is the host that would serve cosmo. A cosmo
change that breaks the valley host, sshd, or tailscale removes the push access needed to land the
fix. This is a recovery-path property rather than a blocker: every clone is complete, restic sits
behind the primary ([[oc-9949561]]
([oc-9949561-push-replication.md](./oc-9949561-push-replication.md))), and the machine can be
repaired at the console or from any clone. It is worth naming because it raises the cost of an
unchecked change, which is the whole argument in [[ida-7638082]].

## Why this outcome depends on S1 rather than the reverse

One of S1's unchecked acceptance boxes is that the migration-plus-restore runbook exists and is
repeatable for the next repo, and cosmo is that next repo — which invites hanging [[oc-f3bcfd0]]
([oc-f3bcfd0-s1-holds.md](./oc-f3bcfd0-s1-holds.md)) off this outcome. That would be false twice
over. The box asks for a repeatable runbook, not for a second migration to have happened; and making
S1 depend on this outcome would make S1 wait on Phase 5. The truthful edge runs the other way: this
outcome sits downstream of S1, because cosmo does not migrate onto a host whose durability story is
not yet established.
