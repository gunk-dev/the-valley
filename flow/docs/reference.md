# flow reference

The complete surface of flow, the agent-run client that `valley flow` runs: commands and flags,
configuration, environment, files, trailers, the data ref, events, and the constraints the code
enforces. `valley flow` runs a separate program, `valley-flow`, installed with its package; without
it on `PATH`, `valley flow` prints one line and exits 127. The [guide](./guide.md) tells the
everyday story; the [design](./design.md) explains the mechanisms.

In this reference, **GitHub work** is a GitHub pull request <!-- prose-allow: vocabulary --> that an
agent opened or that flow tracks, and `<pr>` names one by number, URL or `owner/repo#N`. A
**change** is a valley change; its **handle** is the shortest unique prefix of its change ID.

## Commands

| Command                                                 | Purpose                                                       |
| ------------------------------------------------------- | ------------------------------------------------------------- |
| `valley flow`, `valley flow session`, `valley flow new` | Resume or start an interactive coordinator session            |
| `valley flow launch`                                    | Launch an autonomous agent run                                |
| `valley flow status`                                    | Every run of the session, then running and recent jobs        |
| `valley flow queue`                                     | What waits on the coordinator, then on the operator           |
| `valley flow dashboard`                                 | Live TUI of runs, changes and jobs; runs the pipeline         |
| `valley flow logs <run-id>`                             | A run's output                                                |
| `valley flow watch`                                     | Stream the session's events, or wait for the next ones        |
| `valley flow notifications`                             | Events since the last check, summarised (alias `notif`)       |
| `valley flow jobs [show <id>]`                          | Consults, reviews and prose passes                            |
| `valley flow consult`                                   | Ask another model a read-only question                        |
| `valley flow review <pr>`                               | Second-opinion review of GitHub work by another model family  |
| `valley flow approve`, `valley flow merge`              | Approve GitHub work; merge it in order                        |
| `valley flow ready <run-id\|handle>`                    | Mark a valley change ready once its review passes             |
| `valley flow track`, `valley flow untrack`              | Follow work no agent of the session made                      |
| `valley flow failed-checks`                             | Dispatch fixes for failed valley checks, once                 |
| `valley flow restack-conflicts`                         | Dispatch runs for valley restack conflicts, once              |
| `valley flow cleanup`                                   | Remove runs' worktrees, panes and state                       |
| `valley flow push-log <run-id>`                         | Store a log held back for sensitivity on the data ref         |
| `valley flow report time`                               | Where agent wall-clock time went                              |
| `valley flow target`                                    | Set or show the session's default repository                  |
| `valley flow project`, `valley flow sync`               | The project registry; fetch every registered project          |
| `valley flow scaffold <name>`                           | Create a GitHub repository and have an agent scaffold it      |
| `valley flow init`                                      | Write default configuration                                   |
| `valley flow webhook check\|setup`                      | GitHub webhooks for registered projects                       |
| `valley flow self-update`                               | Install the tip of the configured branch into the nix profile |

`valley flow --version` prints `valley flow version <v>` (run directly as `klaus`, it prints
`klaus version <v>`).

### valley flow, valley flow session, valley flow new

`valley flow` and `valley flow session` resume the most recent coordinator session or create one;
`valley flow new` creates a fresh one. They must run inside tmux. In a repository the coordinator
works in a worktree of `origin/<default branch>` under `worktree_base`, with flow's hooks but no
trailers; elsewhere in `~/.klaus/sessions/<id>/workspace`, configured by `~/.klaus/config.json`
alone. Each creates the session's `events.jsonl` and syncs registered projects in the background
(`~/.klaus/sync.log`).

| Flag                     | Meaning                                                               |
| ------------------------ | --------------------------------------------------------------------- |
| `--backend <kind>`       | Coordinator backend: `claude`, `codex` or `agy`; saved on the session |
| `--agent-backend <kind>` | The session's default worker backend                                  |
| `--continue`             | (`session`) resume the most recent session                            |
| `--resume <id>`          | (`session`) resume the given session                                  |

A resumed coordinator keeps its saved backend. A Claude coordinator's project memory directory
(`~/.claude/projects/<encoded path>/memory`) is a symlink to `~/.klaus/memory`, shared by every
session; older memory files migrate into it on resume.

### valley flow launch

`valley flow launch "<prompt>" | --prompt-file <path> [flags]`. The positional prompt and
`--prompt-file` are mutually exclusive, and one is required.

