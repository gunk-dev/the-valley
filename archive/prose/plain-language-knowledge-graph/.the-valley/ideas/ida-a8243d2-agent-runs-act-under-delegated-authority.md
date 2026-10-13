---
type: idea
id: ida-a8243d2
status: exploring
title: "Constrain agent work through recorded delegation"
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Constrain agent work through recorded delegation

An agent run acts under permissions delegated by the human who authorized the work. It may receive
those permissions directly or through another run that dispatched it. The run holds no independent
signing key.

Each delegation may narrow the permissions it receives. It must not add permissions. A verifier
needs to check that rule at every link in the chain.

## Human decisions

Required physical approval cannot be delegated to an automated run. Otherwise an agent could satisfy
the human gate on its own work.

[[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md) permits
human-authorized delegation and policy exemptions for ordinary changes while requiring physical
approval for changes to the delegation rules themselves.
[[ida-b7025b5]](ida-b7025b5-human-decisions-are-signed-acts.md) describes that signed human act.

## Capability model

Capability-based systems provide relevant prior art. They attach rights to references and make
permission transfer an explicit operation. Fuchsia's handles and capability routing are examples of
representing what a process may access and which rights it may pass on.

For the-valley, the useful properties are explicit initial authority, delegation that only narrows
it, and a checkable path from a requested action back to its authorization.

A per-run key would add key management without establishing much lasting reputation. A long-lived
agent key would introduce a durable credential that could be stolen. The current approach keeps run
attribution in provenance and signing authority with registered principals.

## Enforcement and evidence

A delegation record must state scope, not just name the initiating human. Naming the human answers
who authorized work, but leaves open which actions were permitted.

The record also needs trustworthy verification. If the harness both executes the run and writes the
chain, its signature alone does not independently establish the chain's accuracy.

Finally, some component must be able to refuse an action outside the delegated permissions. The
integrator fills that role for landing changes.
[[ida-f1b39e8]](ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) explores mediation of
other external actions.

## Open questions

What does dispatch authorization identify? How narrow can its permissions be? How are delegation
links represented and verified? Which operations can bypass the proposed enforcement points?

[[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) defines the host-signed
attestation that attributes a run. [[ida-51605e8]](ida-51605e8-authenticity-not-git-coupled.md) and
[[ida-d2dc957]](ida-d2dc957-attestations-ride-on-a-transparent-leaf.md) discuss the signed content
and possible carriers.
