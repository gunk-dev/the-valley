---
type: outcome
id: oc-9b94fe4
status: open
title: The corpus states each claim once
created: 2026-08-02
source: design conversation, 2026-08-02
blocked_by: []
---

# The corpus states each claim once

Every claim the corpus makes is stated in one place, and every other place that needs it refers to
that place. Done means a reader who meets a claim twice can tell which statement is current, because
there is only one. The remedy for most items below is a reference replacing a copy, not a deletion.

This node is the inventory. It surveys `design/*.md`, `.the-valley/**`, and `README.md`, and it
records rather than resolves: no contradiction below is settled here, and no `supersedes` edge is
asserted. The four headings separate kinds because they want different remedies — a contradiction
needs a judgement about which claim holds, a duplication needs one copy replaced by a pointer,
overlapping nodes need a decision about whether they are one node, and an internal defect needs a
document repaired against itself.

Dates are authorship dates from `git blame` with two mechanical sweeps of 2026-07-17 ignored — the
100-column rewrap and the disembodied-voice pass. Blaming through them dates most of `design/` to a
single day on which it was not written.

## Contradictions

**An attestation is keyed by commit hash.** `design/contribute.md:28-29` (2026-07-02) stores it "as
a git blob under `refs/the-valley/attestations/<commit-sha>`, fetchable and lookupable by SHA";
`design/roadmap.md:216` (2026-07-16) repeats the ref shape; `design/openquestions.md:93-96`
(2026-07-02) builds a question on it. [[ida-51605e8]]
([../ideas/ida-51605e8-authenticity-not-git-coupled.md](../ideas/ida-51605e8-authenticity-not-git-coupled.md),
2026-07-25) holds that "keying an attestation by commit hash" is a statement "about git's data model
rather than about the change being approved" and is "defective for approvals". The node is the more
recent.

**An agent run signs with its own key.** `design/roadmap.md:306` (2026-07-11): "Per-agent identity.
Each agent signs commits and attestations with its own key"; the same claim is Phase 4's gate at
`design/roadmap.md:48` and its exit criteria at `:329` and `:336`; [[ida-594df79]]
([../ideas/ida-594df79-klaus-s3-requirements-oracle.md](../ideas/ida-594df79-klaus-s3-requirements-oracle.md))
repeats it at `:61` (2026-07-13). [[ida-a8243d2]]
([../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md](../ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md),
2026-07-25) states the opposite in one sentence: "An agent run holds no key of its own." The node is
the more recent.

**Whether agent identity is decided at all.** Beside the two positions above,
`design/openquestions.md:10-12` (2026-07-02) still lists the choice as unmade — "Ephemeral per-run,
long-lived per-agent, or delegated from a human signer? … a choice has to be made" — and
`design/scenarios.md:67` (2026-05-14) says "Agent identity is unresolved". Three states of one
question: decided one way, decided the other, and open.

**Pushed work is in two independent places within minutes.** `design/user-scenarios.md:47-49` and
its ticked acceptance box at `:71` (2026-07-16), `design/requirements.md:82-84` (2026-07-16), and
`design/roadmap.md:124-126` (2026-07-16) all state it unqualified. [[dcr-24d62f7]]
([../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md](../decisions/dcr-24d62f7-publication-mirror-not-review-queue.md),
2026-07-25) narrows it: "S1's 'two independent places within minutes' therefore holds for integrated
work and not for in-flight branches." The decision is the more recent, and one of the three
documents ticks the unqualified claim as met.

**The knowledge convention has no validation.** `design/user-scenarios.md:61-62` (2026-07-16) and
`design/roadmap.md:101-102` (2026-07-11): "No indexer, no events, no validation — the schemas are
documentation until there is an integrator to enforce them."
[.the-valley/README.md](../README.md)`:83-87` (2026-08-02) says the opposite — "The convention is
checked, not enforced. `nix flake check` runs `knowledge-lint`" — and [[ida-1ec03b1]]
([../ideas/ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))`:31-32`
(2026-07-06) already called it "a `nix flake check` derivation today (no integrator needed)". The
graph README is the most recent.

**Builds, verification, and artifacts _are_ derivations.** `README.md:46` (2026-07-02) states that
identity as a constraint; `design/requirements.md:68-69` (2026-07-02) and
`design/verification.md:13-16` (2026-07-02) define pure checks the same way. [[ida-b9f646c]]
([../ideas/ida-b9f646c-nix-backend-not-substrate.md](../ideas/ida-b9f646c-nix-backend-not-substrate.md),
adopted, 2026-07-06) holds that "checks-as-derivations is the reference implementation, not the
contract", and names the README line at `:35-37` as the one to reframe. `design/roadmap.md:492-494`
(2026-07-11) already carries the node's position. The node and the roadmap are the more recent;
three documents still carry the identity.

