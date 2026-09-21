# Annotated sources: knowledge, agents, and outcomes

This register supports [Chapter 5](05-knowledge-agents-and-outcomes.md). All links were checked on
2026-09-20. “Selected sections” means the named portions were read; it does not mean the entire
paper or its proofs were reviewed. Abstract-only entries support correspondingly narrow
descriptions. Living documentation establishes what a project documents, not an independently
verified performance claim. Limitations and proposed applications below are this report's
assessments unless attributed.

## A01 — Programming as Theory Building

Peter Naur, 1985. _Microprocessing and Microprogramming_.
[Paper, in a later reprint](https://gwern.net/doc/cs/algorithm/1985-naur.pdf);
[university-hosted scan](https://pages.cs.wisc.edu/~remzi/Naur.pdf).

**Read:** selected sections of the reprint: introduction, modification, and program life, death, and
revival. **Contribution:** a sharp account of understanding that exceeds code and documentation.
**Limit:** conceptual argument and historical examples, not evidence about LLM-based reconstruction.
**Relevance:** the strongest challenge to the graph's theory-revival ambition. Read before designing
its evaluation; success should mean useful new work, not reproducing an allegedly complete mental
state from text.

## A02 — gIBIS: A Hypertext Tool for Exploratory Policy Discussion

Jeff Conklin and Michael L. Begeman, 1988. _ACM CSCW_, 140–152.
[DOI](https://doi.org/10.1145/62266.62278);
[author-uploaded record and abstract](https://www.researchgate.net/publication/220515641_gIBIS_A_Hypertext_Tool_for_Exploratory_Policy_Discussion).

**Read:** abstract only; full PDF retrieval was unsuccessful. **Contribution:** collaborative typed
hypertext supporting design deliberation through IBIS. **Limit:** no detailed usability or empirical
claim is derived from this limited reading. **Relevance:** establishes that structured rationale
capture substantially predates agent memory. The precursor is Kunz and Rittel's 1970 _Issues as
Elements of Information Systems_
([institutional record](https://escholarship.org/uc/item/5cj786v8)); that precursor's bibliographic
record was checked, but its scanned body was not successfully read.

## A03 — A Truth Maintenance System

Jon Doyle, 1979. MIT AI Memo 521; journal version in _Artificial Intelligence_.
[Institutional record and abstract](https://dspace.mit.edu/entities/publication/5377b306-4ecc-4687-b1f5-78cbb4a0543a).

**Read:** abstract only. **Contribution:** maintaining reasons for beliefs and revising assumptions
when they conflict with discoveries. **Limit:** this review does not establish the details or
complexity of the TMS algorithms. **Relevance:** motivates separating logical support from task
precedence. A design's invalidated premise should trigger reconsideration, not be interpreted as a
completed work dependency.

## A04 — PROV-DM: The PROV Data Model

Luc Moreau and Paolo Missier, editors, 2013. W3C Recommendation, 30 April 2013.
[Specification](https://www.w3.org/TR/prov-dm/).

**Read:** introduction and overview. **Contribution:** an interoperable provenance vocabulary.
**Limit:** representation alone does not establish truth. **Relevance:** compare run and evidence
records with existing concepts before defining new ones.

## A05 — git-bug

git-bug contributors. Living official repository documentation; accessed 2026-09-20.
[Repository and README](https://github.com/git-bug/git-bug).

**Read:** README overview, workflows, and development/feature pointers. **Contribution:** a
distributed tracker embedded in Git, usable offline and through multiple interfaces and bridges.
**Limit:** no installation, bridge round trip, concurrency test, or performance measurement was
performed for this report. **Relevance:** demonstrates an alternative to checked-out Markdown nodes.
The-valley's authorization and atomic-publication requirements would need separate comparison with
git-bug's storage and synchronization protocol.

## A06 — Beads

Beads contributors, associated with Steve Yegge's agent-tooling work. Living official documentation;
accessed 2026-09-20. [Repository and README](https://github.com/gastownhall/beads).

**Read:** README features, commands, storage modes, synchronization, and Git-free usage.
**Contribution:** dependency-aware work, ready queries, atomic claiming, and persistent memory over
Dolt. **Limit:** fast-moving documentation; no independent concurrency or durability test here.
Marketing phrases such as conflict elimination are not adopted as findings. **Relevance:** the
nearest tool-level comparison for the outcome dispatch interface. Its separate database history
requires analysis of publication semantics, not a presumption that files are inherently safer.

## A07 — Goal-Oriented Requirements Engineering: A Guided Tour

Axel van Lamsweerde, 2001. _IEEE International Symposium on Requirements Engineering_.
[Author-hosted paper](https://webperso.info.ucl.ac.be/~avl/files/RE01.pdf).

**Read:** introduction and sections 2–4 on goals, goal links, and specification. **Contribution:**
AND/OR refinement, responsibilities, environmental assumptions, and temporal goal classes, including
KAOS. **Limit:** a survey and methodological account; it does not demonstrate the cost of
formalizing the-valley's workload. **Relevance:** clarifies what an outcome means, whether children
suffice, and why “attain once” differs from “maintain continuously.” Start here before expanding
edge types.

## A08 — HTN Planning: Complexity and Expressivity

Kutluhan Erol, James Hendler, and Dana S. Nau, 1994. _AAAI_.
[Publisher-hosted paper](https://cdn.aaai.org/AAAI/1994/AAAI94-173.pdf).

**Read:** abstract, introduction, and initial planning overview; proofs not reviewed.
**Contribution:** formal distinctions and complexity results for hierarchical task decomposition.
**Limit:** the results depend on precise formal restrictions and cannot be transferred directly to
small informal task graphs. **Relevance:** shows why choosing a decomposition is a planning problem
distinct from dispatching known ready tasks. Its vocabulary helps specify which recursion and
ordering restrictions an initial outcome engine accepts.

## A09 — Performance-Effective and Low-Complexity Task Scheduling for Heterogeneous Computing

Haluk Topcuoglu, Salim Hariri, and Min-You Wu, 2002. _IEEE Transactions on Parallel and Distributed
Systems_. [Paper](https://disco.ethz.ch/courses/fs14/seminar/paper/Jochen/4.pdf);
[DOI](https://doi.org/10.1109/71.993206).

**Read:** abstract and introductory algorithm description; detailed experiments not reviewed.
**Contribution:** HEFT and CPOP heuristics for mapping known task graphs to heterogeneous
processors. **Limit:** estimated costs and a pre-existing graph differ from uncertain agent
decomposition. **Relevance:** a concrete baseline for discussions of critical-path scheduling.
Topology, execution costs, resource assignment, and communication all matter; root priority alone is
insufficient.

## A10 — A Proof for the Queuing Formula: L = λW

John D. C. Little, 1961. _Operations Research_ 9(3), 383–387.
[Publisher record and abstract](https://pubsonline.informs.org/doi/10.1287/opre.9.3.383).

**Read:** abstract and theorem conditions; proof not reviewed. **Contribution:** connects average
population, arrival rate, and residence time under stated conditions. **Limit:** not a universal
causal rule for an unstable or changing workflow. **Relevance:** encourages measuring queues and
end-to-end delay instead of counting concurrent agents as productivity. Separate integration and
human-decision queues can reveal bottlenecks hidden by a single completion-rate metric.

## A11 — MemGPT: Towards LLMs as Operating Systems

Charles Packer et al., 2023; revised 2024. arXiv:2310.08560.
[Version 2 full text](https://arxiv.org/html/2310.08560v2).

**Read:** introduction, architecture, and selected conversational/document experiments in sections
2–3. **Contribution:** explicit working and external memory with model-directed memory operations.
**Limit:** task-specific evaluations and dependence on retrieval and tool-use ability; virtual
memory is an analogy rather than a correctness guarantee. **Relevance:** illustrates how disposable
agent context can be derived from durable records. The system still needs independent rules for the
authority and promotion of stored claims.

## A12 — Generative Agents: Interactive Simulacra of Human Behavior

Joon Sung Park et al., 2023. _ACM UIST_; arXiv:2304.03442.
[Version 2 full text](https://arxiv.org/html/2304.03442v2).

**Read:** abstract, introduction, and selected end-to-end evaluation in section 7. **Contribution:**
a combined experience-memory, reflection, planning, and retrieval architecture, evaluated in a
simulated community. **Limit:** believability and short simulated social interactions do not
establish reliable engineering outcomes. **Relevance:** architectural inspiration for memory loops,
with an important warning to match evaluation criteria to the intended use. A coherent story about
behavior is not the same result as verified task success.

## A13 — Reflexion: Language Agents with Verbal Reinforcement Learning

Noah Shinn et al., 2023. _NeurIPS_; arXiv:2303.11366.
[Version 4 full text](https://arxiv.org/html/2303.11366v4).

**Read:** abstract, introduction, selected experiment/ablation passages, and limitations.
**Contribution:** textual feedback from attempts retained for subsequent trials without weight
updates. **Limit:** feedback quality, retry budgets, test specification, and local optimization
affect the result. **Relevance:** informs incident-to-memory and retry loops. Store an agent's
reflective explanation as a hypothesis until observation supports it, and compare with simpler retry
baselines under the same total budget.

## A14 — Lost in the Middle: How Language Models Use Long Contexts

Nelson F. Liu et al., 2023 preprint; 2024 _TACL_. arXiv:2307.03172.
[Version 3 full text](https://arxiv.org/html/2307.03172v3).

**Read:** introduction and selected methods/results in sections 2–4. **Contribution:** controlled
position tests reveal uneven use of long contexts in the evaluated models. **Limit:** measured
magnitudes belong to those models and tasks, not every later model. **Relevance:** provides a robust
evaluation pattern for generated project briefings. Vary placement, irrelevant material, and context
budget while keeping needed facts fixed.

## A15 — LongMemEval: Benchmarking Chat Assistants on Long-Term Interactive Memory

Di Wu, Hongwei Wang, Wenhao Yu, Yuwei Zhang, Kai-Wei Chang, and Dong Yu, 2024 preprint; 2025 _ICLR_.
[Version 1 full text](https://arxiv.org/html/2410.10813v1).

**Read:** introduction, memory-ability definitions, and selected evaluator/commercial-system
details. **Contribution:** evaluates temporal change, cross-session information, updates, and
abstention as well as recall. **Limit:** constructed conversational histories differ from changing
source-code projects. **Relevance:** supplies dimensions for testing current-versus-historical
project knowledge and justified uncertainty. A single aggregate retrieval score would conceal these
failure modes.

## A16 — LongMemEval-V2: Evaluating Long-Term Agent Memory Toward Experienced Colleagues

Di Wu et al., May 2026. Preprint, arXiv:2605.12493.
[Version 1 full text](https://arxiv.org/html/2605.12493v1).

**Read:** abstract, introduction, benchmark comparison, and context-gathering formulation.
**Contribution:** environment-specific memory questions about state, workflows, pitfalls, and
premise awareness; bounded retrieved evidence is evaluated through a fixed reader. **Limit:** recent
preprint; web-environment question answering leaves engineering transfer unproven. **Relevance:** a
promising way to isolate briefing quality from downstream model ability. Treat as an experimental
design to adapt, not a ready-made measure of the-valley's theory reconstruction.

## A17 — Retrieval-Augmented Generation for Knowledge-Intensive NLP Tasks

Patrick Lewis et al., 2020. _NeurIPS_. arXiv:2005.11401.
[Paper record and abstract](https://arxiv.org/abs/2005.11401).

**Read:** abstract only. **Contribution:** combines learned document retrieval with generation for
knowledge-intensive language tasks. **Limit:** this reading supports the broad mechanism, not a
claim that its architecture is best for current repository search. **Relevance:** foundational
retrieval prior art. the-valley additionally needs version, authority, and evidence semantics that
retrieval relevance alone does not supply.

## A18 — SWE-agent: Agent-Computer Interfaces Enable Automated Software Engineering

John Yang et al., 2024. _NeurIPS_. arXiv:2405.15793.
[Version 3 full text](https://arxiv.org/html/2405.15793v3).

**Read:** introduction, interface-design principles, and selected ablation descriptions.
**Contribution:** treats repository tools and their feedback as design variables for coding agents.
**Limit:** specific harnesses, models, and benchmark versions constrain the empirical result.
**Relevance:** supports testing small graph query and evidence-navigation tools. Comparisons of
model ability must hold the tool interface constant or explicitly report its contribution.

## A19 — SWE-bench: Can Language Models Resolve Real-World GitHub Issues?

Carlos E. Jimenez et al., 2023 preprint; 2024 _ICLR_. arXiv:2310.06770.
[Version 3 full text](https://arxiv.org/html/2310.06770v3).

**Read:** abstract, discussion/limitations, task fields, and Appendix A evaluation procedure.
**Contribution:** executable repository issue-resolution tasks assessed with designated failing and
regression tests. **Limit:** original Python scope and incomplete test oracles; the paper itself
notes qualities that execution-based evaluation misses. **Relevance:** a useful patching baseline,
but the-valley needs additional measures of architectural fit, completion semantics, and later
maintenance costs.

## A20 — SWE-bench Goes Live!

Linghao Zhang et al., 2025. arXiv:2505.23419.
[Version 1 full text](https://arxiv.org/html/2505.23419v1).

**Read:** abstract, introduction, and limitations appendix. **Contribution:** a live-updatable task
collection and automated execution-environment construction. **Limit:** the reviewed version focuses
on Python and reports no repeated full experiments; fresh tasks are not proof of zero exposure.
**Relevance:** motivates prospective task collection and recorded environment snapshots. Keep task
freshness, repository diversity, and oracle completeness as separate evaluation properties.

## A21 — Proving Test Set Contamination in Black Box Language Models

Yonatan Oren, Nicole Meister, Niladri Chatterji, Faisal Ladhak, and Tatsunori B. Hashimoto, 2023.
arXiv:2310.17623. [Version 1](https://arxiv.org/html/2310.17623v1);
[latest paper record](https://arxiv.org/abs/2310.17623).

**Read:** abstract and problem statement; proofs not reviewed. **Contribution:** a statistical test
using canonical versus shuffled benchmark order likelihoods. **Limit:** requires the method's
assumptions and likelihood access; non-detection is not a general certificate of clean data.
**Relevance:** disciplines claims about leakage. the-valley should document task exposure and retain
prospective holdouts instead of labeling a benchmark uncontaminated without a defined argument.

## A22 — AI Agents That Matter

Sayash Kapoor, Benedikt Stroebl, Zachary S. Siegel, Nitya Nadgir, and Arvind Narayanan, 2024.
arXiv:2407.01502. [Version 1 full text](https://arxiv.org/html/2407.01502v1).

**Read:** introduction, cost-controlled evaluation discussion, and simple-baseline results.
**Contribution:** joint accuracy/cost evaluation, holdout design, and reproducibility criticism.
**Limit:** its empirical comparisons concern particular benchmarks and cannot rank every current
agent design. **Relevance:** a methodological priority for testing repeated dispatch and multi-step
memory systems. Compare total spend and operator work, and include simple retries or escalation
before attributing gains to architectural sophistication.

## A23 — Measuring the Impact of Early-2025 AI on Experienced Open-Source Developer Productivity

Joel Becker, Nate Rush, Beth Barnes, and David Rein, 2025. METR research report.
[Paper](https://metr.org/Early_2025_AI_Experienced_OS_Devs_Study-paper.pdf).

**Read:** abstract and introductory study design/results. **Contribution:** a randomized field
comparison on experienced maintainers' own tasks, including a mismatch between perceived and
measured speedup. **Limit:** specific participants, tasks, period, and tools; no universal
productivity effect follows. **Relevance:** actual completion time and later rework matter more than
confidence or generated output. Read with A24 before applying the result to current agent workflows.

## A24 — We are Changing our Developer Productivity Experiment Design

Joel Becker, Nate Rush, Tom Cunningham, David Rein, and Khalid Mahamud, 24 February 2026. METR.
[Research update](https://metr.org/blog/2026-02-24-uplift-update/).

**Read:** findings, selection effects, time-measurement problems, and proposed design changes.
**Contribution:** explains why later participant/task selection and concurrent agent use undermine a
simple continuation of the earlier estimate. **Limit:** an update diagnosing measurement problems,
not a definitive current uplift estimate. **Relevance:** the-valley should record task-selection and
abandonment effects. Parallel-agent elapsed time and human effort require separate accounting.

## A25 — Ironies of Automation

Lisanne Bainbridge, 1983. _Automatica_.
[Paper](https://gwern.net/doc/sociology/technology/1983-bainbridge.pdf).

**Read:** introduction and selected monitoring, intervention, and operator-skill passages.
**Contribution:** explains why residual human supervision can become harder as routine operation is
automated. **Limit:** industrial automation analysis, not a direct experiment on software agents.
**Relevance:** frames the risk that reviewers lose context while remaining responsible for rare
failures. Attention and retained understanding should be outcomes of the workflow evaluation.

## A26 — A Model for Types and Levels of Human Interaction with Automation

Raja Parasuraman, Thomas B. Sheridan, and Christopher D. Wickens, 2000. _IEEE Transactions on
Systems, Man, and Cybernetics, Part A_. [DOI](https://doi.org/10.1109/3468.844354);
[accessible paper transcription](https://paperzz.com/doc/9452159/a-model-for-types-and-levels-of-human-interaction-with).

**Read:** abstract, introduction, and initial types/levels framework. **Contribution:**
differentiates automation across information, analysis, decision, and action functions. **Limit:** a
general framework does not select appropriate boundaries for this repository. **Relevance:** specify
what a human retains control over rather than choosing a single autonomy level for the whole
pipeline.

## A27 — Principles of Mixed-Initiative User Interfaces

Eric Horvitz, 1999. _ACM CHI_.
[Author-organization-hosted paper](https://www.microsoft.com/en-us/research/wp-content/uploads/2016/11/chi99horvitz.pdf).

**Read:** principles and initial LookOut decision-making discussion. **Contribution:** reasons about
uncertainty, attention, interruption costs, clarification, and reversible interaction with
automation. **Limit:** illustrated through calendaring assistance, with no direct result on
agent-code review. **Relevance:** informs decision-request routing, escalation, and the value of
asking at the right time. Measure whether a request resolves uncertainty efficiently, rather than
counting delivery as successful human participation.

## A28 — On the acceptability of arguments and its fundamental role in nonmonotonic reasoning, logic programming and n-person games

Phan Minh Dung, 1995. _Artificial Intelligence_ 77, 321–357.
[Paper](https://cse-robotics.engr.tamu.edu/dshell/cs631/papers/dung95acceptability.pdf).

**Read:** introduction and section 2 definitions of attack, acceptability, and extension semantics.
**Contribution:** a formal account of which arguments can be accepted together given their attacks.
**Limit:** abstract argumentation does not validate factual premises or extract sound arguments from
prose. **Relevance:** separates disagreement from work precedence. A useful graph may contain cyclic
argument relationships while requiring acyclic execution dependencies; one graph does not imply one
relation or one acceptance rule.
