# Roadmap

The roadmap develops the-valley through useful increments and tests the claims behind each one. The
[user scenarios](./user-scenarios.md) define what must become possible. Phases describe the work and
evidence needed to support them.

The immediate product priorities are a usable interface for improving the-valley and moving the
projects in `gunk-dev/` and `patflynn/` off GitHub. The
[interface proposal](../.the-valley/ideas/ida-23aa413-project-surfaces-are-derived-views.md) and
[Cosmo migration outcome](../.the-valley/outcomes/oc-87deec8-valley-can-host-cosmo.md) identify work
toward those priorities. Their minimum scope and ordering remain to be selected alongside the open
design problems.

## How phases are used

Each phase should:

1. Deliver something useful even if no later phase is built.
2. Leave a coherent system that can continue to be used.
3. Test one main design claim, so a failed validation is understandable.
4. Serve a named user scenario.

A scenario may require several phases. If the implementation plan no longer serves the scenario,
revise the plan. Later phases remain less detailed until experience makes their requirements
concrete.

Completion depends on observed exit criteria. A component's existence is not enough to mark its
phase complete. The plan uses validation gates rather than calendar estimates.

## Phases at a glance

| Phase | Delivers                                                       | Scenario                         | Main validation                                                               |
| ----- | -------------------------------------------------------------- | -------------------------------- | ----------------------------------------------------------------------------- |
| 0     | Durable self-hosted repositories and initial project knowledge | S1                               | Verified restore and a week of real work without GitHub                       |
| 1     | Git ref-event projection and inspection                        | Supports S2 and later automation | Real ref updates are visible; current-state projection is reproducible        |
| 2     | Signed local check evidence                                    | S2                               | The contributor protocol works well for daily changes                         |
| 3     | Request-based integration                                      | S2                               | Protected history advances through evaluated requests                         |
| 4     | Agent contributions with recorded provenance                   | S3                               | Authorized dispatched work lands without further supervision                  |
| 5     | Builds, deployments, and notifications as reactions            | S4                               | Their causal history is recoverable and queryable                             |
| 6     | Independent verification and a trust process                   | Hardens S3; supports S5          | False claims are detected and unknown signers follow explicit admission rules |
| 7     | Discussion, attention routing, and incident memory             | S6; extends S4                   | Review and incident recovery use a durable shared record                      |

S7, contribution by strangers, has no phase. It remains deferred.

Outcome-based work also extends beyond this sequence. Phase 4 assigns work to an outcome, and Phase
5 records completion, but a general scheduler still needs its own design.

## Current state

The initial hosting, mirroring, and restore exercise have been performed. S1 still has unchecked
criteria: a recorded week of independent daily work and a repeatable migration and restore runbook.

Phase 1 has a recorded live validation from 2026-07-17. Local attestations and the integrator are
implemented, and the contributor CLI can submit evidence and a request. Identity compilation and
policy composition have also been added. Their presence does not mark every Phase 2, 3, or 6
criterion complete.

The remaining sections retain the validation sequence and distinguish initial arrangements from the
current workflow. [Contributor protocol](./contribute.md) is the operational guide for submitting a
change now.

The initial deployment uses classic-laddie as the host and Cosmo as the NixOS configuration project
that installs it. Klaus supplies the existing agent workflow. Armstrong is the proposed controller
for external effects, and a Tessera-backed log is the proposed transparency component. These are
reference choices, not requirements that every deployment use the same hosts or tools.

## Phase 0 — durable self-hosting

