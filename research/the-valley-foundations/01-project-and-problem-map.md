# 1. What the-valley is trying to make possible

the-valley is an experiment in making software work portable, verifiable, and understandable across
changes of host and author. Its immediate application is a developer working with coding agents. Its
larger ambition is a system that turns requested outcomes into completed work while preserving the
reasons, authority, and evidence behind each consequential action.

This chapter reconstructs that ambition from the repository. The later chapters ask which parts have
established solutions, which are engineering integration problems, and which remain research
questions. The reconstruction describes commit `5bb3a7736fe49b0cc4914299b142c0c9bafb6870`. It is an
interpretation for this report, not a replacement for the project's requirements.

The engineering aim is to satisfy clear requirements using good existing solutions wherever they
fit. The project's constraints determine which combinations are acceptable. Research helps clarify
those requirements, discover reusable solutions, and test the complete system. A novel mechanism is
one possible result of that work, never a prerequisite for success.

## 1.1 Three connected capabilities

The first capability is **durable, independently operated development infrastructure**. Code and
project knowledge should survive the disappearance of a hosting provider. Normal development should
remain recognizable. The scenario ladder therefore starts with hosting, replication, and
demonstrated recovery, rather than with a new collaboration interface.

The second is **integration that accepts evidence produced before submission**. A contributor should
be able to establish that the required checks passed where the work was authored. The integrator
should decide whether that evidence still applies to the state it will publish. This is more
specific than making CI faster: it changes the relationship between execution, evidence, and
admission.

The third is **a persistent account of intent that can drive further work**. Outcomes, dependencies,
decisions, and explanations become inputs to agents. The graph should help a fresh agent understand
the project and choose useful work. Completion must eventually mean that the requested result
exists, rather than that a worker has stopped or a status field has changed.

These capabilities reinforce one another, and each can also be useful independently. Portable
evidence could be useful with an ordinary forge. A well-maintained knowledge graph could help agents
without an autonomous scheduler. Reliable self-hosting could be valuable even if the outcome engine
never materializes. Those independent uses help diagnose and compare components. The primary
evaluation still concerns the complete workflow: whether the same intent, evidence, and authority
remain meaningful as work passes between them.

The local basis is the [premise](../../README.md), [requirements](../../design/requirements.md),
[scenario ladder](../../design/user-scenarios.md), and
[outcome sketch](../../.the-valley/ideas/ida-eac723e-outcome-dag.md).

## 1.2 Requirements as research questions

The requirements mix desirable experiences with technical guarantees. Each needs an observable test.
The following translation preserves their intent while making the questions more precise. These
questions guide both the selection of existing tools and the evaluation of their composition.

| Local goal                                  | Question that can be investigated                                                                           | Principal literatures                                                | Report treatment                                                                             |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| S1: repositories cannot be lost             | Which acknowledged states remain recoverable after which failures, and within what time?                    | Replication, backup consistency, local-first systems                 | [Chapter 4](04-events-trust-and-durability.md)                                               |
| S2: integration in seconds                  | Under which workload and trust assumptions can already-verified work be integrated in seconds?              | Incremental computation, build systems, concurrency control          | [Chapter 3](03-verification-and-integration.md)                                              |
| S3: attributable autonomous authors         | What can the system establish about a run's origin, authority, actions, and checks?                         | Workload identity, capabilities, provenance, software agents         | Chapters [4](04-events-trust-and-durability.md) and [5](05-knowledge-agents-and-outcomes.md) |
| S4: one history explains effects            | What facts must be retained to reconstruct an action and distinguish observation from explanation?          | Distributed causality, event sourcing, workflow recovery, provenance | Chapter 4                                                                                    |
| S5: bounded and revocable trust             | Which permissions are checked at each boundary, and when does withdrawal take effect?                       | Authorization, delegation, secure update systems                     | Chapters 3 and 4                                                                             |
| S6: incidents are attributed and remembered | What evidence supports attribution, and how is uncertainty represented?                                     | Causal reasoning, operational learning, knowledge maintenance        | Chapters 4 and 5                                                                             |
| Demand-shaped work                          | How are goals refined, prioritized, attempted, evaluated, and abandoned without losing the original intent? | Goal-oriented requirements, planning, scheduling, human automation   | Chapter 5                                                                                    |
| Minimal, portable composition               | Can an implementation be replaced without rewriting the project's authoritative state?                      | Modular design, protocols, interoperability                          | [Chapter 2](02-existing-systems.md) and [Chapter 6](06-synthesis-and-research-agenda.md)     |

