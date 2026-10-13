---
type: idea
id: ida-99b87d9
status: exploring
title: "Evaluate whether a model can reconstruct the project's reasoning from the graph"
created: 2026-08-22
source: design conversation, 2026-08-22
---

# Evaluate whether a model can reconstruct the project's reasoning from the graph

Ask a model to explain the project's design using only the knowledge graph, then assess whether it
understood the reasons behind the design. This can evaluate both the model and the graph.

With the graph held fixed, compare which models can handle different kinds of question or proposed
change. With the model held fixed, compare how revisions to the graph affect the effort and accuracy
of reconstruction.

Useful tasks include explaining why a decision was made, proposing a modification consistent with
the design, and detecting a plausible change that violates an important assumption.

Someone who understands the project must judge whether the reconstruction is sound. A model that has
repeatedly demonstrated that understanding could help evaluate other answers, but its judgments need
calibration against the human assessment.

[[ida-f83d7ba]](ida-f83d7ba-theory-revival-accelerator.md) explains why preserving reconstructible
design reasoning is a purpose of the graph. This proposal would make that purpose testable through
small, repeated exercises.
