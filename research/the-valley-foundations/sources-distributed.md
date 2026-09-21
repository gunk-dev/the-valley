# Sources: events, trust, and durability

This register supports [Events, trust, and durability](04-events-trust-and-durability.md). All links
were checked on 2026-09-20. “Sections” means the named portions were examined; it does not imply a
full reading of the paper, specification, proofs, implementation, or linked references. None of the
systems below was deployed or benchmarked for this report. Source annotations identify both what is
useful and what the source does not establish for the-valley.

## D01 — Logical causality

**Leslie Lamport. 1978.
[Time, Clocks, and the Ordering of Events in a Distributed System](https://lamport.azurewebsites.net/pubs/time-clocks.pdf).
Communications of the ACM 21(7), 558–565.**

Read: sections on partial ordering, logical clocks, and total ordering, including the
clock-condition distinction. Establishes the difference between causal precedence and a chosen total
order. Essential for interpreting “one history.” It does not supply a model of incident
responsibility or establish completeness of observed events. Read before designing causal references
across valleys.

## D02 — Event sourcing and replay

**Martin Fowler. 2005. [Event Sourcing](https://martinfowler.com/eaaDev/EventSourcing.html).
Author's architecture essay, explicitly marked draft.**

Read: definition, reconstruction discussion, external updates, external queries, and code changes.
Useful for distinguishing replayable domain state from repeating real-world effects. This is a
primary design account, not a formal correctness result or current product specification. Its
external-system discussion is particularly relevant to the actuator proposal.

## D03 — Ref history and expiration

**Git project. [git-reflog](https://git-scm.com/docs/git-reflog). Living command documentation,
accessed 2026-09-20.**

Read: description and expiration options. Documents the operational log of ref updates and its
retention controls. Useful when assessing whether repository state can reconstruct earlier ref
transitions. A reflog's existence on one host is not evidence that the project's backup or
synchronization path retains it. The report does not recommend changing global reflog policy without
an explicit history contract.

## D04 — Ref transactions

**Git project. [git-update-ref](https://git-scm.com/docs/git-update-ref). Living command
documentation, accessed 2026-09-20.**

Read: expected old values, `--stdin`, transaction commands, and concurrent-reader warning. Supplies
the relevant existing primitive for coordinating target, evidence, and journal refs. Its caveat
about readers observing a subset matters to the proposed landing protocol. Further
implementation-specific fault testing is required; this documentation alone does not establish
arbitrary power-loss guarantees for every ref backend.

## D05 — Stream storage contracts

**NATS project.
[Streams](https://github.com/nats-io/nats.docs/blob/master/nats-concepts/jetstream/streams.md).
Official documentation repository, accessed 2026-09-20.**

Read: publication acknowledgement, retention, discard policy, and configuration table. Documents the
choices behind a “durable stream.” The official repository was archived on 2026-08-24; the
historical documentation is linked because former documentation URLs redirected during research.
Check these guarantees against the deployed server and CLI versions before implementation. No
deployment-specific performance claim is inferred.

## D06 — Deduplication and acknowledgements

**NATS project.
[JetStream Model Deep Dive](https://github.com/nats-io/nats.docs/blob/master/using-nats/jetstream/model_deep_dive.md).
Official archived documentation, accessed 2026-09-20.**

Read: message deduplication, acknowledgement modes, exactly-once semantics, and starting positions.
Explains the actual scope of the transport mechanisms. Particularly useful for resisting the
inference that acknowledging a message also commits a remote effect. This describes the documented
mechanism and default duplicate window, not every newer server feature or configuration.

## D07 — Consumer recovery state

**NATS project.
[Consumers](https://github.com/nats-io/nats.docs/blob/master/nats-concepts/jetstream/consumers.md).
Official archived documentation, accessed 2026-09-20.**

Read: consumer model, delivery guarantees, redelivery, and push/pull overview. Establishes that
consumers maintain delivery and acknowledgement state. Relevant to deciding what replay restores and
what a restarted subscriber must remember. It does not establish that application processing is
idempotent or that an external action and acknowledgement are atomic.

## D08 — Reconciliation

**Kubernetes project. [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/).
Living documentation, accessed 2026-09-20.**

Read: controller pattern, API-server control, direct external control, and desired/current-state
discussion. Provides the operational precedent for the implemented polling integrator and proposed
standing-demand controller. This is a conceptual description rather than a proof that any particular
controller converges. It supports borrowing the pattern without adopting the Kubernetes platform.

## D09 — Durable workflow history

**Temporal project. [Events and Event History](https://docs.temporal.io/workflow-execution/event).
Living documentation, accessed 2026-09-20.**

Read: event and activity-event definitions, persisted history, history limits, and recovery
discussion. Explains what Temporal retains to recover an execution. Useful as a comparison for any
custom operation journal. The chapter deliberately avoids relying on exact current limits as a
design constant. It does not evaluate Temporal's operational cost in this project's deployment.

## D10 — Activity failures and cancellation

**Temporal project. [Activity Execution](https://docs.temporal.io/activity-execution). Living
documentation, accessed 2026-09-20.**

Read: lifecycle, timeouts, retries, cancellation, and external asynchronous completion discussion.
Makes the boundary between durable orchestration and fallible activity execution explicit. Relevant
to deployment and agent attempts. It does not promise exactly-once behavior in an arbitrary external
service or that cancellation reverses an effect already accepted there.

## D11 — Transactional outbox

**Microsoft / Azure Samples. 2026.
[Azure Cosmos DB design pattern: Transactional Outbox](https://learn.microsoft.com/en-us/samples/azure-samples/cosmos-db-design-patterns/transactional-outbox/).
Published 2026-07-06; accessed 2026-09-20.**

Read: pattern explanation, transaction boundary, relay, and duplicate-consumer discussion. Shows the
local atomicity requirement behind reliable publication. The concrete implementation is Cosmos DB
specific; the report transfers the pattern, not its product guarantees, to a possible Git journal.
Example code and its crash demonstration were not executed.

## D12 — Compensating long operations

**Hector Garcia-Molina and Kenneth Salem. 1987.
[Sagas](https://sigmodrecord.org/?download_id=11106&smd_process_download=1). ACM SIGMOD, 249–259.**

Read: abstract and introductory long-transaction and reservation examples. Establishes the
motivation for committed subtransactions with compensation. Useful for deployments and coordinated
releases that cannot remain one atomic transaction. This chapter uses the conceptual distinction; it
does not claim to have reviewed the full recovery algorithm or proved that a proposed deployment
compensation preserves an invariant.

## D13 — Independent transaction scopes

**Pat Helland. 2007.
[Life beyond Distributed Transactions: an Apostate's Opinion](https://www.cidrdb.org/cidr2007/papers/cidr07p15.pdf).
CIDR.**

Read: message-versus-method discussion and §5 on retries, idempotence, and substantive behavior. A
strong engineering account of obligations that reappear when transactional scope is local. Relevant
even at small scale because a local process and remote actuator already cross a transaction
boundary. Its large-scale framing is not evidence that the-valley needs large-scale infrastructure.

## D14 — A provenance vocabulary

**Luc Moreau and Paolo Missier, editors; W3C Provenance Working Group. 2013.
[PROV-DM: The PROV Data Model](https://www.w3.org/TR/prov-dm/). W3C Recommendation, 30 April 2013.**

Read: overview, attribution, association, delegation, influence, and bundles. Useful for separating
production claims and authority. Representation does not establish truth or enforce permissions.
Also registered as A04.

## D15 — Operational correlation

**OpenTelemetry project. [Tracing API](https://opentelemetry.io/docs/specs/otel/trace/api/). Living
specification, accessed 2026-09-20.**

Read: span context, identifiers, sampled flags, recording, and links. Useful for correlating
execution detail with retained project decisions, including operations with multiple predecessors. A
tracing API is not a durable audit-store contract. The report does not equate possession of a trace
ID with authenticated provenance or assume all spans are recorded.

## D16 — Ownership and local operation

**Martin Kleppmann, Adam Wiggins, Peter van Hardenberg, and Mark McGranaghan. 2019.
[Local-First Software: You Own Your Data, in spite of the Cloud](https://martin.kleppmann.com/papers/local-first.pdf).
Onward! DOI: 10.1145/3359591.3359737.**

Read: motivation, seven-ideals framing, and selected storage/synchronization comparison sections. A
close conceptual predecessor to repo-carried project knowledge and provider independence. It is a
design and research agenda, not evidence that CRDTs solve every governance conflict. The report does
not claim a complete review of the prototypes or their usability results.

## D17 — Convergence guarantees

**Marc Shapiro, Nuno Preguiça, Carlos Baquero, and Marek Zawirski. 2011.
[Conflict-free Replicated Data Types](https://perso.lip6.fr/Marc.Shapiro/papers/2011/CRDTs_SSS-2011.pdf).
SSS 2011, 386–400.**

Read: introduction and §2 system model, strong eventual consistency, and state-based convergence
conditions. Clarifies which assumptions support automatic merging. Relevant to collaborative
metadata and federation. The cited model assumes non-Byzantine processes; convergence alone does not
establish authorization or arbitrary application invariants. Graph constructions and omitted
companion proofs were not independently checked.

## D18 — Git-backed decentralized collaboration

**Radicle project. [Radicle Protocol Guide](https://radicle.dev/guides/protocol). Living protocol
guide, accessed 2026-09-20.**

Read: identity, delegates, signed references, collaborative objects, and concurrency. Relevant to
portable governance; compatibility with the-valley remains untested. Also registered as S03.

## D19 — Integrated project memory

**Fossil project.
[Fossil Versus Git](https://www.fossil-scm.org/home/doc/trunk/www/fossil-v-git.wiki). Living project
documentation, accessed 2026-09-20.**

Read: features, packaging, and export limitations. Relevant to operational simplicity;
project-authored comparisons are not independent benchmarks. Also registered as S04.

## D20 — Enforcement boundaries

**Jerome H. Saltzer and Michael D. Schroeder. 1975.
[The Protection of Information in Computer Systems: Basic Principles](https://web.mit.edu/Saltzer/www/publications/protection/Basic.html).
Proceedings of the IEEE 63(9), 1278–1308. Author-hosted transcription.**

Read: design principles, especially complete mediation, least privilege, and separation of
privilege. The foundational test for whether the registry and actuator constrain real access paths.
The paper is much broader than the selected excerpt. It does not supply a ready-made delegated-agent
protocol or remove the need to define a trusted computing base.

## D21 — Attenuated delegation

**Arnar Birgisson, Joe Gibbs Politz, Úlfar Erlingsson, Ankur Taly, Michael Vrable, and Mark
Lentczner. 2014.
[Macaroons: Cookies with Contextual Caveats for Decentralized Authorization in the Cloud](https://theory.stanford.edu/~ataly/Papers/macaroons.pdf).
NDSS.**

Read: introductory construction and examples, caveat verification, holder-of-key discussion, and
revocation/versioning. Highly relevant to monotonic delegation. The bearer-token default differs
from the registry's stated credential policy. Formal security arguments, implementation details, and
performance results were not independently audited. The recommendation concerns the semantics before
the token format.

## D22 — Workload authentication

**SPIFFE project. [SPIFFE Overview](https://spiffe.io/docs/latest/spiffe-about/overview/). Living
documentation, accessed 2026-09-20.**

Read: standards overview, short-lived identity documents, and Workload API description. Identifies
an existing interface for workload authentication across heterogeneous infrastructure. It is not an
authorization policy for projects or delegated agent tasks. The ecosystem table was not used as a
product-selection ranking, and no individual implementation's feature coverage was verified.

## D23 — Identity issuance machinery

**SPIFFE / SPIRE project.
[SPIRE Concepts](https://spiffe.io/docs/latest/spire-about/spire-concepts/). Living documentation,
accessed 2026-09-20.**

Read: server/agent architecture, registration entries, node attestation, and workload attestation.
Supplies a concrete comparison for the deferred certificate-issuance service. Potentially compatible
with registry-derived policy, but that integration is a report proposal. Security depends on the
chosen attestors, host boundary, and issuance policy; those have not been selected or tested here.

## D24 — Trust succession

**The Update Framework project.
[Specification](https://theupdateframework.github.io/specification/latest/), version 1.0.36
(2026-08-05).**

Read: §§5.2–5.3, trusted roots and updates. Relevant to succession; initial trust remains separate.
Also registered as V20.

## D25 — Human authentication properties

**W3C Web Authentication Working Group. 2026.
[Web Authentication: An API for accessing Public Key Credentials, Level 3](https://www.w3.org/TR/webauthn-3/).
W3C Recommendation, 25 August 2026.**

Read: scope, single/multi-device terminology, backup eligibility, authenticator flags, and
assertion-binding structure. Corrects overly broad equations between passkeys, non-exportability,
presence, and approval. The standard is large; this is a targeted reading. No approval envelope,
authenticator policy, or browser implementation was validated for the-valley.

## D26 — What a clone preserves

**Git project. [git-clone](https://git-scm.com/docs/git-clone). Living command documentation,
accessed 2026-09-20.**

Read: ordinary, bare, and mirror cloning options, especially custom-ref coverage. Important for
backup acceptance tests because attestations and requests live outside ordinary branch names. This
documents command behavior rather than the repository's actual backup completeness. Neither a
successful clone nor a configured mirror demonstrates a verified disaster recovery.

## D27 — Replication and agreement

**Diego Ongaro and John Ousterhout. 2014.
[In Search of an Understandable Consensus Algorithm (Extended Version)](https://raft.github.io/raft.pdf).
USENIX ATC extended paper.**

Read: leader election, log replication, commitment/safety discussion, and membership-change
explanation. Provides the right scope for claims about agreed replicated state. It is not a
prescription to add Raft to this project. The report did not re-prove safety or use the paper as
evidence for protection against malicious administrators or backup deletion.

## D28 — Actual cause and model assumptions

**Joseph Y. Halpern and Judea Pearl. 2005.
[Causes and Explanations: A Structural-Model Approach. Part I: Causes](https://www.cs.cornell.edu/home/halpern/papers/actcaus.pdf).
British Journal for the Philosophy of Science 56(4), 843–887; author manuscript dated 24
October 2005.**

Read: abstract, introduction, and structural-model discussion, especially variable selection and
counterfactual interpretation. Useful for distinguishing causal explanation from provenance. This is
one influential formal account, not a claim that causation has a single settled definition. Later
examples, proofs, and competing revisions were outside this chapter's reading.

## Repository evidence

The primary local evidence was the [requirements](../../design/requirements.md),
[architecture](../../design/architecture.md), [roadmap](../../design/roadmap.md),
[open questions](../../design/openquestions.md), and
[self-transparency draft](../../design/self-transparency.md). The implementation reading covered
event and identity schemas, ref replay, publication, the integrator's landing and evidence paths,
and registry rendering and installation. The chapter links those files at the corresponding
findings.

The [signal contracts](../../.the-valley/decisions/dcr-62ecc36-signal-contracts.md),
[identity registry](../../.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md),
[valley repository](../../.the-valley/decisions/dcr-2965320-valley-is-its-repository.md),
[agent provenance](../../.the-valley/ideas/ida-45178f6-agent-identity-is-provenance.md),
[delegation](../../.the-valley/ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md),
[actuator](../../.the-valley/ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md), and
[production-DAG](../../.the-valley/ideas/ida-b48bded-production-dags-and-events.md) nodes informed
the interpretation of intended behavior. An idea node is evidence of a proposal, not evidence that
its mechanism exists.
