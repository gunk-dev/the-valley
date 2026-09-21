# 7. Reading guide and glossary

The most useful way to study this material is to move between a primary source, a concrete failure
case, and the corresponding part of the-valley. The sequence below is designed to develop judgment
about mechanisms rather than familiarity with names. The source registers provide direct links and
state which portions were inspected for this report.

## 7.1 The shortest route through the foundations

For a first sustained pass, prioritize these twelve readings. They span the main intellectual
dependencies without requiring every product manual first.

| Reading                                                                          | Source register                | Question to carry into the-valley                                                          |
| -------------------------------------------------------------------------------- | ------------------------------ | ------------------------------------------------------------------------------------------ |
| Parnas, _On the Criteria To Be Used in Decomposing Systems into Modules_         | [S01](sources-systems.md)      | Which decisions can a component change without forcing other components to change?         |
| Saltzer, Reed, and Clark, _End-to-End Arguments in System Design_                | [S02](sources-systems.md)      | Which desired result can only the application establish?                                   |
| Dolstra, _The Purely Functional Software Deployment Model_, selected foundations | [V01](sources-verification.md) | What makes an execution input explicit, and what remains an assumption?                    |
| Mokhov, Mitchell, and Peyton Jones, _Build Systems à la Carte_                   | [V02](sources-verification.md) | Which part chooses what to do, and which part decides whether a result remains valid?      |
| Kung and Robinson, _On Optimistic Methods for Concurrency Control_               | [V11](sources-verification.md) | What must be checked immediately before publishing speculative work?                       |
| Torres-Arias et al., _in-toto_                                                   | [V15](sources-verification.md) | How can independently performed steps carry a verifiable relationship?                     |
| Lamport, _Time, Clocks, and the Ordering of Events in a Distributed System_      | [D01](sources-distributed.md)  | What does an order of events establish about causality?                                    |
| Helland, _Life beyond Distributed Transactions_                                  | [D13](sources-distributed.md)  | Who resolves uncertainty after an external action and a local crash?                       |
| Naur, _Programming as Theory Building_                                           | [A01](sources-agents.md)       | What understanding must a new contributor acquire beyond reading the code?                 |
| van Lamsweerde, _Goal-Oriented Requirements Engineering: A Guided Tour_          | [A07](sources-agents.md)       | Why should the proposed subgoals be sufficient for the parent goal?                        |
| Bainbridge, _Ironies of Automation_                                              | [A25](sources-agents.md)       | What must a human retain in order to supervise exceptions competently?                     |
| Kapoor et al., _AI Agents That Matter_                                           | [A22](sources-agents.md)       | Would an apparent gain survive equal budgets, stronger baselines, and held-out evaluation? |

For each reading, write four short notes: the problem it addresses, its assumptions, what it
establishes, and one thing it does not establish. Then find a local design sentence that becomes
more precise after reading it. This exercise is more valuable than importing the paper's vocabulary
into the project unchanged.

## 7.2 Eight deeper study sessions

### Session 1: Ownership and composition

Read Parnas and the end-to-end paper, then compare Radicle, Fossil, SourceHut, and Tangled in
[chapter 2](02-existing-systems.md). Consult S03–S09 for the original system descriptions.

Draw the authoritative stores for one project under each architecture. Mark what a normal clone
preserves, what requires another export, and who can decide the accepted branch. Then remove the
primary host from the drawing. Identify which functions continue and which require recovery.

The test of understanding is the ability to explain why storage ownership, identity independence,
availability, and decision authority can vary separately. Do not rank systems by a single
"decentralized" score.

Then choose one end-to-end requirement and assign its responsibilities to existing tools. Construct
a case in which every tool passes its own checks while the complete workflow fails. Identify the
missing agreement between them. This gives a concrete reason for a shared constraint, an adapter, or
a different selection of tools. Use section 6.7 to assess whether that cost is justified.

### Session 2: Computation identity and reuse

Read the Nix foundations, _Build Systems à la Carte_, and the selective-testing work in V27. Use the
Nix manual and Bazel documents in V04–V08 to connect the abstractions to actual execution controls.

Choose three real checks: a prose formatter, a compiler test, and a network-dependent observation.
For each, list all the information required to decide whether an old result still applies. Include
the executable definition and facts about absent or newly introduced inputs.

