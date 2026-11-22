# flow design

flow, the agent-run client that `valley flow` runs, splits agent work three ways. The coordinator,
an interactive agent you talk to, does the thinking: it researches and writes briefs. Agents do the
building, each headless in its own worktree. The dashboard does the mechanical following: CI,
reviews, conflicts, failed checks, landings. The operator approves. Toward a valley, flow is a
client: the valley restacks, runs checks and lands; flow decides what to build and resolves what the
valley reports from the states it lists. Commands and formats are in the
[reference](./reference.md).

## A run's life

```
valley flow launch
  → run ID (YYYYMMDD-HHMM-<8 hex>), branch name, change ID
  → git fetch origin <default>; git worktree add <worktree_base>/<run-id> -b <branch>
  → hooks, shared-git snapshot, runs/<run-id>.json, prompts/<run-id>.md
  → a window of tmux session klaus-agents-<session-id> runs:
      <worker> < prompt | tee logs/<run-id>.jsonl | klaus _format-stream; klaus _finalize <run-id>
klaus _finalize
  → cost, duration, session ID and outcome from the log
  → deliver: record GitHub work, or the pushed branch (and valley ready), or salvage, or pause
  → scan, then commit state, log and conversation to the data ref
  → remove the worktree, the scratch directory and the pane
```

The worker reads its prompt on stdin from a file, so a brief of any length avoids tmux's
command-line limit and the shell. `_format-stream` renders the JSONL stream in the pane and times
the checks it recognises. `_finalize` runs in the pane's own shell after the worker exits, however
it exits.

**Isolation.** Each agent has its own worktree, so agents cannot step on each other or on your
checkout. Their panes live in a detached tmux session owned by the server, so they take no screen
space and survive the coordinator exiting. A run ends when the agent's turn ends: every worker
prompt says to run long builds in the foreground, and finalize records background work it finds
still running. Scratch output goes to `KLAUS_SCRATCH`, outside the worktree, because salvage commits
whatever the worktree holds uncommitted. A worker's writes to its clone's git config,
`info/attributes` or hooks reach every worktree of the clone, so launch snapshots them and finalize
compares.

**Workers do not reach the tmux server by accident.** A local worker starts without `TMUX` and
`TMUX_PANE`, and with `TMUX_TMPDIR` set to its run's own empty directory, so a `tmux kill-server` it
runs reaches an empty location rather than the server holding your session and every agent. flow's
own commands in the worker reach the real server through `KLAUS_TMUX_SOCKET`. This guards against
accidents, not against a hostile worker: the worker runs as the server's Unix user and is given that
socket's path. tmux falls back to `/tmp` when `TMUX_TMPDIR` names a missing directory, so a worker
that points it at one can still reach a server on tmux's default socket.

**Hooks per worktree.** Trailers and formatting are added by hooks, never by the prompt, so an agent
cannot forget them. flow sets `core.hooksPath` per worktree (`extensions.worktreeConfig`), leaving
the main clone and other worktrees their own hooks, and chains to the repository's hooks.

## Persistence

Session state (`~/.klaus/sessions/`, or `~/.valley/flow/sessions/`) is machine-local and disposable.
A finalized run's state, log and Claude conversation are committed to `refs/klaus/data` in the local
clone: a ref outside `refs/heads`, so it shows as no branch, written with git plumbing and a
temporary index, so no working tree changes. Before a log is stored a regex scan looks for private
addresses, keys and credentials; a log it flags, or cannot scan in full, is held back. The ref stays
local unless `push_data_ref` is on, because transcripts hold prompts, file contents and environment
details and a pushed ref is readable by anyone who can read the repository.

**Observed, not recorded.** Whether a pushed branch landed is checked against the run's clone each
time flow looks and never written to the run; where a valley lists the change, its listing decides.
What the dashboard records is what it has reported: `pr-transitions.json` and
`change-transitions.json` make each transition an event exactly once across restarts, and dashboards
on one session take a lock to update them, so only one reports each. A change first seen is recorded
as it stands, so a first start reports no history.

