# 2. Existing systems and the alternatives they make credible

Most individual mechanisms proposed by the-valley have substantial prior art. These systems are
candidates to adopt or configure. The selection question is whether they satisfy the requirements
and preserve the chosen guarantees when combined. The relevant comparison concerns complete
workflows, including the work needed to connect, operate, and replace their components.

This chapter compares documented mechanisms. It does not report hands-on product benchmarks. System
documentation was consulted on 2026-09-20; the [source register](sources-systems.md) records
versions and reading depth. Evaluations of suitability are this report's judgments.

## 2.1 A map of the alternatives

| Family                      | Systems worth understanding                      | Problem substantially addressed                                  | Question still belonging to the-valley                                                   |
| --------------------------- | ------------------------------------------------ | ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Self-operated forge         | Forgejo, SourceHut                               | Hosting control and established collaboration                    | Does replacing the collaboration model provide enough benefit to repay integration work? |
| Distributed collaboration   | Radicle, Fossil, Tangled                         | Keeping collaboration independent of a single conventional forge | Which state and authority must travel with a project?                                    |
| Change-oriented review      | Gerrit; email with b4                            | Separating a proposed change from an ordinary branch             | What is the stable identity of a change and its evidence?                                |
| Concurrent gating           | Zuul, GitHub merge queues, GitLab merge trains   | Testing combined changes while maintaining merge order           | Can evidence transfer reduce repeated work without weakening admission?                  |
| Portable execution          | Nix, Bazel, Dagger                               | Sharing execution definitions and reusing results                | Who is authorized to assert that a particular result occurred?                           |
| Version-control experiments | Jujutsu, Pijul                                   | Recording operations and treating changes more explicitly        | Which ideas can improve authoring without replacing the durable protocol?                |
| Durable reactions           | Kubernetes controllers, Temporal                 | Reconciliation and recovery of long-running work                 | What remains the system of record for effects and decisions?                             |
| Project memory and agents   | git-bug, Beads, memory systems, agent frameworks | Persistent coordination and context                              | Which knowledge is authoritative, and how does it improve attained outcomes?             |

The last two rows receive detailed treatment in chapters [4](04-events-trust-and-durability.md) and
[5](05-knowledge-agents-and-outcomes.md). Nix's foundations and evidence systems receive detailed
treatment in [chapter 3](03-verification-and-integration.md).

## 2.2 Sovereignty has several implementations

