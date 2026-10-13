# Requirements

These requirements describe what the system must do. The [user scenarios](./user-scenarios.md)
explain the needs behind them; the [architecture](./architecture.md) explains the proposed
solutions.

## Who it is for

The first users are a solo developer and the AI agents working with them, as described in S1–S4. The
system must be useful in that setting before it expands to small, invited teams in S5–S6.

Open participation by unknown contributors is deferred. Public discovery, social features, and spam
resistance are outside the current scope.

## User needs

1. **Control and recover project state (S1).** Repositories live on infrastructure the operator
   controls. Familiar development habits remain usable. Project knowledge is available alongside the
   code and receives the same protection against loss. The recovery requirements are
   [below](#durability).
2. **Integrate verified changes in seconds (S2).** Verification performed during authorship must be
   trustworthy enough to count at integration. A change should not wait for already established
   results to be produced again. The evidence must identify the work it covers and allow a verifier
   to check the claim.
3. **Support agent authorship (S3).** An agent's change meets the same requirements as a human's.
   Authorized work can proceed without someone supervising each step. The record identifies the run
   that produced the change and the authority under which it acted. The strength and limits of that
   attribution must be clear.
4. **Explain automated consequences (S4).** Builds, deployments, and notifications follow authorized
   changes automatically. Reactions can be added or removed independently. A durable history
   connects an effect to the events that caused it.
5. **Grant and revoke bounded trust (S5).** The operator can invite a collaborator, give them the
   access they need, and withdraw it. This must not require adopting a vendor's account or role
   system.
6. **Preserve incident knowledge (S6).** When a change causes a failure, the system supports finding
   the cause, reverting it, and recording what was learned. Attribution must state its uncertainty.
   The incident and its response remain available to later work.
7. **Work toward requested outcomes.** The system records desired outcomes and their dependencies so
   it can help determine and perform the work needed to complete them. This broader requirement
   comes from the [project's direction](../README.md#where-this-goes). Agent dispatch in S3 and
   outcome completion in S4 are its first applications.

Project knowledge grows with these needs. S1 requires it to survive alongside the code. S3 requires
agents to use and improve it. S4 connects it to the record of automated work. S6 adds incidents and
their causes. No scenario requires a complete knowledge-management system before useful work can
begin.

## Constraints

These constraints come from the [project premise](../README.md) and apply to every proposed
solution.

- **Open source.** The system must be inspectable and forkable.
- **Small, replaceable parts.** Components should remain understandable and independently
  replaceable. Meeting one need should not require adopting an unrelated set of services.
- **A Nix reference implementation with portable contracts.** Nix provides the reference build and
  verification environment. Schemas must remain implementable without it, as described in the
  [backend decision](../.the-valley/ideas/ida-b9f646c-nix-backend-not-substrate.md).
- **One verification environment.** A check has one definition and runs the same way wherever
  verification occurs. Local development, agent work, and verification services must not require
  separate configuration languages or debugging procedures.
- **Verification latency must not determine integration throughput.** A slow check may delay its own
  change. It must not force unrelated changes to wait behind it. This requirement still needs
  validation under contention; the
  [integration starvation risk](../.the-valley/bugs/bd-c324184-occ-starvation-under-contention.md)
  questions whether the current design meets it.
- **Explicit coordination.** Central coordination is acceptable where ordering or agreement requires
  it. The reason for that coordination and the authority it holds must be visible.
- **An auditable supply chain.** Dependencies must be enumerable and their provenance establishable.
  Pinning a large dependency set does not by itself make that set understandable or reviewed.

## Durability

Project state must survive the loss of the primary host. Today, code and knowledge nodes live in
Git. The requirement applies to any additional
[project stores](../.the-valley/ideas/ida-48c8868-stores-beyond-git.md) as they are introduced.

Every integrated change must be present in at least two independent locations within minutes, with
one copy offsite. Full recovery from the offsite copy must be possible within a day. A copy counts
toward the guarantee only after a restore from it has been performed and verified.

Pending topic branches have a different guarantee. They receive periodic offsite backup, but the
publication mirror contains integrated history only. The
[durability scope decision](../.the-valley/decisions/dcr-24d62f7-publication-mirror-not-review-queue.md)
records this distinction.

Infrastructure and derived views may be rebuilt. Records needed to explain deployments or recover
project state may not be discarded merely because they are outside Git. S4 therefore requires a
durability decision for cross-system history before it can be considered complete.

## Non-goals

- Public social features, discovery, and spam resistance.
- A platform that requires administering unrelated services to host a project or invite a
  collaborator.
- Proving that a fully compromised authoring machine executed a check honestly solely from that
  machine's signature. Detection and recovery remain part of the trust design; the signature alone
  cannot establish execution.
- General migration tooling for every existing forge and tracker. Migration starts with the
  operator's own projects.
