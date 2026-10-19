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
- The docs speak from the project, never about its participants. State a position as the project's
  own, and refer to people only by role (the integrator, the instance's human operator). Prior art
  and external authors may be cited freely.

The test for every paragraph: could an engineer new to this repo read it once and correctly say it
back?

## Document altitudes

The docs are layered. Each document stays at its altitude:

1. **Premise** — [README.md](./README.md). What this is and why it matters. No mechanisms.
2. **Purpose and principles** — [docs/purpose.md](./docs/purpose.md). The operator's goals, the
   principles that govern the system, ownership boundaries, and what makes a change fitting or
   unfitting.
3. **Architecture and security** — [docs/architecture.md](./docs/architecture.md) and
   [docs/security.md](./docs/security.md). What runs in production, how each gate along the path of
   a change protects the hosts, and which executable check verifies each guarantee.
4. **Roadmap** — [docs/roadmap.md](./docs/roadmap.md). Sequenced milestones, what each step unlocks,
   deliberately deferred ideas, and open decisions.
5. **Archive** — [archive/design/](./archive/design/) and
   [archive/.the-valley/](./archive/.the-valley/). Read-only historical exploration and
   knowledge-graph nodes kept so stable node IDs (`dcr-*`, `ida-*`, `bd-*`, `oc-*`) cited in code,
   schemas, and commit history continue to resolve.

When you find detail at the wrong altitude, move it down rather than piling on. Prune elaboration
freely — mechanics, taxonomies, and formats can be re-derived, and deleted text lives in git
history. Never silently compress insight: a conceptual move or reframe does not re-derive on demand.

## Knowledge-graph nodes

Historical knowledge-graph nodes live under [archive/.the-valley/](./archive/.the-valley/), with
node mechanics — types, IDs, frontmatter, and linking — defined in
[archive/.the-valley/README.md](./archive/.the-valley/README.md). Each node opens with one plain
paragraph stating the idea, decision, bug, or outcome, followed by context, implications, and open
questions.

`archive/.the-valley/` is frozen, read-only history, and nothing checks it against today's schema.
Design decisions are recorded directly in [docs/architecture.md](./docs/architecture.md),
[docs/security.md](./docs/security.md), and [docs/roadmap.md](./docs/roadmap.md). A question that is
still open lives in the project's plan graph as a `question` knot, owned by whoever has to answer
it. When its answer settles something about the design, the answer enters these docs through a
change, and the knot links that change.

## Documentation style and verification

1. **Simple language, one new idea per sentence.** Lead every section with a plain statement of what
   the thing is in ordinary words before qualifying it. Split sentences that stack abstractions.
2. **Meaning before mechanism.** Say what a concept or command accomplishes for the reader before
   explaining git refs, CUE unification, or Nix derivations.
3. **Example before abstraction.** In guides and reference material, show a concrete command or a
   short snippet before edge-case rules or schema tables.
4. **Separate three kinds of claim.** Label code guarantees `implemented`, instance configuration
   `deployment choice`, and mechanisms that do not work yet `not built`. Never present a deployment
   choice or an unbuilt plan as a code guarantee. Configuration includes `valley.cue`,
   `services.valley`, `policy/`, and `identity/`.
5. **Carry a claim-to-code checklist.** Every documentation slice brief and change description must
   list each factual claim added or modified, its kind, and an exact `file:line` citation verified
   against HEAD before review.
6. **Present tense, no transition narration.** Describe what HEAD does today. Live plans and open
   questions belong in `valley plan` (`refs/the-valley/knots`), not in prose docs. Do not compare
   the-valley to other platforms; positioning is the operator's to write.
7. **Use the project's vocabulary.** A change's recorded state is `draft`, `ready`, or `withdrawn`.
   A version is `approved` when an integration request names it, and `submitted` once it has landed.
   Never call a change a PR, pull request, or branch; a git branch may carry a change. A change
   **requires** checks; it never owes them. A plan item is a **knot** in the **plan graph**, never
   an issue, ticket, or knowledgebase entry. Operational refs (`refs/the-valley/*`) are **project
   storage**, distinct from **the source** (`main` and release tags).
8. **Keep the Taoist wink at thresholds.** One light sentence can introduce the valley as the low
   place where streams of work converge, or knots as records named for knotted cords before writing.
   Keep metaphor out of procedures, error messages, and security invariants.
