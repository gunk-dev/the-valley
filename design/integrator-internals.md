# Integrator internals

How the integration automation actually performs its work, given an `integration-requested` event with a topic branch and attestations. The high-level controller pattern is established in [integration.md](./integration.md); this document fills in the operational detail.

## The integrator's loop

Radically simple. For each `integration-requested` event the integrator owns:

1. Fetch the topic branch and its attestation refs from the bare repo.
2. Verify attestation signatures against keys authorized by current policy.
3. Determine whether the attestation **satisfies current integration policy** for the target ref.
4. Attempt the integration onto the current tip of the target ref.
5. If both succeed: update the target ref, emit `integration-succeeded`.
6. If either fails for any non-trivially-recoverable reason: mark the request **stale**, emit a single `request-stale` event with the structured reason. Stop.

The integrator never tries to be clever on the contributor's behalf. Anything that's not a clean integration of a valid attestation is the branch owner's problem to refresh.

## Attestation invariance under rebase

Attestations cover the **tree hash** and **input hashes** of the change, not the commit hash. This makes a *trivial rebase* (target moved, no conflicts) attestation-invariant: the new commit has a different SHA but the same tree, and the original attestation still applies.

| Scenario | Tree after rebase | Attestation status |
| --- | --- | --- |
| Fast-forward (target unmoved) | Identical | Valid |
| Trivial rebase (target moved, no conflicts) | Identical | Valid |
| Mechanical merge (git auto-resolves conflicts) | **Changed** | Stale (tree no longer matches) |
| Semantic conflict (git can't auto-resolve) | N/A — operation fails | Stale (rebase impossible) |

Only fast-forward and trivial rebase preserve attestation validity. Everything else requires the branch owner to re-attest.

This is a stronger primitive than GitHub's merge queue — they re-run all CI on every rebased commit because their checks aren't pure derivations. Ours are; we can prove invariance for the trivial case.

## Staleness as the unified failure mode

There is no rejection event. There is only **stale** — a state the integrator marks on a request when it cannot progress. The request ref persists; the branch sits where it is; the owner produces fresh attestations or the branch is abandoned.

Causes of staleness, all surfaced through the same mechanism:

| Cause | How the integrator detects it |
| --- | --- |
| Rebase would change the tree (conflict) | Three-way merge produces a non-trivial result |
| Policy added a required check that the attestation doesn't cover | Current policy's required checks ⊄ attestation's covered checks |
| Signer's trust score dropped below the policy's threshold | Trust controller updated; threshold for this target no longer met |
| Witness denied an attestation the policy required confirmation on | Async re-derivation produced a different result |
| Attestation inputs garbage-collected from the cache | Required re-derivation impossible |

All five collapse to: *the attestation no longer satisfies the integrator's policy for this target*. The `request-stale` event carries a structured `reason` field naming which cause(s) applied.

## Policy as a derived query over active principles

The integration policy is not a separate config file. It is **derived from the knowledge graph**.

A `principle` node with `enforced_by: [check:X]` adds X to the required-checks set for the protected refs it `applies_to`. The integrator computes the current policy by querying:

```
principles where status = active AND enforced_by ≠ ∅
```

Adding a principle adds requirements. Superseding a principle drops them. Policy changes go through the same integration mechanism as any other change to the knowledge graph (see [knowledge.md](./knowledge.md)).

Other policy dimensions (trust thresholds per protected ref, integrator key authorizations, witness requirements) are configured similarly — as structured nodes the integrator queries.

## What the branch owner does on staleness

The branch owner has access to a `request-state` query (over bus or git) that returns one of:

- **Pending** — not yet processed.
- **In-flight** — being processed; possibly waiting on witness confirmation.
- **Integrated** — done. The success event is the durable record.
- **Stale** — cannot progress. Includes the structured reason and a hint at remediation.

On stale, the branch owner refreshes:
- For tree-conflict staleness: `git rebase` locally onto the new target, resolve conflicts, re-run `nix run .#attest`, atomic push.
- For policy-required-check staleness: add the missing check to local run, re-attest, push.
- For trust-score staleness: address the underlying signal (incident, drift); not always self-resolvable.
- For witness denial: investigate the divergence (reproducibility bug, environment drift); fix and re-attest.

How the branch owner *does* the refresh is their tooling concern. Manual git + `nix run .#attest` is the baseline. They might opt into klaus-style agents that do it for them.

## Agentic resolution is out of scope

Falls out naturally from the staleness reframe. When attestations go stale, the integrator does nothing — it's the owner's problem. If the owner chooses to dispatch an agent to handle the refresh on their behalf, the agent operates *in the owner's identity context* and produces signed attestations under their key. From the integrator's perspective, fresh attestations show up and integration proceeds.

This pushes the agent-identity question (still open from [scenarios.md](./scenarios.md)) out of the integrator's critical path. Whatever the resolution there, the integrator is unaffected.

The architecture *supports* this pattern — agent dispatch is just a controller reaction to a `request-stale` event scoped to the owner's repos. It's tooling, not integrator policy.

## Queue mechanics

**One queue per protected ref.** Strict FIFO. Concurrent integration across different protected refs (different repos, or different protected branches in the same repo) is fine and runs in parallel.

**Within a queue:** the integrator processes requests one at a time. When a request becomes stale, it's removed from the *active* queue (the request ref still exists in git, just not in the integrator's working set). When the owner pushes fresh attestations, the post-receive hook publishes a new `integration-requested` event and the request re-enters the queue at the back.

