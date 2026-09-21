# Events, trust, and durability

the-valley is trying to make a project's history sufficient to explain and govern its operation.
That is a stronger ambition than keeping source code in Git. It reaches into distributed systems,
workflow recovery, authorization, and the conditions under which a record deserves to be called
evidence. The most useful distinction is between **remembering a decision, making its consequences
happen, and knowing what those consequences were**. Each needs a separate contract, even when all
three use the same infrastructure.

This chapter's central judgment is that the project's direction is sound, but its strongest language
about replay and transparency currently exceeds the guarantees visible in the implementation. The
next step should be to specify exactly which facts survive failure and which actions can be retried.
That work would make the existing small tools more dependable without requiring a general workflow
platform.

The analysis uses repository revision `5bb3a7736fe49b0cc4914299b142c0c9bafb6870` and sources checked
on 2026-09-20. “Implemented” below means visible in this checkout, not independently verified on the
deployed host. Code inspection supports the implementation findings; the proposed failure
experiments have not been executed. The [annotated source register](sources-distributed.md) records
reading depth and limitations for D01–D28.

## 1. What exists, and what remains an ambition

The [requirements](../../design/requirements.md) ask for durable project state, agent attribution,
bounded and revocable trust, and a single history from which consequences can be explained. The
[architecture](../../design/architecture.md) chooses Git, a replaceable event bus, independent
controllers, and a governed identity registry. These choices already produce useful machinery, but
they do not yet form the complete system described by the later scenarios.

| Area              | Evidence in this checkout                                                                              | Larger claim still requiring work                                                                                    |
| ----------------- | ------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| Event publication | A detached post-receive hook publishes ref changes; the integrator publishes outcomes.                 | Every historically important transition can be recovered after bus loss.                                             |
| Integration       | A polling controller reads durable request refs and compares evidence before updating a protected ref. | A landing and its durable decision record survive every interruption as one complete result.                         |
| Replay            | `valley replay` enumerates current refs and publishes synthetic creation events.                       | Replay reproduces the original sequence of updates, deletions, judgments, and external effects.                      |
| Identity          | A CUE registry compiles into SSH authorized keys and attestation verifier keys.                        | Every capability, delegation, and recovery action is checked against current governed authority.                     |
| Governance        | Policy and registry changes are described as amendments judged under earlier rules.                    | Runtime behavior, privileged recovery, and external outputs are covered by the proposed self-transparency invariant. |

The relevant implementation is in [the host module](../../nix/valley-host.nix),
[the CLI](../../bin/valley), [the integrator](../../integrator/refs.go), and
[the identity compiler](../../identity/compile.go). Some prose lags the code: the integrator's help
still says no registry compiler exists, although the compiler is present. Conversely, a decision
document's settled status does not demonstrate that every enforcement boundary it describes has
shipped.

This distinction matters because architectural simplification can hide obligations. Saying that the
bus is replaceable is safe only when every irreplaceable fact has another durable home. Saying that
identity is declared in a repository is safe only when running gates check the declaration, or a
clearly identified compilation of it. The design should be judged at these boundaries.

## 2. A Git repository contains several different histories

### State reconstruction is weaker than event reconstruction

