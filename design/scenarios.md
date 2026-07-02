# Scenarios

End-to-end walk-throughs of how the architecture handles real situations. Forces the design to be honest, and surfaces gaps that the abstract docs miss. These are solution-space — architecture walk-throughs in event notation; the problem-space counterpart, what a user needs with no mechanisms, is [user-scenarios.md](./user-scenarios.md).

Conventions used in the flows below:

- `▶` is a contributor or external actor doing something.
- `→` is an event landing on the bus.
- `↺` is a controller reacting to an event.
- `✓` / `✗` is an integration outcome.

---

## 1. Solo dev: edit → integrate → deploy

The everyday case. A human edits code, runs local checks, and ships a change.

```
▶ edit, save
▶ git push --integrate topic/x   (atomic: refs/heads/topic/x + refs/attestations/<sha>)
  → ref-updated   topic/x, <sha>
  → integration-requested   { repo, branch=topic/x, target=main, attestations=[A1, A2] }
↺ integrator   verify signature, attestation contents, signer trust score
↺ integrator   topic/x merges cleanly into main, fast-forward
✓ → integration-succeeded   { commit=<sha>, target=main }
↺ deploy controller   build artifact derivation D from <sha>
  → derivation-built   { commit, D, hash }
↺ deploy controller   deploy D to staging
  → deployed   { commit, env=staging, D }
↺ witness   re-derive A1's pure check
  → attestation-confirmed   { commit, attestation=A1 }
↺ trust controller   bump signer's confirm rate
```

**Stress points:**

- Whether the integrator waits for witness confirmation before integrating is policy, not architecture — but the policy needs to exist (trusted signer: probably not; new signer: probably yes).
- The "local checks are sufficient" assumption is load-bearing. Environment drift from canonical surfaces as witness rejections and trust-score drift; the system should make that debuggable.

---

## 2. klaus-style agent change

An agent dispatched by klaus does the work. Same flow as a human, but identity and trust live in different places.

```
▶ klaus launch "fix the auth bug ..."
  → agent-dispatched   { run_id, repo, prompt, agent_key=<key> }
↺ agent (in worktree)   edits, runs local checks, signs attestation with agent_key
▶ agent push --integrate
  → integration-requested   { repo, branch, attestations, signer=agent_key }
↺ integrator   verify signature, look up agent_key trust policy
↺ integrator   policy says "agent changes auto-integrate if attestations re-derive cleanly OR a witness pre-confirms"
↺ integrator   either: (a) wait for witness confirm event, or (b) integrate now and accept the risk
✓ → integration-succeeded   ...
```

**Stress points:**

