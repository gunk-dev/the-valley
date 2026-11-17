# klaus guide

This guide walks the everyday journeys: setting klaus up, launching agent runs, following them, and
seeing their work through GitHub or a valley. Every command, flag and key is in the
[reference](./reference.md); how the pieces fit is in the [design](./design.md).

## Setup

1. Install klaus ([README](../README.md#install)) and the programs it runs
   ([prerequisites](../README.md#runtime-prerequisites)). Authenticate each agent CLI you select on
   every machine that runs it, sandbox hosts included.
2. Optionally run `klaus init`: in a repository it writes `.klaus/config.json` and the worker prompt
   template `.klaus/prompt.md`; elsewhere it writes the host config `~/.klaus/config.json`. klaus
   works without either. The host config holds what a repository must not choose (self-update,
   forbidden terms); a repository's `.klaus/config.json` overrides the rest
   ([configuration](./reference.md#configuration)).
3. Register the repositories you work on, so runs and consults can name them:

   ```bash
   klaus project add acme/widget            # clone into the projects directory and register
   klaus project add acme/site --path .     # register an existing checkout
   klaus project set-dir ~/src              # where project add clones
   klaus sync                               # fetch and fast-forward every registered project
   ```

4. If you keep rules that hold across all your projects, point `house_rules` at the file in a
   registered project's repository; klaus adds it to every worker's brief and to the reviews and
   consults it runs ([`house_rules`](./reference.md#prompts-and-house-rules)).

## Coordinator sessions

Run `klaus` inside tmux. It resumes the most recent coordinator session, or starts one; `klaus new`
always starts a fresh one, and `klaus session --resume <id>` resumes a given one. In a repository
the coordinator gets its own worktree from `origin/<default branch>`; anywhere else it gets a
scratch workspace under `~/.klaus/sessions/<id>/`. Registered projects sync in the background as the
session starts.

The coordinator is an ordinary interactive agent with `klaus` on its `PATH`. You plan with it; it
researches and writes briefs, and launches runs for work that can proceed in parallel. With
`klaus target acme/widget` (or a project name), launches from a session outside any repository go to
that repository by default. A Claude coordinator's memory directory is shared across sessions
through `~/.klaus/memory`; launched agents start clean.

### Choosing backends

The coordinator and the workers are chosen independently, from `claude`, `codex` and `agy`:

```bash
klaus new --backend codex --agent-backend agy     # coordinator codex, workers agy by default
klaus launch --backend claude --effort high --prompt-file brief.md
```

Persistent defaults live in `default_coordinator_backend`, `default_agent_backend` and per-backend
`backends.<name>.model`/`effort`. A resumed coordinator keeps its backend; switch with `klaus new`.
Budgets, budget pause and stored-trajectory replay are Claude-only; the
[backend table](./reference.md#backends) lists what else differs.

## Launching runs

```bash
klaus launch --prompt-file /tmp/brief.md
klaus launch --repo acme/widget --issue 42 --prompt-file /tmp/brief.md
```

Each launch mints a run ID, `<date>-<time>-<8 hex digits>`, fetches the default branch, makes a
worktree on a new branch, installs commit hooks, and starts the agent headless in its own tmux
window. The agent works, runs its checks, commits and pushes; when its turn ends, klaus finalizes
the run: it records cost and outcome, stores the run on the [data ref](./reference.md#the-data-ref),
and removes the worktree.

**Write the brief to a file.** The agent has the repository but none of the conversation: the brief
is its whole briefing. Name what changes and why, the files and functions involved, what done looks
like, and the constraints. `--prompt-file` passes it verbatim; a brief in a shell argument is
subject to the shell's quoting, which expands backticks and `$VARS` outside single quotes. Briefs of
any length work.

**Choose the repository** with `--repo`: a registered project name, `owner/repo` or a URL (cloned
under `<worktree_base>/.repos/`). Without it, launch uses the current repository, then the session
target.

**Model, effort, budget.** `--model` and `--effort` override the backend defaults; `--budget` caps a
Claude run's spend in dollars (default `default_budget`, 5.00). `--reason` labels why a run was
dispatched, for its events and `klaus report time`.

**Branch names** are `<slug>-<run ID tail>`, the slug taken from the `--issue` title or the brief's
first `# ` heading or line, so `# Fix the healthz timeout` becomes
`fix-the-healthz-timeout-bd92f081`. `--branch` names it exactly.

**Where agents run.** Agents run locally unless `sandbox_host` names an SSH host: then the worktree
is synced there and the agent runs remotely, while logging and finalize stay local. `--local` and
`--host <name>` override it per launch ([sandbox](./reference.md#sandbox-hosts)).

How a run delivers follows from the repository's `origin`: on github.com the agent opens a GitHub
pull request <!-- prose-allow: vocabulary --> and the [GitHub pipeline](#runs-on-github) takes over;
anywhere else it pushes its branch ([direct push](#repositories-without-github)), and a repository
that [delivers to a valley](#repositories-that-deliver-to-a-valley) marks it ready there.

## Following runs

### The dashboard

`klaus dashboard` is the live view. It lists rows by what they work on: the runs on one GitHub pull
request <!-- prose-allow: vocabulary --> share its row, and the runs on one change share the
change's row. Rows are grouped by whose move it is:

| Group        | What is in it                                                                                                              |
| ------------ | -------------------------------------------------------------------------------------------------------------------------- |
| **yours**    | runs needing attention (`ATTN`), changes to approve (`READY`) or look at, branches to submit, GitHub rows ready to approve |
| **agents**   | rows with an agent running on them                                                                                         |
| **pipeline** | everything waiting on CI, review, a merge or the integrator                                                                |
| **done**     | merged, closed, integrated, withdrawn, gone, and exited agents                                                             |

`j`/`k` move, `a` approves the selected GitHub row, `o` opens it in a browser, `d` drafts a message
about it into the coordinator's pane, `h` shows hidden done rows, `x` clears finished runs, `tab`
selects a job and `enter` reads it, `q` quits ([all keys](./reference.md#klaus-dashboard)). With
`dashboard.approve_command` set, the footer shows the command that approves the selected `READY`
change, filled in with its handle and head, for you to copy; the dashboard never runs it.

The dashboard also does work: it polls GitHub (or takes webhooks), dispatches fix runs, checks
pushed branches and valley changes every minute, reports transitions as events, and restarts itself
onto a new `klaus` on `PATH`.

### Status, logs and jobs

```bash
klaus status            # every run: status, change, CI, conflicts, merge readiness; then jobs
klaus logs <run-id>     # the live pane, or the formatted saved log (--raw, --replay)
klaus jobs              # running and recent consults, reviews and prose passes
klaus jobs show 1a2b --answer
klaus notifications     # events since you last asked, summarised
```

### Events for the coordinator

Pipeline, valley, consult and review events go to the session's `events.jsonl`. `klaus watch`
streams them: a Claude coordinator arms it in its Monitor tool at startup, so a merge, a landing or
a failed check reaches it between turns. Codex and agy coordinators run `klaus watch --once` as a
background task, which prints the events that arrived and a `next:` line with the command that waits
for the following ones; starting each watch from the last one's `next:` line delivers every event
exactly once. Wait on `change:integrated` rather than polling for a landing
([events](./reference.md#events)).

### Clearing finished runs

`klaus cleanup --done` (or `x` on the dashboard) removes the runs of finished work and the older
runs of open work, keeping each open item's latest run, anything needing attention, and anything
running. `klaus cleanup <run-id>` removes one run; `--force` also removes a running one, discarding
its uncommitted work. `klaus cleanup --all` tears the session down. Removed runs still count in the
session's cost.

## Correcting and continuing runs

Agents run headless: nothing typed into their pane reaches them. To correct one, let it finish or
remove it, and continue its conversation in a fresh run:

```bash
klaus launch --resume-from <run-id> --prompt-file /tmp/correction.md
klaus launch --pr 42 --prompt-file /tmp/review-notes.md           # more commits on a GitHub branch
klaus launch --change nxvp --prompt-file /tmp/review-notes.md     # more commits on a change
```

`--pr` and `--change` work on the existing branch and keep its change ID; `--change` is for
repositories without GitHub. A Claude follow-up replays the previous run's stored conversation when
it is eligible ([trajectory replay](./reference.md#trajectory-replay)). These launches refuse while
another run works on the same branch or change, naming it; wait for it, or pass `--force-takeover`.
They never remove a worktree that has the branch checked out, nor discard local commits that origin
lacks, except that `--force-takeover` resets the local branch to origin's tip.

## Runs on GitHub

Once an agent opens its GitHub pull request, <!-- prose-allow: vocabulary --> the dashboard follows
it through a state machine:

```
ci_pending → ci_passed → approved → merged
    ↓            ↓           ↓
ci_failed   changes req.  conflicts / behind
    ↓            ↓           ↓
fix agent    fix agent    rebase agent / branch update
```

- **CI fails**: a fix agent is dispatched onto the branch (`klaus launch --pr <n>`).
- **Changes requested**, or comments from a `trusted_reviewers` login: an agent addresses them.
- **Conflicts**: a rebase agent resolves them. **Behind the base branch**: the branch is updated
  from the base (`gh pr update-branch`) and merged once CI re-runs.
- **Approved, CI green, no conflicts**: merged automatically when `auto_merge_on_approval` is on.
- A merge or branch update that keeps failing moves to `stalled` after three tries and emits one
  `pipeline:stalled`.

The pipeline never dispatches while a fix run is already working on the branch.

### Review, approval and merge

Approval is the operator's: the coordinator never approves. Approve with `a` on the dashboard or
`klaus approve 40 41`, then `klaus merge 40 41` lands them in order, waiting for CI, rebasing a
conflicting one and verifying it (`merge_verify_command`) before its force-push. With
`require_approval` (the default), merge skips or asks about unapproved ones; `--yes` skips them
without asking.

A comment from a `trusted_reviewers` login dispatches a fix agent even without a formal "changes
requested". Your own conversation comments on an agent's work count only when they start with a
`/klaus fix` line or carry `<!-- klaus-actionable -->` on a line of their own, since you and the
agents post through the same GitHub account.

Each agent requests review from `pr_reviewer`, or from a reviewer klaus picks from the repository's
collaborators ([`pr_reviewer`](./reference.md#configuration-keys)).

### Webhooks

Polling every 30 seconds is the default. For prompt updates, run a relay that forwards GitHub
webhooks to the dashboard, set `webhook.relay_url` and `webhook.secret_file`, and create the hooks:

```bash
klaus webhook check
klaus webhook setup              # or: klaus webhook setup widget
```

## Repositories without GitHub

When `origin` is not on github.com (a plain SSH remote such as `git@192.0.2.10:widget.git`, or a
local path), the agent pushes its branch instead of opening anything, and starts its final report
with the run ID, the branch and its head. The run shows as `awaiting-integration`, and the dashboard
as `SUBMIT <branch>@<short sha>` until klaus sees the work on the default branch (`INTEGRATED`, or
`INTEGRATED~` for a content match) or the branch deleted (`GONE`).

A run that ends without delivering, at a session limit, with uncommitted work, or crashed, is
salvaged: finalize commits what is left, pushes the branch without force, keeps the local branch,
and marks the run `needs-attention` (`ATTN`). Continue it with `--resume-from`, `--pr` or
`--change`; the run that takes the pushed work on carries the attention from then on.

## Repositories that deliver to a valley

A direct-push repository whose head carries `policy/valley` or a `policy/instance/` directory, with
a `valley` on `PATH` that offers `ready` and `changes`, delivers to a valley. After the push,
finalize marks the pushed head ready as a version of the run's change:

```
valley ready --change <change-id> --head <pushed sha> <branch>
```

The change ID is the `Change-Id` trailer klaus stamps on every agent commit
([trailers](./reference.md#commit-hooks-and-trailers)). The valley names the uploader from the
clone's `valley.principal`, which the host sets. The run then reads `READY` (`awaiting-approval`)
until the valley says otherwise: `APPROVED` once you approve it, `INTEGRATED` once it lands, or
`STALE`, `REJECTED`, `CHECKS FAILED`, `CONFLICTING` and the rest
([valley states](./reference.md#valley-change-states)). You approve on your own machine, with the
valley's own `approve`; `dashboard.approve_command` shows the command for the selected row.

### Drafts until review passes

What you approve is what shows as ready, so a change should not show as ready before its review is
done. Set `"ready_at_finalize": false` (host config, or a project's `.klaus/config.json`) and
finalize records the version but returns the change to draft (`REVIEW`, `awaiting-review`). When the
review passes, the coordinator marks exactly the reviewed head ready:

```bash
klaus ready nxvp --head 1a2b3c4
```

`klaus ready` refuses when the change has moved to another head since the review, and while any run
is working on the change.

### Follow-ups on a change

Every follow-up on a change goes through `klaus launch --change <handle|change-id|run-id>`. The run
works on the change's branch and keeps its change ID; launch first returns the change to draft so
nobody approves it mid-rework, and finalize marks the new head ready again as the next version.
Because a valley lands a change as one commit under its head message, the agent amends the head
commit (or ends with a commit describing the whole change), never merges another branch in, and
pushes with a lease on the head it started from, so a push someone made meanwhile makes its push
fail rather than be lost. On the dashboard the follow-up joins the change's row.

### Restack conflicts

After each landing the valley's restacker rebases ready changes onto main. When that conflicts on a
change klaus owns, the dashboard (or `klaus restack-conflicts` once, without a dashboard) returns it
to draft and dispatches a run that rebases onto `origin/main`, resolves the conflicts and pushes;
finalize marks the result ready. It acts once per set of inputs, at most three runs per change until
a clean restack, and never on a change it does not own or one that sits on other changes. When it
stops short, it emits `change:restack-stalled`, and the change becomes yours.

### Failed checks

The valley host runs a ready change's required checks. klaus shows them in the CI column (`CI …`,
`CI ✗`, `CI ✓`), emits `change:checks-failed` and `change:checks-passed`, and dispatches a run to
fix each failed head of a ready change klaus owns, as for failing CI: the brief names the failed
checks, says how to read the worker's build output with `valley telemetry`, and has the run build
every check `valley checks --names origin/main HEAD` lists before it pushes. It stops after three
failed attempts until the checks pass. `klaus failed-checks` does the same once, without a
dashboard.

### Working a knot

On a valley project, `--issue` takes a knot from the project's plan graph, by its ID (`oc-`, `bd-`,
`ida-` or `qn-` and seven hex digits) or as `<project>:<id>` for another project on the same valley:

```bash
klaus launch --issue <knot-id> --prompt-file /tmp/brief.md
```

Launch reads the knot, its parents and comments through `valley knot`, appends them to the brief as
untrusted project notes, names the branch from the knot's title, and claims the knot for the run.
Finalize links the change to the knot and releases the claim; when the dashboard sees the change
land, it comments on the knot and closes it if nothing else keeps it open
([knots](./reference.md#knots)).

### Tracking existing work

`klaus track` adds work no agent of the session made to the dashboard and `klaus status`, GitHub
work and valley changes alike; `klaus untrack` removes it:

```bash
klaus track 405 acme/widget#406            # GitHub, by number, owner/repo#N or URL
klaus track site:vpkz                      # a valley change, by <project>:<handle>
klaus untrack 405 site:vpkz
```

## Cross-model review

Same-family review shares blind spots, so klaus reviews with another model family than the one that
wrote the code. The `_pre-review` an agent runs before delivering already does so. For a second
opinion on open GitHub work:

```bash
klaus review 303                        # reviewer family chosen by cross_review
klaus review 303 --backend codex
klaus review 303 --post                 # submit one COMMENT review
```

The reviewer reads only the diff, title and description, read-only, in an empty directory. A posted
review is a second opinion, never an approval: its inline findings feed the trusted-review fix loop,
and `klaus review` refuses to post twice on one head or beyond `cross_review.max_rounds`
([cross-model review](./reference.md#klaus-review)).

## Prose rewrites

Beside its peer review, `_pre-review` has another model family rewrite the prose a change adds (its
Markdown, READMEs, files `pre_review.prose.paths` matches, and the head commit message), and a third
fact-check the rewrite. It prints each rewrite with any fact-check failure and saves the passing
ones as a patch for `git apply` and a message for `git commit --amend -F`. Nothing is applied for
the agent, and the pass never fails `_pre-review`. Each pass is a `prose` job; finalize marks each
rewrite applied or rejected by whether the worktree has it.

## Consulting other models

`klaus consult` asks another model family a read-only question, without a worker, worktree or
branch:

```bash
klaus consult --role critic --file plan.md --thread auth "Critique this plan"
klaus consult --repo widget "Where is the retry policy decided?"
klaus consult --panel "Polling or webhooks for build status?"
klaus consult --thread design "Draft the migration plan" &   # then: klaus jobs show <id> --answer
```

Roles are `partner`, `critic`, `reviewer`, or your own system prompt. `--thread NAME` keeps a
conversation per topic in the session. `--repo` reads a registered project as origin's default
branch has it, through a fresh snapshot, never the clone's working tree. `--panel` asks every other
installed family at once. Each call is a job, listed by `klaus jobs` and the dashboard
([details](./reference.md#klaus-consult)).

## Budget pause and resume

Budget-paused work waits in a draft GitHub pull request <!-- prose-allow: vocabulary --> labelled
`klaus:budget-paused`. When a Claude agent reaches its `--budget`, klaus commits what is
uncommitted, pushes the branch, opens or updates the draft with a comment saying how to continue,
and cleans up, rather than discarding the work. The dashboard shows it as budget paused, awaiting
your decision. To resume:

```bash
klaus launch --pr 42 "continue the work"
```

The new agent replays the paused conversation when eligible, and its finalize removes the label
(emitting `agent:resumed`). Close the draft to abandon the work, or push commits to redirect it.

## Where agent time goes

```bash
klaus report time                     # the last week, every session
klaus report time --since 2w --repo widget
klaus report time --current --json
```

The report sums runs by dispatch reason, time by phase, checks by kind, idle time between a change's
runs, time from a run to approval, follow-ups per change, consult and review time, and cost where
known ([report fields](./reference.md#run-fields-on-events)).

## Keeping klaus up to date

A host can follow a branch of this repository without a system rebuild. In the host config:

```json
"self_update": { "repo": "git@valley-host:~/the-valley", "branch": "main" }
```

Then run `klaus self-update` from a timer: nothing notifies the host when the remote branch moves.
It resolves the branch's tip, refuses (changing nothing) unless the flake at that commit offers
`packages.<system>.klaus`, builds exactly that package, smoke-tests it, and installs it into your
nix profile as the element `klaus`. Each step prints one line, so the timer's journal reads as a
history. A running dashboard restarts onto the new klaus by itself; running agents finish on the
klaus they started with.

```bash
klaus self-update --rollback   # back to the previous klaus; again to return
klaus self-update --pin        # hold the current klaus until --unpin
klaus self-update --retry      # rebuild a tip that failed before
```

`klaus self-update` installs klaus; `valley self-update` is the valley CLI's own command and does
not touch klaus ([self-update](./reference.md#klaus-self-update)).
