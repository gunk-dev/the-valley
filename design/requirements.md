# Requirements

What the system must be. No solutions here — those are [architecture.md](./architecture.md). Each
requirement derives from a rung of the [scenario ladder](./user-scenarios.md#the-ladder): the rung
says what a user experiences; the requirement is what must hold for the rung to be true. The mapping
runs both ways — every rung below S7 leaves a mark here, and a requirement no rung demands has no
business on this page. The one exception is the constraints, which are imposed by the premise
([README](../README.md)) rather than derived: they bind the solutions, not the problem.

## Who it's for

The ladder is ordered by actors and trust, so the audience falls straight out of it:

- **v1: a solo developer plus AI agents as first-class authors** (S1–S4). The system must be
  pleasant at N=1 before it's asked to hold at N>1.
- **Later: small, trusted teams** (S5–S6). Invite-only contributor sets where trust is granted,
  bounded, and revocable.
- **Explicitly not: public-social scale.** S7 is deferred, maybe never; it stays on the ladder only
  as the limit the trust model should degrade toward gracefully.

## The needs

1. **Never losable** (S1). Repos live on infrastructure the operator controls, with no vendor
   lock-in: daily life stays clone-edit-push, and the platform fades to a mirror nobody thinks
   about. Project knowledge (outcomes, ideas, decisions, threads) travels with the repo, protected
   by the same motion that protects the code. Precisely what this means: [durability](#durability),
   below.
2. **Integrated in seconds** (S2). Push to integrated completes in seconds, resting on checks that
   are **trustworthy where the work was written** — a verifiable claim, not a hope — with
   reproducible build outputs so the claim can be checked. This is the largest quality-of-life
   change in the whole design and the reason unbundling is worth it at N=1.
3. **Agents as first-class authors** (S3). An agent's change lands with the same guarantees as the
   operator's own, unsupervised, attributable to exactly the agent that made it — attribution for a
   non-human author that can be neither waved through nor forged.
4. **Causality queryable from one history** (S4). Consequences — builds, deploys, notifications —
   follow without anyone kicking anything, each reaction independently addable and removable; "why
   did X occur" is answerable from one durable history, not reconstructed across tools.
5. **Trust grantable, bounded, revocable** (S5). A second, semi-trusted human lands a change without
   the operator administering accounts, roles, or a platform: exactly enough access — easy to grant,
   limit, and take back — without a vendor.
6. **Attributed incident memory** (S6). A landed change that goes bad gets attributed, reverted, and
   remembered: the incident becomes durable project knowledge, not a war story. When the record
   assigns blame, it carries its uncertainty honestly — a confidently wrong attribution is worse
   than none.
7. **Demand-shaped work** ([README: where this goes](../README.md#where-this-goes)). Work to be done
   is itself knowledge — outcomes on a dependency graph the system is under pressure to complete
   toward what someone actually asked for, not merely to record.

**The unbundling note.** GitHub's bundle — hosting, identity & access, verification & artifacts,
automation, integration, observability & feedback, project knowledge & discussion — cuts across
these needs: hosting is need 1; verification, artifacts, and integration are need 2; identity &
access serves needs 3 and 5; automation is need 4; observability & feedback runs through needs 4 and
6; project knowledge & discussion runs through needs 1, 3, 4, 6, and 7. The concerns remain the
vocabulary the [architecture](./architecture.md) unbundles by; the rungs are why each is needed. The
knowledge substrate itself grows one rung-sized increment at a time instead of arriving as a system:
nodes live with the repo as plain files (S1); agents read and write them, and work is dispatched
against them (S3); changes to them are observable, and a landed change can close the outcome it
serves (S4); incidents file their own nodes, with attribution (S6).

## Constraints

Not derived from the ladder — imposed on every solution to it, from the premise
([README](../README.md)):

- **Open source.** The substrate must be inspectable and forkable; a closed dependency reintroduces
  the lock-in being escaped.
- **Minimal.** Small composed tools stay understandable and replaceable; platforms accrete.
- **Nix-native.** Hermetic, content-addressed builds are what make verification trustworthy and
  artifacts reproducible.
- **Decentralized where possible.** Centralization is accepted only where ordering or coordination
  genuinely require it, and must be explicit.

## Durability

The git data is the crown jewel; losing it is the one unrecoverable failure. Everything else —
infrastructure, tooling, even the event history's ephemeral parts — is rebuildable. Durability is
therefore a first-class requirement, not an operational afterthought — it is the whole of
[S1](./user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them), the rung
everything else stands on. Pushed means replicated: pushed work is in at least two independent
places, one of them offsite, within minutes. No copy counts until a restore from it has been
performed and verified. Configured is not durable; tested is.

## Non-goals

- Public-social scale, discovery, or spam resistance (S7: deferred, maybe never).
- Building a platform. This is a set of small tools over a substrate; S5 is passed precisely by
  _not_ having a platform to administer.
- Defense against a compromised developer machine signing genuine attestations of malicious code —
  hardware attestation is the only fix and is out of scope. No rung demands it.
- Migration tooling for the world's existing repos and trackers. The pilot is the project's own —
  every rung is anchored in the operator's real repos.
