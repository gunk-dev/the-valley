---
type: idea
id: ida-7638082
status: raw
title: Migration removes a project's verification gate — nothing on the host runs a check
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# Migration removes a project's verification gate

A valley host stores refs, mirrors them, and publishes `ref-updated` events. It runs no checks, and
it has no notion of a change that is blocked on one. A project whose only gate is a pull-request
check therefore arrives on the host with no gate at all. The gate does not degrade at migration; it
disappears, because the pull request it was attached to no longer exists.

This is not a defect in what the host serves today. Phase 0 is hosting only, and the pilot repo's
gate is a human reading a diff, which migrates unchanged. The gap only becomes visible when a
project arrives whose gate is a machine.

## What this looks like for cosmo

cosmo's gate is `.github/workflows/ci.yml`, triggered on `pull_request` against `main`. It runs four
jobs: `nixfmt --check` over the tree, `nix flake check`, `nix build --dry-run` over the
`classic-laddie`, `makers-nix`, and `johnny-walker` NixOS configurations, and `nix build --dry-run`
over four home-manager configurations. `.github/workflows/zizmor.yml` adds a workflow-security lint
on the same trigger.

Two things are worth stating precisely, because they change what is actually lost. The NixOS jobs
are `--dry-run`: what is checked is that the derivation evaluates and its dependencies resolve, not
that the system builds. And the matrix names three of cosmo's five machine configurations — `weller`
and `klaus-worker-0` are already outside it. The gate that vanishes is narrower than "cosmo builds
five machines", and it was already incomplete.

The `push` trigger on `main` survives migration in a misleading way. Mirror pushes do trigger
Actions — the-valley's own `notify-cosmo.yml` relies on exactly that — so cosmo's CI would keep
running on the GitHub mirror after every replicated push. It would run against history that has
already landed on the primary and that the converge loop may already have deployed. A check that
runs after the fact and cannot refuse is a notification.

The checks themselves are not the missing piece. cosmo already runs them where the work is written:
`checks.x86_64-linux.pre-commit-check` in its flake wires `nixfmt`, `detect-private-keys`, and
`zizmor` into the dev shell. What is absent is anything that records that they ran, and anything
that requires them before a ref moves.

## What would close it

The policy design is already held by [[ida-1ec03b1]]
([ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md)):
required attestations are a function of the tree diff, and the policy is data, so it is a CUE
document. That policy as a schema is in flight on a separate branch and is not restated here.

What the policy needs in order to bind is two mechanisms the host does not have:

- A signed attestation that a named check ran on a named tree with a named result, pushed alongside
  the commit — [roadmap Phase 2](../../design/roadmap.md#phase-2--attestations-verification-mvp).
- An integrator that derives the required set from the diff and will not write `main` without it,
  behind the `pre-receive` invariant that makes refusal possible at all —
  [roadmap Phase 3](../../design/roadmap.md#phase-3--the-integrator).

Policy without either is a document. An attestation without an integrator is a receipt nobody
checks. Both phases are therefore load-bearing for a migrated cosmo, which is the sequencing point
made in [[oc-87deec8]]
([oc-87deec8-valley-can-host-cosmo.md](../outcomes/oc-87deec8-valley-can-host-cosmo.md)).

## Open

- Whether a project can migrate with its gate deliberately absent for a period, and what makes that
  acceptable for one project and not another. The pilot repo answered yes implicitly; cosmo's
  stakes — an unchecked change to the configuration of five machines, deployed within the hour by a
  timer — are the reason the same answer does not obviously carry.