This translation exposes a useful distinction. "Runs in seconds" is a performance target. "Cannot
accept the wrong check" is a safety property. "Eventually processes every eligible request" is a
liveness property. "Still works after the primary host is destroyed" is a recovery property. One
successful demonstration cannot establish all four.

### Keep requirements, constraints, and mechanisms separate

A requirement states the behavior that must hold for an actor in a specified situation. A constraint
limits the acceptable solutions. An architectural bet selects a way to meet the requirement within
those constraints. An implementation supplies that mechanism. Keeping these statements separate
makes it possible to adopt an existing tool without accidentally changing the intended result.

For example, preserving the explanation for an accepted change is a requirement. Operator ownership
of the retained record can be a constraint. Storing that record in Git is an architectural choice.
The same requirement might be met by another store with suitable recovery and export behavior. A
preference for Git should therefore be argued as a means of satisfying the chosen constraints.

The premise already imposes open source, minimalism, Nix in the reference implementation, and
decentralization where possible. Their exact consequences need interpretation. Minimalism could mean
few components, little custom code, or low operating burden; those are different objectives. The
report recommends judging it by the understanding and work needed to operate the complete system.

### Questions that must be answered before choosing the composition

The existing requirements leave several boundaries unsettled. These are proposed clarification
questions, not silently adopted answers:

- **Acknowledged durability.** If the primary host disappears immediately after a successful
  integration, may that change be lost? "Integrated means replicated" and replication "within
  minutes" describe different acknowledgment policies. Define the allowed loss window, independent
  failure domains, and which record the durability promise covers.
- **Latency and capacity.** What workload, percentile, resource budget, and evidence prerequisites
  does "seconds" assume? Report author-side checking as well as submission-to-acceptance time. State
  what happens when changes invalidate one another or arrive faster than they can be processed.
- **Trust and attribution.** Is an authorized but dishonest author in scope? Which observer is
  trusted to report execution and identify an agent run? Equal acceptance guarantees for humans and
  agents need not imply equal permissions. These answers determine which existing verification and
  identity mechanisms are sufficient.
- **History and explanation.** Which accepted decisions, failed attempts, authority changes, and
  external actions must remain explainable after losing the controller and bus? Distinguish observed
  events from an incident's inferred cause. An uncertain external result needs an explicit state.
- **The recoverable project.** Does recovery preserve only code and knowledge, or also the evidence,
  policy snapshots, trust roots, artifact references, and outstanding obligations needed to
  interpret and continue its work? Identify what can be discarded or reconstructed.
- **Completion.** What observation establishes that the requested result exists, and who may accept
  it or revise the criterion? A landed change, a cancelled prerequisite, and an attained outcome
  need separate meanings. State how a continuing obligation is reassessed.
- **Operating burden.** What does adding a project or collaborator require? Which credentials,
  policy surfaces, upgrades, and recovery procedures must the operator understand? Test whether the
  operator can resume safely after an absence and replace a component without redesigning its
  neighbors.
- **Execution equivalence.** Must local and remote checks use the same identified definition and
  environment, or literally the same backend? Which results must be reproducible, and which are
  observations with a freshness limit? State what a non-Nix consumer must understand or reproduce.