Configuration is JSON, read with the standard library; prompts are Go templates, so one default
works for any repository and a repository can replace it.

## The GitHub pipeline

The dashboard evaluates each piece of GitHub work on every poll or webhook against ordered rules and
moves it through the stages `ci_pending`, `ci_failed`, `ci_passed`, `review_pending`, `approved`,
`needs_rebase`, `merging` and `merged`, with `budget_paused` and `stalled` beside them.

- **Budget paused** comes first: a draft labelled `klaus:budget-paused` with no agent running waits
  for the operator, whatever CI says.
- **CI failed**: a fix agent (`valley flow launch --pr`) is told to read `gh pr checks` and
  `gh run view --log-failed`. A fix whose agent finished while CI still fails counts as failed.
- **Changes requested**, or new trusted comments: a fix agent addresses them, and the pipeline then
  resolves the review threads.
- **Conflicts** with passing CI: a rebase agent rebases onto main, resolves, tests and pushes;
  approval is not required.
- **Behind** and approved: `gh pr update-branch` satisfies an up-to-date rule; the merge follows on
  a later poll once CI re-ran.
- **Approved**, green, conflict-free, with `auto_merge_on_approval`: `valley flow merge --yes`.

Guards keep dispatch from spinning. No dispatch while a fix run, including one the coordinator
launched with `--pr`, works on the branch, since intermediate commits of a multi-step fix may fail
CI. Dispatches for one piece of work are 60 seconds apart. CI fixes, review fixes and rebases each
stall after 3 failures. Merges and branch updates each get 3 tries per approval, 60 seconds apart;
losing and regaining approval resets them. A failed dispatch stalls the work with one
`pipeline:stalled` naming the error, and nothing is dispatched again until an input the dispatch
read changes (head, CI result, review decision, trusted comments), since retrying on the same inputs
fails the same way. When GitHub cannot be reached (timeout, rate limit), the work is left as it was,
never treated as open.

GitHub's own approval is detected and recorded like `valley flow approve`. A cross-model review
posted by `valley flow review` is trusted when its author is the authenticated gh user, so its
inline findings feed the review-fix loop and its breaker; the marker alone grants nothing, and the
pipeline does not start cross-reviews itself.

## Delivery to a valley

**Ready at finalize.** The valley identifies the uploader from `valley.principal`, which the host
sets, not flow. Because the valley has no verb that records a version as a draft, finalize records a
draft by running `valley ready` then `valley unready`. `approve` refuses a draft and the integrator
lands nothing for one, so a request filed between those two calls waits until the change is ready
again. A follow-up returns its change to draft before its agent starts so nobody approves a version
the agent is replacing; finalize's `valley ready` re-readies that version when the head is
unchanged.

**One commit per change.** A valley lands a change as one commit under its head message and takes it
only as a line of commits on main. A follow-up therefore amends and restacks rather than merging,
and pushes with a lease on the head launch found. A concurrent push makes the agent's push fail
instead of overwriting it, and the valley keeps every version's head so rewriting loses nothing.

**Ownership.** A change is flow's when its record's owner is flow's principal. The latest uploader
does not count: the restacker uploads the versions it rebases. flow never takes over a change it
does not own.

**Restack conflicts fail closed.** A restack report counts only while the valley calls it current
and everything it observed still holds; flow re-reads origin's refs itself before acting. It returns
the change to draft and launches nothing unless the valley then lists it as flow's draft at the same
head and branch. Launch checks those conditions again under its continuation lock, before creating
the worktree and before starting the worker (`--expect-*`). Nothing from the report reaches the
brief beyond names and commits flow read itself: the run finds the conflicts by rebasing. A change
sitting on other changes, or a conflict in a change below, is left to the coordinator.

