# Integration

How a change makes it into `main` (or any protected ref) without a central platform gate.

## The choice: gate vs. controller

The simplest option is a `pre-receive` hook on the bare git repo: every push to a protected branch is intercepted, the hook checks for valid attestations, push is accepted or rejected. This works.

But it makes integration a different *shape* from the rest of the system. Everything else is "controllers reacting to events on the log." A `pre-receive` hook is a synchronous gate at the git boundary — a special case. And its failure mode is a terse line of stderr to whoever ran `git push`.

The alternative — and the one this design adopts — is a **pull-based integrator**: a controller subscribing to events, performing integration as a reaction, emitting outcome events back into the log.

| | Pre-receive gate | Pull-based integrator |
| --- | --- | --- |
| Where policy lives | Hook script on git server | Controller subscribing to events |
| Failure mode | Push error to stderr | Outcome event in log, threadable |
| Multiple policies | Hard (one hook per repo) | Trivial (multiple subscribers) |
| Async validation | No — must answer before push completes | Yes — controller takes its time |
| Re-running on transient failure | Re-push | Re-fire the event |
| Architectural consistency | Special case | Same pattern as everything else |

## The minimal invariant

The bare git repo still needs to prevent *direct* writes to protected refs (`refs/heads/main`, deploy branches, anything authoritative). Otherwise contributors bypass the integrator by pushing directly.

But the gate this requires is trivial: **only the integrator's key may write protected refs**. Everything else — topic branches, attestation refs, integration request refs — is wide open to anyone with push access.

That's a one-line pre-receive hook, or just filesystem ACLs on the bare repo. The complex policy lives in the integrator; the git boundary only enforces the one structural invariant.

## Mechanism

```
contributor                       bus                    integrator
─────────────────────────────────────────────────────────────────────
push topic + attestations ──►  refs updated
                          ──►  integration-requested
                                      │
                                      ├──► fetch topic + attestations
                                      ├──► verify signatures, attestations
                                      ├──► check trust score
                                      ├──► await witness confirmation (if policy)
                                      ├──► check conflicts with main
                                      ├──► integrate (FF / rebase / merge)
                                      └──► emit outcome
                          ◄──  integration-succeeded / -failed
```

Steps in more detail:

1. **Contributor pushes.** Topic branch, attestation refs, anything else relevant — pushed atomically to the bare repo. No protected refs are touched. The push always succeeds at the git layer as long as the contributor has push rights.
2. **Contributor signals intent to integrate.** Three reasonable options:
   - **Bus event** — `integration-requested { repo, branch, attestations, target }`. Most consistent with the rest of the architecture. Canonical mechanism.
   - **Request ref** — push `refs/integration-requests/<name>` alongside the topic branch. Atomic with the push, observable via git, can be queued/dequeued by deleting the ref.
   - **Branch naming convention** — `integrate/<name>`. UX shorthand only. Tooling translates this into one of the above when the contributor runs `git push --integrate`.
   The system probably wants the bus event as the canonical signal, with the request ref as a durable backup that can be replayed if the bus is rebuilt.
3. **Integrator reacts.** Subscribes to `integration-requested` events for repos it owns. Fetches the topic branch and attestations.
4. **Verification.** Runs whatever policy this integrator implements:
   - Verify signatures on the attestations.
   - Validate attestation contents — derivation hashes, claimed checks, signer.
   - Check the signer's current trust score.
   - Optionally wait for or fetch witness re-derivation results.
   - Check the topic branch merges cleanly into the current target.
5. **Integration.** Whatever strategy this integrator implements — fast-forward only, rebase-then-FF, merge commit, squash. The integrator is the only entity with permission to write the target ref; the bare repo enforces this.
6. **Outcome emission.** `integration-succeeded { commit, signer, attestations }` on success, or `request-stale { reason }` when the request cannot progress against current policy. Downstream subscribers (deploy controller, thread view, notification routing) consume the outcome. See [integrator-internals.md](./integrator-internals.md) for the staleness model and the integrator's loop.

## Properties this gives you

- **Merge queue semantics for free.** Multiple requests targeting the same ref get serialized by the integrator. If A is in flight and B arrives, the integrator finishes A, rebases B on the new tip, re-verifies, integrates. Standard queue, no special infrastructure.
- **Multiple integrators are tractable.** Different repos, different branches, different audiences can have different controller instances with different policies. Same event type, different consumer behavior. A fast-forward-only integrator for a deploy branch; a more permissive one for a docs branch; a human-required one for a sensitive path.
- **Bad branches don't pollute protected refs.** A topic with a broken attestation just sits in the repo. The contributor can fix it and re-request, or abandon it. Garbage collection of unintegrated topic branches is a separate housekeeping concern.
- **Failure is observable.** Every integration outcome is an event with full reasoning. Threads for a change accumulate the outcome alongside review comments, build results, deploy status — one chronology, no "merge result hidden in CI logs."
- **Latency is modest and observable.** Push completes in milliseconds; integration follows within seconds. Tooling subscribes to outcome events; humans need not wait staring at a terminal.

## Failure modes worth naming

- **Integrator crash or backlog.** Requests pile up. The bus is durable, so nothing is lost, but contributors see delayed integration. Need an "integrator alive" health event and visibility into queue depth.
- **Conflict with concurrent integration.** Two contributors targeting the same area. The integrator processes serially; the second arrival is a trivial rebase (attestation invariant) if no conflicts, or goes stale if it would change the tree. See [integrator-internals.md](./integrator-internals.md) for the invariance model.
- **Witness disagreement post-integration.** A witness re-derives a pure attestation and the result differs from what the contributor claimed. The change is *already integrated*. The outcome is a `divergence-detected` event, which downstream feeds trust-score recalculation, potential revert events, and a thread for human attention.
- **Compromised contributor key.** Same problem as anywhere — but now bounded by trust score and visible in the log. Trust controller revokes; future requests from that signer require additional gating.

## Open questions

- **Signaling.** Bus event vs. request ref vs. branch convention — best to pick one canonical and treat the others as conveniences. Probably bus event with request ref as a durable replay mechanism.
- **Dependent changes / stacks.** When B depends on A, the integrator needs to know to integrate them as a unit (or in order). Stacked-diff tooling exists; how does it surface in the request event?
- **Long-running topic branches.** A topic branch that integrates incrementally over time (not just once). Does each integration request consume one commit, a range, the whole branch? Probably the request specifies a commit range, and the topic branch tip drifts naturally.
- **Cross-repo integration.** A change in repo A coordinated with a change in repo B. Two integration requests, one logical operation. Possibly a wrapper controller that conditions B's integration on A's success. Out of scope for the first cut.
- **Integrator identity and bootstrapping.** The integrator is itself code in a repo. How does that code get integrated? Likely the integrator integrates itself with a stricter policy (always require human approval on its own changes), but the chicken-and-egg deserves explicit handling.
