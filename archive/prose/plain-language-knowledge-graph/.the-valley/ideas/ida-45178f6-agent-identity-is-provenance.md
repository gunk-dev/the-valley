---
type: idea
id: ida-45178f6
status: exploring
title: "Identify agent runs through their recorded inputs and execution history"
created: 2026-07-31
source: design conversation, 2026-07-31
supersedes: [ida-a763b3f]
---

# Identify agent runs through their recorded inputs and execution history

An agent run can be identified by a record of what produced it: context, prompt, tools and
environment, model, and the chain of actions leading back to the initiating human. This provenance
describes the run without giving it an independent signing identity.

[[ida-a8243d2]](ida-a8243d2-agent-runs-act-under-delegated-authority.md) examines the same chain as
a record of delegated authority. Provenance answers what produced the run; delegation answers what
it was allowed to do.

## Verification and evaluation

Recorded inputs can support policy checks on a run. A policy might require an approved model,
exclude a particular model, or check the prompt for specified content. Such checks need a
trustworthy record and access to the relevant data.

The record also defines an experiment. A later run could vary the model, prompt, or tool environment
and compare its trajectory and result with the original. This would support evaluation on actual
project work.

Recording enough information for that comparison is difficult. A hosted model is a changing service,
and repeating a request does not guarantee identical output. Recording exact open-model weights
would identify one more input by digest, but reproducibility would still depend on the execution
environment and other sources of nondeterminism. [verification.md](../../design/verification.md)
supplies the pure-versus-effectful distinction.

## Private and large inputs

Prompts and contexts may be too large or private to publish. The digest commitments discussed in
[[ida-d2dc957]](ida-d2dc957-attestations-ride-on-a-transparent-leaf.md) could authenticate a
supplied value while allowing the value itself to remain outside the public log.

This record is a candidate use of that proposal's project-specific context fields.

## Who attests to the record

Provenance needs a signature from something able to observe and record the run. Otherwise it is only
an unverified description.

For Phase 2, [[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md) assigns the
signature to the execution host. The run is attributed inside the host-signed statement. The
outbound actuator in [[ida-f1b39e8]](ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) is a
possible signer in the broader execution design.

Open questions concern that broader signer role, the context fields to standardize, and how to
verify a record produced by the same harness that ran the agent. A signature identifies the
recorder; it does not independently establish that every recorded claim is true.

[[ida-1ec03b1]](ida-1ec03b1-path-scoped-verification-policy.md) covers the policy that would require
these checks.
