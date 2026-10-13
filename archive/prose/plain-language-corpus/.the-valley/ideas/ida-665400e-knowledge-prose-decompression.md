---
type: idea
id: ida-665400e
status: exploring
title: "Rewrite the knowledge graph so its claims can be understood and reviewed"
created: 2026-09-01
source: knowledge base design review, 2026-09-01
---

# Rewrite the knowledge graph so its claims can be understood and reviewed

The graph needs plain prose that lets a reader understand each claim, compare it with related
claims, and decide what still needs work. Dense wording and repeated reframing have made
requirements and design harder to converge.

[AGENTS.md](../../AGENTS.md) already sets the standard: state the point directly, unpack unfamiliar
ideas, and use the level of detail appropriate to the document.

## Problems to address

A slogan can hide the actual rule a reader needs to apply. Several abstract terms in one sentence
can leave their relationship unstated. Repeating the same idea under different names can make it
look like several separate requirements.

Theoretical comparisons can be useful, but a reader needs to understand the proposal before being
asked to assess the comparison. Writing about earlier writing decisions can also crowd out the
current design.

These problems call for restatement and consolidation. They do not establish that an underlying idea
is wrong or unimportant.

## A useful structure

A node can usually explain:

1. What it proposes, decides, requires, or reports.
2. The problem and circumstances that make it relevant.
3. How it works, to the extent that the design has settled that question.
4. Its consequences, limitations, and open questions.

This is a writing aid to evaluate, not an adopted schema requirement. An exploratory idea may have
valuable reasoning without a concrete implementation. Requirements should remain in the problem
space. Detailed decisions may need formats and mechanics.

## Editorial approach

Use consistent terms for the same concept and link to the node that defines it. Remove duplicate
explanations when the link preserves the reader's ability to understand the argument.

Preserve the distinction between a decision, a proposal, an observed defect, and a predicted
failure. Uncertainty should be stated where the claim is introduced.

The foundational policy, identity, attestation, and integration decisions are a useful starting
point because many other nodes depend on them. The rest of the graph then needs the same treatment.
End-to-end examples may help explain how the pieces work together.

Pruning or reclassifying an idea requires a judgment about its substance. Vocabulary alone is not a
reason to discard it.

## Open questions

Would lightweight checks catch missing openings or undefined terms without imposing a rigid
template? Would vocabulary checks improve readability, or merely encourage new euphemisms?

The practical test remains whether an engineer can read a node once and accurately explain its
point. [[oc-ec73cd4]](../outcomes/oc-ec73cd4-corpus-reads-in-plain-english.md) tracks the rewrite
outcome, and [.the-valley/README.md](../README.md) defines the graph conventions.