An event-sourced system retains the events needed to reconstruct application state. Fowler's account
also identifies two replay hazards: repeating external updates and substituting present-day external
query results for historical ones. Event sourcing therefore requires a replay policy for effects and
observations, not just a stored sequence.
[D02: Event Sourcing](https://martinfowler.com/eaaDev/EventSourcing.html)

Git can carry an event store, but its ordinary commit graph does not automatically record every
action taken against a repository. A commit says what tree it names and which commits precede it. A
branch ref says which object it currently names. Those are different facts from “this ref moved from
A to B in this accepted push.” The same final refs can result from several different sequences of
pushes.

The current [event schema](../../schema/events.cue) acknowledges this directly: replay sets `old` to
the all-zero object ID because a ref's previous value is not derivable from the current repository.
[The replay command](../../bin/valley) runs `git for-each-ref` and publishes one `ref-updated` event
per surviving ref. A deleted ref has no row to enumerate. A branch that moved A→B→C is presented as
a creation at C.

This gives a concrete counterexample to the strongest replay claim. Create a temporary branch,
update it twice, and delete it. Erase the bus. The final ref inventory cannot tell a fresh
subscriber that any of those operations occurred. A clone containing the same current objects and
refs cannot recover the missing transitions merely by inspecting commit ancestry. Some objects may
eventually disappear if nothing retains them.

Reflogs are useful recovery aids, but their standard contract is different from a permanent domain
ledger. Git documents ref-update logs with expiration controls, including default expiration periods
for ordinary and unreachable entries. Making a reflog the authoritative event history would
therefore require explicit retention, replication, and restore rules.
[D03: git-reflog](https://git-scm.com/docs/git-reflog)

There are three separate promises the project could make:

1. **Reconstruct present state.** Rebuild indexes and let controllers converge from durable current
   facts.
2. **Reconstruct decision history.** Recover accepted and rejected decisions with the evidence and
   policy used at the time.
3. **Recover unfinished effects.** Determine which authorized actions remain incomplete or uncertain
   after failure.

The existing ref replay primarily serves the first promise. The S4 “why did X occur” requirement
needs the second. Reliable deployment and notification need the third. None follows from the others.

### The landing has a recording gap

The integrator already treats the target-ref update as a commit point. In
[`land`](../../integrator/refs.go), it prepares signed evidence, moves the target ref using the
expected previous object ID, stores evidence refs, deletes the request, and publishes the outcome.
It attempts the later steps even when an earlier post-commit step fails. That is sensible error
handling within a running process.

A process can also disappear between those steps. [`countersign`](../../integrator/transfer.go)
writes evidence blobs before the target moves, but the refs that make those blobs discoverable are
installed afterwards. A crash immediately after the target update can therefore leave the accepted
commit without its ordinary evidence references. The objects may still exist, but an ordinary
consumer has no complete durable landing record to follow. This is a crash-consistency concern from
inspection, not a claim of an observed production incident.

Git offers transactions over multiple ref updates through `update-ref --stdin`. Its documentation
also warns that a concurrent reader may observe only some modifications even when the transaction's
updates were accepted together. That makes multi-ref transactions a candidate tool, with reader and
crash behavior to test against the selected ref backend. They are not a blanket claim of
database-style snapshot isolation. [D04: git-update-ref](https://git-scm.com/docs/git-update-ref)

**Recommendation.** Make one immutable landing record the durable explanation of a transition. It
should name the previous and accepted heads, request identity, evidence, policy snapshot, and
intended downstream work. Protect the relationship between that record and the target update. A
transaction, a recovery journal, or one authoritative record from which the target is derived are
alternatives to evaluate. Bus publication then becomes a retryable projection of an already recorded
fact.

The objection is extra machinery around a small tool. The answer is to keep the record narrow. The
system need not remember every internal computation. It must remember enough that a restarted
controller does not have to guess whether an accepted change was authorized or which consequences
remain due.

## 3. Causality is not the order of entries in a log

Lamport's happened-before relation orders events connected by process order and message
transmission. It is a partial order: independent events may be concurrent. Logical timestamps can
extend that relation into a total order, but an earlier logical timestamp does not establish that
one event caused another. A total ordering chooses a sequence compatible with known dependencies; it
does not discover the dependencies.
[D01: Time, Clocks, and the Ordering of Events in a Distributed System](https://lamport.azurewebsites.net/pubs/time-clocks.pdf)

For the-valley, an integration event followed by a deployment event in a bus is therefore
insufficient explanation. The deployment might concern a different commit. It might have been
manually requested. A restarted subscriber might publish old work after newer work. The record needs
explicit links: which accepted artifact the controller observed, which policy authorized the action,
and which request identified the intended effect.

“One history” can mean one queryable body of linked evidence without demanding one global sequence
across all valleys. Per-project ordering remains useful at the protected ref. Cross-project
causality can be represented by references to particular decisions or artifact versions. A single
cross-valley total order would add coordination without answering the semantic question of why a
change mattered.

W3C PROV distinguishes production relationships from attribution and delegation. Its delegation
relation describes responsibility; it does not enforce a permission.
[D14: PROV-DM](https://www.w3.org/TR/prov-dm/) The knowledge chapter develops the vocabulary. Here
the practical lesson is to record authorization separately from a claim about who acted.

OpenTelemetry addresses a neighboring problem. Its tracing API supplies trace and span identifiers,
parent relationships, and links across traces; links are especially useful when one operation has
several predecessors. The API also exposes recording and sampling distinctions. These are good
correlation mechanisms, but the tracing specification is not a promise to retain every domain
decision permanently or authenticate its truth.
[D15: OpenTelemetry Tracing API](https://opentelemetry.io/docs/specs/otel/trace/api/)

A practical division follows. Domain records should carry stable causal references even if telemetry
expires. Traces should point to those records and supply operational detail while retained. The
existing [three signal contracts](../../.the-valley/decisions/dcr-62ecc36-signal-contracts.md)
already separate durable events, lossy metrics, and logs retained under policy. The difficult part
is deciding which observation must be promoted before the raw data disappears.

### Provenance does not establish incident blame

A deployment record may prove which artifact ran and who authorized it. It does not prove that this
artifact caused an outage. A database failure, traffic change, or independent configuration update
may have contributed. Halpern and Pearl formalize actual causation using structural models and
counterfactual interventions. Their analysis makes the chosen variables and model assumptions part
of the answer. A dependency graph alone is not that model.
[D28: Causes and Explanations, Part I](https://www.cs.cornell.edu/home/halpern/papers/actcaus.pdf)

The project's incident memory should preserve this distinction. A useful report would separate
observed facts, candidate explanations, discriminating tests, and the current assessment. “Rollback
restored service” is evidence. It can remain inconclusive if rollback also restarted a dependency or
changed traffic. Automated attribution should preserve the observations supporting a conclusion and
the alternatives that remain plausible. A confidence number without that structure would make the
record look more scientific than it is.

## 4. Controllers and workflows solve different recovery problems

Kubernetes controllers repeatedly compare desired and observed state and request changes that reduce
the difference. They can act through the API server or on external systems, and they report observed
state for other controllers to use. This is a strong precedent for durable request refs plus
repeated reconciliation: a missed notification should delay inspection rather than erase an
obligation.
[D08: Kubernetes Controllers](https://kubernetes.io/docs/concepts/architecture/controller/)

The implemented integrator takes this approach. Its [polling loop](../../integrator/main.go) works
without a bus subscription. The
[production-DAG idea](../../.the-valley/ideas/ida-b48bded-production-dags-and-events.md) makes the
same move for future work: events invalidate observations, while durable demand determines what
should progress. This is a better foundation for the outcome engine than encoding obligations only
in chains of callbacks.

Temporal retains an event history for each workflow execution so execution can recover after a
crash. The history records commands and externally reported results, and it has explicit size and
event-count limits. This is a mature example of preserving the progress of a particular operation,
rather than inferring all progress from current desired state.
[D09: Temporal Events and Event History](https://docs.temporal.io/workflow-execution/event)

Temporal's activity lifecycle also makes an essential limit visible. A worker can disappear after
starting an activity; a timeout can trigger a retry. Cancellation is cooperative and may be ignored
by activity code. A workflow engine consequently does not remove the need to reason about actions
that may already have occurred outside the engine.
[D10: Temporal Activity Execution](https://docs.temporal.io/activity-execution)

For this project, the useful comparison is by task:

| Task                                              | State that must survive                             | Natural starting point                           |
| ------------------------------------------------- | --------------------------------------------------- | ------------------------------------------------ |
| Keep a service on an accepted artifact            | Desired artifact and current observation            | Reconciliation                                   |
| Land a pending change                             | Request, evidence, judgment, target state           | Reconciliation with a durable commit record      |
| Execute a deployment with an approval and timeout | Progress, approval scope, deadline, effect results  | Explicit operation state or durable workflow     |
| Run an agent until an outcome is satisfied        | Demand, attempts, budget, reservations, evaluations | Reconciliation around recorded attempts          |
| Send a notification once for a decision           | Effect identity and delivery result                 | Durable effect record plus recipient-aware retry |

These are recommendations, not requirements to adopt Kubernetes or Temporal. A small Git-backed
state machine may be sufficient at the project's intended scale. However, once it implements durable
timers, retries, cancellation, compensation, operation versioning, and recovery, the project is
implementing workflow machinery even if there is no workflow configuration file.

**Opinion.** Keep the bus a transport and keep standing obligations in durable state. Introduce a
workflow engine only when a real operation requires enough sequential recovery logic to justify it.
Compare a small custom controller and Temporal on one deployment experiment. Count operational
dependencies, understandable failure states, and recovery behavior as well as lines of application
code.

The outcome engine needs an additional distinction: the dependency graph says what could be
attempted; it does not say which attempt owns a budget or may publish a result. Attempts need
durable ownership, expiry, and acceptance rules. Otherwise two controllers can satisfy the same
apparent demand twice while both behave correctly against stale observations.

## 5. Reliable messages do not make external effects atomic

JetStream streams define retention through limits and policies. They may discard older messages or
refuse new ones when limits are reached. The official documentation distinguishes ordinary
unacknowledged publication from JetStream publication acknowledged after storage. “Persistent
stream” is therefore an incomplete durability specification without the publication mode, retention
configuration, and recovery source.
[D05: NATS Streams](https://github.com/nats-io/nats.docs/blob/master/nats-concepts/jetstream/streams.md)

JetStream's documented exactly-once mechanisms combine publication deduplication with
acknowledgement confirmation. The standard duplicate window is finite, and deduplication uses the
message ID rather than payload equality. A successful double acknowledgement prevents redelivery
caused by a lost acknowledgement. These are useful transport guarantees; they do not put an
arbitrary deployment API in the same transaction as the consumer acknowledgement.
[D06: JetStream Model Deep Dive](https://github.com/nats-io/nats.docs/blob/master/using-nats/jetstream/model_deep_dive.md)

Consumers separately track delivery and acknowledgements and can redeliver unacknowledged messages.
JetStream exposes pull and push consumers for different processing patterns. Consumer state should
consequently be treated as part of an operational recovery plan, even when the underlying domain
facts can be re-created elsewhere.
[D07: NATS Consumers](https://github.com/nats-io/nats.docs/blob/master/nats-concepts/jetstream/consumers.md)

The current host module sets a bounded file store and creates a stream for `valley.>` using CLI
defaults. Its publishers call ordinary `nats pub`; the inspected calls do not request a JetStream
storage acknowledgement. Publication errors are logged and nonfatal. These are understandable
choices for observational notifications. They do not yet establish durable delivery of an
irreplaceable effect request. The broad subject wildcard also needs review when the planned lossy
metrics namespace is introduced.

The classical publication gap occurs when one operation changes durable state and a separate
operation sends a message. A transactional outbox records the state change and message together,
then lets a relay publish the message repeatedly until acknowledged. Microsoft's implementation
example makes that local atomic boundary explicit and uses idempotent consumers for duplicate
delivery.
[D11: Transactional Outbox](https://learn.microsoft.com/en-us/samples/azure-samples/cosmos-db-design-patterns/transactional-outbox/)

The Git equivalent need not be a database table. A durable landing record can contain the fact that
needs publishing. The relay's cursor is disposable only if the retained records support
re-enumeration and consumers tolerate repetition. The word “outbox” names the atomicity requirement,
not a mandatory storage technology.

The next gap is harder. Consider this sequence:

1. An actuator reads an authorized deployment request.
2. The remote platform accepts the deployment.
3. The actuator crashes before recording the response.
4. Recovery sees a request without a completion record.

Retrying can create a duplicate. Declining to retry can abandon a request that never reached the
remote service. A local log cannot tell these cases apart. The external system must support a stable
operation key, a way to query the original operation, or a compensation whose meaning is acceptable
to the project. Otherwise the honest state is “outcome unknown,” with a defined resolution
procedure.

Helland's treatment of independent transaction scopes puts duplicate handling in application
semantics: an operation is idempotent when repetitions do not repeat the substantive change. The
definition depends on what the application regards as substantive. Repeated observation and repeated
charging are plainly different.
[D13: Life beyond Distributed Transactions](https://www.cidrdb.org/cidr2007/papers/cidr07p15.pdf)

Sagas supply another option: divide a long operation into committed steps with compensating actions.
Compensation restores an acceptable business condition rather than erasing history. For the-valley,
reverting a release can compensate a deployment, but it cannot make an already delivered
notification unseen or undo every external consequence of bad code.
[D12: Sagas](https://sigmodrecord.org/?download_id=11106&smd_process_download=1)

**Recommendation.** The proposed
[actuator](../../.the-valley/ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md) should
own authorization and an effect ledger. Each effect should have a stable identity, target, approved
arguments, deduplication contract, result, and recovery policy. Separate a logical effect from its
attempts. Rebuilding a view must never implicitly issue historical effects again.

## 6. Durability needs a recoverable set of objects

The [durability requirement](../../design/requirements.md#durability) is unusually concrete:
integrated work should reach independent copies, including an offsite copy, within minutes, and a
copy counts only after a verified restore. That is a stronger starting point than merely configuring
backup software. The remaining ambiguity is what must be restored together.

A useful recovery inventory includes source commits, knowledge nodes, policy history, registry
history, attestation refs, requests, decision records, and any effect results that cannot be
recovered from the target system. It also includes the material necessary to interpret and verify
those records. A clean source checkout is insufficient if it omits the evidence namespace that
justified its accepted state.

Git's ordinary clone and mirror modes have different ref coverage. `--mirror` maps all refs and
configures later updates to overwrite those mirrored refs. This matters for custom namespaces such
as attestations and integration requests. An export or backup test must specify which namespaces it
includes rather than assuming that “clone” means all project state.
[D26: git-clone](https://git-scm.com/docs/git-clone)

There are two recovery problems: losing bytes and losing authority. A backup may retain all commits
but restore an obsolete registry that re-enables a revoked key. A correct current registry may be
useless if the sole authorized recovery credential was lost with the host. These cases call for
separate restore checks: content completeness, trustworthy policy continuity, and a safe way to
establish the recovered instance's current authority.

Replication also has a different failure model from backup. Raft maintains an agreed replicated log
through leader election and majority-based commitment under its stated assumptions. Its safety
argument is about agreement despite failures, not protection from every authorized destructive
command or compromised administrator.
[D27: In Search of an Understandable Consensus Algorithm](https://raft.github.io/raft.pdf)

For the-valley, several copies on one host do little for host loss. Several continuously
synchronized copies can faithfully propagate an unwanted deletion. A recovery copy that retains
earlier state serves a different purpose from a live replica. These are consequences of the
project's failure scenarios, not reasons to operate a consensus cluster immediately.

**Recommendation.** Name three observable states for an accepted change: locally committed,
independently replicated, and included in a verified recoverable snapshot. This need not put a
remote round trip on the integration path. It makes the allowed exposure window visible. The
requirement's phrase “integrated means replicated … within minutes” otherwise mixes an immediate
status with an eventual obligation.

A restore exercise should recover one complete accepted change, its author evidence, the
integrator's decision, the policy used, and one downstream effect record. It should demonstrate that
an old revoked credential cannot regain access silently. That experiment would validate much more
than a successful `git fsck`.

## 7. Identity, authority, and evidence should stay distinct

The
[identity registry decision](../../.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md)
contains a valuable rule: a permission belongs in the schema only when an enforcement boundary
checks it. This resists a common failure in authorization design, where richly named roles express
intentions that no running component actually enforces.

Still, four questions must remain separate. Which key authenticated the request? Which principal
does current policy associate with that key? What may that principal do here? What evidence supports
the claims it makes? A machine can authenticate correctly and still supply a false check result. An
honest machine can produce valid evidence while lacking permission to modify a protected policy.

Saltzer and Schroeder's complete-mediation principle requires authority checks across access paths,
including initialization, recovery, and maintenance. Their least-privilege and
separation-of-privilege principles also explain why a signing service and an unrestricted agent
process should not automatically share authority. The relevance is structural: a policy file cannot
constrain an operation that bypasses its enforcement point.
[D20: The Protection of Information in Computer Systems](https://web.mit.edu/Saltzer/www/publications/protection/Basic.html)

### Delegation must narrow what can be done

The proposed
[delegation model](../../.the-valley/ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)
lets agent runs act under a principal without receiving persistent registry entries. Each delegation
narrows authority, and human approval is non-delegable. That avoids treating an ephemeral run as a
durable account. It leaves the enforcement question open: who verifies the claimed chain, and who
prevents a run from using credentials outside it?

Macaroons are important prior art for attenuating authority by adding caveats. Their chained
authentication construction supports restrictions and third-party discharge conditions. They also
discuss expiry and state-based revocation. Their ordinary bearer-credential model is a poor direct
match for the registry's rejection of long-lived bearer secrets, although the paper discusses
holder-of-key restrictions. Study the attenuation semantics before choosing the credential format.
[D21: Macaroons](https://theory.stanford.edu/~ataly/Papers/macaroons.pdf)

A minimal dispatch authorization could name the project, allowed verbs, input or target scope,
expiry, resource budget, parent authorization, and recipient enforcement boundary. A child
authorization would be accepted only if its rights are a subset of the parent's rights. The receiver
would check the entire relevant chain and current revocation policy. These fields are a proposed
experiment, not a settled format.

The most significant choice is whether the run ever receives reusable credentials. If every
privileged action passes through an actuator, the actuator can verify scope and sign receipts. If
the run has a general shell on a host with ambient credentials, a carefully signed delegation chain
describes intended authority while the actual process can do more. The boundary determines the
guarantee.

### Workload identity is a useful implementation option

SPIFFE defines workload identities and short-lived identity documents, with a Workload API for
obtaining them across heterogeneous environments. It provides a basis for mutual authentication. It
does not decide that an authenticated workload may approve a registry amendment or deploy a
particular artifact. Those remain authorization decisions.
[D22: SPIFFE Overview](https://spiffe.io/docs/latest/spiffe-about/overview/)

SPIRE implements this model with a server, node agents, registration entries, and node/workload
attestation. It can issue identities based on configured selectors and conditions. This offers
concrete machinery for replacing repeatedly provisioned raw machine keys, at the cost of another
service and another set of attestation assumptions.
[D23: SPIRE Concepts](https://spiffe.io/docs/latest/spire-about/spire-concepts/)

There is no necessary conflict with a repository as the governance source. In a possible deployment,
the registry would determine permitted workload identities and compile registration policy; SPIRE
would perform issuance. The registry would still govern authority. At the current scale, that may be
more machinery than the problem warrants. The decision should turn on measured credential churn and
revocation needs, not the appeal of a standardized name.

### Expiry is only as strong as its enforcement

The actual [identity renderer](../../identity/render.go) omits expired principals when it
successfully compiles. The host deliberately keeps manually declared SSH keys as a recovery path. A
failed compilation preserves the last good files; it does not cause already installed raw keys to
expire at the SSH server. Thus “expires on this date” currently means removal after a successful
convergence, subject to separate recovery access.

That tradeoff favors avoiding accidental lockout. It should be stated as such. If convergence stops,
the effective revocation delay can exceed the timer interval indefinitely. A stronger policy could
use short-lived certificates, enforce expiry at the receiving boundary, or reject sensitive
operations when the compiled policy is too old. Those alternatives have different availability
consequences.

The claim that access is purely a function of integrated repository bytes also needs a time input
once expiry exists. The more precise model is that access depends on the selected governance state,
the evaluation time, and the enforcement boundary's installed state. Recording those values would
make stale authorization diagnosable.

### Human presence and agent attribution need precise claims

WebAuthn distinguishes user presence from user verification and distinguishes backup-eligible
multi-device credentials from single-device credentials. A generic passkey is therefore not evidence
that its private key can never leave one hardware device. An approval protocol must select and check
the properties it requires and bind the assertion to the intended statement and relying party.
Presence or verification alone does not establish that the person understood and approved that
statement. The approval interface and transaction binding remain part of the trust boundary.
[D25: Web Authentication Level 3](https://www.w3.org/TR/webauthn-3/)

Similarly, the
[agent-provenance proposal](../../.the-valley/ideas/ida-45178f6-agent-identity-is-provenance.md) is
a useful record of inputs, but identical prompts and model identifiers can produce different
executions. A run needs an occurrence identity as well as input provenance. Two identical
experiments are still two observations. A host signature establishes who asserted the provenance; it
cannot by itself establish that an untrusted harness recorded everything accurately.

**Opinion.** Keep persistent principals few, but give every run and every privileged effect a
distinct durable identity. Keep the signer, the claimed producer, the delegating authority, and the
verifier as separate fields. Collapsing them makes records simpler to print and harder to trust.

## 8. Recursive governance needs a declared trust boundary

The [self-transparency draft](../../design/self-transparency.md) proposes that no actor durably
changes the system or its outputs without transparency. The architecture has since added a partial
answer to the bootstrap question: rules at tip N judge the transition to tip N+1. That temporal
induction is coherent. It prevents a proposed amendment from authorizing itself merely by declaring
more permissive rules in its own output.

The Update Framework supplies a concrete succession precedent: a root update requires both old and
new signing thresholds. The client starts with trusted root metadata; it does not manufacture
initial trust.
[D24: The Update Framework Specification, §5](https://theupdateframework.github.io/specification/latest/)

The unresolved part is broader than succession. A root user can replace an executable, edit a bare
repository, change authorized keys, or restore an earlier disk image. An external service can accept
a credential outside the integrator's path. Versioning the intended configuration does not make
these paths disappear. The architecture already recognizes recovery below the machinery; the
invariant must explain how that recovery relates to transparency.

Three interpretations are available:

| Interpretation                        | Meaning                                                              | What would establish it                                                  |
| ------------------------------------- | -------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Governed normal operation             | Supported changes go through recorded policy decisions.              | Enforced gates and a documented privileged recovery path.                |
| Detectable deviation                  | Out-of-band durable changes can be discovered after they occur.      | Independent observation, retained baselines, and evidence of comparison. |
| Prevented deviation within a boundary | No actor inside a specified authority boundary can bypass recording. | A complete mediator with a stated trusted computing base.                |

These interpretations have different costs. The first fits the solo-operator scenario. The second is
valuable when a compromised controller might hide a change. The third requires a much sharper
account of which host, kernel, keys, and external services are trusted. A universal statement
covering the operator's root access and all external outputs would be difficult to defend within the
project's explicit non-goals.

**Recommendation.** State the initial invariant over named enforcement boundaries. Record
exceptional recovery as a separate signed transition when normal operation resumes. Preserve
evidence that an exception occurred, even when it was necessary. Distinguish an authorized
exceptional action from an unobserved action; neither should silently become an ordinary
integration.

This still leaves a research question: what evidence can survive the compromise of the system that
ordinarily records evidence? An independent witness or retained checkpoint can constrain history
rewriting. It cannot demonstrate every unobserved host action. The strongest useful guarantee may be
that accepted project states have an externally checkable chain of authority, while runtime drift is
separately monitored.

Self-modification also has an epistemic limit. An old policy can authorize a new policy correctly
while the new policy is a poor decision. Governance proves the route by which rules changed. It does
not prove that every permitted amendment preserves the project's goals. The knowledge layer's
rationale and evaluations remain necessary.

## 9. Federation should exchange evidence while preserving local authority

Local-first software treats users' local data as primary and seeks useful operation without a
provider's continued permission. The original paper connects offline work, collaboration, ownership,
privacy, and long-term preservation, while treating CRDTs as one promising technical ingredient.
This is a close philosophical fit for the-valley's insistence that project knowledge travels with
the repository. [D16: Local-First Software](https://martin.kleppmann.com/papers/local-first.pdf)

CRDTs offer convergence when replicas receive the same updates under specified assumptions. The
foundational work gives sufficient conditions involving monotonic state merges or commuting
operations. That is useful for some collaborative metadata. It does not by itself decide whether
concurrent approvals satisfy a governance rule or whether two individually valid changes jointly
violate an application invariant.
[D17: Conflict-free Replicated Data Types](https://perso.lip6.fr/Marc.Shapiro/papers/2011/CRDTs_SSS-2011.pdf)

For example, an append-only set of signed review comments is a reasonable candidate for independent
replication. “The release is approved by the current governing set” is a derived judgment that
depends on a particular policy state. Automatically merging both sides of a concurrent membership
change may preserve bytes while producing an unauthorized interpretation. The project should
identify which data can merge freely and which decisions require an owning authority.

Radicle provides an especially relevant detail beyond the
[systems comparison](02-existing-systems.md): a repository identifier anchors the history of its
identity document. [D18: Radicle Protocol Guide](https://radicle.dev/guides/protocol) This deserves
comparison with the-valley's genesis and subsequent policy amendments.

The research opportunity for the-valley is more specific: combine governed integration, transferable
verification evidence, durable project knowledge, and recoverable effects. Radicle deserves an
implementation-level comparison before inventing a federation format. Whether its identity and
collaborative-object rules can carry the-valley's policies remains to be tested; similarity is not
compatibility.

Fossil's integrated project facilities provide another comparison; their scope is covered in the
[systems chapter](02-existing-systems.md). Its own account favors compact deployment.
[D19: Fossil Versus Git](https://www.fossil-scm.org/home/doc/trunk/www/fossil-v-git.wiki)

Fossil challenges the assumption that minimalism always means many independently replaceable tools.
A single program can be easier to operate than several services whose failure contracts must be
composed. the-valley's modularity can still be valuable, especially for verification portability.
The comparison should count the total recovery and maintenance burden, not just the size of each
component.

A useful first federation experiment would send a proposed change, its evidence, and its approval
claims from valley A to valley B. B would verify the material under B's own policy and return an
explicit acceptance or refusal. A statement that A trusts a signer would not automatically become
B's authorization. Imported history would retain its origin and causal references.

Cross-repository release atomicity should be deferred until its meaning is explicit. Two refs moving
together is one possible requirement. Ensuring all deployed consumers remain compatible is another,
harder requirement. A staged schema change with a compatibility interval may serve the scenario
better than a distributed commit protocol. The experiment should begin with the user-visible
invariant, then choose coordination machinery.

## 10. Experiments that would resolve the important uncertainties

The following experiments are proposed research work. They are deliberately small enough to produce
decisive evidence before a broad architecture commitment.

| Experiment                           | Procedure                                                                                                            | What would count as success                                                                                                               |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| History versus snapshot replay       | Create, update, force-move, and delete disposable refs; record notifications; erase only the bus; rebuild.           | The report names exactly which facts return and which do not. Any promised historical facts have a demonstrated durable source.           |
| Interrupted landing                  | Kill a disposable integrator at each step around target update, evidence storage, request deletion, and publication. | Restart yields one accepted transition with discoverable evidence and no indefinitely stranded request.                                   |
| Duplicate external effect            | Use a fake target that accepts an operation and then drops the response; restart the actuator repeatedly.            | One logical effect occurs, or the operation becomes explicitly unknown with a safe recovery procedure.                                    |
| Revocation during failed convergence | Expire a disposable principal while registry compilation fails; test each receiving boundary.                        | Observed access matches a declared maximum staleness policy, including the recovery path.                                                 |
| Complete restore                     | Restore onto a fresh disposable host from an offsite copy without the original machine.                              | One accepted change, its evidence, applicable policy, and downstream result remain verifiable; revoked authority is not silently revived. |
| Federation without shared trust      | Exchange one contribution between two isolated valleys with different signer policies.                               | Transport success never substitutes for local authorization, and refusal explains the unsatisfied policy.                                 |

Two further comparisons would sharpen the solution choice. Implement one interrupted deployment in
both a small controller and Temporal, then compare the amount of durable state and recovery logic
each requires. Export a representative project's knowledge and approval records through Radicle and
Fossil-shaped models, then list what remains awkward or unrepresentable. These are evaluations of
fit, not proposals to migrate the project immediately.

The most promising direction is a small number of durable, independently interpretable records:
proposed change, accepted decision, delegated authorization, intended effect, observed result, and
exceptional recovery. Transport, indexes, and most telemetry can then remain replaceable. The
research burden shifts to proving the relationships among those records and the operations they
govern. That is a tractable version of the project's ambition, and a useful basis for its more
speculative outcome engine.
