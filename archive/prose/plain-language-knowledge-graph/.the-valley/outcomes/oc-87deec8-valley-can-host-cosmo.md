---
type: outcome
id: oc-87deec8
status: open
title: "Host cosmo with its verification, automation, and deployment needs met"
created: 2026-07-31
blocked_by: [oc-f3bcfd0, ida-7638082, ida-62a7a3b, ida-b037dc9, ida-a0e5d03]
---

# Host cosmo with its verification, automation, and deployment needs met

Cosmo can move its canonical origin to the valley when required checks still gate its changes, its
automation continues to work, its machines can deploy without reading GitHub, and its durability is
established.

Cosmo configures five machines, including the host serving the valley. Its migration therefore
requires more than creating a bare repository and changing a remote URL.

## Completion criteria

- [ ] Cosmo's canonical repository is hosted by the valley.
- [ ] Its pre-integration checks are at least as strong as the existing gate, with the intended
      build coverage made explicit.
- [ ] Required check implementations cannot be weakened by the submitted branch.
- [ ] Upstream-triggered and scheduled automation can submit work to the canonical repository.
- [ ] Workflows that would write only to the publication mirror are retired or redirected.
- [ ] Upgrade, convergence, and dependency reads use reachable sources that meet the migration's
      GitHub-independence requirement.
- [ ] Any retained GitHub mirror has a working credential arrangement.
- [ ] The established backup and recovery process covers the migrated repository.

## Dependencies

| Area            | Work to resolve                                                                                                                                           |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Verification    | Select and run the right checks, establish trusted definitions, and verify enforcement; [[ida-7638082]](../ideas/ida-7638082-host-has-no-check-runner.md) |
| Automation      | Replace reactive and scheduled jobs that currently author through GitHub; [[ida-62a7a3b]](../ideas/ida-62a7a3b-reactions-have-no-home.md)                 |
| Consumer reads  | Move deployment and dependency fetches, with reachability verified for every consumer; [[ida-b037dc9]](../ideas/ida-b037dc9-consumers-read-the-mirror.md) |
| Mirror identity | Choose credentials for another mirrored repository; [[ida-a0e5d03]](../ideas/ida-a0e5d03-second-mirror-identity.md)                                       |

The existing attestation and integrator implementations supply part of the verification path.
[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) defines policy
composition, and [[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)
identifies the remaining check-definition boundary.

The linked ideas remain blockers until their design is resolved under the lifecycle rules in
[[dcr-593d3d1]](../decisions/dcr-593d3d1-ideas-are-transitional.md).

## Sequencing

The [roadmap](../../design/roadmap.md) places attestations and integration in Phases 2 and 3, and
effectful automation in Phase 5. The migration needs the relevant capabilities, even if the minimum
implementation is smaller than those phases' full scope.

Some work may remain entirely in cosmo. Repointing fetches is a configuration change if deployment
control stays there. Building a general valley deployment controller is a separate scope choice.

A deliberate interval with weaker checks would need an explicit decision. The consequence is
especially significant for cosmo because integration can be followed by automatic deployment to
several machines.

## Recovery

Cosmo can change the host that serves its own repository. A bad SSH, networking, or host
configuration change could prevent normal access needed to submit a repair.

Recovery therefore needs to remain possible from a clone, the independent backup, and the machine's
console or other recovery access. [[oc-9949561]](oc-9949561-push-replication.md) records the initial
backup and restore exercise.

## Relationship to S1

This outcome depends on [[oc-f3bcfd0]](oc-f3bcfd0-s1-holds.md), which establishes the initial
hosting and durability workflow. S1 requires a repeatable migration and restore runbook; it does not
require cosmo to have migrated already.

The dependency remains in this direction so the initial durability outcome does not wait on cosmo's
additional automation requirements.
