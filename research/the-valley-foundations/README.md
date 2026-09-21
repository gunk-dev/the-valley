# The foundations and research opportunities of the-valley

**A critical literature and systems review, 2026-09-20**

the-valley investigates how software development could work when authors are often agents, project
state belongs to its operator, and acceptance decisions carry evidence that can outlive a hosting
service. This report studies those goals against existing systems and academic literature. It
identifies the distinctions needed to reason about the design, the strongest alternatives, and
experiments that could establish whether its more ambitious ideas work.

The central question is whether **clear requirements and an opinionated set of end-to-end
constraints can make established tools work better together**. A good existing solution to a
requirement is a reason to adopt it. Innovation in an individual subsystem is optional. The project
succeeds when the complete workflow meets its requirements with acceptable cost and complexity.

Portable acceptance evidence and project knowledge are especially important connections between the
parts. They let work retain its justification as it moves between authors, tools, and hosts.
Reliable effect recovery, precise authority boundaries, and honest completion semantics determine
whether that continuity survives actual use. Their value should be measured in the whole workflow.

This report is independent research material. It lives outside `.the-valley/`, has no knowledge-node
status, and does not amend the design or roadmap. Its judgments are proposals to consider.

## Principal findings

1. **Evidence reuse is a strong idea with exact preconditions.** Equal relevant inputs can justify
   reuse of a deterministic check. The check definition, dependency model, execution assumptions,
   and current authority must all be explicit. A familiar check name is insufficient.
2. **Signing and verification establish different facts.** A signature authenticates a claim. It
   does not prove that a named program ran, that the test was adequate, or that an outcome was
   attained. The inspected design makes several stronger claims than its mechanisms support.
3. **Replay has three different jobs.** Rebuilding current state, recovering historical decisions,
   and finishing interrupted effects require different records. Current Git ref enumeration handles
   only part of the first job.
4. **Existing solutions are candidates to adopt as well as baselines.** Zuul, Gerrit, distributed
   collaboration systems, and portable build runtimes make important parts of the problem familiar.
   Each deserves a fit assessment against the requirements before custom work replaces it.
5. **The knowledge graph should be tested through unfamiliar changes.** Recall and retrieval are
   intermediate measures. The decisive question is whether a fresh contributor can apply the design
   with fewer conceptual mistakes and less later correction.
6. **A work graph does not define success.** Readiness, accepted attainment, cancellation,
   abandonment, and continuing obligations need distinct meanings. Scheduling more attempts cannot
   repair an inadequate acceptance criterion.
7. **The complete system is the unit of success.** The important test is whether shared constraints
   preserve intent, evidence, authority, and recovery across a useful workflow. Each custom
   component needs a demonstrated reason to exist. None needs to be novel for the system to be
   worthwhile.

These findings are developed and qualified in the chapters below. Static implementation findings are
not reported as demonstrated exploits. Proposed experiments are not reported as results.

## The report

| Chapter                                                                   | Subject                                                                   | What it should make clear                                                                      |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| [1. Project and problem map](01-project-and-problem-map.md)               | Goals, implemented state, and research questions                          | The difference between the immediate infrastructure project and the larger outcome engine      |
| [2. Existing systems](02-existing-systems.md)                             | Forges, portable collaboration, review, queues, and execution             | Which alternatives are credible and what each already solves                                   |
| [3. Verification and integration](03-verification-and-integration.md)     | Nix, incremental computation, evidence, concurrency, supply-chain trust   | When evidence transfers, what signatures mean, and what witnesses can establish                |
| [4. Events, trust, and durability](04-events-trust-and-durability.md)     | Causality, replay, effects, recovery, delegation, and governance          | Which facts and obligations must survive failure                                               |
| [5. Knowledge, agents, and outcomes](05-knowledge-agents-and-outcomes.md) | Design rationale, memory, planning, scheduling, attention, and evaluation | How to distinguish useful understanding and attained outcomes from stored context and activity |
| [6. Synthesis and research agenda](06-synthesis-and-research-agenda.md)   | Judgments, architectural choices, and nine experiments                    | What to investigate next and which results would change the design                             |
| [7. Reading guide and glossary](07-reading-guide-and-glossary.md)         | A structured route through the literature                                 | How to deepen understanding and keep related concepts distinct                                 |

