---
type: outcome
id: oc-ec73cd4
status: open
title: The corpus reads in plain engineering English
created: 2026-09-01
source: knowledge base design review, 2026-09-01
blocked_by: []
---

# The corpus reads in plain engineering English

Every design document, reference, and knowledge-graph node in the repository reads in plain, direct
software engineering English. Done means an engineer new to the repository can read each document
once and accurately describe what the system does, how it works mechanically, and why it was
designed that way, without translating cryptic epigrams, theological vocabulary, or compressed
abstractions.

The diagnosis, recurring failure modes, and four-part node template are established in
[[ida-665400e]]
([ida-665400e-knowledge-prose-decompression.md](../ideas/ida-665400e-knowledge-prose-decompression.md)).
This outcome tracks the corpus-wide execution of that plan.

## The acceptance standard

Per [AGENTS.md](../../AGENTS.md), prose must satisfy the plain engineering test:

1. **Lead with the plain statement.** Every document, section, and node opens by stating what the
   thing is in ordinary software engineering terms before qualifying, justifying, or elaborating it.
2. **Unpack rather than compress.** Two plain sentences beat one dense one. Concepts are unpacked
   into concrete data structures, CLI commands, file paths, and execution flows rather than
   theoretical analogies.
3. **Strip elevated and theological vocabulary.** Concepts named with terms like "genesis entry,"
   "sovereign home," "shared law," "induction unrolled in time," or "neural nodes" are replaced with
   concrete terms: bootstrap keys, repository namespaces, shared policy, inductive commit
   verification, and agent/human actors.
4. **Preserve full design insight.** Decompression removes stylistic ornamentation and false
   profundity; it must never discard architectural insight, security boundaries, or subtle failure
   modes.

## Inventory of work

The work spans the design documents, core decisions, and premise:

### 1. Core architecture and design documents (`design/`)

- [x] **Inventory and standards definition.** Diagnosed in [[ida-665400e]], failure modes captured
      in [[bd-c324184]], [[bd-e63bac8]], [[bd-c4d069b]], and [[bd-5cb2034]].
- [ ] **`design/architecture.md`.** Decompress the unbundled table and core bets. Remove theological
      metaphors around bootstrap loops; explain the concrete relationship between git, NATS, and the
      integrator.
- [ ] **`design/integration.md`.** Decompress the request ref lifecycle, verdict rules, and OCC
      commit point into concrete Go struct and data flow walkthroughs.
- [ ] **`design/verification.md`.** Replace gnomic phrasing around line ordering and escape refusals
      with clear specifications of the canonical text format and signing envelope.
- [ ] **`design/requirements.md`.** Ensure needs and constraints read as clear software requirements
      rather than philosophical treatises.
- [ ] **`design/contribute.md`.** Provide direct step-by-step instructions for contributors and
      agents.

### 2. Foundational decision nodes (`.the-valley/decisions/`)

- [ ] **`dcr-439b771` (Integration OCC over content-addressed evidence).** Pilot rewrite: decompress
      database transaction analogies into concrete test-caching across clean git rebases using Nix
      input closures.
- [ ] **`dcr-f41f718` (Declared verification policy).** Remove the "floor vs answering back" prose;
      present the concrete CUE unification rules and directory layout.
- [ ] **`dcr-0de694f` & `dcr-de9d996` (Attestation shape and line format).** Explain the SSH Ed25519
      note format and tree digest cleanly.
- [ ] **`dcr-b87f6e8` (Identity is a governed registry).** Explain the CUE identity registry and
      compiled `authorized_keys` artifact.
- [ ] **`dcr-8f069dd` (The valley is the unit).** Strip the grammatical essay on English articles;
      state the operational hierarchy of Host $\rightarrow$ Valley $\rightarrow$ Project.

### 3. Premise and conventions

- [ ] **`README.md`.** Ensure the premise reads cleanly to an outside engineer without elevated
      jargon.
- [ ] **`.the-valley/README.md`.** Update the knowledge-graph convention instructions to mandate the
      four-part node template.

## Related

- Prose decompression idea: [[ida-665400e]]
  ([ida-665400e-knowledge-prose-decompression.md](../ideas/ida-665400e-knowledge-prose-decompression.md))
- Writing standard: [AGENTS.md](../../AGENTS.md)
- Prior corpus audit: [[oc-9b94fe4]]
  ([oc-9b94fe4-corpus-states-each-claim-once.md](./oc-9b94fe4-corpus-states-each-claim-once.md))
