---
type: idea
id: ida-b037dc9
status: raw
title: A mirror that consumers deploy from is a dependency, not a mirror
created: 2026-07-31
source: cosmo readiness analysis, 2026-07-31
---

# A mirror consumers read is a dependency, not a mirror

Sovereignty is a property of the read path as much as the write path. Flipping a project's canonical
origin decides where work is written; it decides nothing about where the work is read from. Where a
consumer still resolves the project through the mirror, the mirror's availability remains on the
critical path, and the flip is cosmetic for everything downstream of it.

## What this looks like for cosmo

cosmo reads GitHub in three distinct places, and only one of them is the pilot host:

- `modules/common/system.nix` sets `system.autoUpgrade.flake = "github:patflynn/cosmo"`, nightly.
  This module is imported by every machine, so this is not one host's habit — it is how all of them
  update.
- `hosts/classic-laddie/default.nix` runs the `cosmo-rebuild` converge unit. It resolves the target
  with `git ls-remote https://github.com/patflynn/cosmo.git refs/heads/main` and then deploys
  `github:patflynn/cosmo/$rev`. That is two reads, resolve and fetch, and each is independently
  fatal. The unit deliberately fails when `ls-remote` fails, retrying on its hourly timer, so GitHub
  being unreachable means the machine does not deploy.
- `flake.nix` pins the-valley itself as `github:gunk-dev/the-valley`. A repo whose canonical origin
  is already a valley host is consumed through its own mirror.

None of this is wrong today. It is the correct configuration for a project whose canonical home is
GitHub, and the third case is the only one available while the sovereign host is reachable on a
tailnet and GitHub Actions runners are not. Migration is what makes it false rather than merely
awkward: after the flip, the mirror is one push behind by construction and is a surface the project
does not control.

## The open question, stated rather than answered

Whether "a host converges from a project" is a valley concern at all is unresolved, and it should be
decided before anything is built for it. Two coherent answers exist:

- **It is cosmo's own business.** The valley grows nothing. cosmo repoints its converge unit,
  `autoUpgrade`, and its the-valley input at sovereign URLs, and the whole gap closes as a change to
  one project's configuration. This is available now and costs the valley nothing.
- **It is the valley's business.** A machine that deploys when integration succeeds is exactly
  commit → build → deploy as a reaction on the log, which is what
  [roadmap Phase 5](../../design/roadmap.md#phase-5--effectful-reactions-armstrong-as-controller)
  claims to validate. Under this answer, the converge timer is a controller that has not been
  written yet, and cosmo is its first subject.

Deciding needs an answer to reachability, which neither option removes. A flake input must be
fetchable by every consumer that evaluates it. The valley host is tailnet-only, so any machine or
runner outside the tailnet cannot resolve a sovereign URL, and the mirror is what it falls back to.
Whether the answer is a tailnet-only fleet, a second reachable sovereign remote, or an accepted
mirror read for outside consumers is the substance of the question.

## Related

- [[dcr-24d62f7]]
  ([dcr-24d62f7-publication-mirror-not-review-queue.md](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md))
  — the mirror exists so consumers can fetch integrated history, and so GitHub-hosted consumers can
  depend on the project at all. That purpose is what makes this gap easy to miss: the reads are the
  mirror working as designed.
- [[oc-87deec8]]
  ([oc-87deec8-valley-can-host-cosmo.md](../outcomes/oc-87deec8-valley-can-host-cosmo.md)) — the
  outcome this blocks.