| Flag                         | Meaning                                                                   |
| ---------------------------- | ------------------------------------------------------------------------- |
| `--prompt-file <path>`       | Read the brief from a file, verbatim                                      |
| `--repo <r>`                 | Registered project name, `owner/repo` or URL                              |
| `--issue <n\|knot>`          | GitHub issue to reference; on a valley project, a [knot](#knots) to work  |
| `--pr <n>`                   | Work on existing GitHub work's branch; also resumes a budget-paused one   |
| `--change <ref>`             | Continue a change in a repository without GitHub on its branch            |
| `--resume-from <run-id>`     | Continue a previous run's conversation                                    |
| `--branch <name>`            | Name the new branch; not with `--pr` or `--change`                        |
| `--backend <kind>`           | Worker backend                                                            |
| `--model`, `--effort`        | Model and reasoning effort for the backend                                |
| `--budget <usd>`             | Claude only: spending cap (default `default_budget`)                      |
| `--replay`, `--no-replay`    | Force or disable [trajectory replay](#trajectory-replay)                  |
| `--replay-threshold-kb <n>`  | Largest trajectory replayed; 0 uses `replay_threshold_kb`                 |
| `--reason <reason>`          | Dispatch reason recorded on the run and its events                        |
| `--force-takeover`           | Launch past a run still on the branch or change; reset the local branch   |
| `--local`, `--host <name>`   | Run locally, or on this [sandbox host](#sandbox-hosts)                    |
| `--run-id <id>`              | Use this run ID (`YYYYMMDD-HHMM-` and 8 hex digits); refused if taken     |
| `--expect-owner <principal>` | With `--change <change-id>`: require this record owner                    |
| `--expect-branch <branch>`   | ... the active version, and the launch, on this branch                    |
| `--expect-head <sha>`        | ... the active version, origin's branch and the worktree at this full SHA |
| `--expect-draft`             | ... the change listed as a draft                                          |

**Target repository**, in order: `--repo` naming a registered project; `--repo` as `owner/repo` or a
URL (cloned or fetched under `<worktree_base>/.repos/<owner>/<repo>`); the current repository; the
session target (`valley flow target`); else an error.

**Prompt storage.** However given, the brief is written to `prompts/<run-id>.md` in the session
directory, kept as the record, and every worker reads it on stdin. A local Claude worker reads its
system prompt from `prompts/<run-id>.system.md`; an agy worker reads system prompt and brief
together from `prompts/<run-id>.input.md`. What still goes on the pane command line (the Codex
system prompt, a sandbox Claude system prompt) is checked first: a pane command over 12000 bytes
fails the launch before any worktree exists, naming the part that is too large.

**Branch names.** Without `--pr` or `--change`, the branch is `<slug>-<tail>`, the tail being the
run ID's last 8 hex digits. The slug comes from the `--issue` title (GitHub issue or knot), else the
brief's first `# ` heading outside code fences, else its first non-empty line: a leading
`<repo-name>:` is dropped, the text is lowercased, each run of characters outside `[a-z0-9]` becomes
one `-`, and it is cut at a word boundary to at most 40 characters. With no slug, or a name git
rejects, the branch is `agent/<run-id>`. `--branch` must pass `git check-ref-format --branch` and
exist neither locally nor on origin.

**Setup.** Launch fetches the default branch, makes the worktree, snapshots the clone's shared git
files (config, `info/attributes`, hooks), installs [the hooks](#commit-hooks-and-trailers), runs
`nix develop --command true` when the worktree has a `flake.nix`, and starts the worker in a window
of the detached tmux session `klaus-agents-<session-id>` (or a split of the coordinator's window
with `agent_display: "pane"`). The pane runs the worker piped through `tee` to the run's log and
`klaus _format-stream`, then `klaus _finalize <run-id>`. A local worker needs `bash` and its own CLI
on the `PATH` launch runs with; launch fails, naming the command and the `PATH`, when either is
missing. Launch prints `delivery:`, `change:`, `replay:`, `resume:` and `takes on:` lines saying
what it decided.

#### Change IDs of follow-ups

A fresh launch mints a change ID; a follow-up keeps the one it continues. `--pr` takes the change ID
of the run that opened the GitHub work, in any session on the machine, else the `Change-Id` of its
head commit. `--change` takes the named change's, even with `--resume-from`. `--resume-from` alone
takes its run's. A follow-up to a run recorded without a change ID gets a new one, and the `change:`
line says so. Dispatch reasons follow: `launch`, `pr-follow-up`, `budget-resume`,
`change-follow-up`, `resume`.

#### Resolving `--change`

`<ref>` is resolved in order: a run ID names its run's change; a change ID, a prefix of one, or a
recorded handle is matched against runs of the target repository in any session; failing that, in a
repository that delivers to a valley, `valley changes --json` names it. A run belongs to the target
repository when its [repository name](#repository-names) is the target's last component. A ref
naming several changes is an error listing them. `--change` refuses a GitHub repository, and `--pr`
or `--branch` beside it. The worktree tracks the change's branch on origin: the valley's active
version's branch, else the branch the change's newest run pushed; launch fails when none is known or
origin lacks it.

In a valley repository the worker prompt has the agent amend the change's head commit (or end with a
commit whose message describes the whole change), never merge another branch in, rebase onto
`origin/main` when asked or when it conflicts, and push with
`git push --force-with-lease=refs/heads/<branch>:<head> origin HEAD:refs/heads/<branch>`, where
`<head>` is the tip launch found. Elsewhere it commits on top and pushes fast-forward, without
rebasing or force-pushing.

#### `--resume-from` on its run's branch

Combined with `--pr` or `--change`, `--resume-from` supplies the conversation and they supply the
branch. Alone, it continues the branch its run pushed without GitHub work, as `--change` does, only
when all of these hold: the run is in the target repository (by name, and by GitHub owner where
known); no run of the change has GitHub work; origin has the branch; no run of another change worked
on it and origin's tip carries no other `Change-Id`; the change's branch (the valley's active
version's, else its newest push's) is still that branch; the branch holds that run's push, or a
later head of the change (a later run's push on it, or the active version's head); and on GitHub, no
GitHub work of any state has the branch as its head. Otherwise the follow-up gets a new branch and
the `resume:` line says why.

#### A run still working on the branch

`--pr`, `--change`, and `--resume-from` continuing its run's branch refuse while a run in any
session is running on that branch or change in the target repository, naming it. They also refuse,
with or without `--force-takeover`, while any worktree has the branch checked out (naming it, its
run, and whether it holds uncommitted changes or commits origin lacks), and while the local branch
holds commits `origin/<branch>` lacks. `--force-takeover` passes the running-run check and resets
the local branch to origin's tip, discarding such commits. In one clone these launches hold
`klaus-continue.lock` from the check through the new run's registration; a second waits up to 5
minutes, then refuses. `valley flow ready` takes the same lock.

#### Dispatcher launches

`--run-id` lets a dispatcher record the run before launching; a launch with it that fails before the
worker starts exits with status 3. With `--change <full change ID>`, the `--expect-*` flags make
launch ask the valley, under the continuation lock, before creating the worktree and again after
returning the change to draft, just before the worker starts; a valley or origin it cannot ask, a
change not as expected, or a failed `valley unready` fails the launch. Launch stops a worker whose
run it could not record.

#### Trajectory replay

At finalize, flow commits a Claude run's resumable conversation to the data ref at
`sessions/<run-id>.jsonl`. `--pr` on budget-paused work, and `--change` on any change, fetch the ref
from origin (best effort), restore the newest run's conversation into the new worktree and run
`claude --resume <uuid>`. Only the most recent run is replayed; otherwise the agent starts fresh,
and the `replay:` line names the run and the reason, when: that run used another backend; nothing
was stored (sensitive content, or work older than replay); it ran on another machine and
`push_data_ref` was off there or its push failed; the trajectory exceeds the threshold (default 300
KB; `--replay` bypasses it); no Claude session ID is known; it ran on a sandbox host; or
`--no-replay` is given. A Codex follow-up of a Codex run forks its thread (`codex exec fork`) on the
same machine; agy always starts fresh. A replayed conversation mentions the old worktree path and
holds tool results from the time it stopped. With `--resume-from`, that run's conversation is used
instead; when it cannot be resumed the agent starts fresh rather than replaying another run's.

#### Backends

| Capability                       | Claude                   | Codex                            | agy                       |
| -------------------------------- | ------------------------ | -------------------------------- | ------------------------- |
| Coordinator resume               | saved conversation ID    | newest conversation in workspace | workspace conversation ID |
| `--resume-from`                  | staged transcript        | local thread, same machine       | fresh, with a warning     |
| `--pr`/`--change` conversation   | replay from the data ref | fork, same machine               | fresh                     |
| Budget, budget pause, `--replay` | yes                      | refused                          | refused                   |
| Shared coordinator memory        | `~/.klaus/memory`        | the CLI's own                    | the CLI's own             |

Worker backend precedence: `--backend`, the session's saved worker default,
`VALLEY_FLOW_AGENT_BACKEND` (or `KLAUS_AGENT_BACKEND`), `default_agent_backend`, then `claude`.
Model and effort: the flag, else `backends.<kind>`, else for Claude only
`default_agent_model`/`default_agent_effort`; a `backends.<kind>` entry replaces the legacy defaults
even with empty fields, and with nothing set the CLI's own default applies. Model names pass through
unchanged. Claude efforts are `low`, `medium`, `high`, `xhigh`, `max`. Workers run with each CLI's
permission bypass; agy runs with a 24-hour print timeout. `valley flow status` shows the model the
worker reported, else the one launched with, else `(default model)`.

### valley flow status

Prints the runs table; a line for each run that changed its clone's shared git files; the failed
valley checks, the restack conflicts on changes flow owns, and the [data ref](#the-data-ref) pushes
that failed at finalize; a `Session cost` line that includes removed runs; then the session's
running jobs and consults and reviews that finished in the last 3 hours. When prose passes finished
in the same period, a summary line below the jobs table gives their total and any failures (use
`valley flow jobs --kind prose` to see individual passes).

| Column            | Values                                                                         |
| ----------------- | ------------------------------------------------------------------------------ |
| RUN ID            | the run's ID                                                                   |
| STATUS            | where the run stands (below)                                                   |
| AGE               | how long the run or its change has been in its state; `-` when unknown or done |
| MODEL, COST       | backend and model; the run's cost                                              |
| ISSUE, REPO, HOST | the `--issue`; the target repository; where the agent ran                      |
| CHANGE            | `#N`, else the change's handle, else the first 8 characters of its change ID   |
| CI                | `passing`, `failing`, `pending`, `unknown`; valley checks for a ready change   |
| CONFLICTS         | `none`, `yes`, `unknown`, `restack`                                            |
| BEHIND            | commits the base has that the head lacks                                       |
| MERGE             | `ready`, `behind N`, `blocked`, `pending`                                      |
| PROMPT            | the prompt, or the change's title, cut at 40 characters                        |

STATUS is `running`, `needs-attention`, `superseded`, `pr-created`, `merged`, `closed`, `no-access`,
`awaiting-integration`, `integrated`, `branch-gone`, a [valley state](#valley-change-states),
`working`, `tracking` or `exited`. `running` means the pane is running the run (a dead pane, or one
idle at a shell after finalize, is not). `working`: another run of its change is running.
`superseded`: a run needing attention whose pushed work a later run of its change took on, and that
run is running or finished. `no-access`: GitHub will not show the work with the current credentials.
`tracking`: added by `valley flow track` and not yet settled.

AGE is the age [the queue](#valley-flow-queue) gives the run's state: `running` counts from the
run's launch, `working` from the launch of the run working on the change, and every other state from
the latest event that enters it.

### valley flow queue

`valley flow queue [--json]` lists what waits on the coordinator, oldest first, then what waits on
the operator. It checks run state as `valley flow status` does without printing a table, and prints
nothing when nothing waits. Each item is one line with `◇` (or `✗` for `checks-failed`, `rejected`
and `failed`), the project and change handle (else GitHub number, else run ID), the state,
`for <age>`, the head, and any note (failed checks, where restack dispatch stands, or an operator
item's `checks pending` or `checks passing`). Coordinator items add `  run: <command>`
(`  see: valley flow status` for shared git files). `--json` prints
`{"coordinator":[...],"operator":[...]}`, with empty lists when nothing waits; each item has `run`,
`change`, `change_id`, `project`, `head`, `state`, `checks`, `since` (RFC 3339), `age_seconds`,
`next` and `note`.

| State                         | Waits on    | When                                                                 | Next command                                                                                     |
| ----------------------------- | ----------- | -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `needs-attention`             | coordinator | finalize salvaged the work, or the clone's shared git files changed  | `valley flow launch --resume-from <run> "<brief>"`, or `valley flow status` for shared git files |
| `awaiting-review`             | coordinator | a draft finalize left for review                                     | `valley flow ready <run> --head <head>`                                                          |
| `checks-failed`               | coordinator | the active version's required checks failed, and flow won't fix them | `valley flow launch --change <run> "<brief>"`                                                    |
| `conflicting`                 | coordinator | a restack conflict flow stopped short of, or has no dispatch for     | `valley flow launch --change <run> "<brief>"`                                                    |
| `stale`, `rejected`, `failed` | coordinator | the integrator's decision on the approved version                    | `valley flow launch --change <run> "<brief>"`                                                    |
| `draft`                       | coordinator | returned to draft, and no run works on it                            | `valley flow launch --change <run> "<brief>"`                                                    |
| `awaiting-approval`           | operator    | ready, its checks not failed and no restack conflict standing        | none                                                                                             |
| `awaiting-integration`        | operator    | a pushed branch the operator lands by hand                           | none                                                                                             |
| `uncertain`                   | operator    | no landing evidence for a consumed or failed request                 | none                                                                                             |
| `ready-to-merge`              | operator    | GitHub work (tracked too), ready to merge and not yet approved       | none                                                                                             |

Launch commands carry `--repo <target>` when the run names one. Failed checks stay with flow while
its [failed-check dispatch](#failed-check-dispatch) is due or busy, or its fix run is alive or
pushed a new head, and move to the coordinator once the dispatch stopped, its launch failed, its fix
run finished without a new head, or flow does not own the change. Restack conflicts likewise follow
[restack-conflict dispatch](#restack-conflict-dispatch), moving to the coordinator once stopped,
refused or held, or once its resolution run finished without a new head. A dispatched run removed by
cleanup has finished; otherwise, one with no record counts as launching. If its launch fails before
recording a run and the pass cannot save the failure, the pass reports the error and the change
stays with flow until its next head. A draft that flow's restack dispatch made reads `conflicting`.
A restack conflict on a ready change flow does not own reaches the coordinator once the integrator
finds the approved version stale (`change:stale`).

Everything else (running, working, approved, open GitHub work the pipeline moves, or a dispatch
under way) is in progress or done and is left out. GitHub work reaches the queue as a run's
`needs-attention` or as `ready-to-merge`; a pipeline that gave up on GitHub work or a run paused on
its budget shows on the dashboard and as `pipeline:stalled` or `agent:paused` in
[`valley flow watch`](#valley-flow-watch). A change appears as one item under its newest run, while
a run's `needs-attention` is its own item. A valley change with a run that reads `older-version`
stands where the valley lists its active version unless another of its runs stands for that version
or is at work on the change (runs that exited without pushing or need attention do not). The
operator's group carries no command: approving is the operator's, and the coordinator relays those
items.

An item's age runs from the latest event that enters its state and is not about another head:
`agent:change-draft` for `awaiting-review`, `agent:change-ready` or `change:new-version` for
`awaiting-approval`, and so on. Events are matched to the run itself for `needs-attention`, its
GitHub work for `ready-to-merge`, and the change or any of its runs otherwise. With no such event
the age is unknown: `-` in text, `null` in JSON. A time ahead of the clock, after the clock stepped
back, reads 0.

### valley flow dashboard

| Key                   | Action                                                                                           |
| --------------------- | ------------------------------------------------------------------------------------------------ |
| `j`/`k`, `↑`/`↓`      | move the selection                                                                               |
| `PgUp`/`PgDn`         | move a page                                                                                      |
| `g`/`G`, `Home`/`End` | top, bottom                                                                                      |
| `h`                   | show or hide done rows older than `hide_finished_after_minutes`                                  |
| `a`                   | approve the selected GitHub work                                                                 |
| `d`                   | pre-fill `WRT PR#<num>:` in the coordinator's pane and focus it <!-- prose-allow: vocabulary --> |
| `o`                   | open the selected GitHub work in a browser (`xdg-open`, or `open`)                               |
| `x`                   | after `y`: remove what `valley flow cleanup --done` would; hide finished jobs                    |
| `tab`                 | select a job, or back to the runs                                                                |
| `enter`               | read the selected job's record and answer (`esc` back)                                           |
| `r`                   | refresh and redraw                                                                               |
| `q`, `ctrl+c`         | quit                                                                                             |

**Rows.** Runs on one GitHub work share its row (named by the owner and repository in its URL and
its number); runs without GitHub work on one change (same change ID, same repository) share the
change's row. A change's row shows the handle (else the change ID's first 8 characters), the
valley's title (else the pushed head's subject), and where the active version stands, with its head;
earlier versions fold into it. A newer push the valley holds no version of is noted in the footer
(`a newer push 1a2b3c4 isn't ready`). While an agent runs on a row, the row names it, sorts with the
agents and offers no approve command. Rows are grouped yours, agents, pipeline, done, newest first;
the line above the list counts groups and shows the position and hidden count. Done rows older than
`hide_finished_after_minutes` (default 60) are hidden; a row the dashboard watched finish stays the
full period. A repository column appears when the session spans repositories or shows a change.

**Sources.** File-system watching of run state, GitHub polling every 30 seconds (one GraphQL query
per repository for all its tracked work, plus a compare when a head or base moved), or
[webhooks](#webhook-receiver). Work seen merged or closed is not asked about again
(`pr-transitions.json`); reopened work stays closed to flow. Work the account cannot read (404, a
non-rate-limit 403, an unresolvable reference) shows `NO ACCESS`, skips CI, conflict and review
queries, and is asked again every 30 minutes. When GitHub rate-limits, polling stops until the time
GitHub gives (or 2 minutes), the header says `GitHub rate-limited until HH:MM`, and rows keep their
status. Pushed branches and valley changes are checked every minute, with at most one
`valley changes` per check. While it shows changes, the dashboard follows each valley host's
telemetry with one `valley telemetry --json follow` per host (one of the principal's follow slots);
a decision, stored evidence or a batch's end triggers an extra check. Where telemetry cannot be
followed, it logs that once, keeps to the minute and retries every 10 minutes.

**Footer commands.** `dashboard.approve_command` is expanded for a selected `READY` row (`approve:`)
or `SUBMIT` row (`submit:`): `{repo}` (the repository as the row names it), `{branch}`, `{change}`
(full change ID), `{handle}`, `{head}`. Values other than `[A-Za-z0-9._/-]` are shell-quoted, so
placeholders go unquoted. A row lacking a placeholder's value, a `DRAFT` row, or a row with an agent
running shows none.

**Jobs.** The jobs section lists `RUNNING` jobs and consults and reviews that finished in the last 3
hours (`SUCCEEDED`, `FAILED`, `TIMED OUT`, `INTERRUPTED`, `DIED`). It refreshes every 30 seconds.
The header summarises prose passes that finished in the same period. Select a run row to see its
finished prose passes in the footer. Each summary gives the total and any failures and appears only
when there are passes to count. Job row counts and `tab` selection cover only job rows.

**Restart.** Every 30 seconds the dashboard looks for the name it was started as, `klaus` or
`valley-flow`, in the absolute directories on `PATH`. When the first match is a different file that
runs `--version`, the footer says `flow updated to <version>; restarting`, and after 10 seconds
without a key press the dashboard shuts down as on `q` and execs that file with the same arguments
and environment. A dashboard started as `valley-flow` notes the `PATH` directory it was found in. If
that directory later holds `klaus` without `valley-flow`, as when a profile is rolled back to a
build that lacks it, the dashboard restarts onto that `klaus`. A failed exec is shown and that file
is not tried again.

Notes go to `dashboard.log` in the session directory, never to the screen. A narrow or short pane
drops spacing, status, job and help lines before rows.

### valley flow logs

`valley flow logs <run-id>` shows the live pane while the run runs, else replays the saved log
through the formatter. `--replay` re-formats the saved JSONL log; `--raw` prints it as is.

### valley flow watch

| Flag                  | Meaning                                                                                   |
| --------------------- | ----------------------------------------------------------------------------------------- |
| `--filter <type>`     | Include this type (repeatable); replaces the default filter                               |
| `--filter-out <type>` | Exclude this type (repeatable), after `--filter`                                          |
| `--run <id>`          | Only this run's events (a number for pipeline events, a handle for a change's)            |
| `--since-start`       | Emit the file's existing events first                                                     |
| `--once`              | Wait for at least one matching event, print all that arrived, then a `next:` line, exit 0 |
| `--after <offset>`    | With `--once`: the byte offset to read from                                               |
| `--queue`             | Wait until [the queue](#valley-flow-queue) differs from `--seen`; print it, exit 0        |
| `--seen <digest>`     | With `--queue`: the digest on the last `--queue` watch's `next:` line                     |
| `--json`              | Raw JSONL lines                                                                           |
| `--list-types`        | Print live and reserved types and the default filter                                      |
| `--session-id <id>`   | Override `VALLEY_FLOW_SESSION_ID` / `KLAUS_SESSION_ID`                                    |

`--after` is a byte offset into `events.jsonl` at the start of a line. The `next:` line repeats the
other flags given, for example `next: valley flow watch --once --after 18342`; with `--json` it is
`{"after":18342,"next":"valley flow watch --once --after 18342 --json"}`. Without `--after`,
`--once` starts at the end of the file (the start with `--since-start`). Interrupted before any
event, it prints only its `next:` line. An offset past the end or inside a line is an error, as is
an events file replaced or removed while a watch waits. A watch on a session without an events file
waits for it while the session directory exists; an unknown session fails at once.

`--queue` reads the queue as `valley flow queue` does and returns once its digest differs from
`--seen`. The digest hashes each item's group, identity, state and head, and an operator item's
checks; ages, notes and order are left out. Without `--seen` it compares with an empty queue, so it
returns at once when anything waits. It prints the queue as `valley flow queue` prints it, then
`next: valley flow watch --queue --seen <digest>` (omitting `--seen` when the queue is empty).
Interrupted, it prints only its `next:` line, which waits from the `--seen` it was given. An item
that stays as it was doesn't end the next watch; it stays in every listing, its age growing.

The watch re-reads the queue after each burst of writes to `events.jsonl` or `runs/` and keeps
nothing between reads. If a run record fails to parse, as when caught mid-rewrite, the watch holds
off until a completing write wakes it; a record that stays malformed blocks every wait until
rewritten or removed. Updates that write to neither file (another client returning a change to
draft, or, when no dashboard runs to turn them into events, the valley's decisions and GitHub's CI
results) surface at the next write or `valley flow queue`. So does a draft saved by a finalize that
died before clearing its pane: the watch checked that write while the run still read `running`, and
no later write follows. For GitHub work the digest holds the head the run recorded, not the one
GitHub shows, so a new head on an item otherwise unchanged ends no wait. `--queue` takes no flag but
`--seen` and `--session-id`.

### valley flow notifications

Shows events since the last run (marker `notifications-marker` in the session directory), grouped
into summaries; `--all` shows every event, `--json` lists them. A superseded run's
`agent:needs-attention` is left out of the summary.

### valley flow jobs

`valley flow jobs` lists running jobs and jobs that finished in the last 3 hours, including prose
passes. Use `--all` to list all retained records. Filter with `--kind`, using `consult`, `review`,
or `prose`. Repeat the flag or separate kinds with commas.

`valley flow jobs show <id>` prints a record and its answer. Use `--answer` for just the answer. The
ID can be a job ID, a unique suffix of a job ID, or a panel ID. A job record (`jobs/<run_id>.json`)
holds `kind` (`consult`, `review` or `prose`), `thread`, `turn`, `role`, `backend`, `model`,
`effort`, `dir`, `repo`, `commit` (or a review's `pr`), the `prompt` excerpt, `started_at`,
`finished_at`, the flow `pid` and its start time from `/proc/<pid>/stat`, `panel`, and `status`:
`running`, `succeeded`, `failed`, `timed out` or `interrupted`, with a short `error`. A `running`
record whose process is gone reads `died`. A one-shot consult's answer is `<run_id>.answer` beside
the record, a failed one's raw output `<run_id>.raw`; a thread's answer is in its log. Records
finished over 7 days ago are removed when a job starts.

### valley flow consult

`valley flow consult [flags] ("<question>" | --prompt-file PATH)`.

| Flag                  | Meaning                                                                                                       |
| --------------------- | ------------------------------------------------------------------------------------------------------------- |
| `--backend <kind>`    | Model family (default: first installed in `consult.order` other than `VALLEY_FLOW_BACKEND` / `KLAUS_BACKEND`) |
| `--model`, `--effort` | Default `backends.<kind>`                                                                                     |
| `--role <r>`          | `partner`, `critic`, `reviewer` or a verbatim system prompt (default `consult.default_role`)                  |
| `--thread NAME`       | Continue a named conversation in the current or most recent session                                           |
| `--dir <path>`        | Workspace, read as it stands (default: current directory or the thread's)                                     |
| `--repo NAME`         | A registered project, as a snapshot of origin's default branch                                                |
| `--file <path>`       | Inline a file (repeatable, 200 KB total)                                                                      |
| `--prompt-file`       | Read the question from a file                                                                                 |
| `--timeout <d>`       | Deadline per backend call (default `10m`)                                                                     |
| `--panel`             | Ask every installed family other than the caller, concurrently                                                |
| `--list`              | List threads with backend, successful turns and last use                                                      |

`--dir`/`--repo`, `--panel`/`--thread` and `--panel`/`--backend` are exclusive. Paths resolve from
the invoking directory; each attachment sits under a header with its path (for agy, between BEGIN
and END lines). The question goes on stdin.

**`--repo`** fetches origin's default branch (the project's `default_branch`, else `main`) at the
start of each turn and checks it out, detached, in a snapshot worktree of the clone. The prompt's
first line is `Workspace: <project> at origin/<branch> <commit>`, and the commit is kept in the
thread, the `consult:started` event and the job record. A one-shot snapshot (shared by a panel)
lives in `~/.klaus/consult-snapshots/` and goes when the consult ends; a thread's lives at
`consults/NAME.workspace` and moves to the current origin branch every turn. When the fetch fails, a
thread reads its last snapshot with a warning; a first turn fails. `valley flow cleanup --all`
removes thread snapshots, and a missing one is made again.

**Threads** keep backend, model, effort, role and workspace; conflicting overrides are refused.
`consults/NAME.json` holds metadata and `consults/NAME.log` the transcript: JSON lines, each turn
written `pending` before the backend starts and updated to `ok` or `error: <message>`, recording
backend, model, resume ID, the snapshot commit, and how the session ID was found (`stderr`,
`stdout-json`, `diagnostic-log`, `text`, `assigned`, `resume`). A failed turn keeps its output in
`NAME.<turn>.raw`. Outside a pane, threads use the most recent session; one-shot questions need
none.

**Panels** print each answer whole as it finishes, under a backend/model heading; a timed-out member
prints `timed out`; any failed member fails the command after the others print.

**Isolation.** Codex runs in its read-only sandbox with `--ignore-user-config` (no MCP servers from
`$CODEX_HOME/config.toml`; authentication still uses `CODEX_HOME`) and apps, plugins, hooks and
subagents disabled. Claude runs in safe mode with only Read, Grep and Glob. agy runs in plan mode
with a temporary agent definition exposing only file reads and search, removed after the call.
Consults never create run state.

### valley flow review

`valley flow review <pr>` has a model family other than the author's review GitHub work. The
author's family is that of the flow run that opened it, else Claude.

| Flag                  | Meaning                                                                   |
| --------------------- | ------------------------------------------------------------------------- |
| `--repo <r>`          | Repository of a bare number                                               |
| `--backend <kind>`    | Reviewer family (default: the [cross-review choice](#configuration-keys)) |
| `--model <m>`         | Default `backends.<reviewer>.review_model`                                |
| `--post`              | Submit one `COMMENT` review (default `cross_review.post`)                 |
| `--allow-same-family` | With `--backend`, allow the author's own family                           |

The reviewer sees only `gh pr diff`, the title and the description, with consult isolation, and
returns findings and a verdict on whether the change matches its stated intent. A posted review has
event `COMMENT`, never `APPROVE` or `REQUEST_CHANGES`: findings on diff lines become inline
comments, the rest go in the body, which ends with
`<!-- klaus-cross-review backend=<kind> model=<model> sha=<head-sha> -->`. Reading reviews accepts
both `<!-- klaus-cross-review ... -->` and `<!-- flow-cross-review ... -->`, counting each review
once. Posting is refused when the same reviewer already reviewed the head commit, or when
`cross_review.max_rounds` marker reviews from your gh account exist; both checks, and a check that
the head has not moved, run again just before posting. `locks/review-<owner>-<repo>-<n>.lock` makes
a concurrent `--post` in the session fail at once. In a session it emits
`review:started`/`review:completed` and leaves a job record; SIGINT and SIGTERM end it as
`interrupted`.

### valley flow approve, valley flow merge

`valley flow approve <n>...` (`--run <run-id>`, `--all` for every merge-ready one) marks GitHub work
approved in run state and emits `pr:approval-changed`.

`valley flow merge <n>...` merges in order, stopping at the first that fails and reporting the rest.
For each: check readiness; wait for CI (polling every 30 seconds, up to 10 minutes); if it
conflicts, rebase it onto main in a temporary worktree, run the verification
(`merge_verify_command`, else `go build ./...` when there is a `go.mod`, else nothing) and push with
`--force-with-lease`; merge with `gh pr merge`; mark the run merged. Work merely behind is merged as
is; if GitHub demands an up-to-date branch, flow runs GitHub's branch update (a merge from main,
which keeps the head and its approval), waits for CI and merges again. Merge never removes a
worktree that has the branch checked out. Without a clone it resolves one (current repository,
registered project, or a clone under `<worktree_base>/.repos`), and the target repository's
`.klaus/config.json` applies.

| Flag                  | Meaning                                                  |
| --------------------- | -------------------------------------------------------- |
| `--dry-run`           | Print the plan                                           |
| `--merge-method <m>`  | `squash` (default), `merge` or `rebase`                  |
| `--no-delete-branch`  | Omit `--delete-branch`                                   |
| `--repo <owner/repo>` | Repository for every number                              |
| `--force`             | Bypass the approval check                                |
| `--yes`, `-y`         | Skip unapproved work with a warning instead of prompting |

### valley flow ready

`valley flow ready <run-id|handle> [--head <sha>]` marks a change's current head ready
(`valley ready --change <change-id> --head <head> <branch>`, in the run's clone): the active
version's head when the valley lists the change, else the head its newest run in any session pushed.
`<run-id|handle>` is a run ID, or a handle, change ID or prefix as this session's runs record them.
With `--head` (full, or at least 7 hex digits) it marks exactly that head and refuses, naming the
current head, when the change has moved on; the current head is then origin's tip of the change's
branch when it differs from the active version, and an unreachable origin is a refusal. It refuses
while an agent in any session runs on the change, and for a repository that does not deliver to a
valley. It clears `valley.draft` on this session's run that pushed the head, emits
`agent:change-ready` with `"via": "klaus ready"`, and prints
`<repo> <handle> v<n> ready at <head> (run <id>)`.

### valley flow track, valley flow untrack

`valley flow track <ref>...` and `valley flow untrack <ref>...`, with `--repo` naming the repository
of a bare number or the project of a bare handle or change ID. GitHub work is a number (in `--repo`,
else the session target), a URL or `owner/repo#N`; it reads `tracking` until GitHub says merged or
closed. A change is a handle, a change ID or `<project>:<handle>`; the project is the one before the
colon, else `--repo`, else the session target, else the current directory's repository, and flow
runs `valley changes --json` in its checkout and records the change ID, repository, handle, and the
active version's title, branch and head. A change already followed is not added again; a landed or
withdrawn one is added with a warning. Untrack removes only what track added, and a ref naming
tracked changes in several projects removes none.

### valley flow failed-checks, valley flow restack-conflicts

One pass of what the dashboard does every minute: dispatch fix runs for
[failed valley checks](#failed-check-dispatch), and runs for
[restack conflicts](#restack-conflict-dispatch), on changes flow owns.

### valley flow cleanup

`valley flow cleanup <run-id> | --done | --all [--force]` kills a run's pane, removes its worktree
and scratch directory, deletes its local branch and its state, keeping its cost and duration in the
session's ledger (`cleared.jsonl`). `--done` removes the runs of work that is merged, closed,
integrated, withdrawn or gone, and the older runs of open work, keeping each open item's latest run,
runs needing attention and running runs, with a line for each. `--all` removes every run and the
session's unused consult thread snapshots, and the detached agents session once no run holds a pane
in it and no pane runs a command. Running runs are skipped unless `--force`.

### valley flow push-log, valley flow report time

`valley flow push-log <run-id> [--push]` commits a log held back by the
[sensitivity scan](#the-data-ref) to the data ref after you reviewed it; `--push` also pushes the
ref once.

`valley flow report time [--since 7d] [--repo <r>] [--current] [--json]` reads every session's event
log (`--current`: this one) and reports, per repository and overall, for runs started in the window
(`0`: all time): runs by dispatch reason, time by phase, checks by kind, idle time between a
change's runs, time from the last run to the first approval, follow-ups per change, consult and
review time, and cost. Runs sharing a change ID in a repository, or GitHub work, are one change.
`--repo` takes a key, valley name or short name; `--json` gives durations in milliseconds. Runs
without phase events count from `agent:started` to their last event; a run without a reason counts
as `unknown`.

### valley flow target, valley flow project, valley flow sync

`valley flow target [name | owner/repo]` sets the session's default repository (`target.json`);
without an argument it shows it, and `--clear` removes it.

`valley flow project add <owner/repo | name> [--path <dir>] [-d <description>]` clones into the
projects directory (or registers an existing checkout) and records it in `~/.klaus/projects.json`; a
bare name searches your GitHub repositories, and without `-d` the description comes from
`gh repo view`. `project list`, `project remove <name>` (keeps the clone),
`project describe <name> <text>` (empty clears) and `project set-dir <path>` complete it.
Descriptions appear in the coordinator's `## Registered projects` block. `valley flow sync` fetches
and fast-forwards every registered project.

### valley flow scaffold, valley flow init

`valley flow scaffold <project-name> [--description <d>] [--type web|cli] [--budget <usd>] [--backend <kind>]`
creates a GitHub repository, registers it, and launches an agent that scaffolds it from
`.klaus/principles.md` in the current directory (else built-in principles), without templates. It
must run inside tmux.

`valley flow init` writes `.klaus/config.json` (the defaults) and `.klaus/prompt.md` in a
repository, or `~/.klaus/config.json` outside one.

### valley flow webhook

`valley flow webhook check` shows each registered project's webhook state;
`valley flow webhook setup [project]` creates missing ones. Both need `webhook.relay_url`; `setup`
also needs `webhook.secret_file`. Hooks subscribe to `push`, `check_run`, `check_suite`,
`pull_request`, `pull_request_review` and `issue_comment`.

### valley flow self-update

Off until the host config has `self_update` (`repo`, and `branch`, default `main`); a repository's
`.klaus/config.json` cannot set it. `repo` is an `https://`, `ssh://`, `git://` or `file:///` URL,
an absolute path, or `user@host:path` (read as `ssh://user@host/path`; write `user@host:~/path` for
a home-relative path). Other transports (`ext::`, `fd::`) and characters outside
`[A-Za-z0-9._~/:@-]` are refused; git and nix read the same URL, with
`GIT_ALLOW_PROTOCOL=https:ssh:git:file`. A `git://` repository needs a nix that accepts `git+git://`
flake references; nix 2.31 refuses them.

The flake offers flow as two packages of one program, `packages.<system>.valley-flow` and
`packages.<system>.klaus`, each holding it as both `valley-flow` and `klaus`. Self-update builds
`klaus`. A package's store path installs as the profile element of its name, so flow is in the
profile as `klaus` or as `valley-flow`, and self-update replaces whichever it finds. A profile
holding both is refused before anything else, with nothing built or changed, by every run and by
`--rollback`, `--pin` and `--unpin`. A run:

1. takes a lock under `$XDG_STATE_HOME/klaus/` (a run finding another exits 0, saying so) and lists
   the profile, refusing one holding both or one nix cannot list; a pinned install exits 0 with a
   note;
2. resolves the branch with `git ls-remote <repo> refs/heads/<branch>`;
3. exits 0 when the installed flow was built from that commit (`-v` says so);
4. reads the packages the flake offers at that commit and, unless it offers
   `packages.<system>.klaus`, refuses with nothing built or changed. The refusal asks for
   `self_update.repo` to name a repository whose flake offers it, such as the-valley's; the next run
   then builds, smoke-tests and installs from there, whatever the state file remembers of the
   repository before;
5. exits 0 when that commit already failed to build, test or install (retried when the branch moves,
   or with `--retry`);
6. builds exactly `git+<repo>?ref=refs/heads/<branch>&rev=<sha>#packages.<system>.klaus`, never a
   default package;
7. smoke-tests it: `klaus --version` must report the commit and `klaus status` must exit 0; a
   failure records the commit as failed, installs nothing and exits non-zero;
8. replaces the nix profile element `klaus` or `valley-flow`, leaving other elements and any system
   install alone. Replacing removes the element and adds the build; when the add fails, it puts the
   element's store path back and records the commit as failed. When putting it back fails too, the
   profile holds no flow: the state records no install and no failure, so the next run installs the
   tip, and the error gives the `nix profile add` command to put the old build back by hand;
9. records the current and previous store paths, commits, profile elements and packages, the time
   and any failure in `$XDG_STATE_HOME/klaus/self-update.json`. An entry without an element or
   package reads as `klaus`.

Removing the element and adding the build are two profile operations. Putting the element back
covers a failed add, not a killed run: a run killed between the two leaves the profile without flow,
while `self-update.json` still records the removed build as current. To put that build back, run
`nix profile add <store path>` with the state file's `current.store_path`; to install the branch tip
instead, run `nix profile add 'git+<repo>?ref=refs/heads/<branch>#packages.<system>.klaus'`.

`--rollback` adds the previous store path in place of the element holding flow, restoring it under
the element name it came from (run again to switch back); `--pin` holds the current install until
`--unpin`; `--retry`, `--rollback`, `--pin` and `--unpin` are exclusive.

### Internal commands

| Command                                                 | Use                                                      |
| ------------------------------------------------------- | -------------------------------------------------------- |
| `klaus _finalize <run-id>`                              | Finalize a run; the pane runs it after the worker        |
| `klaus _format-stream [--sandbox]`                      | Format a worker's JSONL stream; time its checks          |
| `klaus _pre-review [--dir <worktree>]`                  | Forbidden terms, linters, peer review and prose rewrites |
| `klaus _forbidden-terms [--repo <r>] [<base>] [<head>]` | Check a range for forbidden terms; exit 1 on a match     |
| `klaus _event --run-id <id> --type <t> --data <json>`   | Append an event to the session's log                     |

`_pre-review` runs [forbidden terms](#forbidden-terms) first, even with `pre_review.enabled` false,
then the linters (`pre_review.linters`, else detected), then the peer review with the
[cross-review choice](#configuration-keys) (printing the reviewer), then the
[prose pass](#configuration-keys). It exits 1, saying the delivery should be blocked, on a forbidden
term, a lint failure, or findings at or above `pre_review.block_on`.

Forbidden terms, peer review and the prose pass each read the change since its merge base with
`origin/<default_branch>` (the local branch only when the clone has no origin copy), as committed:
no textconv or external diff runs on it. A file that is not text (not valid UTF-8, or holding a NUL
byte) reaches the reviewer and the prose writer by name, without its content, and with its size
unless git itself takes it for binary.

## Configuration

Configuration layers: built-in defaults, then the host config `~/.klaus/config.json`, then the
repository's `.klaus/config.json`. Two keys are read from the host config only: `forbidden_terms` (a
repository's value is ignored) and `self_update`. Finalize reads `ready_at_finalize` and
`push_data_ref` from the checkout the run's worktree belongs to, never from the agent's worktree.

### Configuration keys

| Key                                                 | Default                                  | Meaning                                                       |
| --------------------------------------------------- | ---------------------------------------- | ------------------------------------------------------------- |
| `worktree_base`                                     | `$TMPDIR/klaus-sessions`                 | Where worktrees and `.repos/` clones go                       |
| `default_branch`                                    | `main`                                   | The repository's default branch                               |
| `default_budget`                                    | `5.00`                                   | Claude spending cap in USD                                    |
| `data_ref`                                          | `refs/klaus/data`                        | The [data ref](#the-data-ref)                                 |
| `push_data_ref`                                     | `false`                                  | Push the data ref to origin after every run                   |
| `replay_threshold_kb`                               | `300`                                    | Largest trajectory replayed                                   |
| `default_coordinator_backend`                       | `claude`                                 | Coordinator backend                                           |
| `default_agent_backend`                             | `claude`                                 | Worker backend                                                |
| `backends.<kind>.model`, `.effort`                  | unset                                    | Per-backend model and effort for workers and consults         |
| `backends.<kind>.review_backend`                    | unset                                    | Reviewer family for work this family wrote                    |
| `backends.<kind>.review_model`                      | unset                                    | Model when this family reviews                                |
| `default_agent_model`, `default_agent_effort`       | unset                                    | Legacy Claude worker defaults                                 |
| `agent_display`                                     | `detached`                               | `detached` session windows, or `pane` splits                  |
| `sandbox_host`                                      | unset                                    | SSH host for agent runs                                       |
| `format`                                            | `nix fmt` if the flake has a `formatter` | Formatter command line for the pre-commit hook                |
| `house_rules.repo`, `.path`                         | unset                                    | [House rules](#prompts-and-house-rules) file                  |
| `forbidden_terms`                                   | unset                                    | Host only: [forbidden terms](#forbidden-terms)                |
| `trusted_reviewers`                                 | `["gemini-code-assist[bot]"]`            | Logins whose comments dispatch fix agents                     |
| `pr_reviewer`                                       | picked at launch                         | Login an agent requests review from                           |
| `require_approval`                                  | `true`                                   | `valley flow merge` requires approval                         |
| `auto_merge_on_approval`                            | `false`                                  | The pipeline merges approved, green, conflict-free work       |
| `merge_verify_command`                              | `go build ./...` with `go.mod`           | Verification before a merge's force-push                      |
| `ready_at_finalize`                                 | `true`                                   | Mark a valley change ready at finalize, or leave a draft      |
| `pre_review.enabled`                                | `true`                                   | Run linters and peer review                                   |
| `pre_review.linters`                                | detected                                 | Linter commands                                               |
| `pre_review.review_model`                           | `haiku`                                  | Claude reviewer model                                         |
| `pre_review.block_on`                               | `high`                                   | Lowest finding severity that blocks                           |
| `pre_review.prose.enabled`                          | `true`                                   | Run the prose pass                                            |
| `pre_review.prose.order`                            | `agy`, `codex`, `claude`                 | Writer preference                                             |
| `pre_review.prose.paths`                            | none                                     | Files beyond Markdown and READMEs whose added lines are prose |
| `pre_review.prose.timeout_seconds`                  | `600`                                    | Per model call                                                |
| `pre_review.prose.max_input_bytes`                  | `40000`                                  | Diff sent to the models                                       |
| `pre_review.prose.writer.<kind>`, `.checker.<kind>` | backend defaults                         | `model` and `effort` for the writer and checker               |
| `cross_review.enabled`                              | `true`                                   | `false` keeps the author's family                             |
| `cross_review.order`                                | `codex`, `claude`, `agy`                 | Reviewer preference                                           |
| `cross_review.post`                                 | `false`                                  | Default for `valley flow review --post`                       |
| `cross_review.max_rounds`                           | `2`                                      | Posted cross-reviews per GitHub work                          |
| `consult.order`                                     | `codex`, `claude`, `agy`                 | Consult family preference                                     |
| `consult.default_role`                              | `partner`                                | Consult role                                                  |
| `dashboard.hide_finished_after_minutes`             | `60`                                     | Hide older done rows; negative never hides                    |
| `dashboard.approve_command`                         | empty                                    | Footer approve template (`submit_command`: deprecated name)   |
| `webhook.*`                                         | absent                                   | [Webhook receiver](#webhook-receiver)                         |
| `self_update.repo`, `.branch`                       | absent, `main`                           | Host only: [self-update](#valley-flow-self-update) source     |

**Cross-review choice.** The reviewer family is `backends.<author>.review_backend`, else the first
`cross_review.order` entry that is not the author and whose CLI is on `PATH`, else the author's own;
`enabled: false` skips the order. The model is `backends.<reviewer>.review_model`, else
`pre_review.review_model` for Claude, else the CLI default.

**Prose pass.** Prose is the added lines of Markdown files and READMEs and of files `paths` matches
(a pattern without `/` matches a file name anywhere; `dir/**` a tree), plus the head commit message;
a change with no prose beyond a short commit message is skipped. The writer is the first installed
family in `order` other than the author's, the checker the next (else the author's); each runs at
`writer.<kind>`/`checker.<kind>`, else backend defaults, except that an agy writer's model defaults
to `gemini-4-hydrogen-high`. The writer lists each passage's claims in a ledger before rewriting,
following the writing sections of the repository's `AGENTS.md` and `CLAUDE.md` and the house rules;
the checker reports each rewrite that changes the meaning.

**`pr_reviewer`.** Used as given. Unset, launch lists the repository's collaborators with push
access, drops bots and the `gh` user, and picks the owner if present, else the only one left; with
several left or a failed `gh` call it warns and requests no review. Runs that open nothing new skip
it.

**`trusted_reviewers`.** Inline review comments and conversation comments both count; a comment
stops counting once a newer commit is pushed, and an edit counts as new. A conversation comment by
the work's author counts only with `/klaus fix` or `/flow fix` as its first non-blank line, or with
`<!-- klaus-actionable -->` or `<!-- flow-actionable -->` on its own line. Quoted lines do not
count. Flow ignores replies marked `<!-- klaus-agent-reply -->` or `<!-- flow-agent-reply -->`.
Either cross-review marker makes a review trusted when it comes from the authenticated gh user.

### Prompts and house rules

`.klaus/prompt.md` is the worker system prompt template, `.klaus/session-prompt.md` the
coordinator's and `.klaus/pr-fix-prompt.md` the fix agents'. They are Go templates:

| Variable          | Value                                                                            |
| ----------------- | -------------------------------------------------------------------------------- |
| `{{.RunID}}`      | the run ID                                                                       |
| `{{.Issue}}`      | the GitHub issue, if any                                                         |
| `{{.Branch}}`     | the agent's branch                                                               |
| `{{.RepoName}}`   | the repository's name                                                            |
| `{{.Reviewer}}`   | the login to request review from; empty when none, and for direct push           |
| `{{.DirectPush}}` | true when origin is not on github.com                                            |
| `{{.Head}}`       | the commit the worktree starts at when continuing a change or GitHub work        |
| `{{.PR}}`         | (fix prompt) the number of the work being fixed <!-- prose-allow: vocabulary --> |

flow appends a delivery section and its worker rules after `.klaus/prompt.md` and
`.klaus/pr-fix-prompt.md`, so custom templates receive both. Delivery states how the run delivers
(push, GitHub work, or valley amend-and-lease). For follow-ups on an existing change or GitHub work
(`--change`, `--pr`, or `--resume-from` on a pushed branch), it adds a default scope rule that the
brief can override: limit edits to the brief's items, their tests, and sentences those edits make
untrue; stop and report the choice and cost before introducing new mechanism; run one `_pre-review`
pass on the final head, acting on in-scope critical and high findings and listing the rest; and
report each item's status and the round's diff stat against `{{.Head}}`. Worker rules cover the
mechanics shared across repositories: the run ends with the agent's turn, so long builds run in the
foreground; scratch output stays out of the worktree; `_pre-review` prose rewrites are applied
unless they change meaning; a broken environment is reported, not worked around; processes are
killed only by PID; and the final report has the headings Intent, Decisions, Shape, What the
implementation taught us, Checks, Diff summary.

`house_rules` (`{"repo": "<registered project>", "path": "guidance/house-rules.md"}`) is read at
launch with `git show` from the project's freshly fetched origin default branch, never a working
tree, and appended after the worker rules, and to the context of `valley flow consult --repo`, the
`_pre-review` reviewer, and the prose writer and checker. The run records its source, commit and
sha256, and launch prints them. An unreadable file is warned about once, recorded as missing, and
skipped. A repository's config may name other rules.

### Forbidden terms

```json
"forbidden_terms": {
  "site": {"terms": ["internal-host", "corp.example.org", "10.0.2."], "allow_paths": ["docs/hosts.md"]}
}
```

A key is a registered project name (its checkout, worktrees, and clones with the same GitHub origin)
or `owner/repo` (any checkout with that GitHub origin); both apply when both match. The check fails
when a term appears, case-insensitively as a literal substring, in a line the change adds since its
merge base with `origin/<default_branch>` (binary files too), in an added or renamed file's name, or
in a new commit's message. Deleted lines do not count; a moved file's lines count at the new path.
Files equal to or under an `allow_paths` entry are skipped. The report lists `file:line: term` or
`commit <sha>: term`, naming only the terms found.

## Environment variables

| Variable                                           | Set by                | Meaning                                                                                           |
| -------------------------------------------------- | --------------------- | ------------------------------------------------------------------------------------------------- |
| `VALLEY_FLOW_SESSION_ID`, `KLAUS_SESSION_ID`       | session, agent panes  | The session; without it, commands use the most recent session                                     |
| `VALLEY_FLOW_BACKEND`, `KLAUS_BACKEND`             | session, launch       | The running agent's backend: consults pick another family; `_pre-review` takes it as the author's |
| `VALLEY_FLOW_AGENT_BACKEND`, `KLAUS_AGENT_BACKEND` | session               | The session's worker backend default                                                              |
| `VALLEY_FLOW_RUN_ID`, `KLAUS_RUN_ID`               | launch                | The run, in the agent's pane (and on a sandbox host)                                              |
| `VALLEY_FLOW_RUN_EVENTS`, `KLAUS_RUN_EVENTS`       | launch, sandbox runs  | `output`: `_pre-review` prints its check as a stream line                                         |
| `VALLEY_FLOW_SCRATCH`, `KLAUS_SCRATCH`             | launch, local workers | The run's scratch directory                                                                       |
| `VALLEY_FLOW_TMUX_SOCKET`, `KLAUS_TMUX_SOCKET`     | launch, local workers | The real tmux server's socket, for flow's own commands                                            |
| `TMUX_TMPDIR`                                      | launch, local workers | The run's own empty tmux directory; `TMUX`, `TMUX_PANE` unset                                     |
| `XDG_STATE_HOME`                                   | user                  | Self-update's lock and state go in `$XDG_STATE_HOME/klaus` (default `~/.local/state/klaus`)       |
| `CODEX_HOME`                                       | user                  | Codex authentication, also for consults                                                           |

Flow exports the `KLAUS_*` names, and removes the `VALLEY_FLOW_*` name of each variable it sets so
an inherited value cannot override it. When reading a paired variable, it uses `VALLEY_FLOW_*` if
set, even if empty, and falls back to `KLAUS_*` only if unset. A nonempty `TMUX` overrides both
socket variables.

## Files and state

| Path                                              | Holds                                           |
| ------------------------------------------------- | ----------------------------------------------- |
| `~/.klaus/config.json`                            | Host config                                     |
| `~/.klaus/projects.json`                          | The project registry                            |
| `~/.klaus/memory/`                                | Shared Claude coordinator memory                |
| `~/.klaus/sync.log`                               | Background project syncs                        |
| `~/.klaus/consult-snapshots/`                     | One-shot consult snapshots                      |
| `~/.klaus/sessions/<id>/`                         | A session (below); machine-local                |
| `<worktree_base>/`                                | Run and coordinator worktrees; `.repos/` clones |
| `<clone's git common dir>/klaus-continue.lock`    | The continuation lock                           |
| `$XDG_STATE_HOME/klaus/self-update.json`, `.lock` | Self-update state and lock                      |

In a session directory:

| Path                                                         | Holds                                                   |
| ------------------------------------------------------------ | ------------------------------------------------------- |
| `runs/<run-id>.json`                                         | Run state (the coordinator's too)                       |
| `logs/<run-id>.jsonl`                                        | The worker's raw stream                                 |
| `prompts/<run-id>.md`, `.system.md`, `.input.md`             | The brief and the prompts workers read                  |
| `tmux/<run-id>/`, `scratch/<run-id>/`                        | The run's `TMUX_TMPDIR` and `KLAUS_SCRATCH` (mode 0700) |
| `events.jsonl`                                               | The [event log](#events)                                |
| `jobs/`, `consults/`                                         | Job records; consult threads                            |
| `locks/`                                                     | Per-review posting locks                                |
| `pr-transitions.json`, `change-transitions.json` (+ `.lock`) | Transitions already reported                            |
| `checks-dispatches.json`, `restack-dispatches.json`          | What the dispatchers did                                |
| `cleared.jsonl`                                              | Cost and duration of removed runs                       |
| `target.json`, `notifications-marker`                        | Session target; last notifications check                |
| `dashboard.log`                                              | Dashboard notes                                         |
| `workspace/`                                                 | The coordinator's workspace outside a repository        |

Run state records, among others: `id`, `prompt`, `issue`, `pr`, `pr_url`, `branch`, `worktree`,
`tmux_pane`, `budget`, `log_file`, `created_at`, `cost_usd`, `duration_ms`, `backend`, `model`,
`resolved_model`, `effort`, `target_repo`, `clone_dir`, `host`, `approved`, `merged_at`,
`failure_reason`, `needs_attention`, `pushed_branch`, `pushed_sha`, `pushed_subject`, `change_id`,
`delivery` (`pr` or `push`), `valley`, `unready`, `change_repo`, `knot`, `dispatch_reason`,
`repo_key`, `head_sha`, `took_over`, `house_rules`, `shared_git` and `data_ref_push`.

## The data ref

At finalize flow commits, with a temporary index and without touching any working tree, to
`data_ref` (default `refs/klaus/data`) in the local clone: `runs/<run-id>.json`,
`logs/<run-id>.jsonl` and the Claude conversation `sessions/<run-id>.jsonl`. Before a log is stored,
a scan looks for private IP addresses, SSH keys, credential patterns and `.age` secret references
(the repository's own remote hosts are exempt); a log that matches, or cannot be scanned in full, is
held back, only the state is stored, and `valley flow push-log` stores it after review. A trajectory
with sensitive content is not stored. The ref is pushed to origin only when `push_data_ref` is true;
a push sends its whole history, and anyone who can read the repository can read it.

Finalize records the data-ref push in the run's `data_ref_push`: `ref`, `commit` (the commit it
wrote for the run) and `status` (`published`, `not_attempted` when `push_data_ref` is off, or
`failed`). It pushes the ref's tip and records `published` only when the pushed history holds the
run's commit; otherwise `status` is `failed`, `class` is `other` and `message` says so. A failure
adds a `class` (`refused`, `authentication`, `network`, `not_fast_forward`, `unconfirmed` or
`other`) and a `message` holding the remote's `remote:` lines and the status `git push --porcelain`
reports for the pushed ref; a status line for any other ref is ignored. The message stays as the
remote sent it, except that control characters are dropped and a remote line, status or reason
containing `://` or `@` is replaced whole by `[withheld: it contains an address]`. Past 4 KiB it is
cut and ends in `[truncated]`. The record keeps none of git's other output, which can echo origin's
URL with its credentials. Nothing changes the record after finalize; a build without the field drops
it when it rewrites the run. A failed push leaves the run's outcome and the local ref as they are,
and `valley flow status`, the dashboard, `valley flow notifications` and the default
`valley flow watch` stream show it.

The ref is one history per clone. Each finalize moves it only from the tip it built on, rebuilding
its commit when another finalize moved it first, so the next finalize in that clone that publishes
carries every earlier run's commit. To publish sooner, run `git push origin refs/klaus/data` in the
clone. `not_fast_forward` means origin's ref has moved: it may already hold the run's commit,
carried by a later run's push from the same clone, or hold commits the clone lacks (such as another
clone's runs) that no push from the clone publishes over. `unconfirmed` means the remote did not
report the push's outcome, so origin may hold the run's commit.

## Commit hooks and trailers

Every commit an agent makes ends with:

```
Change-Id: <change id>
Klaus-Run: <run id>
```

`Change-Id` is in jj's change-id form (32 characters from `k` to `z`); `Klaus-Run` leads to the run
and its prompt, log, cost and trajectory. Reading a commit's run ID accepts either `Flow-Run:` or
`Klaus-Run:`; when both are present they must agree, or the commit is treated as carrying none. They
label commits for finding and auditing; they grant nothing, and no tool may trust a commit or skip a
check because of them.

Launch installs, for the agent's worktree only (`core.hooksPath` set with `git config --worktree`,
enabling `extensions.worktreeConfig` once on the clone):

- **commit-msg**: strips Claude/Anthropic attribution, adds `Change-Id` unless present and
  `Klaus-Run` unless that run is listed (an amend by a later run keeps the change ID and lists both
  runs), then runs the repository's own `commit-msg` hook. A message empty or made only of `#` lines
  is left alone.
- **pre-commit**: runs `format` from the worktree root, re-stages the files that were staged, then
  runs the repository's own `pre-commit` hook; unstaged changes stay unstaged, and a failing
  formatter stops the commit.

A shared `core.hooksPath` pointing inside `.git/worktrees/` is removed; any other shared value is
the repository's, and its hooks run. The coordinator's worktree gets the hooks without trailers.
Finalize's own salvage and budget-pause commits skip hooks and add the trailers themselves.

## Delivery and finalize

`delivery` is decided at launch from the clone's `origin`: a github.com URL
(`git@github.com:o/r.git`, `https://github.com/o/r`, `ssh://git@github.com/o/r.git`) is `pr`,
anything else `push`; a `--pr` fix is always `pr`. A `push` run never records GitHub work, whatever
URLs its log holds.

Finalize parses the log for cost, duration, session ID, exit status (`klaus_exit` or `flow_exit`
events) and, for `pr` runs, the URL; checks the clone's shared git files against the launch snapshot
(a difference beyond `git push -u` tracking emits `agent:shared-git-changed` and marks the run
needing attention); records processes still running in the worktree as `failure_reason`
(`background work still running (<commands>)`), read from `/proc` or, where `/proc` is absent (as on
macOS), from `ps` and `lsof`; stores the run on the data ref; and removes the worktree and scratch
directory. It deletes the local branch only when its work is on the default branch or origin's live
tip (`git ls-remote`) matches.

**Pushed branches.** A clean run whose branch is on origin records `pushed_branch` and `pushed_sha`
and emits `agent:completed` and `agent:branch-pushed` (`repo`, `branch`, `sha`); the local branch is
deleted. Of the branches the worktree ends on and launch made (not the default branch), a head on
origin counts as pushed when the local branch holds it. The run's change is the pushed head whose
`Change-Id` matches the final head's, else the head of the branch the worktree ends on; when that
differs from launch's, `agent:change-recorded` (`change_id`, `branch`, `head`, `launch_change_id`,
`launch_branch`, and `pushed` when several changes were pushed) precedes `agent:completed`, and runs
recorded as taken over are dropped.

**Integration checks** (each look, never written to the run): one `git ls-remote` per repository, a
fetch of the default branch and pushed branches, then per run: `integrated` when the pushed SHA or
the branch's tip is an ancestor of the default branch; integrated by content (`INTEGRATED~`) when a
first-parent commit made after the run started has the same `git patch-id --stable` as the branch's
diff from the merge base (searching at most 500 commits); `branch-gone` when the branch was deleted
and neither matched; else `awaiting-integration`. A missing clone, an unreachable origin, a pruned
SHA, a too-shallow history or the 500-commit cap leave it awaiting.

**Salvage.** A run that ends without delivering, or crashes, gets a commit of everything uncommitted
that `.gitignore` does not exclude (`wip: klaus finalize salvage <id>`), a plain `git push -u`
unless origin's live tip already matches (not for runs with GitHub work), keeps its local branch,
and emits `agent:needs-attention` with `branch`, `pushed` and `reason` (`no_pr`, `session_limit`,
background work, or the crash). The worktree stays if the commit failed. The attention passes to a
later run of the change once it records `took_over` (at launch, when it continues a branch holding
the salvaged head; at finalize, when its head contains it) and is running or finished. A run with no
commits beyond the default branch and no background work finishes as `agent:completed`.

**Budget pause.** A Claude run stopped at its budget gets `WIP from klaus run <id> (budget paused)`,
a `--force-with-lease` push, a draft opened or updated with the label `klaus:budget-paused` and a
one-line comment, and cleanup. Either `klaus:budget-paused` or `flow:budget-paused` records the
pause. When a follow-up clears the labels at finalize, it emits one `agent:resumed` event. Claude's
structured result takes precedence over a nonzero exit at the budget or turn limit.

## Valley delivery

A repository delivers to a valley when its origin is not GitHub, the pushed head (at launch: the
commit the worktree starts from) has `policy/valley` or a `policy/instance/` directory, and
`valley help` lists `ready` and `changes`. `valley ready`, `valley unready` and every knot call are
bounded by 30 seconds; `valley changes` gets three minutes.

- **Finalize** runs `valley ready --change <change_id> --head <pushed_sha> <branch>`, expecting
  `<repo> <handle> v<n> ready`, records `valley` (repo, handle, version, title) and emits
  `agent:change-ready` (`repo`, `change_id`, `handle`, `version`, `title`, `head`). Any other
  outcome is recorded as `valley.error` and warned about; the run shows `SUBMIT` with the reason.
- With `ready_at_finalize` false, it then runs `valley unready <change_id>`, records `valley.draft`
  and emits `agent:change-draft` instead. A valley without `unready` gets no ready at all; a failed
  unready leaves the change ready and emits `agent:change-ready`.
- **Follow-up launches** return an inherited change to draft first (`valley unready <change_id>`,
  expecting `<repo> <handle> v<N> draft` or `<repo> <handle> draft`) and record `unready`; a change
  that was not ready is left alone, and a failure is recorded as `unready.error` and the launch goes
  on (unless `--expect-draft`).

### Repository names

A run's valley repository is the one `valley ready` reported; else, for a follow-up, the repository
of the run it continues (`change_repo`); else the one `valley unready` reported; else the last
component of the target repository without `.git`. Runs share a change when they share a change ID
and repository name. `repo_key` is `valley:<name>` for these, `owner/repo` on GitHub, else the short
name.

### Valley change states

`valley flow status` and the dashboard read `valley changes --all --json` once per look (the
`valley-changes/v1` view; other schemas are refused) and match a run to the version, in its
repository and change, whose head is its pushed SHA.

| Dashboard                     | `valley flow status`          | Group    | Meaning                                                                                   |
| ----------------------------- | ----------------------------- | -------- | ----------------------------------------------------------------------------------------- |
| `REVIEW`                      | `awaiting-review`             | yours    | a draft left by finalize for the coordinator's review; approve refuses it                 |
| `READY`                       | `awaiting-approval`           | yours    | the active version, ready, awaiting approval                                              |
| `APPROVED`                    | `approved`                    | pipeline | an integration request is filed and undecided, or stale only because checks are `pending` |
| `INTEGRATED`                  | `integrated`                  | done     | the change landed (every version shows it)                                                |
| `UNCERTAIN`                   | `uncertain`                   | yours    | a request consumed or failed without landing evidence, or an unknown value                |
| `CHECKS FAILED`               | `checks-failed`               | yours    | stale, and the version's checks failed                                                    |
| `CONFLICTING`                 | `conflicting`                 | yours    | stale, and the current restack report says `conflict`                                     |
| `STALE`, `REJECTED`, `FAILED` | `stale`, `rejected`, `failed` | yours    | the integrator's decision                                                                 |
| `WITHDRAWN`                   | `withdrawn`                   | done     | the change was withdrawn                                                                  |
| `OLDER VERSION`               | `older-version`               | done     | a newer version exists and the change has not landed                                      |
| `DRAFT`                       | `draft`                       | yours    | returned to draft with no run of it running                                               |

Landing is the change's `landed_version`, else any version `landed` or `landed-recorded`. A run the
listing lacks, or a failed listing, falls back to the git integration check, reading `READY` (or
`REVIEW`) rather than `GONE`. A row the dashboard has seen keeps its last verdict marked
`(last known)`. While a listing fails, the dashboard shows
`valley unreachable: <reason>; showing git's view` and `valley flow status` prints it on stderr.
`valley changes --checks` adds checks; a valley refusing the flag is listed without it, and the
refusal is remembered for 10 minutes.

### Restack-conflict dispatch

flow owns a change when its record's owner is the clone's `valley.principal`. A report
(`refs/the-valley/restack/main/<change-id>`) counts while the valley calls it current and the
change's record, active version, head and branch match it; flow re-reads origin's refs before
acting. The dispatch is `valley unready`, then
`valley flow launch [--repo <project>] --change <change-id> --run-id <id> --expect-owner <principal> --expect-branch <branch> --expect-head <head> --expect-draft --reason restack-conflict "<brief>"`.
The brief names the change, version, head, branch and main's tip, never the report's paths or text.
Limits: no second run on a change, 60 seconds between dispatches, at most 3 runs until a clean
report, once per input set (head, main's tip), 16 input sets remembered per change. An attempt is
given back only when no worker started (stopped before launching, or exit status 3). Events:
`change:restack-dispatched`, and `change:restack-stalled` with a `reason` once per input set.
`valley flow status` shows `restack` in CONFLICTS.

### Failed-check dispatch

For each failed head of a ready change flow owns, the dispatch is
`valley flow launch --change <change-id> --run-id <id> --expect-owner <principal> --expect-branch <branch> --expect-head <head> --reason checks-failed <brief>`.
Once per failed head, never while a run works on the change, and at most 3 failed attempts (a fix
whose next version still fails counts) until the checks pass; recorded in `checks-dispatches.json`.
When flow stops short of a head (its attempts spent, or the launch failed), it records that and then
emits `change:checks-stalled`, once for that head. A pass that cannot save the record neither
dispatches nor emits for the head, and fails with the error.

### Knots

`--issue` is a knot when its form is `oc-`, `bd-`, `ida-` or `qn-` with 7 hex digits, optionally
`<project>:` first. Launch refuses it with `--pr`, outside a valley repository, without
`valley knot`, and for a knot that is not an active outcome or bug, or that a running or unknown run
claims; a claim left by a run flow knows has ended is released (`--if claimed_by=<run>`) and taken
over. The knot, parents and comments go into the brief in a `## Project notes (untrusted)` block
between marker lines carrying a random token. Just before the worker starts,
`valley knot claim <id> --run <run-id>` (a refusal fails the launch). Finalize, once the head is
ready, runs `valley knot link <id> --change valley:<repo>/<change-id>` and
`valley knot unclaim <id> --if claimed_by=<run-id>`; a failed run comments (`valley knot comment`)
and releases instead. When the change lands, the dashboard comments with the version and commit and
sets the knot `done` (outcome) or `closed` (bug) only if every linked change landed, no child is
active and nobody claims it, with `--if` conditions on status, claim and links. Recorded as
`knot.landed`, `knot.closed`, `knot.error`.

## Events

Each line of `events.jsonl` is
`{"timestamp": <RFC3339>, "run_id": ..., "type": ..., "data": {...}}`.

| Type                                                  | When                                                               |
| ----------------------------------------------------- | ------------------------------------------------------------------ |
| `agent:started`, `agent:completed`                    | A run started; finished                                            |
| `agent:pr-created`                                    | A run opened GitHub work                                           |
| `agent:branch-pushed`                                 | A run finished with its branch on origin, no GitHub work           |
| `agent:change-recorded`                               | Finalize recorded another change or branch than launch             |
| `agent:change-ready`, `agent:change-draft`            | A pushed head became a ready, or draft, version                    |
| `agent:needs-attention`                               | Salvaged or failed; needs the operator                             |
| `agent:shared-git-changed`                            | The clone's shared git files changed during the run                |
| `agent:data-ref-push`                                 | Finalize published the data ref, or kept it local                  |
| `agent:data-ref-push-failed`                          | Finalize's push of the data ref failed (`class`, `message`)        |
| `agent:paused`, `agent:resumed`                       | Budget pause; its follow-up finalized                              |
| `agent:ci-passed`, `agent:ci-failed`                  | CI on an agent's GitHub work                                       |
| `pr:awaiting-approval`, `pr:approved`, `pr:merged`    | GitHub work ready for approval; approved; merged (once each)       |
| `pr:approval-changed`                                 | `valley flow approve` changed flow's approval                      |
| `pipeline:stalled`                                    | The pipeline gave up on GitHub work                                |
| `change:approved`, `change:integrated`                | A ready version got a request; the change landed (`landed_commit`) |
| `change:stale`, `change:rejected`, `change:failed`    | The integrator's decision on the active version                    |
| `change:withdrawn`                                    | The change was withdrawn                                           |
| `change:new-version`                                  | A version this session did not record (`uploader`)                 |
| `change:checks-failed`, `change:checks-passed`        | The host's verdict on a ready version's checks (`failed`)          |
| `change:restack-dispatched`, `change:restack-stalled` | A restack run launched; flow stops short                           |
| `change:checks-stalled`                               | Flow stops short of fixing a failed head's checks (`reason`)       |
| `consult:started`, `consult:completed`                | A consult backend call                                             |
| `review:started`, `review:completed`                  | A `valley flow review`                                             |
| `phase:started`, `phase:completed`                    | A run phase (`setup`, `agent`, `finalize`)                         |
| `check:started`, `check:completed`                    | A check an agent ran                                               |

The default `valley flow watch` filter is every type above except `agent:started`,
`agent:completed`, `agent:paused`, `agent:resumed`, `agent:ci-*`, `agent:change-recorded`,
`agent:shared-git-changed`, `agent:data-ref-push`, `pr:awaiting-approval`, `pr:approval-changed`,
`phase:*` and `check:*`, plus the reserved types `agent:error`, `ci:failed`, `ci:passed` and
`pr:comment`, which nothing emits.

`pr:approved` and `pr:merged` fire once per piece of GitHub work across restarts; work already
merged when first seen emits neither. Change transitions fire once per mark, under the change's
handle, from the dashboard's minute check; each carries `repo`, `handle`, `change_id`, `version`,
`head`, `title`, `repo_key` and `key`. A change first seen is recorded as it stands, without events.
Dashboards on one session share the transition files under their `.lock`. `valley flow status` emits
nothing.

**Consult events** share a `run_id` `consult-YYYYMMDD-HHMM-xxxxxxxx` (one pair per panel member) and
carry `backend`, `model`, `effort`, `role`, `thread`, `dir`, `repo`, `commit`, `panel`, `prompt`
(first line, up to 120 characters) and `prompt_file`; `consult:completed` adds `duration_ms`,
`success` and on failure a short `error` (`timeout after 10m0s`, `interrupted`), never model output.
**Review events** carry `backend`, `model`, `repo`, `pr_number`, `author`, `post`, then
`duration_ms`, `success`, `findings`, `review_url` or `error`. SIGINT and SIGTERM still emit the
completion; outside a session nothing is emitted.

### Run fields on events

Run events (`agent:*`, `phase:*`, `check:*`, `pr:approval-changed`) also carry:

| Field                    | Meaning                                                                                                                                                                                     |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `repo_key`               | `owner/repo`, `valley:<name>`, else the repository's short name                                                                                                                             |
| `change_id`, `pr_number` | The run's change; its GitHub number once it has one                                                                                                                                         |
| `key`                    | `owner/repo#12`, else `<repo_key>@<change_id>`                                                                                                                                              |
| `dispatch_reason`        | `launch`, `pr-follow-up`, `budget-resume`, `change-follow-up`, `resume`, `ci-fix`, `review-fix`, `review-comment-fix`, `conflict-fix`, `restack-conflict`, `checks-failed`, or a `--reason` |
| `head_sha`               | The tip finalize found on origin, else the worktree's `HEAD` at the end or start                                                                                                            |

Pipeline and change events carry `repo_key` and `key` when known; consult events carry `repo_key`.
Unknown fields are left out.

`phase:completed` carries `phase`, `started_at`, `ended_at`, `duration_ms` (and `cost_usd` for
`agent` where known): `setup` is launch's work before the agent; `agent` runs from pane start to
finalize; `finalize` up to killing the pane. `check:*` carry `kind` (`pre-review`, `build`, `test`,
`lint`), `command`, `source`, `duration_ms` and `success`; `_pre-review` adds `lint_ms`,
`review_ms`, `findings`, `blocking`, `lint_failed`. `_format-stream` times the build, test and lint
commands it recognises in the stream (`go test`, `go vet`, `cargo build`, `npm test`,
`make <target>`, `nix build`, `nix flake check`, ...); a command line that also runs
`klaus _pre-review`, `valley flow _pre-review`, or `valley-flow _pre-review` gets
`includes_pre_review`. On a sandbox host, `_pre-review` prints `klaus-check-completed: {...}`. The
pane's formatter accepts that prefix and `flow-check-completed:` and records the report with
`via: stream`.

## Sandbox hosts

With `sandbox_host` set (or `--host`), launch checks the host
(`ssh -o ConnectTimeout=3 -o BatchMode=yes <host> true`), creates the worktree path there and syncs
the worktree with `rsync -az --delete`; the agent runs over SSH and its stream, formatting and
finalize stay local. An unreachable host falls back to local execution. A sandbox worker gets no
`KLAUS_SCRATCH` or private `TMUX_TMPDIR`; its prompt says to keep scratch files in a `mktemp -d`
directory, and nothing removes the worktree copy. The dashboard tags such runs `[sandbox]` and shows
reachability; `valley flow status` shows HOST. Claude conversations stay on the sandbox host, so its
runs are never replayed.

## Webhook receiver

| Key                                  | Default           | Meaning                                                         |
| ------------------------------------ | ----------------- | --------------------------------------------------------------- |
| `webhook.port`                       | `9800`            | Port the dashboard listens on                                   |
| `webhook.path`                       | `/webhook/github` | Path of deliveries                                              |
| `webhook.relay_url`                  | none              | Public URL of the relay                                         |
| `webhook.secret_file`                | none              | File holding the secret `setup` gives GitHub to sign deliveries |
| `webhook.poll_fallback`              | `false`           | Also poll every 30 seconds                                      |
| `webhook.reconcile_interval_seconds` | `300`             | Full re-fetch interval when not polling; negative disables      |

A delivery names the work or repository to re-fetch from GitHub; the receiver builds no status from
it and checks no signature. Only one dashboard per user holds the port; another polls as if no
webhook were configured, and its source line says so. The source line shows the age of the last
delivery (`· last event 5m`), dim under 30 minutes, yellow under 2 hours, red beyond; a long silence
may be a quiet repository or a broken path.

## Constraints

- Sessions, launches and `valley flow scaffold` run inside tmux; agents are headless and take no
  input.
- A run ends with its agent's turn; background work still running is interrupted and recorded.
- No approval is automated: flow posts no approving review, and `valley flow ready` never approves.
- The pipeline dispatches nothing on work a fix run is on, spaces dispatches 60 seconds apart, and
  stalls work after 3 failed attempts of a kind.
- Briefs of any length go through files; inline pane content is capped at 12000 bytes.
- Budgets, budget pause and `--replay` are Claude-only; `--resume-from` never moves a conversation
  between backends or machines.
- Consult attachments total at most 200 KB; each consult call has a deadline (default 10 minutes);
  `valley flow merge` waits at most 10 minutes for CI; the continuation lock waits at most 5
  minutes.
- `valley ready`, `valley unready` and knot calls are bounded by 30 seconds, `valley changes` by
  three minutes; the dashboard holds one telemetry follow slot per valley host.
