# Reference

This manual specifies the commands, environment variables, git refs, wire formats, JSON outputs, and
CUE schemas of the-valley. Like a valley where streams of work meet and settle into `main`, these
contracts define how clients and hosts exchange changes, evidence, and plans.

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
  signed notes, digest manifests, transfer statements, outcome records, knots, and CLI JSON
  contracts.
- [CUE configuration schemas](#cue-configuration-schemas) — `schema/valley.cue`,
  `schema/verification.cue`, `schema/attestation.cue`, `schema/identity.cue`, `schema/node.cue`, and
  `schema/events.cue`.

---

## CLI reference

`valley` is the command-line tool for authoring, reviewing, approving, rebasing, and inspecting
changes and the plan graph ([bin/valley:57-710](../bin/valley),
[status/main.go:46-128](../status/main.go)).

```sh
valley <verb> [args]
```

### Naming changes, handles, branches, and requests

Commands that inspect or act on an existing change (`status`, `review`, `approve`, `rebase`,
`unready`, and `withdraw`) accept a Change ID, a short handle, or a branch name, whereas
`valley ready [<branch>]` takes a branch name as its positional argument
([bin/valley:656-690](../bin/valley), [change/id.go:8-157](../change/id.go),
[status/changes.go:19-245](../status/changes.go), [status/ready.go:88-93](../status/ready.go)).

```sh
valley status kqxm                        # by handle
valley status kqxmnxvpwlkmqtsrzyoukklnpmqrstuv  # by 32-letter Change ID
valley status demo:kqxm                   # by <repo>:<handle>
valley status topic                       # by branch name
valley status origin/topic                # explicitly as a branch
```

- **Change ID (`[k-z]{32}`):** A 32-character lowercase identifier using letters `k` through `z`.
  `valley ready` generates a new Change ID from 16 random bytes (encoding each nibble `0`–`15` as
  `z` down to `k`; [change/id.go:63-90](../change/id.go)).
- **Handle (`[k-z]{4,32}`):** The shortest prefix of a Change ID (at least 4 characters) that no
  other change currently recorded on `origin` shares ([change/id.go:11](../change/id.go),
  [92-112](../change/id.go)). A handle can lengthen if a later change shares its prefix. In
  single-repository commands, `<repo>:<handle>` is also accepted when `<repo>` matches the current
  repository's name. In multi-repository listings (`--all`), a Change ID recorded in more than one
  listed repository is formatted as `<repo>:<prefix>` in each
  ([status/list.go:495-525](../status/list.go)).
- **Branch name:** Any ref name accepted by `git check-ref-format refs/heads/<name>`. No
  slash-separated segment may be empty, start with `.`, or end with `.lock`; the name may not end
  with `.`, contain `..` or `@{`, or contain ASCII control characters, space, or `~ ^ : ? * [ \`
  ([change/id.go:137-157](../change/id.go)). In change records (`record.json`), `branch` must also
  be valid UTF-8 ([change/record.go:226](../change/record.go)); in restack reports (`report.json`),
  branch names are additionally bounded to 255 bytes (`maxBranchLen = 255`;
  [change/report.go:123](../change/report.go), [334-340](../change/report.go)).
- **Resolution order:**
  - A name starting with `origin/` is always treated as the branch `<branch>`
    ([status/changes.go:97-100](../status/changes.go)).
  - A handle that is also a branch name on `origin` is refused as ambiguous unless the change's
    active version records that branch ([status/changes.go:123-126](../status/changes.go)).
  - When a branch name is given (with or without `origin/`), `review`, `approve`, `rebase`,
    `unready`, `withdraw`, and `status` check all records on `origin` whose active version has not
    landed (`landed` or `landed-recorded`). If exactly one unlanded change's active version records
    that branch, the command acts on that change at its recorded active version head. If more than
    one unlanded change's active version records that branch, the command refuses and lists the
    colliding changes ([status/changes.go:184-224](../status/changes.go),
    [status/ready.go:358-378](../status/ready.go)). If no unlanded change is on that branch,
    `status`, `review`, `approve`, and `rebase` act on the branch directly, whereas `unready` and
    `withdraw` refuse because they require a recorded change
    ([status/ready.go:277-285](../status/ready.go), [367-368](../status/ready.go)).
- **Request name (`<request>`):**
  - For a recorded change: `c-<change-id>` (such as `c-nxvpwlkmqtsrzyoukklnpmqrstuvwxyz`), stored at
    `refs/the-valley/integration-requests/main/c-<change-id>`
    ([change/id.go:22-30](../change/id.go)).
  - For an unrecorded branch: the branch name with every `/` replaced by `-`
    ([rebase/outcome.go:54-60](../rebase/outcome.go)). A branch whose request name would take the
    reserved `c-<change-id>` shape is refused by `valley approve` and `valley status`
    ([status/status.go:193-202](../status/status.go), [bin/valley:339-341](../bin/valley)).
- **Principal, project, and run names:** Must match `^[a-zA-Z0-9][a-zA-Z0-9._-]*$`
  ([change/record.go:299-306](../change/record.go), [schema/valley.cue:17](../schema/valley.cue),
  [149](../schema/valley.cue)). In the plan graph, principal, project, and run names are
  additionally bounded to 64 bytes (`MaxName = 64`; [knot/contract.go:58-59](../knot/contract.go),
  [119-121](../knot/contract.go)).

### Exit codes and git environment sanitization

Every `valley` command sanitizes git object-replacement environment variables before running git and
exits with a small set of status codes ([bin/valley:41-55](../bin/valley),
[status/main.go:67-128](../status/main.go), [152-164](../status/main.go)).

```sh
export GIT_NO_REPLACE_OBJECTS=1
unset GIT_REPLACE_REF_BASE GIT_GRAFT_FILE GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES
```

| Exit code | Meaning                                                                                                                                                                                                                                                                                                               |
| :-------- | :-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `0`       | Command succeeded (`self-update` also exits `0` when already up to date, when another run holds the lock, when pinned, or when the current branch tip already failed previously without `--retry`; [status/selfupdate.go:37-43](../status/selfupdate.go)).                                                            |
| `1`       | Command refused or failed (invalid state, failed check, rejected push, missing git config, runtime error, or invalid CLI flags in `status`, `ready`, `unready`, `withdraw`, `changes`, `pending`, `rebase`, `plan`, or `bin/valley`; [bin/valley:52-55](../bin/valley), [status/main.go:123-126](../status/main.go)). |
| `2`       | Invalid `valley knot` or `valley self-update` CLI usage, bad `VALLEY_NOW` timestamp in `knot` or `plan`, or unknown `valley-go` verb ([status/main.go:110-122](../status/main.go), [status/selfupdate.go:136-139](../status/selfupdate.go), [status/knot.go:214-219](../status/knot.go)).                             |
| `3`       | Plan graph compare-and-set or precondition conflict in `valley knot` (such as concurrent modification of a field without `--force`, a failed `--if <field>=<value>` check, or a claim held by another run; [status/knot.go:58-64](../status/knot.go), [220-230](../status/knot.go)).                                  |
| `130`     | Interrupted by `SIGINT` or `SIGTERM` during `valley review`, `valley approve`, `valley rebase`, or `valley self-update` ([bin/valley:2453](../bin/valley), [2509](../bin/valley), [status/rebase.go:23-26](../status/rebase.go), [status/selfupdate.go:150-154](../status/selfupdate.go)).                            |

### `valley ready`

`valley ready` creates or updates a change record on `origin`
(`refs/the-valley/changes/main/<change-id>`) and marks its active version `ready`
([status/ready.go:1-251](../status/ready.go)).

```sh
valley ready --stamp
valley ready [--head <sha>] [--change <id>] [--if-active <sha>] [--stamp] [<branch>]
```

- **Arguments and flags:**
  - `[<branch>]`: Branch name (with or without `origin/`). Defaults to the current checked-out
    branch (`git symbolic-ref --short HEAD`; [status/ready.go:88-93](../status/ready.go),
    [116-123](../status/ready.go)).
  - `--stamp`: For a new change whose local branch tip is not yet on `origin`, amends the local
    branch head with a `Change-Id: <id>` trailer, pushes the branch to `origin` with a lease on the
    remote branch state read at start, and creates `v1`. Refused if the change already has a record
    on `origin`, if the local head commit is already on `origin/<branch>`, or if combined with
    `--head` or `--if-active` ([status/ready.go:124-132](../status/ready.go),
    [148-151](../status/ready.go), [184-205](../status/ready.go)).
  - `--head <sha>`: Marks a specific commit `<sha>` ready instead of `origin/<branch>`. `<sha>` must
    already be an ancestor of `origin/<branch>` ([status/ready.go:152-159](../status/ready.go)).
  - `--change <id>`: Specifies the 32-letter Change ID explicitly. Required when the commit has
    neither a `jj` `change-id` commit header nor a `Change-Id:` trailer and should update an
    existing change ([status/ready.go:386-415](../status/ready.go)).
  - `--if-active <sha>`: Full commit object ID. Adds the new version only if the change is currently
    `ready` and its active version's head is `<sha>` ([status/ready.go:127-129](../status/ready.go),
    [215-221](../status/ready.go)).
- **Behavior:**
  - Requires `git config valley.principal` to be set to a valid principal name
    ([status/ready.go:112-115](../status/ready.go)).
  - Resolves the Change ID from `--change`, the commit's `change-id` header (`jj`), or the commit's
    `Change-Id:` message trailer, or generates a new ID. Refuses if a header, trailer, or `--change`
    disagree, or if multiple `Change-Id:` trailers carry different IDs
    ([status/ready.go:386-447](../status/ready.go)).
  - Refuses if the commit is already an ancestor of `origin/main`, has no merge base or multiple
    merge bases with `origin/main`, or includes a merge commit in `base..head`
    ([status/ready.go:167-182](../status/ready.go), [380-384](../status/ready.go)).
  - If the change has no record on `origin`, creates `v1` (`state: "ready"`, `seq: 1`, `active: 1`).
    If the change is `draft` or `withdrawn` and the target head equals the active version's head,
    transitions the record back to `ready` (`seq + 1`) without adding a version. If the change is
    already `ready` at the active version's head, refuses because the head has not changed.
    Otherwise, appends `v(N+1)` (`state: "ready"`, `seq + 1`, `active: N+1`;
    [status/ready.go:215-238](../status/ready.go), [rebase/record.go:25-27](../rebase/record.go)).
  - Pushes `refs/the-valley/changes/main/<id>` with `--force-with-lease` on the prior record commit
    and prints `<repo> <handle> v<n> ready` ([status/ready.go:240-250](../status/ready.go)).

### `valley unready`

`valley unready` pauses a `ready` change by setting its recorded state to `draft`
([status/ready.go:253-310](../status/ready.go)).

```sh
valley unready kqxm
valley unready <change>
```

- **Behavior:** Requires `git config valley.principal`. Reads the change record on `origin` (by
  Change ID, handle, or a branch that an unlanded change's active version is on); refuses and exits
  `1` if the change is already `draft` or `withdrawn`
  ([status/ready.go:287-293](../status/ready.go)). Otherwise writes a single-parent record commit
  (`P = [old]`) setting `state: "draft"` and incrementing `seq` by 1, leased on `old`. Prints a note
  to `stderr` if a pending integration request still exists on `origin` (an existing request is not
  deleted by `unready`, and the integrator refuses to land it while the change is `draft`, so it
  stands until the change is readied again; [status/ready.go:308](../status/ready.go),
  [323-356](../status/ready.go)).

### `valley withdraw`

`valley withdraw` marks a `ready` or `draft` change `withdrawn` so the integrator voids its pending
requests on its next pass ([status/ready.go:257-310](../status/ready.go)).

```sh
valley withdraw kqxm
valley withdraw <change>
```

- **Behavior:** Requires `git config valley.principal`. Reads the change record on `origin` (by
  Change ID, handle, or a branch that an unlanded change's active version is on); refuses and exits
  `1` if the change is already `withdrawn` ([status/ready.go:288-289](../status/ready.go)).
  Otherwise writes a single-parent record commit (`P = [old]`) setting `state: "withdrawn"` and
  incrementing `seq` by 1, leased on `old`. If a pending request exists on `origin`, warns on
  `stderr` that the integrator will delete it on its next pass while the change stays `withdrawn`
  (if the change is readied again before that pass, a holder of the `request` grant must delete the
  request ref directly; [status/ready.go:308](../status/ready.go), [339-353](../status/ready.go)).

### `valley pending`

`valley pending` lists work waiting to land in three sections: `ready` changes whose active version
has not landed, unrecorded `branches` ahead of `main`, and `drafts`
([status/pending.go:1-165](../status/pending.go)).

```sh
valley pending
valley pending [--all] [--repo <name>] [--checks] [--json] [-v]
```

- **Flags:**
  - `--all`: Queries every project listed at `refs/the-valley/projects` in the instance repository
    (discovered via `policy/valley` on `origin/main` or `policy/instance/` when the current repo is
    the instance repo), keeping clones under `$XDG_CACHE_HOME/valley/repos`. If discovery is
    unavailable and `--repo` is not set, warns on `stderr` and lists the current repository alone
    ([status/list.go:160-167](../status/list.go), [200-425](../status/list.go)).
  - `--repo <name>`: Queries that one project. When `<name>` matches the current repository, reads
    the local repository directly without discovery; when passed together with `--all`, `--repo`
    takes precedence ([status/list.go:156-173](../status/list.go)).
  - `--checks`: Appends a line to each change in human output showing how many checks the composed
    verification policy requires of it, grouped by `quick` and `slow` cost (ignored when combined
    with `--json`; [status/pending.go:121-129](../status/pending.go),
    [246-248](../status/pending.go), [571-604](../status/pending.go)).
  - `--json`: Emits [`valley-pending/v1`](#valley-changesv1-and-valley-pendingv1) JSON
    ([status/pending.go:96-105](../status/pending.go)).
  - `-v`: Warns on `stderr` when a project's plan graph (`refs/the-valley/knots`) cannot be read
    ([status/does.go:58-74](../status/does.go)).
- **Behavior:** Groups stacked entries under their top entry by commit ancestry, shows linked knots
  (`<title> · <knot-id>`), marks `includes <handle>` for unlanded non-entry changes (such as
  `withdrawn` changes) contained in a head, and marks `uncertain` when a version's landing cannot be
  verified. Also writes the shell-completion handle cache for the current repository at
  `<git-common-dir>/valley/handles` ([status/handles.go:60-92](../status/handles.go)).

### `valley changes`

`valley changes` outputs every change recorded on `origin` across all recorded states (`ready`,
`draft`, and `withdrawn`, whether submitted or unlanded) as a
[`valley-changes/v1`](#valley-changesv1-and-valley-pendingv1) JSON document
([status/list.go:44-188](../status/list.go), [661-685](../status/list.go)).

```sh
valley changes --json
valley changes --json [--all] [--repo <name>] [-v]
```

- **Flags:** `--json` (required; running `valley changes` without `--json` exits `1` and suggests
  `valley pending` for human output; [status/list.go:666-668](../status/list.go)), `--all`,
  `--repo <name>`, and `-v`.

### `valley status`

`valley status` inspects a single change or branch on `origin`, reporting its active version,
landing state, restack report, pending integration request, and last recorded integrator outcome
([status/status.go:33-120](../status/status.go), [status/render.go:15-100](../status/render.go)).

```sh
valley status kqxm
valley status [--json] [-v] <change>
```

- **Arguments and flags:**
  - `<change>`: Change ID, handle, or branch name.
  - `--json`: Emits a single-line [`valley-status/v1`](#valley-statusv1) JSON object including
    linked knots ([status/render.go:188-235](../status/render.go)).
  - `-v`: With `--json`, warns on `stderr` if the plan graph cannot be read
    ([status/status.go:59-80](../status/status.go)).
- **Behavior:** Fetches the needed objects from `origin` without updating `FETCH_HEAD` or moving any
  local branch ([status/status.go:321-330](../status/status.go)). Computes one of 13 status states
  (`integrated`, `integrated-recorded`, `outdated-request`, `outcome-unreadable`,
  `voided-withdrawn`, `not-requested`, `awaiting-decision`, `awaiting-new-decision`, `stale`,
  `rejected`, `refused-not-ready`, `refused-merge`, or `failed`;
  [status/status.go:384-423](../status/status.go)).

### `valley review`

`valley review` pages a change's diff (or a stack layer by layer from bottom to top), shows linked
knots and comparison against any prior approved version, and prompts to approve, rebase, view the
full diff, reject the remote branch, or skip ([bin/valley:144-318](../bin/valley),
[2430-2470](../bin/valley)).

```sh
valley review kqxm
valley review [-v] <change>
```

- **Arguments and flags:**
  - `<change>`: Change ID, handle, or branch name. The change's active version must record a
    non-empty `branch` ([bin/valley:1720-1729](../bin/valley)).
  - `-v`: Warns on `stderr` if the plan graph cannot be read ([bin/valley:2434](../bin/valley)).
- **Comparison against the approved version (`valley-go compare`):**
  - Before paging a recorded change, `valley review` identifies the change's **approved version**:
    the greatest version `vM` below the shown version `vN` whose exact `head` commit is named on
    `origin` by a pending integration request or a `land`, `stale`, or `reject` outcome record under
    `c-<change-id>` or an unambiguous branch request name
    ([status/compare.go:1-46](../status/compare.go), [131-215](../status/compare.go)). If the
    greatest version with evidence is ambiguous, no fallback to an older version occurs.
  - If an approved version is found and both versions have a single merge base (`B0` and `B1`) on
    `origin/main`'s first-parent history with `B0` equal to or an ancestor of `B1`,
    `valley-go compare` computes
    [`valley-delta-v1`](#canonical-digest-manifests-valley-tree-v1-valley-inputs-v1-valley-delta-v1)
    between each version's merge base and its head ([status/compare.go:30-35](../status/compare.go),
    [68-72](../status/compare.go), [240-310](../status/compare.go)):
    - `clean`: Both versions produce the identical `valley-delta-v1` file delta. `valley review`
      prints `<change> v<n> at <head>: clean restack of approved v<m> at <old head>.`, the two
      bases, and any commits `main` landed between them, and pages nothing
      ([bin/valley:976-1025](../bin/valley)).
    - `changed`: The deltas differ. `valley review` pages `changed since approved v<m>`, the paths
      whose delta entries differ, `git range-diff`, and the shown version's diff restricted to those
      changed paths ([bin/valley:1026-1059](../bin/valley)).
    - `none`: No approved version can be compared; `valley review` pages the full log and diff
      against `origin/main` (or the layer below in a stack).
- **Interactive prompt actions:**
  - `[a]pprove` (or `[a]pprove all <n>` at the top of a stack, or `[a]pprove <lands>` on a lower
    layer): Runs all required checks in a temporary worktree of the shown head, signs attestations
    with `VALLEY_ATTEST_KEY`, and pushes the attestations and
    `refs/the-valley/integration-requests/main/<request>` in one atomic, leased `git push`
    ([bin/valley:1395-1642](../bin/valley)). Not offered when the change (or any layer at or below
    it) is `draft` or `withdrawn`, or after its checks fail at that head.
  - `[b]ase` (or `[b]ase all <n>` before paging a stack whose bottom does not fast-forward from
    `origin/main`): Rebases the change or stack onto `origin/main` via `valley-go rebase`, then
    re-reads and re-pages the rebased commits ([bin/valley:2125-2155](../bin/valley),
    [2223-2263](../bin/valley)).
  - `[v]iew full diff`: Pages the full log and diff against `origin/main` (or the layer below)
    regardless of approved-version comparison ([bin/valley:2156-2164](../bin/valley),
    [2310-2323](../bin/valley)).
  - `[r]eject`: Confirms and deletes the remote branch `refs/heads/<branch>` on `origin`, leased on
    the recorded head (offered only on a single change or the ready top of a stack;
    [bin/valley:2084-2110](../bin/valley)).
  - `[n]ext layer`: In a stack review, advances to the next layer up
    ([bin/valley:2388-2394](../bin/valley)).
  - `[s]kip` / `[s]top`: Exits without modifying `origin`.
- **Inline review notes:** Pressing `v` inside `less` opens the diff buffer in `$EDITOR`. Any lines
  inserted by the reviewer are anchored to their file and new-side line number, appended to
  `$XDG_STATE_HOME/valley/notes/<branch>.md`, and printed at the end of the review
  ([bin/valley:245-255](../bin/valley), [729-975](../bin/valley)).

### `valley approve`

`valley approve` runs and signs all required checks for a change and files its integration request
in one atomic push without paging the diff ([bin/valley:319-355](../bin/valley),
[2483-2522](../bin/valley)).

```sh
valley approve kqxm
valley approve --head 99b5f2f7c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6 kqxm
valley approve [--head <sha>] <change>
```

- **Arguments and flags:**
  - `<change>`: Change ID, handle, or branch name.
  - `--head <sha>`: Approves that exact commit. For a recorded change, `<sha>` must be one of the
    record's version heads (active or prior); without `--head`, `approve` prompts on a terminal to
    confirm the active version's head and title, and refuses off a terminal
    ([bin/valley:1772-1825](../bin/valley)). For an unrecorded branch, `<sha>` (if given) must match
    `origin/<branch>`'s current tip ([bin/valley:2513-2518](../bin/valley)).
- **Behavior:**
  - Requires the pushing SSH key's principal to hold the `request` grant on the host (the `request`
    grant is the grant that lets a principal approve;
    [valleyhook/policy.go:151-158](../valleyhook/policy.go)).
  - Refuses if the change record is `draft` or `withdrawn`, if the commits include a merge, if the
    head brings an unlanded version of another change that is `draft` or `withdrawn`, or if the head
    does not fast-forward from `origin/main` while bringing an unlanded lower change in a stale
    stack (instructing the user to run `valley rebase <change>` first;
    [bin/valley:342-355](../bin/valley), [1395-1455](../bin/valley)).
  - Composes the verification policy from `origin/main` and the instance repository's
    `refs/heads/main`, derives the required checks for `base..head`, and when one or more checks are
    required, invokes `attest run` in a temporary worktree using `VALLEY_ATTEST_KEY` (and optional
    `VALLEY_ATTEST_NAME`; [bin/valley:1420-1530](../bin/valley)).
  - Pushes `refs/the-valley/attestations/<tree-digest>/<key-hash>` (when checks ran) and
    `refs/the-valley/integration-requests/main/<request>` (leased on the prior request value) in one
    `git push --atomic` ([bin/valley:1569-1582](../bin/valley)). Skipping checks that are already
    attested on `origin` is `not built` (re-attesting the same tree with the same signer is refused
    by the create-only attestation namespace; [bin/valley:174-177](../bin/valley),
    [1573-1576](../bin/valley)).

### `valley rebase`

`valley rebase` rebases a change and its entire underlying stack onto `origin/main`, pushes all
updated branches atomically, and records a new version on each rebased change
([status/rebase.go:1-190](../status/rebase.go), [bin/valley:356-395](../bin/valley)).

```sh
valley rebase kqxm
valley rebase <change>
```

- **Arguments:** `<change>` (Change ID, handle, or branch name). (`valley-go rebase` also accepts
  `--head <full-sha>` when invoked by `valley review`'s `[b]ase`;
  [status/rebase.go:45](../status/rebase.go), [105-123](../status/rebase.go)).
- **Behavior:**
  - Requires `git config valley.principal` whenever the stack contains a recorded change
    ([status/rebase.go:155-158](../status/rebase.go)).
  - Captures one snapshot of `origin` and builds the stack of unlanded changes and branches beneath
    `<change>`. Commits at the bottom that have already landed on `origin/main` (`landed` or
    `landed-recorded`, verified using `VALLEY_KNOWN_SIGNERS` and `VALLEY_INTEGRATOR_SIGNER`) drop
    out automatically ([rebase/family.go:1-120](../rebase/family.go)).
  - If the stack already fast-forwards from `origin/main`, changes nothing and exits `0`
    ([status/rebase.go:175-182](../status/rebase.go)).
  - Refuses without changing anything if any layer in the stack is `draft` or `withdrawn`, has no
    recorded branch, has a missing or moved remote branch (`origin/<branch> != head`), shares a
    branch with another layer, contains a merge commit, or contains a version head of a non-ready
    change or a change outside the stack ([status/rebase.go:310-430](../status/rebase.go)).
  - Rebases every layer bottom-first in a temporary worktree. On a merge conflict in any layer,
    aborts, pushes nothing, and prints the conflicting layer and file paths
    ([status/rebase.go:432-460](../status/rebase.go)).
  - Publishes in two phases:
    1. One `git push --atomic` moving every layer's `refs/heads/<branch>` with `--force-with-lease`
       on the tips read in the snapshot ([rebase/publish.go:20-95](../rebase/publish.go)).
    2. Bottom-first, adds a new version to each recorded change's
       `refs/the-valley/changes/main/<id>` just as `valley ready --if-active <old-head>` does
       ([status/rebase.go:215-260](../status/rebase.go)). If any phase-2 record write fails, prints
       a manifest and the exact
       `valley ready --change <id> --head <new-sha> --if-active <old-sha> <branch>` command to
       finish it, and exits `1` ([status/rebase.go:262-308](../status/rebase.go)).

### `valley checks`

`valley checks` composes the instance floor and project verification policy and prints the checks
required for the diff between two commits, along with the matched path classes and carry mode
([bin/valley:585-628](../bin/valley), [3285-3426](../bin/valley)).

```sh
valley checks
valley checks --names --cost quick
valley checks [--schema <file>] [--instance <dir>] [--project <dir>] [--names] [--cost quick|slow] [<base> [<head>]]
```

- **Arguments and flags:**
  - `[<base> [<head>]]`: `<head>` defaults to `HEAD`; `<base>` defaults to
    `git merge-base <target> <head>` where `<target>` is `origin/main` (falling back to local `main`
    only if `origin/main` does not exist; [bin/valley:3058-3069](../bin/valley),
    [3313-3330](../bin/valley)).
  - `--schema <file>`: CUE verification schema file (default: `$VALLEY_POLICY_SCHEMA`,
    `$VALLEY_VERIFICATION_SCHEMA`, or `<repo-root>/schema/verification.cue`;
    [bin/valley:2730-2731](../bin/valley)).
  - `--instance <dir>`: Directory of `*.cue` files for the instance floor. By default, read from
    `policy/instance/` at `origin/main` if present in this repository, or fetched from
    `refs/heads/main` of the repository URL named in `policy/valley` at `origin/main`
    ([bin/valley:2901-2971](../bin/valley)).
  - `--project <dir>`: Directory of `*.cue` files for the project layer. By default, read from
    `policy/project/` at `origin/main` (or an empty `package verification` layer if
    `policy/project/` holds no `*.cue` files; [bin/valley:2823-2856](../bin/valley)).
  - `--names`: Prints only the required check names, sorted, one per line
    ([bin/valley:3370-3379](../bin/valley)).
  - `--cost quick|slow`: Requires `--names`; filters the `--names` output to checks with
    `cost: "quick"` or `cost: "slow"` ([bin/valley:3305-3311](../bin/valley)).
- **Behavior:** Reads all added, modified, deleted, renamed (both old and new paths), and copied
  paths from `git diff --no-relative --name-status -M -z <base> <head>`, matches them against
  `policy.classes` (and `policy.unclassified` when any path matches no class), probes whether each
  required check is `mandatory` (pinned by the floor) or `default`, resolves the strictest `carry`
  mode (`exact`, `same-class`, or `same-paths`) across matched classes, and prints the report
  without running any check ([bin/valley:3100-3426](../bin/valley)).

### `valley plan`

`valley plan` renders the project's live plan graph (`refs/the-valley/knots`) as a milestone tree
followed by `waiting on you`, `in flight`, and `open residuals` sections
([status/plan.go:1-249](../status/plan.go)).

```sh
valley plan
valley plan [--project <name> | --all] [--owner <principal>] [--json]
```

- **Flags:**
  - `--project <name>`: Reads the plan graph of `<name>` on the same host (cannot be combined with
    `--all`; [status/plan.go:73-111](../status/plan.go)).
  - `--all`: Reads the plan graph of every project listed at `refs/the-valley/projects` in the
    host's instance repository ([status/plan.go:153-165](../status/plan.go)).
  - `--owner <principal>`: Overrides `git config valley.principal` when selecting which knots appear
    under `waiting on you` ([status/plan.go:145-150](../status/plan.go)).
  - `--json`: Emits [`valley-plan/v1`](#valley-planv1) JSON
    ([status/plan.go:845-908](../status/plan.go)).
- **Glyphs and sections:**
  - Root `outcome` knots (those with no `parent`) are milestones, showing descendant progress
    (`done/of` over non-abandoned descendants). Active non-outcome knots with no `parent` appear
    under `no milestone` ([status/plan.go:338-343](../status/plan.go),
    [550-615](../status/plan.go)).
  - State glyphs: `○` (`ready`), `◐` (`in-progress`), `⊘` (`blocked`), `◇` (`active`, such as an
    open `idea` or `question`, or an unclaimed `outcome` with children that is not itself ready),
    `✓` (`done`: `done`, `closed`, `graduated`, `answered`), `✗` (`abandoned`: `abandoned`,
    `discarded`, `superseded`, `dropped`) ([status/plan.go:396-415](../status/plan.go),
    [640-660](../status/plan.go)).

### `valley knot`

`valley knot` reads and writes knots in the plan graph at `refs/the-valley/knots` on `origin`
without a local branch checkout or review step ([status/knot.go:1-265](../status/knot.go)).

```sh
valley knot new outcome "Publish the CLI reference" --owner operator
valley knot <verb> [args] [--project <name>] [--json]
```

Every `valley knot` subcommand accepts `--project <name>` (to act on another project on the same
host) and `--json` (to emit
[`valley-knot/v1`, `valley-knots/v1`, `valley-knot-op/v1`, or `valley-knot-schema/v1`](#valley-knot---json-valley-knotv1-valley-knotsv1-valley-knot-opv1-valley-knot-schemav1);
[status/knot.go:84-135](../status/knot.go)). All write subcommands require
`git config valley.principal` and retry lease races up to 5 attempts (`knot.Attempts = 5`). After a
push whose outcome could not be determined, `valley knot` searches the latest 64 commits
(`knot.OpSearch = 64`) on `refs/the-valley/knots` for the operation's `Valley-Op` trailer before
retrying ([knot/contract.go:91-99](../knot/contract.go),
[status/knot.go:890-1005](../status/knot.go)).

| Subcommand | Synopsis and contract                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| :--------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `new`      | `valley knot new <type> <title> [--parent <id>] [--blocked-by <id>]... [--owner <p>] [--target <project>] [--body -]`<br>Creates a knot of `<type>` (`outcome`, `bug`, `idea`, or `question`) in its initial active status (`open` for `outcome`/`bug`/`question`, `raw` for `idea`). Computes `<slug>` from `<title>` (up to 48 bytes) and derives `<id>` as `<prefix>-<first-7-hex-of-sha256(slug)>`; if `<id>` is already taken in the graph, appends `-2`, `-3`, … to `<slug>` (trimming the base slug to fit 48 bytes) until `<id>` is unused ([knot/id.go:12-71](../knot/id.go), [status/knot.go:295-375](../status/knot.go)). `--blocked-by` and `--target` are valid on `outcome` and `bug` only. `--body -` reads Markdown body text from stdin.                                                                                       |
| `show`     | `valley knot show <id> [--project <name>] [--json]`<br>Displays all fields, derived edges (`blocks`, `children`, `progress`, `claim`, `ready`, `waiting_on`, `cleared_by_ending`, `dangling`, `unresolved`), Markdown body, and comments for `<id>` ([status/knot.go:1093-1175](../status/knot.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `list`     | `valley knot list [--type <t>] [--status <s>] [--owner <p>] [--ready] [--project <name>] [--json]`<br>Lists knots matching all given filters. `--status` accepts `active`, `terminal`, or any status name (`open`, `in-progress`, `done`, `abandoned`, `closed`, `raw`, `exploring`, `adopted`, `graduated`, `superseded`, `discarded`, `answered`, `dropped`). `--ready` filters to active, unclaimed `outcome` or `bug` knots with no unresolved or active blockers ([status/knot.go:1177-1239](../status/knot.go)).                                                                                                                                                                                                                                                                                                                          |
| `set`      | `valley knot set <id> <field>=<value>... [--if <field>=<value>]... [--force]`<br>Updates `status`, `title`, `source`, `owner`, `target`, or `graduated_into` (setting an optional field to `""` clears it; setting `status` on an `outcome` or `bug` to a terminal status also clears `claimed_by` and `claimed_at`; [status/knot.go:377-560](../status/knot.go)). Uses compare-and-set on the initial values of modified fields across lease retries unless `--force` is passed. `--if <field>=<value>` requires `<field>` to equal `<value>` (accepting any of the 15 frontmatter fields, with list fields `blocked_by` and `changes` compared as comma-joined strings; [knot/model.go:208-245](../knot/model.go), [status/knot.go:464-479](../status/knot.go)); exits `3` on conflict. Changing `title` keeps `<id>` and `<slug>` unchanged. |
| `link`     | `valley knot link <id> (--blocked-by <ref> \| --parent <id> \| --change <link>)... [--if <field>=<value>]...`<br>Adds one or more edges on `<id>` ([status/knot.go:562-618](../status/knot.go)). `--parent <id>` sets `parent` to a local `outcome` (using compare-and-set on the old `parent`). `--blocked-by <ref>` adds a local `<id>` or cross-project `<project>:<id>` to `blocked_by` (on `outcome` or `bug`). `--change <link>` adds `valley:<project>/<change-id>` or `https://github.com/<org>/<repo>/pull/<n>` to `changes` (on `outcome`, `bug`, or `question`).                                                                                                                                                                                                                                                                     |
| `unlink`   | `valley knot unlink <id> (--blocked-by <ref> \| --parent <id> \| --change <link>)... [--if <field>=<value>]...`<br>Removes the specified `--blocked-by`, `--parent` (accepts `--parent <current-parent-id>` or `--parent=""`), or `--change` edges from `<id>` ([status/knot.go:133-142](../status/knot.go), [594-600](../status/knot.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| `edit`     | `valley knot edit <id> --body - [--if <field>=<value>]...`<br>Replaces `<id>`'s Markdown body with text read from stdin (compare-and-set on the old body; [status/knot.go:619-638](../status/knot.go)). Passing an empty stdin clears the body.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `comment`  | `valley knot comment <id> (-m <text> \| --body -)`<br>Appends an immutable comment file at `comments/<id>/<YYYYMMDDTHHMMSSZ>-<16-hex-op>.md` with `author` set to `git config valley.principal` ([status/knot.go:640-668](../status/knot.go)). Comments cannot be edited or deleted.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `claim`    | `valley knot claim <id> --run <run-id> [--if <field>=<value>]...`<br>Claims an active, unclaimed `outcome` or `bug` for `<run-id>`, setting `claimed_by` to `<run-id>`, `claimed_at` to the current UTC timestamp (`RFC 3339`), and transitioning an `open` `outcome` to `in-progress`. If already claimed by the same `<run-id>`, prints a note to `stderr` and exits `0`; if `<id>` is not active or is an `idea` or `question`, exits `1`; if claimed by another run, exits `3` ([status/knot.go:225-233](../status/knot.go), [670-689](../status/knot.go)).                                                                                                                                                                                                                                                                                 |
| `unclaim`  | `valley knot unclaim <id> [--if <field>=<value>]...`<br>Clears `claimed_by` and `claimed_at` on `<id>` (leaving `status` unchanged). Exits `3` if the claim changed concurrently before the release landed ([status/knot.go:691-715](../status/knot.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `schema`   | `valley knot schema [--json]`<br>Prints the knot types, statuses, fields, edges, and graph limits in human text or [`valley-knot-schema/v1`](#valley-knot---json-valley-knotv1-valley-knotsv1-valley-knot-opv1-valley-knot-schemav1) JSON ([knot/derive.go:208-279](../knot/derive.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |

### `valley tail` and `valley replay`

`valley tail` subscribes to NATS bus events, and `valley replay` publishes one `ref-updated` event
for every current ref in a git repository ([bin/valley:629-634](../bin/valley),
[2533-2572](../bin/valley)).

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
into the user's default Nix profile, while `--version` and `help` report the CLI stamp and usage
([status/selfupdate.go:17-211](../status/selfupdate.go),
[selfupdate/selfupdate.go:1-420](../selfupdate/selfupdate.go),
[bin/valley:3428-3445](../bin/valley)).

```sh
valley self-update [-v] [--retry | --rollback | --pin | --unpin] [--repo <url>] [--branch <name>]
valley --version
valley help [self-update]
```

- **`valley self-update` flags:**
  - `--repo <url>`: Source repository URL (`https://`, `ssh://`, `git://`, `file://`, absolute path,
    or `user@host:path` with characters in `[A-Za-z0-9._~/:@-]`), overriding
    `git config valley.selfUpdate.repo`. If neither is set, `self-update` is disabled and exits `0`
    ([status/selfupdate.go:22-36](../status/selfupdate.go), [159-175](../status/selfupdate.go)).
  - `--branch <name>`: Branch to track, overriding `git config valley.selfUpdate.branch` (default
    `main`).
  - `-v`, `--verbose`: Prints status even when the installed `valley` is already at the branch tip.
  - `--retry`: Retries building the branch tip even if that exact commit previously failed its build
    or smoke test.
  - `--rollback`: Reinstalls the previous store path recorded in
    `$XDG_STATE_HOME/valley/self-update.json` (running `--rollback` again switches back).
  - `--pin` / `--unpin`: Freezes or unfreezes the installed `valley` in
    `$XDG_STATE_HOME/valley/self-update.json`; while pinned, `valley self-update` exits `0` without
    building or installing.
- **`valley --version`:** Prints `valley <version>-<short-rev>` (with `-dirty` for a dirty build
  tree or `-unknown` when Nix had no commit rev) when built by Nix (`nix/packages.nix`), or
  `valley unstamped (run from a checkout, not built by nix)` when run directly from a checkout
  ([bin/valley:3436-3438](../bin/valley), [nix/packages.nix:27-32](../nix/packages.nix)).
- **`valley help [self-update]`** (also `-h`, `--help`): Prints general `valley` usage, or detailed
  `self-update` usage when given `self-update` ([bin/valley:3440-3445](../bin/valley)).

### Helper and host commands

The `valley` package and NixOS host module build six Go binaries that `bin/valley` and `systemd`
invoke ([nix/packages.nix:1-145](../nix/packages.nix)).

```sh
attest key --key ~/.ssh/valley-attest --name operator
```

- **`attest`** ([attest/main.go:13-122](../attest/main.go)): Composes, signs, stores, and verifies
  `the-valley/attestation/v1` notes and computes canonical digests. Exits `0` on success, `1` on
  verification or runtime failure, `2` on invalid flags.
  - `attest run [--repo <dir>] [--rev <rev>] [--check <name>[=<attr>]]... [--command <name>=<cmd>]... [--environment <name>] --key <path> [--name <name>] [--provenance <file>] [--schema <file>] [--system <system>] [--push <remote>] [--branch <ref>]`
  - `attest verify --note <file> --known-keys <file> [--signer <name>]... [--schema <file>] [--repo <dir>] [--rev <rev>]`
  - `attest digest [--repo <dir>] [--rev <rev>]` — prints `valley-tree-v1:<64-hex>`
    ([attest/main.go:159](../attest/main.go)).
  - `attest inputs [--repo <dir>] [--rev <rev>] [--check <name>[=<attr>]]... [--system <system>]` —
    prints `<check> valley-inputs-v1:<64-hex> sha256:<64-hex-drv>` per check without building
    ([attest/inputs.go:58](../attest/inputs.go)).
  - `attest render [<file>]` — renders statement JSON (or canonical statement text) on `<file>` or
    stdin to canonical statement text.
  - `attest sign --key <path> [--name <name>]` — signs canonical statement text on stdin into a
    note, or appends a signature line to an existing note on stdin.
  - `attest key --key <path> [--name <name>]` — prints the `<name>+<8-hex-hash>+<base64>` verifier
    key line.
- **`valley-go`** ([status/main.go:46-128](../status/main.go)): Internal helper binary invoked by
  `bin/valley` for `status`, `ready`, `unready`, `withdraw`, `changes`, `pending`, `rebase`, `knot`,
  `plan`, and `self-update`, plus internal plumbing verbs (`resolve <change>`, `stack <change>`,
  `does [-v] <change-id>...`, `brings [--change <id>] <head>`, `versions <change-id>`,
  `compare --tip <commit> <change-id> <version> <head>`).
- **`valleyhook`** ([valleyhook/main.go:25-135](../valleyhook/main.go),
  [valleyhook/shell.go:43-80](../valleyhook/shell.go)):
  - `valleyhook pre-receive --project <name> --policy <json-file> [--grants <file>]... [--known-signers <file>]... [--principal <name>] [--then <hook-file>]`
    — validates ref updates on stdin.
  - `valleyhook shell --converged <file> --expect <id> [--hold <file>] [--authorized-keys <file>]... -- [-c <command>]`
    — login shell for the `git` user on a valley host.
- **`valley-integrator`** (binary `integrator`; [integrator/main.go:25-106](../integrator/main.go),
  [185-234](../integrator/main.go)):
  - `integrator reconcile [flags]` and `integrator watch [--interval <duration>] [flags]` — requires
    `--key`, `--known-signers`, `--schema` (default `$VALLEY_VERIFICATION_SCHEMA`),
    `--attest-schema` (default `$VALLEY_ATTEST_SCHEMA`), and either `--instance-repo` or
    `--instance` (optional flags: `--repo`, `--project`, `--name`, `--instance-ref`,
    `--instance-policy`, `--project-policy`, `--event-schema`, `--bus`, `--publish-queue`,
    `--system`, `--interval`, `--now`).
- **`valley-identity`** (binary `identity`; [identity/main.go:18-95](../identity/main.go)):
  - `identity compile --repo <dir> [--ref <ref>] [--dir <dir>] [--schema <file>] --known-signers <file> --authorized-keys <file> --grants <file> [--declared-keys <file>] [--now <YYYY-MM-DD>]`
    — compiles `identity/*.cue` from an instance repository tip into runtime files.
- **`valley-restacker`** ([restack/main.go:34-125](../restack/main.go)):
  - `valley-restacker once --report-only --repo <url> --state <dir> --principal <name> --schema <file> [--target main] [--known-signers <file>] [--integrator-name <name>] [--attest <path>] [--instance-repo <url> | --instance <dir>] [--instance-ref <ref>] [--instance-policy <dir>] [--project-policy <dir>] [--json]`
    — read-only planner (`implemented`); the write pass and host service are `not built`
    ([restack/main.go:114-115](../restack/main.go)).

---

## Environment variables and configuration

`valley` and its helpers read three `valley.*` git configuration keys, environment variables for
signing and schema paths, and local state and cache files ([bin/valley:704-708](../bin/valley)).

### Git configuration

Configure your principal name in each project clone, and optionally configure `self-update` in your
global git config ([status/ready.go:112-115](../status/ready.go),
[status/selfupdate.go:22-27](../status/selfupdate.go)).

```sh
git config valley.principal operator
git config --global valley.selfUpdate.repo git@valley.example:the-valley
git config --global valley.selfUpdate.branch main
```

| Git config key             | Scope               | Read by                                                           | Meaning                                                                                                                                                                                                                                                                                                                                                                       |
| :------------------------- | :------------------ | :---------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `valley.principal`         | repo or global      | `ready`, `unready`, `withdraw`, `rebase`, `knot` (writes), `plan` | The principal name your SSH key authenticates as on the host. Required for writing change records (`owner` / `uploader`) and knots (`created_by`, `author`, commit `Committer`), and used as the default operator in `valley plan` ([status/ready.go:112-115](../status/ready.go), [status/knot.go:258-264](../status/knot.go), [status/plan.go:146-150](../status/plan.go)). |
| `valley.selfUpdate.repo`   | global (`--global`) | `self-update`                                                     | Default repository URL for `valley self-update` when `--repo` is not passed ([status/selfupdate.go:159-165](../status/selfupdate.go)).                                                                                                                                                                                                                                        |
| `valley.selfUpdate.branch` | global (`--global`) | `self-update`                                                     | Default branch name for `valley self-update` when `--branch` is not passed (defaults to `main`; [status/selfupdate.go:166-170](../status/selfupdate.go)).                                                                                                                                                                                                                     |
| `pager.diff`, `core.pager` | repo or global      | `review`                                                          | Diff renderer used as `less`'s input preprocessor when `valley review` pages a diff (`git var GIT_PAGER` fallback; [bin/valley:716-765](../bin/valley)).                                                                                                                                                                                                                      |

### Environment variables

Set `VALLEY_KNOWN_SIGNERS` and `VALLEY_INTEGRATOR_SIGNER` to verify merged landings on the client,
and set `VALLEY_ATTEST_KEY` (and `VALLEY_ATTEST_NAME`) to sign check evidence during approval
([status/inventory.go:45-58](../status/inventory.go), [bin/valley:181-185](../bin/valley)).

```sh
export VALLEY_KNOWN_SIGNERS="$HOME/.config/valley/known-signers"
export VALLEY_INTEGRATOR_SIGNER=integrator
export VALLEY_ATTEST_KEY="$HOME/.ssh/valley-attest"
export VALLEY_ATTEST_NAME=operator
```

| Environment variable         | Read by                                                         | Meaning                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| :--------------------------- | :-------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `VALLEY_KNOWN_SIGNERS`       | `status`, `changes`, `pending`, `rebase`                        | Path to the `known-signers` file of verifier keys used to verify the integrator's transfer statement on merged (`landed-recorded`) landings. Must be set together with `VALLEY_INTEGRATOR_SIGNER`; if either is unset, merged landings cannot be verified and report as `uncertain` ([status/inventory.go:45-58](../status/inventory.go), [rebase/snapshot.go:250-255](../rebase/snapshot.go)).                                                                                            |
| `VALLEY_INTEGRATOR_SIGNER`   | `status`, `changes`, `pending`, `rebase`                        | The signing name (`<signer-name>`) the host's integrator signs transfer statements under ([status/inventory.go:45-58](../status/inventory.go)).                                                                                                                                                                                                                                                                                                                                            |
| `VALLEY_ATTEST_KEY`          | `approve`, `review` (`[a]pprove`)                               | Path to the OpenSSH `ssh-ed25519` private key passed to `attest run --key` whenever a change requires at least one check ([bin/valley:181-185](../bin/valley), [1423-1430](../bin/valley)).                                                                                                                                                                                                                                                                                                |
| `VALLEY_ATTEST_NAME`         | `approve`, `review` (`[a]pprove`)                               | Signer name passed to `attest run --name` (when unset, `attest` defaults to `<hostname>/attestations`, or `localhost/attestations` if `os.Hostname` fails; [bin/valley:184-185](../bin/valley), [1435-1437](../bin/valley), [attest/run.go:305-312](../attest/run.go)).                                                                                                                                                                                                                    |
| `VALLEY_VERIFICATION_SCHEMA` | `checks`, `approve`, `review`, `integrator`, `valley-restacker` | Path to `schema/verification.cue`. Set automatically by the Nix wrapper (`nix/packages.nix:74`); falls back to `<repo>/schema/verification.cue` when running `bin/valley` from a checkout ([bin/valley:2731](../bin/valley), [integrator/main.go:185](../integrator/main.go), [restack/main.go:106](../restack/main.go)).                                                                                                                                                                  |
| `VALLEY_POLICY_SCHEMA`       | `checks`, `approve`, `review`                                   | Optional override for the verification policy schema path in `bin/valley` (takes precedence over `VALLEY_VERIFICATION_SCHEMA` when `--schema` is not passed; [bin/valley:2730-2731](../bin/valley)).                                                                                                                                                                                                                                                                                       |
| `VALLEY_ATTEST_SCHEMA`       | `attest`, `integrator`                                          | Path to `schema/attestation.cue`, set by the Nix wrapper ([attest/statement.go:140](../attest/statement.go), [integrator/main.go:186](../integrator/main.go), [nix/packages.nix:48](../nix/packages.nix)).                                                                                                                                                                                                                                                                                 |
| `VALLEY_EVENT_SCHEMA`        | `integrator`                                                    | Path to `schema/events.cue`, set by the Nix wrapper ([integrator/main.go:187](../integrator/main.go), [nix/packages.nix:122](../nix/packages.nix)).                                                                                                                                                                                                                                                                                                                                        |
| `VALLEY_IDENTITY_SCHEMA`     | `identity`                                                      | Path to `schema/identity.cue`, set by the Nix wrapper ([identity/compile.go:20](../identity/compile.go), [nix/packages.nix:136](../nix/packages.nix)).                                                                                                                                                                                                                                                                                                                                     |
| `VALLEY_COMMAND`             | `pending --checks`                                              | Executable invoked by `valley-go pending --checks` to run `<valley> checks <base> <head>` (`bin/valley` sets `VALLEY_COMMAND` to its own path; defaults to `"valley"`; [bin/valley:779](../bin/valley), [status/pending.go:572-583](../status/pending.go)).                                                                                                                                                                                                                                |
| `VALLEY_NOW`                 | `knot`, `plan`                                                  | Optional UTC timestamp in `RFC 3339` format (`YYYY-MM-DDTHH:MM:SSZ`) overriding the system clock for knot `created` dates, comment stamps, claim timestamps, and claim age calculations ([status/main.go:110-114](../status/main.go), [193-219](../status/main.go)). Invalid values exit `2`.                                                                                                                                                                                              |
| `VALLEY_VERSION`             | `bin/valley`, `self-update`                                     | Package stamp (`<version>-<rev>`) baked into the Nix-wrapped `valley` script (`nix/packages.nix:110`), captured into `VALLEY_STAMP` and unset on startup so child processes do not inherit it, and passed explicitly to `valley-go self-update` ([bin/valley:49-50](../bin/valley), [3431-3434](../bin/valley)).                                                                                                                                                                           |
| `VALLEY_PRINCIPAL`           | `valleyhook shell`, host `pre-receive` wrapper                  | Host-side environment variable set by `valleyhook shell` after looking up the key named in `SSH_USER_AUTH` against the host's `authorized_keys` files ([valleyhook/shell.go:22-28](../valleyhook/shell.go), [95-98](../valleyhook/shell.go), [nix/valley-host.nix:504](../nix/valley-host.nix)). Any client-supplied `VALLEY_PRINCIPAL` or `GIT_*` environment variable is stripped by `valleyhook shell` before `git-shell` runs ([valleyhook/shell.go:140-156](../valleyhook/shell.go)). |
| `SSH_USER_AUTH`              | `valleyhook shell`                                              | Path to the file written by OpenSSH `sshd` (`ExposeAuthInfo yes`) containing the public key that authenticated the SSH session ([valleyhook/shell.go:22-28](../valleyhook/shell.go), [96](../valleyhook/shell.go), [nix/valley-host.nix:638](../nix/valley-host.nix)).                                                                                                                                                                                                                     |
| `XDG_STATE_HOME`             | `review`, `self-update`                                         | Base directory for persistent client state (defaults to `~/.local/state`; [bin/valley:836](../bin/valley), [selfupdate/selfupdate.go:103](../selfupdate/selfupdate.go)).                                                                                                                                                                                                                                                                                                                   |
| `XDG_CACHE_HOME`             | `pending`, `changes`, `plan`, `knot`                            | Base directory for cached cross-project clones (defaults to `~/.cache`; [status/list.go:402](../status/list.go)).                                                                                                                                                                                                                                                                                                                                                                          |
| `EDITOR`                     | `review` (via `less`)                                           | Editor invoked when pressing `v` while paging a diff in `valley review` ([bin/valley:245](../bin/valley), [729-735](../bin/valley)).                                                                                                                                                                                                                                                                                                                                                       |
| `GIT_PAGER`, `PAGER`, `LESS` | `review`                                                        | Pager and diff-renderer environment variables read by `valley review` (`LESS` defaults to `FRX` when unset; [bin/valley:716-769](../bin/valley)).                                                                                                                                                                                                                                                                                                                                          |
| `NO_COLOR`                   | `pending`, `plan`                                               | When set to a non-empty value, disables ANSI bold and color sequences on terminal output ([status/terminal.go:25-32](../status/terminal.go)).                                                                                                                                                                                                                                                                                                                                              |
| `NATS_URL`                   | `tail`, `replay` (via `nats`)                                   | NATS server URL used by the `nats` CLI when streaming or replaying bus events ([bin/valley:630-631](../bin/valley)).                                                                                                                                                                                                                                                                                                                                                                       |

### Client state and cache files

The client stores review notes and `self-update` state under `$XDG_STATE_HOME`, cached
multi-repository clones under `$XDG_CACHE_HOME`, and tab-completion handles inside the local git
directory ([bin/valley:706-708](../bin/valley), [status/handles.go:13-92](../status/handles.go)):

- `${XDG_STATE_HOME:-$HOME/.local/state}/valley/notes/<branch>.md`: Markdown review notes captured
  by `v` in `valley review` ([bin/valley:835-845](../bin/valley)).
- `${XDG_STATE_HOME:-$HOME/.local/state}/valley/self-update.json` and `self-update.lock`: Installed
  store paths, commits, pin flag, last failure record, and lock file for `valley self-update`
  ([selfupdate/selfupdate.go:95-120](../selfupdate/selfupdate.go)).
- `${XDG_CACHE_HOME:-$HOME/.cache}/valley/repos/<16-hex-url-hash>-<project>`: Cached non-bare git
  repositories (`git init --quiet` with no working tree checkout) fetched when `--all`, `--repo`, or
  `--project` reads another project on the host ([status/list.go:401-425](../status/list.go)).
- `<git-common-dir>/valley/handles`: Tab-separated list (`<handle>\t<state>\t<branch>\t<title>`) of
  unlanded `ready` and `draft` changes in the current repository, written by `valley pending` and
  `valley changes` and read by `completions/_valley`
  ([status/handles.go:13-92](../status/handles.go),
  [completions/\_valley:6-9](../completions/_valley)).

### NixOS host options

On a NixOS valley host, machine paths, service toggles, static keys, and static grants are
configured under `services.valley` (`deployment choice`;
[nix/valley-host.nix:870-1475](../nix/valley-host.nix)).

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

| NixOS option (`services.valley.*`)                                                           | Default                                            | Meaning                                                                                                                                                                                                                                                                                                                                                                                                          |
| :------------------------------------------------------------------------------------------- | :------------------------------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `enable`                                                                                     | `false`                                            | Enables the valley git host (`valleyhook shell`, `valley-init` service, and repository hooks; [nix/valley-host.nix:871](../nix/valley-host.nix)).                                                                                                                                                                                                                                                                |
| `user`, `group`, `dataDir`                                                                   | `"git"`, `"git"`, `"/srv/git"`                     | System user and group owning the bare `<project>.git` repositories, and the directory holding `<project>.git`, `.valley-converged`, and the optional `.valley-hold` push-hold sentinel ([nix/valley-host.nix:873-895](../nix/valley-host.nix)).                                                                                                                                                                  |
| `config`                                                                                     | (required)                                         | Path to the host's `valley.cue` declaration (`package valley`), validated against `schema/valley.cue` at build time ([nix/valley-host.nix:1036-1046](../nix/valley-host.nix)).                                                                                                                                                                                                                                   |
| `authorizedKeys`                                                                             | `[]` (must be non-empty when `enable = true`)      | Static SSH public keys written to `/etc/ssh/authorized_keys.d/<user>`: plain strings authenticate as anonymous pushers (`VALLEY_PRINCIPAL=""`), and `{ key, principal }` submodules authenticate as `<principal>`. No key may appear twice, and `users.users.<user>.openssh.authorizedKeys.keyFiles` must be empty ([nix/valley-host.nix:897-959](../nix/valley-host.nix), [1335-1390](../nix/valley-host.nix)). |
| `extraPreReceive`                                                                            | `{}`                                               | Attribute set mapping project name to a Nix store-path script run by `valleyhook pre-receive --then` after the push policy accepts a push ([nix/valley-host.nix:961-981](../nix/valley-host.nix)).                                                                                                                                                                                                               |
| `grants.request`, `grants.restack`                                                           | `[]`, `[]`                                         | Static principal lists holding the `request` (approval) and `restack` boundary grants in `valleyhook` ([nix/valley-host.nix:987-1034](../nix/valley-host.nix)).                                                                                                                                                                                                                                                  |
| `bus.enable`, `bus.listen`, `bus.storeDir`                                                   | `false`, `"127.0.0.1:4222"`, `"${dataDir}/.bus"`   | Optional local NATS JetStream server for `valley.git.<repo>.*` events ([nix/valley-host.nix:1051-1080](../nix/valley-host.nix)).                                                                                                                                                                                                                                                                                 |
| `integrator.enable`, `integrator.user`                                                       | `false`, `"valley-integrator"`                     | Spawns `valley-integrator@<project>` for every git-enabled project in `valley.cue` that declares a `protection` block (requires at least one protected project; `integrator.user` must differ from `services.valley.user`; [nix/valley-host.nix:1088-1112](../nix/valley-host.nix), [1395-1410](../nix/valley-host.nix)).                                                                                        |
| `integrator.signingKeyFile`, `integrator.signingName`                                        | `null` (required when enabled), `null`             | Runtime path to the integrator's Ed25519 private key and optional signer name (when `null`, `integrator` defaults to `<hostname>/attestations`; [nix/valley-host.nix:1114-1123](../nix/valley-host.nix), [1141-1156](../nix/valley-host.nix), [integrator/main.go:319-325](../integrator/main.go)).                                                                                                              |
| `integrator.knownSignersFile`                                                                | `null`                                             | Runtime path to the `known-signers` file of trusted verifier keys (used by both `valleyhook` and `valley-integrator`). Required when `integrator.enable = true` and `identity.enable = false`; must be `null` whenever `identity.enable = true` ([nix/valley-host.nix:1125-1139](../nix/valley-host.nix), [1415-1430](../nix/valley-host.nix)).                                                                  |
| `integrator.instanceProject`, `integrator.instancePolicy`                                    | `null`, `null`                                     | Exactly one must be set when `integrator.enable = true`: `instanceProject` names the hosted repository carrying `policy/instance/` (whose `refs/heads/main` must be protected) and causes `valley-init` to write `refs/the-valley/projects`; `instancePolicy` names a local directory of floor `*.cue` files ([nix/valley-host.nix:1158-1218](../nix/valley-host.nix), [1395-1445](../nix/valley-host.nix)).     |
| `integrator.interval`                                                                        | `"15s"`                                            | Poll interval between `integrator watch` reconciliation passes ([nix/valley-host.nix:1220-1230](../nix/valley-host.nix)).                                                                                                                                                                                                                                                                                        |
| `identity.enable`, `identity.ref`, `identity.directory`, `identity.interval`                 | `false`, `"refs/heads/main"`, `"identity"`, `"5m"` | Compiles `<directory>/*.cue` from `integrator.instanceProject` at `<ref>` (which must be covered by `protection`) into `/var/lib/valley-identity/{known-signers,authorized_keys.<user>,grants}` on a timer (`knownSignersFile` must be `null`; [nix/valley-host.nix:1239-1280](../nix/valley-host.nix), [1430-1475](../nix/valley-host.nix)).                                                                    |
| `backup.repositoryFile`, `backup.passwordFile`, `backup.sshKeyFile`, `backup.knownHostsFile` | `null`                                             | Runtime secret paths required whenever `valley.cue` enables `backup` (`backup.enable` defaults to `true` when `backup` is declared; [nix/valley-host.nix:1288-1350](../nix/valley-host.nix)).                                                                                                                                                                                                                    |

---

## Project storage ref layout and bounds

Every repository on a valley host separates **the source** (`refs/heads/main` and `refs/tags/*`),
topic branches (`refs/heads/<branch>`), and **project storage** (`refs/the-valley/*`), and
`valleyhook pre-receive` validates every SSH push against fixed structural rules and size limits
([valleyhook/main.go:36-106](../valleyhook/main.go),
[valleyhook/policy.go:34-225](../valleyhook/policy.go)).

### Push-wide bounds

`valleyhook` enforces four repository-wide and push-wide counters before accepting a push
([valleyhook/main.go:144](../valleyhook/main.go), [204-250](../valleyhook/main.go)):

```sh
git push --atomic origin topic refs/the-valley/changes/main/nxvpwlkmqtsrzyoukklnpmqrstuvwxyz
```

| Bound                                                | Value                                                          | Enforcement                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| :--------------------------------------------------- | :------------------------------------------------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Ref updates per push (`maxUpdates`)                  | `<= 1,000`                                                     | Refuses the entire push before evaluating any ref ([valleyhook/main.go:144](../valleyhook/main.go), [204-208](../valleyhook/main.go)).                                                                                                                                                                                                                                                                                                                                  |
| Change record updates per push (`MaxRecordUpdates`)  | `<= 32`                                                        | Refuses the push if more than 32 `refs/the-valley/changes/*` refs are updated in one push ([change/record.go:84](../change/record.go), [valleyhook/main.go:222-228](../valleyhook/main.go)).                                                                                                                                                                                                                                                                            |
| Change records per repository (`MaxRecords`)         | `<= 2,000` (soft push cap)<br>`<= 4,000` (hard integrator cap) | `valleyhook` refuses creating a new change record when `existingRecords + recordCreates > 2,000` (concurrent pushes can briefly exceed it; [change/record.go:89](../change/record.go), [valleyhook/record.go:200-202](../valleyhook/record.go)). The integrator refuses to reconcile any pass if a repository holds more than `4,000` change records (`recordLimit = 4000`; [integrator/records.go:78](../integrator/records.go), [120-122](../integrator/records.go)). |
| Restack report updates per push (`MaxReportUpdates`) | `<= 64`                                                        | Refuses the push if more than 64 `refs/the-valley/restack/*` refs are updated in one push ([change/report.go:127](../change/report.go), [valleyhook/main.go:229-233](../valleyhook/main.go)).                                                                                                                                                                                                                                                                           |

### Ref layout, permissions, and object bounds

The table below lists every ref namespace on a valley host, who may write it over SSH vs. locally on
the host, the required git object type, and the size bounds enforced by `valleyhook`
([valleyhook/policy.go:34-225](../valleyhook/policy.go),
[valleyhook/repo.go:65-175](../valleyhook/repo.go),
[valleyhook/record.go:18-48](../valleyhook/record.go),
[valleyhook/report.go:18-24](../valleyhook/report.go),
[valleyhook/knots.go:20-23](../valleyhook/knots.go)). After checking a ref's namespace rule below,
`valleyhook` also checks `#Protection.refs` (`deployment choice`): if any protected pattern matches
a ref in **any** namespace (`refs/heads/*`, `refs/tags/*`, or `refs/the-valley/*`), the pushing
principal must also be listed in `#Protection.writers`
([valleyhook/policy.go:110-114](../valleyhook/policy.go), [186-188](../valleyhook/policy.go)).

| Ref pattern                                                                                               | Purpose                                                                  | Who may write                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Object type and layout                                                                                                                                                                                                                | Size and structure bounds                                                                                                                                                                                                                                                                                                                                                                                              |
| :-------------------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refs/heads/main` (or any ref matching `#Protection.refs`)                                                | Protected integration branch (`the source`)                              | **Local host write:** `valley-integrator` via `git update-ref` (`implemented`).<br>**SSH push:** Only principals listed in `#Protection.writers` (`deployment choice`, default `[]` when `protection` is declared; if `protection` is omitted in `valley.cue`, all branches are open; [schema/valley.cue:49-53](../schema/valley.cue), [119-134](../schema/valley.cue), [valleyhook/policy.go:186-188](../valleyhook/policy.go)).                                                                                                                                                                                  | Commit object                                                                                                                                                                                                                         | Standard git commit; `refs/heads/main` is replicated to `mirrors` by `valley-publish` ([nix/valley-host.nix:252-265](../nix/valley-host.nix)).                                                                                                                                                                                                                                                                         |
| `refs/heads/<branch>` (unprotected)                                                                       | Topic branches                                                           | Any SSH key in `authorized_keys` (named principal or anonymous pusher; [valleyhook/policy.go:142-144](../valleyhook/policy.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | Commit object                                                                                                                                                                                                                         | `<branch>` must pass `git check-ref-format`; not replicated to `mirrors`.                                                                                                                                                                                                                                                                                                                                              |
| `refs/tags/<tag>` (and any ref outside `refs/heads/`, `refs/replace/`, `refs/notes/`, `refs/the-valley/`) | Release tags (`the source`) and custom refs                              | Closed by default (`implemented`); opened over SSH only to principals listed in a matching `#Project.grants.<name>` (`#RefGrant`) in `valley.cue` (`deployment choice`), and also requires `#Protection.writers` if matched by `#Protection.refs` ([valleyhook/policy.go:177-190](../valleyhook/policy.go)).                                                                                                                                                                                                                                                                                                       | Any git object (tag, commit, tree, or blob)                                                                                                                                                                                           | `refs/tags/*` is replicated to `mirrors` by `valley-publish` ([nix/valley-host.nix:252-265](../nix/valley-host.nix)).                                                                                                                                                                                                                                                                                                  |
| `refs/replace/*`, `refs/notes/*`                                                                          | Git object replacements and notes                                        | **Nobody.** Categorically refused on all SSH pushes (`implemented`; [valleyhook/policy.go:125-128](../valleyhook/policy.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | —                                                                                                                                                                                                                                     | Always refused.                                                                                                                                                                                                                                                                                                                                                                                                        |
| Any symbolic ref                                                                                          | Symbolic ref target                                                      | **Nobody.** Categorically refused on all SSH pushes (`implemented`; [valleyhook/policy.go:135-137](../valleyhook/policy.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | —                                                                                                                                                                                                                                     | Always refused.                                                                                                                                                                                                                                                                                                                                                                                                        |
| `refs/the-valley/changes/main/<change-id>`                                                                | Change record (`valley-change-record/v1`)                                | **Create:** Any named principal (becomes `owner`).<br>**Add version / `unready` / `re-ready` / `withdraw`:** Change `owner` or a `request` grant holder.<br>**Add restacked version (`ready` → `ready`, same `branch` & `title`):** `restack` grant holder.<br>**Delete:** `request` grant holder only.<br>(`valleyhook` accepts target `main` only; `<change-id>` is `[k-z]{32}`; [valleyhook/record.go:160-207](../valleyhook/record.go)).                                                                                                                                                                       | **Commit** whose root **tree** contains a single regular file (`100644`) named `record.json` ([valleyhook/record.go:19-46](../valleyhook/record.go), [88-104](../valleyhook/record.go)).                                              | Commit `<= 4 KiB` (`4,096` bytes);<br>Tree `<= 1 KiB` (`1,024` bytes, 1 entry `100644 record.json`);<br>Blob `record.json` `<= 64 KiB` (`65,536` bytes);<br>`<= 1,000` versions, `seq <= 100,000` ([change/record.go:69-84](../change/record.go)).                                                                                                                                                                     |
| `refs/the-valley/restack/main/<change-id>`                                                                | Restack report (`valley-restack-report/v1`)                              | **Create / extend (`P = []` or `P = [old]`):** Principals holding the `restack` grant, only for a `<change-id>` that already has a change record on `main`.<br>**Delete or history rewrite:** Refused ([valleyhook/report.go:45-103](../valleyhook/report.go)).                                                                                                                                                                                                                                                                                                                                                    | **Commit** whose root **tree** contains a single regular file (`100644`) named `report.json` ([valleyhook/report.go:18-27](../valleyhook/report.go)).                                                                                 | Commit `<= 4 KiB` (`4,096` bytes);<br>Tree `<= 1 KiB` (`1,024` bytes, 1 entry `100644 report.json`);<br>Blob `report.json` `<= 64 KiB` (`65,536` bytes);<br>`<= 64` layers, `<= 100` conflict paths, `<= 64` blocking requests ([change/report.go:108-128](../change/report.go)).                                                                                                                                      |
| `refs/the-valley/attestations/<tree-digest>/<key-hash>`                                                   | Signed check attestations and integrator transfer statements             | **Create over SSH:** Any pusher, provided `--known-signers` is configured and every note in the tree verifies under a known signer with key hash `<key-hash>` (`[0-9a-f]{8}`) for `<tree-digest>` (`[0-9a-f]{64}`).<br>**Update or delete over SSH:** Refused (create-only; [valleyhook/policy.go:146-149](../valleyhook/policy.go), [valleyhook/attestation.go:19-80](../valleyhook/attestation.go)).<br>**Local host write:** `valley-integrator` writes countersigned `<check>/statement.note` and `transfer.note` under its own `<key-hash>` on landing ([integrator/refs.go:494-518](../integrator/refs.go)). | **Tree** object (not a commit) containing `100644` note blobs (such as `<check>/statement.note` and `transfer.note`; [attest/git.go:244-260](../attest/git.go), [integrator/transfer.go:31](../integrator/transfer.go)).              | Tree depth `<= 3` (root tree is depth 1);<br>`<= 64` entries per tree object;<br>`<= 256` total entries visited across walk (`>= 1` note blob required);<br>Each tree object `<= 16 KiB` (`16,384` bytes);<br>Each note blob (`100644`) `<= 64 KiB` (`65,536` bytes);<br>Total bytes read in walk `<= 1 MiB` (`1,048,576` bytes; [valleyhook/repo.go:75-96](../valleyhook/repo.go), [147-174](../valleyhook/repo.go)). |
| `refs/the-valley/integration-requests/<target>/<request>`                                                 | Integration request (approval of a commit)                               | **Create / replace / delete over SSH:** Principals holding the `request` grant (`valleyhook` checks the `refs/the-valley/integration-requests/` prefix and the `request` grant; [valleyhook/policy.go:151-158](../valleyhook/policy.go)).<br>**Local host delete:** `valley-integrator` deletes the ref (CAS on request head) when a change lands or when voiding a `withdrawn` change's request ([integrator/refs.go:118](../integrator/refs.go), [504-506](../integrator/refs.go)).                                                                                                                              | **Commit** object (written by `valley approve` and required by `valley-integrator` at `refs/the-valley/integration-requests/main/<request>`, where `<request>` contains no `/`; [integrator/refs.go:173-215](../integrator/refs.go)). | Must resolve to a commit object in the repository for the integrator to consider it ([integrator/refs.go:196-205](../integrator/refs.go)).                                                                                                                                                                                                                                                                             |
| `refs/the-valley/integration-outcomes/<target>/<request>`                                                 | Integrator outcome record                                                | **SSH push:** Nobody (`implemented`; [valleyhook/policy.go:129-133](../valleyhook/policy.go)).<br>**Local host write:** `valley-integrator` writes after judging a request ([integrator/refs.go:44](../integrator/refs.go), [548-567](../integrator/refs.go)).                                                                                                                                                                                                                                                                                                                                                     | **Blob** object (single line of space-separated fields; [rebase/outcome.go:10-52](../rebase/outcome.go)).                                                                                                                             | 4 or 5 whitespace-separated fields terminated by `\n`.                                                                                                                                                                                                                                                                                                                                                                 |
| `refs/the-valley/knots`                                                                                   | Project plan graph (exact ref name; `refs/the-valley/knots/*` is closed) | **Create (`P = []`) / update (`P = [old]`):** Any named principal (`Committer` must match pushing principal, commit message must end with `Valley-Op: <16-hex>`).<br>**Delete or non-fast-forward:** Refused ([valleyhook/policy.go:171-174](../valleyhook/policy.go), [valleyhook/knots.go:90-175](../valleyhook/knots.go)).                                                                                                                                                                                                                                                                                      | **Commit** whose **tree** holds `<type-dir>/<id>-<slug>.md` and `comments/<id>/<stamp>-<op>.md` (`100644` blobs; [knot/contract.go:9-16](../knot/contract.go)).                                                                       | Commit `<= 16 KiB` (`16,384` bytes);<br>Changed paths per commit `<= 32`;<br>Tree depth `<= 3`, `<= 20,000` entries/tree, `<= 30,005` total walk entries, each tree `<= 2 MiB`;<br>Knot file `<= 64 KiB`, `<= 5,000` knots;<br>Comment file `<= 16 KiB`, `<= 20,000` comments;<br>Total walk read `<= 64 MiB`, hook deadline `5s` ([knot/contract.go:39-89](../knot/contract.go)).                                     |
| `refs/the-valley/projects`                                                                                | Hosted project list in the instance repository                           | **SSH push:** Nobody (`implemented`; [valleyhook/policy.go:129-133](../valleyhook/policy.go)).<br>**Local host write:** `valley-init` writes in `integrator.instanceProject` ([nix/valley-host.nix:68](../nix/valley-host.nix), [559-575](../nix/valley-host.nix)).                                                                                                                                                                                                                                                                                                                                                | **Blob** object                                                                                                                                                                                                                       | Sorted enabled git project names, one per line, terminated by `\n`.                                                                                                                                                                                                                                                                                                                                                    |
| Any other `refs/the-valley/*`                                                                             | Reserved for host machinery                                              | **SSH push:** Nobody (`implemented`; [valleyhook/policy.go:129-133](../valleyhook/policy.go)).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | —                                                                                                                                                                                                                                     | Always refused on push.                                                                                                                                                                                                                                                                                                                                                                                                |

---

## Wire formats and JSON schemas

This section specifies the exact wire formats for records in project storage and the JSON documents
emitted by `--json` commands.

### Change record (`valley-change-record/v1`)

A change record tracks a change's owner, target branch, state (`ready`, `draft`, or `withdrawn`),
version history, and active version as `record.json` inside a commit at
`refs/the-valley/changes/main/<change-id>` ([change/record.go:10-249](../change/record.go),
[valleyhook/record.go:160-383](../valleyhook/record.go)).

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
  rejected; [change/record.go:28-34](../change/record.go), [251-297](../change/record.go)).
- **Top-level fields ([change/record.go:97-106](../change/record.go),
  [196-233](../change/record.go)):**
  - `schema` (`string`): Must be `"valley-change-record/v1"`.
  - `change_id` (`string`): 32 lowercase letters in `k`–`z` (`[k-z]{32}`), matching `<change-id>` in
    the ref name.
  - `target` (`string`): Valid UTF-8 branch name matching `<target>` in the ref name (`valleyhook`
    admits `"main"` only; [valleyhook/record.go:187-189](../valleyhook/record.go)).
  - `owner` (`string`): Principal name (`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`). Set to the pushing
    principal on creation and never changes on update
    ([valleyhook/record.go:237-238](../valleyhook/record.go), [316-317](../valleyhook/record.go)).
  - `state` (`string`): `"ready"`, `"draft"`, or `"withdrawn"`.
  - `seq` (`int`): Monotonically increasing update counter in `1..100000` (`MaxSeq = 100000`).
    Starts at `1` on creation and increases by exactly `1` on every update
    ([valleyhook/record.go:239-240](../valleyhook/record.go), [314-315](../valleyhook/record.go),
    [349-350](../valleyhook/record.go)).
  - `versions` (`array` of objects): `1..1000` entries (`MaxVersions = 1000`), numbered
    `n: 1, 2, ...` in order with no gaps. Prior versions are immutable once recorded
    ([valleyhook/record.go:332-336](../valleyhook/record.go)).
  - `active` (`int`): 1-based version number in `1..len(versions)`. Every `valleyhook` transition
    sets `active` to `1` on create, keeps `active` unchanged on state changes, and sets `active` to
    `len(versions)` on a new version, so `active` on a host-validated record always equals
    `len(versions)` ([valleyhook/record.go:241](../valleyhook/record.go),
    [318](../valleyhook/record.go), [345](../valleyhook/record.go)).
- **`versions[]` fields ([change/record.go:108-115](../change/record.go),
  [217-231](../change/record.go)):**
  - `n` (`int`): `i + 1` (1-based index).
  - `head` (`string`): `"git-sha1:<40-lowercase-hex>"` naming a commit object in the repository.
  - `title` (`string`): `1..200` bytes (`MaxTitle = 200`) of valid UTF-8 with no Unicode control
    characters ([change/record.go:72](../change/record.go), [321-333](../change/record.go)).
  - `branch` (`string`): `""` (empty) or a valid UTF-8 git branch name (`ValidBranch`).
  - `uploader` (`string`): Principal name of the pusher that added this version.
- **Record commit parent-history and transition table
  ([valleyhook/record.go:160-370](../valleyhook/record.go)):**

| Transition      | Authorized principal                                                   | Record commit parents `P`                                                                      | State and field rules                                                                                                                                                                                                                                                          |
| :-------------- | :--------------------------------------------------------------------- | :--------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Create**      | Any named principal (`p`)                                              | `P = [h]` (1 parent: version 1's `head` commit `h`)                                            | `seq = 1`, `active = 1`, `len(versions) = 1`, `versions[0].head = "git-sha1:" + h`, `owner = p`, `versions[0].uploader = p`, `state = "ready"`.                                                                                                                                |
| **New version** | `owner` or `request` grant holder                                      | `P = [old, h]` (2 parents: replaced record commit `old`, then new version's `head` commit `h`) | Appends exactly 1 version (`n = len(before.versions) + 1`, `head = "git-sha1:" + h != before.activeVersion.head`, `uploader = p`), `active = n`, `seq = before.seq + 1`, `state = "ready"` (from `ready`, `draft`, or `withdrawn`).                                            |
| **Unready**     | `owner` or `request` grant holder                                      | `P = [old]` (1 parent: replaced record commit `old`)                                           | `before.state = "ready"` → `after.state = "draft"`; `seq = before.seq + 1`; `owner`, `active`, and `versions` unchanged.                                                                                                                                                       |
| **Re-ready**    | `owner` or `request` grant holder                                      | `P = [old]` (1 parent: replaced record commit `old`)                                           | `before.state` in `{"draft", "withdrawn"}` → `after.state = "ready"`; `seq = before.seq + 1`; `owner`, `active`, and `versions` unchanged. (`withdrawn` → `draft` is refused.)                                                                                                 |
| **Withdraw**    | `owner` or `request` grant holder                                      | `P = [old]` (1 parent: replaced record commit `old`)                                           | `before.state` in `{"ready", "draft"}` → `after.state = "withdrawn"`; `seq = before.seq + 1`; `owner`, `active`, and `versions` unchanged.                                                                                                                                     |
| **Restack**     | `restack` grant holder (who is neither `owner` nor a `request` holder) | `P = [old, h]` (2 parents: replaced record commit `old`, then new version's `head` commit `h`) | `before.state = "ready"` → `after.state = "ready"`; appends 1 version (`head = "git-sha1:" + h != before.activeVersion.head`, `uploader = p`) keeping `branch == before.activeVersion.branch` and `title == before.activeVersion.title`; `active = n`, `seq = before.seq + 1`. |
| **Delete**      | `request` grant holder                                                 | Ref deletion (`new = 0000...`)                                                                 | Deletes `refs/the-valley/changes/main/<change-id>`.                                                                                                                                                                                                                            |

### Restack report (`valley-restack-report/v1`)

A restack report records what an automated restack attempt observed and published for a change as
`report.json` inside a commit at `refs/the-valley/restack/main/<change-id>`
([change/report.go:14-435](../change/report.go),
[valleyhook/report.go:45-103](../valleyhook/report.go),
[rebase/report.go:11-94](../rebase/report.go)).

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
  parent (`P = [old]`); deletions are refused
  ([valleyhook/report.go:88-92](../valleyhook/report.go)).
- **Fields and cross-field invariants ([change/report.go:130-398](../change/report.go)):**
  - `schema` (`string`): Must be `"valley-restack-report/v1"`.
  - `change_id` (`string`): `[k-z]{32}`, matching the ref's `<change-id>` and appearing in
    `layers[]`.
  - `target` (`string`): Valid UTF-8 branch name matching the ref's `<target>` (`"main"` in
    `valleyhook`; [valleyhook/report.go:65-68](../valleyhook/report.go)).
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
      and `record` ([change/report.go:345-366](../change/report.go),
      [392-396](../change/report.go)).
  - `failing_layer` (`string` or `null`): `null` or a `change_id` present in `layers[]` (required to
    be non-null when `result == "conflict"`).
  - `conflict_paths` (`array` of `string`): `0..100` items (`MaxConflictPaths = 100`), each
    `1..1024` bytes (`MaxConflictPath = 1024`) of valid UTF-8 without control characters.
  - `blocking_requests` (`array` of objects): `0..64` items (`MaxBlockingRequest = 64`), unique by
    `ref`. Each item has `ref` (`refs/the-valley/integration-requests/<target>/<name>`, `<= 256`
    bytes, `MaxRequestRef = 256`) and `tip` (`"git-sha1:<40-hex>"`).
  - `refusal` (`string` or `null`): `null` or `1..2048` bytes (`MaxRefusal = 2048`) of valid UTF-8
    without control characters.
- **Currency rule (`Origin.Current`; [rebase/report.go:57-94](../rebase/report.go)):** A report is
  marked `current: true` in CLI outputs only while `refs/heads/main` is still at `target_tip`, every
  `blocking_requests[]` ref is still at `tip`, and for every layer,
  `refs/the-valley/changes/main/<change_id>` is at `published.record` (if non-null, else `record`)
  and `refs/heads/<branch>` (when `branch != ""`) is at `published.head` (if `published` is
  non-null, else `branch_tip`, or still absent when `branch_tip == null`). Otherwise `current` is
  `false` (historical).

### Signed notes and attestation statement (`the-valley/attestation/v1`)

An attestation note wraps canonical line-oriented `the-valley/attestation/v1` statement text in a
signed note envelope (`golang.org/x/mod/sumdb/note` format) and is stored at
`refs/the-valley/attestations/<64-hex-tree-digest>/<8-hex-key-hash>`
([note/note.go:1-190](../note/note.go), [attest/text.go:1-215](../attest/text.go),
[schema/attestation.cue:1-356](../schema/attestation.cue)).

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

- **Note envelope, key hash, and verifier key ([note/note.go:46-185](../note/note.go)):**
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
    `SHA-256(<signer-name> + "\n" + 0x01 + <32-byte-ed25519-pubkey>)`
    ([note/note.go:65-77](../note/note.go)). Because `<signer-name>` is inside the key hash, the
    same Ed25519 public key under two different names produces two distinct verifier keys.
  - **Verifier key line (`known-signers`):**
    `<signer-name>+<8-hex-key-hash>+<base64(0x01 || 32-byte-ed25519-pubkey)>`
    ([note/note.go:135-140](../note/note.go)).
- **Statement written form (`the-valley/attestation/v1`;
  [attest/text.go:1-215](../attest/text.go)):**
  - Line 1 is `the-valley/attestation/v1`.
  - Every subsequent line is `<dot.separated.key> <value>\n`. There are no escape sequences; every
    leaf value is a non-empty string without ASCII control characters; empty objects and empty
    arrays are rejected; array indices are dense decimal integers starting at `0`.
  - Lines appear in the fixed `statementOrder` ([attest/text.go:114-205](../attest/text.go)):
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
  - `predicate.output.sha256` (64-hex `valley-tree-v1` digest of the Nix build output path;
    [attest/check.go:147-151](../attest/check.go))
- **Effectful check predicate (`the-valley/check/effectful/v1`; stored at
  `<check>/statement.note`):**
  - `predicate.check.name`, `predicate.check.runner` (`command`), `predicate.check.command`
  - `predicate.environment` (default `"local"`)
  - `predicate.observedAt` (`YYYY-MM-DDTHH:MM:SSZ`)
  - `predicate.result` (`passed` or `failed`)

### Canonical digest manifests (`valley-tree-v1`, `valley-inputs-v1`, `valley-delta-v1`)

the-valley uses three canonical SHA-256 manifest formats for trees, pure check inputs, and
version-to-version file deltas, plus a 16-hex evidence fingerprint
([attest/digest.go:5-102](../attest/digest.go), [delta/delta.go:1-143](../delta/delta.go),
[change/id.go:118-135](../change/id.go)).

```text
valley-tree-v1
100644 e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 12 greeting.txt
```

1. **`valley-tree-v1` ([attest/digest.go:5-78](../attest/digest.go),
   [109-185](../attest/digest.go)):**
   - **Purpose:** Content-addressed 64-hex SHA-256 digest of a git tree or filesystem output tree,
     independent of commit metadata.
   - **Encoding:** Header `"valley-tree-v1\n"`, followed by one line per file or symlink entry
     across the tree, sorted ascending by raw `<path>` bytes:
     `<mode> <64-hex-sha256-of-content> <decimal-byte-length-of-path> <path>\n`
   - **Modes and content:** `100644` (regular file), `100755` (executable file), or `120000`
     (symlink, whose hashed content is the symlink target bytes). Directory entries are omitted
     (implied by file paths), and submodules (`160000`) are refused
     ([attest/digest.go:130-150](../attest/digest.go)).
2. **`valley-inputs-v1` ([attest/digest.go:19-25](../attest/digest.go),
   [80-102](../attest/digest.go), [attest/check.go:156-175](../attest/check.go)):**
   - **Purpose:** 64-hex SHA-256 digest of the set of Nix store paths referenced by a pure Nix
     check's `.drv` (`nix-store --query --references <drv>`).
   - **Encoding:** Header `"valley-inputs-v1\n"`, followed by one line per input name, sorted
     bytewise ascending and deduplicated: `<decimal-byte-length-of-name> <name>\n`
3. **`valley-delta-v1` ([delta/delta.go:1-143](../delta/delta.go),
   [delta/tree.go:31-69](../delta/tree.go)):**
   - **Purpose:** 64-hex SHA-256 digest of the whole-file changes between a pre-tree `T` and a
     post-tree `R`, used by `valley review` (`valley-go compare`) to check whether two versions of a
     change make the exact same modifications to their respective merge bases.
   - **Encoding:** Header `"valley-delta-v1\n"`, followed by one line per path whose entry differs
     between `T` and `R`, sorted bytewise ascending by `<path>`:
     `<decimal-byte-length-of-path> <path> <pre-image> <post-image>\n` Each image is `-` when the
     path is absent on that side, or `<mode> <64-hex-sha256>` (`100644`, `100755`, or `120000`).
     Both trees are validated for Linux checkout safety (modes in `{100644, 100755, 120000, 40000}`,
     no empty/`.`/`..`/case-insensitive `.git` segments, component names `<= 255` bytes, non-empty
     symlink targets `<= 4,095` bytes without NUL; [delta/tree.go:34-45](../delta/tree.go)).
4. **Evidence fingerprint ([change/id.go:118-135](../change/id.go),
   [integrator/refs.go:155-171](../integrator/refs.go)):**
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
([integrator/transfer.go:1-110](../integrator/transfer.go),
[integrator/refs.go:466-518](../integrator/refs.go),
[schema/attestation.cue:162-238](../schema/attestation.cue)).

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

- **Subject and predicate fields ([integrator/transfer.go:43-97](../integrator/transfer.go),
  [attest/text.go:132-160](../attest/text.go)):**
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
    which the project policy was read and the 64-hex SHA-256 digest of the composed policy rendering
    ([composed/composed.go:120-165](../composed/composed.go)).
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
fingerprint, and decision ([integrator/refs.go:44](../integrator/refs.go),
[146-171](../integrator/refs.go), [217-247](../integrator/refs.go),
[548-567](../integrator/refs.go), [rebase/outcome.go:10-52](../rebase/outcome.go)).

```text
2222222222222222222222222222222222222222 3333333333333333333333333333333333333333 9a8b7c6d5e4f3a2b land
```

- **Format:**
  `<head-oid> <target-tip-oid> <16-hex-evidence-fingerprint> <decision>[ <failed-at-rfc3339>]\n`
  - `<head-oid>` and `<target-tip-oid>`: 40-hex SHA-1 (or 64-hex SHA-256) commit object IDs.
  - `<16-hex-evidence-fingerprint>`: First 16 hex digits of SHA-256 over sorted `<ref> <oid>\n`
    lines under `refs/the-valley/attestations/`
    ([integrator/refs.go:155-171](../integrator/refs.go)).
  - `<decision>`: One of:
    - `land`: The change landed on `<target>`, and its integration request ref was consumed.
    - `stale`: The change conflicted with `<target>` or required check evidence was missing,
      expired, or did not transfer; the request ref remains standing.
    - `reject`: Attestation note or statement failed cryptographic or schema verification; the
      request ref remains standing.
    - `refused-not-ready`: The change record (or a change whose version head is brought by the
      request) is not `ready`, or a `c-<id>` request's head is not listed in the change's
      `versions`; the request ref remains standing and is reconsidered on every integrator pass
      ([integrator/main.go:53-55](../integrator/main.go),
      [integrator/refs.go:220-229](../integrator/refs.go)).
    - `refused-merge`: The commits between the merge base with `<target>` and `<head-oid>` include a
      merge commit; the request ref remains standing and is reconsidered on every integrator pass
      ([integrator/refs.go:220-229](../integrator/refs.go)).
    - `voided-withdrawn`: The change record was `withdrawn` when the integrator pass ran, and the
      integrator deleted the pending request ref.
    - `failed`: An internal error occurred during the pass. Requires the 5th field
      `<failed-at-rfc3339>` (`YYYY-MM-DDTHH:MM:SSZ`). The integrator retries a `failed` request on
      the same `(head, tip, evidence)` tuple after `retryFailedAfter = 10m`
      ([integrator/refs.go:47](../integrator/refs.go), [230-247](../integrator/refs.go)). Any
      unparseable outcome blob is treated by readers as `decision: "unknown"` (`outcome-unreadable`
      in `valley status`; [rebase/outcome.go:30-52](../rebase/outcome.go),
      [status/status.go:395-396](../status/status.go)).

### Plan graph and knots (`refs/the-valley/knots`)

The plan graph lives at the exact ref `refs/the-valley/knots` as a linear chain of commits (`P = []`
on create, `P = [old]` on update) whose tree holds canonical Markdown files with YAML frontmatter
for knots and comments ([knot/contract.go:1-214](../knot/contract.go),
[knot/canon.go:13-405](../knot/canon.go), [knot/model.go:19-139](../knot/model.go)). Knots take
their name from the knotted cords used to keep records before writing.

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
blocked_by: [bd-89abcde, "cosmo:oc-0123456"]
changes: ["valley:the-valley/nxvpwlkmqtsrzyoukklnpmqrstuvwxyz"]
---

Write `docs/reference.md` and link it from [[oc-7654321]].
```

- **Tree layout and filenames ([knot/contract.go:9-16](../knot/contract.go),
  [133-143](../knot/contract.go)):**
  - Knot files (`100644`, `<= 64 KiB`, `<= 5,000` knots total):
    - `outcomes/oc-<7-hex>-<slug>.md` (`type: outcome`, statuses: active `open`, `in-progress`;
      terminal `done`, `abandoned`)
    - `bugs/bd-<7-hex>-<slug>.md` (`type: bug`, statuses: active `open`; terminal `closed`)
    - `ideas/ida-<7-hex>-<slug>.md` (`type: idea`, statuses: active `raw`, `exploring`, `adopted`;
      terminal `graduated`, `superseded`, `discarded`)
    - `questions/qn-<7-hex>-<slug>.md` (`type: question`, statuses: active `open`; terminal
      `answered`, `dropped`)
  - `<slug>` matches `^[a-z0-9]+(-[a-z0-9]+)*$` (`1..48` bytes, `MaxSlug = 48`), and `<id>` is
    `<prefix>-` followed by the first 7 lowercase hex digits of `SHA-256(<slug>)`
    ([knot/id.go:12-71](../knot/id.go)). Every knot must have `created > "2026-08-05"`
    (`IDRuleCutoff`; [knot/contract.go:104-110](../knot/contract.go)).
  - Comment files (`100644`, `<= 16 KiB`, `<= 20,000` comments total):
    - `comments/<knot-id>/<YYYYMMDDTHHMMSSZ>-<16-hex-op>.md`
    - Frontmatter is one line (`author: "<principal>"`) between `---` fences, followed by a blank
      line and a required non-empty Markdown body ([knot/canon.go:350-404](../knot/canon.go)).
- **Canonical text and body rules ([knot/canon.go:49-63](../knot/canon.go),
  [138-143](../knot/canon.go), [308-324](../knot/canon.go)):**
  - Every knot and comment file must be valid UTF-8 with no control characters except `\n` (`0x0a`)
    and `\t` (`0x09`), and no `U+FFFE` or `U+FFFF` (`RuleText`).
  - Frontmatter opens with `---\n` on line 1 and closes with `---\n`. When a knot has a non-empty
    body, the body is separated from the closing `---\n` by exactly one blank line (`\n`), has no
    leading or trailing blank lines, and ends with a single `\n`; when the body is empty, the file
    ends immediately after the closing `---\n` (`RuleCanonical`).
- **Commit rules ([knot/commit.go:10-100](../knot/commit.go),
  [valleyhook/knots.go:108-175](../valleyhook/knots.go)):**
  - Commit size `<= 16 KiB`, `<= 32` changed paths per commit, `Committer` name must equal the
    pushing principal, and the commit message must end with the trailer
    `Valley-Op: <16-lowercase-hex>`.
  - No knot or comment file may be deleted, no comment file may be modified, and a knot's `id`,
    `type`, `created`, and `created_by` are immutable after creation. On a newly created knot or
    comment, `created_by` / `author` must equal the pushing principal.
- **Canonical frontmatter fields in required order ([knot/model.go:106-139](../knot/model.go),
  [knot/canon.go:13-43](../knot/canon.go)):**

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
| 15    | `graduated_into` | `idea`                       | bare token        | Required when `status == "graduated"` and forbidden otherwise; must name an existing local `outcome` (`oc-*`) or `question` (`qn-*`) in the live plan graph ([knot/graph.go:395-404](../knot/graph.go)).           |

### CLI JSON outputs

Five `--json` commands emit versioned JSON documents for scripts and front ends.

#### `valley-status/v1`

`valley status --json <change>` emits one `valley-status/v1` object on a single line
([status/render.go:153-235](../status/render.go)).

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

- **Fields ([status/render.go:153-186](../status/render.go)):**
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
    `awaiting-new-decision`, `stale`, `rejected`, `refused-not-ready`, `refused-merge`, or `failed`
    ([status/status.go:384-423](../status/status.go)).
  - `change_id`, `handle`, `title`, `owner`, `landing` (`string` or `null`) and `version` (`int` or
    `null`): Populated for a recorded change (`landing` is `"landed"`, `"landed-recorded"`,
    `"uncertain"`, or `"not-landed"`); `null` for an unrecorded branch.
  - `restack` (`object` or `null`):
    `{ "ref": <string>, "commit": <oid>, "current": <bool>, "report": <RestackReport> }` when the
    change has a valid restack report on `origin` ([status/restack.go:21-34](../status/restack.go)).
  - `knots` (`array` of `{ "id", "type", "status", "title" }`): Knots in `refs/the-valley/knots`
    whose `changes` field links `valley:<project>/<change_id>`, active knots first (`[]` when none
    or for an unrecorded branch; [status/does.go:24-31](../status/does.go)).

#### `valley-changes/v1` and `valley-pending/v1`

`valley changes --json` emits `valley-changes/v1`, and `valley pending --json` emits
`valley-pending/v1` using the same repository and change structure
([status/list.go:44-103](../status/list.go), [status/pending.go:94-105](../status/pending.go)).

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

- **Top-level fields ([status/list.go:45-60](../status/list.go)):**
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
- **`jsonChange` fields ([status/list.go:62-97](../status/list.go)):**
  - `change_id`, `handle`, `target`, `owner`, `state`, `record` (record commit OID), `seq`,
    `active`.
  - `versions` (`array` of
    `{ "n", "head" (40-hex OID without git-sha1: prefix), "title", "branch", "uploader", "landing" ("landed"|"landed-recorded"|"uncertain"|"not-landed"), "landed_commit" (<oid or null>), "request" ({"ref","commit"} or null), "outcome" ({"decision","head","tip"} or null) }`).
  - `includes` (`array` of `string`): Change IDs of other changes in the candidate set (all changes
    in `valley-changes/v1`; unlanded changes in `valley-pending/v1`) whose active version head is a
    strict ancestor of this change's active version head in `main..head`
    ([status/list.go:555-605](../status/list.go)).
  - `restack` (`jsonRestack` or `null`) and `knots` (`array` of
    `{ "id", "type", "status", "title" }`).

#### `valley-plan/v1`

`valley plan --json` emits one indented `valley-plan/v1` JSON document
([status/plan.go:280-303](../status/plan.go), [845-908](../status/plan.go)).

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

- **Fields ([status/plan.go:847-875](../status/plan.go)):**
  - `schema`: `"valley-plan/v1"`.
  - `now` (`string`): UTC evaluation timestamp (`RFC 3339`).
  - `operator` (`string` or `null`): Principal from `--owner` or `git config valley.principal`.
  - `projects[]`: Each element has:
    - `project` (`string`), `error` (`string` or `null`), `tip` (`string`, commit OID of
      `refs/the-valley/knots` or `""` when the project has no graph).
    - `roots` (`array` of `string`): Root `outcome` knot IDs in tree order (`created`, `title`,
      `id`).
    - `knots` (`array` of `planKnot`): Every knot in tree walk order. Each `planKnot` embeds
      [`jsonKnot`](#valley-knot---json-valley-knotv1-valley-knotsv1-valley-knot-opv1-valley-knot-schemav1)
      (without `body` or `comments`, with `children` ordered in tree order and `progress` counted
      over all descendants) plus:
      - `state` (`string`): `"ready"`, `"in-progress"`, `"blocked"`, `"active"`, `"done"`, or
        `"abandoned"` ([status/plan.go:396-415](../status/plan.go)).
      - `edges` (`array` of `planEdge`): Resolved cross-project references from `blocked_by` and
        `target`, each
        `{ "field": "blocked_by"|"target", "ref": "<project>:<id>", "project": <string>, "id": <string>, "state": "resolved"|"dangling", "why": "missing"|"unreadable"|null, "status": <string or null>, "title": <string or null> }`
        ([status/plan.go:290-303](../status/plan.go)).
    - `waiting_on_you` (`object` or `null`):
      `{ "knots": [<id>...], "changes": [{ "repo", "handle", "change_id", "version", "title", "head" }...], "changes_error": <string or null> }`
      (`null` when `error != null`). `knots` is `null` when `operator` is `null`, and otherwise
      lists active `outcome`/`bug` and open `question` knots owned by `operator`.
    - `in_flight` (`array` of `string` or `null`): Claimed active knot IDs ordered by oldest
      `claimed_at` first.
    - `open_residuals` (`array` of `{ "parent": <id or null>, "bugs": [<id>...] }` or `null`): Open
      `bug` knots grouped by `parent`.

#### `valley knot --json` (`valley-knot/v1`, `valley-knots/v1`, `valley-knot-op/v1`, `valley-knot-schema/v1`)

`valley knot <verb> --json` emits one of four indented JSON schemas depending on the subcommand
([status/knot.go:1007-1288](../status/knot.go), [knot/derive.go:88-257](../knot/derive.go)):

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
  - `valley knot show <id> --json`:
    `{ "schema": "valley-knot/v1", "project": <string>, "tip": <oid>, "knot": <jsonKnot with body & comments> }`
    ([status/knot.go:1104-1111](../status/knot.go)).
  - `valley knot list --json`:
    `{ "schema": "valley-knots/v1", "project": <string>, "tip": <oid>, "knots": [<jsonKnot without body & comments>...] }`
    ([status/knot.go:1208-1218](../status/knot.go)).
  - `valley knot (new|set|link|unlink|edit|comment|claim|unclaim) --json`:
    `{ "schema": "valley-knot-op/v1", "project": <string>, "op": <16-hex-op-id>, "commit": <oid>, "knot": <jsonKnot with body & comments> }`
    ([status/knot.go:1007-1020](../status/knot.go)). When a write subcommand is a no-op (for
    example, `set` or `claim` leaves the knot unchanged), it prints a note to `stderr`, pushes
    nothing, and emits no JSON on `stdout` ([status/knot.go:225-227](../status/knot.go)).
  - `valley knot schema --json`:
    `{ "schema": "valley-knot-schema/v1", "ref": "refs/the-valley/knots", "types": [...], "fields": [...], "edges": [...], "limits": { "knots": 5000, "comments": 20000, "knot_bytes": 65536, "comment_bytes": 16384, "changed_paths_per_commit": 32, "title_bytes": 200, "list_items": 64 } }`
    ([knot/derive.go:211-257](../knot/derive.go)).
- **`jsonKnot` fields ([status/knot.go:1028-1075](../status/knot.go),
  [knot/derive.go:91-133](../knot/derive.go)):**
  - Stored fields: `id`, `type`, `status`, `active` (`bool`), `title`, `created`, `created_by`,
    `source` (`omitempty`), `parent` (`omitempty`), `owner` (`omitempty`), `blocked_by`
    (`[]string`), `claimed_by` (`omitempty`), `claimed_at` (`omitempty`), `changes` (`[]string`),
    `target` (`omitempty`), `graduated_into` (`omitempty`), `path`, `comment_count` (`int`).
  - Full-only fields (included in `show` and write ops, omitted in `list` and `plan`): `body`
    (`string`), `comments` (`array` of `{ "author", "at" (RFC 3339), "op" (16-hex), "body" }`,
    `omitempty` — omitted when the knot has `0` comments; [status/knot.go:1047](../status/knot.go),
    [1068](../status/knot.go)).
  - Derived `knot.View` fields: `ready` (`bool`), `waiting_on` (`[]string`), `cleared_by_ending`
    (`[]string`), `dangling` (`[]string`), `unresolved` (`[]string`), `children` (`[]string`),
    `blocks` (`[]string`), `progress` (`{ "done": <int>, "of": <int> }`, `omitempty`), `claim`
    (`{ "by": <string>, "at": <rfc3339>, "age_seconds": <int64>, "stale": <bool, true when age > 24h> }`,
    `omitempty`).

---

## CUE configuration schemas

Six CUE schemas under `schema/` validate host declarations, verification policies, attestation
statements, identity registries, knowledge/plan nodes, and bus events. What an instance writes in
its CUE files is a `deployment choice`; the schema constraints below are `implemented`.

### Host configuration (`schema/valley.cue`)

[`schema/valley.cue`](../schema/valley.cue) (`package valley`) defines `#Host`, the declaration of
projects, ref protection, ref grants, mirrors, and offsite backup for a valley host
([schema/valley.cue:1-203](../schema/valley.cue)).

```sh
cue vet -c schema/valley.cue valley.cue
```

| Definition / field                             | Type / default                                 | Rules                                                                                                                                                                                                                                                                                                               |
| :--------------------------------------------- | :--------------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `projects.<name>` (`#Project`)                 | `[=~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"]: #Project` | Top-level map of hosted projects. Only `projects` and `backup` are permitted at top level ([schema/valley.cue:17](../schema/valley.cue), [191-202](../schema/valley.cue)).                                                                                                                                          |
| `projects.<name>.git.enable`                   | `bool \| *true`                                | Whether the host serves `<name>.git`. Disabling leaves existing data on disk untouched ([schema/valley.cue:30-35](../schema/valley.cue)).                                                                                                                                                                           |
| `projects.<name>.mirrors`                      | `[...string] \| *[]`                           | Push-mirror URLs receiving `refs/heads/main` and `refs/tags/*` on updates ([schema/valley.cue:37-47](../schema/valley.cue)).                                                                                                                                                                                        |
| `projects.<name>.protection` (`#Protection`)   | optional                                       | When omitted, no refs in `<name>` are protected. When present: `refs` (`[#RefPattern, ...#RefPattern] \| *["refs/heads/main"]`, where `#RefPattern` is `=~"^refs/[^ ~^:?\\\\[]+$"`) and `writers` (`[...#PrincipalName] \| *[]`; [schema/valley.cue:49-53](../schema/valley.cue), [119-149](../schema/valley.cue)). |
| `projects.<name>.grants.<grant>` (`#RefGrant`) | `[#GrantName]: #RefGrant`                      | Opens ref patterns outside `refs/heads/`, `refs/replace/`, `refs/notes/`, and `refs/the-valley/` (`#GrantPattern`: `#RefPattern & !~"^refs/(heads\|replace\|notes\|the-valley)/"`) to `writers: [#PrincipalName, ...#PrincipalName]` ([schema/valley.cue:55-82](../schema/valley.cue)).                             |
| `backup` (`#Backup`)                           | optional                                       | Host-level backup policy: `enable` (`bool \| *true`), `target` (`"restic-sftp"`), `cadence` (`"nightly"`), `retention` (`{ daily: int & >0 \| *7, weekly: int & >0 \| *4, monthly: int & >0 \| *6 }`; [schema/valley.cue:158-183](../schema/valley.cue)).                                                           |

### Verification policy (`schema/verification.cue`)

[`schema/verification.cue`](../schema/verification.cue) (`package verification`) defines `#Policy`
and composes the instance `floor`, optional project-type `templates`, and the `project` layer into
`policy: floor & templates[type] & project`
([schema/verification.cue:1-345](../schema/verification.cue)).

```sh
cue vet -c schema/verification.cue policy/instance/*.cue policy/project/*.cue
```

| Definition / field                     | Type / default                                    | Rules                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| :------------------------------------- | :------------------------------------------------ | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Top-level fields                       | `floor`, `templates`, `type`, `project`, `policy` | `floor: #Policy`, `templates: [#Name]: #Policy` (with `templates: none: {}` built in), `type: #Name \| *"none"`, `project: #Policy`, and derived `policy: floor & templates[type] & project` (`#Name` is `=~"^[a-z0-9][a-z0-9-]*$"`; [schema/verification.cue:284-344](../schema/verification.cue)).                                                                                                                                                                                                             |
| `#Policy.checks.<name>` (`#Check`)     | `[#Name]: #Check`                                 | Check catalogue. Every check required by any class or `unclassified` in the composed `policy` must be defined in `policy.checks` ([schema/verification.cue:36-43](../schema/verification.cue), [310-324](../schema/verification.cue)).                                                                                                                                                                                                                                                                           |
| `#Check`                               | `runner: "nix" \| "command"`                      | When `runner == "nix"`: requires `attribute: #Name`, forbids `command` and `validity`. When `runner == "command"`: requires `command: =~"^[^\\x00-\\x1f\\x7f]+$"`, forbids `attribute`, and accepts optional `validity?: =~"^[1-9][0-9]*(s\|m\|h\|d)$"` (`#Duration`). Both accept `cost: *"quick" \| "slow"` ([schema/verification.cue:179-240](../schema/verification.cue)).                                                                                                                                   |
| `#Policy.classes.<class>` (`#Class`)   | `[#Name]: #Class`                                 | Path classes: `paths` (`[#Pattern]: bool`, where `#Pattern` is relative to repo root with no leading `/`, `..` segment, or control characters), `requires` (`[#Name]: bool`), and `carry` (`["same-class" \| "exact"]: bool`, resolving to strictest of `exact`, `same-class`, or default `same-paths`; [schema/verification.cue:81-174](../schema/verification.cue), [254-265](../schema/verification.cue)). Concrete `true` in `floor` is mandatory; `bool \| *true` in `templates` is an overridable default. |
| `#Policy.unclassified`                 | `[#Name]: bool` (`#Requires`)                     | Checks required when a changed path matches no class in `policy.classes` ([schema/verification.cue:62-67](../schema/verification.cue)).                                                                                                                                                                                                                                                                                                                                                                          |
| `#Policy.restack.maxAutomaticVersions` | `int & >=1 \| *64`                                | Cap on automatic restack versions in the policy schema (`schema/verification.cue:69-77`; read by `valley-restacker once --report-only`, while automatic restack writes are `not built`).                                                                                                                                                                                                                                                                                                                         |

### Attestation statement (`schema/attestation.cue`)

[`schema/attestation.cue`](../schema/attestation.cue) (`package attestation`) validates the JSON
representation of `the-valley/attestation/v1` statements before rendering or during `attest verify`
([schema/attestation.cue:1-356](../schema/attestation.cue)).

```sh
cue vet -c schema/attestation.cue statement.json
```

- **Top-level fields:** `statementType: "the-valley/attestation/v1"`, `subject: #Subject`
  (`primary: "valley-tree-v1"`, `digest: #DigestSet`), `predicateType: #PredicateType`, `predicate`
  (`#PureCheck`, `#EffectfulCheck`, or `#Transfer` selected by `predicateType`), and optional
  `provenance?: #Provenance` ([schema/attestation.cue:317-355](../schema/attestation.cue)).
- **Definitions ([schema/attestation.cue:34-308](../schema/attestation.cue)):**
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
`known-signers`, `authorized_keys`, and `grants`
([schema/identity.cue:1-224](../schema/identity.cue),
[identity/render.go:1-215](../identity/render.go)).

```sh
cue vet -c schema/identity.cue examples/identity/registry/*.cue
```

| Definition / field                 | Type / default         | Rules                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| :--------------------------------- | :--------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `boundaries.<name>` (`#Boundary`)  | `[#Name]: #Boundary`   | Enforcement boundaries run by the instance (`#Name`: `=~"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"`). Every boundary has `kind: "git-push" \| "registry" \| "request" \| "restack"`. Every `boundary` referenced by a principal's grant must exist in `boundaries` ([schema/identity.cue:110-144](../schema/identity.cue), [177-180](../schema/identity.cue)).                                                                                                                                                                                                                                                                                                    |
| `principals.<name>` (`#Principal`) | `[#Name]: #Principal`  | Named principals: `kind: "human" \| "machine" \| "service"`, `keys: [#Key, ...#Key]`, `grants: [#Name]: #Grant` (`{ boundary: #Name }`), `external: #External \| *null` (`{ kind: "domain" \| "did", handle: =~"^[^\\s]+$" }`), and `expires?: =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"`. `expires` is **required** whenever `kind != "human"` or `external != null` ([schema/identity.cue:30-64](../schema/identity.cue)).                                                                                                                                                                                                                                   |
| `#Key`                             | `class: "ssh-ed25519"` | `bound: "hardware" \| "host"`, `public: =~"^ssh-ed25519 [A-Za-z0-9+/]+={0,2}( [^\\n]*)?$"`, and optional `signs?: =~"^[^\\s+]+$"` (when set, `identity compile` emits a verifier key in `known-signers`; [schema/identity.cue:66-89](../schema/identity.cue)). Hardware-key (`sk-*`) classes are `not built`.                                                                                                                                                                                                                                                                                                                                         |
| `genesis` and governance checks    | `genesis: #Name`       | `genesis` must be a key in `principals` that holds at least one grant at a boundary with `kind == "registry"` (`genesisGovernance: [_, ...]`), and no principal with `external != null` may hold a `registry` boundary grant (`externalGovernance`; [schema/identity.cue:185-211](../schema/identity.cue)). At compile time, `identity compile` omits principals with `expires <= now` and fails if no unexpired principal holds a `registry` grant, if two principals holding `git-push` grants share a public key, or if a `git-push` key conflicts with a host-declared `authorizedKeys` tag ([identity/render.go:65-145](../identity/render.go)). |

### Node frontmatter and bus events (`schema/node.cue`, `schema/events.cue`)

Two additional CUE schemas validate knot frontmatter and NATS bus payloads:

```sh
cue vet -c -d '#Node' schema/node.cue frontmatter.yaml
cue vet -d '#RefUpdated' schema/events.cue payload.json
```

- **[`schema/node.cue`](../schema/node.cue) (`package knowledge`):** Defines `#Node`, `#Graph`, and
  `#Directories` ([schema/node.cue:1-205](../schema/node.cue)). The `knot-agree` flake check
  verifies that `#Node` and `knot/` enforce the same frontmatter rules (note that
  `#GraduationTarget` in `schema/node.cue:156` also admits `*.md` paths for tree-based test graphs,
  whereas the live plan graph in `knot/graph.go:395-404` requires `graduated_into` to name a local
  `oc-*` or `qn-*` knot; historical nodes under `archive/.the-valley/` are frozen read-only history
  and are not checked against today's schema).
- **[`schema/events.cue`](../schema/events.cue) (`package valley`):** Defines the three NATS event
  payloads published under `valley.git.<repo>.<event>` when the optional host event bus is enabled
  (`services.valley.bus.enable`, a `deployment choice`; publication is best-effort and never blocks
  a push or landing; [schema/events.cue:1-126](../schema/events.cue)):
  - `#RefUpdated`: `{ event: "ref-updated", repo, ref, old, new }` (published by the host's
    post-receive publisher hook, by the integrator on landing, and by `valley replay`).
  - `#IntegrationSucceeded`:
    `{ event: "integration-succeeded", repo, change, target, old, new, transferred: [...#CheckName] }`
    (published by the integrator when a request lands).
  - `#RequestStale`:
    `{ event: "request-stale", repo, change, target, tip, reason: "conflict" | "evidence", checks: [...#CheckName] }`
    (published by the integrator when a request cannot land because of a merge conflict or
    non-transferring check evidence).