**A knowledge-only diff requires a signature as a matter of policy.** `design/roadmap.md:262-263`
(2026-07-11): "knowledge-only changes (`.the-valley/**`) take signature plus knowledge lint";
[[ida-1ec03b1]]`:15` (2026-07-06) lists the same pair. [[dcr-f41f718]]
([../decisions/dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md),
2026-07-31)`:182-186` removes it: "The signature is deliberately absent … it is not path-scoped, so
it is not policy." The decision is the most recent.

**What the one structural git invariant is.** `design/architecture.md:104` (2026-07-02): "The bare
repo enforces exactly one thing: **only the integrator's key may write protected refs**."
`design/contribute.md:39-40` and `design/roadmap.md:255` state two rules, adding that "attestation
refs are create-only", and `design/openquestions.md:93-94` reasons from the second one. Same age;
the document that claims to state exactly one thing is the one missing a rule the others depend on.

**Whether the contributor push uses a wrapper command.** `design/contribute.md:16` (2026-07-02):
"native git verbs plus one helper for attestation composition, no `git` wrapper command";
`design/roadmap.md:237`: "The push is one atomic native-git command; no wrapper".
`design/scenarios.md` (2026-05-14) uses `git push --integrate topic/x` at `:23` and `:113`, and
`agent push --integrate` at `:57`. That file is also the only place the ref namespace is
`refs/attestations/<sha>` (`:23`) rather than `refs/the-valley/attestations/`, and the only place a
signing key appears as `agent_key` (`:55-59`). It is the oldest document in `design/`.

**What `blocked_by` may point at.** [[ida-eac723e]]
([../ideas/ida-eac723e-outcome-dag.md](../ideas/ida-eac723e-outcome-dag.md))`:35-36` defines the
edge between outcomes — "`blocked_by` / `blocks` edges between outcome nodes **are** a dependency
DAG" — and defines the frontier at `:66-67` as outcomes with "no open blockers remaining".
`schema/node.cue:48-49` (2026-08-02) says "Any node type may block an outcome", and [[oc-87deec8]]
([oc-87deec8-valley-can-host-cosmo.md](./oc-87deec8-valley-can-host-cosmo.md)) carries
`blocked_by: [oc-f3bcfd0, ida-7638082, ida-62a7a3b, ida-b037dc9, ida-a0e5d03]`. The idea status enum
has no closed state, so four of that outcome's five blockers can never clear and it can never reach
the frontier. The schema comment is the more recent.

**Which replication decision is current.** `design/openquestions.md:105-107` (2026-07-11) records
the mechanism as resolved by [[dcr-db1acbb]]
([../decisions/dcr-db1acbb-hetzner-replication-mechanism.md](../decisions/dcr-db1acbb-hetzner-replication-mechanism.md))
— "push-triggered git-native mirror plus nightly restic offsite backup" — but that node's status is
`superseded`, and [[dcr-d7952bc]]
([../decisions/dcr-d7952bc-phase0-replication-github-transitional.md](../decisions/dcr-d7952bc-phase0-replication-github-transitional.md))
replaced the live layer with GitHub and deferred the sovereign remote. `design/roadmap.md:500-505`
cites the current decision. Written the same day; one of the two points a reader at a superseded
node.

