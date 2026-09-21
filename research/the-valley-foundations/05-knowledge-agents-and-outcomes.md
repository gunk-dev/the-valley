# Knowledge, agents, and outcomes

The most consequential question for the-valley is whether a project can preserve enough
understanding to let a succession of unfamiliar contributors change it well. An agent that can
retrieve a decision is useful. An agent that understands when the decision applies, notices when its
premises fail, and produces a change consistent with the project's purpose is substantially more
useful. The knowledge graph and outcome engine should be judged against that second standard.

This chapter examines that ambition through design rationale, belief revision, planning, agent
memory, human automation, and evaluation. The central conclusion is an inference from those fields:
the graph can be a strong common substrate, but it does not eliminate the distinct problems of
deciding what is true, deciding what to attempt, deciding what counts as success, or deciding when a
human needs to intervene. Those distinctions should survive even if one interface presents them
together.

Research was checked on 2026-09-20. Source identifiers A01–A28 refer to the
[annotated source register](sources-agents.md). Literature findings are attributed below. Statements
marked **assessment**, **recommendation**, or **hypothesis** are this report's judgments, rather
than results established by the cited work. Proposed experiments have not been run.

## 1. The actual starting point

The repository already has a useful, modest implementation. Its
[knowledge convention](../../.the-valley/README.md) defines Markdown files with typed frontmatter.
[The schema](../../schema/node.cue) and [lint](../../nix/knowledge-lint.py) check structure,
identifiers, links, supersession, and graduation. The convention explicitly describes itself as a
directory convention. This is materially different from an implemented autonomous planner.

The [outcome-DAG sketch](../../.the-valley/ideas/ida-eac723e-outcome-dag.md) proposes priority
propagation and dispatch against unblocked outcomes. The
[demand-pressure experiment](../../.the-valley/ideas/ida-3145b7a-demand-pressure.md) adds continuous
reconciliation, leases, and escalation. The later
[production-and-events node](../../.the-valley/ideas/ida-b48bded-production-dags-and-events.md)
describes events as reasons to re-examine state, with a reconciler deciding what work remains. These
are design proposals with different maturity levels. The roadmap places agent dispatch and
observable knowledge changes in later phases.

Three existing bug nodes already identify important limits:
[runaway dispatch](../../.the-valley/bugs/bd-5cb2034-unbounded-loops-under-demand-pressure.md),
[integration starvation](../../.the-valley/bugs/bd-c324184-occ-starvation-under-contention.md), and
[task-state churn](../../.the-valley/bugs/bd-c4d069b-git-churn-from-task-state.md). Their mechanisms
deserve tests. Their strongest claims about inevitable severity should not be treated as
measurements. Small text objects in Git do not, by themselves, establish unacceptable clone cost;
that depends on update frequency, packing, retention, and repository size.

One concrete semantic problem appears before any performance question. The current convention and
`blocker_cleared` predicate treat an abandoned outcome as a cleared blocker. That can reasonably
mean “the parent may now be reconsidered.” It cannot mean “the abandoned requirement has been
fulfilled.” The demand-pressure node itself recognizes abandonment as a possible stale dependency.
An automated engine will need an explicit answer to this difference.

**Assessment:** the small file convention is a good experimental platform. Its main missing feature
is not a vector database. It is a tested account of how a change in knowledge changes the validity
of plans and completion claims.

## 2. Knowledge preservation is a theory-reconstruction problem

### Naur supplies the challenge, not a guarantee