**No priority lanes in v1.** Fairness within a queue is FIFO; cross-queue priority is implicit (different queues run in parallel).

**No merge groups in v1.** GitHub's "synthesize multiple PRs into a trial merge and validate together" is an optimization for high-throughput scenarios. Not needed at the scales we're designing for; can be added later as an integrator strategy.

## Failure semantics

| Event | When | Repetition |
| --- | --- | --- |
| `integration-succeeded` | Target ref updated | Once per integration |
| `request-stale` | Transition from pending/in-flight to stale | Once per transition; not repeated while still stale |
| `request-invalid` | Structurally malformed (bad signature, missing data, ref not found) | Once; integrator stops processing the request |

The integrator does **not** spam events. A stale request that sits stale for a week emits one `request-stale` event, not a hundred. The owner queries state when they want to know it.

A `request-invalid` is distinct from `request-stale`: invalid means *can never be processed as-is* (something is broken about the request itself), stale means *can't be processed against current policy* (refresh might fix it).

## Crash recovery

Integrator state is fully derivable from:
- `refs/the-valley/integration-requests/*` in the bare repo (the durable record of what requests exist).
- The bus event stream (the durable record of what has been processed).

On restart, the integrator reads both, computes the set of unprocessed requests, and resumes. No internal database, no per-integrator state file. The integrator process is stateless modulo configuration.

## Open questions

- **Policy bootstrap.** Someone has to be able to land the first principle / the first policy change before the policy exists to govern principle changes. v1: integrator-key-holders can land changes to `.the-valley/principles/` directly with relaxed policy. Worked out in a follow-up.
- **Per-repo integrator configuration.** Where does the integrator's per-repo configuration live — which protected refs, which trust thresholds per ref, which witnesses to wait for? Probably as a `policy` or `config` node in the knowledge graph, queried alongside principles.
- **Cross-repo coordinated integration.** Two requests in two repos that must succeed together (schema producer + consumer). The integrator pattern can support this — a wrapper controller conditions B on A — but the design is deferred to v2.
- **Backpressure visibility.** Contributors should see queue depth and estimated wait time. Easy to expose via the same `request-state` query; not strictly part of v1.
- **Stale-request expiry.** A request that sits stale forever clutters the namespace. Probably a periodic controller that emits `request-abandoned` events after $T of staleness with no owner action, allowing tooling to clean up the request ref.
