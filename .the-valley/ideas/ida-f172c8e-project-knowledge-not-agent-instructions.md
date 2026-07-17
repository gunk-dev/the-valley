---
type: idea
id: ida-f172c8e
status: exploring
title: Project knowledge is project knowledge — no agent-specific instruction files
created: 2026-07-17
source: owner design conversation
---

# Project knowledge is project knowledge — no agent-specific instruction files

The owner's position, near-verbatim (2026-07-17): I don't really like "agent"-specific instructions. Knowledge about the project is knowledge about the project. Valley projects' knowledge graphs should be automatically hooked up into agent harnesses and as easily discoverable and navigable by humans.

Context: the-valley currently carries an AGENTS.md (writing standard, document altitudes, node conventions) with CLAUDE.md as a one-line bridge — the standard agent-harness convention, and the owner is not happy with where it landed. The convention splits project knowledge by audience: AGENTS.md addresses agents, the design docs address humans, and the harness hookup is manual per-vendor glue.

The direction this points:

- Everything in AGENTS.md is knowledge about the project — how to write here, how the docs are layered, how nodes work. It belongs with the rest of the project's knowledge, addressed to any contributor, human or agent.
- The valley engine, not per-repo glue files, hooks a project's knowledge graph into agent harnesses automatically. The graph is the briefing. Same knowledge, same navigation, for both kinds of contributor.

Open: where the AGENTS.md content re-homes (design/contribute.md is the natural candidate), and what the automatic hookup mechanically is — if harness files must exist at all, they are derived artifacts generated from project knowledge, never sources ([[ida-4557af7]], [ida-4557af7-spec-driven-iteration.md](./ida-4557af7-spec-driven-iteration.md), makes the same move for implementations).

Ratified consequence (owner, 2026-07-17): this applies to coordinating agents' private memory too. An agent's session-persistent memory is not a second home for project knowledge — project truth goes into the graph, and memory shrinks to a rebuildable projection: pointers into graphs, plus what genuinely does not belong in a repo ("you can always use it for stuff that doesn't belong in the repo"). That makes agent memory the same kind of thing as every other valley projection — derived and disposable, with the graph as the durable layer.

Related: [[ida-3e87f5c]] ([ida-3e87f5c-self-describing-projects.md](./ida-3e87f5c-self-describing-projects.md)) — the same move for host declarations: what a project is travels in its store.