The scenario ladder deliberately postpones detailed acceptance criteria for later rungs. Preserve
that discipline. Clarify a boundary when the active workflow depends on it, while recording later
questions as unresolved. The proposed constraints in
[section 6.7](06-synthesis-and-research-agenda.md#67-an-opinionated-end-to-end-system) help organize
those decisions without prematurely selecting their implementation.

The report treats S7, public contribution by strangers, as a boundary condition. It does not
recommend building a public federation or an abuse-management system before the solo and small-team
cases are useful. However, mechanisms introduced at S2 and S3 should state their trust assumptions
so that their limits remain visible at S5.

## 1.3 What exists at the snapshot

The repository contains considerably more implementation than its opening status paragraph alone
suggests. It also contains proposed behavior that implementation comments sometimes describe in the
present tense. A research report needs a separate inventory.

| Area                       | Evidence in the repository                                                                                                                                                   | What this report can conclude                                                                                                                                                |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hosting and replication    | [Host module](../../nix/valley-host.nix), [host schema](../../schema/valley.cue), scenario acceptance checkboxes                                                             | Configuration and test artifacts exist. The scenario document records a restore exercise. This study did not independently inspect the deployed host or repeat that restore. |
| Knowledge convention       | [Graph documentation](../../.the-valley/README.md), [node schema](../../schema/node.cue), [lint](../../nix/knowledge-lint.py)                                                | Typed Markdown nodes and validation are implemented. Semantic adequacy of their contents is a separate question.                                                             |
| Evidence production        | [Attestation implementation](../../attest/), [schema](../../schema/attestation.cue), [conformance vectors](../../attest/conformance/)                                        | Tree identification, statements, signed notes, and verification tooling exist. The existence of a signature does not establish the truth of every field.                     |
| Integration                | [Controller](../../integrator/main.go), [verdict](../../integrator/verdict/verdict.go), [detailed design](../../design/integration.md)                                       | A polling controller evaluates requests and transfers evidence. It is not yet the event-subscribing controller suggested by the architecture overview.                       |
| Identity                   | [Registry schema](../../schema/identity.cue), [compiler](../../identity/), [governance decision](../../.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) | Registry compilation exists. This does not establish enforcement of every proposed delegation or human-presence rule.                                                        |
| Events                     | [Event schema](../../schema/events.cue), [CLI](../../bin/valley), [integrator publication](../../integrator/bus.go)                                                          | Ref projections and integration notifications exist. Current-state reconstruction and complete historical recovery are different properties.                                 |
| Witnesses and transparency | [Verification design](../../design/verification.md), [roadmap](../../design/roadmap.md)                                                                                      | The full independent re-verification and transparency backstop remains a proposed layer in the inspected material.                                                           |
| Outcome production         | [Demand-pressure idea](../../.the-valley/ideas/ida-3145b7a-demand-pressure.md), [production-DAG idea](../../.the-valley/ideas/ida-b48bded-production-dags-and-events.md)     | The conceptual model is developed. This study found no basis to call the complete general outcome engine implemented or evaluated.                                           |
| Recursive transparency     | [Draft invariant](../../design/self-transparency.md), architecture bootstrap discussion                                                                                      | Governance induction has concrete design, but the claimed closure over every durable effect remains unresolved.                                                              |

These are static observations. The report does not certify a deployment, claim a security audit, or
claim benchmark results. Its experimental designs are proposals, clearly separated from findings.

## 1.4 Six distinctions that organize the whole study

### Identity, authority, and truth

A signature can establish that a key signed some bytes. A policy can establish that the key was
authorized to make a particular decision. Neither alone establishes that an asserted computation
occurred, that the computation tested a useful property, or that the requested outcome was achieved.

Consider a statement saying that a migration test passed. The signature might be valid. The signer
might be an approved development host. The test might nevertheless contain no assertions. These are
three separate questions, requiring three separate kinds of evidence.

This distinction should govern both local checks and agent provenance. A host can attest that it
launched a particular harness with particular inputs. That provides an accountable observation. It
does not identify an independently acting model principal or expose all the causes of the model's
output. Chapters 3 and 4 develop the relevant trust boundaries.

### State, history, and explanation

A checkout answers what the project currently contains. A durable operation record answers what
happened to it. An explanation also needs the intent, policy, observations, and assumptions that
made an action reasonable at the time.

Suppose a branch is created, rejected twice, rewritten, integrated, and deleted. The final source
tree may retain the result while losing most of that sequence. Conversely, retaining every event
still does not explain why a requirement mattered. The report therefore evaluates state recovery,
historical recovery, and explanation quality separately.

### Valid computation and adequate judgment

Reproducing a result establishes agreement about a computation under some assumptions. It does not
establish that the computation was the right way to decide whether to act. The same separation
applies to an LLM judge: a consistently repeated assessment can still use an inadequate rubric.

This is especially important for the proposed progression from judgment by agents to deterministic
checks. The useful question is which parts of a decision have become stable enough to encode. A
mechanized proxy should retain a link to the requirement it approximates and to examples it gets
wrong. Otherwise the system can improve the proxy while losing the purpose.

### Availability and authority

Many replicas can make an object easy to retrieve. They do not decide which object is the accepted
project state. A designated integrator can supply that decision without being the only holder of the
data. Federation can also replicate a proposal without requiring the receiving valley to accept its
signer or policy.

This makes decentralization a collection of choices: storage, discovery, identity, decision making,
and recovery authority. Counting servers is an inadequate measure. Chapter 2 compares systems that
make different choices along these dimensions.

### Eligibility and attainment

A task is eligible when its prerequisites permit an attempt. An outcome is attained when the desired
condition holds. Closing blockers may establish eligibility. It cannot generally establish
attainment, because the decomposition may be incomplete or a prerequisite may have been abandoned.

The current graph's terminal-state convention is therefore an important boundary. It is useful for
administrative traversal, but an autonomous scheduler needs to know whether "no longer blocking"
means satisfied, waived, replaced, or impossible. Chapter 5 treats this as a central design problem.

### Fast admission and safe consequences

A change can become part of source history before every possible consequence is authorized. A
low-risk documentation edit and an irreversible external action need not share one evidence
threshold. This is a research recommendation, not a statement of current policy.

Separating these decisions gives the latency ambition more room. Local evidence might justify
integration while an independent check is still required for a particular deployment. The resulting
system must make both decisions explicit so that "landed" never silently becomes "approved for every
effect."

## 1.5 The strongest case for the project

An important engineering requirement is to make **the reason an action was accepted portable**.
Source code already travels well. Approval context, check definitions, evidence, and authority often
travel less well. the-valley can investigate a compact record that lets another implementation
reconstruct a decision without consulting the original service.

An important empirical question is how to make **project understanding testable across fresh
authors**. The
[theory-revival idea](../../.the-valley/ideas/ida-f83d7ba-theory-revival-accelerator.md) asks
whether a well-maintained graph helps a new agent preserve a project's design. The
[evaluation sketch](../../.the-valley/ideas/ida-99b87d9-theory-rebuild-as-evaluation.md) suggests
why-questions, proposed changes, and violations as probes. Those are substantially better targets
than measuring the amount of stored context.

Their connection is central to the end-to-end system. An agent could use project knowledge to
propose a change, attach evidence to it, and leave a durable record of what the system accepted.
Subsequent agents could then distinguish accepted decisions from speculative notes and failed
attempts. The interesting object is that entire learning cycle. Each component has extensive prior
art; whether their composition improves sustained development remains an empirical question.

## 1.6 The strongest case against it

The project could recreate a forge, a workflow engine, an authorization service, a build
coordinator, and an agent memory system under separate names. Small executables do not ensure a
small operating burden. If every component needs custom recovery rules and tightly coordinated
upgrades, the overall system may become harder to replace than a conventional platform.

It could also obtain speed mainly by weakening the acceptance criterion. Accepting a trusted host's
claim immediately is a legitimate policy for a personal project. It should be compared with another
system given the same trust policy. Comparing that path only with a remote build that independently
executes everything would confound trust and performance.

Finally, it could confuse richer records with better judgment. More provenance, dependencies, and
agent transcripts can increase both storage and attention cost without reducing mistakes. The right
tests ask whether those records change decisions, shorten recovery, or improve subsequent work.

These objections should shape a small end-to-end reference workflow assembled from the strongest
available components. Tests of evidence transfer, recovery, and knowledge quality can then explain
where that workflow succeeds or fails. The project need not demonstrate a separate innovation in
each subsystem before evaluating their joint value. [Chapter 6](06-synthesis-and-research-agenda.md)
sets out the composition constraints, adoption rule, and experiments for that evaluation.
