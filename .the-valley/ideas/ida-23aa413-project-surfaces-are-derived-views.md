---
type: idea
id: ida-23aa413
status: exploring
title: A project's browsable surface is a derived view
created: 2026-08-02
source: design conversation, 2026-08-02
---

# A project's browsable surface is a derived view

A project needs a browsable surface: somewhere to monitor the project, read its sources, read its
diffs, see the status of the changes in flight, and review and work on the knowledge graph. That
surface must be reachable privately, and it must be possible to share a view of it publicly.

The knowledge graph is the least served of these. It is the ideas, decisions, bugs and outcomes kept
as plain files beside the code ([.the-valley/README.md](../README.md)), and it is the project's
durable memory across a reader's context resets — what survives when the conversation that produced
it does not. Today it is read and written only through a filesystem and a text editor. Reading what
a change to a node actually says, following the edges from one node to the ones it links, and
noticing where two nodes cover the same ground are all work that no tooling does.

A surface is part of leaving the hosted forge behind rather than a convenience laid on top of it.
The forge remains the public face of every project and the only way to browse one, so the exit is
not complete while browsing depends on it.

The design already answers this in the abstract.
[architecture.md](../../design/architecture.md#the-concerns-unbundled) unbundles observability and
feedback into continuous feedback as events, with threads as derived views over those events, and
bets that
[review is observability plus feedback](../../design/architecture.md#bet-review-is-observability--feedback).
A browsable surface is the concrete form of that bet. Browsing appears in the design today only in
passing, as an optional extra on the git host
([architecture.md § Components](../../design/architecture.md#components),
[roadmap.md, Phase 0](../../design/roadmap.md#phase-0--mvp-repos-off-github)).

## What the surface inherits

**It is a view, never the interface.** Among the [README](../../README.md)'s objections to the
hosted forge is that agents are second-class there, because the platform is built around a human
clicking through a web interface. A surface that becomes the way things are done reintroduces
exactly that. Everything the surface offers must be equally reachable without it.

**It holds no state of its own.** Everything it displays already lives in git, in the event log, in
the knowledge graph, and in attestations. The design holds that per-repo events are a projection of
git and never a second source of truth; a surface that owned state would be a second source of truth
wearing a different hat.

Working on the graph means writing, and writing is compatible with this. A surface that authors a
change is an authoring client: the change it produces is a commit like any other, and it travels the
same path as a change authored at a text editor. The constraint is about graph state the repository
does not hold, and a surface that holds none of that may still help produce what the repository
does.

**It is not a forge.** The _Minimal_ constraint in
[requirements.md](../../design/requirements.md#constraints) holds that small composed tools stay
understandable and replaceable while platforms accrete. Adopting an existing full forge would
reintroduce the platform being unbundled, whatever its licence.

**Public sharing is a new exposure.** Identity today is tailnet-scoped: Tailscale ACLs and SSH keys,
with the host reachable only from the operator's private network. A publicly shareable view is
reachable from outside that boundary, which makes it a genuinely new surface rather than a
configuration change. It also arrives while machine credentials on the host are known not to expire
([[bd-8a591dc]]
([bd-8a591dc-machine-credentials-never-expire.md](../bugs/bd-8a591dc-machine-credentials-never-expire.md))).

**Implementation follows the group's language policy.** The browser-facing ecosystem is the case
that policy most directly concerns.

## A candidate home for the approval gate

This is a direction worth recording, not a decision.

A browser is the one environment where hardware-key presence is a first-class primitive.
[[ida-b7025b5]]
([ida-b7025b5-human-decisions-are-signed-acts.md](./ida-b7025b5-human-decisions-are-signed-acts.md))
requires a human approval to be an act requiring physical presence — a hardware key that must be
touched. [[ida-d2dc957]]
([ida-d2dc957-attestations-ride-on-a-transparent-leaf.md](./ida-d2dc957-attestations-ride-on-a-transparent-leaf.md))
records as open that no hardware token today can produce the raw signature its leaf format needs,
which forces a human approval to travel as data submitted under a machine identity. A browsable
surface is therefore a candidate home for the approval gate, where the key touched is the one the
human is holding rather than one attached to a server. This is where the two use cases meet:
reviewing a change to the graph is exactly the activity such a gate sits on.

This relocates the problem rather than dissolving it. A browser-originated hardware assertion is not
the same shape as an SSH signature, so the format question [[ida-d2dc957]] raises stays open in a
different form.

## Open

**Where this lands in the plan.**

**What a publicly shared view exposes, and what authorises the sharing.**

**Whether the surface authors changes at all, or only displays them.**

**Whether the approval gate belongs here.**
