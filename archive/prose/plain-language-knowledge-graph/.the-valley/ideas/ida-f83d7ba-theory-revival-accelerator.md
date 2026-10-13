---
type: idea
id: ida-f83d7ba
status: exploring
title: "Help new contributors reconstruct the reasoning behind the design"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Help new contributors reconstruct the reasoning behind the design

The knowledge graph should help a new contributor understand why the system has its current shape
and which changes fit its purposes. Its usefulness depends on how well a reader can reconstruct that
understanding, not simply on how much text it stores.

Peter Naur's [Programming as Theory Building](https://gwern.net/doc/cs/algorithm/1985-naur.pdf)
provides the starting point. He describes a programmer's understanding as the ability to explain how
a program relates to its subject, why it is built as it is, and how to modify it appropriately. Code
and documentation can support that understanding without fully containing it.

Agent-assisted work repeatedly encounters this problem. A new run may have the files but lack the
reasoning developed in earlier work. It can then produce a locally plausible change that conflicts
with the design as a whole.

## What the graph can preserve

A node should state current understanding and explain its important reasons. Restating a changed
idea gives the next reader a coherent account. Git retains the earlier versions.

Humans and agents need access to the same project reasoning, as proposed in
[[ida-f172c8e]](ida-f172c8e-project-knowledge-not-agent-instructions.md). Where an assumption can
become a specification and check, the system can enforce part of that reasoning automatically.
[[ida-b48bded]](ida-b48bded-production-dags-and-events.md) explores that transition.

Human review remains necessary for important judgments that no check captures.

## The hypothesis to test

Naur emphasizes that artifacts do not replace people who understand a program. The project's
hypothesis is more limited: inexpensive, timely capture of decisions may preserve enough reasoning
for a capable new reader to reconstruct what is needed for the next change.

Some understanding may remain difficult to express or recover. The graph should be judged by
reconstruction effort and the quality of subsequent changes.

[[ida-99b87d9]](ida-99b87d9-theory-rebuild-as-evaluation.md) proposes exercises that can test both
the reader and the graph.