This phase serves [S1](./user-scenarios.md#s1--repositories-under-my-control-and-recoverable). It
establishes the canonical home and recovery process on which later work depends.

### Delivery

Bare Git repositories are served over SSH on infrastructure the operator controls. The CUE
[host schema](../schema/valley.cue) declares projects, publication mirrors, and backup policy. The
[NixOS module](../flake.nix) installs the reference host from that declaration. Other installers can
consume the same schema.

The initial workflow used direct pushes to `main`. Agents could push a topic branch for manual
review and integration. This kept agent work usable before the integrator existed. That arrangement
is historical; protected projects now use the [request workflow](./contribute.md).

Project knowledge begins as Markdown files containing bugs, outcomes, ideas, and decisions. The
[graph conventions](../.the-valley/README.md) and node schema define their format. Initially, lint
could report invalid content without preventing a direct push. The integrator later makes required
graph checks part of the admission policy.

### Replication and migration

The initial durability design combines three copies: the primary repositories, an independent live
mirror, and periodic encrypted offsite backup. GitHub serves as the transitional live mirror. The
[replication decision](../.the-valley/decisions/dcr-d7952bc-phase0-replication-github-transitional.md)
records this choice.

The host's per-project `mirrors` list controls publication. Replication publishes `main` and tags,
propagates their deletions, and removes other branch heads on the mirror. It does not publish
pending topic branches or modify a hosting service's separate namespaces. Replication is
best-effort: an unavailable mirror does not reject a push to the primary.

The durability requirement is therefore an operational result to verify. Integrated work must reach
at least two independent locations, one offsite, within minutes. Pending work has periodic backup
depth until it integrates. Recovery from the offsite copy must complete within a day and must have
been tested.

Migration starts by establishing copies and checking recovery. The canonical remote then changes per
repository as confidence is earned. A publication mirror can remain during the transition or serve a
later public view. Adding another mirrored project still needs the
[credential decision](../.the-valley/ideas/ida-a0e5d03-second-mirror-identity.md).

The-valley itself is the pilot. Later projects reuse the verified workflow and add their own
verification, automation, and consumer-access requirements.

### Validation

The phase is complete when every item in
[S1's acceptance checklist](./user-scenarios.md#acceptance-criteria) is satisfied. The claim is that
self-hosting can be the reliable and practical home for everyday work.

## Phase 1 — the event log

This phase tests Git ref updates as a useful source of observable events. It supports S2 and the
later automation scenarios; the current integrator can also reconcile directly from Git.

### Delivery

A `post-receive` hook projects ref updates onto NATS JetStream. The event vocabulary includes
ordinary ref changes and the attestation and request namespaces as those appear. `valley tail`
displays events. No automated effect is required to validate this first step.

Git remains authoritative for repository state. Replaying current refs rebuilds the corresponding
projection. It does not recreate every past update or any external event absent from Git.

### Validation

- A real push produces a `ref-updated` event visible within seconds, with the expected object ID.
- Repeated projection of the same repository refs produces identical event sets.

This was validated on the pilot host on **2026-07-17**: a branch push appeared within a second, and
two full ref replays produced identical sets.

Two limits remain explicit:
[bus authorization](../.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md) is needed before an
automated consumer trusts events to act, and
[service isolation](../.the-valley/bugs/bd-500adf7-bus-shares-git-user.md) is needed before wider
network access. Full history retention is separate work.

## Phase 2 — local attestations

This phase tests whether signed local verification is a practical daily workflow for S2.

### Delivery

The `attest` helper runs checks over a committed tree, composes one statement per check, signs the
statements with the host's key, and stores them by subject digest and signer. Native atomic push
publishes the relevant refs.

[Verification](./verification.md) defines pure and effectful claims, the exact text and signature
formats, and the helper's commands. The Nix runner is the reference implementation. Other runners
must be able to satisfy the same portable statement contract.

External transparency publication and witness re-verification are deferred to Phase 6. Local
attestations establish attributable claims, not independent proof that a recognized signer executed
them honestly.

### Validation

- Real changes carry evidence for their required checks and the trees checked.
- Publication uses native atomic Git push.
- Daily use shows that this is preferable to waiting for a separate CI service.

The helper is implemented. The practical preference and sustained-use criteria still require
explicit validation.

## Phase 3 — the integrator

This phase completes the intended S2 workflow: submit a verified change and have it integrated
promptly when its evidence remains applicable.

### Delivery

Receive hooks restrict protected refs and prevent contributors from rewriting attestation refs. The
integrator reads requests, derives required checks from governing policy, verifies the evidence, and
applies the delta. It publishes the resulting outcome.

The current controller polls request refs. Event consumption is not required for that
implementation. [Integration](./integration.md) defines its reconciliation loop, evidence-transfer
rules, and commit point.

Checks are selected from all path classes touched by the diff. A knowledge change therefore takes
the same protected path as code while requiring only the checks its governing policy selects. The
[policy decision](../.the-valley/decisions/dcr-f41f718-declared-verification-policy.md) defines this
behavior.

A change is a delta targeting a stream. The branch carries it. If the target moves, evidence can
still transfer per check when its inputs remain valid. A changed tree alone is not a reason to
repeat every check.

### Validation

- Direct contributor pushes to protected `main` are rejected.
- A request leads to integration without a manual protected-ref write.
- A conflict or stale-evidence case produces a useful `request-stale` result naming the work that
  needs refreshing, without repeated unchanged announcements.
- Invalid evidence is distinguished from stale evidence.

The integrator and review submission path exist. The
[check-definition gap](../.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md) and
[contention risk](../.the-valley/bugs/bd-c324184-occ-starvation-under-contention.md) still need to
be addressed when assessing the integration guarantee.

Changes to the integrator itself also require the bootstrap, approval, and recovery procedures
described in [open questions](./openquestions.md#identity-approval-and-bootstrap).

## Phase 4 — agent contributions

This phase serves S3. A dispatched change follows the same integration process as human work, with
the agent run and its authority recorded.

It follows usable attestations and integration from Phases 2 and 3. Any dispatch consumer acting on
bus events also depends on authenticated events.

### Delivery

Klaus assigns work to an outcome node and agents read and update the graph as part of their work.
Dispatch needs answers about required checks, conflicts, review or approval, and whether a request
can land. Those answers must come from valley records and policy.

An agent run has no signing key or registry entry of its own. The host signs provenance naming the
run's harness, model, prompt and context digests, and recorded delegation. The
[provenance design](../.the-valley/ideas/ida-45178f6-agent-identity-is-provenance.md) describes that
record.

The current helper accepts provenance supplied by a caller. Automatic collection and verification of
delegated authority remain work for this phase and the trust design.

Unattended integration applies where policy authorizes it. The
[core approval decision](../.the-valley/decisions/dcr-9c75283-substrate-adoption-and-the-core.md)
permits exemptions for ordinary work but retains physical approval for policy changes. Agent support
must preserve those requirements.

### Validation

- A Klaus-dispatched change lands without human action between dispatch and landing when the
  applicable policy permits it.
- The accepted record identifies the agent run through signed provenance and distinguishes it from
  human-authored work.
- The assignment names an outcome node, and the work reads and writes knowledge nodes end to end.

The claim is that the contribution workflow supports unattended authorized work. The accuracy of the
recorded provenance still depends on its producer and key custody. A later transparency log can
preserve the statement's publication; it cannot make false provenance true.

## Phase 5 — effectful reactions

This phase serves S4: builds, deployments, and notifications follow changes, and their causes can be
reconstructed.

### Delivery

The proposed Armstrong controller reacts to an integrated change, builds the required artifact, and
performs the configured deployment and notification. Controllers own these reactions independently.

Knowledge changes join the same record. A change that satisfies an outcome can update that node
through the governed contribution path. Landing a commit alone does not establish that every outcome
it mentions is complete; completion links and evaluation need a concrete design.

This phase must also choose durable storage for external-effect history. The bus cannot remain the
only unprotected copy of records required to explain deployments.

### Validation

- Integration causes a build and deployment through a controller without manual coordination.
- The chain for a selected commit is reconstructable from the shared history.
- A change that completes an outcome records that completion, and the history connects the two.

The minimum automation needed to migrate a project may be smaller than the full phase.
[Cosmo's migration outcome](../.the-valley/outcomes/oc-87deec8-valley-can-host-cosmo.md)
distinguishes moving existing configuration and consumers from building a general deployment
controller.

## Phase 6 — trust backstop

This phase strengthens S3 and supports S5, contribution by an invited collaborator whose claims are
not accepted solely because they arrived.

### Delivery

The proposed design adds:

- External attestation publication with a verifiable inclusion proof.
- Independent witnesses that rerun pure checks and report agreement or divergence.
- A trust process that determines admission, restrictions, and revocation from evidence.

A Tessera-backed transparency log is the current proposed publication mechanism. The trust
controller still needs rules for new signers, sampling, thresholds, and response to divergence.

The identity registry and compiler provide part of the access machinery now. They do not replace
these verification and trust decisions.

### Validation

- Every attestation is published to the log with a verifiable inclusion proof.
- A deliberately false pure-check claim is detected by independent verification and affects the
  signer's standing under an explicit rule.
- An unknown signer's change enters only through the defined trust process.
- A real invited collaborator contributes through that process with bounded, revocable access.

These observations would test particular guarantees. They do not establish an assurance level from
the names of the mechanisms alone.

[Verification](./verification.md#trust-and-limits) explains the residual risks. In particular, the
[post-landing recovery problem](../.the-valley/bugs/bd-e63bac8-cascading-revocation-on-divergence.md)
needs an answer for accepted work and external effects; revoking a key does not undo them.

## Phase 7 — feedback and incident memory

This phase serves S6 and develops the attention and discussion parts of S4.

### Delivery

Views collect discussion, evidence, approvals, and outcomes for a change or causal chain. Their
records remain usable independently of a pull-request page.

An attention router selects events that need a person's response. The
[outcome scheduler](../.the-valley/ideas/ida-eac723e-outcome-dag.md) chooses work; it does not
replace this routing decision.

An incident creates durable project knowledge linking the observed failure, suspected cause,
uncertainty, and recovery. Attribution must distinguish evidence from inference.

The graph already exists. This phase adds feedback and incident behavior to the files, agent use,
and recorded changes introduced earlier. The first product interface can expose those existing
records before the full routing and incident system is ready.

### Validation

- A change's discussion, approval, and outcome are readable as one chronology.
- The router brings an event requiring action to the appropriate person without overwhelming them
  with unrelated events.
- A real or deliberately exercised bad deployment produces an incident record with attribution,
  stated uncertainty, and a link to the revert or other recovery.

Attention routing remains a substantive design problem in
[open questions](./openquestions.md#attention-and-work-scheduling).

## Work across phases

**Schemas and compatibility.** CUE already describes host declarations, events, graph nodes,
attestations, identity, and verification policy. New behavior must define how records are validated
and how older producers and consumers remain compatible. Conformance examples are part of that
contract.

**CLI and product interface.** The CLI currently provides commands including `pending`, `review`,
`checks`, `tail`, and `replay`. The native contribution protocol remains available to other clients.
The [CLI graduation proposal](../.the-valley/decisions/dcr-2aa1a12-valley-cli-graduation.md) is
unresolved; continued prototype growth is not already an approved replacement for its lifecycle
rules. A product interface should expose the same state and operations to people and agents.

**Durability.** Every new store needs a recovery and retention contract. Git refs preserve
repository state, but historical ref transitions, run records, deployment events, and later
transparency records need their own explicit treatment. A rebuildable view must not be mistaken for
the only copy of required history.

**Portability.** Nix and NixOS are reference backends. Schemas and observable behavior must remain
implementable elsewhere. A field that only one backend can produce needs examination before it
becomes part of a shared contract.

**Operational experience.** Migration and daily use should reveal which parts are costly or
confusing. Feed those findings into the graph and revise the next increment. The aim is useful
software and a development process that can keep improving with it.