**Whether the valley CLI has graduated.** [[dcr-74c3158]]
([../decisions/dcr-74c3158-valley-cli-lifecycle.md](../decisions/dcr-74c3158-valley-cli-lifecycle.md),
`decided`, 2026-07-13)`:20-23`: it graduates "the moment ANY of these is crossed: it grows past ~300
lines; … it needs dependencies beyond git + coreutils (Phase 1's `valley tail` will trip this)".
`bin/valley` is 580 lines and needs the nats CLI, and `README.md:85-86` describes it as "a plain
shell script, accreting one verb per phase". The only node reconciling this, [[dcr-2aa1a12]]
([../decisions/dcr-2aa1a12-valley-cli-graduation.md](../decisions/dcr-2aa1a12-valley-cli-graduation.md)),
is `status: proposed`. A decided rule and the described state disagree with no decided resolution
between them.

## Duplication

**The path-scoped verification policy.** Held by [[ida-1ec03b1]]`:12-19`. Restated in full at
`design/roadmap.md:261-265`, again at [[dcr-f41f718]]`:22-26`, and again at [[oc-87deec8]]`:66-72`.
The roadmap copy has already drifted, per the signature entry above.

**S1's acceptance checklist.** The list at `design/user-scenarios.md:70-78`. Restated as prose at
`design/roadmap.md:146-151`, in a sentence that begins "The rung owns them" — the copy carries no
checkboxes, so it cannot show which criteria are met.

**The Phase 0 replication mechanism.** Held by [[dcr-d7952bc]]. Restated as a layers table plus
RPO/RTO framing at `design/roadmap.md:112-126`, again at `design/roadmap.md:500-505`, again at
`design/openquestions.md:105-107`, and again at [[oc-9949561]]
([oc-9949561-push-replication.md](./oc-9949561-push-replication.md))`:12-25`.

**Per-repo events are durable in git; the bus is a rebuildable projection.**
`design/architecture.md:50-52`, `design/roadmap.md:181-184`, `design/roadmap.md:485-489`,
`design/openquestions.md:113-116`, and [[bd-d853d9c]]
([../bugs/bd-d853d9c-bus-unauthenticated.md](../bugs/bd-d853d9c-bus-unauthenticated.md))`:17-19`.
Five independent statements of one claim; two of them are in the same document.

**cosmo's gate disappears at migration, and what that gate contains.** [[ida-7638082]]
([../ideas/ida-7638082-host-has-no-check-runner.md](../ideas/ida-7638082-host-has-no-check-runner.md))`:23-28`
inventories the workflows. [[dcr-f41f718]]`:30-35` inventories them again — `nixfmt --check` and the
build matrix over classic-laddie, makers-nix and johnny-walker. [[oc-87deec8]]`:31-34` states the
conclusion a third time.

**A group has exactly one instance.** [[ida-8482624]]
([../ideas/ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md))`:30` holds
it, `design/architecture.md:166-168` states the thin form and says so, and [[dcr-f41f718]]`:44-48`
states it a third time as a premise its own argument rests on.

**The knowledge lint's contents.** Enumerated at [.the-valley/README.md](../README.md)`:83-87`, at
[[ida-1ec03b1]]`:15-17`, and at [[dcr-f41f718]]`:276-278`. Three lists of what the lint checks, each
free to drift as the lint grows.

**The seven unbundled concerns.** Enumerated at `README.md:14-15` and again at
`design/requirements.md:49-53`; `design/architecture.md:22-31` tables them as eight rows, splitting
verification from artifacts. Three enumerations that have to agree and already differ in count.

**The outcome-engine framing.** `README.md:35-40` and [[ida-eac723e]]`:19-23` state it in
near-identical sentences, down to the same worked example — "add this code to a VCS" serving
"deliver a feature users love".

## Overlapping nodes

**[[ida-a8243d2]], [[ida-53ec742]]
([../ideas/ida-53ec742-authority-is-enforced-at-the-effect-boundary.md](../ideas/ida-53ec742-authority-is-enforced-at-the-effect-boundary.md)),
[[ida-f1b39e8]]
([../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md](../ideas/ida-f1b39e8-outbound-effects-pass-through-an-actuator.md)).**
All three are about what a run may do and what enforces it. `ida-a8243d2:54` says a capability model
"needs a mediator which cannot be bypassed"; `ida-f1b39e8:32` says "The actuator is the mediator the
capability model otherwise lacks" and calls it "the effect boundary made concrete", which is
`ida-53ec742`'s subject.

**[[ida-eac723e]], [[ida-3145b7a]]
([../ideas/ida-3145b7a-demand-pressure.md](../ideas/ida-3145b7a-demand-pressure.md)),
[[ida-b48bded]]
([../ideas/ida-b48bded-production-dags-and-events.md](../ideas/ida-b48bded-production-dags-and-events.md)).**
All three read the outcome graph generatively. The latter two each declare themselves an extension
of the first, and both independently argue level-triggered reconciliation against the graph —
`ida-3145b7a:40-46` and `ida-b48bded:43-48` — with pressure as the engine.

**[[ida-45178f6]]
([../ideas/ida-45178f6-agent-identity-is-provenance.md](../ideas/ida-45178f6-agent-identity-is-provenance.md)),
[[ida-a763b3f]]
([../ideas/ida-a763b3f-attestations-are-the-substrate-for-evaluation.md](../ideas/ida-a763b3f-attestations-are-the-substrate-for-evaluation.md)),
[[ida-4ac1125]]
([../ideas/ida-4ac1125-identity-is-derived-not-assigned.md](../ideas/ida-4ac1125-identity-is-derived-not-assigned.md)).**
`ida-a763b3f` is one paragraph saying that `ida-45178f6`'s input list, carried on `ida-d2dc957`'s
leaf, makes a run replayable with inputs varied; `ida-4ac1125:26-28` restates `ida-45178f6`'s claim
as one of its instances and then spends a section on whether it is the same property.

**[[ida-93e4f91]]
([../ideas/ida-93e4f91-changes-not-branches.md](../ideas/ida-93e4f91-changes-not-branches.md)),
[[ida-cbcbb3c]]
([../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md](../ideas/ida-cbcbb3c-attestations-bind-to-a-base.md)),
[[ida-51605e8]].** All three turn on what identifies a change and what the evidence about it binds
to. `ida-cbcbb3c:29-30` restates `ida-93e4f91:13-14`'s definition of a change; `ida-51605e8:23-27`
argues from the same rebase fact to a different conclusion.

**[[ida-b7025b5]]
([../ideas/ida-b7025b5-human-decisions-are-signed-acts.md](../ideas/ida-b7025b5-human-decisions-are-signed-acts.md)),
[[ida-c59c30e]]
([../ideas/ida-c59c30e-approval-signed-by-a-reviewing-agent.md](../ideas/ida-c59c30e-approval-signed-by-a-reviewing-agent.md)).**
Both are about who may sign an approval, and both are motivated by the same failure, stated in each:
an approval gate administered by the agent it gates (`ida-b7025b5:15-18`, `ida-c59c30e:27-29`).

**[[ida-3e87f5c]]
([../ideas/ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md)),
[[dcr-5da1f36]]
([../decisions/dcr-5da1f36-project-is-repo.md](../decisions/dcr-5da1f36-project-is-repo.md)),
[[dcr-0f5d9b1]]
([../decisions/dcr-0f5d9b1-cue-config-host-module.md](../decisions/dcr-0f5d9b1-cue-config-host-module.md))
item 3.** All three state what the unit of declaration is and what travels in its store:
`ida-3e87f5c:12-15`, `dcr-5da1f36:12-13`, `dcr-0f5d9b1:27-29`.

## Internal defects

**`design/roadmap.md:327-339`.** Phase 4's three exit criteria appear twice, separated by a blank
line. Introduced 2026-07-17 by the disembodied-voice sweep, which rewrote one copy of a list it had
duplicated.

**[[oc-87deec8]]`:74-76` against `:104`.** The first says "S1's own last two unchecked boxes: a week
of real human and agent work without GitHub, and a migration-plus-restore runbook"; the second says
"S1's last unchecked acceptance box is that the migration-plus-restore runbook exists". Two boxes
and one box, in one node.

**[[dcr-db1acbb]]`:27-29`.** A trailing "**2026-07-11:** superseded by [[dcr-d7952bc]]" paragraph.
The node states its decision and corrects it at the end, which
[.the-valley/README.md](../README.md)`:49-61` forbids, and the frontmatter already carries
`status: superseded`.

**Four done outcomes assert and then correct themselves.** [[oc-49555c7]]
([oc-49555c7-requirements-from-ladder.md](./oc-49555c7-requirements-from-ladder.md))`:12-17`,
[[oc-2fbcd7b]] ([oc-2fbcd7b-roadmap-rederived.md](./oc-2fbcd7b-roadmap-rederived.md))`:12-17`,
[[oc-fc348f0]] ([oc-fc348f0-hetzner-mechanism.md](./oc-fc348f0-hetzner-mechanism.md))`:12-18`, and
[[oc-9949561]]`:12-25` each state the work in the future tense — "should be re-derived", "Frontier —
nothing blocks this" — and then append a `Done <date>` paragraph correcting it. A reader who stops
halfway is left with the superseded version, the defect named at
[.the-valley/README.md](../README.md)`:59-61`.

**Node ids are derived by two different hash functions.** Of the 51 nodes, 31 ids are the first 7
hex characters of SHA-1 of the slug and 20 are SHA-256, including all seven outcomes; `bd-8a591dc`
(`machine-credentials-never-expire`) matches neither. [.the-valley/README.md](../README.md)`:38`
says only "e.g. first 7 hex chars of a hash of the slug" and `schema/node.cue:74` checks only
`^(oc|bd|ida|dcr)-[0-9a-f]{7}$`, so nothing detects the split and a new node has no rule to follow.

**`design/openquestions.md:3-5` against its own contents.** The header says design-level questions
survive "only when a current or near-term roadmap phase (0–2) needs them". Two `[design]` entries
belong to later phases: the effectful test catalogue at `:90-92` (Phase 6) and the lived event log
as the only witness to destructive ref updates at `:76-86` (S6, Phase 7).

## Open

- Whether a design document may restate a node's claim at all, or must always link to it. Several
  duplication entries above are only defects under the stricter reading.
- Which hash derives a node id, given that half the corpus answers each way.
