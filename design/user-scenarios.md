# User scenarios

The docs are layered: premise ([README](../README.md)) → requirements ([requirements.md](./requirements.md)) → architecture ([architecture.md](./architecture.md)) → design. This document sits at the requirements layer: an escalating ladder of user scenarios, ordered by actors and trust, that the requirements fall out of. It is strictly problem-space — what a user experiences and needs, with zero mechanisms. The complementary solution-space artifact is [scenarios.md](./scenarios.md), which walks the architecture through concrete situations in event notation: this document says what must be true, that one shows how the system makes it true.

A scenario gets acceptance-level detail only when it becomes the top priority; each rung is fleshed out as we build toward it, never sooner. Right now only S1 is detailed.

## The ladder

Each rung adds actors or removes trust. Each names its knowledge-graph increment — the slice of the [project-knowledge need](./requirements.md) that rung forces into existence, because the knowledge substrate grows with the scenarios rather than arriving as a system.

| Id | Scenario | Anchored in | Knowledge increment |
| --- | --- | --- | --- |
| S1 | My repos live on my infrastructure and I can never lose them. | the-valley itself as the pilot | Issues, outcomes, ideas, and decisions live with the repo as plain files |
| S2 | I push, and seconds later it's integrated; I never wait for CI. | Daily solo work | None new |
| S3 | I dispatch an agent; its change lands with the same guarantees, attributably, unsupervised. | klaus, the owner's daily agent-orchestration tool | Agents read and write knowledge nodes; dispatch targets an outcome node, not a GitHub issue |
| S4 | Builds, deploys, and notifications just happen; "why did X occur" is answerable from one history. | cosmo (NixOS infra), reel-life (a deployed service) | Knowledge changes become observable events; a landed change can close the outcome it serves |
| S5 | A second, semi-trusted human lands a change without me granting them anything platform-shaped. | The first collaborator | None new |
| S6 | A bad deploy gets attributed, reverted, and remembered. | A reel-life regression | Incidents file their own knowledge nodes, with attribution |
| S7 | Strangers contribute safely. | Nothing — explicitly deferred, maybe never | — |

## S1 — my repos live on my infrastructure and I can never lose them

**The narrative.** Daily life looks almost exactly like today. Clone, edit, push — same git, same agents, same habits. The difference is where the pushes go: the canonical copy of every repo sits on hardware the owner controls, and within minutes of any push the work exists in at least two independent places, one of them offsite. GitHub fades to a mirror nobody thinks about. If the primary box dies, everything comes back from the offsite copy within a day — and that claim has been tested, not assumed. Meanwhile the project's issues, ideas, and decisions travel with the repo as files, cloned and backed up by the same motion that protects the code.

**Scope decisions already made.**

