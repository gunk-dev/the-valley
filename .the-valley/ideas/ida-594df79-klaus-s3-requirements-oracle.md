---
type: idea
id: ida-594df79
status: exploring
title: klaus is the requirements oracle for S3
created: 2026-07-13
source: owner-directed review
---

# klaus is the requirements oracle for S3

**Thesis.** [klaus](https://github.com/patflynn/klaus) — the owner's daily agent orchestrator, S3's anchor in the [ladder](../../design/user-scenarios.md#the-ladder) — is a *working* system whose observed behaviors are S3's requirements. It is GitHub-shaped end to end: PR-indexed pipeline FSM, `gh`-mediated state, webhook-relay feed. The migration story is therefore not "rebuild klaus" but **swap the GitHub feed for valley events** — klaus already anticipates this (its `pr:approval-changed` event is documented as "deliberately backend-agnostic … for non-GitHub merge-readiness sources", `internal/event/event.go`).

## Behavior → valley concept

| klaus behavior (today) | valley concept | phase |
| --- | --- | --- |
| github-relay webhook feed + poll reconcile — events are *invalidation signals*; GitHub API is the state store | event log as the consumer feed (`ref-updated`, …) | 1 |
| `klaus approve` / `auto_merge_on_approval` / trusted-reviewer config | integrator policy | 3 |
| agent opens a PR (hard-coded in the default agent prompt, `internal/config/config.go`) | integration request | 3 |
| `klaus launch --issue` / prompt-carried dispatch | dispatch against an outcome node ([[ida-eac723e]], [ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md)) | 4 |
| run state files + `refs/klaus/data` artifacts + dashboard + `klaus watch` JSONL | one queryable history | 5 |
| trusted-reviewer PR comment → fix-agent dispatch | threads / review-as-feedback | 7 |
| budget-pause: draft PR + `klaus:budget-paused` label + comment *is* the persisted state; `klaus launch --pr` is the only resume | **no valley equivalent yet** — needs paused-work persistence, likely a node + branch | gap |

## Evidence: one week of S1 direct-push (the-valley, qinling)

| # | observed | precise cause in klaus |
| --- | --- | --- |
| 1 | agent pushed a branch, ended as bare `exited`; owner finds work via `git ls-remote` | no event kind carries a pushed branch; `determineStatus` knows only PR/merged states (`internal/cmd/status.go`); default `klaus watch` filter omits `agent:completed` (`internal/cmd/watch.go`) |
| 2 | budget-capped direct-push agent has **no** persistence/resume story | `draft.HandleBudgetPause` pushes WIP fine (pure git) but then needs `gh pr create --draft` + label; on a sovereign origin that fails and `_finalize` falls through to a normal completion — no `agent:paused`, nothing to resume against |
| 3 | no "branch awaiting integration" state anywhere | pipeline FSM keyed by PR number (`internal/pipeline/pipeline.go`); `track`/`approve`/`merge` all take PR refs |
| 4 | a run got labeled with an upstream repo's PR #130 it merely *mentioned* | `_finalize` regex-scrapes any `github.com/.../pull/N` URL from the transcript, last match wins, never checked against the run's target repo (`internal/cmd/hidden.go`) |
| 5 | agent killed by a usage limit died silently; only the log tail said why | crash detection reads only the final `result` JSONL line; a killed stream sets no `FailureReason` → false `agent:completed`; `agent:needs-attention` is also absent from the default watch filter |

Gap 5 is [[ida-3145b7a]]'s stall row "agent run died silently" ([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md)), observed in production — klaus is the spawn-delivery mechanism that idea's demand pressure schedules, and it needs the lease/re-dispatch machinery sketched there.

## What Phases 1/3/4 must minimally satisfy for klaus to swap feeds

- **Phase 1 (events).** `ref-updated` carrying repo, ref, old/new sha, and pusher identity — that alone kills gaps 1 and 5's silence (a branch push and a *missing* expected push are both observable). Remotely subscribable (klaus runs on the workstation, not classic-laddie) and deterministically replayable, since klaus treats events as invalidation + reconcile, never as the store.
- **Phase 3 (integrator).** `integration-requested` addressable by branch/request id, not PR number — klaus's FSM re-keys from PR to request id; `integration-succeeded` / `request-stale` replace what `gh pr view` answers today; plus a *queryable* per-request status, because klaus's controller is level-triggered and reconciles state, it doesn't just react. "Awaiting integration" (gap 3) becomes a first-class stage. `klaus approve` becomes integrator policy input.
- **Phase 4 (dispatch + attribution).** Dispatch targets an outcome node — but klaus doctrine says the prompt is the agent's *entire briefing* (`docs/PIPELINE.md`), so the node body must be brief-quality or dispatch stays prompt+node. Per-agent keys sign commits and attestations: attribution then lives in git objects, which retires gap 4's transcript-scraping for good. Paused work (gap 2) persists as a node + WIP branch — klaus already syncs the resume trajectory over plain git (`refs/klaus/data`), so only the pause *marker* and resume index need valley-native homes.

## Related

- Dispatch semantics and the outcome DAG this feeds: [[ida-eac723e]] ([ida-eac723e-outcome-dag.md](./ida-eac723e-outcome-dag.md))
- Demand pressure / anti-stall, whose failure taxonomy gap 5 instantiates: [[ida-3145b7a]] ([ida-3145b7a-demand-pressure.md](./ida-3145b7a-demand-pressure.md))
- Near-term operational fixes filed upstream: patflynn/klaus issues (branch-push visibility, sovereign budget-pause, PR-URL misattribution)