Construct one conservative invalidation and one unsound reuse rule. Explain why the former wastes
work while the latter can accept a result for the wrong computation. This prepares the ground for E2
and E3 in [chapter 6](06-synthesis-and-research-agenda.md).

### Session 3: Claims, trusted builders, and witnesses

Read in-toto, the in-toto Statement format, DSSE, and SLSA's actual build requirements. Compare the
Sigstore security model, TUF, and transparency guarantees in V15–V22. Read Thompson's compiler paper
to challenge an overly simple definition of independent verification.

Follow one hypothetical signed check through the system. Ask separately whether its bytes are
authentic, its check definition is approved, its result can be reproduced, its signer is authorized,
and its test is adequate. Produce an example in which four answers are favorable and the fifth
prevents acceptance.

Then use chapter 3's sampling equations with several audit probabilities. State the independence and
detection assumptions aloud. Explain the difference between reducing the chance of an undetected
pattern and ensuring that one particular claim will be checked.

### Session 4: Integration under contention

Read Kung and Robinson, Crystal's collaboration-conflict paper, and the Zuul gating guide. Compare
the GitHub and GitLab mechanisms in chapter 2. Keep the 2026 queue-governance preprint as an
additional research lead rather than the foundation of the argument.

Work through three changes by hand. Make two textually disjoint changes interact through a shared
behavioral assumption. Make a third change alter the check definition. For each possible order,
identify the combined tree, reusable evidence, invalidated evidence, and serial decision work.

The goal is to explain why correct ordering, complete computation dependencies, and adequate checks
solve different problems. A scheduler cannot discover every omitted semantic dependency merely by
choosing a better queue order.

### Session 5: History and effect recovery

Read Lamport, the event-sourcing discussion, the controller and Temporal contracts, Helland, and
Sagas in D01–D13. Inspect the actual JetStream retention and acknowledgement semantics alongside
those readings.

Draw a failure timeline for "accept source, request deployment, remote deployment succeeds, local
process crashes." At each interval, write what durable fact exists and what a restarted process can
know. Design separate procedures for re-enumerating current state, recovering the decision history,
and resolving the uncertain external result.

A strong answer includes a case where the right recovery state is explicitly unknown. Replacing
uncertainty with an automatic retry is a policy choice, not an explanation of what happened.

### Session 6: Authority and recursive governance

Read complete mediation, Macaroons, SPIFFE/SPIRE, TUF's update rules, and the WebAuthn distinctions
in D20–D25. Compare them with the local registry, adoption decision, and self-transparency draft.

Draw the trust boundary around a running agent. Mark every credential and effect path it can reach.
Then compare that diagram with its recorded delegation. Any extra path is authority the record alone
does not constrain.

Work through a normal policy amendment, a key compromise, an expired key during failed convergence,
and a host restore from an old image. Identify who can restore authority and what survives as
evidence of the exceptional action. Explain the difference between authorization then and
authorization now.

### Session 7: Understanding, rationale, and memory

Read Naur, gIBIS, Doyle's truth-maintenance work, and selected agent-memory papers in A01–A17. Add
Dung's argumentation model, A28, when considering objections and conflicting reasons. Read these as
different problem statements, rather than as interchangeable support for "use a knowledge graph."

Choose a current design decision and write the smallest explanation that would let an unfamiliar
engineer defend it, identify a condition that would invalidate it, and propose a compatible
extension. Compare that explanation with a chronological conversation transcript and a retrieved
summary.

Use the theory-reconstruction experiment to ask what was lost. If the reader gives the right answer,
change the situation and see whether the understanding transfers. A memorized phrase should not earn
the same score as a justified new decision.

### Session 8: Outcomes, supervision, and evaluation

Read goal-oriented requirements, HTN planning, scheduling, the automation papers, and the evaluation
work in A07–A10 and A18–A27. Pair the earlier METR study with the later methodology update. Treat
benchmark claims as conditional on their tasks, harnesses, and observation periods.

Design an outcome with an incomplete decomposition, an abandoned blocker, two valid alternatives,
and a prerequisite shared by two roots. Decide who may revise the acceptance criterion. Add a budget
and a cancellation halfway through a run.

Write a completion report that can say "not attained" even when substantial useful work has been
done. Then specify the comparison that would show whether automation improved the outcome rather
than merely increasing activity.

## 7.3 Questions for judging a new paper or tool

Use the same questions when extending this report:

