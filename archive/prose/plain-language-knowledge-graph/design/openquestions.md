# Open questions

This document indexes unresolved requirements, architectural choices, and detailed design questions.
Labels identify those levels. Linked graph nodes carry the fuller reasoning and implementation
findings.

Some early questions now have decisions or working implementations. They are summarized under
[decisions already made](#decisions-already-made) so readers can distinguish remaining work from
obsolete uncertainty.

## Product interface and migration

- `[design]` **First interface scope.** What is the smallest useful interface for navigating the
  graph, reviewing changes, and improving the project? The
  [interface proposal](../.the-valley/ideas/ida-23aa413-project-surfaces-are-derived-views.md)
  considers editing, private access, selected public views, and browser-based approval. Its scope
  and sequence are still open.
- `[design]` **Migration requirements for each project.** Hosting alone does not replace checks,
  automation, deployment reads, or browsing. The
  [Cosmo outcome](../.the-valley/outcomes/oc-87deec8-valley-can-host-cosmo.md) identifies the
  dependencies for the next infrastructure project. Which capabilities must the first migration
  deliver, and which can remain in existing tools?

## Identity, approval, and bootstrap

- `[architecture]` **Verify delegated authority.** Agent runs use host-signed provenance and do not
  hold their own registry keys. How does an enforcement point verify the recorded chain and the
  scope granted to a run? Recording the chain does not establish that it was valid.
  [Agent authority](../.the-valley/ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)
  and [provenance](../.the-valley/ideas/ida-45178f6-agent-identity-is-provenance.md) define the
  intended distinction.
- `[design]` **Deliver physical approval.** The
  [core decision](../.the-valley/decisions/dcr-9c75283-substrate-adoption-and-the-core.md)
  establishes default physical approval and permitted exemptions. The usable signing interface,
  exact change identity, envelope, and verification rules still need implementation. The core must
  not activate before a person can satisfy it.
- `[architecture]` **Trust for a new contributor.** What evidence permits a previously unknown
  signer to contribute? Independent verification, initial restrictions, thresholds, and later
  revocation need concrete rules. A lack of history is not evidence of reliability.
- `[architecture]` **Update and recover the integrator.** Previous accepted policy governs ordinary
  updates, and the initial installation precedes enforcement. The complete procedure for replacing
  broken integration machinery or recovering from compromise of the initial governing identity
  remains open.
- `[requirements]` **Record changes to the system itself.** The
  [self-transparency proposal](./self-transparency.md) asks which persistent changes must be
  governed and recorded, including emergency repairs outside the normal integration path. Its full
  scope and enforcement limits remain unsettled.
- `[design]` **Credential convergence and expiry.** The identity compiler now omits expired machine
  keys on a successful compile; the original
  [non-expiry bug](../.the-valley/bugs/bd-8a591dc-machine-credentials-never-expire.md) is closed. A
  failed convergence can retain older compiled credentials. Provisioning, timely revocation, and
  recovery must be tested together. The
  [access verbs](../.the-valley/decisions/dcr-e544f20-access-is-verbs-on-projects.md) also need
  their project and bus enforcement points.

## Integration and evidence

- `[design]` **Trusted check implementations.** The policy resolver reads governing state, but the
  attestation and integration tools still evaluate check definitions from the candidate tree. The
  [check-definition bug](../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)
  tracks how a mandatory check can be weakened there. How are the canonical tool and required check
  definitions distributed, pinned, and enforced at the runner?
- `[architecture]` **Progress under contention.** How does repeatedly invalidated work get a fair
  chance to land? The
  [starvation risk](../.the-valley/bugs/bd-c324184-occ-starvation-under-contention.md) describes the
  conditions to measure. Batching, narrower valid input closures, reservations, and faster
  verification are candidates, not adopted queue policy.
- `[design]` **Conflicting notes for a check.** The verdict rejects invalid selected evidence. The
  [collector](../integrator/evidence.go) keeps an admissible note once found, but can replace
  earlier invalid evidence with a later admissible note. How should invalid or contradictory notes
  be retained and reported so they cannot be silently ignored?
- `[design]` **Reconsider after governing state changes.** The current
  [request memo](../integrator/refs.go) compares the request head, target tip, and attestation
  namespace. How should changes to the valley floor or accepted signers invalidate a remembered
  answer when those three inputs stay unchanged?
- `[architecture]` **Recover after accepting a false claim.** Revocation can restrict a signer's
  future work, but does not undo accepted changes or their effects. The
  [recovery risk](../.the-valley/bugs/bd-e63bac8-cascading-revocation-on-divergence.md) asks how to
  identify actual dependent work and choose a response. Later commits are not automatically invalid
  merely because they follow a bad change.

## Cross-repository coordination

Coordination within a valley shares governance and can use common services. Coordination across
valleys must also cross independent policy and access boundaries. Shared infrastructure does not by
itself provide atomic integration.

- `[architecture]` **Related changes in two repositories.** How should a producer and consumer
  change be ordered or accepted together? A controller can make one depend on the other, but
  dependency ordering alone does not provide an all-or-nothing update. The within-valley design is
  deferred; the cross-valley case is later work.
- `[architecture]` **Feedback from a consumer.** If a change in one project breaks another, how does
  the failure reach the relevant change discussion? Within a valley this needs routing. Across
  valleys it also needs authorized transport and attribution.
- `[architecture]` **Contribution to a shared project.** The-valley is common software but is itself
  a project owned by one valley. How does another valley carry changes, attestations, and approvals
  to it for evaluation under the owner's policy? The
  [federation proposal](../.the-valley/ideas/ida-8482624-federation-groups.md) records this case.

## Valleys and hosts

- `[architecture]` **Bus isolation.** Should each valley have a bus, or should one bus use
  authenticated subjects scoped to each valley? The current bus is local and unauthenticated.
  [Bus authorization](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md) and
  [host isolation](../.the-valley/decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md) define the
  requirements. Sharing a host must not imply sharing event visibility.
- `[design]` **Mirror credentials.** Mirroring another repository needs a credential arrangement.
  Per-repository keys and a broader machine account have different access and configuration costs.
  The [mirror identity proposal](../.the-valley/ideas/ida-a0e5d03-second-mirror-identity.md) records
  this choice.
- `[design]` **Second-valley deployment.** The separation model is decided, but Unix naming,
  filesystem layout, and the services needing separate users still need concrete configuration and
  validation.

## Attention and work scheduling

- `[architecture]` **Route human attention.** Which events require a person, how urgently, and how
  should they be grouped or escalated? Rules, digests, and learned priorities are possible
  approaches. Outcome scheduling chooses work; it does not answer this question.
- `[design]` **Bound continued work.** How are repeated attempts, decomposition, and spending
  limited across an entire requested outcome? The
  [unbounded-work risk](../.the-valley/bugs/bd-5cb2034-unbounded-loops-under-demand-pressure.md)
  explains why per-run budgets alone do not answer this.

## Storage, retention, and evolution

- `[design]` **Retain evidence inputs.** Which source inputs, derivations, and outputs must remain
  available for independent re-verification, and for how long? Losing a cached output differs from
  losing the source needed to rebuild it. Pure evidence has no time-based expiry in the integration
  rule, but that does not establish a retention promise.
- `[architecture]` **Evolve schemas.** How do event and statement producers and consumers negotiate
  versions and migrations? CUE validates a selected schema; compatibility across versions needs its
  own rules.
- `[architecture]` **Keep cross-system history durable.** S4 requires the causes of deployments and
  notifications to survive. Records that exist only on the bus cannot be reconstructed from
  repository refs. Phase 5 must choose a durable event store or establish replication and recovery
  for the relevant bus records.
- `[design]` **Preserve destructive ref history.** Replaying current refs cannot recover all
  force-pushes or deletions. Server-side reflogs could retain earlier ref values, subject to their
  own expiry and object retention. S6 needs the history that actually occurred, not just a
  current-state projection. An incidental filesystem backup of JetStream is not a demonstrated
  history-retention contract.
- `[architecture]` **Store frequent task updates.** The
  [Git churn risk](../.the-valley/bugs/bd-c4d069b-git-churn-from-task-state.md) asks whether
  high-frequency runtime state belongs in Git. There is no measured workload yet. Any alternative
  store must specify what survives failures and which transitions remain auditable.
- `[design]` **Replace or continue the CLI prototype.** The
  [graduation proposal](../.the-valley/decisions/dcr-2aa1a12-valley-cli-graduation.md) remains
  undecided. Continuing under a specification-coverage rule would require explicitly replacing the
  existing size and dependency limits.

## Later work

`[design]` An effectful-test catalogue belongs with Phase 6: identify which checks can use a
controlled VM or sandbox and which observations inherently depend on the live world. The goal is to
improve what can be independently verified without overstating reproducibility.

`[requirements]` Public repository discovery remains outside the current scope. It becomes a
question to revisit only if open contribution becomes a supported scenario.

## Decisions already made

| Earlier question                          | Current answer                                                                           | Remaining limit                                                                    |
| ----------------------------------------- | ---------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Initial replication mechanism             | A Git-native publication mirror plus periodic offsite backup                             | Migration and restore must be repeatable for each project                          |
| Attestation identity and multiplicity     | Tree-digest subjects; separate evidence by signer; sibling signatures over one statement | Retention and independent verification remain open                                 |
| Whether the bus holds repository state    | Git holds authoritative repository state; the bus projects it                            | Current-state replay does not recover all history or external effects              |
| How to choose work from project knowledge | Outcomes and dependencies are the adopted direction                                      | Scheduling mechanics, fairness, and spending limits remain open                    |
| Initial identity model                    | A governed registry and compiler replace manually maintained key lists                   | Fine-grained enforcement, delegation verification, and physical approval need work |

The [roadmap](./roadmap.md) identifies the scenarios and validation work these questions affect.
