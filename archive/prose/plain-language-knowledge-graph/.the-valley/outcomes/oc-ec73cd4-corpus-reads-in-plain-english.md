---
type: outcome
id: oc-ec73cd4
status: open
title: "Make the corpus readable without losing its design reasoning"
created: 2026-09-01
source: knowledge base design review, 2026-09-01
blocked_by: []
---

# Make the corpus readable without losing its design reasoning

The knowledge graph and design documents should let a new engineer understand the project's
requirements, decisions, and open questions without translating slogans or dense abstractions.

The rewrite must preserve the reasoning needed to assess the design. Clearer wording should also
make genuine flaws easier to discuss.

[AGENTS.md](../../AGENTS.md) supplies the writing standard.
[[ida-665400e]](../ideas/ida-665400e-knowledge-prose-decompression.md) describes the editorial
approach; its suggested structure is a writing aid, not a mandatory four-part schema.

## Acceptance criteria

- [ ] Nodes open with a direct statement of their subject.
- [ ] Terms have consistent meanings and are defined before they are needed.
- [ ] Explanations make the relationships between concepts explicit.
- [ ] Requirements stay focused on needs; implementation detail appears where it helps assess a
      design.
- [ ] Decisions, proposals, observations, and predicted risks are distinguishable.
- [ ] Important reasoning, failure conditions, and open questions survive the rewrite.
- [ ] Repeated definitions are replaced with clear references where practical.
- [ ] A human review confirms that the rewritten text is understandable and faithful.

Passing graph and formatting checks verifies structure, not these editorial judgments.

## Scope and review

The review change covers the full graph, its reading guide, the project README, and all nine
documents under `design/`. Approval of the text is separate from choosing the next design work.

The foundational documents included in the same rewrite are:

- [README.md](../../README.md): purpose and motivation.
- [architecture.md](../../design/architecture.md): major design choices and their reasons.
- [integration.md](../../design/integration.md): requests, evidence evaluation, and landing.
- [verification.md](../../design/verification.md): statements, formats, and verification rules.
- [requirements.md](../../design/requirements.md): needs and constraints.
- [contribute.md](../../design/contribute.md): the contributor workflow.
- [user-scenarios.md](../../design/user-scenarios.md): user needs and acceptance criteria.
- [self-transparency.md](../../design/self-transparency.md): the proposed requirement for recording
  changes to the system itself.
- [openquestions.md](../../design/openquestions.md): unresolved decisions and implementation
  questions.
- [roadmap.md](../../design/roadmap.md): delivery and validation.

The outcome remains open until human review confirms that the complete rewrite is understandable and
faithful. Structural and formatting checks do not supply that approval.

[[oc-9b94fe4]](oc-9b94fe4-corpus-states-each-claim-once.md) tracks consistent definitions and
references. The risk reports [[bd-c324184]](../bugs/bd-c324184-occ-starvation-under-contention.md),
[[bd-e63bac8]](../bugs/bd-e63bac8-cascading-revocation-on-divergence.md),
[[bd-c4d069b]](../bugs/bd-c4d069b-git-churn-from-task-state.md), and
[[bd-5cb2034]](../bugs/bd-5cb2034-unbounded-loops-under-demand-pressure.md) should remain available
for substantive review.