- What exact problem is solved, and which local requirement needs that solution?
- Which assumptions make the guarantee possible? Who controls the dependencies and evaluator?
- Is the evidence a proof, a benchmark, a field study, a project description, or a proposal?
- What is the strongest baseline? Are trust, budgets, caches, and human work held comparable?
- Which failure cases are excluded? What happens to existing results after revocation or recovery?
- Does the evaluation measure the desired outcome or a proxy that can improve independently?
- Could the mechanism be adopted as one component? What durable state or protocol would become tied
  to it?
- What observation would make its adoption a mistake for the current pilot?

## 7.4 Working glossary

These definitions state how the report uses the terms. They are reading aids, not additions to the
project's schema. Follow the relevant chapter for assumptions and primary sources.

| Term                           | Meaning in this report                                                                                                |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| Acceptance criterion           | A stated procedure or condition used to decide whether requested work counts as achieved                              |
| Action cache                   | A mapping from an identified computation to result metadata                                                           |
| Attestation                    | An attributable statement about an identified subject; its predicate says what is claimed                             |
| Attainment                     | The requested condition is accepted as holding in a specified scope and environment                                   |
| Authority                      | Permission to make a particular decision or perform an action at an enforcement boundary                              |
| Canonical state                | The project state accepted under the relevant governance rules                                                        |
| Causality                      | A relationship of influence or dependence; its exact meaning differs between execution order and incident explanation |
| Complete mediation             | Checking authority across every relevant access path within a declared boundary                                       |
| Content address                | An identifier derived from the content it names                                                                       |
| Critical path                  | A duration-dependent chain constraining completion under a specified scheduling model                                 |
| CRDT                           | A data type whose defined concurrent operations support convergence under specified assumptions                       |
| Delegation                     | A grant of authority from one actor to another, with a scope that must be enforced                                    |
| Determinism                    | The same specified computation and inputs yield the same result                                                       |
| Effect                         | A change outside a computation's returned value, such as updating a service or sending a message                      |
| Effect attempt                 | One execution attempt toward a logical effect; retries need not represent new intended effects                        |
| Evidence transfer              | Accepting a prior result as applicable to the candidate state under a stated rule                                     |
| Hermeticity                    | Restricting a computation's observations to its specified environment and dependencies                                |
| Idempotency                    | Repetition does not repeat the substantive state change the application seeks to perform                              |
| Inclusion proof                | Evidence that an entry belongs to a particular committed log view                                                     |
| Input closure                  | The dependencies required by a computation, under a specified dependency model                                        |
| Invalidation                   | A reason that earlier evidence or a derived conclusion can no longer be used as before                                |
| Materialized view              | Stored results derived from authoritative facts for convenient access                                                 |
| Optimistic concurrency control | Doing work speculatively and validating its assumptions before publishing its effect                                  |
| Oracle                         | The procedure used to judge correctness or success in an evaluation                                                   |
| Outcome                        | A condition someone wants to hold; distinct from the attempt intended to produce it                                   |
| Provenance                     | Recorded relationships among inputs, activities, outputs, and responsible actors                                      |
| Readiness                      | Satisfaction of the conditions that permit an attempt to begin                                                        |
| Reconciliation                 | Repeatedly comparing durable desired state with observations and acting on remaining differences                      |
| Reproducibility                | Ability to recreate specified artifacts from identified source, environment, and instructions                         |
| Revocation                     | Withdrawal of authority or acceptability under a policy; it does not erase past external effects                      |
| Saga                           | A sequence of committed steps with compensating actions for relevant failures                                         |
| Sound reuse                    | Reuse that preserves the result of the specified approved computation under its assumptions                           |
| Speculative gating             | Checking possible combined future states before their final integration order is completed                            |
| Theory reconstruction          | Acquiring enough project understanding to explain and modify it appropriately                                         |
| Transactional outbox           | Recording a state change and the obligation to publish it within one atomic boundary                                  |
| Transparency                   | Making a defined class of actions or claims externally inspectable under an explicit retention and trust model        |
| Trusted computing base         | The components and assumptions whose failure can invalidate a stated guarantee                                        |
| Witness                        | An independent participant that checks or observes a specified claim; independence must be defined                    |

The most important distinctions to retain are authenticity versus truth, provenance versus cause,
readiness versus attainment, current state versus event history, and replay versus repeating an
external effect. Many attractive designs become easier to assess once those words stop doing each
other's work.
