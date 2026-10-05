---
type: idea
id: ida-665400e
status: exploring
title: Plain engineering register and prose decompression for the knowledge graph
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Plain engineering register and prose decompression for the knowledge graph

The knowledge graph and companion design documents have accumulated a dense, aphoristic, and overly
abstract writing style. This style obfuscates the underlying mechanics of the system and directly
violates the writing standards defined in [AGENTS.md](../../AGENTS.md). The remedy is a systematic
decompression of the knowledge base into a plain engineering register: leading with plain
statements, unpacking mechanisms before theoretical justifications, and eliminating theological and
self-referential vocabulary.

## The failure modes in current prose

A review of the knowledge base under `.the-valley/` and `design/` identifies four primary failure
modes that recur across nodes:

1. **Cryptic epigrams and aphorisms.** Technical decisions are frequently stated as gnomic riddles
   rather than operational descriptions. Phrases like "both layers say what; they differ only in
   whether a project can answer back" or "an entry boundary a path could forge is a digest an
   attacker could steer" require the reader to reverse-engineer basic data structures and error
   conditions from poetic meter.
2. **Theological and elevated vocabulary.** Standard systems engineering primitives are obscured
   behind archaic or dramatic terminology. A bootstrap SSH key or configuration is termed a "genesis
   entry" or an "induction unrolled in time"; an organization's repository is a "sovereign home"
   under "shared law"; task performers are "actors in an OODA loop over neural nodes"; and automated
   task execution is framed as "standing demand pressure." This language imparts an aura of theology
   that hides whether a concept is an implemented Go struct or an abstract metaphor.
3. **Premature abstraction stacking.** Documents regularly explain database concurrency control
   theory, read sets, write sets, and serialization invariants before stating the concrete file and
   command interactions. For example, the core property of test transfer across clean rebases is
   obscured by paragraphs of theoretical framing before explaining that unchanged input closures
   simply bypass test re-execution.
4. **Meta-documentation bloat.** Significant corpus volume is spent debating documentation rules,
   grammatical articles (such as whether "valley" requires a definite article), and cataloging minor
   sentence discrepancies across historical document rewrites. These records focus on corpus
   archaeology rather than running software.

## The concrete node template

To restore readability and adhere to [AGENTS.md](../../AGENTS.md), each node in the graph must
follow a four-part structure that bans ungrounded abstraction:

1. **Plain Statement (The What):** One to two sentences stating what the component, format, or
   decision is in ordinary software engineering terms.
2. **Concrete Mechanics (The How):** Specific file formats, data structures, CLI flags, file paths,
   and execution steps.
3. **Problem and Rationale (The Why):** The concrete failure mode, latency bottleneck, or
   operational problem this mechanism resolves.
4. **Tradeoffs and Unresolved Edges:** The practical failure modes, performance limits, and open
   questions of the approach.

## Plan for decompression

Decompressing the knowledge base proceeds in four stages:

- **Stage 1: Core decision nodes.** Rewrite the foundational decision nodes that establish system
  mechanics: [[dcr-439b771]] (integration OCC and test transfer), [[dcr-f41f718]] (verification
  policy composition), [[dcr-0de694f]] and [[dcr-de9d996]] (attestation statement and note formats),
  [[dcr-b87f6e8]] (identity registry), and [[dcr-8f069dd]] (the valley as the administrative unit).
- **Stage 2: Idea pruning and grounding.** Demote or archive purely meta-philosophical nodes.
  Re-ground operational ideas (such as scheduling controllers and capability boundaries) into
  concrete engineering specifications.
- **Stage 3: End-to-end walkthroughs.** Add concrete scenarios showing the exact command and ref
  lifecycle for both human and agent contributors.
- **Stage 4: Linter enforcement.** Extend knowledge verification to check that nodes conform to the
  four-part structure and reject forbidden vocabulary.

## Related

- The repository writing standard: [AGENTS.md](../../AGENTS.md)
- Knowledge conventions: [.the-valley/README.md](../README.md)
- Integration decision: [[dcr-439b771]]
  ([dcr-439b771-integration-occ-over-content-addressed-evidence.md](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md))
- Verification policy: [[dcr-f41f718]]
  ([dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md))
