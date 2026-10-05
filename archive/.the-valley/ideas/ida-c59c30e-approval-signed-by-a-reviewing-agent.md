---
type: idea
id: ida-c59c30e
status: exploring
title: Approval may be signed by a reviewing agent
created: 2026-08-01
source: design conversation, 2026-08-01
---

# Approval may be signed by a reviewing agent

Policy may require certain changes to carry an approval signature over the change. That signature
may come from a human using a hardware key, or from an agent run whose responsibility is approval —
either general review, or a review capability carrying specific security expertise, characterised by
specific tools, prompting and skills.

This is agent-run identity ([[ida-45178f6]],
[ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) in a
reviewing role rather than an authoring one. What the approval is worth is a function of the
reviewing run's provenance — which model, which tools, which prompting.

One property of the shape: the approval outcome is signed over the change, but the journey to it
includes blocking and non-blocking feedback delivered into the contributing agent's trajectory,
until the change passes. The signature is the endpoint of a loop, not a single verdict passed over a
finished artifact.

A reviewing agent must not be the authoring agent. That is the failure [[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
describes: an approval gate administered by the agent it gates.

## Open

An agent that can sign an approval is in direct tension with [[ida-a8243d2]]
([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md)),
which holds that the right to approve is non-delegable and terminates at the human: "If it can be
placed in any bundle at all, however attenuated, delegation quietly reintroduces the very failure
that physical presence exists to prevent: a coordinating agent holding a delegated right to approve
is once again a gate administered by the thing it gates."

Two directions are candidates, and this node picks neither:

- The non-delegable right could be narrower than approval in general, applying only to the change
  classes policy marks as requiring human presence.
- A reviewing agent could produce something distinct from a human approval — a signed review
  attestation rather than the approval itself — in which case the gate that must not be delegated is
  only the latter.

## Related

- [[ida-45178f6]]
  ([ida-45178f6-agent-identity-is-provenance.md](./ida-45178f6-agent-identity-is-provenance.md)) —
  the identity class a reviewing run signs under.
- [[ida-a8243d2]]
  ([ida-a8243d2-agent-runs-act-under-delegated-authority.md](./ida-a8243d2-agent-runs-act-under-delegated-authority.md))
  — the non-delegable right this is in tension with.
- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — the motivating failure, and the human form of the signature.
