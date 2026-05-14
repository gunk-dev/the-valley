# Scenarios

End-to-end walk-throughs of how the architecture handles real situations. Forces the design to be honest, and surfaces gaps that the abstract docs miss.

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

**Latency observed by the developer:** `git push` returns immediately. Integration outcome lands within seconds. Deploy completes within minutes. Witness confirmation arrives async — the dev never waits on it.

**Stress points:**

- The integrator policy must decide whether to wait for witness confirmation before integrating. For a trusted, well-attested signer: probably not. For a new signer: probably yes. This is policy, not architecture, but the policy needs to exist.
- The "local checks are sufficient" assumption is load-bearing. If the contributor's environment drifts from canonical (different Nix version, host-specific paths bleeding in), attestations will land but witnesses will reject them. This is a *bug* in the contributor's setup, surfaced as trust-score drift — the system should make this debuggable.

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

- **Agent identity.** What key signs the attestation? Three plausible models:
  - *Ephemeral per-run key* — klaus mints a key, signs it with klaus's own root, key is bound to one run. Strong audit trail, key compromise is bounded.
  - *Long-lived per-agent key* — one key per agent type. Simpler. Compromise has wider blast radius but is easy to revoke at the trust controller.
  - *Delegated from dispatcher* — the dispatching human's key authorizes the agent; the agent signs with a derived key. Aligns trust with the human responsible.
  This is an unresolved design question. The architecture supports any of them — the trust controller doesn't care about key shape, only about confirm/deny rates per signer.
- **Klaus's own events** (agent-completed, pr-created in the current klaus design) become first-class on the bus. The "PR" concept dissolves — what klaus today calls "PR created" is just `integration-requested` with the agent as signer.
- **Agent loops.** An agent reacting to a regression event (Scenario 3) dispatches another agent run. Easy to imagine pathological loops; the architecture needs a cap (klaus's existing run-budget mechanism extends naturally).

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

- **Attribution.** Monitoring rarely *knows* which commit caused a regression. The honest fields here are *attributed_commits* (a window or best-guess) and a confidence. Probably: most recent N deploys in the window where the metric degraded. The thread router and the dispatched agent both treat this as a lead, not a verdict.
- **Auto-rollback vs human-in-the-loop.** Different orgs/repos want different thresholds. This is per-environment policy, not architecture — but the policy needs to be expressible and observable. The rollback controller's decision logic should itself emit its reasoning as events.
- **Trust score implications.** A regression in itself doesn't lower the signer's trust score (regressions ≠ attestation forgery). But a *correlation* — "this signer's changes account for a disproportionate share of regressions" — is interesting. Probably a separate signal from witness divergence, surfaced to humans rather than auto-acted.
- **Closing the loop on the thread.** When the regression is fixed (a follow-up commit, an explicit revert), the thread can mark itself resolved. The "did this thing actually get fixed" event is necessary for the system to know what to forget.

---

## 5. Untrusted contributor

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

- **The trust state machine.** "Co-signed by trusted party" → "trusted enough to auto-integrate" is a discrete transition that needs clear policy. How many? Which kinds of changes count? Does trust decay with inactivity? Should the contributor see their own trust state? All policy questions, not architecture.
- **Threads as the coordination focal point.** This scenario is the most thread-centric — review happens *in* the thread, which subsumes what GitHub calls a PR. The thread holds the request, the diff view, the attestations, the maintainer's comments, the approval event, and the eventual integration outcome. Every event is part of the same chronology.
- **The "approval has authority" claim.** The integrator trusts the maintainer's approval because the maintainer's key is on a known list. That list is itself a piece of policy that needs versioning, audit, and a way to grant/revoke. Probably stored as a signed config event on the bus, not a YAML file in a repo.
- **Sybil concerns.** The trust model is permissive by default — anyone can push a topic branch and request integration. That's fine for invite-only / personal use. For public projects, the request-rate and the priority router need to handle spam. Probably out of scope for the first cut, worth naming.

---

## 4. Cross-repo schema change (sketch)

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

**What this tests:** that events flow across repo boundaries on the same bus, that controllers can be scoped per-repo but consume cross-repo events.

**Open:** how does a consumer *declare* its interest in another repo's events? Per-repo subscription config? Discovery via schema metadata? Probably explicit config — implicit discovery is too magical.

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

From the integrator's perspective, indistinguishable from a human or agent contribution. The dep-updater has its own signing key and its own trust score, accruing confirmations like any other signer.

**What this tests:** time is just another event source. There's no "scheduled workflows" subsystem — scheduling is one controller emitting `scheduled-tick` events.

---

## What these scenarios collectively prove (or fail to prove)

| Architectural claim | Tested by | Result |
| --- | --- | --- |
| Local attestations replace CI gates | 1, 2 | Works if reproducibility policy holds and witnesses are present |
| Integrator pattern handles real cases | 1, 2, 5 | Works; policy variation per-signer is essential |
| Feedback closes the loop post-merge | 3 | Works; attribution is the weakest link |
| Threads subsume PR review | 5 | Works; the "approval has authority" claim needs concrete policy mechanism |
| Cross-repo flows on one bus | 4 | Plausible; subscription/discovery is unsolved |
| Time integrates as just another event source | 6 | Clean fit |
| Agents are first-class contributors | 2 | Architecturally yes; agent identity model unresolved |

## What's still uncovered

- **Forensic walk-back.** Replaying the log to debug "why did this happen?" The architecture promises it works; nothing in these scenarios actually exercises it as a workflow.
- **Multi-author / stacked changes.** A chain of dependent commits with multiple signers. The integrator doc names this as open; scenarios should eventually test it.
- **Recovery from bus loss.** What if the NATS log is corrupted or rebuilt? Per-repo request refs in git provide some durability for in-flight integrations, but a deeper story is needed.
- **Onboarding a new repo.** Not a runtime scenario — addressed in the migration plan.
