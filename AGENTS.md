# Working in this repository

Most of the-valley is prose: design documents and knowledge-graph nodes. How you write is most of
the work.

## Writing standard

Write clearly, simply, and without compression that would tax a knowledgeable software engineer.

- Lead with the plain statement. Say what the thing is in ordinary words before qualifying,
  justifying, or elaborating it.
- One new idea per sentence. Density is fine when restating what the reader already knows; a new
  concept gets its own sentence.
- Unpack rather than compress. If a sentence stacks abstractions or asks the reader to infer an
  unstated step, spell it out. Two plain sentences beat one dense one.
- Use existing vocabulary. Coin a term only when the document defines it at first use and needs it
  repeatedly. Prefer concrete words to abstract ones.
- Anything a reader must act on — requirements, acceptance criteria, plans, decision records — gets
  the plainest register of all.
- When capturing someone else's idea, keep their words wherever they are clear enough to stand.

The test for every paragraph: could an engineer new to this repo read it once and correctly say it
back?

## Document altitudes

The docs are layered. Each document stays at its altitude:

1. **Premise** — [README.md](./README.md). What this is and why it matters. No mechanisms.
2. **Problem space** — [design/user-scenarios.md](./design/user-scenarios.md) and
   [design/requirements.md](./design/requirements.md). What must hold, derived from the scenarios.
   No solution detail.
3. **Architecture** — [design/architecture.md](./design/architecture.md). The bets and their
   rationale, traced to requirements.
4. **Detailed design** — the remaining files under [design/](./design/). Internals, formats,
   mechanics.

When you find detail at the wrong altitude, move it down rather than piling on. Prune elaboration
freely — mechanics, taxonomies, and formats can be re-derived, and deleted text lives in git
history. Never silently compress insight: a conceptual move or reframe does not re-derive on demand.
If an insight does not fit the document it arose in, capture it at full fidelity as a
knowledge-graph node and leave a one-line pointer.

## Knowledge-graph nodes

Node mechanics — types, ids, frontmatter, linking — are defined in
[.the-valley/README.md](./.the-valley/README.md). For the body: open with one plain paragraph saying
what the idea, decision, or outcome is. Context, implications, and open questions follow it.
