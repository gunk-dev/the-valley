---
type: bug
id: bd-eaefe82
status: open
title: A change supplies the definitions of the checks that gate it
created: 2026-08-02
source: design review of dcr-f41f718, 2026-08-02
---

# Check definitions come from the branch being gated

The integrator reads the verification policy at the target ref's tip — the policy that is already
integrated — so a change never supplies the policy that gates it ([[dcr-f41f718]],
[decisions/dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md)).
Weakening the policy takes two landings.

That soundness does not extend to the checks themselves. The same decision ends by saying the policy
"says which ones apply where, not what a check is". The derivations a policy names live in the tree
being gated. A change can therefore satisfy a mandated check by redefining what that check computes:
the policy from the target tip requires a check by name, the branch supplies a derivation that does
nothing, and the attestation truthfully records that it passed. The gate is sound about which checks
run and unsound about what they do.

This is a known class rather than a novel discovery. Hosted forges gate edits to
continuous-integration definitions behind a permission distinct from the permission to change code,
precisely because a proposed change that can edit its own checks has neutered its own gate.

## The tension any resolution has to serve

Reading the check definitions from the target tip as well would close the hole and break something
necessary. A change could then never introduce a check, repair a broken one, or demonstrate that a
change to a check works, because its own version would never run against it. The two requirements
are in genuine tension, and any resolution has to serve both.

## The instance-supplied case, one level down

A check supplied by the instance rather than by the project — arriving in a consuming project as a
flake input, which is how the knowledge lint is now consumed ([[ida-a9e274c]],
[ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md](../ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md))
— does not by itself close this. The lock file pinning that input is also in the tree being gated,
so a change can repoint the input at a fork whose check computes nothing. Closing the hole requires
the integrator to resolve an instance-mandated check from the instance's own pin rather than from
the pin the branch carries.

## Why it is acceptable today

The enforcement point now exists — Phase 3's integrator
([integration.md](../../design/integration.md)) — so the half of this the integrator can close is
closed and the half it cannot is live. The policy it gates a change under is composed from its own
checkout at the target tip and from the instance layer it is configured to find, never from the
submitted tree. But a pure check's input closure is recomputed over the tree that would land, and
the derivation naming that closure is in that tree. So the gate is sound about which checks are
required and still unsound about what they compute.

What holds in place of a fix: every contributor is the operator, and the projects are few and read
in full. The defect is a live exposure now rather than a structural one, and the gate below is where
it stops being tolerable.

## The gate

The mechanism needs a resolution before either of these becomes true:

- a project is served whose changes the group does not read in full, or
- the policy is relied on to enforce compliance across projects rather than to describe it.

cosmo depending on this for its build gate makes the second true.

## Directions, not decisions

- **A path class covering the check definitions** — the flake, the check derivations, the policy
  documents, the lock file — whose required set includes human approval. This is the forge
  permission expressed in machinery this design already has: [[ida-1ec03b1]]
  ([ideas/ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))
  makes policy path-scoped, and [[ida-b7025b5]]
  ([ideas/ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md))
  supplies an approval that an agent cannot manufacture. Branch definitions still run, so a change
  can still improve its own checks, but weakening one becomes a deliberate gated act.
- **The integrator resolving instance-mandated checks from the instance's pin**, so the floor's
  derivations are not branch-controlled.
- **Accepting it**, on the architecture's existing bet:
  [architecture.md](../../design/architecture.md) chooses attestation with revocation over CI as a
  gate, and Phase 6's witness re-derivation ([roadmap.md](../../design/roadmap.md)) is what would
  eventually detect a check that computes nothing.
