---
type: outcome
id: oc-9b94fe4
status: open
title: "Give each design rule a clear authoritative statement"
created: 2026-08-02
source: design conversation, 2026-08-02
blocked_by: []
---

# Give each design rule a clear authoritative statement

The documentation should give each rule a clear home and use references wherever that rule is needed
elsewhere. A reader should be able to identify the current decision without comparing several
competing explanations.

This outcome covers the graph, design documents, and top-level README. It remains open while their
consistency and division of responsibility need review.

## Completion criteria

- [ ] Each important rule has an identifiable defining node or design section.
- [ ] Other documents summarize only what they need and link to that definition.
- [ ] Current statements agree with the defining rule.
- [ ] Superseded nodes direct readers to their replacements.
- [ ] Distinct concepts that share vocabulary are explained separately.
- [ ] Proposed mechanisms and implemented behavior are clearly distinguished.
- [ ] Implementation details and historical notes do not obscure the current requirements.

A short local explanation may be necessary for a document to be understandable. The aim is to avoid
independently maintained definitions, not to prohibit every repeated sentence.

## Rule locations to check

| Subject                                           | Defining material                                                                                                                                                                                                                         |
| ------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Valley, project, and host boundaries              | [[dcr-8f069dd]](../decisions/dcr-8f069dd-valley-is-the-unit.md), [[dcr-5da1f36]](../decisions/dcr-5da1f36-project-is-repo.md), [[dcr-9b5da04]](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md)                                  |
| Governing state and policy adoption               | [[dcr-2965320]](../decisions/dcr-2965320-valley-is-its-repository.md), [[dcr-9c75283]](../decisions/dcr-9c75283-substrate-adoption-and-the-core.md)                                                                                       |
| Required checks and their trusted source          | [[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md), [[ida-a9e274c]](../ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md), [[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)  |
| Attestation subjects, text, and signatures        | [[dcr-0de694f]](../decisions/dcr-0de694f-phase2-attestation-shape.md), [[dcr-de9d996]](../decisions/dcr-de9d996-statement-text-and-signed-note.md)                                                                                        |
| Evidence after the base moves                     | [[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md)                                                                                                                                              |
| Identity, delegated work, and physical approval   | [[dcr-b87f6e8]](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md), [[ida-a8243d2]](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md), [[ida-b7025b5]](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md) |
| Replication scope and recovery                    | [[dcr-d7952bc]](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md), [[dcr-24d62f7]](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md), [[oc-9949561]](oc-9949561-push-replication.md)                     |
| Node format, IDs, validation, and terminal states | [Graph conventions](../README.md), [[dcr-593d3d1]](../decisions/dcr-593d3d1-ideas-are-transitional.md)                                                                                                                                    |
| Portable schemas and implementation backends      | [[ida-b9f646c]](../ideas/ida-b9f646c-nix-backend-not-substrate.md)                                                                                                                                                                        |
| CLI lifecycle and its unresolved revision         | [[dcr-74c3158]](../decisions/dcr-74c3158-valley-cli-lifecycle.md), [[dcr-2aa1a12]](../decisions/dcr-2aa1a12-valley-cli-graduation.md)                                                                                                     |

## Related concepts that need clear boundaries

Outcome dependencies, continuing demand, and reconciliation serve different purposes.
[[ida-eac723e]](../ideas/ida-eac723e-outcome-dag.md) defines scheduled work,
[[ida-3145b7a]](../ideas/ida-3145b7a-demand-pressure.md) considers progress and stalls, and
[[ida-b48bded]](../ideas/ida-b48bded-production-dags-and-events.md) connects dependencies to
event-driven observations.

Delegation describes permitted actions. The actuator proposal describes how external actions could
be refused and recorded. Their separate homes are
[[ida-a8243d2]](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md) and
[[ida-f1b39e8]](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md).

Stable change identity, checked-tree identity, and evidence validity are related but distinct.
[[ida-93e4f91]](../ideas/ida-93e4f91-changes-not-branches.md),
[[ida-51605e8]](../ideas/ida-51605e8-authenticity-not-git-coupled.md), and
[[ida-cbcbb3c]](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md) introduce them; the integration
decision supplies the transfer rule.

Run provenance and evaluation also belong together without being treated as identical.
[[ida-45178f6]](../ideas/ida-45178f6-agent-identity-is-provenance.md) defines the record and its
uses, while [[ida-99b87d9]](../ideas/ida-99b87d9-theory-rebuild-as-evaluation.md) tests
reconstruction of project understanding.

## Review method

Read the defining statement and the places that rely on it. For an apparent contradiction, use the
history of the relevant claim to establish which decision is current. Check implementation claims
against the code or the recorded observation.

Correct the stale statement where it appears. Preserve unresolved design questions as questions. A
catalogue of obsolete quotations and line numbers should not become another competing account of the
design.

[[oc-ec73cd4]](oc-ec73cd4-corpus-reads-in-plain-english.md) tracks readability across the corpus.
This outcome tracks consistency and where each claim belongs.
