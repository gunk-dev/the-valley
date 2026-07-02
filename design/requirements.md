# Requirements

What the system must be. No solutions here — those are [architecture.md](./architecture.md).

## Who it's for

- **v1: a solo developer plus AI agents as first-class authors.** The system must be pleasant at N=1 before it's asked to hold at N>1.
- **Later: small, trusted teams.** Invite-only contributor sets where trust is granted, measured, and revocable.
- **Explicitly not: public-social scale.** No open-registration contributors, no spam-resistance, no social graph in v1.

## The needs

GitHub's bundle, restated as needs rather than features:

1. **Hosting.** Repos live on infrastructure the owner controls, reachable and browsable, with no vendor lock-in.
2. **Identity & access.** Contributors — human and agent — are cryptographically identifiable, and access is grantable and revocable without a vendor.
3. **Verification & artifacts.** Checks give feedback in seconds, not minutes; build outputs are reproducible, content-addressed, and cacheable.
4. **Automation.** Reactions to events are independently addable and removable, without a central workflow file.
5. **Integration.** Changes reach protected branches through an observable, policy-driven path — never by unchecked direct write.
6. **Observability & feedback.** Anyone or anything that needs to know about a change's state — before or after it lands — can find out, without drowning in a notification firehose.
7. **Project knowledge & discussion.** Everything that isn't code or user docs — bugs, decisions, principles, ideas, threads — has a durable home, equally legible to humans and agents.

## Constraints

- **Open source.** The substrate must be inspectable and forkable; a closed dependency reintroduces the lock-in being escaped.
- **Minimal.** Small composed tools stay understandable and replaceable; platforms accrete.
- **Nix-native.** Hermetic, content-addressed builds are what make verification trustworthy and artifacts reproducible.
- **Decentralized where possible.** Centralization is accepted only where ordering or coordination genuinely require it, and must be explicit.

## Durability

The git data is the crown jewel; losing it is the one unrecoverable failure. Everything else — infrastructure, tooling, even the event history's ephemeral parts — is rebuildable. Durability is therefore a first-class requirement, not an operational afterthought: multiple copies, offsite, with restores actually performed and verified before any copy becomes the only one.

## Non-goals

- Public-social scale, discovery, or spam resistance.
- Building a platform. This is a set of small tools over a substrate.
- Defense against a compromised developer machine signing genuine attestations of malicious code — hardware attestation is the only fix and is out of scope.
- Migration tooling for the world's existing repos and trackers. The pilot is our own.
