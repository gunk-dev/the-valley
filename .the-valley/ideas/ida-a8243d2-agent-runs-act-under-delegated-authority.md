---
type: idea
id: ida-a8243d2
status: exploring
title: Agent runs act under delegated authority
created: 2026-07-25
source: design conversation, 2026-07-25
---

# Agent runs act under delegated authority

Contributors arrive in identity classes that differ in whether their signing can be automated. A
human decision requires physical presence, and must not be automatable — that is the whole point of
it. An agent run is the opposite. It is automation, and no human is present at the moment it acts.

The direction taken is that an agent run holds no key of its own. Its authority derives from the
human authorization that dispatched it: a signed act naming what the run was permitted to do. The
run's output cites that authorization rather than carrying an independent identity. No per-run
keypair is minted, and no long-lived per-agent key accrues trust.

This shape is chosen over the alternatives so that identity stays thin until a scenario forces it to
grow. Per-run keys give the finest provenance, but they accrue no reputation and carry real
key-management weight. A long-lived per-agent key does accrue trust, but it becomes a durable
forgery risk if it leaks. At present scale neither buys anything the delegated chain does not
already provide.

## Open

- What a dispatch authorization names, and how narrowly it scopes a run.
- How the chain is verified when the harness that ran the agent is also the software recording the
  citation.
- Whether an agent's attestation is signed at all, or merely attributed.

The last question is load-bearing rather than decorative. The contributor protocol admits both
humans and agents as contributors, so the attestation helper cannot be built until the agent case
has an answer.

## Related

- [[ida-b7025b5]]
  ([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
  — the human act that authority derives from.
- [[ida-51605e8]]
  ([ida-51605e8-authenticity-not-git-coupled.md](./ida-51605e8-authenticity-not-git-coupled.md)) —
  what such a signature is taken over.
- [contribute.md](../../design/contribute.md) — defines a contributor as human or agent.
- [roadmap.md, Phase 2](../../design/roadmap.md#phase-2--attestations-verification-mvp) — the phase
  this question gates.
