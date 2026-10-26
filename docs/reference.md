# Reference

This manual specifies the commands, environment variables, git refs, wire formats, JSON outputs, and
CUE schemas of the-valley.

Claims below separate code guarantees (`implemented`), instance configuration (`deployment choice`,
covering `valley.cue`, `services.valley`, `policy/`, and `identity/`), and mechanisms that do not
work yet (`not built`). Unless a rule or option is marked `deployment choice` or `not built`, it is
`implemented` at HEAD. See [Concepts and glossary](./concepts.md) for how the pieces fit together
and [A guide to working in the valley](./guide.md) for step-by-step workflows.

- [CLI reference](#cli-reference) — `valley` subcommands, flags, exit codes, naming rules, and host
  helpers.
- [Environment variables and configuration](#environment-variables-and-configuration) — git config
  keys, environment variables, client state files, and NixOS options.
- [Project storage ref layout and bounds](#project-storage-ref-layout-and-bounds) — ref namespaces,
  push permissions, object types, and size limits.
- [Wire formats and JSON schemas](#wire-formats-and-json-schemas) — change records, restack reports,
  review records, signed notes, digest manifests, transfer statements, outcome records, knots, CLI
  JSON contracts, the web export, and the web server.
- [CUE configuration schemas](#cue-configuration-schemas) — `schema/valley.cue`,
  `schema/verification.cue`, `schema/web.cue`, `schema/attestation.cue`, `schema/identity.cue`,
  `schema/node.cue`, and `schema/events.cue`.

---

## CLI reference

`valley` is the command-line tool for authoring, reviewing, approving, rebasing, and inspecting
changes and the plan graph.

```sh
valley <verb> [args]
```

### Naming changes, handles, branches, and requests

Commands that inspect or act on an existing change (`status`, `review`, `approve`, `rebase`,
`unready`, and `withdraw`) accept a Change ID, a short handle, or a branch name, whereas
`valley ready [<branch>]` takes a branch name as its positional argument.

```sh
valley status kqxm                        # by handle
valley status kqxmnxvpwlkmqtsrzyoukklnpmqrstuv  # by 32-letter Change ID
valley status demo:kqxm                   # by <repo>:<handle>
valley status topic                       # by branch name
valley status origin/topic                # explicitly as a branch
```

- **Change ID (`[k-z]{32}`):** A 32-character lowercase identifier using letters `k` through `z`.
  `valley ready` generates a new Change ID from 16 random bytes (encoding each nibble `0`–`15` as
  `z` down to `k`).
- **Handle (`[k-z]{4,32}`):** The shortest prefix of a Change ID (at least 4 characters) that no
  other change currently recorded on `origin` shares. A handle can lengthen if a later change shares
  its prefix. In single-repository commands, `<repo>:<handle>` is also accepted when `<repo>`
  matches the current repository's name. In multi-repository listings (`--all`), a Change ID
  recorded in more than one listed repository is formatted as `<repo>:<prefix>` in each.
- **Branch name:** Any ref name accepted by `git check-ref-format refs/heads/<name>`. No
  slash-separated segment may be empty, start with `.`, or end with `.lock`; the name may not end
  with `.`, contain `..` or `@{`, or contain ASCII control characters, space, or `~ ^ : ? * [ \`. In
  change records (`record.json`), `branch` must also be valid UTF-8; in restack reports
  (`report.json`), branch names are additionally bounded to 255 bytes (`maxBranchLen = 255`).
- **Resolution order:**
  - A name starting with `origin/` is always treated as the branch `<branch>`.
  - A handle that is also a branch name on `origin` is refused as ambiguous unless the change's
    active version records that branch.
  - When a branch name is given (with or without `origin/`), `review`, `approve`, `rebase`,
    `unready`, `withdraw`, and `status` check all records on `origin` whose active version has not
    landed (`landed` or `landed-recorded`). If exactly one unlanded change's active version records
    that branch, the command acts on that change at its recorded active version head. If more than
    one unlanded change's active version records that branch, the command refuses and lists the
    colliding changes. If no unlanded change is on that branch, `status`, `review`, `approve`, and
    `rebase` act on the branch directly, whereas `unready` and `withdraw` refuse because they
    require a recorded change.
- **Request name (`<request>`):**
  - For a recorded change: `c-<change-id>` (such as `c-nxvpwlkmqtsrzyoukklnpmqrstuvwxyz`), stored at
    `refs/the-valley/integration-requests/main/c-<change-id>`.
  - For an unrecorded branch: the branch name with every `/` replaced by `-`. A branch whose request
    name would take the reserved `c-<change-id>` shape is refused by `valley approve` and
    `valley status`.
- **Principal, project, and run names:** Must match `^[a-zA-Z0-9][a-zA-Z0-9._-]*$`. In the plan
  graph, principal, project, and run names are additionally bounded to 64 bytes (`MaxName = 64`).

### Exit codes and git environment sanitization

Every `valley` command sanitizes git object-replacement environment variables before running git and
exits with a small set of status codes.

```sh
export GIT_NO_REPLACE_OBJECTS=1
unset GIT_REPLACE_REF_BASE GIT_GRAFT_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES
```

| Exit code | Meaning                                                                                                                                                                                                                               |
| :-------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `0`       | Command succeeded (`self-update` also exits `0` when already up to date, when another run holds the lock, when pinned, or when the current branch tip already failed and `--retry` is not given).                                     |
| `1`       | Command refused or failed (invalid state, failed check, rejected push, missing git config, runtime error, or invalid CLI flags in `status`, `ready`, `unready`, `withdraw`, `changes`, `pending`, `rebase`, `plan`, or `bin/valley`). |
| `2`       | Invalid `valley knot` or `valley self-update` CLI usage, bad `VALLEY_NOW` timestamp in `knot` or `plan`, or unknown `valley-go` verb.                                                                                                 |
| `3`       | Plan graph compare-and-set or precondition conflict in `valley knot` (such as concurrent modification of a field without `--force`, a failed `--if <field>=<value>` check, or a claim held by another run).                           |
| `130`     | Interrupted by `SIGINT` or `SIGTERM` during `valley review`, `valley approve`, `valley rebase`, or `valley self-update`.                                                                                                              |

### `valley ready`

`valley ready` creates or updates a change record on `origin`
(`refs/the-valley/changes/main/<change-id>`) and marks its active version `ready`.

```sh
valley ready --stamp
valley ready [--head <sha>] [--change <id>] [--if-active <sha>] [--stamp] [<branch>]
```

- **Arguments and flags:**
  - `[<branch>]`: Branch name (with or without `origin/`). Defaults to the current checked-out
    branch (`git symbolic-ref --short HEAD`).
  - `--stamp`: For a new change whose local branch tip is not yet on `origin`, amends the local
    branch head with a `Change-Id: <id>` trailer, pushes the branch to `origin` with a lease on the
    remote branch state read at start, and creates `v1`. Refused if the change already has a record
    on `origin`, if the local head commit is already on `origin/<branch>`, or if combined with
    `--head` or `--if-active`.
  - `--head <sha>`: Marks a specific commit `<sha>` ready instead of `origin/<branch>`. `<sha>` must
    already be an ancestor of `origin/<branch>`.
  - `--change <id>`: Specifies the 32-letter Change ID explicitly. Required when the commit has
    neither a `jj` `change-id` commit header nor a `Change-Id:` trailer and should update an
    existing change.
  - `--if-active <sha>`: Full commit object ID. Adds the new version only if the change is currently
    `ready` and its active version's head is `<sha>`.
- **Behavior:**
  - Requires `git config valley.principal` to be set to a valid principal name.
  - Resolves the Change ID from `--change`, the commit's `change-id` header (`jj`), or the commit's
    `Change-Id:` message trailer, or generates a new ID. Refuses if a header, trailer, or `--change`
    disagree, or if multiple `Change-Id:` trailers carry different IDs.
  - Refuses if the commit is already an ancestor of `origin/main`, has no merge base or multiple
    merge bases with `origin/main`, or includes a merge commit in `base..head`.
  - If the change has no record on `origin`, creates `v1` (`state: "ready"`, `seq: 1`, `active: 1`).
    If the change is `draft` or `withdrawn` and the target head equals the active version's head,
    transitions the record back to `ready` (`seq + 1`) without adding a version. If the change is
    already `ready` at the active version's head, refuses because the head has not changed.
    Otherwise, appends `v(N+1)` (`state: "ready"`, `seq + 1`, `active: N+1`).
  - Pushes `refs/the-valley/changes/main/<id>` with `--force-with-lease` on the prior record commit
    and prints `<repo> <handle> v<n> ready`.

### `valley unready`

`valley unready` pauses a `ready` change by setting its recorded state to `draft`.

```sh
valley unready kqxm
valley unready <change>
```

- **Behavior:** Requires `git config valley.principal`. Reads the change record on `origin` (by
  Change ID, handle, or a branch that an unlanded change's active version is on); refuses and exits
  `1` if the change is already `draft` or `withdrawn`. Otherwise writes a single-parent record
  commit (`P = [old]`) setting `state: "draft"` and incrementing `seq` by 1, leased on `old`. Prints
  a note to `stderr` if a pending integration request still exists on `origin` (an existing request
  is not deleted by `unready`, and the integrator refuses to land it while the change is `draft`, so
  it stands until the change is readied again).

### `valley withdraw`

`valley withdraw` marks a `ready` or `draft` change `withdrawn` so the integrator voids its pending
requests on its next pass.

```sh
valley withdraw kqxm
valley withdraw <change>
```

- **Behavior:** Requires `git config valley.principal`. Reads the change record on `origin` (by
  Change ID, handle, or a branch that an unlanded change's active version is on); refuses and exits
  `1` if the change is already `withdrawn`. Otherwise writes a single-parent record commit
  (`P = [old]`) setting `state: "withdrawn"` and incrementing `seq` by 1, leased on `old`. If a
  pending request exists on `origin`, warns on `stderr` that the integrator will delete it on its
  next pass while the change stays `withdrawn` (if the change is readied again before that pass, a
  holder of the `approve` grant, also accepted as `request`, must delete the request ref directly).

### `valley pending`

`valley pending` lists work waiting to land in three sections: `ready` changes whose active version
has not landed, unrecorded `branches` ahead of `main`, and `drafts`.

```sh
valley pending
valley pending [--all] [--repo <name>] [--checks] [--json] [-v] [--known-signers <file>] [--integrator-name <name>]
```

- **Flags:**
  - `--all`: Queries every project listed at `refs/the-valley/projects` in the instance repository
    (discovered via `policy/valley` on `origin/main` or `policy/instance/` when the current repo is
    the instance repo), keeping clones under `$XDG_CACHE_HOME/valley/repos`. If discovery is
    unavailable and `--repo` is not set, warns on `stderr` and lists the current repository alone.
  - `--repo <name>`: Queries that one project. When `<name>` matches the current repository, reads
    the local repository directly without discovery; when passed together with `--all`, `--repo`
    takes precedence.
  - `--checks`: Appends a line to each change in human output showing how many checks the composed
    verification policy requires of it, grouped by `quick` and `slow` cost (ignored when combined
    with `--json`).
  - `--json`: Emits [`valley-pending/v1`](#valley-changesv1-and-valley-pendingv1) JSON.
  - `-v`: Warns on `stderr` when a project's plan graph (`refs/the-valley/knots`) cannot be read.
  - `--known-signers <file>`, `--integrator-name <name>`: The known-signers file and the
    integrator's signing name a transfer statement is verified with. Each takes precedence over its
    environment variable and over the host's publication (see
    [Environment variables](#environment-variables)). When either value comes from the publication,
    its `signer-projects` file is read too. While that file names the integrator name, no transfer
    statement is verified. A publication whose `signer-projects` does not read is not used.
- **Behavior:** Groups stacked entries under their top entry by commit ancestry, shows linked knots
  (`<title> · <knot-id>`), marks `includes <handle>` for unlanded non-entry changes (such as
  `withdrawn` changes) contained in a head, and marks `uncertain` when a version's landing cannot be
  verified. Also writes the shell-completion handle cache for the current repository at
  `<git-common-dir>/valley/handles`.

### `valley changes`

`valley changes` outputs every change recorded on `origin` across all recorded states (`ready`,
`draft`, and `withdrawn`, whether submitted or unlanded) as a
[`valley-changes/v1`](#valley-changesv1-and-valley-pendingv1) JSON document.

```sh
valley changes --json
valley changes --json [--all] [--repo <name>] [-v] [--known-signers <file>] [--integrator-name <name>]
```

- **Flags:** `--json` (required; running `valley changes` without `--json` exits `1` and suggests
  `valley pending` for human output), `--all`, `--repo <name>`, `-v`, `--known-signers <file>` and
  `--integrator-name <name>` (as for [`valley pending`](#valley-pending)).

### `valley status`

`valley status` inspects a single change or branch on `origin`, reporting its active version,
landing state, restack report, pending integration request, and last recorded integrator outcome.

```sh
valley status kqxm
valley status [--json] [-v] [--known-signers <file>] [--integrator-name <name>] <change>
```

- **Arguments and flags:**
  - `<change>`: Change ID, handle, or branch name.
  - `--json`: Emits a single-line [`valley-status/v1`](#valley-statusv1) JSON object including
    linked knots.
  - `-v`: With `--json`, warns on `stderr` if the plan graph cannot be read.
  - `--known-signers <file>`, `--integrator-name <name>`: As for
    [`valley pending`](#valley-pending).
- **Behavior:** Fetches the needed objects from `origin` without updating `FETCH_HEAD` or moving any
  local branch. Computes one of 13 status states (`integrated`, `integrated-recorded`,
  `outdated-request`, `outcome-unreadable`, `voided-withdrawn`, `not-requested`,
  `awaiting-decision`, `awaiting-new-decision`, `stale`, `rejected`, `refused-not-ready`,
  `refused-merge`, or `failed`).

### `valley review`

`valley review` pages a change's diff (or a stack layer by layer from bottom to top), shows linked
knots and comparison against any prior approved version, and prompts to approve, rebase, view the
full diff, reject the remote branch, or skip.

```sh
valley review kqxm
valley review [-v] <change>
```

- **Arguments and flags:**
  - `<change>`: Change ID, handle, or branch name. The change's active version must record a
    non-empty `branch`.
  - `-v`: Warns on `stderr` if the plan graph cannot be read.
- **Comparison against the approved version (`valley-go compare`):**
  - Before paging a recorded change, `valley review` identifies the change's **approved version**:
    the greatest version `vM` below the shown version `vN` whose exact `head` commit is named on
    `origin` by a pending integration request or a `land`, `stale`, or `reject` outcome record under
    `c-<change-id>` or an unambiguous branch request name. If the greatest version with evidence is
    ambiguous, no fallback to an older version occurs.
  - If an approved version is found and both versions have a single merge base (`B0` and `B1`) on
    `origin/main`'s first-parent history with `B0` equal to or an ancestor of `B1`,
    `valley-go compare` computes
    [`valley-delta-v1`](#canonical-digest-manifests-valley-tree-v1-valley-inputs-v1-valley-delta-v1)
    between each version's merge base and its head:
    - `clean`: Both versions produce the identical `valley-delta-v1` file delta. `valley review`
      prints `<change> v<n> at <head>: clean restack of approved v<m> at <old head>.`, the two
      bases, and any commits `main` landed between them, and pages nothing.
    - `changed`: The deltas differ. `valley review` pages `changed since approved v<m>`, the paths
      whose delta entries differ, `git range-diff`, and the shown version's diff restricted to those
      changed paths.
    - `none`: No approved version can be compared; `valley review` pages the full log and diff
      against `origin/main` (or the layer below in a stack).
- **Interactive prompt actions:**
  - `[a]pprove` (or `[a]pprove all <n>` at the top of a stack, or `[a]pprove <lands>` on a lower
    layer): Runs all required checks in a temporary worktree of the shown head, signs attestations
    with `VALLEY_ATTEST_KEY`, and pushes the attestations and
    `refs/the-valley/integration-requests/main/<request>` in one atomic, leased `git push`. Not
    offered when the change (or any layer at or below it) is `draft` or `withdrawn`, or after its
    checks fail at that head.
  - `[b]ase` (or `[b]ase all <n>` before paging a stack whose bottom does not fast-forward from
    `origin/main`): Rebases the change or stack onto `origin/main` via `valley-go rebase`, then
    re-reads and re-pages the rebased commits.
  - `[v]iew full diff`: Pages the full log and diff against `origin/main` (or the layer below)
    regardless of approved-version comparison.
  - `[r]eject`: Confirms and deletes the remote branch `refs/heads/<branch>` on `origin`, leased on
    the recorded head (offered only on a single change or the ready top of a stack).
  - `[n]ext layer`: In a stack review, advances to the next layer up.
  - `[s]kip` / `[s]top`: Exits without modifying `origin`.
- **Inline review notes:** Pressing `v` inside `less` opens the diff buffer in `$EDITOR`. Any lines
  inserted by the reviewer are anchored to their file and new-side line number, appended to
  `$XDG_STATE_HOME/valley/notes/<branch>.md`, and printed at the end of the review.

### `valley approve`

`valley approve` files a change's integration request without paging the diff. It reuses the
evidence `origin` already holds for the change's required checks, runs and signs the rest, and
publishes the new evidence with the request in one atomic push.

```sh
valley approve kqxm
valley approve --head 99b5f2f7c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6 kqxm
valley approve [--head <sha>] <change>
```

- **Arguments and flags:**
  - `<change>`: Change ID, handle, or branch name.
  - `--head <sha>`: Approves that exact commit. For a recorded change, `<sha>` must be one of the
    record's version heads (active or prior); without `--head`, `approve` prompts on a terminal to
    confirm the active version's head and title, and refuses off a terminal. For an unrecorded
    branch, `<sha>` (if given) must match `origin/<branch>`'s current tip.
- **Behavior:**
  - Requires the pushing SSH key's principal to hold the `approve` grant on the host (the grant that
    lets a principal approve, also accepted as `request`).
  - Refuses if the change record is `draft` or `withdrawn`, if the commits include a merge, if the
    head brings an unlanded version of another change that is `draft` or `withdrawn`, or if the head
    does not fast-forward from `origin/main` while bringing an unlanded lower change in a stale
    stack (instructing the user to run `valley rebase <change>` first).
  - Composes the verification policy from `origin/main` and the instance repository's
    `refs/heads/main`, and derives the required checks for `base..head`.
  - When a check is required, lists `origin`'s `refs/the-valley/attestations/<tree-digest>/*` for
    the head's tree and fetches the objects they name (`valley-go coverage`). It never counts a
    local attestation ref as evidence. A listing or a fetch that fails is refused, and nothing runs
    or is published.
  - Judges each required `nix` check by the note the integrator judges it by: the first note for the
    check, with the refs in byte order, that `attest verify` accepts under the `known-signers` file
    the host publishes at `refs/the-valley/signers`. It reads each note byte for byte. With no
    publication, nothing is reused.
  - Counts a note as accepted only when one of its verified signers counts in this project. That
    signer is not the publication's `integrator-name`, and the publication's `signer-projects` file,
    when there is one, lets it count in this project. The project is the last path segment of
    `origin`'s URL, without `.git`. A publication whose `signer-projects` is not a file, or does not
    read, is not used, so nothing is reused.
  - Reuses that note when it is pure evidence for the check's name and flake attribute, and records
    `passed`. When `VALLEY_KNOWN_SIGNERS` is set, `attest verify` must accept the note under that
    file too, by a signer that counts in this project, so a local file can narrow what is reused but
    never widen it. A `command` check is never reused.
  - Prints `reusing the evidence origin holds for <change> at <head>:`, then one line per reused
    check naming its signer. For each check whose note it does not reuse, it prints the reason on
    `stderr`, for example
    `valley: not reusing code-build: the integrator judges it by <ref>:code-build/statement.note, and its predicate.result is "failed", not "passed"`.
  - Runs every other required check with `attest run` in a temporary worktree, using
    `VALLEY_ATTEST_KEY` (and optional `VALLEY_ATTEST_NAME`). When every required check is reused,
    nothing runs and no signing key is needed.
  - Before running anything, refuses when `origin` already holds this signer's ref for the tree,
    `refs/the-valley/attestations/<tree-digest>/<key-hash>`. That namespace is create-only, so the
    new evidence would have nowhere to go
    ([bd-7b7d58f](../archive/.the-valley/bugs/bd-7b7d58f-same-tree-re-attestation-cannot-be-published.md)).
  - Pushes the attestation refs stored for the tree (when checks ran) and
    `refs/the-valley/integration-requests/main/<request>` (leased on the prior request value) in one
    `git push --atomic`. When the request already names the head and there is no new evidence, it
    makes no push.
  - Ends with a summary: `request` with the request ref and head, `evidence` for each attestation
    ref it published, `reused` for each ref on `origin` whose evidence it reused, and `checks` for
    every required check.
  - Prints a `time` line after each check's `check` line: the check's name, its result, how long it
    took, and how it was satisfied. It passes `--timings` to `attest run`, so each check is also
    logged to the local [timing log](#the-timing-log). The log is not evidence, and a log that
    cannot be written is a warning: approval continues.

### `valley rebase`

`valley rebase` rebases a change and its entire underlying stack onto `origin/main`, pushes all
updated branches atomically, and records a new version on each rebased change.

```sh
valley rebase kqxm
valley rebase [--known-signers <file>] [--integrator-name <name>] <change>
```

- **Arguments:** `<change>` (Change ID, handle, or branch name). (`valley-go rebase` also accepts
  `--head <full-sha>` when invoked by `valley review`'s `[b]ase`).
- **Flags:** `--known-signers <file>` and `--integrator-name <name>`, as for
  [`valley pending`](#valley-pending).
- **Behavior:**
  - Requires `git config valley.principal` whenever the stack contains a recorded change.
  - Captures one snapshot of `origin` and builds the stack of unlanded changes and branches beneath
    `<change>`. Commits at the bottom that have already landed on `origin/main` (`landed` or
    `landed-recorded`, its transfer statement verified as [`valley changes`](#valley-changes)
    verifies one) drop out automatically.
  - If the stack already fast-forwards from `origin/main`, changes nothing and exits `0`.
  - Refuses without changing anything if any layer in the stack is `draft` or `withdrawn`, has no
    recorded branch, has a missing or moved remote branch (`origin/<branch> != head`), shares a
    branch with another layer, contains a merge commit, or contains a version head of a non-ready
    change or a change outside the stack.
  - Rebases every layer bottom-first in a temporary worktree. On a merge conflict in any layer,
    aborts, pushes nothing, and prints the conflicting layer and file paths.
  - Publishes in two phases:
    1. One `git push --atomic` moving every layer's `refs/heads/<branch>` with `--force-with-lease`
       on the tips read in the snapshot.
    2. Bottom-first, adds a new version to each recorded change's
       `refs/the-valley/changes/main/<id>` just as `valley ready --if-active <old-head>` does. If
       any phase-2 record write fails, prints a manifest and the exact
       `valley ready --change <id> --head <new-sha> --if-active <old-sha> <branch>` command to
       finish it, and exits `1`.

### `valley checks`

`valley checks` composes the instance floor and project verification policy and prints the checks
required for the diff between two commits, along with the matched path classes and carry mode.

```sh
valley checks
valley checks --names --cost quick
valley checks [--schema <file>] [--instance <dir>] [--project <dir>] [--names] [--cost quick|slow] [<base> [<head>]]
```

- **Arguments and flags:**
  - `[<base> [<head>]]`: `<head>` defaults to `HEAD`; `<base>` defaults to
    `git merge-base <target> <head>` where `<target>` is `origin/main` (falling back to local `main`
    only if `origin/main` does not exist).
  - `--schema <file>`: CUE verification schema file (default: `$VALLEY_POLICY_SCHEMA`,
    `$VALLEY_VERIFICATION_SCHEMA`, or `<repo-root>/schema/verification.cue`).
  - `--instance <dir>`: Directory of `*.cue` files for the instance floor. By default, read from
    `policy/instance/` at `origin/main` if present in this repository, or fetched from
    `refs/heads/main` of the repository URL named in `policy/valley` at `origin/main`.
  - `--project <dir>`: Directory of `*.cue` files for the project layer. By default, read from
    `policy/project/` at `origin/main` (or an empty `package verification` layer if
    `policy/project/` holds no `*.cue` files).
  - `--names`: Prints only the required check names, sorted, one per line.
  - `--cost quick|slow`: Requires `--names`; filters the `--names` output to checks with
    `cost: "quick"` or `cost: "slow"`.
- **Behavior:** Reads all added, modified, deleted, renamed (both old and new paths), and copied
  paths from `git diff --no-relative --name-status -M -z <base> <head>`, matches them against
  `policy.classes` (and `policy.unclassified` when any path matches no class), probes whether each
  required check is `mandatory` (pinned by the floor) or `default`, resolves the strictest `carry`
  mode (`exact`, `same-class`, or `same-paths`) across matched classes, and prints the report
  without running any check. A layer's documents are the `*.cue` entries directly in its directory
  whose names do not begin with a dot. The derivation runs in the C locale whatever the caller's
  locale: a glob matches bytes, so `?` matches one byte, and lists sort bytewise.

### `valley plan`

`valley plan` renders the project's live plan graph (`refs/the-valley/knots`) as a milestone tree
followed by `waiting on you`, `in flight`, and `open residuals` sections.

```sh
valley plan
valley plan [--project <name> | --all] [--owner <principal>] [--json] [--known-signers <file>] [--integrator-name <name>]
```

- **Flags:**
  - `--project <name>`: Reads the plan graph of `<name>` on the same host (cannot be combined with
    `--all`).
  - `--all`: Reads the plan graph of every project listed at `refs/the-valley/projects` in the
    host's instance repository.
  - `--owner <principal>`: Overrides `git config valley.principal` when selecting which knots appear
    under `waiting on you`.
  - `--json`: Emits [`valley-plan/v1`](#valley-planv1) JSON.
  - `--known-signers <file>`, `--integrator-name <name>`: As for
    [`valley pending`](#valley-pending), for the changes listed as ready for approval.
- **Glyphs and sections:**
  - Root `outcome` knots (those with no `parent`) are milestones, showing descendant progress
    (`done/of` over non-abandoned descendants). Active non-outcome knots with no `parent` appear
    under `no milestone`.
  - State glyphs: `○` (`ready`), `◐` (`in-progress`), `⊘` (`blocked`), `◇` (`active`, such as an
    open `idea` or `question`, or an unclaimed `outcome` with children that is not itself ready),
    `✓` (`done`: `done`, `closed`, `graduated`, `answered`), `✗` (`abandoned`: `abandoned`,
    `discarded`, `superseded`, `dropped`).

### `valley knot`

`valley knot` reads and writes knots in the plan graph at `refs/the-valley/knots` on `origin`
without a local branch checkout or review step.

```sh
valley knot new outcome "Publish the CLI reference" --owner operator
valley knot <verb> [args] [--project <name>] [--json]
```

Every `valley knot` subcommand accepts `--project <name>` (to act on another project on the same
host) and `--json` (to emit
[`valley-knot/v1`, `valley-knots/v1`, `valley-knot-op/v1`, `valley-knot-log/v1`, or `valley-knot-schema/v1`](#valley-knot---json-valley-knotv1-valley-knotsv1-valley-knot-opv1-valley-knot-logv1-valley-knot-schemav1)).
All write subcommands require `git config valley.principal` and retry lease races up to 5 attempts
(`knot.Attempts = 5`). After a push whose outcome could not be determined, `valley knot` searches
the latest 64 commits (`knot.OpSearch = 64`) on `refs/the-valley/knots` for the operation's
`Valley-Op` trailer before retrying.

| Subcommand | Synopsis and contract                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| :--------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `new`      | `valley knot new <type> <title> [--parent <id>] [--blocked-by <id>]... [--owner <p>] [--target <project>] [--body -]`<br>Creates a knot of `<type>` (`outcome`, `bug`, `idea`, or `question`) in its initial active status (`open` for `outcome`/`bug`/`question`, `raw` for `idea`). Computes `<slug>` from `<title>` (up to 48 bytes) and derives `<id>` as `<prefix>-<first-7-hex-of-sha256(slug)>`; if `<id>` is already taken in the graph, appends `-2`, `-3`, … to `<slug>` (trimming the base slug to fit 48 bytes) until `<id>` is unused. `--blocked-by` and `--target` are valid on `outcome` and `bug` only. `--body -` reads Markdown body text from stdin.                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `show`     | `valley knot show <id> [--at <commit>] [--project <name>] [--json]`<br>Displays all fields, derived edges (`blocks`, `children`, `progress`, `claim`, `ready`, `waiting_on`, `cleared_by_ending`, `dangling`, `unresolved`), Markdown body, and comments for `<id>`. Each comment shows its ID (`<YYYYMMDDTHHMMSSZ>-<16-hex-op>`) and, if edited, when and by whom it was edited. `--at <commit>` shows `<id>` at `<commit>`. `<commit>` must be a full or abbreviated (at least 4 lowercase hex digits) object ID of the fetched tip of `refs/the-valley/knots` or one of its ancestors. Any other commit, or a knot that does not exist at `<commit>`, exits `1`. Even with `--at`, a claim's `age_seconds` and `stale` are computed against the current clock (`VALLEY_NOW` when set), and each `<project>:<id>` in `blocked_by` is resolved against that project's current tip.                                                                                                                                                                                                                     |
| `log`      | `valley knot log <id> [--project <name>] [--json]`<br>Lists every commit on `refs/the-valley/knots` that added, changed, or deleted `<id>`'s knot file or a file under `comments/<id>/`, newest first in topological order, down to and including the root commit. Each entry shows the commit ID, UTC committer time, committer name, `Valley-Op` operation ID, commit subject, and each touched file marked `added`, `changed`, or `deleted`. `knot.ParseCommit` reads the time, committer, and operation ID from the commit object.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `list`     | `valley knot list [--type <t>] [--status <s>] [--owner <p>] [--ready] [--project <name>] [--json]`<br>Lists knots matching all given filters. `--status` accepts `active`, `terminal`, or any status name (`open`, `in-progress`, `done`, `abandoned`, `closed`, `raw`, `exploring`, `adopted`, `graduated`, `superseded`, `discarded`, `answered`, `dropped`). `--ready` filters to active, unclaimed `outcome` or `bug` knots with no unresolved or active blockers.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `set`      | `valley knot set <id> <field>=<value>... [--if <field>=<value>]... [--force]`<br>Updates `status`, `title`, `source`, `owner`, `target`, or `graduated_into` (setting an optional field to `""` clears it; setting `status` on an `outcome` or `bug` to a terminal status also clears `claimed_by` and `claimed_at`). Uses compare-and-set on the initial values of modified fields across lease retries unless `--force` is passed. `--if <field>=<value>` requires `<field>` to equal `<value>` (accepting any of the 15 frontmatter fields, with list fields `blocked_by` and `changes` compared as comma-joined strings); exits `3` on conflict. Changing `title` keeps `<id>` and `<slug>` unchanged.                                                                                                                                                                                                                                                                                                                                                                                              |
| `link`     | `valley knot link <id> (--blocked-by <ref> \| --parent <id> \| --change <link>)... [--if <field>=<value>]...`<br>Adds one or more edges on `<id>`. `--parent <id>` sets `parent` to a local `outcome` (using compare-and-set on the old `parent`). `--blocked-by <ref>` adds a local `<id>` or cross-project `<project>:<id>` to `blocked_by` (on `outcome` or `bug`). `--change <link>` adds `valley:<project>/<change-id>` or `https://github.com/<org>/<repo>/pull/<n>` to `changes` (on `outcome`, `bug`, or `question`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `unlink`   | `valley knot unlink <id> (--blocked-by <ref> \| --parent <id> \| --change <link>)... [--if <field>=<value>]...`<br>Removes the specified `--blocked-by`, `--parent` (accepts `--parent <current-parent-id>` or `--parent=""`), or `--change` edges from `<id>`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `edit`     | `valley knot edit <id> --body - [--if <field>=<value>]...`<br>Replaces `<id>`'s Markdown body with text read from stdin (compare-and-set on the old body). Passing an empty stdin clears the body.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `comment`  | `valley knot comment <id> (-m <text> \| --body -) [--edit <comment-id>]`<br>Without `--edit`, adds a comment file at `comments/<id>/<YYYYMMDDTHHMMSSZ>-<16-hex-op>.md` with `author` set to `git config valley.principal`. `--edit <comment-id>` replaces the body of the comment at `comments/<id>/<comment-id>.md`. The comment keeps its `author` and path; `edited` is set to the current UTC timestamp (`RFC 3339`) and `edited_by` is set to `git config valley.principal`. A `<comment-id>` that does not match `<YYYYMMDDTHHMMSSZ>-<16-hex-op>` exits `2`. A `<comment-id>` not found on `<id>` (including one belonging to another knot) exits `1`. Edits use compare-and-set on the comment file's initial blob across lease retries and exit `3` on conflict. If the comment body already equals the new text, the command prints a note to `stderr` and exits `0`. The CLI does not check who may edit a comment; the host's hook admits an edit only from the comment's `author` or from a principal holding the `approve` grant (also accepted as `request`). Comments are never deleted. |
| `claim`    | `valley knot claim <id> --run <run-id> [--if <field>=<value>]...`<br>Claims an active, unclaimed `outcome` or `bug` for `<run-id>`, setting `claimed_by` to `<run-id>`, `claimed_at` to the current UTC timestamp (`RFC 3339`), and transitioning an `open` `outcome` to `in-progress`. If already claimed by the same `<run-id>`, prints a note to `stderr` and exits `0`; if `<id>` is not active or is an `idea` or `question`, exits `1`; if claimed by another run, exits `3`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `unclaim`  | `valley knot unclaim <id> [--if <field>=<value>]...`<br>Clears `claimed_by` and `claimed_at` on `<id>` (leaving `status` unchanged). Exits `3` if the claim changed concurrently before the release landed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `schema`   | `valley knot schema [--json]`<br>Prints the knot types, statuses, fields, edges, and graph limits in human text or [`valley-knot-schema/v1`](#valley-knot---json-valley-knotv1-valley-knotsv1-valley-knot-opv1-valley-knot-logv1-valley-knot-schemav1) JSON.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |

### `valley tail` and `valley replay`

`valley tail` subscribes to NATS bus events, and `valley replay` publishes one `ref-updated` event
for every current ref in a git repository.

```sh
valley tail [subject]
valley replay [repo-dir]
```

- `valley tail [subject]`: Runs `nats sub <subject>` (default subject: `valley.>`). Uses `NATS_URL`
  or the active `nats` CLI context (default `localhost`).
- `valley replay [repo-dir]`: Iterates `git -C <repo-dir> for-each-ref` (default `<repo-dir>`: `.`)
  and publishes
  `{"event":"ref-updated","repo":"<repo>","ref":"<ref>","old":"0000...","new":"<sha>"}` to
  `valley.git.<repo>.ref-updated` for each ref.

### `valley self-update`, `--version`, and `help`

`valley self-update` builds, smoke-tests, and installs the tip of a configured branch of the-valley
into the user's default Nix profile, while `--version` and `help` report the CLI stamp and usage.

```sh
valley self-update [-v] [--retry | --rollback | --pin | --unpin] [--repo <url>] [--branch <name>]
valley --version
valley help [<verb>]
```

- **`valley self-update` flags:**
  - `--repo <url>`: Source repository URL (`https://`, `ssh://`, `git://`, `file://`, absolute path,
    or `user@host:path` with characters in `[A-Za-z0-9._~/:@-]`), overriding
    `git config valley.selfUpdate.repo`. If neither is set, `self-update` is disabled and exits `0`.
  - `--branch <name>`: Branch to track, overriding `git config valley.selfUpdate.branch` (default
    `main`).
  - `-v`, `--verbose`: Prints status even when the installed `valley` is already at the branch tip.
  - `--retry`: Retries building the branch tip even if that exact commit already failed its build or
    smoke test.
  - `--rollback`: Reinstalls the previous store path recorded in
    `$XDG_STATE_HOME/valley/self-update.json` (running `--rollback` again switches back).
  - `--pin` / `--unpin`: Freezes or unfreezes the installed `valley` in
    `$XDG_STATE_HOME/valley/self-update.json`; while pinned, `valley self-update` exits `0` without
    building or installing.
- **`valley --version`:** Prints `valley <version>-<short-rev>` (with `-dirty` for a dirty build
  tree or `-unknown` when Nix had no commit rev) when built by Nix (`nix/packages.nix`), or
  `valley unstamped (run from a checkout, not built by nix)` when run directly from a checkout.
- **`valley help [<verb>]`** (also `valley -h`, `valley --help`, or `valley` with no arguments):
  Prints a one-screen command summary when called with no `<verb>`, or detailed usage for `<verb>`
  (`pending`, `changes`, `review`, `approve`, `rebase`, `status`, `ready`, `unready`, `withdraw`,
  `knot`, `plan`, `checks`, `tail`, `replay`, or `self-update`). `valley <verb> --help` and
  `valley <verb> -h` print the same per-verb help.

### Helper and host commands

The `valley` package, the NixOS host module, and the web publisher's module build seven Go binaries
that `bin/valley` and `systemd` invoke ([`nix/packages.nix`](../nix/packages.nix)).

```sh
attest key --key ~/.ssh/valley-attest --name operator
valley-worker --repo the-valley --key ~/.ssh/valley-worker --name worker/attestations
```

- **`attest`** ([`attest/main.go`](../attest/main.go)): Composes, signs, stores, and verifies
  `the-valley/attestation/v1` notes and computes canonical digests. Exits `0` on success, `1` on
  verification or runtime failure, `2` on invalid flags.
  - `attest run [--repo <dir>] [--rev <rev>] [--check <name>[=<attr>]]... [--command <name>=<cmd>]... [--environment <name>] --key <path> [--name <name>] [--provenance <file>] [--schema <file>] [--system <system>] [--push <remote>] [--branch <ref>] [--timings] [--change <id>] [--change-version <n>] [--cost <name>=<class>]...`
    — prints a `time` line per check. `--timings` appends each check's timing to the
    [timing log](#the-timing-log); `--change`, `--change-version` and `--cost` supply the fields
    `attest` cannot read itself.
    - Runs every `nix` check first. One nix evaluation names every check's derivation.
    - Builds each derivation in a `nix build --no-link --print-out-paths <drv>^*` of its own. Up to
      one build runs per CPU at a time, and the nix daemon runs them side by side. Checks with the
      same derivation share its one build.
    - The machine's `max-jobs` and `cores` settings apply within each build, not across them. So up
      to one check derivation per CPU builds at once.
    - Decides each `nix` check by its own build, as when checks were built one at a time. A check
      passed when its build succeeded. A check that failed keeps its own build's `stderr` as its
      log. When the build wrote nothing there, such as when `nix` could not start or was killed by a
      signal, the log is the error that ended the build. A build that prints other than one output
      path is an error, since a check attribute must produce exactly one.
    - A check that does not evaluate is an error for that check alone. Every other check is still
      built and reported. When the shared evaluation stops at an error nix cannot catch, such as a
      type error, each check is evaluated on its own.
    - Runs the `command` checks after the `nix` checks, one at a time, in the order given.
    - Prints each check's `check` and `time` lines, then the log of every check that failed, under
      `--- <name> ---` on `stderr`. Publishes nothing unless every check passed, and exits `1`.
  - `attest verify --note <file> --known-keys <file> [--signer <name>]... [--schema <file>] [--repo <dir>] [--rev <rev>]`
  - `attest digest [--repo <dir>] [--rev <rev>]` — prints `valley-tree-v1:<64-hex>`.
  - `attest inputs [--repo <dir>] [--rev <rev>] [--check <name>[=<attr>]]... [--system <system>]` —
    prints `<check> valley-inputs-v1:<64-hex> sha256:<64-hex-drv>` per check without building.
  - `attest render [<file>]` — renders statement JSON (or canonical statement text) on `<file>` or
    stdin to canonical statement text.
  - `attest sign --key <path> [--name <name>]` — signs canonical statement text on stdin into a
    note, or appends a signature line to an existing note on stdin.
  - `attest key --key <path> [--name <name>]` — prints the `<name>+<8-hex-hash>+<base64>` verifier
    key line.
- **`valley-go`** ([`status/main.go`](../status/main.go)): Internal helper binary invoked by
  `bin/valley` for `status`, `ready`, `unready`, `withdraw`, `changes`, `pending`, `rebase`, `knot`,
  `plan`, and `self-update`, plus internal plumbing verbs (`resolve <change>`, `stack <change>`,
  `does [-v] <change-id>...`, `brings [--change <id>] <head>`, `versions <change-id>`,
  `compare --tip <commit> <change-id> <version> <head>`,
  `coverage --head <sha> [--check <name>=<attribute>]...`), and
  `checks [--schema <file>] --instance <dir> --project <dir> <base> <head>`, which prints the Go
  deriver's `valley-checks/v1` document and which `bin/valley` does not run. `work` is the check
  worker, which `valley-worker` runs. `status`, `stack`, `rebase`, `brings`, `changes`, `pending`
  and `plan` also take `--known-signers <file>` and `--integrator-name <name>`.
- **`valley-worker`** ([`status/worker.go`](../status/worker.go)): One pass of the check worker (see
  [The check worker](architecture.md#the-check-worker)). It is `valley-go work`, wrapped with this
  flake's git, cue and `attest` and with `VALLEY_VERIFICATION_SCHEMA` set. It runs the machine's own
  `nix`. On a host, `services.valley.worker` runs it on a timer (see
  [NixOS host options](#nixos-host-options)).
  - `valley-worker --repo <name>... --key <path> [--name <name>] [--state <dir>]` — run in a clone
    of any project the host serves.
    - `--repo <name>`: A project to build evidence for, once per project. At least one is required.
      The worker builds evidence for no other project. To find a project other than its clone's own,
      it reads `policy/valley` on `origin`'s `main` and the instance repository's
      `refs/the-valley/projects`. When that `main` holds `policy/instance` and no `policy/valley`,
      `origin` is itself the instance repository. It can also fetch `main` of the owning valley a
      project's `policy/valley` names.
    - `--key <path>` and `--name <name>`: The worker's signing key and signing name, passed to
      `attest run`. The name defaults as `attest`'s does.
    - `--state <dir>`: The directory holding the lock and the record of failed batches and pushes
      (default `${XDG_STATE_HOME:-$HOME/.local/state}/valley/worker`).
  - Prints lines of the form `<project>: <handle> v<n> at <short-head>: <what>` for the ready
    changes in a project whose policy at `main` loads. A change it builds or publishes gets one line
    for each step, with what `attest run` prints between them. `<what>` is usually one of these.
    Errors print their own messages.
    - `requires no nix check`
    - `origin holds evidence for every nix check it requires`
    - `building <check>, <check>`, then what `attest run` prints, then `published <ref>`
    - `origin already holds <ref>, this worker's evidence for its tree, and it does not cover <checks>. …`
    - `an earlier pass stored <ref>, and origin does not hold it`, then the line for publishing it
    - `its batch <checks> failed <n> times in a row, so it is not built again before <time>`
    - `the batch did not pass (<reason>); it is not built again before <time>`
    - `publishing <ref> failed <n> times in a row, so it is not pushed again before <time>`
    - `could not publish <ref>; it is not pushed again before <time>`
  - A project with ready changes gets one line for the whole project instead when `origin` has no
    `main`, or when the policy at `main` does not load. Its changes are skipped. The line is
    `<project>: origin has no main to read the policy at, so nothing is built`, or
    `<project>: the policy at main did not read, so nothing is built: <reason>`.
  - A pass that finds the lock held prints
    `valley: another pass holds <state>/lock; waiting for it to end` on `stderr`, and waits.
  - Exits `0` when everything it tried succeeded, and `2` when `VALLEY_NOW` is not a time. It exits
    `1` for invalid flags, when a read of `origin` failed, which stops the pass, and when anything
    else went wrong. A `failures.json` that cannot be read or does not parse is the exception: the
    pass prints `valley: <state>/failures.json does not read, so everything it held is tried again`
    on `stderr`, starts the record empty, and goes on.
- **`valleyhook`** ([`valleyhook/main.go`](../valleyhook/main.go),
  [`valleyhook/shell.go`](../valleyhook/shell.go)):
  - `valleyhook pre-receive --project <name> --policy <json-file> [--grants <file>]... [--known-signers <file>]... [--principal <name>] [--then <hook-file>]`
    — validates ref updates on stdin.
  - `valleyhook shell --converged <file> --expect <id> [--hold <file>] [--authorized-keys <file>]... -- [-c <command>]`
    — login shell for the `git` user on a valley host.
- **`valley-integrator`** (binary `integrator`; [`integrator/main.go`](../integrator/main.go)):
  - `integrator reconcile [flags]` and `integrator watch [--interval <duration>] [flags]` — requires
    `--key`, `--known-signers`, `--schema` (default `$VALLEY_VERIFICATION_SCHEMA`),
    `--attest-schema` (default `$VALLEY_ATTEST_SCHEMA`), and either `--instance-repo` or
    `--instance` (optional flags: `--repo`, `--project`, `--name`, `--signer-projects`,
    `--instance-ref`, `--instance-policy`, `--project-policy`, `--event-schema`, `--bus`,
    `--publish-queue`, `--system`, `--interval`, `--now`).
  - `--signer-projects <file>` restricts signers to projects. Each line names a signer, then the
    projects whose evidence it may sign, separated by spaces, as in
    `worker/attestations the-valley app`. A signer the file names counts only in the projects on its
    line. The project is the one the controller serves (`--project`, by default the repository's
    name). In any other project the controller reports that signer's evidence as
    `signed by no signer the integrator holds`. A line with a name and no project lets that signer
    count nowhere. A signer the file does not name counts wherever `--known-signers` lets it. Blank
    lines and lines that open with `#` are skipped. Every name and project is printable ASCII with
    no space. The controller reads the file once, at start. A file that cannot be read, names a
    signer twice, holds a word that is not printable ASCII, names a signer no verifier key can
    carry, or names the controller's own `--name` stops the controller before it judges anything.
    The controller's own signature never makes a check's attestation count. It countersigns the
    attestations a landing carried, and that vouches for the landing, not for the check. Another
    controller's signature counts like any signer's, so a host whose `--known-signers` holds another
    controller's key lists that controller's name in this file with no project.
  - `integrator publish-signers --instance-repo <dir> --known-signers <file> [--name <name>] [--signer-projects <file>]`
    — writes the signers publication, `refs/the-valley/signers`, into the instance repository: the
    known-signers file and the signing name (default `<hostname>/attestations`, as for `watch`).
    Given `--signer-projects`, the publication holds that file too, unless it is empty. Refuses a
    file that is not verifier keys, a name no verifier key can carry, or a signer projects file the
    controllers would refuse, such as one that names `--name`, and writes nothing when the
    publication would not change. Publishes a file with no key in it. Holds a lock in the instance
    repository from reading the file to moving the ref, so runs at once take turns.
- **`valley-identity`** (binary `identity`; [`identity/main.go`](../identity/main.go)):
  - `identity compile --repo <dir> [--ref <ref>] [--dir <dir>] [--schema <file>] --known-signers <file> --authorized-keys <file> --grants <file> [--approval-signers <file>] [--approval-signers-hardware <file>] [--declared-keys <file>] [--now <YYYY-MM-DD>]`
    — compiles `identity/*.cue` from an instance repository tip into runtime files.
    `--approval-signers` and `--approval-signers-hardware` each write one of the
    [approval signers files](#approval-signers-files), and neither is written without its flag.
- **`valley-restacker`** ([`restack/main.go`](../restack/main.go)):
  - `valley-restacker once --report-only --repo <url> --state <dir> --principal <name> --schema <file> [--target main] [--known-signers <file>] [--integrator-name <name>] [--signer-projects <file>] [--attest <path>] [--instance-repo <url> | --instance <dir>] [--instance-ref <ref>] [--instance-policy <dir>] [--project-policy <dir>] [--json]`
    — read-only planner (`implemented`); the write pass and host service are `not built`. While the
    `--signer-projects` file names the `--integrator-name`, no landing is proved by a transfer
    statement. A file that does not read stops the pass.
- **`valley-web-publish`**
  ([`webui/cmd/valley-web-publish/main.go`](../webui/cmd/valley-web-publish/main.go)): One pass of
  the web publisher (see [Web export publisher](#web-export-publisher)). It is wrapped with this
  flake's `git`, `cue`, and `valley`, and with `VALLEY_WEB_SCHEMA` set to `schema/web.cue`. On a
  host, `services.valley.webPublisher` runs it on a timer (see
  [The web publisher's options](#the-web-publishers-options)).
  - `valley-web-publish --instance-repo <url> --instance <name> --destination <name> (--receiver <url> | --out <dir>) [--principals <file>] [--state <dir>] [--schema <file>] [--recheck <duration>] [--max-bundles <size>] [--max-storage <size>]`
    - `--instance-repo <url>`: An absolute local path to the instance repository. It is the `origin`
      of the publisher's own bare repository, and the CLI discovers projects and signers through it.
      Its `main` must hold `policy/instance` and no `policy/valley`. `<url>` must hold no `#`, no
      control character, and no space at either end, because `policy/valley` cannot name such a URL.
    - `--instance <name>`: The instance the manifest names. The export key signs under
      `<name>/web-export`.
    - `--destination <name>`: The receiver the manifest is for.
    - `--receiver <url>`: The receiver's base URL, `http` or `https`, with no user, query, or
      fragment. The upload's paths are appended to it.
    - `--out <dir>`: Write the export to `<dir>` instead of uploading it. `<dir>` must not exist or
      must be empty. Exactly one of `--receiver` and `--out` is given.
    - `--principals <file>`: The display mapping, a JSON object from each principal's name to the
      name an export shows. Without it, every principal is shown as `contributor`.
    - `--state <dir>`: The state directory (default
      `${XDG_STATE_HOME:-$HOME/.local/state}/valley/web-publish`).
    - `--schema <file>`: The web visibility schema (default `$VALLEY_WEB_SCHEMA`).
    - `--recheck <duration>`: How long an acknowledgement from the receiver is trusted (default
      `1h`).
    - `--max-bundles <size>`: The most the bundles of one export may hold together (default `1G`).
    - `--max-storage <size>`: The most the state directory and the caches may hold (default `16G`).
    - A `<size>` is a number of bytes, or a number followed by `K`, `M`, `G`, or `T` for KiB, MiB,
      GiB, or TiB.
  - Prints `valley-web-publish: public: <projects>; private: <projects>` on every pass, then one
    line for what it did. Warnings go to `stderr`, and so does what the CLI prints there.
  - Exits `0` when the pass uploaded the export, found nothing to send, or wrote `--out`. It exits
    `1` when the pass failed, and `2` for invalid flags. A pass that fails sends and writes nothing.

---

## Environment variables and configuration

`valley` and its helpers read three `valley.*` git configuration keys, environment variables for
signing and schema paths, and local state and cache files.

### Git configuration

Configure your principal name in each project clone, and optionally configure `self-update` in your
global git config.

```sh
git config valley.principal operator
git config --global valley.selfUpdate.repo git@valley.example:the-valley
git config --global valley.selfUpdate.branch main
```

| Git config key             | Scope               | Read by                                                           | Meaning                                                                                                                                                                                                                             |
| :------------------------- | :------------------ | :---------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `valley.principal`         | repo or global      | `ready`, `unready`, `withdraw`, `rebase`, `knot` (writes), `plan` | The principal name your SSH key authenticates as on the host. Required for writing change records (`owner` / `uploader`) and knots (`created_by`, `author`, commit `Committer`), and used as the default operator in `valley plan`. |
| `valley.selfUpdate.repo`   | global (`--global`) | `self-update`                                                     | Default repository URL for `valley self-update` when `--repo` is not passed.                                                                                                                                                        |
| `valley.selfUpdate.branch` | global (`--global`) | `self-update`                                                     | Default branch name for `valley self-update` when `--branch` is not passed (defaults to `main`).                                                                                                                                    |
| `pager.diff`, `core.pager` | repo or global      | `review`                                                          | Diff renderer used as `less`'s input preprocessor when `valley review` pages a diff (`git var GIT_PAGER` fallback).                                                                                                                 |

### Environment variables

The client verifies merged landings with the signers the host publishes, so it needs no setting for
them. Set `VALLEY_KNOWN_SIGNERS` and `VALLEY_INTEGRATOR_SIGNER` only to use other values. Set
`VALLEY_ATTEST_KEY` (and `VALLEY_ATTEST_NAME`) to sign check evidence during approval.

```sh
export VALLEY_KNOWN_SIGNERS="$HOME/.config/valley/known-signers"
export VALLEY_INTEGRATOR_SIGNER=integrator
export VALLEY_ATTEST_KEY="$HOME/.ssh/valley-attest"
export VALLEY_ATTEST_NAME=operator
```

| Environment variable         | Read by                                                         | Meaning                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| :--------------------------- | :-------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `VALLEY_KNOWN_SIGNERS`       | `status`, `changes`, `pending`, `rebase`, `plan`, `approve`     | Path to the `known-signers` file of verifier keys used to verify the integrator's transfer statement on merged (`landed-recorded`) landings. `--known-signers` takes precedence over it, and it takes precedence over the `known-signers` file the host publishes at `refs/the-valley/signers`. With neither of the three, merged landings cannot be verified and report as `uncertain`. `approve` and `[a]pprove` reuse evidence from `origin` only when its note verifies under the file the host publishes, and also under this file when it is set: here it can narrow what is reused, never widen it. |
| `VALLEY_INTEGRATOR_SIGNER`   | `status`, `changes`, `pending`, `rebase`, `plan`                | The signing name (`<signer-name>`) the host's integrator signs transfer statements under. `--integrator-name` takes precedence over it, and it takes precedence over the `integrator-name` the host publishes. A file that cannot be read, does not read as verifier keys, or holds no key under the name is reported on `stderr`, and no statement is verified.                                                                                                                                                                                                                                           |
| `VALLEY_ATTEST_KEY`          | `approve`, `review` (`[a]pprove`)                               | Path to the OpenSSH `ssh-ed25519` private key passed to `attest run --key` whenever a required check has to run, because `origin` holds no evidence `approve` can reuse for it.                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `VALLEY_ATTEST_NAME`         | `approve`, `review` (`[a]pprove`)                               | Signer name passed to `attest run --name` (when unset, `attest` defaults to `<hostname>/attestations`, or `localhost/attestations` if `os.Hostname` fails).                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `VALLEY_VERIFICATION_SCHEMA` | `checks`, `approve`, `review`, `integrator`, `valley-restacker` | Path to `schema/verification.cue`. Set automatically by the Nix wrapper, `valley-worker`'s included; falls back to `<repo>/schema/verification.cue` when running `bin/valley` from a checkout.                                                                                                                                                                                                                                                                                                                                                                                                             |
| `VALLEY_POLICY_SCHEMA`       | `checks`, `approve`, `review`                                   | Optional override for the verification policy schema path in `bin/valley` (takes precedence over `VALLEY_VERIFICATION_SCHEMA` when `--schema` is not passed).                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `VALLEY_ATTEST_SCHEMA`       | `attest`, `integrator`                                          | Path to `schema/attestation.cue`, set by the Nix wrapper.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `VALLEY_EVENT_SCHEMA`        | `integrator`                                                    | Path to `schema/events.cue`, set by the Nix wrapper.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `VALLEY_IDENTITY_SCHEMA`     | `identity`                                                      | Path to `schema/identity.cue`, set by the Nix wrapper.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `VALLEY_COMMAND`             | `pending --checks`                                              | Executable invoked by `valley-go pending --checks` to run `<valley> checks <base> <head>` (`bin/valley` sets `VALLEY_COMMAND` to its own path; defaults to `"valley"`).                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `VALLEY_NOW`                 | `knot`, `plan`, `valley-worker`                                 | Optional UTC timestamp in `RFC 3339` format (`YYYY-MM-DDTHH:MM:SSZ`) overriding the system clock for knot `created` dates, comment stamps, comment edit times, claim timestamps, claim age calculations, and the check worker's waits after a failed batch. Invalid values exit `2`.                                                                                                                                                                                                                                                                                                                       |
| `VALLEY_VERSION`             | `bin/valley`, `self-update`                                     | Package stamp (`<version>-<rev>`) baked into the Nix-wrapped `valley` script, captured into `VALLEY_STAMP` and unset on startup so child processes do not inherit it, and passed explicitly to `valley-go self-update`.                                                                                                                                                                                                                                                                                                                                                                                    |
| `VALLEY_PRINCIPAL`           | `valleyhook shell`, host `pre-receive` wrapper                  | Host-side environment variable set by `valleyhook shell` after looking up the key named in `SSH_USER_AUTH` against the host's `authorized_keys` files. Any client-supplied `VALLEY_PRINCIPAL` or `GIT_*` environment variable is stripped by `valleyhook shell` before `git-shell` runs.                                                                                                                                                                                                                                                                                                                   |
| `SSH_USER_AUTH`              | `valleyhook shell`                                              | Path to the file written by OpenSSH `sshd` (`ExposeAuthInfo yes`) containing the public key that authenticated the SSH session.                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `XDG_STATE_HOME`             | `review`, `approve` (via `attest run --timings`), `self-update` | Base directory for persistent client state, and for the check worker's (defaults to `~/.local/state`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `XDG_CACHE_HOME`             | `pending`, `changes`, `plan`, `knot`, `valley-worker`           | Base directory for cached cross-project clones (defaults to `~/.cache`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `EDITOR`                     | `review` (via `less`)                                           | Editor invoked when pressing `v` while paging a diff in `valley review`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `GIT_PAGER`, `PAGER`, `LESS` | `review`                                                        | Pager and diff-renderer environment variables read by `valley review` (`LESS` defaults to `FRX` when unset).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `NO_COLOR`                   | `pending`, `plan`                                               | When set to a non-empty value, disables ANSI bold and color sequences on terminal output.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `NATS_URL`                   | `tail`, `replay` (via `nats`)                                   | NATS server URL used by the `nats` CLI when streaming or replaying bus events.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |

### Client state and cache files

The client stores review notes, check timings and `self-update` state under `$XDG_STATE_HOME`,
cached multi-repository clones under `$XDG_CACHE_HOME`, and tab-completion handles inside the local
git directory:

- `${XDG_STATE_HOME:-$HOME/.local/state}/valley/notes/<branch>.md`: Markdown review notes captured
  by `v` in `valley review`.
- `${XDG_STATE_HOME:-$HOME/.local/state}/valley/timings.jsonl`: How long each check took when
  `valley approve` ran it, one JSON line per check (see [The timing log](#the-timing-log)).
- `${XDG_STATE_HOME:-$HOME/.local/state}/valley/self-update.json` and `self-update.lock`: Installed
  store paths, commits, pin flag, last failure record, and lock file for `valley self-update`.
- `${XDG_STATE_HOME:-$HOME/.local/state}/valley/worker/lock` and `failures.json`: The check worker's
  lock, and its record of failed batches and pushes (`--state` names another directory). The record
  holds `projects`, by project name, each a map from `build <tree digest> <check>,<check>` or
  `push <object ID>` to `failures`, how many times in a row it failed, and `retry`, the time before
  which it is not tried again.
- `${XDG_CACHE_HOME:-$HOME/.cache}/valley/repos/<16-hex-url-hash>-<project>`: Cached non-bare git
  repositories (`git init --quiet` with no working tree checkout) fetched when `--all`, `--repo`, or
  `--project` reads another project on the host. A clone is made in a temporary directory beside it
  and renamed into place, so a making that is interrupted leaves no clone. A clone whose
  configuration names no origin is set aside and made again. The check worker keeps the attestation
  refs it stores in the clone it reads a project through.
- `${TMPDIR:-/tmp}/valley-known-signers-*`: A copy of the `known-signers` file the host publishes,
  for `attest verify` to read. It exists only while a command that verifies a transfer statement
  runs.
- `<git-common-dir>/valley/handles`: Tab-separated list (`<handle>\t<state>\t<branch>\t<title>`) of
  unlanded `ready` and `draft` changes in the current repository, written by `valley pending` and
  `valley changes` and read by [`completions/_valley`](../completions/_valley).

### The timing log

The timing log records how long each check took when `valley approve` ran it. It is a local
measurement for the operator, and it is not evidence: nothing signs it, stores it at a ref, or
pushes it, and nothing reads it to decide anything (`implemented`).

```text
$ tail -n 1 ~/.local/state/valley/timings.jsonl
{"time":"2026-10-20T12:01:43.601Z","repository":"ssh://valley.example/the-valley.git","change":"kqxmrvtzkqxmrvtzkqxmrvtzkqxmrvtz","version":2,"head":"4ae651e56c34b79b324aca341508cf35c3aaae8e","check":"prose-format","runner":"nix","cost":"quick","result":"passed","satisfied":"cached","duration_ms":1234,"eval_ms":980}
```

`attest run --timings` appends one line per check to
`${XDG_STATE_HOME:-$HOME/.local/state}/valley/timings.jsonl`, whether the check passed or failed.
The line for a `nix` check is appended once it and every `nix` check before it have ended, and the
line for each `command` check when it finishes. It creates the file and its directory when they are
missing, readable by their owner alone. A log that others can read is made readable by its owner
alone before a line is added. Anything at the path that is not a regular file, such as a FIFO, is
refused. `valley approve` passes `--timings` whenever it runs checks. Without the flag, `attest run`
prints its `time` lines, writes no log, and does not ask the nix store whether an output is already
there.

- `time` (`string`): When the check started, in UTC, to the millisecond. Every `nix` check starts
  when the shared evaluation starts.
- `repository` (`string`): The repository's `origin` remote, reduced to what names it. A URL keeps
  its scheme, host and path, and loses any user name, password, query and fragment. The scp-like
  form `user@host:path` keeps `host:path`. A local path is kept as it is. An `origin` in any other
  form is withheld. In its place, and when there is no `origin`, the field is the directory of the
  main working tree.
- `change` (`string`) and `version` (`integer`): The change ID and the version being approved.
  Omitted for a branch no record is on.
- `head` (`string`): The commit the check ran over.
- `check` (`string`) and `runner` (`string`): The check's name, and its runner, `nix` or `command`.
- `cost` (`string`): The cost class the policy declares for the check, `quick` or `slow`.
- `result` (`string`): `passed`, `failed`, or `error` when the check could not run at all, such as a
  flake check that does not evaluate.
- `satisfied` (`string`): `ran` when the command ran or nix built or fetched the output, `cached`
  when the output was already in the local nix store so nix built nothing, and `unknown` when the
  store could not be asked or did not answer within five seconds. `attest run` runs every check it
  is given, so it never reuses evidence that already exists.
- `duration_ms` (`integer`): The check's wall time in milliseconds. For a `command` check, it is the
  time the command ran. For a `nix` check, it is the check's evaluation (`eval_ms`), the store
  probe, and the time its own build ran. A `nix` check that does not evaluate ends at its evaluation
  error.
  - Summing `duration_ms` over one run's checks overcounts the run's wall time. Every `nix` check
    counts the one shared evaluation, and the `nix` checks build at the same time.
- `eval_ms` (`integer`): How long evaluating a `nix` check took, in milliseconds. It counts the
  shared evaluation of all the `nix` checks, plus the time spent on this check alone, such as
  digesting its inputs or evaluating it on its own. Omitted for a `command` check.

A log that cannot be written produces one warning on standard error, and the run continues. Each
`time` line, warning and log line has one second to be written. One that fails or takes longer is
dropped, and the run continues without it.

### NixOS host options

On a NixOS valley host, machine paths, service toggles, static keys, and static grants are
configured under `services.valley` (`deployment choice`;
[`nix/valley-host.nix`](../nix/valley-host.nix)).

```nix
services.valley = {
  enable = true;
  config = ./valley.cue;
  dataDir = "/srv/git";
  authorizedKeys = [
    { principal = "operator"; key = "ssh-ed25519 AAAA... operator"; }
  ];
};
```

| NixOS option (`services.valley.*`)                                                           | Default                                            | Meaning                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| :------------------------------------------------------------------------------------------- | :------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enable`                                                                                     | `false`                                            | Enables the valley git host (`valleyhook shell`, `valley-init` service, and repository hooks).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `user`, `group`, `dataDir`                                                                   | `"git"`, `"git"`, `"/srv/git"`                     | System user and group owning the bare `<project>.git` repositories, and the directory holding `<project>.git`, `.valley-converged`, and the optional `.valley-hold` push-hold sentinel.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| `config`                                                                                     | (required)                                         | Path to the host's `valley.cue` declaration (`package valley`), validated against `schema/valley.cue` at build time.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `authorizedKeys`                                                                             | `[]` (must be non-empty when `enable = true`)      | Static SSH public keys written to `/etc/ssh/authorized_keys.d/<user>`: plain strings authenticate as anonymous pushers (`VALLEY_PRINCIPAL=""`), and `{ key, principal }` submodules authenticate as `<principal>`. No key may appear twice, and `users.users.<user>.openssh.authorizedKeys.keyFiles` must be empty.                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `extraPreReceive`                                                                            | `{}`                                               | Attribute set mapping project name to a Nix store-path script run by `valleyhook pre-receive --then` after the push policy accepts a push.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `grants.approve`, `grants.request`, `grants.restack`                                         | `[]`, `[]`, `[]`                                   | Static principal lists holding the `approve` (approval) and `restack` boundary grants in `valleyhook`. `grants.request` declares the `approve` grant under the name it is also accepted as: its holders are the principals named in either list, and a principal named in both holds the grant once.                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `bus.enable`, `bus.listen`, `bus.storeDir`                                                   | `false`, `"127.0.0.1:4222"`, `"${dataDir}/.bus"`   | Optional local NATS JetStream server for `valley.git.<repo>.*` events.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `integrator.enable`, `integrator.user`                                                       | `false`, `"valley-integrator"`                     | Spawns `valley-integrator@<project>` for every git-enabled project in `valley.cue` that declares a `protection` block (requires at least one protected project; `integrator.user` must differ from `services.valley.user`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| `integrator.signingKeyFile`, `integrator.signingName`                                        | `null` (required when enabled), `null`             | Runtime path to the integrator's Ed25519 private key and optional signer name (when `null`, `integrator` defaults to `<hostname>/attestations`). The effective name is published at `refs/the-valley/signers`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `integrator.knownSignersFile`                                                                | `null`                                             | Runtime path to the `known-signers` file of trusted verifier keys (used by both `valleyhook` and `valley-integrator`). Required when `integrator.enable = true` and `identity.enable = false`; must be `null` whenever `identity.enable = true`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `integrator.signerProjects`                                                                  | `{}`                                               | Signer names mapped to the projects their evidence counts in, as in `{ "worker/attestations" = [ "the-valley" ]; }`. Rendered into the controllers' `--signer-projects` and published. An empty list counts nowhere. Names and projects are printable ASCII with no space; the integrator's own name is refused.                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `integrator.instanceProject`, `integrator.instancePolicy`                                    | `null`, `null`                                     | Exactly one must be set when `integrator.enable = true`: `instanceProject` names the hosted repository carrying `policy/instance/` (whose `refs/heads/main` must be protected) and causes `valley-init` to write `refs/the-valley/projects` and `valley-signers` to write `refs/the-valley/signers` there; `instancePolicy` names a local directory of floor `*.cue` files.                                                                                                                                                                                                                                                                                                                                                                      |
| `integrator.interval`                                                                        | `"15s"`                                            | Poll interval between `integrator watch` reconciliation passes.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `identity.enable`, `identity.ref`, `identity.directory`, `identity.interval`                 | `false`, `"refs/heads/main"`, `"identity"`, `"5m"` | Compiles `<directory>/*.cue` from `integrator.instanceProject` at `<ref>` (which must be covered by `protection`) into `/var/lib/valley-identity/{known-signers,authorized_keys.<user>,grants}` on a timer (`knownSignersFile` must be `null`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `buildSlice.enable`, `buildSlice.cpuQuota`, `buildSlice.memoryMax`                           | `false`, `"400%"`, `"8G"`                          | Places `nix-daemon.service` in `valley-builds.slice` with `CPUQuota`, `MemoryMax`, and `MemorySwapMax=0`, capping the total CPU and memory of all Nix daemon builds across all users on the host. When the slice hits `MemoryMax`, the kernel OOM killer kills whichever process in the slice uses the most memory, even if another build pushed the slice over the cap. Required when `worker.enable = true` (see [The build slice](architecture.md#the-build-slice)).                                                                                                                                                                                                                                                                          |
| `worker.enable`, `worker.projects`, `worker.signerName`                                      | `false`, `[]`, (required when enabled)             | Runs `valley-worker` on a timer over `projects` as the `valley-worker` user, signing attestations as `signerName`. Requires `buildSlice.enable = true`, `integrator.enable = true`, `integrator.instanceProject != null`, non-empty `projects` served by this host, `signerName` different from the integrator's signing name, and `integrator.signerProjects.<signerName>` set to the exact `projects` list. Runs in `valley-builds.slice` pinned to CPU 0 (`CPUAffinity=0`) with `max-jobs = 1` and `cores = 4`. Connects to the host's repositories over `localhost` SSH as `user` (`git` by default); its generated key must be enrolled before it can fetch or push (see [The worker on the host](architecture.md#the-worker-on-the-host)). |
| `worker.interval`, `worker.stateDir`                                                         | `"1m"`, `"/var/lib/valley-worker"`                 | Delay (`OnUnitInactiveSec`) between the end of one `valley-worker.service` pass and the start of the next, and the `0700` state directory owned by `valley-worker`. Holds the worker's Ed25519 private key (`key`, mode `0400`, generated on first start), its public key (`key.pub`) and `known-signers` line (`verifier`), its `known_hosts`, the scratch repository (`origin/`), cached project clones (`cache/`), and `lock` and `failures.json`.                                                                                                                                                                                                                                                                                            |
| `backup.repositoryFile`, `backup.passwordFile`, `backup.sshKeyFile`, `backup.knownHostsFile` | `null`                                             | Runtime secret paths required whenever `valley.cue` enables `backup` (`backup.enable` defaults to `true` when `backup` is declared).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |

#### The web publisher's options

The web publisher's module, `nixosModules.webui-publisher`
([`nix/webui-publisher.nix`](../nix/webui-publisher.nix)), adds `services.valley.webPublisher` to a
host that imports the host module (`deployment choice`).

```nix
imports = [ the-valley.nixosModules.default the-valley.nixosModules.webui-publisher ];
services.valley.webPublisher = {
  enable = true;
  destination = "example.org";
  receiver = "https://example.org";
  principals = { operator = "operator"; alice = "agent"; };
};
```

| NixOS option (`services.valley.webPublisher.*`) | Default                         | Meaning                                                                                                                                          |
| :---------------------------------------------- | :------------------------------ | :----------------------------------------------------------------------------------------------------------------------------------------------- |
| `enable`                                        | `false`                         | Runs `valley-web-publish` on a timer. Needs `services.valley.integrator.instanceProject`, the instance repository the publisher reads.           |
| `destination`                                   | (required)                      | The receiver's name, which every manifest is signed for.                                                                                         |
| `receiver`                                      | (required)                      | The receiver's base URL, `http://` or `https://`.                                                                                                |
| `principals`                                    | `{}`                            | The display mapping, written to a JSON file for `--principals`. A principal it does not name is shown as `contributor`.                          |
| `stateDir`                                      | `"/var/lib/valley-web-publish"` | The state directory, under `/var/lib`. The cache is the directory of the same name under `/var/cache`, given to the service as `XDG_CACHE_HOME`. |
| `memoryMax`                                     | `"1G"`                          | The most memory a pass may use, with every program it runs (`MemoryMax`, with `MemorySwapMax=0`). Past it, the kernel ends the pass.             |
| `maxBundles`                                    | `"1G"`                          | Passed as `--max-bundles`: the most the bundles of one export may hold together.                                                                 |
| `maxStorage`                                    | `"16G"`                         | Passed as `--max-storage`: the most the state and cache directories may hold.                                                                    |

- **The pass:** `valley-web-publish.service` is a oneshot. It runs
  `valley-web-publish --instance-repo <dataDir>/<instanceProject>.git --instance <networking.hostName>`
  with the destination, receiver, display mapping, state directory, and size bounds above. No switch
  starts it: nothing wants the service, and a changed unit is not restarted
  (`restartIfChanged = false`). A pass that runs longer than an hour is stopped (`TimeoutStartSec`).
- **The timer:** `valley-web-publish.timer` starts the first pass five minutes after the timer
  starts (`OnActiveSec`). After that it starts a pass five minutes after the last one ended
  (`OnUnitInactiveSec`). Both are delays only. When the switch that starts the timer runs longer
  than five minutes, the first pass can start inside it, and a later pass can start inside any later
  switch.
- **The sandbox:** The service runs as a dynamic user (`DynamicUser`) with the git group
  (`SupplementaryGroups`), so it can read the repositories and owns nothing on disk but its state
  and cache. Its environment carries `safe.directory` entries as `GIT_CONFIG_COUNT`,
  `GIT_CONFIG_KEY_<n>`, and `GIT_CONFIG_VALUE_<n>`. git reads them after the system and global
  configuration, and adds them to what those set. So the first entry is empty, which resets the
  list, and one exact entry follows for each repository the host declaration serves, with no
  wildcard. A system configuration with `safe.directory=*` then trusts no other repository for the
  service. `ProtectSystem=strict` leaves the filesystem read-only except the state and cache
  directories, and the repositories are listed in `ReadOnlyPaths`. `InaccessiblePaths` holds the git
  user's `<dataDir>/.ssh`, `/var/lib/valley-integrator`, the integrator's signing key file, the
  worker's state directory when the worker runs, and the backup's secret files.
  `webui-publisher-e2e` runs the unit in a NixOS machine, with a probe in place of the publisher,
  under a system configuration with `safe.directory=*`. The probe reads a served repository, and
  cannot read an undeclared one, write a repository, or read the git user's ssh key or the backup's
  key.

---

## Project storage ref layout and bounds

Every repository on a valley host separates **the source** (`refs/heads/main` and `refs/tags/*`),
topic branches (`refs/heads/<branch>`), and **project storage** (`refs/the-valley/*`), and
`valleyhook pre-receive` validates every SSH push against fixed structural rules and size limits
([`valleyhook/policy.go`](../valleyhook/policy.go)).

### Push-wide bounds

`valleyhook` enforces four repository-wide and push-wide counters before accepting a push:

```sh
git push --atomic origin topic refs/the-valley/changes/main/nxvpwlkmqtsrzyoukklnpmqrstuvwxyz
```

| Bound                                                | Value                                                          | Enforcement                                                                                                                                                                                                                                                             |
| :--------------------------------------------------- | :------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Ref updates per push (`maxUpdates`)                  | `<= 1,000`                                                     | Refuses the entire push before evaluating any ref.                                                                                                                                                                                                                      |
| Change record updates per push (`MaxRecordUpdates`)  | `<= 32`                                                        | Refuses the push if more than 32 `refs/the-valley/changes/*` refs are updated in one push.                                                                                                                                                                              |
| Change records per repository (`MaxRecords`)         | `<= 2,000` (soft push cap)<br>`<= 4,000` (hard integrator cap) | `valleyhook` refuses creating a new change record when `existingRecords + recordCreates > 2,000` (concurrent pushes can briefly exceed it). The integrator refuses to reconcile any pass if a repository holds more than `4,000` change records (`recordLimit = 4000`). |
| Restack report updates per push (`MaxReportUpdates`) | `<= 64`                                                        | Refuses the push if more than 64 `refs/the-valley/restack/*` refs are updated in one push.                                                                                                                                                                              |

### Ref layout, permissions, and object bounds

The table below lists every ref namespace on a valley host, who may write it over SSH vs. locally on
the host, the required git object type, and the size bounds enforced by `valleyhook`. After checking
a ref's namespace rule below, `valleyhook` also checks `#Protection.refs` (`deployment choice`): if
any protected pattern matches a ref in **any** namespace (`refs/heads/*`, `refs/tags/*`, or
`refs/the-valley/*`), the pushing principal must also be listed in `#Protection.writers`.

| Ref pattern                                                                                               | Purpose                                                                  | Who may write                                                                                                                                                                                                                                                                                                                                                                                                                      | Object type and layout                                                                                                                                                           | Size and structure bounds                                                                                                                                                                                                                                                                                                          |
| :-------------------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refs/heads/main` (or any ref matching `#Protection.refs`)                                                | Protected integration branch (`the source`)                              | **Local host write:** `valley-integrator` via `git update-ref` (`implemented`).<br>**SSH push:** Only principals listed in `#Protection.writers` (`deployment choice`, default `[]` when `protection` is declared; if `protection` is omitted in `valley.cue`, all branches are open).                                                                                                                                             | Commit object                                                                                                                                                                    | Standard git commit; `refs/heads/main` is replicated to `mirrors` by `valley-publish`.                                                                                                                                                                                                                                             |
| `refs/heads/<branch>` (unprotected)                                                                       | Topic branches                                                           | Any SSH key in `authorized_keys` (named principal or anonymous pusher).                                                                                                                                                                                                                                                                                                                                                            | Commit object                                                                                                                                                                    | `<branch>` must pass `git check-ref-format`; not replicated to `mirrors`.                                                                                                                                                                                                                                                          |
| `refs/tags/<tag>` (and any ref outside `refs/heads/`, `refs/replace/`, `refs/notes/`, `refs/the-valley/`) | Release tags (`the source`) and custom refs                              | Closed by default (`implemented`); opened over SSH only to principals listed in a matching `#Project.grants.<name>` (`#RefGrant`) in `valley.cue` (`deployment choice`), and also requires `#Protection.writers` if matched by `#Protection.refs`.                                                                                                                                                                                 | Any git object (tag, commit, tree, or blob)                                                                                                                                      | `refs/tags/*` is replicated to `mirrors` by `valley-publish`.                                                                                                                                                                                                                                                                      |
| `refs/replace/*`, `refs/notes/*`                                                                          | Git object replacements and notes                                        | **Nobody.** Categorically refused on all SSH pushes (`implemented`).                                                                                                                                                                                                                                                                                                                                                               | —                                                                                                                                                                                | Always refused.                                                                                                                                                                                                                                                                                                                    |
| Any symbolic ref                                                                                          | Symbolic ref target                                                      | **Nobody.** Categorically refused on all SSH pushes (`implemented`).                                                                                                                                                                                                                                                                                                                                                               | —                                                                                                                                                                                | Always refused.                                                                                                                                                                                                                                                                                                                    |
| `refs/the-valley/changes/main/<change-id>`                                                                | Change record (`valley-change-record/v1`)                                | **Create:** Any named principal (becomes `owner`).<br>**Add version / `unready` / `re-ready` / `withdraw`:** Change `owner` or an `approve` grant holder.<br>**Add restacked version (`ready` → `ready`, same `branch` & `title`):** `restack` grant holder.<br>**Delete:** `approve` grant holder only.<br>(`valleyhook` accepts target `main` only; `<change-id>` is `[k-z]{32}`).                                               | **Commit** whose root **tree** contains a single regular file (`100644`) named `record.json`.                                                                                    | Commit `<= 4 KiB` (`4,096` bytes);<br>Tree `<= 1 KiB` (`1,024` bytes, 1 entry `100644 record.json`);<br>Blob `record.json` `<= 64 KiB` (`65,536` bytes);<br>`<= 1,000` versions, `seq <= 100,000`.                                                                                                                                 |
| `refs/the-valley/restack/main/<change-id>`                                                                | Restack report (`valley-restack-report/v1`)                              | **Create / extend (`P = []` or `P = [old]`):** Principals holding the `restack` grant, only for a `<change-id>` that already has a change record on `main`.<br>**Delete or history rewrite:** Refused.                                                                                                                                                                                                                             | **Commit** whose root **tree** contains a single regular file (`100644`) named `report.json`.                                                                                    | Commit `<= 4 KiB` (`4,096` bytes);<br>Tree `<= 1 KiB` (`1,024` bytes, 1 entry `100644 report.json`);<br>Blob `report.json` `<= 64 KiB` (`65,536` bytes);<br>`<= 64` layers, `<= 100` conflict paths, `<= 64` blocking requests.                                                                                                    |
| `refs/the-valley/reviews/<target>/<change-id>`                                                            | Review history (`the-valley/review/v1`)                                  | **SSH push:** Nobody (`implemented`): the hook has no rule for the namespace and refuses it. Admitting appends is `not built`; see [Review record](#review-record-the-valleyreviewv1).                                                                                                                                                                                                                                             | **Commit** whose root **tree** contains a single regular file (`100644`) named `review.json`.                                                                                    | Commit `<= 4 KiB`;<br>Tree `<= 1 KiB`;<br>Blob `review.json` `<= 64 KiB`;<br>`<= 512` reviews per history.                                                                                                                                                                                                                         |
| `refs/the-valley/attestations/<tree-digest>/<key-hash>`                                                   | Signed check attestations and integrator transfer statements             | **Create over SSH:** Any pusher, provided `--known-signers` is configured and every note in the tree verifies under a known signer with key hash `<key-hash>` (`[0-9a-f]{8}`) for `<tree-digest>` (`[0-9a-f]{64}`).<br>**Update or delete over SSH:** Refused (create-only).<br>**Local host write:** `valley-integrator` writes countersigned `<check>/statement.note` and `transfer.note` under its own `<key-hash>` on landing. | **Tree** object (not a commit) containing `100644` note blobs (such as `<check>/statement.note` and `transfer.note`).                                                            | Tree depth `<= 3` (root tree is depth 1);<br>`<= 64` entries per tree object;<br>`<= 256` total entries visited across walk (`>= 1` note blob required);<br>Each tree object `<= 16 KiB` (`16,384` bytes);<br>Each note blob (`100644`) `<= 64 KiB` (`65,536` bytes);<br>Total bytes read in walk `<= 1 MiB` (`1,048,576` bytes).  |
| `refs/the-valley/integration-requests/<target>/<request>`                                                 | Integration request (approval of a commit)                               | **Create / replace / delete over SSH:** Principals holding the `approve` grant, also accepted as `request` (`valleyhook` checks the `refs/the-valley/integration-requests/` prefix and the `approve` grant).<br>**Local host delete:** `valley-integrator` deletes the ref (CAS on request head) when a change lands or when voiding a `withdrawn` change's request.                                                               | **Commit** object (written by `valley approve` and required by `valley-integrator` at `refs/the-valley/integration-requests/main/<request>`, where `<request>` contains no `/`). | Must resolve to a commit object in the repository for the integrator to consider it.                                                                                                                                                                                                                                               |
| `refs/the-valley/integration-outcomes/<target>/<request>`                                                 | Integrator outcome record                                                | **SSH push:** Nobody (`implemented`).<br>**Local host write:** `valley-integrator` writes after judging a request.                                                                                                                                                                                                                                                                                                                 | **Blob** object (single line of space-separated fields).                                                                                                                         | 4 or 5 whitespace-separated fields terminated by `\n`.                                                                                                                                                                                                                                                                             |
| `refs/the-valley/knots`                                                                                   | Project plan graph (exact ref name; `refs/the-valley/knots/*` is closed) | **Create (`P = []`) / update (`P = [old]`):** Any named principal (`Committer` must match pushing principal, commit message must end with `Valley-Op: <16-hex>`).<br>**Delete or non-fast-forward:** Refused.                                                                                                                                                                                                                      | **Commit** whose **tree** holds `<type-dir>/<id>-<slug>.md` and `comments/<id>/<stamp>-<op>.md` (`100644` blobs).                                                                | Commit `<= 16 KiB` (`16,384` bytes);<br>Changed paths per commit `<= 32`;<br>Tree depth `<= 3`, `<= 20,000` entries/tree, `<= 30,005` total walk entries, each tree `<= 2 MiB`;<br>Knot file `<= 64 KiB`, `<= 5,000` knots;<br>Comment file `<= 16 KiB`, `<= 20,000` comments;<br>Total walk read `<= 64 MiB`, hook deadline `5s`. |
| `refs/the-valley/projects`                                                                                | Hosted project list in the instance repository                           | **SSH push:** Nobody (`implemented`).<br>**Local host write:** `valley-init` writes in `integrator.instanceProject`.                                                                                                                                                                                                                                                                                                               | **Blob** object                                                                                                                                                                  | Sorted enabled git project names, one per line, terminated by `\n`.                                                                                                                                                                                                                                                                |
| `refs/the-valley/signers`                                                                                 | What clients verify transfer statements with, in the instance repository | **SSH push:** Nobody (`implemented`).<br>**Local host write:** `valley-signers` (`integrator publish-signers`) writes in `integrator.instanceProject`, under a lock and with a compare-and-swap. The integrator, in the `git` group that can write the repository, can also write it.                                                                                                                                              | **Tree** object                                                                                                                                                                  | `100644` blobs: `integrator-name`, the signing name followed by `\n`; `known-signers`, the controllers' known-signers file, byte for byte; and, when `integrator.signerProjects` is set, `signer-projects`, their signer projects file, byte for byte.                                                                             |
| Any other `refs/the-valley/*`                                                                             | Reserved for host machinery                                              | **SSH push:** Nobody (`implemented`).                                                                                                                                                                                                                                                                                                                                                                                              | —                                                                                                                                                                                | Always refused on push.                                                                                                                                                                                                                                                                                                            |

---

## Wire formats and JSON schemas

This section specifies the exact wire formats for records in project storage and the JSON documents
emitted by `--json` commands.

### Change record (`valley-change-record/v1`)

A change record tracks a change's owner, target branch, state (`ready`, `draft`, or `withdrawn`),
version history, and active version as `record.json` inside a commit at
`refs/the-valley/changes/main/<change-id>` ([`change/record.go`](../change/record.go),
[`valleyhook/record.go`](../valleyhook/record.go)).

```json
{
  "schema": "valley-change-record/v1",
  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
  "target": "main",
  "owner": "operator",
  "state": "ready",
  "seq": 2,
  "versions": [
    {
      "n": 1,
      "head": "git-sha1:1111111111111111111111111111111111111111",
      "title": "Add greeting helper",
      "branch": "greeting",
      "uploader": "operator"
    },
    {
      "n": 2,
      "head": "git-sha1:2222222222222222222222222222222222222222",
      "title": "Add greeting helper",
      "branch": "greeting",
      "uploader": "operator"
    }
  ],
  "active": 2
}
```

- **Strict JSON rules:** Valid UTF-8, no duplicate keys in any object, no unknown fields, no
  trailing tokens after the root object, and plain digit integers only (`3.0` and `3e0` are
  rejected).
- **Top-level fields:**
  - `schema` (`string`): Must be `"valley-change-record/v1"`.
  - `change_id` (`string`): 32 lowercase letters in `k`–`z` (`[k-z]{32}`), matching `<change-id>` in
    the ref name.
  - `target` (`string`): Valid UTF-8 branch name matching `<target>` in the ref name (`valleyhook`
    admits `"main"` only).
  - `owner` (`string`): Principal name (`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`). Set to the pushing
    principal on creation and never changes on update.
  - `state` (`string`): `"ready"`, `"draft"`, or `"withdrawn"`.
  - `seq` (`int`): Monotonically increasing update counter in `1..100000` (`MaxSeq = 100000`).
    Starts at `1` on creation and increases by exactly `1` on every update.
  - `versions` (`array` of objects): `1..1000` entries (`MaxVersions = 1000`), numbered
    `n: 1, 2, ...` in order with no gaps. Prior versions are immutable once recorded.
  - `active` (`int`): 1-based version number in `1..len(versions)`. Every `valleyhook` transition
    sets `active` to `1` on create, keeps `active` unchanged on state changes, and sets `active` to
    `len(versions)` on a new version, so `active` on a host-validated record always equals
    `len(versions)`.
- **`versions[]` fields:**
  - `n` (`int`): `i + 1` (1-based index).
  - `head` (`string`): `"git-sha1:<40-lowercase-hex>"` naming a commit object in the repository.
  - `title` (`string`): `1..200` bytes (`MaxTitle = 200`) of valid UTF-8 with no Unicode control
    characters.
  - `branch` (`string`): `""` (empty) or a valid UTF-8 git branch name (`ValidBranch`).
  - `uploader` (`string`): Principal name of the pusher that added this version.
- **Record commit parent-history and transition table:**

| Transition      | Authorized principal                                                    | Record commit parents `P`                                                                      | State and field rules                                                                                                                                                                                                                                                          |
| :-------------- | :---------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Create**      | Any named principal (`p`)                                               | `P = [h]` (1 parent: version 1's `head` commit `h`)                                            | `seq = 1`, `active = 1`, `len(versions) = 1`, `versions[0].head = "git-sha1:" + h`, `owner = p`, `versions[0].uploader = p`, `state = "ready"`.                                                                                                                                |
| **New version** | `owner` or `approve` grant holder                                       | `P = [old, h]` (2 parents: replaced record commit `old`, then new version's `head` commit `h`) | Appends exactly 1 version (`n = len(before.versions) + 1`, `head = "git-sha1:" + h != before.activeVersion.head`, `uploader = p`), `active = n`, `seq = before.seq + 1`, `state = "ready"` (from `ready`, `draft`, or `withdrawn`).                                            |
| **Unready**     | `owner` or `approve` grant holder                                       | `P = [old]` (1 parent: replaced record commit `old`)                                           | `before.state = "ready"` → `after.state = "draft"`; `seq = before.seq + 1`; `owner`, `active`, and `versions` unchanged.                                                                                                                                                       |
| **Re-ready**    | `owner` or `approve` grant holder                                       | `P = [old]` (1 parent: replaced record commit `old`)                                           | `before.state` in `{"draft", "withdrawn"}` → `after.state = "ready"`; `seq = before.seq + 1`; `owner`, `active`, and `versions` unchanged. (`withdrawn` → `draft` is refused.)                                                                                                 |
| **Withdraw**    | `owner` or `approve` grant holder                                       | `P = [old]` (1 parent: replaced record commit `old`)                                           | `before.state` in `{"ready", "draft"}` → `after.state = "withdrawn"`; `seq = before.seq + 1`; `owner`, `active`, and `versions` unchanged.                                                                                                                                     |
| **Restack**     | `restack` grant holder (who is neither `owner` nor an `approve` holder) | `P = [old, h]` (2 parents: replaced record commit `old`, then new version's `head` commit `h`) | `before.state = "ready"` → `after.state = "ready"`; appends 1 version (`head = "git-sha1:" + h != before.activeVersion.head`, `uploader = p`) keeping `branch == before.activeVersion.branch` and `title == before.activeVersion.title`; `active = n`, `seq = before.seq + 1`. |
| **Delete**      | `approve` grant holder                                                  | Ref deletion (`new = 0000...`)                                                                 | Deletes `refs/the-valley/changes/main/<change-id>`.                                                                                                                                                                                                                            |

### Restack report (`valley-restack-report/v1`)

A restack report records what an automated restack attempt observed and published for a change as
`report.json` inside a commit at `refs/the-valley/restack/main/<change-id>`
([`change/report.go`](../change/report.go), [`valleyhook/report.go`](../valleyhook/report.go),
[`rebase/report.go`](../rebase/report.go)).

```json
{
  "schema": "valley-restack-report/v1",
  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
  "target": "main",
  "target_tip": "git-sha1:3333333333333333333333333333333333333333",
  "operation": "20261017T203500Z-1",
  "result": "conflict",
  "layers": [
    {
      "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
      "record": "git-sha1:4444444444444444444444444444444444444444",
      "version": 2,
      "head": "git-sha1:2222222222222222222222222222222222222222",
      "branch": "greeting",
      "branch_tip": "git-sha1:2222222222222222222222222222222222222222",
      "published": null
    }
  ],
  "failing_layer": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
  "conflict_paths": ["greeting.txt"],
  "blocking_requests": [],
  "refusal": null
}
```

- **Parent history:** The first report commit at `refs/the-valley/restack/main/<change-id>` has `0`
  parents (`P = []`); every subsequent report commit has the replaced report commit as its single
  parent (`P = [old]`); deletions are refused.
- **Fields and cross-field invariants:**
  - `schema` (`string`): Must be `"valley-restack-report/v1"`.
  - `change_id` (`string`): `[k-z]{32}`, matching the ref's `<change-id>` and appearing in
    `layers[]`.
  - `target` (`string`): Valid UTF-8 branch name matching the ref's `<target>` (`"main"` in
    `valleyhook`).
  - `target_tip` (`string`): `"git-sha1:<40-hex>"`.
  - `operation` (`string`): `1..64` bytes (`MaxOperation = 64`) matching
    `^[a-zA-Z0-9][a-zA-Z0-9._:-]*$`.
  - `result` (`string`): One of `"conflict"`, `"blocked"`, `"partial"`, `"complete"`, or
    `"superseded"`.
  - `layers` (`array` of objects): `1..64` items (`MaxRestackLayers = 64`), unique by `change_id`,
    containing the report's `change_id`. Each item has:
    - `change_id` (`string`): `[k-z]{32}`.
    - `record` (`string`): `"git-sha1:<40-hex>"` (the change record commit observed).
    - `version` (`int`): `1..1000` (the active version number observed).
    - `head` (`string`): `"git-sha1:<40-hex>"` (that version's head).
    - `branch` (`string`): `""` or valid UTF-8 branch name.
    - `branch_tip` (`string` or `null`): `"git-sha1:<40-hex>"` or `null` (must be `null` when
      `branch == ""`).
    - `published` (`object` or `null`): Must be `null` when `branch == ""` or when `result` is
      `"conflict"` or `"blocked"`. When non-null, carries
      `{ "head": "git-sha1:<40-hex>", "version": <int 1..1000 or null>, "record": <"git-sha1:<40-hex>" or null> }`
      where `version` and `record` are either both non-null or both `null`. When
      `result == "complete"`, every layer must have a non-null `published` with non-null `version`
      and `record`.
  - `failing_layer` (`string` or `null`): `null` or a `change_id` present in `layers[]` (required to
    be non-null when `result == "conflict"`).
  - `conflict_paths` (`array` of `string`): `0..100` items (`MaxConflictPaths = 100`), each
    `1..1024` bytes (`MaxConflictPath = 1024`) of valid UTF-8 without control characters.
  - `blocking_requests` (`array` of objects): `0..64` items (`MaxBlockingRequest = 64`), unique by
    `ref`. Each item has `ref` (`refs/the-valley/integration-requests/<target>/<name>`, `<= 256`
    bytes, `MaxRequestRef = 256`) and `tip` (`"git-sha1:<40-hex>"`).
  - `refusal` (`string` or `null`): `null` or `1..2048` bytes (`MaxRefusal = 2048`) of valid UTF-8
    without control characters.
- **Currency rule (`Origin.Current`):** A report is marked `current: true` in CLI outputs only while
  `refs/heads/main` is still at `target_tip`, every `blocking_requests[]` ref is still at `tip`, and
  for every layer, `refs/the-valley/changes/main/<change_id>` is at `published.record` (if non-null,
  else `record`) and `refs/heads/<branch>` (when `branch != ""`) is at `published.head` (if
  `published` is non-null, else `branch_tip`, or still absent when `branch_tip == null`). Otherwise
  `current` is `false` (historical).

### Review record (`the-valley/review/v1`)

A review records one reviewer's judgment of one version of a change as `review.json` inside a commit
at `refs/the-valley/reviews/<target>/<change-id>` ([`review/review.go`](../review/review.go),
[`review/binding.go`](../review/binding.go), [`review/history.go`](../review/history.go)). The
module is `implemented`. Admitting a review over SSH is `not built`: the push hook refuses every
push to `refs/the-valley/reviews/`.

```json
{
  "schema": "the-valley/review/v1",
  "review_id": "0123456789abcdef0123456789abcdef",
  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
  "target": "main",
  "review": "docs-style",
  "reviewer": "alice",
  "verdict": "request-changes",
  "withdraws": null,
  "binding": {
    "record": "git-sha1:4444444444444444444444444444444444444444",
    "version": 2,
    "head": "git-sha1:2222222222222222222222222222222222222222",
    "target_tip": "git-sha1:3333333333333333333333333333333333333333",
    "base": "git-sha1:1111111111111111111111111111111111111111",
    "delta": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
  },
  "summary": "Two sentences carry two new ideas each.",
  "notes": [
    {
      "side": "after",
      "path": "docs/guide.md",
      "blob": "git-sha1:5555555555555555555555555555555555555555",
      "lines": { "start": 12, "end": 14 },
      "body": "Split this sentence."
    }
  ],
  "provenance": {
    "harness": "agent-runner 0.9.1",
    "model": "claude-opus-5-5",
    "settings": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "inputs": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "input_tokens": 1200,
    "output_tokens": null
  }
}
```

- **Strict JSON rules:** The change record's: valid UTF-8, no duplicate keys in any object, no
  unknown fields (a key in another case is unknown), every field present with its exact type, no
  trailing tokens, and plain digit integers only. The whole file is `<= 64 KiB` (`MaxRecord`).
- **Fields:**
  - `schema` (`string`): Must be `"the-valley/review/v1"`.
  - `review_id` (`string`): 32 lowercase hex digits, unique within the history (`review.NewID`).
  - `change_id` (`string`): `[k-z]{32}`, matching the ref's `<change-id>`.
  - `target` (`string`): Valid UTF-8 branch name matching the ref's `<target>`.
  - `review` (`string`): The review's name, `^[a-z][a-z0-9-]*$`, `1..64` bytes.
  - `reviewer` (`string`): Principal name (`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`).
  - `verdict` (`string`): `"approve"`, `"request-changes"`, or `"withdraw"`.
  - `withdraws` (`string` or `null`): The `review_id` a withdrawal takes back; `null` for any other
    verdict. A review never withdraws itself.
  - `binding` (`object` or `null`): Required for `approve` and `request-changes`; `null` for
    `withdraw`. Its fields are `record`, `head`, `target_tip` and `base`, each
    `"git-sha1:<40-hex>"`; `version` (`int`, `1..1000`); and `delta`, 64 lowercase hex digits.
  - `summary` (`string`): `1..8192` bytes of UTF-8 with no control characters but newline and tab.
  - `notes` (`array` of objects): `0..256` items; always empty for `withdraw`. Each has `side`
    (`"before"` or `"after"`), `path` (`1..4095` bytes of UTF-8, no empty, `.`, `..` or `.git`
    component, no component over 255 bytes, no control character), `blob` (`"git-sha1:<40-hex>"`),
    `lines` (`null` or `{ "start": <int>, "end": <int> }` with `1 <= start <= end <= 16777216`), and
    `body` (`1..8192` bytes, as `summary`).
  - `provenance` (`object` or `null`): `null` for a human's review. Otherwise `harness` and `model`
    (`1..256` bytes, no control characters), `settings` and `inputs` (64 lowercase hex digits, the
    SHA-256 of the generation settings and of the input manifest), and `input_tokens` and
    `output_tokens` (`null` or an `int` in `0..1000000000000`).
- **Binding:** `record` is the change record commit the reviewer read, and it names `version` with
  `head`. `target_tip` is the commit the version was judged against. `base` is the one merge base of
  `target_tip` and `head`. `delta` is the SHA-256 of the `valley-delta-v1` encoding from `base` to
  `head`. When `target_tip` and `head` have no merge base, or more than one, no binding exists and
  no review is made (`review.ErrNoBase`, `review.ErrAmbiguousBase`).
- **Verification (`review.Verify`):** Recomputes every part of the binding: the record names the
  version and head, and is the record of its own `seq` on the change's record ref when that ref is
  given; `target_tip` is a commit, on the target's history when the target's tip is given; `base` is
  the one merge base; the delta has the bound digest; and each note's `path` holds its `blob` on its
  side (`base` for `before`, `head` for `after`), with `lines` inside the file. It reads within a
  `delta.Budget` of bytes, tree entries and a deadline. An error wrapping `delta.ErrBudget` means
  the budget ran out, not that the binding is wrong. That includes a note's file larger than the
  budget's bytes, and a deadline that passes after the last read from git.
- **History and parents:** The ref points at the newest review commit. The first review commit's
  parents are `[record]`; a later review that judges a version has `[previous, record]`; a
  withdrawal has `[previous]`. `previous` is the history's head when the review was appended, and
  `record` is `binding.record`. A history holds at most 512 reviews (`MaxHistory`). A withdrawal
  names an earlier review of the same history and review name that is neither a withdrawal nor
  withdrawn already.
- **Appending (`review.Build`, `review.Publish`, `review.CheckAppend`):** `Build` writes the commit
  that appends a review to a given head, and returns `review.ErrRecorded` with the existing commit
  when the history holds the review already. `Publish` pushes it with
  `--force-with-lease=<ref>:<old>` and returns `review.ErrStale` when another append came first.
  `CheckAppend` decides one ref update for the push hook, which does not call it (`not built`). It
  admits exactly one new review commit on the old head, whose `reviewer` is the pusher. A review
  that judges a version must be under a review name the caller says the pusher may write, must be
  about a change the repository records, and its binding must verify. A withdrawal needs no grant of
  its name. It must be of the pusher's own review, or the pusher an operator, who may withdraw any
  review. It refuses every delete.

### Signed notes and attestation statement (`the-valley/attestation/v1`)

An attestation note wraps canonical line-oriented `the-valley/attestation/v1` statement text in a
signed note envelope (`golang.org/x/mod/sumdb/note` format) and is stored at
`refs/the-valley/attestations/<64-hex-tree-digest>/<8-hex-key-hash>`
([`note/note.go`](../note/note.go), [`attest/text.go`](../attest/text.go),
[`schema/attestation.cue`](../schema/attestation.cue)).

```text
the-valley/attestation/v1
subject.primary valley-tree-v1
subject.digest.git-sha1 99b5f2f7c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6
subject.digest.valley-tree-v1 73847e0b8c5a1e2f3d4b5a6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e
predicateType the-valley/check/pure/v1
predicate.check.name prose-format
predicate.check.runner nix
predicate.check.attribute prose-format
predicate.result passed
predicate.inputs.valley-inputs-v1 a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2
predicate.derivation.sha256 b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3
predicate.output.sha256 c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4

— operator WhFo+DGb1c/Fk6x7y8z9A0B1C2D3E4F5G6H7I8J9K0L1M2N3O4P5Q6R7S8T9U0V1W2X3Y4Z5a6b7c8d9e0f1g2h3i4g=
```

- **Note envelope, key hash, and verifier key:**
  - The signed payload (`text`) is valid UTF-8 with no ASCII control characters except `\n`
    (`0x0a`), ending with `\n`.
  - A blank line (`\n\n`; the parser splits at the last `\n\n` in the note) separates the statement
    text from one or more signature lines.
  - Each signature line has the form
    `— <signer-name> <base64(4-byte-key-hash || 64-byte-ed25519-signature)>\n`, opening with
    `U+2014 EM DASH` followed by a space (`"— "`). `<signer-name>` is non-empty valid UTF-8 with no
    whitespace, no control characters, and no `+`.
  - **Key hash (`KeyHash`):** The first 4 bytes (big-endian `uint32`, written as 8 lowercase hex
    digits in ref names and verifier keys) of
    `SHA-256(<signer-name> + "\n" + 0x01 + <32-byte-ed25519-pubkey>)`. Because `<signer-name>` is
    inside the key hash, the same Ed25519 public key under two different names produces two distinct
    verifier keys.
  - **Verifier key line (`known-signers`):**
    `<signer-name>+<8-hex-key-hash>+<base64(0x01 || 32-byte-ed25519-pubkey)>`.
- **Statement written form (`the-valley/attestation/v1`):**
  - Line 1 is `the-valley/attestation/v1`.
  - Every subsequent line is `<dot.separated.key> <value>\n`. There are no escape sequences; every
    leaf value is a non-empty string without ASCII control characters; empty objects and empty
    arrays are rejected; array indices are dense decimal integers starting at `0`.
  - Lines appear in the fixed `statementOrder`:
    1. `subject.primary` (always `valley-tree-v1`)
    2. `subject.digest.<scheme>` (sorted by `<scheme>`: `git-sha1`, `git-sha256`, `sha256`,
       `valley-inputs-v1`, `valley-tree-v1`)
    3. `predicateType` (`the-valley/check/pure/v1`, `the-valley/check/effectful/v1`, or
       `the-valley/integration/transfer/v1`)
    4. `predicate.*` (ordered per `predicateType` below)
    5. `provenance.*` (optional: `provenance.harness`, `provenance.model`,
       `provenance.prompt.<scheme>`, `provenance.context.<scheme>`,
       `provenance.delegation.<i>.principal`, `provenance.delegation.<i>.grant`)
- **Pure check predicate (`the-valley/check/pure/v1`; stored at `<check>/statement.note`):**
  - `predicate.check.name`, `predicate.check.runner` (`nix`), `predicate.check.attribute`
  - `predicate.result` (`passed` or `failed`)
  - `predicate.inputs.valley-inputs-v1` (64-hex SHA-256)
  - `predicate.derivation.sha256` (64-hex SHA-256 of the `.drv` file)
  - `predicate.output.sha256` (64-hex `valley-tree-v1` digest of the Nix build output path)
- **Effectful check predicate (`the-valley/check/effectful/v1`; stored at
  `<check>/statement.note`):**
  - `predicate.check.name`, `predicate.check.runner` (`command`), `predicate.check.command`
  - `predicate.environment` (default `"local"`)
  - `predicate.observedAt` (`YYYY-MM-DDTHH:MM:SSZ`)
  - `predicate.result` (`passed` or `failed`)

### Canonical digest manifests (`valley-tree-v1`, `valley-inputs-v1`, `valley-delta-v1`)

the-valley uses three canonical SHA-256 manifest formats for trees, pure check inputs, and
version-to-version file deltas, plus a 16-hex evidence fingerprint
([`attest/digest.go`](../attest/digest.go), [`delta/delta.go`](../delta/delta.go),
[`change/id.go`](../change/id.go)).

```text
valley-tree-v1
100644 e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 12 greeting.txt
```

1. **`valley-tree-v1`:**
   - **Purpose:** Content-addressed 64-hex SHA-256 digest of a git tree or filesystem output tree,
     independent of commit metadata.
   - **Encoding:** Header `"valley-tree-v1\n"`, followed by one line per file or symlink entry
     across the tree, sorted ascending by raw `<path>` bytes:
     `<mode> <64-hex-sha256-of-content> <decimal-byte-length-of-path> <path>\n`
   - **Modes and content:** `100644` (regular file), `100755` (executable file), or `120000`
     (symlink, whose hashed content is the symlink target bytes). Directory entries are omitted
     (implied by file paths), and submodules (`160000`) are refused.
2. **`valley-inputs-v1`:**
   - **Purpose:** 64-hex SHA-256 digest of the set of Nix store paths referenced by a pure Nix
     check's `.drv` (`nix-store --query --references <drv>`).
   - **Encoding:** Header `"valley-inputs-v1\n"`, followed by one line per input name, sorted
     bytewise ascending and deduplicated: `<decimal-byte-length-of-name> <name>\n`
3. **`valley-delta-v1`:**
   - **Purpose:** 64-hex SHA-256 digest of the whole-file changes between a pre-tree `T` and a
     post-tree `R`, used by `valley review` (`valley-go compare`) to check whether two versions of a
     change make the exact same modifications to their respective merge bases.
   - **Encoding:** Header `"valley-delta-v1\n"`, followed by one line per path whose entry differs
     between `T` and `R`, sorted bytewise ascending by `<path>`:
     `<decimal-byte-length-of-path> <path> <pre-image> <post-image>\n` Each image is `-` when the
     path is absent on that side, or `<mode> <64-hex-sha256>` (`100644`, `100755`, or `120000`).
     Both trees are validated for Linux checkout safety (modes in `{100644, 100755, 120000, 40000}`,
     no empty/`.`/`..`/case-insensitive `.git` segments, component names `<= 255` bytes, non-empty
     symlink targets `<= 4,095` bytes without NUL).
   - **Bounded reading:** `delta.BetweenWithin` gives the same delta as `delta.Between` within a
     `delta.Budget`: bytes read and paths built, tree entries visited (a shared subtree once per
     path that names it), and a deadline, all required. It asks for each object's type and size
     before its content, kills git at the deadline, and fails with an error wrapping
     `delta.ErrBudget` at the first bound crossed. `review.Verify` recomputes a review's binding
     with it.
4. **Evidence fingerprint:**
   - **Purpose:** 16-hex-character fingerprint of all attestation refs in a repository, recorded in
     outcome records so the integrator and `valley status` know whether new evidence has arrived
     since the last pass.
   - **Encoding:** First 16 lowercase hex characters (8 bytes) of `SHA-256` over `<ref> <oid>\n` for
     every ref under `refs/the-valley/attestations/`, sorted by `<ref>` (`e3b0c44298fc1c14`, the
     SHA-256 prefix of the empty string, when no attestation refs exist).

### Integrator transfer statement (`the-valley/integration/transfer/v1`)

When a change lands (whether fast-forward or merged onto a moved `main`), the integrator signs a
transfer statement (`predicateType: "the-valley/integration/transfer/v1"`) before advancing `main`
and stores it at `transfer.note` under
`refs/the-valley/attestations/<landed-tree-digest>/<integrator-key-hash>` alongside countersigned
`<check>/statement.note` copies under
`refs/the-valley/attestations/<attested-tree-digest>/<integrator-key-hash>`
([`integrator/transfer.go`](../integrator/transfer.go),
[`integrator/refs.go`](../integrator/refs.go),
[`schema/attestation.cue`](../schema/attestation.cue)).

```text
the-valley/attestation/v1
subject.primary valley-tree-v1
subject.digest.git-sha1 88a4e1f0b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7
subject.digest.valley-tree-v1 73847e0b8c5a1e2f3d4b5a6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e
predicateType the-valley/integration/transfer/v1
predicate.change c-nxvpwlkmqtsrzyoukklnpmqrstuvwxyz
predicate.target refs/heads/main
predicate.base.valley-tree-v1 73847e0b8c5a1e2f3d4b5a6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e
predicate.policy.git-sha1 55d1c2b3a4f5e6d7c8b9a0f1e2d3c4b5a6f7e8d9
predicate.policy.sha256 d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5
predicate.checks.0.name prose-format
predicate.checks.0.rule unmoved
predicate.checks.0.evidence.sha256 f1e2d3c4b5a6f7e8d9c0b1a2f3e4d5c6b7a8f9e0d1c2b3a4f5e6d7c8b9a0f1e2

— integrator R7S8T9U0V1W2X3Y4Z5a6b7c8d9e0f1g2WhFo+DGb1c/Fk6x7y8z9A0B1C2D3E4F5G6H7I8J9K0L1M2N3O4P5Q6h3i4g=
```

- **Subject and predicate fields:**
  - `subject.primary`: `"valley-tree-v1"`.
  - `subject.digest.git-sha1`: The landed commit SHA-1 on `refs/heads/main`.
  - `subject.digest.valley-tree-v1`: The 64-hex `valley-tree-v1` digest of the landed commit's tree.
  - `predicateType`: `"the-valley/integration/transfer/v1"`.
  - `predicate.change`: Request name (`c-<change-id>` or `<branch-request-name>`).
  - `predicate.target`: Full target ref name (`refs/heads/main`).
  - `predicate.base.valley-tree-v1`: The 64-hex `valley-tree-v1` digest of the attested commit's
    tree (equal to `subject.digest.valley-tree-v1` when `main` did not move or merged to the
    identical tree).
  - `predicate.policy.git-sha1` and `predicate.policy.sha256`: The target tip commit SHA-1 from
    which the project policy was read and the 64-hex SHA-256 digest of the composed policy
    rendering.
  - **Either `predicate.checks.<i>.*` (when >= 1 check was required) or `predicate.required none`
    (when 0 checks were required; never both):**
    - `predicate.checks.<i>.name`: Check name (`^[a-z0-9][a-z0-9-]*$`).
    - `predicate.checks.<i>.rule`: `"unmoved"` (landed tree equals attested tree), `"closure"` (pure
      check whose `valley-inputs-v1` and derivation `sha256` on the landed tree matched the attested
      note's), or `"untouched"` (effectful check whose requiring path classes were untouched by
      `base..tip` on `main` and whose `observedAt` age is within `validity`).
    - `predicate.checks.<i>.evidence.sha256`: 64-hex SHA-256 of the signed statement text in the
      contributor's attestation note.
    - `predicate.checks.<i>.inputs.valley-inputs-v1`: Present when `rule == "closure"`; the
      recomputed 64-hex `valley-inputs-v1` digest on the landed tree.

### Integrator outcome record (`refs/the-valley/integration-outcomes/<target>/<request>`)

After judging an integration request, the integrator writes a single-line blob at
`refs/the-valley/integration-outcomes/main/<request>` recording the head, target tip, evidence
fingerprint, and decision ([`integrator/refs.go`](../integrator/refs.go),
[`rebase/outcome.go`](../rebase/outcome.go)).

```text
2222222222222222222222222222222222222222 3333333333333333333333333333333333333333 9a8b7c6d5e4f3a2b land
```

- **Format:**
  `<head-oid> <target-tip-oid> <16-hex-evidence-fingerprint> <decision>[ <failed-at-rfc3339>]\n`
  - `<head-oid>` and `<target-tip-oid>`: 40-hex SHA-1 (or 64-hex SHA-256) commit object IDs.
  - `<16-hex-evidence-fingerprint>`: First 16 hex digits of SHA-256 over sorted `<ref> <oid>\n`
    lines under `refs/the-valley/attestations/`.
  - `<decision>`: One of:
    - `land`: The change landed on `<target>`, and its integration request ref was consumed.
    - `stale`: The change conflicted with `<target>` or required check evidence was missing,
      expired, or did not transfer; the request ref remains standing.
    - `reject`: Attestation note or statement failed cryptographic or schema verification; the
      request ref remains standing.
    - `refused-not-ready`: The change record (or a change whose version head is brought by the
      request) is not `ready`, or a `c-<id>` request's head is not listed in the change's
      `versions`; the request ref remains standing and is reconsidered on every integrator pass.
    - `refused-merge`: The commits between the merge base with `<target>` and `<head-oid>` include a
      merge commit; the request ref remains standing and is reconsidered on every integrator pass.
    - `voided-withdrawn`: The change record was `withdrawn` when the integrator pass ran, and the
      integrator deleted the pending request ref.
    - `failed`: An internal error occurred during the pass. Requires the 5th field
      `<failed-at-rfc3339>` (`YYYY-MM-DDTHH:MM:SSZ`). The integrator retries a `failed` request on
      the same `(head, tip, evidence)` tuple after `retryFailedAfter = 10m`. Any unparseable outcome
      blob is treated by readers as `decision: "unknown"` (`outcome-unreadable` in `valley status`).

### Plan graph and knots (`refs/the-valley/knots`)

The plan graph lives at the exact ref `refs/the-valley/knots` as a linear chain of commits (`P = []`
on create, `P = [old]` on update) whose tree holds canonical Markdown files with YAML frontmatter
for knots and comments ([`knot/contract.go`](../knot/contract.go),
[`knot/canon.go`](../knot/canon.go), [`knot/model.go`](../knot/model.go)). Knots take their name
from the knotted cords used to keep records before writing.

```markdown
---
type: outcome
id: oc-c12d4ed
status: open
title: "Publish the CLI reference"
created: 2026-10-19
created_by: "operator"
parent: oc-7654321
owner: "operator"
blocked_by: ["app:oc-0123456", bd-89abcde]
changes: ["valley:the-valley/nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"]
---

Write `docs/reference.md` and link it from [[oc-7654321]].
```

- **Tree layout and filenames:**
  - Knot files (`100644`, `<= 64 KiB`, `<= 5,000` knots total):
    - `outcomes/oc-<7-hex>-<slug>.md` (`type: outcome`, statuses: active `open`, `in-progress`;
      terminal `done`, `abandoned`)
    - `bugs/bd-<7-hex>-<slug>.md` (`type: bug`, statuses: active `open`; terminal `closed`)
    - `ideas/ida-<7-hex>-<slug>.md` (`type: idea`, statuses: active `raw`, `exploring`, `adopted`;
      terminal `graduated`, `superseded`, `discarded`)
    - `questions/qn-<7-hex>-<slug>.md` (`type: question`, statuses: active `open`; terminal
      `answered`, `dropped`)
  - `<slug>` matches `^[a-z0-9]+(-[a-z0-9]+)*$` (`1..48` bytes, `MaxSlug = 48`), and `<id>` is
    `<prefix>-` followed by the first 7 lowercase hex digits of `SHA-256(<slug>)`. Every knot must
    have `created > "2026-08-05"` (`IDRuleCutoff`).
  - Comment files (`100644`, `<= 16 KiB`, `<= 20,000` comments total):
    - `comments/<knot-id>/<YYYYMMDDTHHMMSSZ>-<16-hex-op>.md`. A comment's ID is its filename without
      `.md` (`<YYYYMMDDTHHMMSSZ>-<16-hex-op>`). Because comment files are never moved or deleted, a
      comment's ID never changes.
    - Frontmatter between `---` fences starts with `author: "<principal>"`. An edited comment
      follows `author` with `edited: "<YYYY-MM-DDTHH:MM:SSZ>"` and `edited_by: "<principal>"`, in
      that order. Each value is a JSON string. `edited` and `edited_by` must appear together or not
      at all (`RuleProvenance`); an unedited comment has only the `author` line. A blank line and a
      required non-empty Markdown body follow the closing `---`.

      ```markdown
      ---
      author: "alice"
      edited: "2026-10-25T15:00:00Z"
      edited_by: "operator"
      ---

      Moved to bd-89abcde.
      ```

- **Canonical text and body rules:**
  - Every knot and comment file must be valid UTF-8 with no control characters except `\n` (`0x0a`)
    and `\t` (`0x09`), and no `U+FFFE` or `U+FFFF` (`RuleText`).
  - Frontmatter opens with `---\n` on line 1 and closes with `---\n`. When a knot has a non-empty
    body, the body is separated from the closing `---\n` by exactly one blank line (`\n`), has no
    leading or trailing blank lines, and ends with a single `\n`; when the body is empty, the file
    ends immediately after the closing `---\n` (`RuleCanonical`).
- **Commit rules:**
  - Commit size `<= 16 KiB`, `<= 32` changed paths per commit, `Committer` name must equal the
    pushing principal, and the commit message must end with the trailer
    `Valley-Op: <16-lowercase-hex>`.
  - No knot or comment file may be deleted, and a knot's `id`, `type`, `created`, and `created_by`
    are immutable after creation. On a newly created knot or comment, `created_by` / `author` must
    equal the pushing principal, and a new comment may not include `edited` or `edited_by`.
  - A push that modifies an existing comment file is an edit. The edited comment must keep its
    original `author`, include both `edited` and `edited_by`, and set `edited_by` to the pushing
    principal. The hook admits an edit only from the comment's `author` or from a principal holding
    the `approve` grant (also accepted as `request`)
    ([`valleyhook/knots.go`](../valleyhook/knots.go)). Neither the hook nor the CLI compares
    `edited` against a clock or against the comment's previous `edited` value.
- **Canonical frontmatter fields in required order:**

| Order | Field            | Allowed types                | Encoding          | Required / rules                                                                                                                                                                                                   |
| :---- | :--------------- | :--------------------------- | :---------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1     | `type`           | all                          | bare token        | Always (`outcome`, `bug`, `idea`, `question`).                                                                                                                                                                     |
| 2     | `id`             | all                          | bare token        | Always (`^(oc\|bd\|ida\|qn)-[0-9a-f]{7}$`).                                                                                                                                                                        |
| 3     | `status`         | all                          | bare token        | Always (must belong to `type`'s status list).                                                                                                                                                                      |
| 4     | `title`          | all                          | JSON string       | Always (`1..200` bytes, no newlines/tabs).                                                                                                                                                                         |
| 5     | `created`        | all                          | bare token        | Always (`YYYY-MM-DD`, `> 2026-08-05`).                                                                                                                                                                             |
| 6     | `created_by`     | all                          | JSON string       | Always (principal name, `<= 64` bytes).                                                                                                                                                                            |
| 7     | `source`         | all                          | JSON string       | Optional (`1..200` bytes).                                                                                                                                                                                         |
| 8     | `parent`         | all                          | bare token        | Optional; must name a local `outcome` (`oc-<7-hex>`) in the same graph; `parent` edges may not form a cycle.                                                                                                       |
| 9     | `owner`          | all                          | JSON string       | Optional (principal name, `<= 64` bytes).                                                                                                                                                                          |
| 10    | `blocked_by`     | `outcome`, `bug`             | flow list `[...]` | Always written on `outcome` and `bug` (`[]` when empty); up to 64 sorted, deduplicated items: bare local `<id>` (must exist in graph; local `blocked_by` edges may not form a cycle) or quoted `"<project>:<id>"`. |
| 11    | `claimed_by`     | `outcome`, `bug`             | JSON string       | Optional (`<= 64` bytes); must be paired with `claimed_at`.                                                                                                                                                        |
| 12    | `claimed_at`     | `outcome`, `bug`             | JSON string       | Optional (`YYYY-MM-DDTHH:MM:SSZ`); must be paired with `claimed_by`.                                                                                                                                               |
| 13    | `changes`        | `outcome`, `bug`, `question` | flow list `[...]` | Optional (omitted when empty); up to 64 sorted, deduplicated JSON strings (`<= 256` bytes each) matching `valley:<project>/<32-letter-change-id>` or `https://github.com/<org>/<repo>/pull/<n>`.                   |
| 14    | `target`         | `outcome`, `bug`             | JSON string       | Optional (`<project>` or `<project>:<id>`).                                                                                                                                                                        |
| 15    | `graduated_into` | `idea`                       | bare token        | Required when `status == "graduated"` and forbidden otherwise; must name an existing local `outcome` (`oc-*`) or `question` (`qn-*`) in the live plan graph.                                                       |

### CLI JSON outputs

Five `--json` commands emit versioned JSON documents for scripts and front ends.

#### `valley-status/v1`

`valley status --json <change>` emits one `valley-status/v1` object on a single line.

```json
{
  "schema": "valley-status/v1",
  "change": "greeting",
  "request_name": "c-nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
  "target": "main",
  "target_tip": "3333333333333333333333333333333333333333",
  "head": "2222222222222222222222222222222222222222",
  "request": {
    "ref": "refs/the-valley/integration-requests/main/c-nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
    "commit": "2222222222222222222222222222222222222222"
  },
  "outcome": null,
  "state": "awaiting-decision",
  "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
  "handle": "nxvp",
  "version": 2,
  "title": "Add greeting helper",
  "owner": "operator",
  "landing": "not-landed",
  "restack": null,
  "knots": [
    {
      "id": "oc-c12d4ed",
      "type": "outcome",
      "status": "open",
      "title": "Publish the CLI reference"
    }
  ]
}
```

- **Fields:**
  - `schema`: `"valley-status/v1"`.
  - `change` (`string`): Branch name (from the active version's `branch`, or the queried branch
    name).
  - `request_name` (`string`): `c-<change_id>` for a recorded change, or `<branch>` with `/`
    replaced by `-`.
  - `target` (`string`): `"main"`.
  - `target_tip` (`string`): Current commit OID of `refs/heads/main` on `origin`.
  - `head` (`string` or `null`): Active version head OID (or remote branch tip / request / outcome
    head for an unrecorded branch).
  - `request` (`object` or `null`): `{ "ref": <string>, "commit": <oid> }` when a request ref exists
    on `origin`.
  - `outcome` (`object` or `null`):
    `{ "decision": <string>, "head": <oid or null>, "tip": <oid or null>, "failed_at": <rfc3339 or null> }`
    when an outcome ref exists on `origin`.
  - `state` (`string`): One of `integrated`, `integrated-recorded`, `outdated-request`,
    `outcome-unreadable`, `voided-withdrawn`, `not-requested`, `awaiting-decision`,
    `awaiting-new-decision`, `stale`, `rejected`, `refused-not-ready`, `refused-merge`, or `failed`.
  - `change_id`, `handle`, `title`, `owner`, `landing` (`string` or `null`) and `version` (`int` or
    `null`): Populated for a recorded change (`landing` is `"landed"`, `"landed-recorded"`,
    `"uncertain"`, or `"not-landed"`); `null` for an unrecorded branch.
  - `restack` (`object` or `null`):
    `{ "ref": <string>, "commit": <oid>, "current": <bool>, "report": <RestackReport> }` when the
    change has a valid restack report on `origin`.
  - `knots` (`array` of `{ "id", "type", "status", "title" }`): Knots in `refs/the-valley/knots`
    whose `changes` field links `valley:<project>/<change_id>`, active knots first (`[]` when none
    or for an unrecorded branch).

#### `valley-changes/v1` and `valley-pending/v1`

`valley changes --json` emits `valley-changes/v1`, and `valley pending --json` emits
`valley-pending/v1` using the same repository and change structure.

```json
{
  "schema": "valley-pending/v1",
  "host": "git@valley.example",
  "repos": [
    {
      "repo": "demo",
      "url": "git@valley.example:demo",
      "error": null,
      "changes": [],
      "drafts": [],
      "branches": [
        {
          "branch": "scratch",
          "head": "5555555555555555555555555555555555555555"
        }
      ]
    }
  ]
}
```

- **Top-level fields:**
  - `schema`: `"valley-changes/v1"` or `"valley-pending/v1"`.
  - `host` (`string`): Host prefix derived from `origin`'s URL (URL minus the final `/<repo>` or
    `:<repo>` segment).
  - `repos` (`array` of repository objects):
    - `repo` (`string`), `url` (`string`), `error` (`string` or `null`). When `error` is non-null,
      `changes` and `branches` are `[]` (and `drafts` is `[]` in `valley-pending/v1`).
    - `changes` (`array` of `jsonChange`): In `valley-changes/v1`, all recorded changes in every
      state (ordered by Change ID). In `valley-pending/v1`, only `ready` changes whose active
      version is not landed (`not-landed` or `uncertain`).
    - `drafts` (`array` of `jsonChange`, **present only in `valley-pending/v1`**): Changes whose
      recorded `state` is `"draft"`. Omitted (`omitempty`) in `valley-changes/v1`.
    - `branches` (`array` of `{ "branch": <string>, "head": <oid> }`): Remote branches on `origin`
      (other than `main`) not reachable from `main` and not equal to any recorded change's active
      version head.
- **`jsonChange` fields:**
  - `change_id`, `handle`, `target`, `owner`, `state`, `record` (record commit OID), `seq`,
    `active`.
  - `versions` (`array` of
    `{ "n", "head" (40-hex OID without git-sha1: prefix), "title", "branch", "uploader", "landing" ("landed"|"landed-recorded"|"uncertain"|"not-landed"), "landed_commit" (<oid or null>), "request" ({"ref","commit"} or null), "outcome" ({"decision","head","tip"} or null) }`).
  - `includes` (`array` of `string`): Change IDs of other changes in the candidate set (all changes
    in `valley-changes/v1`; unlanded changes in `valley-pending/v1`) whose active version head is a
    strict ancestor of this change's active version head in `main..head`.
  - `restack` (`jsonRestack` or `null`) and `knots` (`array` of
    `{ "id", "type", "status", "title" }`).

#### `valley-plan/v1`

`valley plan --json` emits one indented `valley-plan/v1` JSON document.

```json
{
  "schema": "valley-plan/v1",
  "now": "2026-10-19T12:00:00Z",
  "operator": "operator",
  "projects": [
    {
      "project": "demo",
      "error": null,
      "tip": "6666666666666666666666666666666666666666",
      "roots": ["oc-c12d4ed"],
      "knots": [],
      "waiting_on_you": {
        "knots": ["oc-c12d4ed"],
        "changes": [],
        "changes_error": null
      },
      "in_flight": [],
      "open_residuals": []
    }
  ]
}
```

- **Fields:**
  - `schema`: `"valley-plan/v1"`.
  - `now` (`string`): UTC evaluation timestamp (`RFC 3339`).
  - `operator` (`string` or `null`): Principal from `--owner` or `git config valley.principal`.
  - `projects[]`: Each element has:
    - `project` (`string`), `error` (`string` or `null`), `tip` (`string`, commit OID of
      `refs/the-valley/knots` or `""` when the project has no graph).
    - `roots` (`array` of `string`): Root `outcome` knot IDs in tree order (`created`, `title`,
      `id`).
    - `knots` (`array` of `planKnot`): Every knot in tree walk order. Each `planKnot` embeds
      [`jsonKnot`](#valley-knot---json-valley-knotv1-valley-knotsv1-valley-knot-opv1-valley-knot-logv1-valley-knot-schemav1)
      (without `body` or `comments`, with `children` ordered in tree order and `progress` counted
      over all descendants) plus:
      - `state` (`string`): `"ready"`, `"in-progress"`, `"blocked"`, `"active"`, `"done"`, or
        `"abandoned"`.
      - `edges` (`array` of `planEdge`): Resolved cross-project references from `blocked_by` and
        `target`, each
        `{ "field": "blocked_by"|"target", "ref": "<project>:<id>", "project": <string>, "id": <string>, "state": "resolved"|"dangling", "why": "missing"|"unreadable"|null, "status": <string or null>, "title": <string or null> }`.
    - `waiting_on_you` (`object` or `null`):
      `{ "knots": [<id>...], "changes": [{ "repo", "handle", "change_id", "version", "title", "head" }...], "changes_error": <string or null> }`
      (`null` when `error != null`). `knots` is `null` when `operator` is `null`, and otherwise
      lists active `outcome`/`bug` and open `question` knots owned by `operator`.
    - `in_flight` (`array` of `string` or `null`): Claimed active knot IDs ordered by oldest
      `claimed_at` first.
    - `open_residuals` (`array` of `{ "parent": <id or null>, "bugs": [<id>...] }` or `null`): Open
      `bug` knots grouped by `parent`.

#### `valley knot --json` (`valley-knot/v1`, `valley-knots/v1`, `valley-knot-op/v1`, `valley-knot-log/v1`, `valley-knot-schema/v1`)

`valley knot <verb> --json` emits one of five indented JSON schemas depending on the subcommand:

```json
{
  "schema": "valley-knot-op/v1",
  "project": "demo",
  "op": "0123456789abcdef",
  "commit": "7777777777777777777777777777777777777777",
  "knot": {
    "id": "oc-c12d4ed",
    "type": "outcome",
    "status": "open",
    "active": true,
    "title": "Publish the CLI reference",
    "created": "2026-10-19",
    "created_by": "operator",
    "blocked_by": [],
    "changes": [],
    "path": "outcomes/oc-c12d4ed-publish-the-cli-reference.md",
    "body": "",
    "comment_count": 0,
    "ready": true,
    "waiting_on": [],
    "cleared_by_ending": [],
    "dangling": [],
    "unresolved": [],
    "children": [],
    "blocks": []
  }
}
```

- **Envelope by subcommand:**
  - `valley knot show <id> [--at <commit>] --json`:
    `{ "schema": "valley-knot/v1", "project": <string>, "tip": <oid>, "at": <oid>, "knot": <jsonKnot with body & comments> }`.
    `tip` is the current tip of `refs/the-valley/knots`. `at` is the full commit OID that `--at`
    resolved to, and is omitted when `--at` is not passed.
  - `valley knot log <id> --json`:
    `{ "schema": "valley-knot-log/v1", "project": <string>, "tip": <oid>, "id": <knot id>, "path": <knot file path>, "commits": [<logEntry>...] }`,
    newest first. Each `logEntry` is
    `{ "commit": <oid>, "at": <RFC 3339 UTC committer time>, "committer": <string>, "op": <16-hex-op-id>, "subject": <string>, "files": [{ "path": <string>, "change": "added"|"changed"|"deleted", "comment": <comment id> }...] }`,
    where `comment` is present only for files under `comments/<id>/`.
  - `valley knot list --json`:
    `{ "schema": "valley-knots/v1", "project": <string>, "tip": <oid>, "knots": [<jsonKnot without body & comments>...] }`.
  - `valley knot (new|set|link|unlink|edit|comment|claim|unclaim) --json`:
    `{ "schema": "valley-knot-op/v1", "project": <string>, "op": <16-hex-op-id>, "commit": <oid>, "knot": <jsonKnot with body & comments> }`.
    When a write subcommand is a no-op (for example, `set` or `claim` leaves the knot unchanged), it
    prints a note to `stderr`, pushes nothing, and emits no JSON on `stdout`.
  - `valley knot schema --json`:
    `{ "schema": "valley-knot-schema/v1", "ref": "refs/the-valley/knots", "types": [...], "fields": [...], "edges": [...], "limits": { "knots": 5000, "comments": 20000, "knot_bytes": 65536, "comment_bytes": 16384, "changed_paths_per_commit": 32, "title_bytes": 200, "list_items": 64 } }`.
- **`jsonKnot` fields:**
  - Stored fields: `id`, `type`, `status`, `active` (`bool`), `title`, `created`, `created_by`,
    `source` (`omitempty`), `parent` (`omitempty`), `owner` (`omitempty`), `blocked_by`
    (`[]string`), `claimed_by` (`omitempty`), `claimed_at` (`omitempty`), `changes` (`[]string`),
    `target` (`omitempty`), `graduated_into` (`omitempty`), `path`, `comment_count` (`int`).
  - Full-only fields (included in `show` and write ops, omitted in `list` and `plan`): `body`
    (`string`), `comments` (`array` of
    `{ "id" (<YYYYMMDDTHHMMSSZ>-<16-hex-op>), "author", "at" (RFC 3339), "op" (16-hex), "edited" (RFC 3339, omitempty), "edited_by" (omitempty), "body" }`,
    `omitempty` — omitted when the knot has `0` comments). `edited` and `edited_by` are included
    only on edited comments.
  - Derived `knot.View` fields: `ready` (`bool`), `waiting_on` (`[]string`), `cleared_by_ending`
    (`[]string`), `dangling` (`[]string`), `unresolved` (`[]string`), `children` (`[]string`),
    `blocks` (`[]string`), `progress` (`{ "done": <int>, "of": <int> }`, `omitempty`), `claim`
    (`{ "by": <string>, "at": <rfc3339>, "age_seconds": <int64>, "stale": <bool, true when age > 24h> }`,
    `omitempty`).

### Web export (`the-valley/web-export/v1`)

A web export is the snapshot a valley host sends to a read-only web server for public projects. It
consists of `export.json`, one git bundle of `main` for each exported project, and a signed manifest
that authenticates both. A publisher on the valley host pushes the export over HTTP to a receiver on
the web server, and the web server never connects back to the host. Package `export` of the `webui`
module ([`webui/export/`](../webui/export/)) implements the types, the verification checks below,
and the receiver's state directory. `valley-web-publish` is the publisher, and the web server,
[`valley-web`](#web-server-valley-web), reads the receiver's state directory with the package. The
receiver's HTTP handlers are `not built`.

#### `export.json` (`valley-web-export/v1`)

`export.Build` builds `export.json` from the valley CLI's JSON output and the publisher's
configuration. It reads `valley plan --all --json`, `valley pending --all --json`, and
`valley knot show --project <project> <id> --json` for every knot in every public project. The
publisher also gives it each project's visibility, each public project's `main` commit
(`source_tip`), and a display mapping for principal names. `Export.Encode` writes the export as
compact JSON ending with a newline.

```json
{
  "schema": "valley-web-export/v1",
  "collected": "2026-10-25T12:00:00Z",
  "projects": [
    {
      "project": "app",
      "visibility": "public",
      "graph_tip": "3f6a0c1d9e8b7a6f5e4d3c2b1a0f9e8d7c6b5a49",
      "source_tip": "ff4ea8a16ee7db8ebda8169da1ce9d6cc2a2ede1",
      "roots": ["oc-a000001"],
      "knots": [],
      "in_flight": [],
      "open_residuals": [],
      "changes": []
    }
  ]
}
```

- **What is exported:** Visibility is set per project. A project is exported only when its
  visibility is `public`, the only visibility value in v1. Projects omitted from the visibility map
  are not exported. The publisher builds the map from `policy/web` declarations (see
  [Web visibility](#web-visibility-schemawebcue)). Every knot and every comment of an exported
  project is included.
- **Top-level fields:** `schema` (`"valley-web-export/v1"`), `collected` (`string`, the plan's
  `now`), and `projects` (sorted by project name).
- **`projects[]` fields:**
  - `project` (`string`): Project name (`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`, at most 64 bytes). The
    pattern forbids `/` and dot segments (`.` and `..`), so the name is safe to use as a file name.
  - `visibility` (`"public"`).
  - `graph_tip` (`string`): Commit object ID of `refs/the-valley/knots` where the knots were read,
    or `""` when the project has no plan graph.
  - `source_tip` (`string`): Commit object ID of `refs/heads/main` carried by the project's bundle.
  - `roots` and `open_residuals`: Same as in [`valley-plan/v1`](#valley-planv1).
  - `knots` (`array`): Every knot in the project, in tree order. Each item carries the `planKnot`
    fields of `valley-plan/v1` plus `body` and `comments` from the knot's detail JSON. It omits
    `path`, `claimed_by`, and `claimed_at`; claim details live in the `claim` object. Each comment
    carries `author`, `at`, `op`, and `body`. An edited comment also carries `edited` (when it was
    last edited) and `edited_by` (who edited it); a comment never edited has neither field.
  - `in_flight` (`array` of `string`): Active claimed knots ordered from oldest claim to newest,
    followed in tree order by active unclaimed knots whose status is `in-progress`.
  - `changes` (`array`): Ready changes whose active version has not landed. Each item carries
    `change_id`, `handle`, `target`, `owner`, `includes`, `knots`, and the active version alone as
    `version`:
    `{ "n", "head", "title", "uploader", "landing", "request": { "commit" } or null, "outcome": { "decision", "head", "tip" } or null }`.
    `includes` lists only changes that are also in `changes`.
- **What is left out:** `export.Build` reads only the fields it knows, so any field added to the
  CLI's JSON is ignored until the projection names it. From the plan, it omits `operator`,
  `waiting_on_you`, and each project's `error`. From the pending listing, it omits `host` and each
  repository's `url`, `error`, `drafts`, and `branches`. From each change, it omits `record`, `seq`,
  `state`, `restack`, and every version except the active one; from the active version, it omits
  `branch`, `landed_commit`, and `request.ref`. From each knot, it omits `path`, `claimed_by`, and
  `claimed_at`. From each comment, it omits `id`.
- **Principal names:** The display mapping (`export.Principals`) rewrites `created_by`, `owner`,
  `claim.by`, each comment's `author` and `edited_by`, a change's `owner`, and a version's
  `uploader` to configured display names such as `operator` or `agent`. Each display name is 1 to 64
  bytes of UTF-8 with no control characters. An empty field stays empty, and any unmapped name
  becomes `contributor`. Free-text fields (`title`, `body`, comment bodies, and `source`) are
  exported as written.
- **References to hidden projects:** When a knot's `blocked_by` or `target` refers to a project that
  is not exported, the knot's `blocked_by` and `target` fields keep the `"<project>:<id>"` string as
  written. The corresponding entry in `edges` has `"state": "hidden"` and `null` for `why`,
  `status`, and `title`. The reference is removed from `waiting_on`, `cleared_by_ending`,
  `dangling`, and `unresolved`, because those lists reflect the hidden knot's status. The referring
  knot's `state` and `ready` are unchanged, so a blocker in a hidden project still blocks it. When a
  knot has one or more open blockers in hidden projects, it carries `"waiting_on_hidden": true`. The
  field omits the hidden knots' IDs and count. Other knots omit `waiting_on_hidden`, and
  `export.Decode` accepts exports without it (see
  [Unpublished blockers](./architecture.md#unpublished-blockers) for what a reader can infer when
  `blocked_by` names a single hidden knot).
- **Refusals (`export.Build`):** `export.Build` fails rather than build a partial export. It fails
  when a public project is missing or has an `error` in the plan or the pending listing, or when a
  knot in a public project is missing its detail, appears twice, or has no `body`. It fails with
  `export.ErrInconsistent` when a knot's detail was read at a different graph `tip` than the plan or
  its comment count disagrees with `comment_count`. On `ErrInconsistent`, the publisher reads its
  inputs again.
- **Strict reading (`export.Decode`):** `export.Decode` rejects unknown JSON fields and any
  non-whitespace token after the root JSON object. It requires `schema` to be
  `"valley-web-export/v1"`, projects to appear once in ascending name order with valid project names
  and `public` visibility, and knot IDs to be valid and unique within each project. Each project's
  `source_tip`, non-empty `graph_tip`, and each change's `version.head` must be a 40- or 64-hex
  lowercase object ID. Every knot's `title`, `body`, `source`, `created_by`, and `owner`, and every
  comment's `body`, `author`, `edited`, and `edited_by`, must be valid UTF-8 with no control
  characters other than `\n` and `\t`.
- **Content digest (`Export.Digest`):** `Export.Digest` is the 64-hex SHA-256 of the export's JSON
  with `collected` cleared and each claim's `age_seconds` and `stale` zeroed. Those fields change
  with the clock rather than with the valley, so two exports of the same valley state have the same
  digest. A reader computes a claim's age from `claim.at`.

#### Web export manifest

The manifest is a [signed note](#signed-notes-and-attestation-statement-the-valleyattestationv1)
signed by the publisher's export key:

```text
the-valley/web-export/v1
instance valley-host
destination example.org
sequence 1792929630000000000
file export.json 2a841dd0581200e7ecc0443fe0a39ab502cd884c09f9232bc372ca108a0e2a99 5108
project app public 3f6a0c1d9e8b7a6f5e4d3c2b1a0f9e8d7c6b5a49 ff4ea8a16ee7db8ebda8169da1ce9d6cc2a2ede1
bundle app fffb1ae32f18c72dae5ee91ed0bcd14f5462cc10900db4d1cf7abc480975443b 1133

— valley-host/web-export e7kLV5lThaGSbsBUVC2cJ8I1vn6XJWS9+L0wwWmdRAYL827G/au5kE5OidGXMH+ofpnTprs+o4GgPNuTwqOexf8zrgw=
```

- **Records:** Line 1 is `the-valley/web-export/v1`. Each remaining line in the note body is a
  keyword and its fields, separated by single spaces:
  - `instance <name>`: Name of the valley instance that signed the manifest.
  - `destination <name>`: Name of the target receiver, matching the receiver's configuration.
  - `sequence <decimal>`: Signing time in Unix nanoseconds. There is no separate sequence counter.
  - `file export.json <sha256> <size>`: SHA-256 digest and byte size of `export.json`.
  - `project <name> public <graph-tip> <source-tip>` and `bundle <name> <sha256> <size>`: One pair
    for each exported project. A project with no plan graph writes `-` for `<graph-tip>`.
- **Grammar rules (`export.ParseManifest`, `Manifest.Text`):** `instance`, `destination`,
  `sequence`, and `file` appear once each. Each exported project has one `project` record and one
  `bundle` record; no project may appear twice, and no `bundle` record may name a project without a
  `project` record. Names in `instance`, `destination`, `project`, and `bundle` follow the
  project-name rule (`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`, at most 64 bytes). Tips (other than `-`) are
  40- or 64-hex lowercase object IDs. Hashes are 64 lowercase hex digits. Sizes and `sequence` are
  positive 64-bit decimal integers with no sign and no leading zero. If two declared bodies share a
  hash, they must declare the same size. `ParseManifest` accepts records in any order;
  `Manifest.Text` writes them in the order shown above, with projects sorted by name.
- **Admission (`export.VerifyManifest`):** The receiver verifies a manifest in six separate steps,
  in this exact order:
  1. Read the manifest. The receiver reads up to 65,537 bytes so that it can tell when the manifest
     is too long. It accepts at most 64 KiB (`MaxManifest = 65,536` bytes) and refuses a longer one
     as `too-large`.
  2. Open the signed note under the receiver's pinned verifier keys.
  3. Check the `the-valley/web-export/v1` header line and parse the records.
  4. Require `instance` and `destination` to match the receiver's configured instance and
     destination.
  5. Require `sequence` to be strictly greater than the receiver's `high-water` mark.
  6. Require the signing time (`sequence`) to be at most 72 hours (`MaxAge`) behind and at most 1
     minute (`MaxAhead`) ahead of the receiver's clock.

  The signing-time window bounds how old a manifest can be replayed if the receiver loses its
  `high-water` file. It never expires an export after the receiver has accepted it.

- **Agreement (`Manifest.CheckExport`):** `Manifest.CheckExport` checks that `export.json` has the
  SHA-256 and byte size declared in the `file` record, passes `export.Decode`, and lists the same
  projects as the manifest with the same `visibility`, `graph_tip`, and `source_tip`.

#### Web export upload

The publisher uploads an export to the receiver in three steps. Every response body is JSON. A full
upload is two requests plus one `PUT` for each needed body. Package `export` defines the paths,
reply schemas, and checks for each step; the HTTP handlers that wire them are `not built`.

| Step | Request                                                                       | Reply                                                                                                                                                                                                                               |
| :--- | :---------------------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | `POST /web-export/v1/manifest` with the signed manifest                       | `200` and `valley-web-inventory/v1`: `active` (the served export's `sequence` and content `digest`, or `null`), `holds` (each project's `source_tip` with a verified repository), and `needs` (bodies to send, `export.json` first) |
| 2    | `PUT /web-export/v1/<sequence>/<sha256>` for each needed body, with its bytes | `204` once the body has been verified and staged                                                                                                                                                                                    |
| 3    | `POST /web-export/v1/<sequence>/commit` with an empty body                    | `200` and `valley-web-committed/v1`: the `sequence` activated                                                                                                                                                                       |

- **Held repositories:** When the receiver's active export already holds a project's repository at
  the manifest's `source_tip`, the inventory lists that project in `holds` and omits its bundle from
  `needs`. On commit, the new generation copies `refs/heads/main` from the held repository
  (`export.CarryRepo`). If `active.digest` matches the publisher's `Export.Digest` and `needs`
  includes no bundles, the receiver already serves the same content and repositories. The publisher
  may then stop after step 1 without sending `export.json` or committing, and the next manifest it
  posts replaces the staged upload.
- **Retries:** Posting the same manifest again keeps the staging directory and returns the same
  inventory minus any bodies already staged. This works only while the manifest is still admissible.
  A retried manifest is refused as `replayed` if the receiver has since accepted its sequence, as
  `superseded` if a staged upload has a later sequence, and as `out-of-window` if its signing time
  is more than 72 hours behind the receiver's clock. A body that is already staged still passes the
  size cap, the `Content-Encoding` check, and the `Content-Length` check. After those, the receiver
  returns `204` without reading the request body again.
- **Body check (`export.StageBody`):** Before reading the request body, the receiver refuses a body
  whose declared `size` exceeds the receiver's cap (`too-large`), a request with a
  `Content-Encoding` header (`content-encoding`), or a request whose `Content-Length`, when given,
  does not equal the declared `size` (`size-mismatch`). A request with no known length skips that
  comparison. The receiver then counts the streamed bytes and refuses a body that is shorter or
  longer than `size` (`size-mismatch`). While reading, it hashes the stream with SHA-256, reads at
  most `size + 1` bytes, and writes to a temporary file inside the upload's `bodies/` directory.
  Once the byte count and SHA-256 match the manifest, it syncs and renames the temporary file to
  `bodies/<sha256>`. Nothing is written outside the upload's staging directory. The temporary file
  and the staged body have mode `0600`.
- **Bundle acceptance (`export.AcceptBundle`):**
  1. Read the bundle header. The receiver reads at most 64 KiB + 1 byte of the file, so that it can
     tell when the header is too long, and never buffers a line past that bound. Require a v2 or v3
     git bundle header with no prerequisite commits.
  2. Require the header to list exactly one ref, `refs/heads/main`, at the project's declared
     `source_tip`.
  3. Initialize a fresh bare repository and fetch the bundle into it with the refspec
     `refs/heads/main:refs/heads/main`, `--no-tags`, `transfer.fsckObjects=true`, and
     `core.fsync=all`.
  4. Verify that the repository holds `refs/heads/main` at `source_tip` and no other ref.
  5. Write the repository's commit-graph with `git commit-graph write --reachable`.

  A bundle that fails any of these checks is refused as `bad-bundle`. Failing to open the bundle
  file, to initialize the repository, or to list its refs is an operational error, not a refusal of
  the bundle. Either way, no repository is left behind.

- **Reachability (`export.Reachable`):** A git bundle can contain objects that `refs/heads/main`
  does not reach. When serving a repository, browsing serves a commit only when `export.Reachable`
  confirms that it is a commit reachable from `source_tip` and that its object ID has the same
  length as `source_tip`.
- **Commit outcome:** A commit has succeeded once `Store.Activate` has written the upload's sequence
  to `high-water` and swapped the `active` symlink to the new generation. The receiver then replies
  `200` with `valley-web-committed/v1`. A later failure while syncing the swap to disk or deleting
  old generations, stale staging directories, and `trash/` does not change the `200` reply. `Store`
  passes that failure to the `report` callback given to `export.OpenStore` for the receiver to log.
  The next sweep retries failed deletions, but it does not retry a failed directory sync. Recovery
  at `OpenStore` returns at once when `active` already names the `high-water` generation. It
  restores the swap after a crash only when the two differ. A commit that fails before `active` is
  swapped leaves the previous export active. If it failed after `high-water` was written, its
  sequence is accepted, and retrying the commit is refused as `replayed`. The publisher's next
  manifest, signed later, replaces the upload.
- **Refusals:** A refused request returns the code's HTTP status and a `valley-web-error/v1` JSON
  object:
  `{ "schema": "valley-web-error/v1", "error": <code>, "message": <string>, "high_water" (with replayed), "needs" (with incomplete) }`.
  The `error` code is stable for programs to act on; `message` is human-readable text.

| Code                | HTTP  | Step | Meaning                                                                                                   |
| :------------------ | :---- | :--- | :-------------------------------------------------------------------------------------------------------- |
| `too-large`         | `413` | 1, 2 | A manifest over 64 KiB, or a body declared larger than the receiver's cap.                                |
| `bad-signature`     | `403` | 1    | The manifest does not open under the pinned key.                                                          |
| `bad-manifest`      | `400` | 1    | The manifest's text is not a valid manifest.                                                              |
| `wrong-instance`    | `403` | 1    | The manifest is from another instance.                                                                    |
| `wrong-destination` | `403` | 1    | The manifest is for another receiver.                                                                     |
| `replayed`          | `409` | 1, 3 | The sequence is not greater than the high-water mark. The publisher waits for its clock to pass the mark. |
| `out-of-window`     | `403` | 1    | The manifest was signed more than three days ago, or more than a minute ahead.                            |
| `superseded`        | `409` | 1-3  | A newer manifest is staged.                                                                               |
| `no-manifest`       | `404` | 2, 3 | Nothing is staged for the sequence.                                                                       |
| `not-declared`      | `404` | 2    | The manifest declares no body with that hash.                                                             |
| `content-encoding`  | `415` | 2    | The body was sent with a `Content-Encoding`.                                                              |
| `size-mismatch`     | `400` | 2    | The body's size is not the declared size.                                                                 |
| `hash-mismatch`     | `422` | 2, 3 | The body's SHA-256 is not the declared one.                                                               |
| `unreadable`        | `400` | 1, 2 | The request body could not be read.                                                                       |
| `incomplete`        | `409` | 3    | A needed body has not verified; `needs` lists what is still needed.                                       |
| `bad-export`        | `422` | 3    | `export.json` is not a valid export, or disagrees with the manifest.                                      |
| `bad-bundle`        | `422` | 3    | A bundle fails acceptance.                                                                                |

A refused upload leaves the active export as it was.

#### Web export publisher

`valley-web-publish` ([`webui/cmd/valley-web-publish/`](../webui/cmd/valley-web-publish/)) builds an
export of a valley host's public projects and uploads it. One run is one pass.

```sh
valley-web-publish --instance-repo /srv/git/valley.git --instance valley-host \
  --destination example.org --receiver https://example.org --principals principals.json
```

- **A pass:** In order, the pass:
  1. takes an exclusive `flock(2)` lock on `<state>/lock`, waiting for a pass that holds it;
  2. reads the export key, or makes one on the first start, and checks the state and the caches
     against `--max-storage`;
  3. fetches the instance repository's `main` and `refs/the-valley/projects` into the publisher's
     own bare repository, `$XDG_CACHE_HOME/valley-web-publish/origin.git`, whose `origin` is
     `--instance-repo`. It fetches them once for the whole pass. It requires that `main` carry
     `policy/instance` and no `policy/valley`;
  4. reads the instance's `policy/web` at that `main`;
  5. runs `valley plan --all --json` and `valley pending --all --json` in the pointer repository,
     `$XDG_CACHE_HOME/valley-web-publish/_pointer.git`, so the CLI discovers the projects through
     `refs/the-valley/projects` and the signers through `refs/the-valley/signers`.
     `VALLEY_KNOWN_SIGNERS` and `VALLEY_INTEGRATOR_SIGNER` are removed from the CLI's environment.
     Each listing must name exactly the projects the fetched `refs/the-valley/projects` lists;
  6. for each project the instance allows, fetches `main` from the URL the pending listing gives
     into a mirror, `$XDG_CACHE_HOME/valley-web-publish/repos/<project>.git`, and reads the
     project's own `policy/web` at that `main`;
  7. runs `valley knot show --project <project> <id> --json` in the publisher's own bare repository,
     for every knot of every public project;
  8. builds the export with `export.Build`, and checks the state and the caches against
     `--max-storage` again;
  9. makes the bundles, each within what `--max-bundles` and `--max-storage` leave;
  10. uploads the export, or writes it to `--out`.
- **One instance:** The pass reads the instance repository's `main` once, at step 3. No command of
  the pass reads it again:
  - The pointer repository is a bare repository whose `origin` is itself. Its `main` holds one file,
    `policy/valley`, which names `--instance-repo`. The CLI finds the instance at `policy/valley` on
    its repository's `origin` `main`, and follows it no further. So `plan` and `pending` read the
    projects and signers that `--instance-repo` publishes.
  - `valley knot show --project` finds a project beside its repository's `origin`, the instance
    repository, and reads no `policy/valley`.

  So a `policy/valley` that lands on the instance repository's `main` during a pass is never
  followed, even when it is gone again before the pass ends. A `main` that carries `policy/valley`
  at step 3 fails the pass, because it names another instance. Two instances can serve projects of
  the same name, and the pass reads only the one given.

- **Visibility:** A project is public when the instance's declaration holds
  `instance: projects: <project>: visibility: "public"` and the project's own declaration holds
  `project: visibility: "public"`. Every other project is private: one the instance does not name or
  does not allow, one with no `main`, one with no declaration, and one whose declaration
  `schema/web.cue` refuses. A refused declaration is said on `stderr`. A missing or refused instance
  declaration makes every project private, and the pass uploads an export with no project.
- **Read failures:** A failure to read is not a private project. The pass fails, and sends and
  writes nothing, when the instance repository or its project list cannot be fetched, when a listing
  names other projects than the instance publishes, as the CLI's listing of its own repository alone
  does when discovery fails, or when the schema does not load. It fails the same way for a project
  the instance allows when the pending listing could not read it or gives no URL for it, when its
  `main` cannot be fetched, or when git or `cue` cannot be run over its declaration. A `cue` run
  that does not finish fails the pass, over the instance's declaration or a project's: one that
  cannot be started, is killed, exits with any status but 1, or runs longer than a minute.
  `export.Build` refuses a public project that the plan could not read. An export without such a
  project would take it off the web.
- **Reading a declaration:** The pass reads the `*.cue` files directly in `policy/web/` of the
  commit, as regular files only, with `git ls-tree` and `git cat-file`, never a checkout. It copies
  them into a scratch directory with no `cue.mod`, so `cue` resolves no import beyond its standard
  library, and runs `cue export --out json` with the schema and the files. `CUE_*` variables are
  removed from `cue`'s environment. A declaration is refused when `cue` exits with status 1, which
  is how `cue` reports an error in what it was given. A declaration with more than 32 files, a file
  over 64 KiB, more than 1 MiB of exported JSON, or exported JSON with a field the schema does not
  define is refused too. Before it reads any declaration, the pass runs `cue export` over the schema
  alone, and fails when that fails.
- **Output bounds:** The pass holds at most 64 MiB of what a `valley` command prints, 4 MiB of what
  a `git` command prints, and 1 MiB of what `cue` prints. A command that prints more fails the pass,
  except `cue`, whose declaration is then refused. What a command prints on `stderr` is kept to its
  first 64 KiB.
- **Consistent reads:** `export.Build` fails with `export.ErrInconsistent` when a knot's detail was
  read at another graph tip than the plan. The pass then reads the valley again, from step 4 with
  the instance step 3 fetched, up to three reads in all, and fails when every read was inconsistent.
- **Bundles:** Each public project's bundle is what `git bundle create - refs/heads/main` prints in
  its mirror. The pass writes it to a temporary file, and keeps it as
  `$XDG_CACHE_HOME/valley-web-publish/bundles/<project>/<source-tip>.bundle`. A bundle is made once
  per source tip, so the body a manifest declares for a tip is the same in every pass. The pass
  checks with `git bundle list-heads` that the bundle carries `refs/heads/main` at the source tip.
  Before it makes any bundle, it deletes the bundles of a project's earlier tips, the bundles of
  projects the export does not hold, and any partial bundle an earlier pass left.
- **Size bounds:** A bundle carries the whole history of `main`, as the export requires.
  `--max-bundles` bounds the bundles of one export together. `--max-storage` bounds the state
  directory, `$XDG_CACHE_HOME/valley-web-publish`, and the CLI's `$XDG_CACHE_HOME/valley` together.
  - Every pass checks `--max-storage` twice: before it fetches anything, and once it has read the
    valley. The second check runs even when the pass then sends nothing for an acknowledged digest.
  - The pass writes each bundle only into the room that both bounds leave. When a bundle is larger,
    the pass stops writing at that room. It deletes what it wrote of that bundle and every bundle it
    made in the pass, and fails.
  - The publisher’s repository caches and the CLI’s caches borrow objects from absolute local
    repository paths through Git alternates. They copy no history. Before replacing each cache, the
    publisher checks that its complete metadata fits alongside the existing storage. It checks
    storage again before installing the replacement and removes partial replacements on failure.
  - The pointer repository is built in memory and staged within the same storage budget. Collection
    reserves room for the CLI’s ref metadata before each command and checks storage afterward.
  - Before collection, the publisher removes CLI caches outside the current project list. During
    each CLI command, the cache parent is read-only. Existing caches remain usable, but discovery
    and foreign knot references cannot create unstaged caches. The next pass restores write access
    before staging, including after an interrupted command.

  A pass that fails on a bound sends and writes nothing.

- **Skipping an unchanged export:** The pass computes `Export.Digest`. It sends nothing when the
  last acknowledgement names the same receiver, destination, and digest, and is less than
  `--recheck` old. Otherwise it posts a manifest. When the inventory's `active.digest` is the
  export's digest and `needs` names no bundle, the pass stops after step 1 of the upload. Either
  way, the acknowledgement records what the receiver replied: the inventory's active sequence, or
  the committed sequence. An upload that was attempted and refused records nothing. A receiver that
  lost its state is sent the whole export at the first pass after the acknowledgement passes
  `--recheck`.
- **Sequence:** A manifest's sequence is the clock in Unix nanoseconds when it is signed. It must be
  greater than the greatest sequence the receiver is known to have accepted: the acknowledged one,
  or the `high_water` of a `replayed` refusal. When the clock is behind that mark by at most two
  minutes, the pass waits for the clock to pass it. Otherwise the pass fails, and the next pass
  tries again.
- **Retries:** A pass makes at most five upload attempts.

  | Reply                                                           | What the pass does                                                                     |
  | :-------------------------------------------------------------- | :------------------------------------------------------------------------------------- |
  | A failure in transit, a status with no refusal, or `unreadable` | Pauses, 2 seconds and then twice as long each time, and posts the same manifest again. |
  | `incomplete`                                                    | Posts the same manifest again and sends what the inventory still needs.                |
  | `replayed`                                                      | Signs a new manifest once the clock has passed `high_water`.                           |
  | `superseded`, `no-manifest`                                     | Pauses, and signs a new manifest.                                                      |
  | `out-of-window`                                                 | Fails the pass, naming the signing time, without a retry.                              |
  | Any other refusal                                               | Fails the pass without a retry.                                                        |

- **Requests:** A body is sent with its declared size as `Content-Length` and no `Content-Encoding`.
  The client follows no redirect. A manifest post may take 2 minutes, a body or a commit 30 minutes.
  A reply over 1 MiB is a failure.
- **`--out`:** The pass writes the upload's content to `<dir>`: `manifest` (the signed manifest),
  `export.json`, and `bundles/<project>.bundle`. It writes them in a temporary directory beside
  `<dir>` and renames it into place, so a reader never sees part of it. A pass with `--out` signs
  with the same key and does not read or write the acknowledgement.
- **State directory:**

  ```text
  <state>/lock           flock(2) lock held for the whole of a pass
  <state>/key            the export key: Ed25519, PKCS #8 in PEM, mode 0400
  <state>/verifier       the key's verifier line, <instance>/web-export+<hash>+<base64>
  <state>/acknowledged   valley-web-publish-acknowledged/v1: receiver, destination, digest,
                         sequence, and when the receiver replied
  ```

  The key is made under a temporary name and renamed into place, so a start that is killed leaves no
  key, and the next start makes one. Every pass compares the key's verifier line with
  `<state>/verifier`. When the line differs, for a new key or a new `--instance`, the pass writes it
  there and prints `valley-web-publish: the export key's verifier line is <line>`. The receiver pins
  that line. The private key is never printed.

#### Web export receiver state

The receiver keeps every accepted export as an immutable generation in one state directory
(`export.OpenStore`):

```text
<state>/lock                   flock(2) lock serialising Stage, Activate, and OpenStore
<state>/high-water             the greatest sequence accepted
<state>/active                 symlink to exports/<sequence>
<state>/exports/<sequence>/    one generation: export.json and repos/<project>.git
<state>/staging/<sequence>/    one upload until it is committed:
    bodies/<sha256>            verified bodies written by StageBody
    export.json                moved from bodies/ during Activate
    tmp/<project>.git          a repository being built
    repos/<project>.git        a repository once accepted and synced
<state>/trash/                 generations renamed away before deletion
```

- **Activation (`Store.Activate`):** Holding an exclusive `flock(2)` on `<state>/lock`, `Activate`
  runs these steps in order:
  1. Refuse the upload if its `sequence` is not greater than `high-water` (`replayed`), if a newer
     manifest is staged (`superseded`), or if any needed body is missing (`incomplete`, with
     `needs`).
  2. Move `export.json` from `bodies/<sha256>` to `<staging>/<sequence>/export.json`, set its mode
     to `0644`, and check it against the manifest with `Manifest.CheckExport`. If `CheckExport`
     fails, remove `export.json` from staging so `Inventory` lists it as needed again.
  3. Build `repos/<project>.git` for each project not already in `repos/`. It removes any leftover
     `tmp/<project>.git`, then builds the repository in `tmp/<project>.git` from the staged bundle
     with `AcceptBundle` or from the active generation's repository at the same `source_tip` with
     `CarryRepo`. It sets the file and directory modes in `tmp/<project>.git` (see **Modes** below),
     syncs every file and directory to disk, and renames the repository to `repos/<project>.git`. If
     `AcceptBundle` fails, the staged bundle is removed from `bodies/` so `Inventory` lists it as
     needed again. A build interrupted by a crash stays in `tmp/` and is rebuilt on the next commit.
  4. Remove `bodies/` and `tmp/`, and verify that the staging directory holds only `export.json` and
     `repos/`, with each project's repository holding `refs/heads/main` at its `source_tip`.
  5. Set the mode of `repos/` and the staging directory to `0755`. Sync `export.json`, `repos/`, and
     the staging directory, rename `<staging>/<sequence>` to `exports/<sequence>`, and sync
     `exports/` and `staging/`.
  6. Write `<sequence>\n` durably to `high-water`.
  7. Swap `active` by renaming a new symlink (`exports/<sequence>`) over it.
  8. Sync `<state>` and sweep inactive generations that no reader holds, staging directories with
     `sequence <= high-water`, and `trash/`.
- **Durability:** Every file and directory of a generation is synced to disk before `high-water`
  names its sequence. Git's `core.fsync=all` syncs what `git fetch` writes, but not the files
  `git init` creates beforehand, so `Activate` recursively syncs each repository before renaming it
  into `repos/`.
- **Modes:** The store sets every file and directory mode explicitly, independent of the receiver's
  umask (see [Reading the state directory](./architecture.md#reading-the-state-directory)).
  - `staging/` and each upload directory inside it have mode `0700`, and each staged body has mode
    `0600`.
  - `exports/` has mode `0755`. Inside each generation, every directory has mode `0755` and every
    file has mode `0644`, or `0444` when git wrote the file read-only.
  - The state directory's own mode is a `deployment choice`: `OpenStore` creates the directory if
    needed and leaves its mode unchanged.
- **Reading (`Store.Open`):** A reader calls `Store.Open` once per request. `Open` resolves `active`
  and holds a shared `flock(2)` (`LOCK_SH`) on `exports/<sequence>` until the caller releases it
  (`export.ErrNoExport` if no export has been activated yet). Because a request serves everything
  from that single opened generation, a concurrent activation never mixes two generations in one
  response. An old generation is deleted only when a sweep can acquire a non-blocking exclusive
  `flock(2)` (`LOCK_EX | LOCK_NB`) on its directory, which fails while any reader holds it. Under
  that exclusive lock, the sweep renames the generation into `trash/` before removing its files.
- **Recovery (`export.OpenStore`):** When `OpenStore` opens `<state>`, it locks `<state>/lock`,
  recovers any interrupted activation, and sweeps leftover directories:
  - A crash before `high-water` is written leaves the previous generation at `active`, and the next
    `OpenStore` deletes the unactivated generation and any staging directory with
    `sequence <= high-water`.
  - A crash after `high-water` is written and before `active` is swapped is finished by the next
    `OpenStore`. Before swapping `active` to `exports/<high-water>`, `OpenStore` checks that
    generation: `export.json` must pass `export.Decode`, and each project's repository must pass
    `git fsck` and hold `refs/heads/main` at its `source_tip` and no other ref. If the check passes,
    `OpenStore` swaps `active` to that generation and syncs `<state>`.
  - If the generation fails the check, `OpenStore` passes the error to `report` and does not
    activate the generation. The previous generation keeps serving, the sweep deletes the damaged
    generation, and the publisher's next manifest replaces it.
  - A repository fails `git fsck` only when `git` exits on its own with a non-zero status and its
    output names a problem (`error: `, `missing `, `broken link from `,
    `fatal: not a git repository`, or `fatal: ... is corrupt`). Any other `git fsck` failure means
    the check did not finish: `git` was killed by a signal, failed to start, or exited without
    naming a problem (as when it runs out of memory). `OpenStore` then returns the error without
    running the sweep and deletes nothing, so the receiver can try again.
- **Fixture (`exporttest`):** Package `exporttest` builds a complete, valid export from a synthetic
  valley with a public project `app` and a hidden project `site`. It yields the export directory,
  the bundles, the signed manifest, and its verifier key, so tests for the publisher, the receiver,
  and the web server share the same bytes.

#### Web server (`valley-web`)

`valley-web` serves the active export in a receiver's state directory to anonymous readers over
HTTP. It serves the plan page, one page per knot, and a source browser over each public project's
`main`. Package `server` of the `webui` module ([`webui/server/`](../webui/server/)) implements the
HTTP handler, and [`webui/cmd/valley-web/`](../webui/cmd/valley-web/) is the command. The flake
builds it as the `valley-web` package.

```sh
valley-web --state /var/lib/valley-web --listen 127.0.0.1:8080
```

- **Flags:** Both flags are required.
  - `--state DIR` names the receiver's existing state directory. `valley-web` opens it read-only
    with `export.OpenReadOnly` (see
    [Reading the state directory](./architecture.md#reading-the-state-directory)).
  - `--listen ADDR` sets the TCP address to listen on. Port `0` picks a free port.
- **Running:** Once listening, `valley-web` prints `valley-web: listening on <addr>` on `stderr`. It
  logs runtime errors on `stderr`. On `SIGINT` or `SIGTERM`, it stops accepting new requests and
  gives in-flight requests up to 5 seconds to finish. It exits `0` after a clean stop and `1` on a
  runtime failure. On invalid flags, it prints usage on `stderr` and exits `2`. `--help` prints
  usage on `stderr` and exits `0`.
- **Routes:** The server answers `GET` and `HEAD`. Any other method answers `405` with
  `Allow: GET, HEAD`.

| Route                                       | Page                                                                       |
| :------------------------------------------ | :------------------------------------------------------------------------- |
| `/`                                         | The plan page                                                              |
| `/<project>/` and `/<project>`              | `302` redirect to the root tree at the project's `source_tip`              |
| `/<project>/knots/<id>`                     | One knot                                                                   |
| `/<project>/source/`                        | `302` redirect to the root tree at the project's `source_tip`              |
| `/<project>/source/tree/<commit>/<path>/`   | One directory and its README                                               |
| `/<project>/source/blob/<commit>/<path>`    | One file                                                                   |
| `/<project>/source/log/<commit>`            | Up to 50 commits of history ending at `<commit>`, newest first             |
| `/<project>/source/log/<commit>?from=<ids>` | The next older page of that history, starting from the commits in `<ids>`  |
| `/-/…`                                      | Reserved for the server's own routes. Every path under `/-/` answers `404` |

- **Reserved paths:** Every path other than `/` and `/-/…` starts with a project name. Project names
  match `^[a-zA-Z0-9][a-zA-Z0-9._-]*$`, which always starts with a letter or digit. A project may be
  named `source`, `knots`, `web-export`, or `robots.txt` and is served on every project route (see
  [Paths](./architecture.md#paths)).

- **One generation per request:** Each request opens the active generation once with `Store.Open`
  and renders its entire response from that generation. It releases the generation once rendering
  finishes, before writing the response. Activating a new export during a request does not change
  that request's response, and a sweep cannot delete the old generation until the request releases
  it. Before the first activation, every route answers `503`.
- **One read per generation:** The server reads and decodes each generation's `export.json` once,
  even when many requests wait for it at the same time. It caches the newest generation it has read
  and builds the plan page's structure once for that generation. A request opens its generation on
  disk only after the server has read that generation.
  - Each read runs in its own worker. The worker opens the active generation, holds it while reading
    `export.json`, and releases it when the read finishes. A request waiting for a read holds no
    generation.
  - At most 2 read workers run at once (`server.MaxLoads`). A worker starts only when a waiting
    request takes one of those 2 worker slots. The server never queues a read without a waiting
    request.
  - A request stops waiting for a read if its reader disconnects or its 15-second deadline runs out.
    When no request is left waiting for a read, the worker stops reading and the server discards the
    unfinished read.
- **Plan page:** The page shows four sections, in this order:
  1. **Waiting on you:** Always empty. Every reader sees the same public page.
  2. **In flight:** The export's `in_flight` knots, showing who claims each knot and how long ago.
     Beside them, a card titled "Ready changes — order not assessed" lists the export's `changes`.
     Each change shows its handle, its active version's title, whether an integration request
     approves it, and the integrator's last decision.
  3. **Paths:** One card per knot whose children wait on one another, grouping those children into
     numbered stages. A child's stage is one after the latest stage of any sibling in its
     `blocked_by`, whether or not that sibling is done. Children with no blocker order between them
     share a stage. Abandoned children are left out.
  4. **Milestones:** Each root knot with the tree of knots under it, ordering children at each level
     by state. Knots that have no parent and are not roots appear under "Unfiled". A tree shows at
     most 32 levels of knots (`server.MaxPlanDepth`). If a knot on the 32nd level has children, its
     row notes that there is more on its page. Each knot appears once in a project's trees. A
     milestone's count includes every knot under it, at any depth.

  A knot whose `state` is `ready` is labeled "free to start". The legend defines this as an open,
  unclaimed, unblocked outcome or bug. The page's single inline script runs the milestone filters.
  Without the script, every knot shows and the trees still open and close.

- **Freshness:** Every page shows the export's `collected` time and its age when the request is
  served. A page marks the export `stale` when it is more than five minutes old
  (`server.StaleAfter`).
- **Knot page:** A knot page shows the knot's type, status, state, and fields, the chain of parents
  above it, and links to the knots that block it, the knots it blocks, and its children. The parent
  chain names the nearest 32 parents (`server.MaxPlanDepth`) and marks any parents above them with
  `…`. The body and each comment render as escaped preformatted text. A comment with an `edited`
  timestamp is marked "edited" and shows its `edited` time and `edited_by`.
- **References into unpublished projects:** A page never shows the name of a project that is not
  public in the export, or the text of a reference into one. Every such reference is replaced with
  fixed text and no link:
  - A `blocked_by` entry, `target`, or `graduated_into` that names a knot in an unpublished project
    is shown as "a knot in an unpublished project".
  - A `target` that names only an unpublished project is shown as "an unpublished project".
  - A `changes` link `valley:<project>/<change ID>` into an unpublished project is shown as "a
    change in an unpublished project".
  - On the plan page, a tree row's "waits on" chip lists the published knots in the knot's
    `waiting_on` list. When the knot also waits on knots in unpublished projects
    (`waiting_on_hidden`), the chip ends with "a knot in an unpublished project" once, regardless of
    how many unpublished knots block it. A blocked knot with no published blocker shows "waits on a
    knot in an unpublished project" alone.

  A `blocked_by` entry that names a missing knot in a public project still shows its reference text.
  It is marked "not found in its graph" when its edge is `dangling`, and "not found" otherwise.

- **Source browser:** The server reads each generation's repositories by running `git` subprocesses
  through `export.Git` and `export.GitStream`. It passes `git` only verified object IDs, never text
  from the request.
  - A source route serves `<commit>` only when it is a full object ID with the same length as the
    project's `source_tip` and `export.Reachable` finds it reachable from `source_tip`. Anything
    else answers `404`, including an unreachable commit, a tree or blob ID, an abbreviated ID, a ref
    name such as `main`, and the first 40 hex digits of a SHA-256 commit.
  - The server resolves `<path>` from the commit's root tree one segment at a time with
    `git ls-tree` on tree object IDs, without `--long`. A path answers `404` if it is deeper than 64
    segments (`server.MaxDepth`) or if any segment is empty, `.`, `..`, or contains `/` or NUL. The
    directories along `<path>` share one 4 MiB budget of `git ls-tree` output (`server.MaxRead`),
    read up to the entry each segment names; a path whose entry lies past that 4 MiB budget answers
    `404`.
  - Every source link names the project, a full commit ID, and a path.
  - The server streams `git` output through `export.GitStream` and reads at most 4 MiB of a
    directory listing or history page (`server.MaxRead`). Once `git` prints more than 4 MiB, the
    server stops `git` and waits for it to exit.
  - A directory page lists at most 1,000 entries (`server.MaxEntries`), directories first. If a
    directory's `git ls-tree` output exceeds 4 MiB, the page lists only the entries from the first 4
    MiB and notes that the listing is partial. Below the listing, the page shows the directory's
    README: `README.md`, `README`, or `README.txt` in that order, or else the first file named
    `readme` in any case with any extension.
  - A file page shows the file as escaped text with numbered lines, each with an anchor `#L<n>`. It
    shows at most 10,000 lines (`server.MaxLines`). It stops at the line that would take the escaped
    text and row markup past 2 MiB (`server.MaxShown`). When the file is longer, the page says "The
    page stops at line `<n>` of `<total>`." A README shows at most 10,000 lines and 2 MiB of escaped
    text, and notes when it is shown in part.
  - A file larger than 1 MiB (`server.MaxFile`) is not read. A file that contains a NUL byte or is
    not valid UTF-8 is not shown, and the page says it is binary. A symbolic link shows its target
    path and is not followed. A submodule is listed with its commit ID and has no link.
  - A log page shows the first 1 KiB of each commit's subject and author name. If a page's commits
    exceed 4 MiB of `git log` output, the page ends at the commit cut off by the 4 MiB bound,
    showing as much of that commit as was read. The page notes that it ends early and links to the
    older commits after it.
  - A log page lists commits in `git log --date-order`, which always places a child before its
    parents. Each page asks `git` for 50 commits, regardless of how far back in history the page
    starts. Its "Older" link points to the same route with `?from=` naming the frontier of remaining
    commits: the parents of the commits shown on the page, plus the commits the page started from,
    minus the commits shown on the page (see
    [A source request's git work](./architecture.md#a-source-requests-git-work)). While every page
    leaves at most 64 commits in that frontier, following "Older" from the first page visits every
    reachable commit once, including across merges.
  - The `from` query parameter takes a single value of 1 to 64 full commit IDs
    (`server.MaxFrontier`), separated by commas, in ascending order, each appearing once. Each
    commit in `from` must be reachable from `<commit>`. Any other `from` value answers `404`. Every
    page after the first includes a "Newest" link back to the first page of that history.
  - When a page leaves more than 64 commits to show, its "Older" link starts from the first 64 in
    ascending order. Below the commit list, the page notes how many lines of history continue and
    links each remaining commit by its short ID to `?from=<id>` for that single commit. Together,
    "Older" and those individual links reach every older commit along the parents the page follows,
    though two of those branches may later visit the same ancestor.
  - A page leaves at most 192 commits to show (`server.MaxLeftToShow`), so it renders at most 128
    individual branch links in addition to "Older". Before adding a merge commit that would push the
    frontier past 192 commits, the page stops early and notes why. Every page shows at least its
    first commit, which leaves at most 63 other starting commits plus 128 parents.
  - A page follows at most the first 128 parents of any commit (`server.MaxParents`). A merge with
    more than 128 parents still renders, with a note on that commit stating that older pages follow
    only its first 128 parents; commits reachable only through later parents are omitted from older
    pages.
- **Time:** Every request has a 15-second deadline from when it arrives, covering waiting, running
  `git` commands, and rendering the page. A request that exceeds 15 seconds answers `503` with
  `Retry-After: 5`. Within that deadline, a source request may hold its `git` slot for at most 5
  seconds, covering its wait for the generation's export to load and all of its `git` commands. If 5
  seconds pass while the request still holds its `git` slot, the server logs the timeout and answers
  `503` with `Retry-After: 5`.
- **Admission:** The server bounds how many requests may run `git` or wait at once. Every admission
  check runs before the request opens a generation.
  - A tree, file, or log request needs a `git` slot. At most 8 such requests hold a `git` slot at
    once (`server.MaxGit`), and at most 16 more wait for a slot to open (`server.MaxWaiting`). When
    all 8 slots and 16 waiting places are full, a new source request answers `503` immediately with
    `Retry-After: 5`.
  - A source request releases its `git` slot before rendering HTML, and every request releases its
    generation before writing the response to the client (see
    [Slow readers](./architecture.md#slow-readers)).
  - Any request whose generation the server has not read yet must wait for the read before opening a
    generation. At most 64 requests wait for export reads at once (`server.MaxLoadWaiting`). A
    request that finds all 64 places full answers `503` immediately with `Retry-After: 5`.
- **Response size and memory:** Every page is rendered in full before the server writes the
  response.
  - A page is at most 8 MiB (`server.MaxResponse`) on any route. A page that would exceed 8 MiB
    stops rendering at that bound. The server logs the failure and answers `500` with a short page
    titled "Page too large".
  - The server holds at most 64 MiB of rendered responses at once (`server.MaxBuffered`). A response
    claims this budget in 64 KiB chunks as it renders and releases them once the response has been
    written. A slow reader holds its response's share of the budget until the write finishes.
    `valley-web` stops writing a response 60 seconds after reading the request's headers.
  - Responses larger than 256 KiB (`server.SmallPage`) may hold at most 48 MiB of the 64 MiB budget
    between them (`server.MaxLarge`). Once a response grows past 256 KiB, its entire size—including
    the first 256 KiB—counts against that 48 MiB cap, leaving at least 16 MiB of the budget for
    responses of 256 KiB or smaller (see [Slow readers](./architecture.md#slow-readers)).
  - A request whose page needs more memory than remains in the budget answers `503` with
    `Retry-After: 5`.
  - The "Busy" (`503`) and "Page too large" (`500`) pages are rendered once when the server starts.
    They show no export data and take none of the 64 MiB budget.
- **Access:** Every route that names a project checks that the export holds the project with
  visibility `public`. Every `404` serves the same page, whether the project is hidden or missing,
  the knot or path does not exist, or the commit is not served.
- **Rendering and headers:** Pages are rendered on the server with Go's `html/template`, which
  escapes every value from the export or a repository. The stylesheet and the plan page's script are
  embedded in the binary and inlined into the page; nothing else is loaded. No route serves a
  repository file as raw content. Every response carries these headers:
  - `Content-Security-Policy: default-src 'self'; script-src 'sha256-<script hash>'; style-src 'sha256-<stylesheet hash>'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`
  - `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
    `Cross-Origin-Opener-Policy: same-origin`, `Cross-Origin-Resource-Policy: same-origin`, and
    `Cache-Control: no-cache`.
- **Errors:** When the server cannot read the export or a repository, it logs the error on `stderr`
  and answers `500` with a generic error page that gives no detail. A request that runs out of time
  answers `503`. The timeout is logged when time runs out while the request waits for its export to
  be read, runs `git`, or renders the page. The server does not log a request refused at admission,
  a request whose time runs out while waiting for a `git` slot, or a request refused because the 64
  MiB response budget is full.

---

## CUE configuration schemas

Seven CUE schemas under `schema/` validate host declarations, verification policies, web visibility
declarations, attestation statements, identity registries, knowledge/plan nodes, and bus events.
What an instance writes in its CUE files is a `deployment choice`; the schema constraints below are
`implemented`.

### Host configuration (`schema/valley.cue`)

[`schema/valley.cue`](../schema/valley.cue) (`package valley`) defines `#Host`, the declaration of
projects, ref protection, ref grants, mirrors, and offsite backup for a valley host.

```sh
cue vet -c schema/valley.cue valley.cue
```

| Definition / field                             | Type / default                                 | Rules                                                                                                                                                                                                                                 |
| :--------------------------------------------- | :--------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `projects.<name>` (`#Project`)                 | `[=~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"]: #Project` | Top-level map of hosted projects. Only `projects` and `backup` are permitted at top level.                                                                                                                                            |
| `projects.<name>.git.enable`                   | `bool \| *true`                                | Whether the host serves `<name>.git`. Disabling leaves existing data on disk untouched.                                                                                                                                               |
| `projects.<name>.mirrors`                      | `[...string] \| *[]`                           | Push-mirror URLs receiving `refs/heads/main` and `refs/tags/*` on updates.                                                                                                                                                            |
| `projects.<name>.protection` (`#Protection`)   | optional                                       | When omitted, no refs in `<name>` are protected. When present: `refs` (`[#RefPattern, ...#RefPattern] \| *["refs/heads/main"]`, where `#RefPattern` is `=~"^refs/[^ ~^:?\\\\[]+$"`) and `writers` (`[...#PrincipalName] \| *[]`).     |
| `projects.<name>.grants.<grant>` (`#RefGrant`) | `[#GrantName]: #RefGrant`                      | Opens ref patterns outside `refs/heads/`, `refs/replace/`, `refs/notes/`, and `refs/the-valley/` (`#GrantPattern`: `#RefPattern & !~"^refs/(heads\|replace\|notes\|the-valley)/"`) to `writers: [#PrincipalName, ...#PrincipalName]`. |
| `backup` (`#Backup`)                           | optional                                       | Host-level backup policy: `enable` (`bool \| *true`), `target` (`"restic-sftp"`), `cadence` (`"nightly"`), `retention` (`{ daily: int & >0 \| *7, weekly: int & >0 \| *4, monthly: int & >0 \| *6 }`).                                |

### Verification policy (`schema/verification.cue`)

[`schema/verification.cue`](../schema/verification.cue) (`package verification`) defines `#Policy`
and composes the instance `floor`, optional project-type `templates`, and the `project` layer into
`policy: floor & templates[type] & project`.

```sh
cue vet -c schema/verification.cue policy/instance/*.cue policy/project/*.cue
```

| Definition / field                     | Type / default                                    | Rules                                                                                                                                                                                                                                                                                                                                                                                                       |
| :------------------------------------- | :------------------------------------------------ | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Top-level fields                       | `floor`, `templates`, `type`, `project`, `policy` | `floor: #Policy`, `templates: [#Name]: #Policy` (with `templates: none: {}` built in), `type: #Name \| *"none"`, `project: #Policy`, and derived `policy: floor & templates[type] & project` (`#Name` is `=~"^[a-z0-9][a-z0-9-]*$"`).                                                                                                                                                                       |
| `#Policy.checks.<name>` (`#Check`)     | `[#Name]: #Check`                                 | Check catalogue. Every check required by any class or `unclassified` in the composed `policy` must be defined in `policy.checks`.                                                                                                                                                                                                                                                                           |
| `#Check`                               | `runner: "nix" \| "command"`                      | When `runner == "nix"`: requires `attribute: #Name`, forbids `command` and `validity`. When `runner == "command"`: requires `command: =~"^[^\\x00-\\x1f\\x7f]+$"`, forbids `attribute`, and accepts optional `validity?: =~"^[1-9][0-9]*(s\|m\|h\|d)$"` (`#Duration`). Both accept `cost: *"quick" \| "slow"`.                                                                                              |
| `#Policy.classes.<class>` (`#Class`)   | `[#Name]: #Class`                                 | Path classes: `paths` (`[#Pattern]: bool`, where `#Pattern` is relative to repo root with no leading `/`, `..` segment, or control characters), `requires` (`[#Name]: bool`), and `carry` (`["same-class" \| "exact"]: bool`, resolving to strictest of `exact`, `same-class`, or default `same-paths`). Concrete `true` in `floor` is mandatory; `bool \| *true` in `templates` is an overridable default. |
| `#Policy.unclassified`                 | `[#Name]: bool` (`#Requires`)                     | Checks required when a changed path matches no class in `policy.classes`.                                                                                                                                                                                                                                                                                                                                   |
| `#Policy.restack.maxAutomaticVersions` | `int & >=1 \| *64`                                | Cap on automatic restack versions in the policy schema (read by `valley-restacker once --report-only`, while automatic restack writes are `not built`).                                                                                                                                                                                                                                                     |
| `#Policy.approval.hardware`            | optional `bool`                                   | Whether an approval must be signed with a hardware key. Left out, it is not required. See [Requiring a hardware approval key](#requiring-a-hardware-approval-key-approvalhardware).                                                                                                                                                                                                                         |

#### Requiring a hardware approval key (`approval.hardware`)

The floor writes this to require that approvals be signed with a hardware key:

```cue
floor: approval: hardware: true
```

A hardware key is an approval key that the identity registry enrolled with proof that the key is
held on a device and that every signature takes a touch. The identity compiler lists those keys in
`approval-signers-hardware` ([Approval signers files](#approval-signers-files)).

- **What it decides.** It decides which keys may sign an approval. It does not decide whether a
  change needs an approval.
- **Left out.** Any enrolled approval key may sign. A policy that leaves the field out exports no
  `approval` field, so the field adds nothing to its policy digest.
- **The floor** writes `true`, and no project can remove it. A project that writes `hardware: false`
  makes the policy fail to compose, and `cue vet` names `policy.approval.hardware`. A project cannot
  edit the floor either: the integrator reads the floor from the instance repository or a host
  directory, never from the project's tree ([Policy](./architecture.md#policy)).
- **A template** may write `bool | *true`. That is a default, and a project may turn it off with
  `false`.
- **A project** may write `true`, which requires a hardware key even where the floor does not.
- **A floor's `false`** would stop every project from writing `true`. A floor writes `true` or
  leaves the field out.
- **Unknown fields and other types** are refused: `approval` holds only `hardware`, and `hardware`
  is a boolean. A document may reopen a schema definition to add a field, such as `Hardware` beside
  `hardware`. The Go reader refuses any composed policy with a field the schema does not give it, or
  a value of the wrong type.

The schema rule and its Go reader, `HardwareApproval` in
[`composed/composed.go`](../composed/composed.go), are `implemented`. Nothing acts on the value.
Enrolling hardware keys in the identity registry is `implemented`: the compiler verifies each key's
attestation and writes `approval-signers-hardware`. The approval gate that would check approvals
against that file alone is `not built`. Whether a floor writes the field is a `deployment choice`.

The schema travels with the tools: `valley`, the integrator, the check worker and the restacker each
compose under their own packaged copy. A tool with an older schema refuses a policy that writes
`approval`, and derives no checks for it. Deploy the tools before a policy writes the field.

### Web visibility (`schema/web.cue`)

[`schema/web.cue`](../schema/web.cue) (`package web`) defines the declaration a repository keeps in
`policy/web/*.cue` on its `main`. The web publisher reads it to decide which projects an export
holds.

```cue
// policy/web/web.cue in the instance repository
package web

instance: projects: {
	app: visibility:  "public"
	site: visibility: "private"
}
```

```cue
// policy/web/web.cue in app
package web

project: visibility: "public"
```

```sh
cue vet -c schema/web.cue policy/web/*.cue
```

| Field                                    | Type                    | Rules                                                                                                                           |
| :--------------------------------------- | :---------------------- | :------------------------------------------------------------------------------------------------------------------------------ |
| `project.visibility` (`#Declaration`)    | `"public" \| "private"` | Optional. What the repository declares of itself. `project` holds no other field.                                               |
| `instance.projects.<name>` (`#Instance`) | `#Declaration`          | Optional. The projects the instance lets the web read. `<name>` follows the project-name rule (`#Name`, at most 64 characters). |
| Top-level fields                         | `project`, `instance`   | Any other top-level field conflicts with a sentinel string and fails vet, naming the field.                                     |

- **Two declarations:** A project is exported publicly only when the instance repository's
  `instance.projects.<name>.visibility` and the project's own `project.visibility` are both
  `"public"`. A declaration that is missing, or that the schema refuses, means private.
- **The instance repository:** The publisher reads `instance` from the instance repository alone,
  and ignores it in any other repository. The instance repository is a project too, so its
  `policy/web/` can carry both blocks.
- **Beside the verification policy:** `policy/web/` sits beside `policy/instance/` and
  `policy/project/`. Those two directories are verification-policy layers, whose top level
  `schema/verification.cue` closes to its own fields.
- **the-valley's own:** [`policy/web/web.cue`](../policy/web/web.cue) declares
  `project: visibility: "public"` (`deployment choice`).

### Attestation statement (`schema/attestation.cue`)

[`schema/attestation.cue`](../schema/attestation.cue) (`package attestation`) validates the JSON
representation of `the-valley/attestation/v1` statements before rendering or during `attest verify`.

```sh
cue vet -c schema/attestation.cue statement.json
```

- **Top-level fields:** `statementType: "the-valley/attestation/v1"`, `subject: #Subject`
  (`primary: "valley-tree-v1"`, `digest: #DigestSet`), `predicateType: #PredicateType`, `predicate`
  (`#PureCheck`, `#EffectfulCheck`, or `#Transfer` selected by `predicateType`), and optional
  `provenance?: #Provenance`.
- **Definitions:**
  - `#DigestSet`: `"valley-tree-v1"?`, `"valley-inputs-v1"?`, `sha256?` (`=~"^[0-9a-f]{64}$"`),
    `"git-sha1"?` (`=~"^[0-9a-f]{40}$"`), `"git-sha256"?` (`=~"^[0-9a-f]{64}$"`).
  - `#Line`: `=~"^[^\\x00-\\x1f\\x7f]+$"`; `#Name`: `=~"^[a-z0-9][a-z0-9-]*$"`; `#KeySegment`:
    `=~"^[A-Za-z][A-Za-z0-9-]*$"`.
  - `#CheckRun`: `name: #Name` and `runner: "nix" | "command"`. When `runner == "nix"`, requires
    `attribute: #Name` and forbids `command`. When `runner == "command"`, requires `command: #Line`
    and forbids `attribute`.
  - `#Result`: `"passed" | "failed"`; `#CheckTransfer`: `rule: "closure" | "untouched" | "unmoved"`.
  - `#Provenance`: optional `harness?: #Line`, `model?: #Line`, `prompt?: #DigestSet`,
    `context?: #DigestSet`, and `delegation?: [...{ principal: #Line, grant?: #Line }]`.

### Identity registry (`schema/identity.cue`)

[`schema/identity.cue`](../schema/identity.cue) (`package identity`) validates an instance's
governed identity registry (`identity/*.cue`), which `valley-identity compile` renders into
`known-signers`, `authorized_keys`, `grants`, and the two
[approval signers files](#approval-signers-files) ([`identity/render.go`](../identity/render.go)).

```sh
cue vet -c schema/identity.cue examples/identity/registry/*.cue
```

| Definition / field                 | Type / default         | Rules                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| :--------------------------------- | :--------------------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `boundaries.<name>` (`#Boundary`)  | `[#Name]: #Boundary`   | Enforcement boundaries run by the instance (`#Name`: `=~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"`). Every boundary has `kind: "git-push" \| "registry" \| "request" \| "restack"`. Every `boundary` referenced by a principal's grant must exist in `boundaries`.                                                                                                                                                                                                                                                                                                                                                                                       |
| `principals.<name>` (`#Principal`) | `[#Name]: #Principal`  | Named principals: `kind: "human" \| "machine" \| "service"`, `keys: [#Key, ...#Key]`, `grants: [#Name]: #Grant` (`{ boundary: #Name }`), `external: #External \| *null` (`{ kind: "domain" \| "did", handle: =~"^[^\\s]+$" }`), and `expires?: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`. `expires` is **required** whenever `kind != "human"` or `external != null`.                                                                                                                                                                                                                                                                                 |
| `#Key`                             | `class: "ssh-ed25519"` | `bound: "hardware" \| "host"`, `public: =~"^ssh-ed25519 [A-Za-z0-9+/]+={0,2}( [^\\n]*)?$"`, and optional `signs?: =~"^[^\\s+]+$"` (when set, `identity compile` emits a verifier key in `known-signers`), and optional `attestation?: #Attestation`. A key with an `attestation` must be `bound: "hardware"`.                                                                                                                                                                                                                                                                                                                                 |
| `#Attestation`                     | `slot`, `f9`           | A YubiKey PIV attestation of the key: `slot`, the certificate the YubiKey wrote for the PIV slot holding the key, and `f9`, the YubiKey's own attestation certificate, which signed it. Each is one PEM certificate and nothing else (`#Certificate`). `identity compile` checks what they say; see [Approval signers files](#approval-signers-files).                                                                                                                                                                                                                                                                                        |
| `genesis` and governance checks    | `genesis: #Name`       | `genesis` must be a key in `principals` that holds at least one grant at a boundary with `kind == "registry"` (`genesisGovernance: [_, ...]`), and no principal with `external != null` may hold a `registry` boundary grant (`externalGovernance`). At compile time, `identity compile` omits principals with `expires <= now` and fails if no unexpired principal holds a `registry` grant, if two principals holding `git-push` grants share a public key, if a `git-push` key conflicts with a host-declared `authorizedKeys` tag, if a key that approves is also listed under another principal, or if an `attestation` does not verify. |

#### Approval signers files

An approval signers file lists the keys whose signed approvals count. `identity compile` writes two
of them, each only when its flag names a path:

```sh
identity compile --repo /srv/git/instance.git --known-signers known-signers \
  --authorized-keys authorized_keys --grants grants \
  --approval-signers approval-signers --approval-signers-hardware approval-signers-hardware
```

Each line is a verifier key in the `known-signers` format, named by the principal who approves with
that key. For the example key of `example-operator`:

```text
example-operator+3649fd12+AT/3+vgQhY5cdQ4AqspQ1A2yW60nn4j1FOyx04WfuQjc
```

- **`approval-signers`** lists every key of every unexpired `human` principal holding the approve
  grant, at a boundary of kind `approve` or `request`. Software keys are listed too.
- **`approval-signers-hardware`** lists the hardware approval keys: the keys from `approval-signers`
  whose entry carries an `attestation` that verifies and says the touch policy is `always`. The
  YubiKey then asks for a touch before every signature.
- No gate reads either file, and the host module does not write them (`not built`).
- [`examples/identity/attested/`](../examples/identity/attested/registry.cue) is an example registry
  enrolling a YubiKey key for `example-operator`. Its keys are examples, and its attestation chains
  to a test-only root, "the-valley example attestation root, not Yubico". The compiler refuses it,
  so an entry copied from it compiles only with the enrolling YubiKey's own certificates.

An `attestation` verifies when all of these hold:

1. The `slot` certificate chains through the entry's `f9` certificate and Yubico's PIV intermediates
   to Yubico Attestation Root 1, on the day the compilation is for (`--now`, default today).
2. The `f9` certificate is the one that signed the `slot` certificate.
3. The key in the `slot` certificate is the entry's `public` key.
4. The `slot` certificate states a PIN and touch policy, in two bytes of Yubico's extension
   `1.3.6.1.4.1.41482.3.8`. The first byte is the PIN policy: 1 never, 2 once per session, 3 always.
   The second byte is the touch policy: 1 never, 2 always, 3 cached. These are the values Yubico
   specifies. An extension of any other length, or a byte with any other value, fails the
   compilation.

The compiler carries that root and those intermediates, in
[`identity/yubico/`](../identity/yubico/attestation-root-1.pem), and a registry cannot add others.
The root is Yubico's `yubico-ca-1.pem`, with the SHA-256 fingerprint Yubico publishes:

```text
62:76:0C:6A:6E:F9:16:79:F4:54:C8:90:2B:80:FD:00:98:25:B3:F2:5D:A9:0F:1F:BA:CE:2E:C6:58:6C:D5:A8
```

The compiler reports each attestation that verifies on stderr, with the YubiKey's serial number,
firmware, PIN policy and touch policy. Whoever reviews an enrollment checks them there. For a
YubiKey with serial number 12345678, enrolled as the first key of `example-operator`:

```text
identity: example-operator: key 0 is on YubiKey serial 12345678, firmware 5.7.4, PIN policy always, touch policy always; it approves as a hardware key
```

Who can do what, and what stops them:

- **Whoever can land a registry change can enroll a key and its attestation.** The registry's own
  governance decides who that is.
- **Only Yubico's chain makes a key a hardware approval key.** A registry entry cannot add a root or
  an intermediate. It needs an attestation that Yubico's chain signs, for that exact key, with touch
  policy `always`. `bound: "hardware"` without an attestation does not make a hardware approval key.
- **Evidence that does not verify stops the compilation.** An attestation naming another key,
  altered, missing its `f9` certificate, chaining to another root, or stating a policy Yubico does
  not specify fails the whole compilation, and the last good files stay as they were. The key is
  never compiled as a software key instead.
- **A cached or never touch policy is not hardware.** Such a key is in `approval-signers` and not in
  `approval-signers-hardware`. With a cached policy, one touch allows further signatures for 15
  seconds.
- **One approval key belongs to one principal.** A key that approves and is also listed under
  another unexpired principal fails the compilation. A line in either file therefore names the only
  principal holding its key.
- **Enrolling someone else's attested key gains nothing.** An approval still needs a signature from
  the device that holds the key.

Limits:

- The compiler trusts Yubico's root, and the YubiKey's firmware, to state the touch policy
  truthfully.
- An attestation chaining to Yubico's older PIV root, used before firmware 5.7.4, does not verify.
- The PIN policy is reported, and nothing requires one.
- The files hold keys, not serial numbers. The compile report and the attestation link a key to its
  device.

### Node frontmatter and bus events (`schema/node.cue`, `schema/events.cue`)

Two additional CUE schemas validate knot frontmatter and NATS bus payloads:

```sh
cue vet -c -d '#Node' schema/node.cue frontmatter.yaml
cue vet -d '#RefUpdated' schema/events.cue payload.json
```

- **[`schema/node.cue`](../schema/node.cue) (`package knowledge`):** Defines `#Node`, `#Graph`, and
  `#Directories`. The `knot-agree` flake check verifies that `#Node` and `knot/` enforce the same
  frontmatter rules (note that `#GraduationTarget` in `schema/node.cue` also admits `*.md` paths for
  tree-based test graphs, whereas the live plan graph in [`knot/graph.go`](../knot/graph.go)
  requires `graduated_into` to name a local `oc-*` or `qn-*` knot; historical nodes under
  `archive/.the-valley/` are frozen read-only history and are not checked against today's schema).
- **[`schema/events.cue`](../schema/events.cue) (`package valley`):** Defines the three NATS event
  payloads published under `valley.git.<repo>.<event>` when the optional host event bus is enabled
  (`services.valley.bus.enable`, a `deployment choice`; publication is best-effort and never blocks
  a push or landing):
  - `#RefUpdated`: `{ event: "ref-updated", repo, ref, old, new }` (published by the host's
    post-receive publisher hook, by the integrator on landing, and by `valley replay`).
  - `#IntegrationSucceeded`:
    `{ event: "integration-succeeded", repo, change, target, old, new, transferred: [...#CheckName] }`
    (published by the integrator when a request lands).
  - `#RequestStale`:
    `{ event: "request-stale", repo, change, target, tip, reason: "conflict" | "evidence", checks: [...#CheckName] }`
    (published by the integrator when a request cannot land because of a merge conflict or
    non-transferring check evidence).