- **Agent identity is unresolved.** Ephemeral per-run key, long-lived per-agent key, or delegated from the dispatching human — the architecture supports any; the trust controller cares only about confirm/deny rates per signer.
- **The "PR" concept dissolves.** What klaus today calls "PR created" is just `integration-requested` with the agent as signer.
- **Agent loops.** An agent reacting to a regression event (Scenario 3) dispatches another agent run; the architecture needs a cap (klaus's existing run-budget mechanism extends naturally).

---

## 3. Post-deploy regression

A change makes it to production. Some time later, runtime signals indicate something is wrong. The system closes the loop back to the change.

```
↺ monitoring   error rate on /api/foo crosses threshold
  → regression-suspected   { service, indicator, severity, observed_at, attributed_commits=[<sha-1>, <sha-2>, <sha-3>] }
↺ rollback controller   policy: severity >= P1 AND attributed deploy < 30min ago
↺ rollback controller   revert to previous artifact
  → deployed   { commit=<previous-sha>, env=prod, reason=rollback, evidence=regression-suspected }
↺ thread router   open/update threads for each attributed commit
  → thread-event   { thread, kind=regression-attributed, evidence }
↺ priority router   thread now has high-severity event; promote to human attention
↺ agent dispatcher   spawn klaus run: "investigate regression Z, propose fix"
  → agent-dispatched   ...
```

**Stress points:**

- **Attribution.** Monitoring rarely *knows* which commit caused a regression; `attributed_commits` is a lead with a confidence, not a verdict.
- **Auto-rollback vs human-in-the-loop** is per-environment policy — but the policy must be expressible and observable, with the controller emitting its reasoning as events.
- **Trust implications.** A regression alone doesn't lower a signer's trust score (regressions ≠ forgery); a *correlation* of regressions per signer is a separate signal, surfaced to humans rather than auto-acted.

---

## 4. Untrusted contributor

A new contributor wants to land a change. They have no attestation history, so the trust controller has no signal on them.

```
▶ alice (new) git push --integrate topic/welcome-fix
  → integration-requested   { signer=alice, attestations, ... }
↺ integrator   alice has no trust score, policy says "do not auto-integrate"
↺ integrator   create a thread, surface the request as needing review
  → thread-event   { thread, kind=integration-pending, signer=alice }
↺ priority router   surfaces thread to maintainer attention
▶ maintainer   reviews diff in the thread view
▶ maintainer   approves (signed approval event)
  → approval   { thread, by=maintainer, target=alice's integration-request }
↺ integrator   sees approval from someone with integration-approval authority; integrates
✓ → integration-succeeded   { signer=alice, approved_by=maintainer }
↺ trust controller   alice now has 1 successful integration co-signed by a trusted party
   (policy: after N co-signed integrations, alice's attestations can auto-integrate)
```

**Stress points:**

- **The trust state machine.** "Co-signed by trusted party" → "trusted enough to auto-integrate" is a discrete transition needing concrete policy: how many, which changes count, does trust decay.
- **Threads subsume the PR.** Request, diff, attestations, comments, approval, and outcome are one chronology in one thread.
- **Approval authority.** The maintainer's key being on a known list is itself policy needing versioning, audit, and grant/revoke.

---

## 5. Cross-repo schema change (sketch)

armstrong's CUE schema changes. Multiple downstream repos depend on it.

```
✓ integration-succeeded   { repo=armstrong, schema-updated=yes, prev_hash, new_hash }
↺ schema-watcher (in each consumer repo)   notices a dependency's schema changed
↺ schema-watcher   runs validation: does my current usage compile against the new schema?
  → schema-compatibility   { consumer, schema, status=ok|broken }
↺ schema-rollout coordinator (if present)   aggregates compatibility events
↺ schema-rollout coordinator   if all known consumers ok: cut a versioned release of the schema
↺ schema-rollout coordinator   if some broken: open threads in those repos with a fix-suggestion
```

**Stress points:**

- Tests that events flow across repo boundaries on the same bus, with per-repo controllers consuming cross-repo events.
- How a consumer *declares* interest in another repo's events is open — probably explicit config; implicit discovery is too magical.

---

## 6. Scheduled task (sketch)

A weekly dependency bump.

```
↺ time source   weekly tick
  → scheduled-tick   { name="weekly-deps", at=... }
↺ dep-updater controller   runs `nix flake update` in worktrees for repos it watches
↺ dep-updater controller   for each repo: generate change, sign attestation, push, request integration
  → integration-requested   { signer=dep-updater, ... }
```

**Stress points:**

- From the integrator's perspective, indistinguishable from a human or agent contribution; the dep-updater has its own key and trust score.
- Time is just another event source — no "scheduled workflows" subsystem, only a controller emitting `scheduled-tick` events.

---

## What these scenarios collectively prove (or fail to prove)

| Architectural claim | Tested by | Result |
| --- | --- | --- |
| Local attestations replace CI gates | 1, 2 | Works if reproducibility policy holds and witnesses are present |
| Integrator pattern handles real cases | 1, 2, 4 | Works; policy variation per-signer is essential |
| Feedback closes the loop post-merge | 3 | Works; attribution is the weakest link |
| Threads subsume PR review | 4 | Works; the "approval has authority" claim needs concrete policy mechanism |
| Cross-repo flows on one bus | 5 | Plausible; subscription/discovery is unsolved |
| Time integrates as just another event source | 6 | Clean fit |
| Agents are first-class contributors | 2 | Architecturally yes; agent identity model unresolved |

## What's still uncovered

- **Forensic walk-back.** Replaying the log to debug "why did this happen?" The architecture promises it works; nothing here exercises it as a workflow.
- **Multi-author / stacked changes.** A chain of dependent commits with multiple signers.
- **Recovery from bus loss.** Per-repo request refs in git provide some durability for in-flight integrations, but a deeper story is needed.