Peter Naur's _Programming as Theory Building_ treats programming as the formation of understanding
about how a program addresses the world. His account makes the continued presence of people holding
that understanding central to a program's life. Familiarity with the program text and documentation
alone is insufficient; exact revival after the theory holders disappear is impossible in his
account. This is a conceptual argument developed through programming experience, not an experiment
on modern language models. [A01: Naur, 1985](https://gwern.net/doc/cs/algorithm/1985-naur.pdf)

The repository's
[theory-revival node](../../.the-valley/ideas/ida-f83d7ba-theory-revival-accelerator.md) recognizes
that challenge and makes a wager against its strongest conclusion. Cheap capture and strong readers
might preserve enough of the important structure to make repeated reconstruction effective. The
[evaluation node](../../.the-valley/ideas/ida-99b87d9-theory-rebuild-as-evaluation.md) proposes
why-questions, defended modifications, and detection of plausible violations as probes.

**Recommendation:** test a narrower, operational claim: access to curated project knowledge improves
the quality of previously unseen modifications at an acceptable reconstruction cost. Success would
be valuable without establishing that text contains the whole original theory. Nor should agreement
with the current theory holder be the only criterion. A contributor may understand a design and find
a valid reason to revise it.

For example, a contributor asked to speed integration might suggest running all missing checks on
the host. Repeating “the host does not run checks” shows recall. Explaining how that choice changes
the trust and execution boundary shows understanding. Recognizing a case that requires a separately
authorized verification service shows the ability to extend the design. These are different levels
of competence, and the evaluation should score them separately.

### Design rationale has a substantial history

Issue-Based Information Systems, associated with Kunz and Rittel's 1970 work, precede contemporary
agent memory by decades. Conklin and Begeman's gIBIS implemented collaborative, typed hypertext for
capturing early design deliberation. The paper's abstract describes a database-backed tool and
reports encouraging but incomplete early experience. It establishes prior art for structured
deliberation, not proof that graph-shaped documentation improves engineering outcomes at scale.
[A02: Conklin and Begeman, 1988](https://doi.org/10.1145/62266.62278)

**Assessment:** the-valley's insistence on a clean statement of current design addresses a real
retrieval problem: a reader should not have to reconstruct the conclusion from a discussion.
However, clean current prose and recoverable justification serve different questions. “What should
the system do?” needs a current answer. “Under which changed conditions should this decision be
reopened?” needs premises and limiting conditions. Git history preserves previous bytes, but those
conditions remain difficult to find if they were never stated clearly.

A useful decision record could therefore say: “This design assumes one integrator per protected ref.
Reconsider the design if integration must remain available during loss of that integrator.” That
sentence is current rationale, not an account of participants or discarded drafts. A report or
research appendix can retain the wider alternatives without turning the normative graph into a
transcript archive.

Formal argumentation addresses a related but different problem. Dung models arguments and attacks
between them, then defines conditions under which sets of arguments are acceptable. Different
acceptance semantics can yield different conclusions. The framework deliberately abstracts away from
the internal content of arguments; it does not determine whether a natural-language argument is
sound.
[A28: Dung, 1995](https://cse-robotics.engr.tamu.edu/dshell/cs631/papers/dung95acceptability.pdf)

**Inference:** disagreement belongs in the research surface even when the normative graph states one
current policy. Two incompatible proposals can each deserve investigation. Recording the objection
to a proposal is useful without granting it blocking authority over all project work. Moreover,
counterarguments can form cycles even when execution prerequisites must remain acyclic. The whole
knowledge graph should not inherit the outcome scheduler's topological restrictions. A typed graph
earns its complexity by giving those relations different meanings.

### Belief dependencies differ from work dependencies

Doyle's truth maintenance system records reasons for beliefs and revises accepted beliefs when their
supporting assumptions change. Its important contribution here is the explicit relationship between
a conclusion and the conditions that justify it. The paper addresses symbolic problem solvers; it
does not solve the problem of extracting reliable justifications from arbitrary prose.
[A03: Doyle, 1979](https://dspace.mit.edu/entities/publication/5377b306-4ecc-4687-b1f5-78cbb4a0543a)

**Inference:** a single untyped notion of “depends on” would obscure three relationships in
the-valley. A deployment can wait for a build. A design decision can rely on an assumption about
threat models. An incident conclusion can be supported by an observed log. Finishing the build
releases work; invalidating the assumption calls for reconsideration; withdrawing the log weakens an
explanation. The appropriate response is different in each case.

W3C PROV distinguishes entities, activities, derivation, and responsibility. That vocabulary helps
represent evidence relationships; it does not establish their truth.
[A04: W3C PROV-DM, 2013](https://www.w3.org/TR/prov-dm/)

**Recommendation:** keep authoritative status, supporting evidence, and observed provenance
separately queryable. Initially this can be prose with a small number of explicit links. Add machine
semantics only when a concrete question requires them. A useful first question is: “Which active
conclusions need review because evidence X was invalidated?” A general-purpose ontology is
unnecessary to ask it. When promoting a conclusion into durable knowledge, retain enough evidence to
reassess it after raw logs expire. If that support becomes unavailable, record the loss explicitly
rather than presenting the conclusion as equally supported.

## 3. Existing tools clarify the storage choices

git-bug embeds a distributed issue tracker in Git without adding ordinary project files. Its
official documentation describes offline operation, synchronization through Git remotes, several
interfaces, and bridges to hosted trackers. It is direct evidence that repository-associated project
knowledge does not require Markdown in the checked-out source tree. Its documented behavior is not
an independent performance evaluation.
[A05: git-bug documentation](https://github.com/git-bug/git-bug)

Beads is closer to the proposed dispatch surface. Its current README describes a dependency-aware
issue graph, a ready-work query, atomic task claiming, persistent memory, and Dolt-backed storage.
Embedded mode is described as single-writer; server mode supports concurrent writers. JSONL is an
export rather than the authoritative database. These are claims about the documented implementation
at the access date. They should not be generalized to every historical Beads version.
[A06: Beads documentation](https://github.com/gastownhall/beads)

The [local Beads comparison](../../.the-valley/ideas/ida-aea57f0-prior-art-beads.md) identifies the
important requirement: knowledge changes should pass through the same governed integration boundary
as other durable project changes. **Assessment:** the phrase “one history” needs a precise
definition before it becomes a storage constraint. A repository can contain many Git refs with
different histories. A system with several stores can present one causally linked audit record.
Neither fact alone establishes atomic publication of code and knowledge.

Consider the specific failure to prevent. A change fixes a parser, but its outcome is marked done
before the protected ref advances. A later integration rejection leaves the outcome falsely closed.
Storing both records in one directory does not prevent this if they land separately. Conversely, two
stores can enforce a publish protocol that never exposes completion before the relevant integration
receipt. That protocol may be too complex for v1, but the decisive property is the transition rule.

Three designs are worth comparing experimentally:

| Design                                                        | Main advantage                                      | Question that determines suitability                                    |
| ------------------------------------------------------------- | --------------------------------------------------- | ----------------------------------------------------------------------- |
| Markdown nodes in the code tree                               | Simple inspection and atomic code-plus-node commits | How much concurrent editing and status traffic remains comfortable?     |
| Git object records outside the code tree                      | Independent collaboration without worktree noise    | Which fetch, backup, and authorization rules cover the additional refs? |
| Transactional graph store with immutable publication receipts | Efficient claims, queries, and concurrent updates   | How are graph publication and code integration coupled and recovered?   |

**Recommendation:** keep the current durable files until measured workloads justify another backend.
Build derived indexes over commit-pinned snapshots. Give transient leases their own operational
contract; do not silently turn a heartbeat into authoritative project knowledge. The existing
[signal contracts](../../.the-valley/decisions/dcr-62ecc36-signal-contracts.md) already distinguish
durable events, lossy metrics, and retained logs. That distinction is a better starting point than
choosing one storage engine for everything.

## 4. Agent memory is several different systems

### Retrieval improves access, not authority

Retrieval-augmented generation couples generation with retrieval from external documents. Lewis and
colleagues demonstrated this approach on knowledge-intensive language tasks using a learned
retriever and sequence generator. It provides a foundation for bringing external material into
generation; it does not establish which project document is authoritative when several disagree.
[A17: Lewis et al., 2020](https://arxiv.org/abs/2005.11401)

MemGPT makes memory management part of agent behavior. It gives the model operations for moving
information between limited working context and external storage, and evaluates conversational
memory and document analysis. Its experiments also expose dependence on tool-use capability and
retrieval behavior. The operating-system analogy is a design aid, not a guarantee that information
will be loaded when needed. [A11: Packer et al., 2023/2024](https://arxiv.org/html/2310.08560v2)

Generative Agents combines remembered experiences, retrieval, reflection, and planning in a
simulated community. Its evaluations emphasize believable behavior and emergent coordination. The
work is useful for understanding how these components interact, but social believability in a small
simulation is not a test of reliable software maintenance.
[A12: Park et al., 2023](https://arxiv.org/html/2304.03442v2)

Reflexion stores verbal feedback from attempts and uses it in later trials without updating model
weights. It supplies a concrete model for learning from execution feedback across attempts. Its
reported benchmark gains depend on the evaluator, feedback, task distribution, and retry setup. They
do not imply that an agent's written explanation of its failure is itself reliable evidence.
[A13: Shinn et al., 2023](https://arxiv.org/html/2303.11366v4)

**Assessment:** these mechanisms are compatible with the-valley's
[single-home-for-project-knowledge principle](../../.the-valley/ideas/ida-f172c8e-project-knowledge-not-agent-instructions.md).
The durable graph can remain authoritative while each run uses a temporary summary, search index, or
working-memory view. The important boundary is promotion. A remembered observation should not become
project policy simply because a model summarized it confidently.

A useful distinction is:

| Material              | Example                                                | Appropriate treatment                      |
| --------------------- | ------------------------------------------------------ | ------------------------------------------ |
| Project commitment    | Integration reads policy from the target branch        | Governed durable knowledge                 |
| Observation           | A command failed against a named tree and tool version | Evidence with execution context            |
| Tentative explanation | The failure may come from an environment mismatch      | Hypothesis, open to correction             |
| Run working state     | Files still to inspect during this attempt             | Disposable or checkpointed execution state |

These categories can share an interface. They should retain different authority and retention rules.
For example, an index may retrieve a hypothesis as relevant context, but the briefing should not
render it as a decided invariant.

### A large context window is not a memory evaluation

_Lost in the Middle_ found that the tested models often used relevant information less successfully
when it appeared in the middle of long contexts. Its contribution is an experimental method:
manipulate position while holding the information constant. The measured degradation belongs to the
tested models and tasks; its magnitude should not be assumed for current models.
[A14: Liu et al., 2023/2024](https://arxiv.org/html/2307.03172v3)

LongMemEval broadens memory testing to extraction, cross-session reasoning, temporal reasoning,
updates, and abstention. It uses curated questions within constructed conversation histories. These
dimensions matter more to the-valley than a single retrieval score, although conversational memory
is not project design understanding.
[A15: Wu et al., 2024/2025](https://arxiv.org/html/2410.10813v1)

The 2026 LongMemEval-V2 preprint moves toward environment-specific experience: state changes,
procedures, recurring pitfalls, and questions whose premises may be false. Its evaluation asks
memory systems to return bounded evidence for a fixed reader. That separation helps distinguish
retrieval quality from answering ability. Its website environments and question-answering
formulation still leave long-term engineering quality unmeasured.
[A16: Wu et al., 2026](https://arxiv.org/html/2605.12493v1)

**Recommendation:** evaluate retrieval over actual project histories containing deliberate changes
of mind. Ask which decision is current, what was true at a past commit, whether an old assumption
still holds, and whether the graph contains enough evidence to answer. “Unknown” should be correct
when the record is insufficient. A system that answers every question fluently is a poor project
memory.

The retrieval result should identify its source commit and preserve access to the full passage. Rank
current authoritative material separately from historical material. Test lexical search before
adding embeddings, and compare both with graph expansion from explicit references. The winning
method may differ for exact identifiers, conceptual questions, and questions about change over time.

SWE-agent provides another relevant result: changing the agent's tools and feedback changes its
ability to work in a repository. Its study evaluates specialized navigation, editing, and execution
interfaces, including ablations against a shell-based baseline. It supports treating interface
design as part of the experimental system, rather than attributing every success or failure to the
model. [A18: Yang et al., 2024](https://arxiv.org/html/2405.15793v3)

**Hypothesis:** small project-specific verbs such as “show current decision,” “explain blockers,”
and “show evidence for completion” will provide more reliable value initially than an elaborate
autonomous memory manager. Test that hypothesis under equal tool-call and context budgets.

## 5. An outcome graph is a plan representation, not a complete planner

### Refinement needs a meaning

Goal-oriented requirements engineering separates desired properties from operations that might
achieve them. Van Lamsweerde's account distinguishes AND refinements, alternative OR refinements,
conflicting goals, responsibility, and assumptions about the environment. It also distinguishes
achieving a condition from maintaining one over time. This is directly relevant to a system that
wants to represent both shipping an artifact and keeping a service healthy.
[A07: van Lamsweerde, 2001](https://webperso.info.ucl.ac.be/~avl/files/RE01.pdf)

**Inference:** the current `blocked_by` relation is sufficient for an initial worklist, but too weak
to carry all the meanings implied by an outcome engine. “Release is waiting for a security review”
is a precedence constraint. “A valid release consists of a tested artifact and a compatible
migration” is a claim about sufficient conditions. “Restore service by rollback or by repair”
presents alternatives. Representing every edge as a mandatory blocker would launch unnecessary work
in the last example.

Hierarchical task network planning studies how compound tasks can be reduced through methods into
more concrete tasks. Erol, Hendler, and Nau show that expressivity and complexity depend strongly on
the allowed task-network structure and restrictions. Their results concern formal planning models;
they do not say that the-valley's current small graph is intractable. They do show why unrestricted
recursive decomposition deserves its own design rather than being treated as free scheduling.
[A08: Erol et al., 1994](https://cdn.aaai.org/AAAI/1994/AAAI94-173.pdf)

**Recommendation:** initially let agents propose decompositions while a deterministic scheduler
executes an accepted graph. Each proposed decomposition should state its parent condition, its
assumptions, why the children are sufficient, and what would invalidate the plan. A new child should
consume a share of the parent's budget; adding an edge should never mint new spending authority.
This is a recommendation for bounded automation, not a demand for approval of every low-impact task.
Pin the accepted objective and acceptance procedure, including which authority may revise them. An
agent's proposed amendment must remain distinguishable from the rule currently deciding success.

### Readiness, priority, and criticality are different

A graph can identify nodes without open predecessors. Priority propagation can identify which root
caused demand. Neither calculation identifies the longest remaining path without an estimate of
duration. Resource limits, actor capabilities, communication, and uncertain work lengths further
change which assignment produces the earliest completion.

HEFT is useful prior art because it explicitly schedules a known task graph on heterogeneous
processors using estimated computation and communication costs. It ranks tasks and assigns them to
processors using a heuristic. Its setting differs from agents discovering new tasks during
execution, but it makes the missing quantities visible.
[A09: Topcuoglu, Hariri, and Wu, 2002](https://disco.ethz.ch/courses/fs14/seminar/paper/Jochen/4.pdf)

Suppose a release needs a two-hour verification run and a five-minute policy clarification. Starting
the expensive run immediately may be best if the clarification cannot change its inputs. Resolving
the clarification first may be better if it could invalidate the run. The second choice is about
reducing uncertainty, not simply following the longest visible path. A scheduler needs to recognize
that information-gathering work can prevent wasted production.

**Recommendation:** begin with eligibility, explicit root priority, age, estimated cost, and actor
availability. Record the estimates and actual outcomes. Add a critical-path heuristic only when
measurements support useful estimates. Preserve a bounded share of capacity for older or lower
priority work if starvation is unacceptable. A stream of high-priority roots otherwise gives
ordinary work no completion guarantee.

Queueing theory offers a basic diagnostic. Little's relation connects average work in progress,
arrival rate, and average time in the system under the theorem's conditions. It is not a universal
causal promise that reducing any visible queue improves throughput.
[A10: Little, 1961](https://pubsonline.informs.org/doi/10.1287/opre.9.3.383)

**Inference:** dispatching more agents can increase work awaiting integration or human clarification
without increasing completed outcomes. Measure each queue separately: ready work, running work,
changes awaiting integration, and requests awaiting human decisions. The bottleneck may move when
one stage improves.

### Stopping is part of the outcome contract

Consider an outcome “users can export all their data.” Its children implement an endpoint and update
the documentation. Both changes land and pass their checks. The parent remains false if the endpoint
omits archived records. A completion receipt demonstrates the checks that ran; it does not repair an
incomplete definition of the outcome.

**Recommendation:** distinguish at least these conditions in the engine's internal model:

1. A prerequisite has reached a terminal administrative state.
2. Work is eligible to attempt.
3. An attempt produced evidence satisfying its declared acceptance procedure.
4. The desired condition is accepted as attained in a stated environment and scope.
5. The condition remains valid under current observations.

This need not mean five new frontmatter statuses. It means the scheduler and closure procedure must
not substitute one statement for another. In particular, an abandoned prerequisite should cause
parent reassessment, replacement of the plan, or abandonment of the parent. It should not count as
evidence that a required condition holds.

The [production sketch](../../.the-valley/ideas/ida-b48bded-production-dags-and-events.md) already
recognizes that service obligations do not terminate. Its claim that versioning turns feedback
cycles into acyclic artifact histories is useful, but acyclicity does not guarantee progress. An
infinite sequence of distinct revisions can remain stuck. Retries need stable logical identity
across runs, otherwise renaming a task erases its failure count.

**Recommendation:** measure progress toward acceptance, not graph growth or number of closed child
nodes. Bound total spend, attempt count, elapsed time, and decomposition. Cancellation should
withdraw the cancelled root's demand through the descendant graph. A shared prerequisite can remain
required by another live root. Lease expiry must not grant two workers simultaneous authority to
perform the same external effect; that requires enforcement at the effect boundary. Log enough of
each failed attempt to distinguish a transient environment failure from repeated misunderstanding.
Budget reservations must account atomically for concurrent descendants, retries, and restarted
controllers. Independent workers checking the same remaining balance can otherwise each authorize
spending that exceeds the shared limit.

## 6. Human attention is a resource with special constraints

The demand-pressure proposal treats a human-blocked node as work dispatched through notification.
That is useful for representing responsibility. It is insufficient as a model of attention. A human
cannot be cloned, paused, or restarted like a process. An interruption has costs beyond the seconds
spent reading it, and multiple notifications can concern the same underlying decision.

Bainbridge's _Ironies of Automation_ explains a central difficulty of supervision: automation may
leave humans responsible for exceptional intervention while depriving them of the practice and
ongoing involvement needed to intervene well. It is a conceptual analysis of industrial automation,
not a quantified study of coding-agent review. Its relevance is the failure mode it makes visible.
[A25: Bainbridge, 1983](https://gwern.net/doc/sociology/technology/1983-bainbridge.pdf)

Parasuraman, Sheridan, and Wickens distinguish automation of information acquisition, analysis,
decision selection, and action implementation. These functions can have different automation levels.
Their framework discourages treating autonomy as one switch that controls the whole system.
[A26: Parasuraman et al., 2000](https://doi.org/10.1109/3468.844354)

**Inference:** the-valley can automate evidence collection and execution while retaining human
judgment over the meaning of a release criterion. It can automatically summarize incidents while
requiring a separate act to revise the project's threat model. These boundaries may vary by outcome
class. “Human in the loop” is too vague to describe them.

Horvitz's mixed-initiative work explicitly considers uncertainty about user goals, the timing of
interruptions, expected costs and benefits, clarification, and easy invocation or termination of
automation. It offers a foundation for deciding when assistance should act or ask, illustrated with
a scheduling interface rather than software production.
[A27: Horvitz, 1999](https://www.microsoft.com/en-us/research/wp-content/uploads/2016/11/chi99horvitz.pdf)

**Recommendation:** route a decision request with the exact choice, relevant evidence, consequence
of waiting, and action that follows each answer. Combine requests that depend on the same decision.
Separate urgent intervention from a digest of completed work. Measure whether the recipient can make
the decision from the supplied context, not merely whether a notification was delivered.

For example, five agents blocked on a signing-policy change should generate one coherent policy
decision with five affected outcomes. Five independent urgent messages would turn graph structure
into interruption amplification. Likewise, an agent's third failed integration should appear as one
recurring failure with its history, not another apparently new proposal.

The [architecture's review-as-feedback bet](../../design/architecture.md) becomes defensible when
paired with evidence about detection delay and consequence. A formatting change and a modification
to the authority boundary need different review timing. The useful comparison is between concrete
policies: pre-integration review, sampled post-integration review, targeted review of uncertain
changes, and automatic acceptance with later incident analysis. The report does not assume one
policy wins for every change class.

**Hypothesis:** a small, calibrated stream of decision requests and sampled completed changes will
preserve understanding better than either reviewing everything superficially or seeing only
failures. A trial should measure missed defects, review time, interruption count, and the operator's
ability to explain current system behavior after several weeks.

## 7. Evaluation should measure useful work and preserved understanding

### What existing benchmarks establish

SWE-bench turns historical repository issues into executable patching tasks. It evaluates whether
candidate changes satisfy tests associated with issue resolution while preserving specified existing
behavior. This is substantially closer to repository work than isolated function generation. It
still does not measure all of specification discovery, architectural coherence, deployment, or
maintenance over successive changes.
[A19: Jimenez et al., 2023/2024](https://arxiv.org/html/2310.06770v3)

SWE-bench-Live develops an updatable collection of issue-resolution tasks and automated environment
construction. Fresh tasks and broader repositories help address staleness and some leakage risks.
Recency does not establish absence of training exposure, nor does it make test-based acceptance
equivalent to complete issue resolution.
[A20: Zhang et al., 2025](https://arxiv.org/html/2505.23419v1)

Oren and colleagues demonstrate a statistical contamination test based on the likelihood of
canonical versus shuffled benchmark orderings. Its assumptions and access requirements matter.
Evidence from a particular detector cannot be generalized into proof that every undetected benchmark
is clean. [A21: Oren et al., 2023](https://arxiv.org/html/2310.17623v1)

_AI Agents That Matter_ argues for jointly evaluating cost and accuracy, adequate holdouts, simple
baselines, and reproducible evaluation procedures. Its empirical examples show that apparent gains
from elaborate agent designs can be misleading when spending and retries are not controlled. That
critique is especially relevant to a scheduler whose normal response to unfinished work is another
attempt. [A22: Kapoor et al., 2024](https://arxiv.org/html/2407.01502v1)

The practical productivity evidence is also conditional. METR's early-2025 randomized study found a
19% increase in completion time when 16 experienced maintainers could use the studied AI tools
across 246 tasks. That result describes those tools, maintainers, and tasks; it is not a general
estimate of present agent productivity.
[A23: Becker et al., 2025](https://metr.org/Early_2025_AI_Experienced_OS_Devs_Study-paper.pdf)

METR's February 2026 update reports that selection into its later experiment and measurement of time
under concurrent agent use made the later estimates unreliable. The organization argues that the
study design needs revision. Reporting the earlier slowdown without this update would invite a
misleading current conclusion.
[A24: Becker et al., 2026](https://metr.org/blog/2026-02-24-uplift-update/)

**Assessment:** the-valley should learn experimental discipline from these sources without choosing
one headline score as its objective. The system exists to produce outcomes while preserving control
and understanding. Patch success, operator time, latency, expenditure, and later rework are separate
measurements.

### A theory-reconstruction experiment

The strongest near-term experiment uses a fixed repository snapshot and tasks that require applying
its design to new situations. The task set should include valid modifications, plausible changes
that violate a principle, and questions the existing record cannot answer. Avoid tasks copied from
documents that already contain their solutions.

Compare four knowledge conditions while holding the model, harness, tools, and budget constant:

| Condition                             | What the contributor receives                                         | What the comparison tests                                            |
| ------------------------------------- | --------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Code and ordinary documentation       | The repository without the knowledge graph                            | Value added by curated project knowledge                             |
| Current graph                         | Code, documents, and the current typed nodes                          | Benefit of the existing convention                                   |
| Graph with justification and evidence | The same material plus explicit premises and source links             | Whether additional structure improves application rather than recall |
| Retrieved briefing                    | A bounded briefing derived from the same snapshot, with source access | Whether context selection preserves understanding cheaply            |

Add a matched-information comparison that presents identical facts and rationale as ordinary prose
and as typed nodes. This separates the value of the information from the value of graph structure.
Keep held-out tasks independent of wording and examples unique to the graph.

Score at least factual recall, validity of proposed modifications, identification of missing
assumptions, justified abstention, and ability to explain tradeoffs. Ask blinded human reviewers to
judge a sample using a rubric established before seeing outputs. A model judge can extend coverage
after calibration, but agreement with one favored model is not a definition of design quality.

Measure tokens and time until a satisfactory solution, including retrieval and unsuccessful
attempts. Count later corrections. Give multiple independent runs each task and report uncertainty
around differences. Publish representative failure cases, not just a mean score. A knowledge
condition that raises recall but produces more overconfident invalid changes should not win.

Then reverse the experiment: hold the model fixed and compare graph versions. Use held-out tasks so
that editing the graph to fix observed failures does not simply teach it the test answers. Separate
improvements in the graph from changes in prompts, tools, and model versions. This makes the
repository's two-sided evaluation proposal concrete.

### Completion claims need adversarial tests

A second evaluation should attack the outcome engine's semantics. Construct small graphs where a
blocker is abandoned, evidence expires, an OR alternative succeeds, a parent is cancelled during a
child run, or a worker loses its lease before returning. Include mutually dependent outcomes and
repeated revisions that never make substantive progress.

The expected behavior should be stated independently of the implementation. For example, abandoned
work must not count as fulfilled evidence; cancellation must stop work justified only by the
cancelled root while preserving shared prerequisites still demanded elsewhere; stale workers must
not duplicate external effects; and a cycle must produce a diagnostic rather than silent inactivity.
Compare the proposed automatic engine with a simple manual ready-work query. The engine must add
useful progress, not merely activity.

For scheduling policies, replay representative arrivals and measured task durations in simulation
before spending model calls. Compare priority-only dispatch, priority with aging, estimated
critical-path dispatch, and a policy that resolves high-impact uncertainty first. Report root
completion time, worst waiting time, total spend, discarded work, and human decision load. The best
policy will depend on workload; finding those boundaries is a useful result.

### Reproducibility has limits that should be explicit

The current
[agent-provenance proposal](../../.the-valley/ideas/ida-45178f6-agent-identity-is-provenance.md)
already notes that a hosted model is an impure service boundary. The useful experiment record
includes the repository tree, graph snapshot, prompt and retrieved context, tool versions, tool
results, model identifier, budgets, and acceptance procedure. A digest of withheld material proves a
commitment to bytes; it does not make those bytes available for independent replay.

**Recommendation:** distinguish rerunning a workflow, reproducing its environment, and reproducing
its exact output. Even known model weights do not by themselves fix sampling, numerical execution,
or changing external observations. Record what is controlled and what is merely identified.

An observed tool transcript is evidence of externally visible actions. An agent's explanatory prose
is a claim about its process. Neither is direct access to all internal reasoning. Evaluation should
prefer interventions: remove a premise, change a tool response, or vary the available evidence and
observe the resulting behavior. This can test dependence on information without pretending to read
the model's mind.

Finally, maintain a prospective task set whose solutions do not exist at task creation. Record when
tasks become public and whether providers may receive them. Reserve some tasks and judging criteria
from normal graph maintenance. Public task freshness, controlled access, and documented exposure
reduce different forms of leakage; none should be described as perfect decontamination.

## 8. The most promising research direction

**Assessment:** the-valley's distinctive opportunity is to connect governed project knowledge with
evidence about completed work. Repo-local notes, agent memory, task graphs, workflow execution, and
provenance each have substantial prior art. The interesting question is whether their combination
can make project understanding cheaper to reconstruct while making delegated work easier to inspect
and correct.

The strongest claim to pursue is therefore conditional and measurable: a fresh contributor using the
project's current record can make an unfamiliar change with fewer conceptual mistakes, at lower
total recovery cost, while preserving an inspectable chain from intent to accepted result. That
claim would justify the substrate even if a fully general outcome-production engine remains distant.

Four investigations can discriminate among the plausible designs:

1. **Knowledge ablation:** determine whether the present graph improves unseen modifications over
   ordinary repository documentation. If it does not, investigate content and retrieval before
   adding more node types.
2. **Completion semantics:** implement an executable model of readiness, acceptance, invalidation,
   abandonment, and cancellation. Use counterexamples to determine which distinctions the public
   schema actually needs.
3. **Operational load:** measure realistic graph mutation and dispatch workloads. Compare files,
   derived indexes, and a transactional backend on conflict rate, recovery, and operator effort.
4. **Attention trial:** run bounded automation for several weeks and measure the work needed to
   maintain understanding. Compare review and notification policies by defects caught and human
   time, not volume of generated summaries.

**Recommendation:** prioritize the first two before a rich autonomous scheduler. Better scheduling
cannot rescue an engine that mistakes an administrative transition for an attained outcome. More
memory cannot rescue an authoritative record that omits the assumptions needed to interpret it. The
research should reward useful negative results: a simpler query, a shorter briefing, or a human
decision at the right boundary may outperform a more elaborate agent loop.
