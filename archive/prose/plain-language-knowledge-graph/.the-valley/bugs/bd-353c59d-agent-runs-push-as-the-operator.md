---
type: bug
id: bd-353c59d
status: open
title: "Agent pushes using a human key inherit that human's permissions"
created: 2026-08-07
source: rejection test on the pilot host, 2026-08-07
---

# Agent pushes using a human key inherit that human's permissions

An agent run that uses the operator's SSH key authenticates as the operator. The receive hook sees
the principal attached to that key and cannot distinguish the agent's push from another use of the
same credential.

This matters wherever a permission is intended to distinguish human action from automated work. The
fact that a human started the run does not mean the human reviewed every action the run later takes.

## Protected refs and remaining scope

The current classic-laddie declaration permits only the integrator to write the protected `main`
refs for the-valley and Qinling. Using the operator's key therefore no longer supplies the direct
protected-ref writer grant in that configuration.

The remaining issue is inherited credential authority. A run with access to a human's key can
exercise whatever other permissions that key has. The intended delegated scope of the run is not
established by SSH authentication alone.

A rejection test must control the authenticated identity and its configured grants. Otherwise it may
test whether the operator is allowed to act while appearing to test whether an agent is constrained.

## Required behavior

Before relying on unattended or delegated agent work, the system needs to enforce the run's
permitted actions independently of the human credential it can reach.

The current identity direction gives runs no key of their own.
[[ida-a8243d2]](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md) instead describes
recorded delegation that can narrow at each hop, while
[[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) defines registered
machine and service principals.

Possible implementations include a machine principal with restricted grants, an actuator enforcing
the run's delegation, or a contribution path that needs no push credential.
[[ida-f1b39e8]](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) and
[[ida-0f580e2]](../ideas/ida-0f580e2-contributors-without-write-access.md) explore the latter
approaches.

[[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md) describes the separate
physical-presence requirement for human approval. This bug remains open pending a complete execution
and credential boundary.