The annotated bibliography is divided into [systems and architecture](sources-systems.md),
[verification](sources-verification.md), [distributed systems and trust](sources-distributed.md),
and [knowledge and agents](sources-agents.md). Entries identify contribution, relevance,
limitations, and the material actually read. Source IDs S, V, D, and A are local to those registers.
When two chapters use the same work, its appearance in two registers does not make it independent
evidence.

For a first pass, read chapters 1 and 6, then the chapter nearest the next engineering decision. For
a sustained study, follow chapter 7 and return to the experiments after reading the primary sources.
For critical design review, start with the guarantee table in chapter 6 and follow each row to its
supporting analysis.

## Scope and method

The repository snapshot is `5bb3a7736fe49b0cc4914299b142c0c9bafb6870`, whose latest commit is dated
2026-09-01. The working tree was clean before this report was added. The review examined the
premise, scenario ladder, requirements, architecture, detailed designs, selected knowledge nodes,
schemas, and relevant implementation paths. It treated proposed nodes as proposals and checked code
where a claim concerned implemented behavior. It did not inspect the deployed host or external
private systems referenced by the project.

The external review was organized around the project's questions rather than one discipline's
terminology. Searches followed these connections:

| Starting concern                          | Search and reading families                                                                             |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| Checks should run once and travel         | Functional deployment, incremental builds, safe test selection, attestation, builder trust              |
| Integration should remain fast            | Optimistic concurrency, speculative gating, merge trains, collaboration conflicts                       |
| One history should explain effects        | Logical clocks, provenance, event sourcing, reconciliation, outboxes, sagas                             |
| Authority should be bounded and revocable | Complete mediation, capabilities, workload identity, secure metadata updates, human-presence assertions |
| Agents need durable understanding         | Theory building, design rationale, truth maintenance, retrieval, memory, software-agent evaluation      |
| Outcomes should drive useful work         | Goal refinement, hierarchical planning, DAG scheduling, stopping criteria, human attention              |

Sources were selected for a direct connection to a requirement, a mechanism the project could adopt,
or a result that challenges an architectural assumption. The review favored original papers,
author-hosted texts, standards, and official documentation. It used references within discovered
papers to follow important intellectual predecessors. Product claims are identified as documented
behavior, not independent evaluations. Recent preprints are labeled and treated cautiously.

This is a **critical narrative review with a scoping survey of systems**, not an exhaustive
systematic review or meta-analysis. There was no preregistered database protocol, exhaustive
screening count, independent reproduction of published experiments, or attempt to pool effect sizes
across unlike studies. Search visibility, available full text, and the repository's own framing
influence coverage. The source registers make those limits inspectable rather than hiding them
behind a citation count.

The study deliberately gives less attention to public-forge abuse, commercial procurement, general
distributed consensus engineering, and broad philosophical claims about model cognition. These are
either deferred by the project or unnecessary to decide the near-term questions. It also avoids
claiming that a sampled literature search establishes novelty or that any existing system implements
the entire proposed combination.

## How to interpret claims

**Repository observation** describes the inspected snapshot. **Established result** describes a
theoretical construction or finding with the cited work's assumptions. **Documented behavior** is an
implementation's published contract. **Inference** applies those ideas to the-valley.
**Recommendation** expresses a design judgment. **Hypothesis** requires measurement. The prose uses
these distinctions where confusion would materially affect a decision.

An abstract-only source is useful for locating a result and reporting what its authors summarize. It
does not support detailed claims about methods that were not inspected. Living documentation can
change after the access date. Versioned papers and documentation are used where available; this
report does not archive full copies of the external corpus.

The report's validation concerns its own prose, local references, citation coverage, and
consistency. Its experimental agenda remains future work. Findings can be adopted into project
decisions later, with their scope and supporting evidence preserved.