**Forgejo is the practical baseline for operating an ordinary forge.** Its documented administration
includes exporting files and databases and restoring repository units such as issues, pull requests,
comments, and releases. This shows that owning the hosting and recovering collaboration data need
not require a new source-management architecture. It also shows why a Git clone alone is a narrower
recovery artifact than the complete service.
[S05: Forgejo CLI](https://forgejo.org/docs/v15.0/admin/command-line/)

The implication is a burden of proof for the-valley. If the immediate problem is control of hosting,
an established forge plus tested backups deserves an actual place in the decision set. A new
substrate becomes compelling when portability of decisions or changes to authoring and integration
matter enough to justify its additional responsibilities.

**SourceHut demonstrates that unbundling can retain familiar collaboration.** Its project hub was
designed to organize independently composed repositories, lists, and trackers, with no fixed
one-repository-to-one-tracker relationship. That is direct prior art for separating concerns while
still offering a coherent project surface.
[S06: SourceHut project hub](https://sourcehut.org/blog/2020-04-30-the-sourcehut-hub-is-live/)

The lesson is architectural rather than aesthetic. A common interface does not require one
authoritative object model, and separate components do not require an incoherent experience.
the-valley's derived views could follow this principle. Their replacement cost should be low even
when the daily interface is cohesive.

**Fossil provides the strongest compact alternative to the assumption that minimalism means
unbundling.** It combines version control with project facilities in a self-contained executable.
Its own comparison describes cloning the project's public history and associated material together.
This is a project-authored comparison, so its favorable usability judgments are not independent
measurements. [S04: Fossil versus Git](https://fossil-scm.org/home/doc/trunk/www/fossil-v-git.wiki)

For the-valley, Fossil asks an uncomfortable but productive question: is the unit to minimize a
component's code, the number of deployment units, or the operator's total burden? Those objectives
can conflict. A distributed set of small tools may need more backup procedures and upgrade
coordination than an integrated program. The report recommends measuring operating burden instead of
treating composition itself as evidence of simplicity.

**Radicle is the closest prior art for portable, authenticated collaboration over Git.** Its
protocol stores issues, patches, and identity changes as collaborative objects. Peers replicate
signed data, and repository delegates determine canonical state. Its object graphs also represent
concurrent operations. [S03: Radicle protocol](https://radicle.dev/guides/protocol)

The important distinction is that replication and acceptance are separate. A valley could receive a
proposal through such a transport while retaining its own evidence policy. Adopting a transport need
not entail adopting every review convention. Conversely, designing another Git namespace for social
objects deserves justification against this existing model. Peer availability, retention, and the
meaning of canonical state still need operational evaluation; the protocol description is not a
restore test.

**Tangled is a particularly relevant adjacent design.** Its documentation separates Git hosting
servers, called knots, from a network view, using AT Protocol for the wider collaboration system.
[S08: Tangled overview](https://docs.tangled.org/) Its spindle component adds workflow execution
with Nixery and microVM engines, while retaining a YAML pipeline format.
[S09: Tangled spindles](https://docs.tangled.org/spindles)

This combination makes Tangled useful for comparison even though its current workflow choices differ
from the-valley's. Decentralized hosting, derived views, and Nix-related execution can coexist
without requiring local evidence to authorize integration. The comparison isolates the-valley's
additional bet: whether an evidence protocol is a better boundary than a service reporting pipeline
status. No interoperability between these systems has been tested in this study.

## 2.3 A pull request is one possible interface to a change

**Gerrit already distinguishes a change from the commits that revise it.** Within a repository and
target branch, a stable Change-Id groups patch sets across revisions. The review record can
therefore follow a logical proposal through amended commits. Gerrit also distinguishes the author,
uploader, reviewers, and submit strategy.
[S10: Gerrit changes](https://gerrit-review.googlesource.com/Documentation/concept-changes.html)

This matters because the-valley's tree identity and a user's concept of "this change" have different
jobs. A tree digest identifies content. A proposal identity preserves a conversation across content
changes. An integration attempt identifies a decision about particular content under particular
policy. Eliminating the PR object does not eliminate these three relationships.

**Gerrit's submit requirements are direct precedent for policy stored with a project.** Requirements
are expressed in project configuration and can apply to changes to that configuration. The docs
explicitly discuss locking out future policy changes through misconfiguration and the recovery paths
available to administrators.
[S11: Submit requirements](https://gerrit-review.googlesource.com/Documentation/config-submit-requirements.html)

The research implication is that recursive governance is a familiar systems problem with practical
failure modes. the-valley adds stronger ambitions for signed decisions and portable history, but it
should learn from systems that already have to recover from rules that prevent their own amendment.

**Email-based contribution separates submission from repository write permission.** b4 retrieves
patch series by message identity, checks attestations, and assembles patches and review trailers for
maintainers. This is a working precedent for proposals arriving through a transport the maintainer
does not equate with authority to update the accepted branch.
[S07: b4 patch retrieval](https://b4.docs.kernel.org/en/latest/maintainer/am-shazam.html)

The relevant lesson is not that email should be the new interface. It is that transport
independence, attribution, review history, and a maintainer's final decision have long been
separable. A valley can borrow that separation while providing a more structured machine interface.

The difficult design work moves to continuity. After a proposal is rewritten, which feedback remains
relevant? Which approvals must be renewed? Can a reader distinguish approval of an intent from
approval of one exact patch? A derived review page still needs explicit answers in the underlying
records. These are semantic requirements, regardless of whether the interface resembles a PR.

## 2.4 Concurrent integration is an established field

The simple model "remote CI runs serially before every merge" is an inadequate baseline.

**Zuul tests speculative combined states concurrently.** If changes A, B, and C are queued, it can
test A, A+B, and A+B+C in parallel. Failure invalidates affected speculative successors. It also
supports dependencies and shared queues across projects.
[S12: Zuul gating](https://zuul-ci.org/docs/zuul/latest/gating.html)

the-valley instead asks when a check result obtained on one state can apply to another without
re-execution. These approaches are compatible: speculation chooses future states to examine, while
evidence transfer decides which computation is already valid for a chosen state. A comparison should
measure both strategies on independent edits, shared dependencies, frequent failures, and highly
contended files. A simple serialized gate is still useful as a baseline, but it is not the strongest
existing alternative.

**GitHub's merge queue verifies temporary combined states and exposes build concurrency.** Its
documentation explains how merge-group events drive checks and how queue reordering can trigger new
builds.
[S13: GitHub merge queues](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue)
**GitLab's merge trains likewise run pipelines for queued combinations and remove failed changes
from the train.** [S14: GitLab merge trains](https://docs.gitlab.com/ci/pipelines/merge_trains/)

Those mechanisms are existing solutions to concurrent checking with ordered integration. The next
question is whether an available configuration meets the project's evidence-reuse and portability
requirements. A custom transfer rule needs a demonstrated gap in those solutions. A second executor
and integrator agreeing on the same record would test the portability requirement, regardless of
which project supplied the rule.

There is also earlier research on detecting conflicts before submission. Brun, Holmes, Ernst, and
Notkin's Crystal work distinguishes textual conflicts from build and test conflicts. It explores
speculative version-control operations to warn collaborators early.
[S20: Proactive Detection of Collaboration Conflicts](https://people.cs.umass.edu/~brun/pubs/pubs/Brun11fse.pdf)

The implication for agents is to investigate coordination before the integration boundary as well as
at it. A warning that another worker is changing the same abstraction can prevent wasted work even
when a merge queue would eventually catch an interaction covered by its required checks. Conversely,
a warning based only on common file paths can be noisy. The useful signal is a predicted interaction
with consequences for the ongoing task.

## 2.5 Portable execution does not settle evidence authority

**Dagger addresses the local/CI runtime split directly.** Its documented model uses code and a
portable execution DAG with caching, usable before or after pushing a change.
[S15: Dagger introduction](https://docs.dagger.io/getting-started/introduction/)

This makes it a meaningful baseline for the complaint about a separate verification platform. Its
execution model alone does not settle whether a different party should accept a result as sufficient
evidence for integration. That authorization question remains available for a portable protocol,
regardless of the executor.

**Bazel makes two useful structures explicit: an action cache and a store addressed by output
content.** The action cache associates an action identity with result metadata; the content store
holds the bytes. [S16: Bazel remote caching](https://bazel.build/remote/caching) Its hermeticity
guidance also identifies host tools, timestamps, source-tree mutation, and other ways an ostensibly
repeatable build can depend on undeclared conditions.
[S17: Bazel hermeticity](https://bazel.build/basics/hermeticity)

For the-valley, an evidence store needs more than artifact identity. It needs the claim, the
computation or observation supporting it, and the policy that accepts the claimant. A useful cache
hit and an authorized integration decision can share hashes without being the same fact. Chapter 3
examines how far Nix's dependency model can support that distinction.

A backend portability experiment should therefore implement one meaningful check with two executors.
The point is not to compare their syntax. It is to discover whether the supposed common contract
contains assumptions that only one backend can satisfy. Identical filenames in two schemas are weak
evidence of portability; independently accepted conformance records are stronger evidence.

The place where a result becomes available also differs from the place where it was executed. A
local Nix invocation may obtain an output from a configured binary cache. The evidence needs to
distinguish executing, importing, observing, and endorsing a result.
[Chapter 3](03-verification-and-integration.md) examines these trust assumptions and the
independence required for a witness rebuild.

## 2.6 Changes, operations, and content are different identities

**Jujutsu records repository operations separately from source history.** An operation records a
view of refs, heads, and workspaces, with parent operations. Its documented recovery commands can
inspect or restore earlier repository views, including situations involving concurrent operations.
[S18: Jujutsu operation log](https://docs.jj-vcs.dev/latest/operation-log/)

That is valuable conceptual precedent for the-valley's distinction between current refs and the
history of what happened to them. An authoring tool's operation log does not automatically become an
authoritative shared audit log. Retention, replication, signatures, and acceptance of events still
need to be specified. It does show that source commits need not carry every kind of history.

**Pijul explores version control in terms of changes and their dependencies.** Its manual describes
commutation of independent changes and a graph representation that retains conflict information.
[S19: Pijul theory](https://pijul.org/manual/theory)

The narrow lesson is to keep change identity conceptually separate from a particular base and
snapshot. The stronger lesson must be resisted: algebraic properties of text changes do not
establish that their combined program satisfies a requirement. Two textually independent changes can
alter a shared behavioral assumption. the-valley still needs semantic checks and a policy for their
reuse.

These tools are worth studying without making replacement of Git a prerequisite. The current project
deliberately preserves native Git workflows. A better authoring client can remain optional while the
evidence protocol defines the acceptance boundary.

## 2.7 Human review does more than discover defects

The empirical case for review is broader than a pass/fail gate. Bacchelli and Bird's study of modern
code review describes knowledge transfer, awareness, alternative solutions, and understanding of
changes alongside defect finding. It is evidence about the studied development setting, not a proof
that all projects need the same review practice.
[S22: Expectations, Outcomes, and Challenges of Modern Code Review](https://www.microsoft.com/en-us/research/publication/expectations-outcomes-and-challenges-of-modern-code-review/)

The implication is that removing routine human review creates obligations beyond replacing its
correctness checks. A system must explain how important design changes become shared understanding.
For one operator and many short-lived agents, the knowledge graph may perform part of that function.
For a small team, selected discussions and summaries may still matter even when no human signature
is required before integration.

The report's recommendation is to distinguish approval, learning, and attention. Approval authorizes
an action. Learning updates project understanding. Attention directs a limited human resource. A
single review interaction can serve all three, but evaluating only approval latency would hide
losses in the other two.

## 2.8 Recent work that narrows the agent-specific questions

Two recent preprints are particularly close to the-valley's problem. They merit attention, with less
evidentiary weight than mature mechanisms or replicated empirical findings.

**BulkPR-Bench** studies selection and ordering of interacting proposed changes. It distinguishes
partial delivery scores from safe completion of a whole queue. Its constructed candidate pools and
fixed harness limit generalization to naturally occurring work. This report treats it as a 2026
preprint; a future conference label in its manuscript is not evidence of an already completed
peer-reviewed publication. [S21: Xiong et al., version 1](https://arxiv.org/html/2608.02685v1)

**AI Agent Pull Requests on GitHub** studies temporal overlap and replayed textual merges in an
agent PR dataset. Its limitations explicitly distinguish textual conflict from semantic conflict and
observed conflicts from unmeasured costs such as maintainer time. Its observation period predates
the publication and should not be read as a measurement of every current agent workflow.
[S23: Xu, Subramanian, and Karthik, version 2](https://arxiv.org/html/2607.04697v2)

Together they suggest a useful experimental split. One experiment should measure contention and
evidence invalidation given known changes. Another should measure whether agents discover the
relationships that make those changes safe to combine. Better scheduling cannot compensate for
missing relationships, while a perfect relationship graph cannot by itself implement safe admission.

## 2.9 What modularity should mean here

Parnas's argument for information hiding concerns the decisions a module keeps from other modules.
It does not equate modularity with dividing a process into many sequential steps.
[S01: On the Criteria To Be Used in Decomposing Systems into Modules](https://www.cs.lafayette.edu/~gexia/cs301/resources/parnas.html)

Applied to the-valley, a useful boundary lets the system change its build executor, bus, or review
interface without changing what a historical acceptance record means. A poor boundary merely moves
the same shared assumptions into several processes. This is the report's application of Parnas's
criterion, not a claim that the paper evaluated developer platforms.

Saltzer, Reed, and Clark's end-to-end argument supplies a second discipline. A lower layer's success
cannot necessarily establish the application's desired outcome. Their examples include delivery and
duplicate suppression.
[S02: End-to-End Arguments in System Design](https://web.mit.edu/saltzer/www/publications/endtoend/endtoend.pdf)

For this project, the inference is straightforward. Delivery to the event bus does not establish
that a deployment occurred. A verified signature does not establish that a requested capability
works. The layer that can observe the required result must contribute to the evidence. Lower-level
guarantees remain useful, but their scope needs to remain visible.

## 2.10 Credible architectural choices

The following are comparison configurations, not recommendations to migrate immediately.

| Configuration                                  | Why it is credible                                                      | Principal sacrifice                                                 | What an experiment should compare                                |
| ---------------------------------------------- | ----------------------------------------------------------------------- | ------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Conventional forge, Nix checks, tested backups | Addresses hosting and execution with few new semantics                  | Accepted decisions remain tied to forge records unless exported     | Operator time, recovery completeness, contribution latency       |
| Gerrit or ordinary review plus Zuul            | Strong comparison for policy and concurrent integration                 | More infrastructure and established review conventions              | Throughput, retesting cost, failures under contention            |
| Portable collaboration plus valley evidence    | Reuses transport and social records while testing the evidence boundary | Interoperability and dual-model complexity                          | Can another host independently reconstruct an acceptance?        |
| Git plus the current narrow valley components  | Preserves present workflow and concentrates new code on evidence        | Valley must own recovery and evidence semantics                     | Equal-trust comparison with the first two configurations         |
| Outcome engine layered over any of the above   | Separates knowledge and planning research from hosting choices          | Requires a stable boundary between proposals and authorized actions | Verified outcomes, revision cost, understanding, human attention |

The report favors the fourth configuration as the immediate experiment, with the first two as
serious baselines. The fifth should be evaluated as an additional layer. That position preserves the
unusual parts of the-valley's ambition while giving each of them a chance to fail independently.