- **Hosting only.** Bare repos on the owner's existing tailnet box (classic-laddie). Direct push to `main` is correct at this rung — there is no integrator yet, and feeling its absence is S2 and S3's job to motivate.
- **Mirror-first migration.** Dual-push with GitHub retained as a transitional mirror; the canonical origin flips per-repo once confidence is earned. Reversible at every step.
- **Durability means "pushed = replicated".** RPO ≈ 0 for pushed work: every push lands in at least two independent locations within minutes, one of them offsite (during migration, the GitHub mirror); a push is never in only one place. RTO is relaxed: full restore within a day from the offsite depth copy — nightly encrypted backups on a Hetzner Storage Box, the copy the restore runs against. A restore must be *performed and verified* before S1 counts as done — configured is not done. The mechanism is decided ([dcr-d7952bc](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md)); the chosen layers live in [roadmap Phase 0](./roadmap.md#phase-0--mvp-repos-off-github), not here.
- **Agents keep working — direct-push interim mode.** klaus agents today work GitHub-PR-shaped. During S1 they operate degraded: push a branch to classic-laddie, the owner reviews the diff and merges by hand — the owner *is* the integrator, manually. This is deliberate: that pain is the validation signal that motivates the integrator rung. S1 is not done if agent-driven development on the pilot repo has to pause.
- **Knowledge v0 — a directory convention, not a system.** Issues, outcomes (`oc-*`), ideas, and decisions are markdown files in the repo with YAML frontmatter (type, id, status, title; edges later). Creating an issue is a commit; closing one is a commit; listing is `ls`; search is `grep`; history is `git log`. No indexer, no events, no validation — the schemas are documentation until there is an integrator to enforce them. The pilot repo's open design questions and review findings become the seed content (a follow-up, not part of establishing this rung). The convention is now instantiated at [.the-valley/](../.the-valley/README.md).

**Acceptance criteria.** S1 holds when every box is checked — by looking, not by reviewing configuration:

- [ ] the-valley's canonical origin is classic-laddie; GitHub is a mirror.
- [ ] Every push is present in at least two independent locations within minutes, verified by checking both, not by trusting the config.
- [ ] One full restore from the offsite copy has been performed and verified.
- [ ] A week of real work — human *and* agent — on the pilot repo without touching GitHub.
- [ ] A klaus agent change lands end to end via direct-push mode.
- [ ] At least one real issue is opened, worked, and closed as an in-repo node — including one agent dispatched against a node instead of a GitHub issue.
- [ ] The migration-plus-restore runbook exists and is repeatable for the next repo.

## The rest of the ladder

One paragraph per rung. None carry acceptance criteria yet; each gets them when it becomes the top priority, informed by what the rungs below it taught.

**S2 — I push, and seconds later it's integrated; I never wait for CI.** The feedback loop that governs daily work drops from minutes on someone else's infrastructure to seconds on the owner's own. This is the largest quality-of-life change in the whole design and the reason unbundling is worth it at N=1. The hardest thing it demands: checks run where the work was written have to be trustworthy enough to integrate against — a verifiable claim, not a hope.

**S3 — I dispatch an agent; its change lands with the same guarantees, attributably, unsupervised.** klaus dispatches agents daily; today their output funnels through GitHub-shaped review. At this rung an agent's change lands with the same guarantees as the owner's own, attributable to the agent that made it, with no human babysitting the pipeline. It matters because agents author a growing share of the work and are the first users of most of this system. Hardest demand: attribution for a non-human author — knowing afterwards exactly who did what, in a way that can't be waved through or forged.

**S4 — Builds, deploys, and notifications just happen; "why did X occur" is answerable from one history.** A change lands and its consequences — build, deploy to cosmo-managed infra, a reel-life release, a notification — follow without anyone kicking anything. When something surprising happens, "why" is answered from one history instead of reconstructed across disconnected tools. Hardest demand: keeping causality queryable — the chain from change to effect must survive as a record, not as tribal memory.

**S5 — A second, semi-trusted human lands a change without me granting them anything platform-shaped.** The first collaborator arrives: trusted enough to invite, not trusted blindly. They land a change without the owner creating accounts, assigning roles, or administering a platform. It matters as the first test that the system holds at N=2 and that trust is grantable, bounded, and revocable — the [requirements](./requirements.md) promise for small teams. Hardest demand: giving a second person exactly enough — access that is easy to grant, easy to limit, and easy to take back.

**S6 — A bad deploy gets attributed, reverted, and remembered.** A reel-life deploy goes bad. The system attributes the regression to the change that caused it, reverts, and remembers: the incident becomes part of the project's durable memory instead of a war story. Hardest demand: attribution under uncertainty — monitoring rarely *knows* which change is at fault, and a confidently wrong answer is worse than none.

**S7 — Strangers contribute safely.** Unknown contributors land changes without endangering anything. Explicitly deferred, maybe never — public-social scale and spam resistance are [non-goals](./requirements.md). It stays on the ladder because it is the limit the trust model should degrade toward gracefully, not a target being built for.
