---
type: idea
id: ida-f172c8e
status: exploring
title: Project knowledge is project knowledge — no agent-specific instruction files
created: 2026-07-17
source: design conversation, 2026-07-17
---

# Project knowledge is project knowledge — no agent-specific instruction files

Knowledge about the project is knowledge about the project. There is no agent knowledge and human
knowledge — only project knowledge, addressed to any contributor. A valley project's knowledge graph
should be automatically hooked up into agent harnesses and be as easily discoverable and navigable
by humans.

The convention this rejects is the one this repo currently follows: an AGENTS.md (writing standard,
document altitudes, node conventions) with CLAUDE.md as a one-line bridge. That shape splits
knowledge by audience — AGENTS.md addresses agents, the design docs address humans — and the harness
hookup is manual per-vendor glue. But everything in AGENTS.md is knowledge about the project: how to
write here, how the docs are layered, how nodes work. It belongs with the rest of the project's
knowledge.

The direction: the valley engine, not per-repo glue files, hooks a project's knowledge graph into
agent harnesses automatically. The graph is the briefing — same knowledge, same navigation, for both
kinds of contributor.

The same reasoning covers an agent's private session memory: it is not a second home for project
knowledge. Project truth goes into the graph, and agent memory shrinks to a rebuildable projection —
pointers into graphs, plus whatever genuinely does not belong in any repo. Agent memory becomes the
same kind of thing as every other valley projection: derived and disposable, with the graph as the
durable layer.

Open: where the AGENTS.md content re-homes (design/contribute.md is the natural candidate), and what
the automatic hookup mechanically is. If harness files must exist at all, they are derived artifacts
generated from project knowledge, never sources ([[ida-4557af7]],
[ida-4557af7-spec-driven-iteration.md](./ida-4557af7-spec-driven-iteration.md), makes the same move
for implementations).

Related: [[ida-3e87f5c]]
([ida-3e87f5c-self-describing-projects.md](./ida-3e87f5c-self-describing-projects.md)) — the same
move for host declarations: what a project is travels in its store.
