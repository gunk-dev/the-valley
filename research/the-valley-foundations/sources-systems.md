# Sources: architecture and existing systems

These entries support chapters [1](01-project-and-problem-map.md) and [2](02-existing-systems.md).
All external sources were accessed on 2026-09-20. "Selected sections" means that the cited mechanism
was read in the source; it does not mean that the complete manual or paper was reviewed.
Documentation establishes a project's stated behavior, not independently measured reliability. No
product in this register was deployed or benchmarked for this report.

## S01 — Information hiding

D. L. Parnas. **On the Criteria To Be Used in Decomposing Systems into Modules**. _Communications of
the ACM_ 15(12), 1053–1058, 1972. [DOI](https://doi.org/10.1145/361598.361623);
[paper transcription](https://www.cs.lafayette.edu/~gexia/cs301/resources/parnas.html).

Read: abstract, worked decomposition discussion, criteria, and implementation tradeoffs. The paper
provides a criterion for replaceable components: hide decisions likely to change. It is conceptual
design analysis, not evidence that microservices reduce operating cost. Apply it to the-valley's
evidence, transport, executor, and storage boundaries.

## S02 — End-to-end responsibility

J. H. Saltzer, D. P. Reed, and D. D. Clark. **End-to-End Arguments in System Design**. _ACM
Transactions on Computer Systems_ 2(4), 277–288, 1984.
[Author-hosted paper](https://web.mit.edu/saltzer/www/publications/endtoend/endtoend.pdf).

Read: introduction, file-transfer argument, performance discussion, and delivery guarantees. The
paper explains why application-level knowledge can remain necessary despite reliable lower layers.
It supports an analogy for the-valley, not a theorem about its architecture. Use it to question
whether bus delivery, signatures, or check success establish the desired outcome.

## S03 — Radicle

Radicle contributors. **Radicle Protocol Guide**. Living Heartwood protocol guide.
[Source](https://radicle.dev/guides/protocol).

Read: repository identity, signed refs, canonical branches, storage, collaborative objects, and
concurrency. It is close prior art for authenticated collaboration objects replicated with Git. The
guide is a project description, not an independent durability evaluation. Compare its acceptance and
object models before inventing parallel social-record protocols.

## S04 — Fossil

Fossil contributors. **Fossil Versus Git**. Living project documentation.
[Source](https://fossil-scm.org/home/doc/trunk/www/fossil-v-git.wiki).

Read: introduction and sections on integrated facilities and self-contained deployment. It supplies
a counterexample to treating many separate programs as the only route to minimalism. Its comparative
claims are explicitly project-authored and favorable to Fossil. Use the architecture as a baseline;
measure operating cost independently.

## S05 — Forgejo

Forgejo contributors. **Forgejo CLI**, v15.0 documentation.
[Source](https://forgejo.org/docs/v15.0/admin/command-line/).

Read: command overview, dump, dump-repo, and restore-repo descriptions. The export model illustrates
the distinction between owning a forge and representing every durable artifact in Git. Backup
command documentation alone does not establish a consistent backup or successful restore. This is
the conventional self-hosted comparison for S1.

## S06 — SourceHut

Drew DeVault. **Announcing the SourceHut project hub**. 2020; page displays April 29, URL April 30.
[Source](https://sourcehut.org/blog/2020-04-30-the-sourcehut-hub-is-live/).

Read: full announcement. It describes composing independently useful development services under a
project view. The announcement establishes the design intent, not a present performance comparison.
Its relevance is separating concern ownership from the coherence of the user interface.

## S07 — b4 and email contribution

b4 contributors. **am, shazam: retrieving and applying patches**. Living end-user documentation.
[Source](https://b4.docs.kernel.org/en/latest/maintainer/am-shazam.html).

Read: retrieval flow, review trailers, attestation checks, and selected options. It demonstrates
structured contribution through a transport independent of target-repository write access. Patch
authentication is not execution attestation. Borrow the separation of proposal, attribution, and
acceptance rather than assuming the email interface itself is required.

## S08 — Tangled's component model

The Tangled Contributors. **Tangled docs**, overview. Living documentation.
[Source](https://docs.tangled.org/).

Read: overview of knots, AT Protocol, and the appview. It is useful comparative architecture for
separating hosting from a collaboration view. The page's displayed update date does not pin all
linked documentation to that date. No federation or recovery behavior was independently tested.

## S09 — Tangled's execution model

The Tangled Contributors. **Spindles**. Living documentation.
[Source](https://docs.tangled.org/spindles).

Read: pipeline format, triggers, engines, and caching sections. Nixery and microVM execution make it
a nearby design, while its workflow format differs from the-valley's runtime constraint. Nix-related
execution alone should not be interpreted as a portable integration-evidence protocol.

## S10 — Gerrit's change model

Gerrit contributors. **Changes**. Living documentation, page displayed `v3.14.2-892-g1ff12a23c6`
when accessed. [Source](https://gerrit-review.googlesource.com/Documentation/concept-changes.html).

Read: change identity, patch sets, related changes, and submit strategies. It distinguishes a
logical review unit from the commits that revise it. It does not establish portability of every
review artifact. Use it to separate proposal identity, content identity, and integration-attempt
identity.

## S11 — Gerrit's governed configuration

Gerrit contributors. **Submit Requirements**. Same living documentation family as S10.
[Source](https://gerrit-review.googlesource.com/Documentation/config-submit-requirements.html).

Read: configuration, testing requirements, and policy-lockout recovery discussion. It shows that
reviewing the rules that govern review already has practical precedent. Its administrative recovery
paths are specific to Gerrit; the-valley still needs its own explicit trust and recovery model.

## S12 — Zuul

Zuul contributors. **Project Gating**. `latest` documentation at access date.
[Source](https://zuul-ci.org/docs/zuul/latest/gating.html).

Read: parallel speculative testing, window behavior, cross-project queues, and dependencies. It is
the strong comparison for concurrent checks with ordered integration. Performance depends on failure
and dependency patterns; this report measured none. It prevents an unfair comparison against only a
serial CI gate.

## S13 — GitHub merge queues

GitHub. **Managing a merge queue**. Living product documentation.
[Source](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-pull-request-merges/managing-a-merge-queue).

Read: merge groups, CI triggers, concurrency settings, and queue-reordering examples. This
establishes that speculative combined-state validation is available in conventional hosting.
Availability and plan restrictions may change; the report makes no procurement recommendation or
benchmark claim.

## S14 — GitLab merge trains

GitLab. **Merge trains**. Living product documentation.
[Source](https://docs.gitlab.com/ci/pipelines/merge_trains/).

Read: combined-state pipeline model, parallel execution, failure removal, and redundant-pipeline
cancellation. It provides another established comparison for concurrency without unsafe independent
merges. Its behavior does not imply the-valley's evidence transfer is unnecessary; the mechanisms
avoid different kinds of repeated work.

## S15 — Dagger

Dagger contributors. **Introduction**. Documentation displayed `1.0-beta` at access date.
[Source](https://docs.dagger.io/getting-started/introduction/).

Read: complete introductory page. It states the portable execution-DAG and local/CI reuse model.
This is a vendor introduction, not a measured performance result or a detailed security contract.
Its role here is to identify a credible baseline for sharing the development and verification
runtime.

## S16 — Bazel caching

Bazel contributors. **Remote Caching**. Living documentation.
[Source](https://bazel.build/remote/caching).

Read: action-cache/content-store distinction, execution flow, and security considerations. It
clarifies the identities needed for computational reuse. A cache hit still depends on the action
model and trust in cache writers. The-valley adds an acceptance-policy question to that
computational question.

## S17 — Bazel hermeticity

Bazel contributors. **Hermeticity**. Living documentation.
[Source](https://bazel.build/basics/hermeticity).

Read: full substantive page. It describes isolation, declared tool dependencies, and common sources
of nondeterministic or host-dependent behavior. A documented goal is not proof that an arbitrary
project meets it. Use these failure classes in executor conformance experiments.

## S18 — Jujutsu

Jujutsu contributors. **Operation log**. Living `latest` documentation.
[Source](https://docs.jj-vcs.dev/latest/operation-log/).

Read: full substantive page. Repository-operation history is distinct from source-commit history and
supports inspection and recovery. Its local operational purpose does not establish shared audit
authority or indefinite retention. It informs the-valley's history model without requiring a VCS
migration.

## S19 — Pijul

Pijul contributors. **Theory**. Living manual. [Source](https://pijul.org/manual/theory).

Read: graph representation, change dependencies, conflicts, and CRDT discussion. It offers a
different model of changes and their composition. Its merge properties concern the representation of
edits, not arbitrary program correctness. Keep that boundary when applying it to verification reuse.

## S20 — Proactive collaboration conflicts

Yuriy Brun, Reid Holmes, Michael D. Ernst, and David Notkin. **Proactive Detection of Collaboration
Conflicts**. ESEC/FSE, 168–178, 2011.
[Author-hosted paper](https://people.cs.umass.edu/~brun/pubs/pubs/Brun11fse.pdf).

Read: introduction, speculative-analysis explanation, relationship taxonomy, and selected study
methods/results. It distinguishes text, build, and test conflicts and presents Crystal. Historical
repositories and available tests limit transport of measured rates to today's agents. Its mechanism
motivates earlier interaction warnings, alongside admission control.

## S21 — BulkPR-Bench

Zetong Xiong et al. **BulkPR-Bench: Benchmarking Queue-Level Governance of Interacting Pull
Requests**. arXiv:2608.02685v1, August 3, 2026.
[Versioned text](https://arxiv.org/html/2608.02685v1).

Read: abstract, task framing, selected results, and discussion/limitations. The useful distinction
is between partial relation-level success and whole-queue safety. Constructed pools, fixed
scaffolding, and limited repeats constrain generalization. Treat as a preprint, irrespective of the
manuscript's future conference label. It is a candidate evaluation lead, not evidence of production
reliability.

## S22 — What review accomplishes

Alberto Bacchelli and Christian Bird. **Expectations, Outcomes, and Challenges of Modern Code
Review**. ICSE, 712–721, 2013.
[Research publication page](https://www.microsoft.com/en-us/research/publication/expectations-outcomes-and-challenges-of-modern-code-review/).

Read: author-institution abstract and bibliographic record only; an attempted author-hosted PDF was
unavailable through the browser. The cited findings are limited to that abstract. It reports
benefits including understanding and knowledge transfer in studied Microsoft teams. The report uses
it to identify functions to preserve, not to quantify a universal effect.

## S23 — Concurrent agent contributions

George Xu, Arjun Subramanian, and Nithilan Karthik. **AI Agent Pull Requests on GitHub: Frequency,
Structure, and Merge Conflict Rates**. arXiv:2607.04697v2, July 7, 2026.
[Versioned text](https://arxiv.org/html/2607.04697v2).

Read: abstract, study framing, discussion, and threats to validity. It studies co-activity and
textual merge replay. Observation is limited to the selected public dataset and earlier collection
period; time costs and semantic failures are not measured. Its more sweeping conclusions should not
be imported as findings. It motivates measuring the actual pilot's conflict distribution.