**Accounting is conservative.** The attempt and run ID are recorded before launch; the attempt is
given back only when flow knows no worker started (it stopped first, or launch exited with status
3). Any other failure keeps the launch pending, and the change busy, until a run by that ID is
recorded or flow proves none runs: no launch holds the continuation lock, no run is recorded, and no
live pane names it. Time alone settles nothing. flow acts once per set of inputs and remembers the
outcome, so an unchanged conflict is not retried. Failed checks are dispatched with the same guards
as CI fixes.

**The listing decides; telemetry wakes.** Where a change stands is the valley's `valley changes`
listing, read once per look for every repository. A telemetry record only triggers a check sooner.
When the listing fails, git's view stands in, a known verdict is kept as `(last known)`, and the
failure is shown, never read as "no change".

**Knots** are read and written only through the `valley` CLI. A knot's text, parents and comments
reach the agent as untrusted notes in a block fenced by marker lines with a random token, so nothing
in them can close the block. Claims, links and closes carry `--if` conditions on what flow read, so
a concurrent claim or link makes the close a conflict rather than overwriting it.

## Messaging a running agent

Workers run in print mode (`claude -p`, `codex exec`, agy's print mode) and read the prompt on stdin
from a file; nothing reads the pane's terminal, so text pasted into a worker's pane never reaches
the session. An interactive coordinator does take a pasted message, but only when its interface is
at a prompt: a dialog swallows the paste.

Feeding a Claude worker its input as `--input-format stream-json` over a per-run FIFO delivers a
multi-line message mid-turn as one message and records it in the Claude transcript (not in flow's
log); a message sent after the turn's result is lost. A `valley flow message` command built that way
is **not built**: it would make every launch depend on FIFO setup, where a mistake fails launches
outright. To correct an agent, `valley flow launch --resume-from <run-id>` continues its
conversation in a fresh run, losing only the turn in flight.

## Trust boundaries

**Only the operator approves.** `valley flow approve` and `valley flow merge`'s approval check are
the human gate for GitHub work; the coordinator prompt forbids approving. Cross-model reviews are
`COMMENT` reviews, never approvals. For a valley, `valley flow ready` marks a reviewed head ready
and flow holds no approval key: approval is the valley's `approve`, run by the operator. The
dashboard only displays `approve_command`, shell-quoting each value so a branch name cannot inject a
command.

**Agents run unattended.** Workers run with each CLI's permission bypass. Run untrusted tasks on an
isolated sandbox host; the gates above hold whatever an agent does.

**The agent cannot steer its own finalize.** Finalize reads `ready_at_finalize` and `push_data_ref`
from the checkout the worktree belongs to, never the agent's worktree. `forbidden_terms` is host
config only: the list is private, and a repository cannot turn off its own check, which runs before
any diff reaches a reviewer. `self_update` is host config only: a repository must not choose what a
host installs.

**Labels grant nothing.** `Change-Id` and `Klaus-Run` trailers find and audit commits; no tool may
trust a commit or skip a check because of them. The cross-review marker counts only from the
authenticated gh user.

**Content is data.** Knot notes, restack reports and review text are never instructions: notes are
fenced as untrusted, report text is never passed on, and a conversation comment by the work's author
counts only with an explicit `/klaus fix` or marker, since the operator and agents post through the
same account. Agent replies carry a marker and are ignored, and an unmarked one is still an author
comment without opt-in.

**Self-update installs only what was asked.** Only the configured branch installs, pinned to the
commit `git ls-remote` returned. A repository whose flake offers no `packages.<system>.klaus` is
refused before anything is built; flow builds exactly that attribute, smoke-tests it, and keeps the
previous install for rollback. The repository URL is limited to known transports and characters nix
leaves unchanged, so git and nix read the same source.

**Dispatchers fail closed.** A dispatched launch checks the valley and origin again just before the
worker starts and refuses on anything it cannot confirm.

**A webhook delivery is only a signal.** The dashboard's receiver does not check signatures; it
takes a delivery as a reason to re-fetch the named work from GitHub through the same path polling
uses, and never builds status from the payload, so a forged delivery costs one extra fetch.
Signatures are for the relay to check, with the secret `valley flow webhook setup` gives GitHub.
