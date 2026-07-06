# Requirements

What the system must be. No solutions here — those are [architecture.md](./architecture.md). Every requirement derives from the [scenario ladder](./user-scenarios.md#the-ladder) and names the rung(s) that demand it; a requirement no rung demands has no business on this page. The mapping runs both ways — every rung below S7 leaves a mark here. The one exception is the constraints, which are imposed by the premise ([README](../README.md)) rather than derived: they bind the solutions, not the problem.

## Who it's for

The ladder is ordered by actors and trust, so the audience falls straight out of it:

- **v1: a solo developer plus AI agents as first-class authors** (S1–S4). Agents arrive at S3 not as tooling but as authors: their changes land with the same guarantees as the owner's, attributed to the agent that made them, with no human babysitting the pipeline. The system must be pleasant at N=1 before it's asked to hold at N>1.
- **Later: small, trusted teams** (S5). Invite-only contributor sets where trust is granted, bounded, and revocable — without the owner ever administering anything platform-shaped.
- **Explicitly not: public-social scale** (S7 — deferred, maybe never). No open-registration contributors, no spam-resistance, no social graph in v1.

## The needs

GitHub's bundle, restated as needs rather than features — each traced to the rungs that force it:

1. **Hosting** (S1). Repos live on infrastructure the owner controls, reachable and browsable, with no vendor lock-in. Daily life stays clone-edit-push; the platform fades to a mirror nobody thinks about.
2. **Identity & access** (S3, S5). Contributors — human and agent — are cryptographically identifiable, and access is grantable, bounded, and revocable without a vendor. S3 sharpens identity into attribution: knowing afterwards exactly who did what, non-human authors included, in a way that can't be waved through or forged.
3. **Verification & artifacts** (S2, S3). Checks give feedback in seconds, not minutes — S2 is the largest quality-of-life change in the whole design and the reason unbundling is worth it at N=1. Check results are trustworthy enough to integrate against — a verifiable claim, not a hope — which is also what lets S3's agent changes land unsupervised. Build outputs are reproducible, so any claim about them can be re-derived and checked.
4. **Automation** (S4). A change's consequences — builds, deploys, notifications — follow without anyone kicking anything. Reactions are independently addable and removable, without a central workflow file.
5. **Integration** (S2, S3, S5). Changes reach protected branches through an observable, policy-driven path — never by unchecked direct write. The path must be fast enough to disappear at N=1 (S2), safe to leave unsupervised when the author is an agent (S3), and open to a second human without granting them anything platform-shaped (S5).
6. **Observability & feedback** (S4, S6). Anyone or anything that needs to know about a change's state — before or after it lands — can find out, without drowning in a notification firehose. "Why did X occur" is answerable from one history: the chain from change to effect survives as a record, not as tribal memory (S4). And when the record assigns blame, it carries its uncertainty honestly — a confidently wrong attribution is worse than none (S6).
7. **Project knowledge & discussion** (S1, S3, S4, S6). Everything that isn't code or user docs — outcomes, bugs, decisions, principles, ideas, threads — has a durable home, equally legible to humans and agents; the work to be done is itself knowledge, not a vendor-tracker artifact. The ladder grows this substrate one increment per rung instead of demanding a system up front: knowledge lives with the repo as plain files (S1); agents read and write it, and work is dispatched against it (S3); changes to it are observable, and a landed change can close the outcome it serves (S4); incidents file their own nodes, with attribution (S6).

## Constraints

Not derived from the ladder — imposed on every solution to it, from the premise ([README](../README.md)):

- **Open source.** The substrate must be inspectable and forkable; a closed dependency reintroduces the lock-in being escaped.
- **Minimal.** Small composed tools stay understandable and replaceable; platforms accrete.
- **Nix-native.** Hermetic, content-addressed builds are what make verification trustworthy and artifacts reproducible.
- **Decentralized where possible.** Centralization is accepted only where ordering or coordination genuinely require it, and must be explicit.

## Durability

The git data is the crown jewel; losing it is the one unrecoverable failure. Everything else — infrastructure, tooling, even the event history's ephemeral parts — is rebuildable. [S1](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them) states the requirement as an experience — *I can never lose them* — and makes it first-class, not an operational afterthought: pushed means replicated, with pushed work in at least two independent places, one of them offsite, within minutes; and no copy counts until a restore from it has been performed and verified. Configured is not durable; tested is.

## Non-goals

- Public-social scale, discovery, or spam resistance (S7 — deferred, maybe never). The stranger rung stays on the ladder only as the limit the trust model should degrade toward gracefully, not a target being built for.
- Building a platform. This is a set of small tools over a substrate; S5 is passed precisely by *not* having a platform to administer.
- Defense against a compromised developer machine signing genuine attestations of malicious code — hardware attestation is the only fix and is out of scope. No rung demands it.
- Migration tooling for the world's existing repos and trackers. The pilot is our own — every rung is anchored in the owner's real repos.
