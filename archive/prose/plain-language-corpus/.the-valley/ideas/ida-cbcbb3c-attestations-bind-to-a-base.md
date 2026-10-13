---
type: idea
id: ida-cbcbb3c
status: raw
title: "Check whether evidence still applies after the base changes"
created: 2026-08-01
source: design conversation, 2026-08-01
---

# Check whether evidence still applies after the base changes

A check describes a particular resulting tree. Giving the proposed change a stable identity does not
make its evidence valid on every base.

The resulting tree depends on both the delta and the base to which it is applied. A clean rebase may
change inputs that a check used, even when the change itself appears unchanged.

[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md) defines
the transfer rules. A pure check can reuse its result when its input closure is unchanged. Effectful
evidence also depends on its path scope and validity window. The integrator must evaluate those
conditions before accepting earlier evidence.

Human approval has an additional question: whether the person approved the exact tree or a change
that may be reapplied to another tree. [[ida-7685e66]](ida-7685e66-birth-and-approval-are-policy.md)
examines that binding.

[[ida-93e4f91]](ida-93e4f91-changes-not-branches.md) describes stable change identity.
[[ida-51605e8]](ida-51605e8-authenticity-not-git-coupled.md) explains identifying the subject
independently of Git commit objects. [contribute.md](../../design/contribute.md) describes the
current integration path.
