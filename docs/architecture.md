# Architecture as deployed

This document describes what the-valley runs today, read from the code, and the architectural
principles its design follows. It makes four kinds of statement. A principle is a rule the design
follows, with the reason for it; the code that carries it out is cited like any other behaviour.
Behaviour is what the code does, cited to the files that do it. A deployment record reports an
observation of a host, and names the record and its date. A guarantee names the flake check
(`checks.x86_64-linux.<name>`) or test that demonstrates it; a statement of behaviour with no named
check is unchecked. Security and rollouts are in [security.md](./security.md), planned work lives in
[the plan graph](./concepts.md#records-project-storage-the-plan-graph-and-knots) (`valley plan`),
and the project's purpose and principles are in [purpose.md](./purpose.md).

## Overview

A valley host keeps bare git repositories and accepts changes to them over ssh. Anyone with a key
may push a topic branch. A protected branch such as `main` accepts a push only from the principals
the project names as its writers. The integrator moves it by landing an integration request. The
integrator lands a request when every check the project's policy requires is covered by a signed
statement that the check passed.

The parts are:

- the host module, which installs everything below (`nix/valley-host.nix`);
- the push hook, which decides what a push may write (`valleyhook/`);
- the integrator, one controller per protected project (`integrator/`);
- `attest`, which runs checks and signs the evidence (`attest/`, `note/`);
- the identity compiler, which turns a registry into keys and grants (`identity/`);
- the `valley` command line tool (`bin/valley`);
- `valley-restacker`, the restacker, which plans and builds its writes without pushing them, and
  which nothing deploys yet (`restack/`);
- the review record and its history, which nothing writes or admits yet (`review/`);
- `sigverify`, a hardware-key signature verifier that nothing calls yet (`sigverify/`).

## The source and project storage

A project keeps its data in one of two places. The source is the project's tree: its code,
configuration, policy, schemas and docs. Project storage sits beside the source, in the same store,
and holds what is about the project rather than part of it. This section states which data belongs
where. The principles hold for any store. How this deployment realises them in git comes last.

**Data that is true of a version of the project belongs in the source.** Code, configuration,
policy, schemas and docs are each true of the version that holds them. They fork when the source
forks, so a release's docs describe that release. A change governs them. This is why the integrator
reads a change's policy from the target's tip: policy is true of the version it governs
(`integrator/policy.go`).

**Data about the project, rather than of one version, belongs in project storage.** Any one of these
reasons puts it there:

- It needs one truth across every line of the source, as the plan does.
- It describes a version, so it cannot live inside that version. An approval or a check's evidence
  for a tree cannot be part of that tree. Change records, attestations, integration requests and
  their outcomes are kept beside the source for this reason.
- A write to it has no effect to gate, as with a knot's status.

**Project storage shares the source's substrate, not its governance.** It uses the project's store,
transport and backup, the one policy point at the host, and the project's principals. Each kind of
data in it is held to rules that match what a write to it can do. This deployment shows the range:

- An attestation can let a change land. So the hook admits one only when each of its notes verifies
  under a known signer and is about the tree the ref names, and never lets it be rewritten
  (`valleyhook/attestation.go`).
- A change record can only refuse. So the hook allows only the transitions in
  [Change records](#change-records), and the integrator reads records only to refuse
  (`valleyhook/record.go`, `integrator/records.go`).
- A knot informs and decides nothing. So any named principal may write one, and the hook checks the
  graph's shape and its history: one commit on the old tip, and never a rewind
  (`valleyhook/knots.go`).

A change to the source has effects. So it lands only when it is approved and every check it requires
has passed (see [The integrator](#the-integrator)).

**Knots can direct work but never authorize it.** A knot can ask for work. That work reaches the
source only as a change, under the source's rules. Nothing that decides a change reads a knot.

**A settled answer crosses over.** While a question is open, it lives in project storage as a knot.
When its answer settles something about the design, the answer enters the source's docs through a
change, and the knot links that change.

**Project storage shares the project's readers.** Whoever can read the source can read project
storage, and the reverse. Data that needs different readers belongs to a different project.

The source says what is; its history, how it came to be; the knots, where it goes.

In this deployment, project storage is the refs under `refs/the-valley/` in the project's git
repository, beside the source's `refs/heads/`. [What a push may write](#what-a-push-may-write) says
which namespace holds what and who may write it, and [The plan graph](#the-plan-graph) describes the
knots. A plain `git clone` fetches branches and tags, so it carries the source and no project
storage. The tools that read project storage fetch it by name: `valley knot` and `valley plan` list
`refs/the-valley/knots` with `ls-remote` and fetch the commit it names, and `valley changes` and
`valley status` read change records the same way (`status/knot.go`, `status/changes.go`). The
mirrors receive `main` and tags only, so no project storage reaches a mirror
(`nix/valley-host.nix`). The host's backup copies the whole data directory, `/srv/git` by default,
so it holds project storage too. The readers follow from git. Anyone who can read a repository can
fetch the objects of every ref in it, and git's own documentation says that even its namespaces are
not effective for read access control (`gitnamespaces(7)`, SECURITY).

## The host module

The NixOS module `services.valley` reads one CUE file, `services.valley.config`. The build vets it
against `schema/valley.cue` and fails on any error (`nix/valley-host.nix`). The file declares the
projects, their mirrors, their protection and the backup policy. Checked by `module-eval` and
`cue-vet`.

### Repositories and the git user

Each project is a bare repository at `/srv/git/<name>.git`. All ssh git traffic logs in as one Unix
user, `git`. The git user's login shell runs `valleyhook shell` (`nix/valley-host.nix`,
`valleyhook/shell.go`). For each session, `valleyhook shell` does three things:

1. For a push only, it refuses the session when the hold file `/srv/git/.valley-hold` exists, or
   when the converged record `/srv/git/.valley-converged` does not match the running configuration.
   Fetches stay open. Both are checked when the session starts, so a push already past this check is
   not stopped by a hold created later.
2. It drops every `GIT_*` variable and any `VALLEY_PRINCIPAL` the client sent. It sets
   `VALLEY_PRINCIPAL` from the tag on the authorized-keys entry of the key that logged in. An
   untagged or ambiguous key gets no principal.
3. It runs `git-shell` with the same arguments. `git-shell` limits the session to git's own
   commands.

The sshd `Match User git` block turns off TCP, agent and X11 forwarding and tunnels, and sets
`ExposeAuthInfo` so the shell can see which key logged in. It leaves stream-local (Unix socket)
forwarding on. cosmo adds its own block with `DisableForwarding yes` and `PermitTTY no` (cosmo
`hosts/classic-laddie/default.nix`). Build-time assertions refuse an sshd config that would let a
client set `VALLEY_PRINCIPAL` or `GIT_*` (`nix/valley-host.nix`). Checked by `ssh-e2e` (a real sshd
in a VM), `module-eval` and `valleyhook-unit`.

### valley-init and the push hold

`valley-init` is a oneshot service that runs as git (`nix/valley-host.nix`). It first deletes the
converged record, so pushes are refused while it runs. It then creates any missing repository and
links each repository's hooks. A foreign `pre-receive` hook or any `core.hooksPath` setting is a
conflict. When there is no conflict, it writes the converged record. The record is the first 32 hex
characters of a SHA-256 of the init script. So pushes resume only when every repository's
`pre-receive` hook is this configuration's. A hand-written `post-receive` hook is left in place and
is not a conflict. Checked by `init-e2e` and `ssh-e2e`.

A host with an instance repository also publishes its project list there. `valley-init` writes a
blob at `refs/the-valley/projects` that names every enabled git project, one per line, sorted, and
nothing else. Clients read it to find the host's projects (`valley changes --all`). It discloses
every project name to whoever can read the instance repository. A host without an instance
repository publishes nothing. A list that cannot be written is a warning, not a conflict. Checked by
`init-e2e` and `valley-changes`.

The hold file belongs to the operator, who creates it to pause all pushes. `valley-init` never
touches it.

### The signers publication

The host publishes what a client verifies the integrator's transfer statements with. The publication
is a tree at `refs/the-valley/signers` in the instance repository. It holds two files:
`known-signers`, the known-signers file the controllers read, and `integrator-name`, the name they
sign under, on one line. Both are in one tree under one ref, so a client reads both values or
neither. A host that restricts signers to projects (see
[Evidence: attest and notes](#evidence-attest-and-notes)) adds a third file, `signer-projects`, the
signer projects file the controllers read. A signer that file names, a worker or another controller,
is never this host's integrator. So while it names the integrator name a client uses, the client
verifies no transfer statement, and a publication whose `signer-projects` does not read is not used
(`status/signers.go`, `rebase/snapshot.go`). The restacker applies the same rule to the file it is
given as `--signer-projects`. Approval reads the published file too: it reuses a note only when one
of its signers counts in the project (`status/coverage.go`).

The publisher is `integrator publish-signers` (`integrator/signers.go`). The oneshot service
`valley-signers` runs it as git (`nix/valley-host.nix`). A host renders the service when the
integrator is enabled and the host serves the instance repository (`integrator.instanceProject`).
The publisher is given the same signers file, the same `--name` and the same `--signer-projects` as
the controllers. When `integrator.signingName` is unset, neither is given a name. Both then take the
integrator's default, `<hostname>/attestations`, computed by the same code on the same host. The
service's sandbox lets it write the instance repository and nothing else.

The service runs at boot and on activation, after `valley-init` and after the registry compiler when
there is one. The signer projects file is rendered into the Nix store, so a changed list is a
changed command for the service, which activation runs again. A path unit, `valley-signers.path`,
runs it again whenever the signers file changes, as it does when a compilation replaces the file. A
run that would publish what is already there writes nothing. A run that changes the publication
moves the ref with a compare-and-swap.

Two runs at once take turns. Each run holds a lock in the instance repository from reading the
signers file to moving the ref. So the later run reads the file after the earlier one has published,
and a run that read an older file cannot publish it over a newer one.

A run that cannot read the signers file, or reads one that is not a list of verifier keys, fails and
leaves the last publication in place. A file that holds no key under the signing name is published
anyway, because it is what the controllers use. That includes a file with no key at all, which is
what the registry compiles to once every signing key is revoked. Clients then stop verifying with
the revoked keys. The run warns that no client can verify a statement with the file.

Only the host's machinery writes the ref, never a push. The hook refuses every push to a
`refs/the-valley/*` ref it does not name. On the host, the publisher writes the ref. The integrator
can write it too: the instance repository is group-writable, and the integrator is in that group
because it lands code there. Checked by `module-eval`, `signers-e2e` and `publish-e2e`.

## What a push may write

Every repository's `pre-receive` hook runs `valleyhook pre-receive` (`valleyhook/main.go`,
`valleyhook/policy.go`). The hook is default-deny. If any one ref update is refused, the whole push
is refused. A push may update at most 1000 refs, and the hook refuses one that updates more before
it decides any ref. It asks `git symbolic-ref` whether each pushed ref is symbolic, so it reads a
symbolic ref exactly as git does. Checked by `protect-e2e`, `ssh-e2e` and `valleyhook-unit`, and for
the plan graph by `knots-e2e`.

| Ref namespace                                       | Who may push                                                                                     | Rule                                                                                                                                                                    |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refs/heads/*` (topic branches)                     | Any key                                                                                          | Create, update and delete.                                                                                                                                              |
| Protected refs (default `refs/heads/main`)          | Principals in `protection.writers` (default empty)                                               | The integrator moves them on disk. cosmo names `integrator` as a writer of both its projects.                                                                           |
| `refs/the-valley/attestations/<tree>/<key-hash>`    | Any key                                                                                          | Create only. The ref must name a tree of notes within size bounds, and each note must verify under a known signer and be about that tree (`valleyhook/attestation.go`). |
| `refs/the-valley/integration-requests/<target>/<c>` | Principals with the `approve` grant (also accepted as `request`)                                 | Create, update and delete. A change with a record is requested as `c-<change-id>` (see [The integrator](#the-integrator)).                                              |
| `refs/the-valley/changes/main/<change-id>`          | Any named principal creates; the owner, `approve` or `restack` holders update; `approve` deletes | A change record. The hook allows only the transitions in [Change records](#change-records) (`valleyhook/record.go`). The integrator reads it to refuse.                 |
| `refs/the-valley/restack/main/<change-id>`          | Principals with the `restack` grant                                                              | A restack report about a change the repository records. Created and extended, never deleted (see [Restack reports](#restack-reports), `valleyhook/report.go`).          |
| `refs/the-valley/knots`                             | Any named principal                                                                              | The plan graph, that name exactly. One commit on the tip that keeps the graph valid; never deleted (see [The plan graph](#the-plan-graph), `valleyhook/knots.go`).      |
| `refs/the-valley/projects` (instance repository)    | Nobody                                                                                           | `valley-init` writes it on disk: the host's project list (see [valley-init](#valley-init-and-the-push-hold)).                                                           |
| `refs/the-valley/signers` (instance repository)     | Nobody                                                                                           | `valley-signers` writes it on disk, and the integrator can: what clients verify transfer statements with (see [The signers publication](#the-signers-publication)).     |
| Every other `refs/the-valley/*`                     | Nobody                                                                                           | This covers the integrator's outcome records, `refs/the-valley/integration-outcomes/<target>/<c>`, and review histories (see [Reviews](#reviews)).                      |
| `refs/replace/*`, `refs/notes/*`                    | Nobody                                                                                           | A replace ref would let git show other content under a watched name.                                                                                                    |
| Symbolic refs                                       | Nobody                                                                                           |                                                                                                                                                                         |
| Tags and every other ref                            | Principals named in a project grant: `grants.<name>: {refs, writers}` (`schema/valley.cue`)      | Grant patterns cannot name `heads`, `replace`, `notes` or `the-valley`.                                                                                                 |

Branches and tags hold the source. The namespaces under `refs/the-valley/` are project storage, as
[The source and project storage](#the-source-and-project-storage) describes.

The `approve` and `restack` grants each come from two places: the host's declared
`services.valley.grants.approve` and `services.valley.grants.restack`, and the identity compiler's
`grants` file. The two sources add up. The `approve` grant is also accepted as `request`, and the
two names are one grant. Its holders are the principals named under either name, in
`services.valley.grants.approve`, in `services.valley.grants.request`, or in the registry. Both
writers write each holder as a `request <principal>` line, and the hook reads a line naming either
name as the `approve` grant (`valleyhook/grants.go`). The `restack` grant is the host restacker's.
It authorises two writes and nothing else: one version added to another principal's ready change,
and the change's restack report. It grants nothing on integration requests. Why replace refs are
closed is recorded in
[bd-75c8721](../archive/.the-valley/bugs/bd-75c8721-pushes-could-write-any-namespace.md).

### Change records

A change is one piece of work aimed at `main`. Its ID is stable across rebases, in jj's form: 32
letters from `k` to `z`. Each time a head of the change is marked ready, the change gains a version.
The change's record lists the versions, and the hook guards every write to it. A record grants
nothing: the integration request is still the only way a change reaches `main`. The integrator reads
records only to refuse. It lands no request that would bring a version of a change that is not
`ready` (see [The integrator](#the-integrator)).

A change is in one of three states:

- `ready`: the change is offered for landing. A change is created ready.
- `draft`: the change is paused, by `valley unready`. A draft voids nothing. When it is readied
  again, the approvals of its active version still stand.
- `withdrawn`: the change is given up. Withdrawing voids everything outstanding on the change: its
  integration requests, and the approvals and reviews of every version. A change brought back from
  withdrawn always needs fresh approvals.

The integrator enforces the pause. It refuses a request that would bring a version of a draft or a
withdrawn change, so a request filed before the change was paused or withdrawn stays where it is and
does not land. A draft's request lands once the change is ready again, which is what a pause means.

The integrator also voids a withdrawn change's requests. On each pass it deletes the request named
`c-<change-id>`, and every request whose head is one of the change's version heads. So readying the
change again needs a fresh approval. A withdrawal followed by a ready within one pass is not seen,
and can leave an old request in place. So `valley withdraw` warns when origin holds a request for
the change, and `valley unready` notes one. The request is looked for under `c-<change-id>`, and
under the name a branch's request has, for the branch of each of the change's versions. Only an
`approve` holder may touch a request ref, so the warning says the operator must drop it if the
change is readied first. Approvals are not records yet either; they become records with M1-C.

The ref `refs/the-valley/changes/main/<change-id>` points at a record commit. The commit's tree
holds one entry, `record.json`, as a plain file. Its schema is `valley-change-record/v1`, read by
one strict parser that the hook and the CLI share (`change/record.go`):

```json
{
  "schema": "valley-change-record/v1",
  "change_id": "<32 letters from k to z>",
  "target": "main",
  "owner": "<principal>",
  "state": "ready",
  "seq": 3,
  "versions": [
    {
      "n": 1,
      "head": "git-sha1:<40 lowercase hex>",
      "title": "<the head's subject>",
      "branch": "<branch, or empty>",
      "uploader": "<principal>"
    }
  ],
  "active": 1
}
```

The parser refuses anything that is not valid UTF-8. It also refuses a duplicate key at any depth,
an unknown field, and data after the object. Every field must have the type shown. `seq` runs from 1
to 100000, and `n` and `active` from 1 to 1000. Versions are numbered 1, 2, 3 with no gap. A title
is 1 to 200 bytes with no control characters. `state` is `ready`, `draft` or `withdrawn`.

The record commit's parents tie it to what it records. In the table, `P` is the record commit's
parents, `old` is the ref's value before the push, and `h` is the head of the version being added.

| Transition                | Who                              | `P`        | What the record must say                                                                                                                                                                            |
| ------------------------- | -------------------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Create                    | Any named principal              | `[h]`      | One version: `n` 1, `head` `h`, and the pusher as `uploader`. The pusher as `owner`, `seq` 1, `active` 1, `state` `ready`. `target` and `change_id` are the ref's.                                  |
| New version               | The owner or an `approve` holder | `[old, h]` | `old`'s versions unchanged, plus one with the next `n`, `head` `h` other than the active head, and the pusher as `uploader`. `active` is the new `n`, `state` `ready`, `seq` one more than `old`'s. |
| Unready                   | The owner or an `approve` holder | `[old]`    | Everything as in `old`, except `state` goes from `ready` to `draft` and `seq` goes up by one. `active` stays.                                                                                       |
| Withdraw                  | The owner or an `approve` holder | `[old]`    | Everything as in `old`, except `state` goes from `ready` or `draft` to `withdrawn` and `seq` goes up by one. `active` stays.                                                                        |
| Ready again, same version | The owner or an `approve` holder | `[old]`    | Everything as in `old`, except `state` goes from `draft` or `withdrawn` to `ready` and `seq` goes up by one. `active` stays.                                                                        |
| Ready again, new version  | The owner or an `approve` holder | `[old, h]` | As for a new version, from `draft` or `withdrawn`.                                                                                                                                                  |
| Restack                   | A `restack` holder               | `[old, h]` | As for a new version, from `ready` only. The new version keeps the `branch` and `title` of the version that was active.                                                                             |
| Delete                    | An `approve` holder              | —          | Allowed, to keep storage tidy.                                                                                                                                                                      |

The restack row applies only when the `restack` grant is what authorises the write: the pusher
neither owns the change nor holds `approve`. Such a pusher may write that row and no other. The hook
refuses its write when the change is `draft` or `withdrawn`, when the record changes the state with
one parent, and when it adds more than one version. It also refuses a changed earlier version,
owner, target, change ID, branch or title, and an uploader other than the pusher. A `restack` holder
may not delete a record. A `restack` holder that owns the change, or also holds `approve`, writes by
the other rows. The hook cannot tell a submitted change from a ready one, because a landed change
can stay `ready`; leaving submitted changes alone is the restacker's own selection.

The hook refuses every other write. That includes a key that names no principal, extra parents,
parents in another order, a head that is not a commit, and a tree of another shape. It also includes
a record with one parent that leaves the state as it was, such as `ready` to `ready` without a new
version, and `withdrawn` to `draft`. A withdrawn change comes back ready, never as a draft. The
owner, the target and the change ID never change.

Each record commit is at most 4 KiB, its tree at most 1 KiB, and its `record.json` at most 64 KiB. A
push may update at most 32 records. The hook checks each of these bounds before it reads anything
past it, and the integrator reads records under the same bounds (`change/record.go`).

A repository may hold at most 2000 records, counted with one `for-each-ref`. This bound is soft. A
pre-receive hook runs before git installs any ref, and nothing serializes two pushes' hooks. So
pushes that run at the same time can each count the same records and each create more. The count can
then pass 2000 by at most the record updates those pushes had in flight, which is 32 per push. Once
the count is past 2000, every later create is refused. The integrator reads at most 4000 records,
twice this bound. A repository that holds more lands nothing until records are deleted, because the
integrator could not tell which heads are versions of changes that are not `ready`. Each project has
its own integrator, so no other repository is held up.

The writer numbers versions, and the hook holds them to append-only and gap-free. The integrator
takes this numbering as it is. A change is owned by the principal whose key pushed its first record,
so `klaus-env` owns every agent change.

A record keeps every head it names reachable, because each head is a parent of a record commit. So
records retain source objects the way topic branches do, and the hook restricts them no further.

Checked by `valleyhook-unit`, with a case for each rule of each transition against real git objects,
by `change-unit`, which runs the parser's tests and its fuzz target's seed corpus, by
`valley-ready`, which drives the transitions through real pushes, and for the restack row by
`protect-e2e` and `identity-e2e`.

### Restack reports

A restack report is what the host restacker observed when it tried to rebase a ready change onto
`main`. It records the inputs it read, what came of the attempt, and what it published. A report is
an observation. It grants nothing, and it is not evidence that a version's content is unchanged or
approved.

The ref `refs/the-valley/restack/main/<change-id>` points at the change's latest report commit. The
commit's tree holds one entry, `report.json`, as a plain file. The first report commit has no
parent. Each later one has the report it replaces as its one parent, so the ref keeps the change's
whole history of reports. Its schema is `valley-restack-report/v1`, read by one strict parser that
the hook and the CLI share (`change/report.go`):

```json
{
  "schema": "valley-restack-report/v1",
  "change_id": "<32 letters from k to z>",
  "target": "main",
  "target_tip": "git-sha1:<main as the attempt read it>",
  "operation": "<the restacker's name for the attempt>",
  "result": "conflict",
  "layers": [
    {
      "change_id": "<32 letters from k to z>",
      "record": "git-sha1:<the record commit read>",
      "version": 2,
      "head": "git-sha1:<the active version's head>",
      "branch": "topic",
      "branch_tip": "git-sha1:<origin's topic as read>",
      "published": null
    }
  ],
  "failing_layer": "<change id, or null>",
  "conflict_paths": ["src/main.go"],
  "blocking_requests": [
    {
      "ref": "refs/the-valley/integration-requests/main/<name>",
      "tip": "git-sha1:<the request as read>"
    }
  ],
  "refusal": null
}
```

- `layers` are the changes the attempt rebased together, bottom first: 1 to 64, each change once,
  and the report's own change among them.
- A layer's `head` and `branch` are its active version's, as the record names them. `branch` is
  empty for a version that names no branch.
- A layer's `branch_tip` is where origin's branch was when the attempt read it. It is `null` when
  origin had no such branch, and always `null` when `branch` is empty. It is recorded apart from
  `head` because the two can differ: the branch can be missing, or somewhere other than the head its
  version names.
- `result` is `conflict`, `blocked`, `partial`, `complete` or `superseded`. A conflict names its
  `failing_layer`. A conflict and a block publish nothing. `partial` means the branches moved and
  not every version was added; `complete` means every layer gained a version. `superseded` means the
  attempt was given up because its inputs moved while it ran.
- `published` is `null` for a layer whose branch did not move. Otherwise it is
  `{"head", "version", "record"}`: the head the branch moved to, and the version and record commit
  that recorded it, both `null` when no version was added. A layer with no branch publishes nothing.
- `blocking_requests` are the integration requests that held the attempt back. Each is the request's
  ref, which is on `main`, and `tip`, the object the ref pointed at when the attempt read it. There
  are at most 64, each ref once.
- `conflict_paths` holds at most 100 paths of 1 to 1024 bytes. `refusal` is `null` or 1 to 2048
  bytes. `operation` is 1 to 64 letters, digits, dots, colons, dashes and underscores. No string
  holds a control character.

A report is current while every ref it observed still holds what the report says it held. Those refs
are `main`, each layer's record ref and branch, and each blocking request. The comparison is exact,
and absence counts: a branch the report saw missing must still be missing. A layer with no branch
has no branch to compare. For a layer the attempt published, the branch must be at the published
head, and the record ref at the published record when a version was added. Each comparison uses what
the report recorded of that ref: a branch is compared with its `branch_tip`, never with the
version's `head`. A report that is not current is historical (`rebase/report.go`).

The parser refuses what the record parser refuses: invalid UTF-8, a duplicate key, an unknown field,
a field of another type, and data after the object.

Only a `restack` holder may write a report. The hook refuses a report for any target but `main`, a
ref whose last part is not a change ID, and a change the repository holds no record of. It refuses a
first report with a parent, a later one whose one parent is not the ref's old value, and every
delete. Each report commit is at most 4 KiB, its tree at most 1 KiB, and its `report.json` at most
64 KiB. A push may update at most 64 reports. The hook checks each bound before it reads anything
past it.

Checked by `protect-e2e` and `identity-e2e`, which push reports through the rendered hook, by
`valleyhook-unit`, and by `change-unit`, which runs the parser's tests and its fuzz target's seed
corpus. `valley-status` reads reports through the CLI and checks when each one is current.

## After a push: events and mirrors

Each repository's `post-receive` hook runs every program in `hooks/post-receive.d/`
(`nix/valley-host.nix`). There are two:

- **The bus publisher**, when the bus is on. It publishes one `ref-updated` event for every ref the
  push updated, on the subject `valley.git.<repo>.ref-updated`. Checked by `bus-e2e`.
- **The mirror hook**, when the project declares mirrors. It does not push. It writes the push's ref
  moves, one `<old> <new> <ref>` line each, as a file in the repository's publish queue,
  `valley-publish-queue/`, and returns. A dead mirror therefore never slows or fails a push.

The integrator moves refs with `git update-ref`, which runs no `post-receive` hook. So it does both
jobs itself for each landing (`integrator/bus.go`). It writes the move to the same publish queue. It
also publishes the `ref-updated` event straight to the bus, with the same payload the bus publisher
would send. It publishes only after the landing is fully recorded: the queue entry written, the
evidence stored and the request consumed. Each publish is stopped after 10 seconds, so a bus that
hangs cannot hold up a landing. Checked by `integrator-e2e`.

One unit pushes the mirrors: `valley-publish@<project>`, a service that runs as git. A systemd path
unit starts it whenever the project's queue is not empty. Mirrors are a list of URLs
(`schema/valley.cue`). The service lists the queued files and then runs `git push --prune` of `main`
and all tags to each URL. Tags are copied whether or not `main` reaches them. Because of `--prune`,
a tag deleted on the primary is deleted on the mirror. Nothing else on the mirror is changed. A
mirror holds refs that other writers put there, such as a forge's own namespaces or a bot's
branches, and those are theirs. Deleting a ref that a public mirror has already served would not
unpublish it anyway, because forges keep its objects fetchable by SHA. The push uses the git user's
own ssh identity, which the host provides. Checked by `mirror-e2e`.

A repository that becomes a mirror keeps every ref it already holds. So the operator deletes what
should not be published when declaring the mirror:

```sh
git ls-remote <url>               # list every ref the mirror holds
git push <url> --delete <ref>...  # delete each one that should not be published
```

The service deletes the files it listed only after every mirror push succeeds. When a push or a
deletion fails, the service fails and the files stay. systemd then runs the service again after a
delay that grows from 30 seconds to 15 minutes. A run that lasts 15 minutes is stopped with
everything it started. Checked by `publish-e2e`. Because the integrator publishes its events itself,
a mirror outage never delays an event on the bus.

Having one publisher is what keeps a mirror from being rewound. git reads the local refs before it
contacts the mirror. So when two pushes run at once, one can read an old `main`, stall, and then
force-push that old `main` over a newer one the other pushed meanwhile. systemd never runs two
instances of one unit at the same time, and nothing else pushes the mirrors. A move queued while a
push runs stays in the queue, so the next run pushes the newer `main`. `mirror-e2e` stalls a push in
that window: two pushers at once rewind the mirror, and the service ends at the newer `main`.

That cosmo's deployed hosts run a publish queue is a deployment record: cosmo #934 merged and laddie
healthy, in the handoff of 2026-10-04. At that point the service also published the integrator's
events, and a push ran its own mirror pusher.

The bus is NATS with JetStream, listening on `127.0.0.1:4222`. `valley-bus-init` creates one stream,
`valley`, over the subjects `valley.>`, with file storage and NATS defaults. The events are defined
in `schema/events.cue`: `ref-updated`, `integration-succeeded` and `request-stale`. The bus has no
authentication, so nothing treats an event as authoritative
([bd-d853d9c](../archive/.the-valley/bugs/bd-d853d9c-bus-unauthenticated.md)). The integrator reads
git directly.

## The integrator

Each protected project has one controller, `valley-integrator@<project>`, running as the
`valley-integrator` user in group git (`nix/valley-host.nix`). It makes one pass over the open
requests in order, then sleeps 15 seconds and starts again (`integrator/main.go`,
`integrator/refs.go`). No bound on how long a request waits is checked.

1. **Find requests.** A request is a ref `refs/the-valley/integration-requests/<target>/<change>`
   that points at the change's head commit. A change with a record is requested as `c-<change-id>`.
   Every name of that form is reserved for the change with that ID. Any other name is a branch's:
   the branch with each `/` replaced by `-`.
2. **Void what a withdrawal voids.** The records are read once per pass (see below). A request named
   for a withdrawn change, or whose head is one of a withdrawn change's version heads, is deleted
   with the outcome `voided-withdrawn`. The record is read again just before each deletion, and the
   request is deleted only while the change is still withdrawn. The deletion is leased on the head
   the pass found, so a request filed again since stands.
3. **Skip what is decided.** The outcome record
   `refs/the-valley/integration-outcomes/<target>/<change>` holds the head, the target tip, a hash
   of all attestation refs, and the outcome. If the first three still match, the request is skipped.
   A `failed` outcome is retried after 10 minutes. A refusal from step 4 is never skipped this way.
4. **Refuse what the records refuse.** A request is refused with the outcome `refused-not-ready`
   when either of these holds:
   - it is named `c-<change-id>`, and the record `refs/the-valley/changes/<target>/<change-id>` is
     missing, is not `ready`, or does not list the request's head as one of its version heads;
   - whatever its name, a commit it would bring to the target, from the merge base to the head, is a
     version head of a record that is not `ready`. The outcome names that change. So a child of a
     draft's head, approved under another name, does not land the draft.

   A request whose commits from the merge base with the target to the head include a merge is
   refused with the outcome `refused-merge`. A merge could make other work look landed by ancestry.
   A refused request stays where it is, and no event is published. A refusal is decided again on
   every pass, and reported only when it is new, so a draft readied again is judged on the next
   pass.

5. **Build the candidate.** The base is `git merge-base` of the tip and the head. If the base is the
   tip, the candidate is the head. Otherwise `git merge-tree --write-tree` builds the tree, and the
   integrator commits it with the tip as the only parent. The commit keeps the head's author. Its
   committer is the integrator at `integrator@the-valley.invalid`. A conflict makes the request
   stale.
6. **Derive the required checks** (see Policy below).
7. **Judge the evidence** (see Evidence below). The outcome is `land`, `stale` or `reject`. A stale
   request publishes `request-stale`.
8. **Land.** The integrator signs a transfer statement that records the policy digest and the checks
   it relied on. Then, as the last thing before the commit point, it reads every record again into a
   fresh index and applies step 4 once more, since judging and signing can take minutes. Then it
   moves the target with compare-and-swap (`git update-ref <target> <new> <tip>`). That is the
   commit point. Then it queues the landing for the publisher, stores its countersigned evidence,
   deletes the request with compare-and-swap, publishes `integration-succeeded`, and writes the
   outcome record.

Each step is a separate `git update-ref` call. A crash between steps can leave a landing without its
evidence or outcome record. Checked by `integrator-e2e` (scenarios for fast-forward, merge,
conflict, stale evidence, tampered notes, retries, a moved target, requests named for changes,
drafts under either kind of name, a child of a draft's head, merges, and a request voided by a
withdrawal) and `integrator-unit`.

Each decision the integrator reports is followed by one timing line in its output, which the unit's
journal keeps (`integrator/timing.go`). The line is the label `timing` and a JSON object:

```text
$ journalctl -u valley-integrator@the-valley -o cat | sed -n 's/^  timing   //p' | tail -n 1
{"time":"2026-10-20T12:00:00.000Z","project":"the-valley","request":"refs/the-valley/integration-requests/main/c1","change":"c1","head":"4ae651e56c34b79b324aca341508cf35c3aaae8e","target":"refs/heads/main","tip":"9d1c0e2b7a6f5e4d3c2b1a0f9e8d7c6b5a4f3e2d","outcome":"land","duration_ms":2310,"stages_ms":{"checkout":40,"evidence":350,"land":3,"policy":1500,"publish":10,"record":30,"refusal":20,"sign":300}}
```

It names the request, the change, its head, the target and its tip, and the outcome. A decision that
fails has the outcome `failed`. `duration_ms` is the whole decision. `stages_ms` divides it among
the stages the decision reached: `refusal` (step 4, and the second reading of the records in step
8), `checkout` (the worktree of the tip), `evidence` (verifying the notes), `policy` (step 6),
`apply` (step 5's merge), `closures` (recomputing input closures for a moved tree), `sign` (the
transfer statement), `land` (the commit point's ref write), `record` (the steps after it), and
`publish` (the bus). A voided request gets a timing line too. A refusal that repeats is not reported
again, so it is not timed again either. The line is output only: no ref, record or event carries it.
The line is printed after the decision's outcome is recorded. It is dropped when it cannot be
written within a second, so a journal that stalls or goes away cannot keep an outcome from being
recorded. Checked by `integrator-unit` and `integrator-e2e`.

The security invariant is the operator's approval: only content the operator approved lands. The
request names the exact head that was approved, and the integrator lands only what a request names.
So a record that moves after the request is filed cannot change what lands.

Readiness is the author's workflow signal, and the integrator enforces it as closely as refs allow.
It does not enforce it atomically. The integrator reads the records last just before it moves the
target, and a record written between that read and the move is not seen. So a head the operator
approved can still land after its author has paused it. Content the operator did not approve can
never land this way. The race-free form binds each approval to the record's withdraw generation, in
the signed approval statements of M1-C.

The records are read with the parser the hook and the CLI share (`change/`), under the bounds the
hook admits them under (`integrator/records.go`). Each pass reads every ref under
`refs/the-valley/changes/` into an index by version head, with one `for-each-ref` and two batched
`cat-file` reads, and step 8 reads them all again. A `c-<change-id>` request's own record is read
fresh each time. A repository that holds more than 4000 records lands nothing, as
[Change records](#change-records) describes.

A stored record that does not read is isolated. It names no head the integrator can see, so it
refuses nothing, and a head it names can land. Every pass that reads records reports it with a
`WARNING` line. The pass also warns with each request that brings a commit the record may name: one
its record commit reaches, since a record names its version heads as parents of its record commits.
`valley status` warns the same way for the change's request. The hook refuses a record that does not
read, so corrupt storage is the only way to get one.

## The restacker

The restacker rebases ready changes onto `main` when `main` moves under them, as its own principal,
so their evidence stays fresh without their owners rebasing and approving again.
`valley-restacker once --report-only` (`restack/`) reads one snapshot of origin and prints what a
pass would do. It plans each family, builds every write a pass would push for it, and decides the
journal of an earlier operation if the state directory holds one. It pushes nothing and writes no
journal. Publishing, the loop and the host service are not built, and nothing deploys it.

It is a git client of origin. It reads origin into a scratch repository under `--state`, with hooks
off and no system or user git configuration, so no filter driver is defined. Nothing is ever checked
out in the scratch repository's work tree. The work tree exists because `attest`, which verifies the
integrator's transfer statements, reads only a repository that has one. Its flags name origin
(`--repo`), the principal it pushes as (`--principal`), the landing verifier (`--known-signers`,
`--integrator-name`, `--attest`), and the floor, which it finds as the integrator does:
`--instance-repo` with `--instance-ref` and `--instance-policy`, or a materialized `--instance`. The
project layer is read from `main`'s tip.

git in the scratch repository commits under the identity that `--commit-name` and `--commit-email`
give. By default that is the principal's name and `restacker@the-valley.invalid`. The restacker sets
that identity as git's author and committer explicitly. It never takes an identity from
`GIT_AUTHOR_*`, `GIT_COMMITTER_*` or `EMAIL` in its environment, and git is told not to guess one.
git there also inherits no configuration from the environment: `GIT_CONFIG_COUNT` with its
`GIT_CONFIG_KEY_<n>` and `GIT_CONFIG_VALUE_<n>`, `GIT_CONFIG_PARAMETERS`, and the other
`GIT_CONFIG*` variables are dropped. Such configuration would beat the scratch repository's own, so
an inherited `author.name` would replace the restacker's identity. A commit the restacker rebases
keeps its author, and the restacker is its committer. A `--report-only` pass writes commits only in
its scratch repository: the rebased commits, and the record and report commits it builds.

No git variable that points git somewhere else reaches anything a pass runs. That covers another
repository (`GIT_DIR`, `GIT_WORK_TREE`, `GIT_COMMON_DIR`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`,
`GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_NAMESPACE`, `GIT_GRAFT_FILE`), other programs
(`GIT_EXEC_PATH`, `GIT_TEMPLATE_DIR`), other attributes (`GIT_ATTR_SOURCE`) and another object
format (`GIT_DEFAULT_HASH`). A pass removes every `GIT_*` variable from its own environment as it
starts, except `GIT_SSH`, `GIT_SSH_COMMAND` and `GIT_SSH_VARIANT`, so cue, attest and the git that
composes the policy run without them too. The scratch repository's `core.attributesFile` is
`/dev/null`. git would otherwise read the user's own attributes file under `XDG_CONFIG_HOME` or
`HOME`, and an attribute such as `merge=union` there would change how a rebase merges.

A pass locks its state directory from start to end, so a second pass on the same directory fails at
once while one runs. The directory belongs to the repository, target and principal of the first pass
that used it, recorded in its `binding.json`. A pass for any other is refused, and needs its own
`--state`. A pass runs for at most `--timeout`, 10 minutes by default. When the timeout passes, or
the pass is interrupted or stopped, every git, cue and attest it started is killed, with whatever
each of them started, ssh included. Writing the plan is bounded the same way. A pass whose output
nobody reads ends at its timeout and lets go of the lock, so the next pass can take it. A message of
the program's own, such as the error it ends with, is given up on after 5 seconds, so a stderr
nobody reads cannot hold the process either. Checked by `restack-unit` and `restack-e2e`.

A family is the connected layers of one snapshot, as the rebase engine captures them
(`rebase.Capture`). Two stacks on a shared lower layer are one family. The planner gives each family
one of four answers:

- **leave**: no layer is a ready change: drafts, withdrawn changes and uncertain landings alone. A
  plain branch is never planned on its own, and a submitted change is no layer.
- **current**: the family already descends from `main`'s tip.
- **hold**: something stops it, and each reason is listed. A layer that is not a ready change holds
  its family, as does any refusal of the engine's `Family.Check`.
- **restack**: nothing holds it.

A family is also held while a related integration request might still land. A request is related by
its name (`c-<change-id>`, or the branch-derived name of any version the change has had), by a head
equal to any version head a layer's change has had, or by an incoming range (what the request's head
reaches and `main`'s tip does not) that contains such a head. A request filed for an older version
therefore holds the family whatever its name. The snapshot lists request heads without fetching
them, so the planner fetches them before it reads any ancestry. A request whose relation cannot be
determined holds every family it might be about. A related request releases the family only with a
current outcome of `stale` or `reject`; a `reject` is noted. Current means the outcome's head,
target tip and evidence fingerprint all equal what the planner sees. The fingerprint is
`change.EvidenceFingerprint`, which the integrator writes and `valley status` reads. The planner
judges requests in name order. One snapshot therefore gives the same reasons, notes and reports
every time it is planned.

A family is held when any record it would change is over a limit, counted with the version the
restack would add:

- `restack.maxAutomaticVersions` versions uploaded by the restacker's principal, from the composed
  policy at `main`'s tip;
- 900 versions;
- 99,000 record updates;
- 48 KiB of encoded record JSON.

The cap is read through `composed/`, the composed-policy reader the integrator reads its checks and
policy digest through. Both run `cue export` of the floor, the template the policy's type selects,
and the project layer, under the packaged schema.

The schema puts no upper bound on the cap. `composed/` keeps the cap as cue writes it, so the
integrator, which does not read the cap, composes any policy the schema admits. The planner counts a
cap larger than a Go `int` holds as the largest `int`. That is no practical cap, since no record
holds that many versions. Checked by `integrator-unit`, `restack-unit` and `restack-e2e`.

The plan prints as a summary, or with `--json`. The JSON is not a published format. `restack-unit`
covers request association, outcome currency and the capacity arithmetic. `restack-e2e` runs the
planner over a repository wired with the rendered hook and checks that no ref on origin moves.

### What a pass builds

For each family it would restack, a pass prepares the family in a throwaway worktree and builds its
writes in the scratch repository (`restack/build.go`):

1. the one atomic branch push, each branch leased on the tip the capture read;
2. each change's next version, lower layers first, built from the record the capture read and leased
   on that record commit. It keeps the active version's branch and title, and names the restacker's
   principal as its uploader. Its push reads origin's branch first, and pushes nothing unless the
   branch is at the prepared head;
3. a report for each recorded change in the family, saying `complete`.

A family whose rebase conflicts gets reports that say `conflict` and name the failing layer. A
family the engine refuses to prepare, or that the planner holds, gets reports that say `blocked`,
with the refusal or the planner's reasons. Neither gets a branch push or a version. A family left
alone or already current gets nothing. A plain branch is no report layer. A report that says what
the change's current report on origin says is not built again: two reports are the same when their
observations and result are, whatever operation wrote each. `--json` shows each family's writes
under `writes`.

A conflict keeps every path that conflicted, whatever bytes the name holds (`rebase/prepare.go`). A
report holds what the report format allows, and when the observation holds more, the report is a
fallback (`restack/report.go`):

- With more than 64 recorded changes in the family, it keeps the change's own layer and the failing
  one.
- It keeps each conflict path and each blocking request that fits on its own, up to 100 paths and 64
  requests. A path fits at up to 1,024 bytes of UTF-8 with no control character. A request fits when
  its ref is at most 256 bytes and its name is a branch name.
- Its refusal has each run of control characters made one space, and is cut to fit 2,048 bytes.
- Its refusal ends by saying what the report leaves out, with the digest of the whole observation,
  so two fallbacks that show the same are told apart.
- If the report is still over 64 KiB, it sheds its conflict paths, then its blocking requests, then
  every layer but the two it keeps.

A family over 64 recorded changes is not held for its size: each change's report keeps its own
layer. Checked by `restack-unit` and `restack-e2e`.

### The journal

The journal is every write one operation intends, kept in the state directory as `journal.json`
before the first of them is pushed (`restack/journal.go`). Its format is
`valley-restack-journal/v1`, local to the restacker. It names the repository, target and principal,
the operation, and `main`'s tip and the evidence fingerprint as captured. For each family it holds
the whole observation (every layer, plain branches included, the related requests, every conflict
path and the full refusal), the branch push, each version and each report, in the kept forms the
rebase engine defines (`rebase/pending.go`). Each conflict path is kept in base64. JSON holds only
UTF-8, and base64 keeps a file name that is not UTF-8 byte for byte. Every object those writes would
push is kept reachable in the scratch repository under `refs/valley-restack/<operation>/`. The
journal is written through a synced temporary file and renamed into place, and the scratch
repository syncs each object and ref it writes.

A journal is read as strictly as it is written. A field missing, unknown or named twice, a form that
does not agree with itself, and a journal for another repository, target or principal are refused,
and nothing in a refused journal is decided. A refused journal is an error that recovery reports,
never a crash. A family's writes must agree with each other:

- A family that would restack adds exactly one version of each of its changes.
- Each report gives the family's result: complete, conflict or blocked.
- A report says a version was published exactly when the family adds that version, and then names
  that version's head, number and record commit.

The restacker does not keep a journal that would not read back as written. A lost journal is never
inferred. A change whose branch a pass moved with no journal to say so is held by the planner,
because its branch is not at its recorded head. Checked by `restack-unit`.

### Recovery

Recovery reads origin and the journal, and decides each write in the order a pass makes them: the
branch push, then the versions, lower layers first, then the reports (`restack/recover.go`). Each
write is `done` when origin holds it or has moved on from it, `finish` when it is still valid to
push, or `stop` when it must not be pushed. A stop is final when no later pass could push it, and
terminal when it is also a local failure the journal keeps the diagnostics of.

| Write           | `finish` while                                                                                                                                            | `stop` (final) on                                                                                                                                             |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The branch push | `main` is at the tip the family was rebased onto, every branch is at its lease, every record is at the record commit read, and nothing has been submitted | a branch moved, `main` moved, a record moved (an unready, a withdrawal, a new version), a submission, or a prepared head missing from the scratch repository  |
| A version       | its branch is at the prepared head, or the branch push is still to finish, and its record is the record commit read                                       | the author moving the branch or the record, a withdrawal, an unready, a submission, a deleted branch, or a `main` that does not descend from the captured tip |
| A report        | origin's report is the one it was built on, and what it says was published is on origin or is still to finish                                             | a version it says was added stopped, or origin's report moved                                                                                                 |

A related integration request that might still land is a stop that is not final: the branch push
waits, and the versions pause. Requests are related as the planner relates them, by every version
head the changes have had, the prepared heads included. A deleted change record is a terminal stop
for its version and its report, since the hook takes no report about a change the repository does
not record. A family whose writes stop does not stop another's. A `main` that has moved on, and
still descends from the captured tip, does not stop a version whose branch was pushed.

In `--report-only` mode recovery decides and pushes nothing, and leaves the journal as it is. The
decisions are printed, and under `recovery` with `--json`. Checked by `restack-unit`.

## The check worker

The check worker builds the checks a ready change requires before anyone approves it. It publishes
the evidence on `origin`, where approval reuses it instead of building it (see
[The valley command](#the-valley-command)). `valley-worker` runs one pass of it. It is
`valley-go work` (`status/worker.go`), packaged with this flake's git, cue and `attest` and the
verification schema (`nix/packages.nix`). It keeps no queue, lease or schedule, and nothing connects
to it. Running it on a timer is **not built**: no host module unit runs it.

A pass reads only the projects named with `--repo`. It reads each project as `valley pending --repo`
does, so it can run from a clone of any project the host serves. For each project it follows
approval's steps:

1. It lists the ready changes whose active version has not landed, and pins each one's active head.
2. It derives the checks the head requires with the Go deriver, as `valley-go checks` does, over the
   head's merge base with `main`. Both policy layers come from `main`'s tip on `origin`, as approval
   reads them, never from the change.
3. It reads the evidence `origin` holds for the head's tree, with the coverage rules approval uses.
   It leaves `command` checks to approval.
4. It runs every `nix` check that is not covered in one batch, with `attest run --rev <head>` under
   the worker's own key. Then it pushes the one ref `attest` stored,
   `refs/the-valley/attestations/<tree>/<key-hash>`, and nothing else.

The hook admits that ref as it admits any evidence. Every note must verify under the host's known
signers and name the ref's tree and key hash, whoever pushes it. The integrator then judges the
worker's notes like any other. Where the host restricts the worker's key to projects (see
[Evidence: attest and notes](#evidence-attest-and-notes)), `--repo` names those projects and no
others (**deployment choice**). The controllers count the worker's evidence nowhere else.

The evidence is about the head's tree, so it stays true if the change moves on while the batch runs.
The pass does not read the change's record again before it publishes. A superseded head's evidence
is published, and the next pass builds the new version.

The attestation namespace admits creations only, so the worker never builds what it could not
publish. When `origin` already holds the worker's own ref for the tree and that ref lacks a required
check, the pass says so and builds nothing. Approval builds the rest under the operator's key
([bd-7b7d58f](../archive/.the-valley/bugs/bd-7b7d58f-same-tree-re-attestation-cannot-be-published.md)).

A push whose reply is lost is not built again. If the push landed, the next pass finds the evidence
on `origin`. If it did not land, the next pass finds the ref still stored in its own clone, and
pushes that same object again.

A batch that fails waits before it runs again. The first wait is 15 minutes, and each failure in a
row doubles it, up to 24 hours. The waits are kept in `failures.json` in the worker's state
directory. A batch is the project, the tree and the checks it runs, so a new version runs at once. A
whole pass forgets every batch it did not meet.

Passes that share a state directory take turns. Each holds an `flock` on the `lock` file there for
the whole pass. A second pass waits for the first, and then reads `origin` after the first has
published.

A read of `origin` that fails stops the pass, because a read that did not happen must not read as no
changes or no evidence. Anything else that goes wrong with one change is reported, and the pass goes
on to the next. The pass exits 1 when anything went wrong.

Checked by `valley-request`, which runs the packaged worker through the protected host's hook and
lands its evidence with the real integrator.

## Policy

A project's required checks come from three CUE layers, unified as
`floor & templates[type] & project` (`schema/verification.cue`):

- **The schema**, `schema/verification.cue`, ships inside the `valley`, integrator and
  `valley-restacker` packages (`nix/packages.nix`).
- **The floor and templates** come from the owning valley's instance repository, at
  `policy/instance/*.cue` on its `main`. `policy/valley` names that repository. A value the floor
  writes as `true` is mandatory. A template value written `bool | *true` is a default a project may
  turn off. Every `*.cue` document directly in that directory is read, from a NUL-separated tree
  listing so git never quotes a non-ASCII name such as `naïve.cue` (`composed/composed.go`). Checked
  by `integrator-unit`.
- **The project layer** is `policy/project/*.cue`, read at the tip of the target branch.

Both layers are read from integrated tips, never from the change being judged
(`integrator/policy.go`, `composed/composed.go`). A path class maps path globs to checks. A change
requires the union of the checks of every class one of its paths matches. A path no class matches
requires the `unclassified` checks. The integrator gets this answer by running `valley checks`,
which diffs with renames counted on both sides. Checked by `policy-deriver` and `cue-vet`.

A layer's documents are the entries bash's glob `"$dir"/*.cue` names. The directory is a name and
never a pattern, so a directory named `policy[1]` is read, and `policy1` is not. A name beginning
with a dot is not a document. Checked by `derive-unit`.

A glob matches bytes. `valley checks` derives in the C locale, whatever locale it is run in. So `?`
matches one byte, and `?.md` does not match `é.md`, whose `é` is two bytes. A path holding a byte
that is not UTF-8 matches a glob like any other path. Under a UTF-8 locale, bash's regular
expressions would match characters, and such a path would match no glob, not even `**`. The checks a
change requires would then depend on the locale of the operator's shell or the integrator's unit.
Checked by `derive-unit`.

The derivation fails closed. A failed read of the diff, the target, a policy layer or the composed
policy makes `valley checks` exit non-zero and print no report. Without that, the failure would read
as an empty list, and an empty list derives a change that requires nothing. The integrator also
refuses output that is not a whole report: no header, no required-checks section, or a
required-checks heading with no checks under it. It believes a report of no changed paths only when
git agrees that the two trees are the same. Checked by `policy-deriver`, `valley-request` and
`integrator-unit`.

A policy cannot forge a line of what it renders. `valley checks` reads the composed policy as lines
of tab-separated fields, and the policy's own strings are fields of those lines. A newline in one
would end its line and start a forged one, such as a looser carry mode for a class the floor pinned.
So the schema refuses control characters in path globs, and its name shape already keeps them out of
class and check names. `valley checks` also refuses a rendered line holding one. It refuses a second
line for anything the policy states once, such as a class's carry mode or a class's requirement of a
check. The integrator refuses a report that states one thing twice, such as two carry modes or two
rows for one check. Checked by `cue-vet`, `policy-deriver` and `integrator-unit`.

A second deriver is written in Go (`derive/`). No caller runs it: `valley checks`, approval and the
integrator use the one in `bin/valley`. It reads the whole composed policy through `composed.Read`
(`composed/model.go`): every class's paths, requirements and carry mode, the `unclassified` set, and
each check's runner, cost and validity. It asks cue whether each requirement is mandatory the way
`valley checks` asks: it vets the layers with a document setting that requirement to `false`. It
reads a diff as `valley checks` does, with renames counted on both sides, and keeps every path
whole, whatever bytes it holds. It matches globs byte by byte, as `valley checks` does. It reads a
layer's documents as `valley checks` does. Checked by `derive-unit`.

`derive-unit` holds the two derivers to the same answer. It runs both over this repository's own
policy, the example policies under `examples/policy/`, and paths drawn from a fixed seed. It runs
`valley checks` under the C, `C.UTF-8` and `en_US.UTF-8` locales, with the layers in directories
named like glob patterns. Every global of the Bash derivation must equal the Go answer: the
documents composed, the changed paths, the classes matched, the carry mode, the unclassified paths,
and each required check's classes, pin, runner, attribute, command and cost. The two derivers differ
in three places, where `valley checks` is wrong:

- `valley checks` splits a path holding a newline into two paths.
- A class added by refining `policy` may require a check no layer defines, because the schema's
  cross-check reads only the layers. `valley checks` derives under it, and fails on an unbound
  variable when a change touches that class. The Go deriver refuses the policy.
- `valley checks` merges a class named `unclassified` with the `unclassified` set, so each requires
  the other's checks. When both require one check, it refuses every derivation. The Go deriver keeps
  them apart.

`valley-go checks` prints the Go deriver's answer as the `valley-checks/v1` JSON document. It takes
the schema, the floor's directory and the project layer's directory, and two commits:

```sh
valley-go checks --schema schema/verification.cue \
  --instance policy/instance --project policy/project <base> <head>
```

```json
{
  "schema": "valley-checks/v1",
  "base": "<sha>",
  "head": "<sha>",
  "policy_digest": "<sha256>",
  "paths": [{ "path": "docs/guide.md" }, { "base64": "c3JjL2NhZukucnM=" }],
  "classes": ["docs"],
  "unclassified_paths": [],
  "carry": "same-paths",
  "required": [
    {
      "name": "prose-format",
      "classes": ["docs"],
      "unclassified": false,
      "pin": "mandatory",
      "cost": "quick",
      "runner": "nix",
      "attribute": "prose-format"
    }
  ]
}
```

- `paths` is every side of the diff, sorted bytewise. A path that is UTF-8 is `{"path": ...}`. Any
  other is `{"base64": ...}`, in standard base64, because JSON holds only UTF-8.
- `classes` is the classes a path matched. `unclassified_paths` is the paths no class covers.
- `policy_digest` is the integrator's policy digest of the same layers.
- Each required check lists the classes requiring it. `unclassified` is `true` when the
  `unclassified` set requires it. `pin` is `mandatory` or `default`. `attribute` is given for the
  `nix` runner, `command` for the `command` runner, and `validity` when the policy declares one.

A failure prints nothing on stdout and exits 1. The document is a contract with a version, so a
change to the name or meaning of any field is a new version. Checked by `valley-go-unit`.

A check also declares a `cost`, `quick` by default. A `slow` check takes minutes, or needs special
hardware such as KVM. `valley checks` reads the cost from the composition it derives from. After the
report it prints two lines, `quick:` and `slow:`, each listing the required checks of that cost.
`valley checks --names` prints the required check names alone, one per line, and `--cost quick` or
`--cost slow` keeps one set. The integration gate does not read the cost. Approving runs every
required check, and the integrator requires evidence for every required check, whatever its cost.
The cost changes neither which checks a change requires nor what the integrator accepts. Checked by
`policy-deriver` and `integrator-unit`.

Each class also declares a carry mode. The mode says how far an approval of a change touching the
class carries when the change is rebased onto a target that has moved. An approval carries only when
the rebased change has exactly the files the approval saw. The mode adds a condition on the landings
since the approval:

- `exact`: there were none, so the target has not moved.
- `same-class`: none of them touched a path in any class the change touches.
- `same-paths`: no further condition.

A class writes its modes as a set of keys, such as `carry: {"same-class": true}`, in the same form
as `requires`. The class's mode is the strictest mode whose key is `true`, and `same-paths` when no
key is `true`. The layers compose as they do for checks. A floor's `true` cannot be turned off, a
template's `bool | *true` is a default a project may turn off, and a project may always add a
stricter mode. A change's carry mode is the strictest among the classes its paths match, and a path
no class matches is `same-paths`. `valley checks` prints the mode on a `carry mode:` line, and the
integrator refuses a report without a valid one. Checked by `cue-vet`, `policy-deriver` and
`integrator-unit`.

A policy also sets `restack.maxAutomaticVersions`, a whole number of at least 1. It is 64 unless a
layer sets it. The restacker rebases a ready change when its target moves, and adds each result to
the change as a new version. The cap is how many of those versions it adds to one change. A host may
impose a lower ceiling of its own, outside the policy. The carry modes and the cap are part of the
composed policy, so the integrator's policy digest, a SHA-256 of the whole composition, changes when
either does. Approval carry reads the carry modes, and is not built yet, so nothing acts on them.
The restacker's planner reads the cap (`composed/composed.go`, `restack/plan.go`). The restacker
builds the versions it would add and pushes none of them. Checked by `cue-vet`, `integrator-unit`,
`restack-unit` and `restack-e2e`.

the-valley's own project layer is `policy/project/policy.cue`. It gives each part of this repository
a class that requires the checks reading that part. A Go module requires its unit tests, and its
end-to-end check where it has one. The CLI requires its script and status checks. The Nix, the
schemas and the policy require their evaluations and vets. The NixOS module also requires the
end-to-end checks that run what it renders, and two of those boot NixOS machines. A change to
`flake.nix` or `flake.lock` does not require them, so a nixpkgs bump boots no machine. A check's own
files, such as its script under `nix/checks/`, require that check. A change to documentation alone
requires the floor's `prose-format` and nothing else, which checks Markdown formatting and relative
links and heading anchors across `README.md`, `AGENTS.md` and `docs/*.md`
(`nix/checks/prose-format.sh`). A project layer that stopped composing would stop every derivation
once it landed, including the derivation of its own fix. So the `own-policy` check composes the
layer with the example floor before it lands. It fails if the layer names a check this flake does
not define, if a script under `nix/checks/` requires no check of its own, or if a docs-only change
would require more than `prose-format`.

In the-valley's layer, four parts are `same-class`: the hook, the integrator, the signature
verifier, and the identity compiler with `schema/identity.cue`. The policy is `exact`: `policy/` and
`schema/verification.cue`. Everything else is `same-paths`. The tree holds no registry, grant or
secret data of its own. The ones under `examples/` and `attest/conformance/` are test fixtures. Two
classes exist only to set a mode, `identity-compiler` and `policy-authority`. They require no
checks, so the paths they cover require the checks of their other classes. `schema/verification.cue`
also requires the integrator's checks, because both compose a policy under it. The layer leaves the
restacker's cap at 64. `own-policy` holds each scope to its mode, holds the paths the two mode
classes cover to the checks of their other classes, and holds the cap to 64.

The reasons for CUE here, and for the mandatory-versus-default encoding, are in
[dcr-0f5d9b1](../archive/.the-valley/decisions/dcr-0f5d9b1-cue-config-host-module.md) and
[dcr-f41f718](../archive/.the-valley/decisions/dcr-f41f718-declared-verification-policy.md).

## Evidence: attest and notes

`attest run` runs checks against a commit exported with `git archive` (`attest/run.go`,
`attest/check.go`). A `nix` check builds the flake check's derivation. A `command` check runs a
shell command in the exported tree. If every check passes, `attest` writes a statement per check,
signs it, and stores it under `refs/the-valley/attestations/<tree>/<key-hash>`.

The `nix` checks build side by side instead of one after another. One nix evaluation names every
check's derivation. Each derivation is then built by a `nix build` of its own, with up to one build
per CPU running at once. Nix applies `max-jobs` and `cores` within each build, so that bound is how
many checks build at once. Each check's outcome is its own build's exit status, and a check that
failed keeps its own build's `stderr` as its log. So a failed build stays failed even when another
derivation produces the same output path, as two fixed-output derivations can. A check that does not
evaluate is an error for that check alone, and the others are still built. The `command` checks run
after the `nix` checks, one at a time. The statements are the ones a check built alone would get,
and nothing is published unless every check passed. Checked by `attest-e2e`, through
integrator-e2e's stand-in for nix, and against a real nix by the tests in `attest/run_test.go`. A
nix build has no daemon to build with, so those tests run under `go test` on a machine with nix, and
skip elsewhere.

A statement is line-based text that opens with `the-valley/attestation/v1` (`attest/text.go`,
`schema/attestation.cue`). Its subject is the tree digest. A pure statement records the derivation,
its inputs digest and the output. An effectful statement records the environment and the time of the
observation. The envelope is a signed note: the `sumdb/note` format with Ed25519, implemented in
`note/note.go`. Checked by `attest-e2e`, `attest-conformance`, `attest-schema` and `note-unit`.

The integrator reads evidence at the head's tree and verifies each note with `attest verify`
(`integrator/evidence.go`). It does not run the checks itself. A tree may hold several notes about
one check, one per signer. The integrator judges the check by one of them: the first note that
verifies, with the refs in byte order, whatever that note records. Until a note verifies, the last
one read stands, so a refusal is reported. This choice is `change.SelectNotes`
(`change/evidence.go`), and approval makes the same choice. A required check is met when its note is
signed by a known signer, says the check `passed`, and one of these holds
(`integrator/verdict/verdict.go`):

- **A pure check, unmoved tree.** The landed tree is the attested tree. No inputs are compared.
- **A pure check, same inputs.** `attest inputs` on the candidate gives the same inputs digest and
  derivation hash. `attest inputs` evaluates the derivation and does not build it; evaluation can
  still build anything the flake imports from a derivation.
- **An effectful check.** No class that requires it changed between the base and the tip, and the
  evidence is younger than the check's `validity` window.

Checked by `integrator-unit` (the verdict tests) and `integrator-e2e`.

A signer can be restricted to projects. This is how a check worker's key can sit in the
known-signers file that every controller on a host shares, while its evidence counts only in the
projects it builds for. The controllers are given a signer projects file (`--signer-projects`,
rendered from `integrator.signerProjects`) that names each restricted signer and its projects
(`note/signerprojects.go`). In a project not listed for it, the controller classifies a note whose
only verified signers are restricted elsewhere as signed by an unknown signer, as it classifies a
stranger's note (`integrator/evidence.go`). A signer the file does not name is not restricted. The
push hook does not read the file, so it admits a restricted signer's evidence in every project, and
only the controllers refuse it. Approval reads the file the host publishes, so it does not skip a
check on such a note, and builds that check instead (see [The valley command](#the-valley-command)).

A controller's own signature never makes a check's attestation count (`implemented`). When a change
lands, the controller countersigns the attestations it carried (`integrator/transfer.go`). That
countersignature vouches for the landing in the controller's own project, not for the check.

Another controller's signature is known to be an integrator's only by declaration. Nothing in a note
marks a signature as a countersignature, and anyone can drop or reorder a note's signature lines. So
a host declares each other controller whose key its known-signers file holds, by listing that
controller's name in `integrator.signerProjects` with no project (`deployment choice`). A listed
name counts nowhere. A countersigned attestation copied in from that controller's project then
counts only through the signer who made the check claim. A host that leaves another controller's key
undeclared lets that controller's signature count as check evidence.

A controller reads the file once, at start. Every name and project in it is printable ASCII with no
space, and the host module holds the list it renders to the same rule. A file that cannot be read,
names a signer twice, holds a word that is not printable ASCII, names a signer no verifier key can
carry, or names the controller's own `--name` stops the controller before it judges anything. The
last is refused because a restricted signer under the integrator's own name could also sign transfer
statements, which clients believe in every project. The publisher refuses the same files. A changed
list does not by itself re-judge a request whose answer is recorded: a request a narrower list held
back waits until its head, the target's tip or the stored evidence moves. Checked by
`integrator-e2e`, `integrator-unit`, `note-unit`, `module-eval`, `signers-e2e` and `restack-e2e`.

Check definitions come from the change being judged. `attest` builds the flake checks of the
exported tree, so a change can edit the check it is judged by
([bd-eaefe82](../archive/.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md),
[ida-a9e274c](../archive/.the-valley/ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md)).

## Reviews

A review is a reviewer's judgment of one version of a change. It approves the version or requests
changes to it, with a summary and notes on the files the reviewer read. Human and agent reviewers
write the same record. An agent's review also records how its judgment was produced. A review grants
nothing: only an integration request lands a change.

The `review/` module defines the record, how a review is bound to what its reviewer read, and the
history reviews are kept in. Host admission is **not built**. The push hook has no rule for
`refs/the-valley/reviews/`, so it refuses every push there, and no `valley` verb writes or reads a
review. Checked by `review-unit`.

**The record.** A review is one JSON file, `review.json`, in the schema `the-valley/review/v1`
(`review/review.go`). It holds:

- `review_id`, 32 lowercase hex digits. The reviewer makes it once and keeps it through every retry
  of publishing the review.
- The change ID, the target, the review's name, such as `docs-style` or `operator`, and the
  reviewer, a principal.
- The `verdict`: `approve`, `request-changes` or `withdraw`.
- The binding, below, a summary, and notes.
- For an agent, its provenance: the harness, the model, digests of the generation settings and of
  the inputs, and the tokens the run reported.

A note is anchored to a file the reviewer read. The anchor is the side (`before` for the base,
`after` for the head), the path, the blob the path held on that side, and an optional range of
lines, numbered from 1. A note on deleted lines is anchored to the before side.

A `withdraw` review takes back an earlier review of the same change, named by its ID. It judges no
version, so it has no binding and no notes, and its summary gives the reason. Only the review's
reviewer or an operator may withdraw it. The project's rule for a blocking review is that a request
for changes stops counting only when it is withdrawn, or when a version with a different delta needs
a fresh review. A later approval does not clear it. Evaluating reviews by that rule is **not
built**.

The parser is as strict as the change record's. It refuses invalid UTF-8, a key given twice, a field
the schema does not have (a key in another case among them), a field of another type, a string or
list past its bound, and anything after the object. A review is at most 64 KiB.

**The binding.** A review records exactly what its reviewer read, so anyone with the objects can
recompute it (`review/binding.go`). It names:

- the change record commit the reviewer read, and the version number and head that commit names;
- the target tip the version was judged against;
- the merge base of that tip and the head;
- the digest of the `valley-delta-v1` delta from the base to the head: the files the version
  changes, whole.

When the tip and the head have no merge base, or more than one, there is no single base to compare
from. Then there is no binding, and so no review. `review.Bind` refuses to make one, and
`review.Verify` refuses a binding that names a base there.

`review.Verify` recomputes every part. The record commit must name the version with the head. Given
the change's record ref, the record commit must be the record of its own `seq` on that ref's
first-parent history, so a review cannot bind a record written to look like one. Given the target's
tip, the bound tip must be on its history. The base must be the one merge base, and the delta must
have the bound digest. Each note's path must hold its blob on its side, with its lines inside the
file.

Recomputing reads trees a pusher chose, so it runs within a budget of bytes, tree entries and time.
`delta.BetweenWithin` is the bounded form of `delta.Between`, which is unbounded. It asks git for
each object's type and size before its content. It counts every entry it visits, so a subtree that
two directories name counts twice, and it counts the bytes of every path it builds. git is killed at
the deadline. The first bound crossed ends the reading with an error that wraps `delta.ErrBudget`,
so a caller can tell a version too large to recompute from a binding that is wrong.
`delta.BetweenWithin`, `review.Bind` and `review.Verify` check the deadline once more before they
succeed, so none of them returns a result finished after it. A note's file has no size bound of its
own. It is read within the budget's bytes, and a file too large for them is the budget's refusal,
not the note's.

**The history.** A change's reviews are kept at `refs/the-valley/reviews/<target>/<change-id>`
(`review/history.go`). Each commit holds one review, as the single file `review.json`, with these
parents:

| Review                               | Parents                                                             |
| ------------------------------------ | ------------------------------------------------------------------- |
| The first review                     | The change record commit it binds                                   |
| A later review that judges a version | The review commit before it, then the change record commit it binds |
| A withdrawal                         | The review commit before it                                         |

The first-parent chain lists every review in order. The last parent keeps each judged record
reachable, with the heads it names, after the change's record ref is deleted and the repository
pruned.

A history is append-only. Each review ID in it is unique. A withdrawal names an earlier review of
the history, under the same review name, that is not itself a withdrawal and is not withdrawn
already. A history holds at most 512 reviews. Each review commit is at most 4 KiB and its tree at
most 1 KiB.

An append is a compare-and-swap. `review.Build` writes the commit that appends a review to the head
the client read. `review.Publish` pushes it with `--force-with-lease`, so the push fails if another
review arrived first. The client then reads the new head and builds again, with the same review.
`review.Build` reports a review the history holds already, so a retry after an unconfirmed push
publishes nothing twice.

`review.CheckAppend` decides one update of a history's ref, for the push hook to call; the hook does
not call it. It refuses a delete, and any update that is not one new review commit on the old head
by the table above. The review must name the pusher as its reviewer. The caller says, from its
grants, which review names the pusher may write under, and whether the pusher is an operator. A
review that judges a version under any other name is refused before its binding is read. A
withdrawal needs no grant of its name. It must be of the pusher's own review, unless the pusher is
an operator, who may withdraw any review. So a reviewer whose grant is revoked can still take back
their own judgment. A review that judges a version must be about a change the repository records,
and its binding must verify.

## Identity

The identity registry is CUE under `identity/` in the instance repository, read at its `main`
(`identity/registry.go`, `schema/identity.cue`). It declares principals and boundaries.

- A principal has a kind (`human`, `machine` or `service`), one or more `ssh-ed25519` keys, and
  grants. A key may also sign attestations, under a name it declares.
- A grant names a boundary. A boundary has a kind: `git-push`, `registry`, `approve` or `restack`.
  The kind `approve` is also accepted as `request`. A registry using either name compiles the same
  files.
- A machine, service or external principal must declare `expires`. From that day on, a successful
  compile leaves the principal out of every file. A compile that is refused keeps the previous
  files, expired entries included. Keys the host declares statically are outside the registry and do
  not expire.

`valley-identity` compiles the registry every 5 minutes, as git (`nix/valley-host.nix`,
`identity/compile.go`, `identity/render.go`). It writes three files under
`/var/lib/valley-identity/`:

- `authorized_keys.git`: one line per key of a principal with a `git-push` grant, tagged
  `environment="VALLEY_PRINCIPAL=<principal>"`. sshd reads it after the host's declared keys.
- `known-signers`: the keys that may sign attestations, for the hook and the integrator. The host
  also publishes it for clients (see [The signers publication](#the-signers-publication)).
- `grants`: one `request <principal>` line per holder of an `approve` grant, at a boundary of kind
  `approve` or `request`, and one `restack <principal>` line per holder of a `restack` grant. A
  principal holding both kinds is written once. The line keeps the name `request`, and the hook
  reads it as the `approve` grant.

The compiler refuses a registry where no principal holds a registry grant, where one key pushes for
two principals, or where a pushing key matches a host-declared key under a different tag or none.
Keys that only sign are not checked for collisions. It computes everything before it writes
anything, so a refused registry leaves the last good files in place. Checked by `identity-e2e` and
`identity-schema`, and by the Go tests in `identity/identity_test.go`. Why identity is a governed
registry, and why machine keys expire, is in
[dcr-b87f6e8](../archive/.the-valley/decisions/dcr-b87f6e8-identity-is-a-governed-registry.md) and
[bd-8a591dc](../archive/.the-valley/bugs/bd-8a591dc-machine-credentials-never-expire.md).

## sigverify

`sigverify` verifies OpenSSH SSHSIG signatures and signed git tags against an `allowed_signers` file
(`sigverify/`). It exists because `ssh-keygen -Y verify` and `git verify-tag` never check the
hardware key's flags. A key file edited with `ssh-keygen -p -O no-touch-required` signs without a
touch, and those tools accept the signature.

- The default class, `fido-sk`, takes only security-key signatures. It requires the user-presence
  bit. It also requires user verification under `--require-uv` or a `verify-required` signer line.
- The `tkey-signer` class takes an `ssh-ed25519` key marked as a TKey key in `allowed_signers`. The
  verifier cannot tell a TKey key from any other ed25519 key, so the mark is trusted at enrollment.
- Plain software keys pass only with `--allow-non-sk` or `--allow-class software`.

Checked by `sigverify-unit` and `sigverify-e2e`, including a touchless-signature negative test.
Nothing in the-valley or cosmo calls `sigverify` yet.

## The valley command

`bin/valley` is a shell script, also packaged with its schema (`nix/packages.nix`). Its `status`,
`ready`, `unready`, `withdraw`, `rebase`, `changes`, `pending`, `knot`, `plan` and `self-update`
verbs are a Go program, `valley-go` (`status/`), which the package puts on the script's `PATH`.
`valley-go` rebases with the rebase engine, a Go module of its own (`rebase/`), which also reads how
far each version has landed for its listings. The package stamps its version on the script, and
`valley self-update` keeps a user's `valley` at the tip of a branch (see
[Version and self-update](#version-and-self-update)).

| Command                      | What it does                                                                                          |
| ---------------------------- | ----------------------------------------------------------------------------------------------------- |
| `valley pending`             | Lists the ready changes not yet landed, then the branches no change is on, then the drafts.           |
| `valley changes --json`      | Prints every change in every state, across the host with `--all`, as `valley-changes/v1`.             |
| `valley review <change>`     | Shows a change, then offers `[a]pprove`, `[v]iew full diff`, `[r]eject`, `[s]kip`, `[b]ase` if stale. |
| `valley approve <change>`    | Approves one head of the change, as `[a]pprove` does. `--head <sha>` names the head.                  |
| `valley rebase <change>`     | Rebases the change's whole stack, as `[b]ase` does, and adds each rebased head as a new version.      |
| `valley checks`              | Reports the checks the policy requires of a change, grouped by declared cost. It runs nothing.        |
| `valley status [--json] <c>` | Reads the request and outcome record for a change. `--json` prints the `valley-status/v1` object.     |
| `valley ready [<branch>]`    | Marks a head of the branch ready as a new version of its change, in the change's record.              |
| `valley unready <change>`    | Makes a ready change a draft, as its owner or the operator.                                           |
| `valley withdraw <change>`   | Marks a ready change or a draft withdrawn, as its owner or the operator.                              |
| `valley knot <verb>`         | Reads and writes the project's plan graph (see [The plan graph](#the-plan-graph)).                    |
| `valley plan`                | Shows the plan graph as a tree, and what waits on the operator (see [The plan view](#the-plan-view)). |
| `valley tail [subj]`         | Subscribes to bus events.                                                                             |
| `valley replay [dir]`        | Publishes one `ref-updated` event per current ref.                                                    |
| `valley self-update`         | Installs the tip of a branch of the-valley into the user's nix profile, once it passes a smoke test.  |
| `valley --version`           | Prints the version stamp: the CLI's version and the commit its package was built from.                |

`[a]pprove` is the operator's approval of a change. It reuses the evidence `origin` already holds
for the head's tree, and runs the other required checks with `attest` in a worktree of the head it
approves. Then one atomic push carries the new evidence refs and the integration request, with a
lease on the request ref. When the request already names this head, the push leaves the request ref
out. When there is no new evidence as well, there is no push. `[b]ase` rebases the change's whole
stack in a temporary worktree, and pushes every layer's branch in one atomic push, with a lease on
each. `[r]eject` deletes the remote branch after a prompt.

Evidence on `origin` comes from a check worker (see [The check worker](#the-check-worker)) or from
an earlier approval. Approval skips a check only when the integrator will accept the same evidence.
Approval reads the evidence with `valley-go coverage` (`status/coverage.go`). It lists `origin`'s
attestation refs for the head's tree and fetches the objects they name. It reads the notes from
those objects byte for byte, as the integrator reads them. A local ref that was never published does
not count. A listing or a fetch that fails ends the approval, and nothing runs.

Approval judges each required `nix` check by the note the integrator judges it by: the first note
for the check that verifies under the `known-signers` file the host publishes (see
[The signers publication](#the-signers-publication)). With no publication, nothing is reused.

A note verifies here only when one of the signers `attest verify` accepts counts in this project, by
the rule the controllers apply (see [Evidence: attest and notes](#evidence-attest-and-notes)). The
publication's `signer-projects` file must let that signer count in the project. The signer must not
be the publication's integrator name, since the integrator's signature on a note is its
countersignature. Approval names the project after `origin`'s URL. The host's controllers name it
after their repository's directory, so the two agree when the URL ends in that directory's name. A
publication whose `signer-projects` is not a file, or does not read, is not used, so nothing is
reused.

The check is covered when that note also meets all of these:

- It is pure evidence for the check's name and its flake attribute.
- It records the check as `passed`.
- When `VALLEY_KNOWN_SIGNERS` is set, `attest verify` accepts it under that file too, by a signer
  that counts in this project. A local file can narrow what approval reuses, never widen it beyond
  what the host publishes.

Comparing the attribute is stricter than the integrator's rule for an unmoved tree, which compares
none. Coverage decides only what approval runs. The integrator judges every note again when it
decides the request.

A `command` check is never covered, so approval runs it again. Every check that is not covered runs
under the operator's own key, so its evidence goes to a ref of its own beside the worker's. When
every required check is covered, nothing runs and no key is needed. When `origin` already holds the
operator's own ref for the tree and a check still has to run, approval refuses before running
anything. The namespace is create-only, so the new evidence would have nowhere to go
([bd-7b7d58f](../archive/.the-valley/bugs/bd-7b7d58f-same-tree-re-attestation-cannot-be-published.md)).
Checked by `valley-request`, which also lands a fully covered approval and a partly covered one with
the real integrator.

Approval also measures how long each required check took. `attest run` prints a `time` line after
each check, with its result, its duration, and whether nix found its output already built.
`[a]pprove` passes `attest run --timings`, which appends one JSON line per check to
`$XDG_STATE_HOME/valley/timings.jsonl` on the operator's machine (`attest/timing.go`). The line
names the repository, the change and version, the head, the check, its runner and declared cost, its
result, and `duration_ms`. A `nix` check's line also gives its evaluation time, `eval_ms`, which is
part of its `duration_ms`. The `nix` checks share one evaluation and build at the same time, so the
sum of a run's `duration_ms` is more than the run took. The log stays local and is not evidence:
nothing signs it or pushes it. A log that cannot be written is a warning, and approval continues.
Timing never holds approval up: anything at the log's path that is not a regular file is refused at
once, and a `time` line, warning or log line that cannot be written within a second is dropped. The
reference describes [the timing log](reference.md#the-timing-log) field by field.

A change with a record is requested as `refs/the-valley/integration-requests/main/c-<change-id>`. A
branch no record is on is requested under its name with each `/` replaced by `-`. A branch whose
name maps to `c-<change-id>` is refused, by `approve` and by `status`: that name is the change's.

When the checks fail under `[a]pprove`, approving the same head again can only fail again. So the
prompt then says approving needs a new head. It offers only `[r]eject` and `[s]kip`, plus `[b]ase`
where it is offered. `[a]pprove` comes back once the change is at another head, as it is after
`[b]ase`. A refusal that is not a check failure, such as a lost lease race, keeps the prompt as it
was.

`review` reads the change's state from its record before it shows the change. A change that is not
ready says so on the first line shown, above its diff, and again in place of the usual prompt:

- A draft:
  `nxvp v2 is a draft: its owner (klaus-env) hasn't marked it ready, so it can't be approved yet.`
- A withdrawn change: `nxvp is withdrawn: there is nothing to approve.`

The prompt then offers only `[v]iew full diff` and `[s]kip`. A change is often a draft because an
agent is working on it: klaus unreadies a change while its run works, and readies the new head when
the run finishes. `[a]pprove` would be refused. `[b]ase` is not offered either, since adding the
rebased head as a version would ready the change behind its owner's back. `[r]eject` is not offered,
because it records nothing on the change. It only deletes the branch, which the owner may still be
working on. The state can change while the review is open, so `[a]pprove` and `[b]ase` still read
the record again when they act, and refuse a change that is not ready. A branch no record is on has
no state, and is offered the usual prompt.

`review` also shows the knots a change does. These are the knots in the project's plan graph whose
`changes` lists `valley:<project>/<change ID>` (see [The plan graph](#the-plan-graph)). The project
is origin's repository name, as `valley knot` reads it. The plan graph holds these links in project
storage; the change record tracks only the change's versions and state. `review` prints one line per
knot above the change's diff:

```text
does oc-6335749 (open): Ready changes stay landable as main moves
```

For a single change, these lines follow the first line of the comparison or diff, or the draft or
withdrawn notice when the change is not ready. In a stack, each layer's page shows that layer's
knots under its heading. Active knots come first, then terminal ones, each group in ID order.
`valley-go does` reads the graph once per review for every change in it (`status/does.go`). A
missing or unreadable graph shows no knots. `valley review -v` says why on stderr. Checked by
`valley-ready`.

A change with a stack under it is reviewed one layer at a time, bottom first, each against the layer
below it. The layers are what `valley pending` lists under the change: the changes whose active
version has not landed, and the branches no record is on, whose heads the change's head contains.
`valley-go stack` lists them (`status/stack.go`). A withdrawn change under the change is a layer
too.

- `review` first lists the layers. It warns of each lower layer that is a draft or withdrawn, since
  the integrator refuses to land it or anything above it. Its last line says how the review goes:
  read each layer and go on with `[n]`, and approving the top lands every layer.
- When the bottom does not fast-forward from `main`, the summary ends with a prompt, before any
  layer is paged, such as
  `one, at the bottom, does not fast-forward from main: [b]ase all 3 onto main, [s]top?`.
  - `[b]ase` rebases the whole stack, as `valley rebase` does. `review` then reads the stack again,
    lists it again, and pages the rebased layers. So the heads paged are the heads approved.
  - `[s]top` ends the review, and nothing is done.
  - When the rebase is refused or left unfinished, `review` says what happened and offers only
    `[s]top`.
- Each layer's page is headed `change <k> of <n>: <name> on <below>`. The page shows the layer's log
  and its diff from the layer below. Notes taken on it are kept under its own branch.
- After each lower layer, `review` asks a prompt that names the layer and its place, such as
  `two, 2 of 3: [n]ext layer, [a]pprove one and two, [v]iew full diff, [s]top?`.
  - `[n]ext layer` goes on to the layer above. It is the usual answer.
  - `[a]pprove` approves that layer at the head shown, exactly as reviewing it alone would. That
    lands it and every layer under it, and ends the review. The prompt names what it lands:
    `one alone` at the bottom, `one and two` at the second layer, and `the <k> up to <name>` from
    the third layer up.
  - `[v]iew full diff` pages the layer's log and diff against the layer below it, and asks again.
  - `[s]top` ends the review, and nothing is done.
- After the top, the prompt names the change and says that approving it lands every layer, such as
  `three, 3 of 3: [a]pprove all 3, [v]iew full diff, [r]eject three, [s]top?`. Only the top is
  offered `[r]eject`.
- When a layer's checks fail, its prompt says approving needs a new head and stops offering
  `[a]pprove`, as for a change alone.
- A layer that is not ready says so under its heading and in its prompt, as a change alone does.
  `[a]pprove` is not offered at that layer or at any layer above it, since approving one would land
  it. Each prompt above it says why in its place, such as
  `three, 3 of 3: it would bring two, change 2 of 3, which is a draft, so it can't be approved yet. [v]iew full diff, [r]eject three, [s]top?`.
  A top that is not ready is not offered `[r]eject` either.
- Every layer's notes are printed at the end, whatever the verdict.

A change with nothing under it is reviewed against `main`.

`valley approve` and `valley rebase` are for front ends, such as the klaus dashboard, that show a
change their own way. Each runs the same code as its key, so the checks, the request write and the
refusals are the same. `approve` exits non-zero when it refuses. `rebase` does not re-review the
result, and it leaves a stack that already fast-forwards from `main` alone. Checked by `valley-cli`,
`valley-request`, `valley-status`, `valley-ready`, `valley-changes`, `review-notes` and
`integrator-e2e`.

`approve`, and so `[a]pprove`, refuses a head before any check runs when two things hold:

- the head does not fast-forward from `main`;
- the commits it would bring, from its merge base with `main` to the head, include a version head of
  another change that has not landed.

Such a head is a layer above the bottom of a stale stack. The refusal gives the command that rebases
the stack: `valley rebase <change>`. This is the project's choice, not a rule the integrator needs.
The integrator could land the head, as one commit recorded against its request alone. The changes
under it would then have no landing record of their own (see [Listing changes](#listing-changes)).
The checks would also run over a head that has to be rebased anyway. A stale head with no change
under it is approved as it is: the bottom layer of a stack, or a change alone. The integrator merges
it onto `main`.

An approval of a change with a record binds one head, and the request names exactly that head:

- `valley approve <change> --head <sha>` approves `<sha>`. It refuses unless `<sha>` is one of the
  record's version heads, active or not. The dashboard passes the head it showed (klaus's
  `approve_command`), so what is approved is what was seen.
- Without `--head`, `approve` prints the active version's head and title and asks for confirmation.
  It does this only on a terminal, and refuses when it is not on one.
- `valley review <change>` shows the active version's head, and `[a]pprove` approves that head.
- A version may name no branch. `approve --head` files its head all the same, since the request
  names the head. `review` refuses such a change, because its notes and `[r]eject` act on a branch.

`valley approve` approves a branch no record is on at its tip on origin, as the fetch left it.
`--head`, when given, must be that tip. `valley review` approves such a branch at the head it
showed. Just before it publishes, it reads the branch on origin, and refuses when it has moved off
that head, naming both heads. The operator reviewed the branch, and the commits it has moved to have
not been read.

`valley rebase <change>`, and `[b]ase` in a review, rebase the change's whole stack. The verb is
`status/rebase.go`, and the rebase engine (`rebase/`) does the work. The stack is the change and the
layers `valley-go stack` lists under it. A change with nothing under it is a stack of one. In a
throwaway worktree, bottom first, each layer's own commits are rebased onto the rebased layer below
it, and the bottom's onto `main`. So each layer keeps its own delta. Commits under the bottom that a
lower change landed through an integrator merge drop out, since their content is on `main`. The
rebase reads that landing as `valley changes` does, and verifies the transfer statement with the
same signers (see [Listing changes](#listing-changes)).

The rebase refuses, and changes nothing, when the stack cannot move whole:

- a change in it is not `ready`, records no branch, or has a branch that is missing on origin or not
  at its recorded head;
- a plain branch in it is the branch of a change that is not in the stack;
- two layers are on one branch;
- the commits to be rebased hold a merge, or a version head of a change that is not `ready` or is
  not in the stack. A rebase gives every commit a new ID, so without this check a draft's commits
  could enter the stack under IDs no record names. This is the integrator's rule for what a request
  may bring, applied before the rebase;
- it holds a change, and `valley.principal` is unset. The refusal gives the command that sets it.

A conflict in any layer pushes nothing and records nothing, and the error names the layer. Otherwise
the rebase publishes in two phases:

1. One atomic push moves every layer's branch, each leased on the tip the rebase read. Every branch
   moves, or none does.
2. Bottom first, each change's rebased head is added to its record as a new version, as the
   principal `valley.principal` names. Each is added with `valley ready --if-active <head>`, which
   writes the version only while the change is still `ready` at the head the rebase began with. The
   record ref is leased on the record that check read, so a change unreadied in the meantime stays a
   draft at its old head. A failure for one change does not stop the others.

A change alone prints its new head. A stack prints a manifest: one line for each layer, with its old
and new head, whether its branch was pushed, and whether a version was added. For each change left
unfinished, the manifest gives the exact `valley ready --head … --change … <branch>` command that
finishes it, and the rebase exits non-zero. Running the rebase again on a change whose branch holds
a head no version records is refused by the recorded-tip check, which prints the same command. When
the push's outcome is unknown, as after a transport error or when origin does not report it, the
rebase says so, prints the manifest, and says how to check: `git ls-remote origin <branches>`.

After a rebase, any ready change or plain branch outside the stack whose head contains a layer's old
head is still on that old head. A second stack on a lower layer is one. The rebase names each, with
the `valley rebase` that moves it, and does not move it. After `[b]ase`, the review goes on at the
new heads. An interrupt or a SIGTERM while the stack is being prepared kills git and any hook it
started, removes the worktree, pushes nothing, and exits 130.

The rebase engine is a Go package that a program imports, such as a service that keeps ready changes
based on `main`. It never exits the process: every step that runs git takes a `context.Context`.
Cancelling it kills git and, for local commands, the process group it runs in, including any hook or
filter it started; `ls-remote`, `fetch` and `push` stay in the caller's process group so ssh can
read the terminal, and git is killed alone. It works on a family of layers rather than one chain.
Linear history gives each layer at most one base, so the layers on one bottom layer form a tree: a
chain such as A → B → C, or a shared lower layer such as A → B and A → C. Its steps are:

1. **Capture.** From one snapshot of origin, read the family that a change or branch belongs to: the
   chain under it, as `valley rebase` takes, or every layer connected to it. Every layer keeps its
   captured head, branch tip and record commit. Each layer has a state: `ready`, `draft`,
   `withdrawn`, a plain branch, `submitted` or `uncertain`. The caller decides from the states
   whether to move the family.
2. **Check.** Refuse what cannot be rebased whole, with the refusals listed above.
3. **Prepare.** Rebase every layer in a throwaway worktree and publish nothing. A shared lower layer
   is rebased once, and each layer on it is rebased onto its new head. A conflict or an empty layer
   anywhere refuses the whole family. The prepared heads stay reachable under local refs until the
   caller releases them. A Prepare that fails or is cancelled leaves no worktree and no local ref
   behind.
4. **Publish.** Read origin again, then move every branch of the family in one atomic leased push.
5. **Append.** Add each change's prepared head as a version, lower layers first, one change at a
   time, with the `ready --if-active` rule. The caller may stop between appends.

Each step returns Go values rather than prose: a refusal with its kind and layer, and for each layer
its prepared head, what became of its branch, and what became of its record. A push's outcome is
read from git's porcelain report. So a push that origin refused is told apart from one whose outcome
is unknown, and a record write keeps the commit it tried to push. Only an outright refusal counts as
refused: `[rejected]`, which git decides before it sends anything, or origin's own
`[remote rejected]`. A ref that origin sent no status for, or a status the engine does not
recognise, leaves the outcome unknown, since origin may have taken the update. `valley rebase` words
these results for the terminal. Checked by `rebase-unit`, over real repositories and real pushes.

`valley ready [--head <sha>] [--change <id>] [--if-active <sha>] [--stamp] [<branch>]` writes a
change's record (`status/ready.go`). The branch is the current one unless named. The head is
`--head` when given, and otherwise the branch's head on origin. Either way the head must already be
on origin. klaus passes `--head` with the head it pushed, so the version names exactly that head.
ready reads the existing record, if there is one, and adds a version. For a ready change, it refuses
a head equal to the active version's. It pushes the record ref with a lease on the value it read,
and prints the handle and version, such as `cosmo nxvp v2 ready`. A draft or a withdrawn change
comes back by `ready` too. At the head of its active version, it is ready again at that version, and
the record commit's one parent is the record before it. At another head, it gains the next version.
The record names the principal from `git config valley.principal`. The host derives the pushing
principal from the key, and the client cannot ask it, so the hook refuses a record that names anyone
else. `--if-active <sha>` adds the version only while the change is `ready` at an active version
whose head is `<sha>`.

ready takes the change ID from the first of these that has one:

1. `--change <id>`;
2. the head commit's `change-id` header, as jj writes it, read from the commit object itself;
3. a `Change-Id:` trailer in the head's message;
4. a new ID, which it prints.

A header and a trailer that disagree are refused, and so are malformed values. IDs on other commits
of the change are not read. ready also refuses a change whose commits, from the merge base with
`main` to the head, include a merge, or that has more than one merge base. `--stamp` amends the
local branch's head to carry the ID as a trailer, and pushes the branch with a lease. It only starts
a new change. It refuses a change that already has a record, so it never decides whose a change is
from `valley.principal`, which is only what the checkout claims. It also refuses a head that is
already on origin.

A change is named by its full ID or by its handle. The handle is the shortest prefix of the ID, at
least four letters, that no other change recorded on origin shares. A change can also be named by
its branch. Every verb that takes a change takes all three (`status/changes.go`). `valley pending`
and `valley changes` write a local handle cache at `valley/handles` under the common git directory,
ready changes before drafts, with each change's handle, state, branch and title
(`status/handles.go`). The zsh completion script (`completions/_valley`) offers those handles with
their titles and never fetches.

`review`, `approve` and `rebase` read a branch name further, whether it is written as `<branch>` or
`origin/<branch>`. They read every record on origin and look for the ones whose active version is on
that branch:

- If there are none, the name is a branch.
- If there is exactly one, the name is that record's change, exactly as if its ID had been given.
- If there is more than one, the verb refuses and lists their change IDs, so that one can be named.

So whether a change's record is read never depends on how the change is named. These three verbs
need the `valley-go` helper for that reason.

`review`, `approve` and `rebase` act on a change with a record at a head its record names. They
never act on the branch's tip. A record's branch is only where its head is expected to be, and
anyone who can push the branch can move it, even in the same push as the record. `review` shows the
active version's head. `approve` files the request at exactly the head bound to it, wherever the
branch is. `rebase` and `review`'s `[r]eject` act on the branch itself, so they refuse when the
branch's tip on origin is another commit than the active version's head, and the refusal names both
commits. `[r]eject` deletes the branch under a lease on the recorded head. `approve`, `rebase`, and
`review`'s `[a]pprove` and `[b]ase` refuse a change whose record is not `ready`, and the refusal
names its state. `review` does not offer them for a change that is not ready when it shows it.
`valley-go resolve` gives these verbs the change's ID, handle, active version, recorded head,
branch, state and owner on one line, and `valley-go versions` lists every version, for `approve`.

The record can change while one of these verbs runs. Another principal may unready or withdraw the
change, or ready a new version, while a review is open or the checks run. So `approve` reads the
record again before it runs the checks, and again before it files the request. It refuses when the
change is not `ready`, or when the head it approves is not a version head. A new version does not
stop it, since the request names the head that was approved. `rebase` reads each change's record
before it starts, and again just before it pushes. It refuses when a record has changed at all in
between. These client checks save a run that would come to nothing. The integrator makes the same
check on origin.

A name that read as a plain branch can come to name a change while the verb runs. For example,
someone readies the branch, and then unreadies it, while a review of it is open. So before `approve`
files, it reads the original name again. If it names a change by then, the change must be `ready`,
and its active version's head must be the head `approve` began with. Before `rebase` pushes, it
reads each plain branch in the stack again, and refuses when one names a change by then.

`valley unready <change>` and `valley withdraw <change>` write a record that changes the state alone
(`status/ready.go`). Either may be run by the change's owner or by the operator, who holds the
`approve` grant; the hook refuses anyone else. They take a change by any of its names, and print the
handle, the active version and the new state, such as `cosmo nxvp v2 draft`. Each says when a
request for the change is pending, and `withdraw` warns, as [Change records](#change-records)
describes. `valley pending` lists the changes whose record says they are drafts in a section of
their own, at the head of each active version. A branch whose tip is a draft's head is listed only
there.

`unready` and `withdraw` stop an approval that begins after them, on the client. They do not always
stop one already under way there. An approval reads the record one last time just before it files
the request, and a change unreadied or withdrawn after that read is filed all the same. No client
can close this window. git compares only the refs a push changes, and the record ref is not one of
them, so a push cannot be made conditional on the record. The integrator narrows it: it refuses to
land any request that would bring a version of a change that is not `ready`, whatever the request is
named. It reads the records last just before it moves the target, so only a record written in the
moment between that read and the move is missed (see [The integrator](#the-integrator)).

`valley status` reads a name as `approve` does. A change with a record is read at its active
version's head, under its request name `c-<change-id>`. A branch no record is on is read at its head
on origin, under the branch's request name. It takes one `ls-remote` snapshot of origin for the
decision, and fetches the objects it names, without moving a local branch or writing `FETCH_HEAD`.
It reads ancestry first: a head already in `origin/main` is integrated, even if recording the
outcome failed. An outcome record it does not recognise is `unknown`, never a success.

### Review against the approved version

`review` sets each version it shows against the change's approved version, so that approving a
rebased change again is a short, informed step. `valley-go compare` (`status/compare.go`) makes the
comparison, and `bin/valley` prints what it says. It is information only: it files no request and
skips no check, and `[a]pprove` is the same act whatever it says. A plain branch is never compared.

The approved version is the greatest version below the one shown whose exact head one of these
names:

- an integration request on origin for the change;
- an outcome record on origin for the change that says `land`, `stale` or `reject` at that head. The
  integrator judges only requests that were filed, so each of these is an approval it judged.

Both are read under `c-<change-id>` and under the request name of each branch the change's versions
have named. A branch's request name is lossy: `a/b` and `a-b` are both `a-b`. A branch named
`c/<ID>` or `c-<ID>` maps to `c-<ID>`, which is reserved for the change with that ID, so it adds no
name. Any other branch's name is ambiguous when anything else maps to it: a branch named by any
version of another change, or a branch on origin that is not one of this change's. A record on
origin that does not read makes every branch's name ambiguous, since it could name such a branch. A
request or outcome under an ambiguous name is not evidence of approval. When the greatest version
with any evidence has only ambiguous evidence, the approved version is not known, and `review` is
ordinary review. It never searches back for an older version instead.

This is not a complete approval history. Outcome records are overwritten, and a version records
neither the base it was approved on nor when. "Approved" means filed through the request protocol;
it does not name who approved.

**The comparison.** `review` freezes `main`'s tip T as its fetch left it, and the two heads: H₀, the
approved version's, and H₁, the one shown. Each head's base is its merge base with T, and each must
have exactly one: B₀ and B₁. B₀ must be B₁ or below it on T's first-parent history. Otherwise there
is no comparison, and `review` says why above the full diff. Then `valley-delta-v1` from B₀ to H₀ is
compared with `valley-delta-v1` from B₁ to H₁. For a layer of a stack, the comparison is the layer's
own change's: the layer's ID, version and head, so a lower layer is set against its own approval.
Its delta from its base includes the layers under it, which approving it would bring.

**valley-delta-v1** (`delta/`) is the change between two git trees, as whole files. It is the set of
paths whose entries differ. Each path has a pre-image and a post-image: its entry in each tree, or
absent. An entry is a mode (`100644`, `100755` or `120000`) and the SHA-256 of the content, as in
`valley-tree-v1`; a symlink's content is its target. A rename is a deletion and an addition. So
binary content, the executable bit, symlink targets, CRLF and final newlines are all bound, and
nothing is positional. The encoding is a header line, `valley-delta-v1`, then one line per path,
sorted bytewise: `<len> <path> <pre> <post>`. `<len>` is the path's byte length in canonical
decimal, and `<path>` is its raw bytes, which a reader takes by that length. Each image is `-` or
`<mode> <sha256>`. The digest is the SHA-256 of the encoding.

The trees are read straight from git's object store with `git cat-file`. No checkout, filter,
attribute, diff driver, rename detection or replacement ref is involved. A tree is refused when a
checkout on case-sensitive Linux could not hold it faithfully: a submodule or any other mode, a name
that is empty, `.`, `..`, `.git` in any case, holds a slash or is longer than Linux's limit of 255
bytes, two entries with one name (as a file and a directory at one path are), an empty subtree,
entries out of git's order, an entry whose object is not the type its mode says, or a symlink whose
target is empty, holds a NUL or is longer than Linux's limit of 4095 bytes. Every entry of both
trees is checked, including those the delta leaves out. The parser is strict. It accepts exactly the
encodings two such trees can give, and refuses anything else, such as a length with a leading zero,
a duplicate or unsorted path, an invalid mode or hash, a symlink with the hash of empty content, or
a line whose images are equal. The one exception is a symlink whose target holds a NUL or is too
long: an encoding holds only the target's hash, which shows neither. Conformance vectors under
`delta/testdata/` fix the encoding and digest. A Python script there computes their expected results
from the specification alone, independent of the Go code. Checked by `delta-unit`.

**What review shows.** When the two deltas are equal, `review` prints, and pages nothing:

```text
<change> v<N> at <head>: clean restack of approved v<M> at <old-head>.
File delta unchanged. Base <old-base> -> <new-base>.
Landings between bases:
  <commit> <subject>
```

The landings are every commit on T's first-parent history in (B₀, B₁], oldest first.

When they differ, `review` pages this, and then `git range-diff B₀..H₀ B₁..H₁`:

```text
<change> v<N> at <head>: changed since approved v<M> at <old-head>.
Review the range-diff before approving.
Base <old-base> -> <new-base>.
Files whose change differs from v<M>'s, as mode and SHA-256 of the content:
  <path>
    v<M>: <pre> -> <post>
    v<N>: <pre> -> <post>
```

range-diff compares patches, and a binary file's patch says only that it differs, so range-diff can
call two commits equal when a binary file or a mode differs between them. The list of files shows
every such difference.

The page ends with the shown version's own diff of those files, from B₁ to H₁, in git's ordinary
form. range-diff's hunks carry no line numbers, so a note written beside them has no anchor. A note
written beside a line of that last diff keeps its file and line.

Either way, the commits from each base to its head are compared for their messages and their author
fields (name, email and date), apart from the files. A difference is shown under its own heading,
`Commit messages changed since approved v<M>.` or `Commit authors changed since approved v<M>:`,
whatever the files say. Every prompt after a change is shown offers `[v]iew full diff`, which pages
the whole diff and asks again. Checked by `valley-go-unit`, `valley-ready` and `review-notes`.

### The status object

`valley status --json <change>` prints one JSON object on one line. Front ends read it, klaus among
them, so it is a contract with a version. A change to the name or meaning of any field is a new
version.

```json
{
  "schema": "valley-status/v1",
  "change": "topic/one",
  "request_name": "topic-one",
  "target": "main",
  "target_tip": "<sha>",
  "head": "<sha>",
  "request": { "ref": "refs/the-valley/integration-requests/main/topic-one", "commit": "<sha>" },
  "outcome": { "decision": "stale", "head": "<sha>", "tip": "<sha>", "failed_at": null },
  "state": "stale",
  "change_id": null,
  "handle": null,
  "version": null,
  "title": null,
  "owner": null,
  "landing": null,
  "restack": null,
  "knots": []
}
```

- `change` is the branch as given, without `origin/`. `request_name` names its integration request:
  the branch with each `/` replaced by `-`. For a change with a record, `change` is the branch its
  active version names, or its handle when that names none, and `request_name` is `c-<change-id>`.
- `target_tip` is `main` on origin. `head` is the branch head on origin, or for a change with a
  record, the head its active version records. When the branch is gone, `head` is the request's
  commit, or else the outcome's head, or else `null`.
- `request` is `null` when origin holds no integration request for the change. `outcome` is `null`
  when origin holds no outcome record.
- `outcome.decision` is `land`, `stale`, `reject`, `refused-not-ready`, `refused-merge`,
  `voided-withdrawn`, `failed` or `unknown`. A record that does not parse is `unknown`.
  `outcome.head` and `outcome.tip` are `null` unless they are object ids, and `failed_at` is `null`
  unless the decision is `failed`.
- `change_id`, `handle`, `version`, `title`, `owner` and `landing` are `null` for a branch no record
  is on. For a change with a record they are its ID, its handle, its active version's number and
  title, its owner, and the active version's landing evidence as [Listing changes](#listing-changes)
  decides it.
- `restack` is `null` for a branch no record is on, and for a change with no restack report.
  Otherwise it is `{"ref", "commit", "current", "report"}`: the report's ref, its report commit,
  whether it is current, and the report as [Restack reports](#restack-reports) gives it. `current`
  is true only while every ref the report observed holds what it says it held, as
  [Restack reports](#restack-reports) defines it: `main`, each layer's record ref and branch, a
  branch it saw missing still missing, and each blocking request at its `tip`. A report that is not
  current is historical. The sentences say which it is, and name a current conflict's command to
  rebase by hand.
- `knots` lists the knots that link the change, as [Listing changes](#listing-changes) gives them,
  and is `[]` for a branch no record is on. When the plan graph is missing or unreadable, `knots` is
  `[]`, and `valley status --json -v` says why on stderr.
- A reader that meets a decision or a state it does not know should treat the status as unknown.
- `state` is one of these:

| `state`                 | What it means                                                                         |
| ----------------------- | ------------------------------------------------------------------------------------- |
| `integrated`            | The head is in `origin/main`.                                                         |
| `integrated-recorded`   | A `land` outcome names this head. The change landed as a different commit.            |
| `outdated-request`      | The request names a different commit from the active version's head, or the branch's. |
| `outcome-unreadable`    | The outcome record does not parse.                                                    |
| `not-requested`         | No request stands for the change. An outcome from an earlier head may remain.         |
| `awaiting-decision`     | A request stands for this head, and no outcome names it.                              |
| `awaiting-new-decision` | The outcome names this head, but `main` or the evidence has moved since it was made.  |
| `stale`                 | The integrator reported the change stale against the current `main` and evidence.     |
| `rejected`              | The integrator rejected the evidence.                                                 |
| `refused-not-ready`     | The integrator refused it: a record of its head is not `ready`. The request stands.   |
| `refused-merge`         | The integrator refused it: its commits from the merge base include a merge.           |
| `voided-withdrawn`      | The integrator deleted its request because the change was withdrawn.                  |
| `failed`                | The integrator failed while processing the request. The change may still have landed. |

An error prints no object. The message goes to stderr and the exit status is non-zero. The errors
are an unreadable origin, an origin without `main`, nothing on origin for the change, and a change
name that is not UTF-8, which JSON cannot carry exactly. Checked by `valley-status`, and by the Go
tests in `status/` for the outcome record's parser.

### Listing changes

`valley changes --json` prints every change a repository holds a record of, in every state, as the
`valley-changes/v1` object (`status/list.go`). klaus reads it, so it is a contract with a version,
like the status object. `valley pending` is the view a person reads (`status/pending.go`).

Both read one snapshot of origin for each repository they list. For each version of each change,
they decide its landing evidence (`status/inventory.go`). The first rule that applies is the answer:

1. `landed`: the version's head is on `main`'s first-parent history, and no commit on that history
   between the head and `main`'s tip is a merge. That is, `git rev-list --first-parent` from `main`
   lists the head, and `git rev-list --first-parent --merges <head>..main` lists nothing. The
   integrator never writes a merge, and a fast-forward puts every commit of a change on that
   history. Ancestry alone is not enough: a merge can drop the content of either of its parents, so
   a head below any merge has not landed by this rule.
2. `landed-recorded`: every one of these holds.
   - A commit `L` on `main`'s first-parent history, with no merge above it, carries a transfer
     statement whose `change` is one of the version's request names. The request names are the
     version's branch with each `/` replaced by `-`, and `c-<change id>`.
   - `L` has exactly one parent, as every commit the integrator lands as does.
   - The statement's `base` is the `valley-tree-v1` digest of the version's tree, and its signature
     verifies as the integrator's.
   - The integrator's outcome record for that request name says `land`, and its head is this
     version's head. Only the integrator writes outcome records: the hook refuses a push to them.
     The record names the exact head that landed, which a statement about trees cannot do.
   - The integrator's merge of the version's head onto `L`'s parent gives `L`'s tree. The client
     recomputes it as `integrator/git.go` makes it:
     `git merge-tree --write-tree --merge-base=<merge base> L^1 <head>`.

   A head with a landed tree on another parent is another delta. These rules keep it from borrowing
   the landing's evidence.

3. `uncertain`: a request for the version was consumed, or its outcome record says `failed`. The
   integrator moves `main` before it stores its evidence, so a landing can exist without its
   evidence. A request is consumed when its outcome record says `land` for this head and the request
   ref does not point at it.
4. `not-landed`: otherwise.

The outcome record alone is never proof of a landing, because a later pass can overwrite it. A
landing whose outcome record a later pass overwrote reads `uncertain`, never `landed-recorded`.

The client checks a transfer statement with `attest verify`. The signature must be by the
integrator's signing name, under a key in a known-signers file. The client takes each of these two
values from the first source that gives it (`status/signers.go`):

1. a flag: `--known-signers <file>` or `--integrator-name <name>`;
2. the environment: `VALLEY_KNOWN_SIGNERS` or `VALLEY_INTEGRATOR_SIGNER`;
3. the serving host's publication at `refs/the-valley/signers` in its instance repository (see
   [The signers publication](#the-signers-publication)). The client finds the instance repository
   the way `--all` does, below.

The client reads the publication only when a statement needs verifying, and only for a value that no
flag or variable gives. A value from a flag or a variable is used whatever the host publishes.
Before the two values are used, the client checks that the file reads as verifier keys and holds a
key under the name. When it does not, the command says so on stderr, naming where each value came
from, and verifies no statement. Without both values, no transfer statement is believed: a version
that only a statement could show landed reads as `uncertain` or `not-landed`, and the command says
so on stderr. `status`, `stack`, `rebase`, `brings`, `pending` and `plan` read a landing the same
way. Checked by `signers-e2e`.

Without `--all`, the commands list the current repository. With `--all`, they list every project the
serving host publishes at `refs/the-valley/projects`. The client reads the instance repository's URL
from `policy/valley` at `main`'s tip on origin, never from a checked-out or pushed tree. A
repository whose `main` carries `policy/instance` is its own instance. Each project's URL is the
instance's URL with its last path segment replaced, so the transport, user and host are kept. A
repository other than the current one is read through a clone under `$XDG_CACHE_HOME/valley/repos`.
When discovery is unavailable, `--all` says so and lists the current repository alone.
`--repo <name>` limits either command to one project.

`policy/valley` is data that anyone who can land on `main` controls, so the client is careful with
the URL it names. It refuses a URL that starts with `-`, which git would read as an option. It
refuses git's `<transport>::<address>` form, such as `ext::`, which runs a command. It refuses any
scheme other than `ssh://`, `git://`, `https://` and `file://`. A scp-like URL and a local path are
allowed. Every git call that takes such a URL passes `--end-of-options` first, and runs with
`GIT_ALLOW_PROTOCOL=ssh:git:https:file`, which overrides any `protocol.*.allow` in the caller's own
configuration.

```json
{
  "schema": "valley-changes/v1",
  "host": "classic-laddie",
  "repos": [
    {
      "repo": "cosmo",
      "url": "git@classic-laddie:cosmo.git",
      "error": null,
      "changes": [
        {
          "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
          "handle": "nxvp",
          "target": "main",
          "owner": "klaus-env",
          "state": "ready",
          "record": "<record commit>",
          "seq": 3,
          "active": 2,
          "versions": [
            {
              "n": 1,
              "head": "<sha>",
              "title": "the head's subject",
              "branch": "topic",
              "uploader": "klaus-env",
              "landing": "not-landed",
              "landed_commit": null,
              "request": { "ref": "refs/the-valley/integration-requests/main/topic", "commit": "<sha>" },
              "outcome": { "decision": "stale", "head": "<sha>", "tip": "<sha>" }
            }
          ],
          "includes": ["<change id>"],
          "restack": null,
          "knots": [
            {
              "id": "oc-6335749",
              "type": "outcome",
              "status": "open",
              "title": "Ready changes stay landable as main moves"
            }
          ]
        }
      ],
      "branches": [{ "branch": "loose", "head": "<sha>" }]
    }
  ]
}
```

- The object is one line. `host` is the host the instance's URL reaches, or origin's without
  `--all`, with its port and without its user. A local path reaches `localhost`.
- A repository that cannot be read has its message in `error`, and empty `changes` and `branches`.
  The others are listed all the same.
- Changes are keyed by repository, target and change ID, in the order of their IDs. `state` is
  `ready`, `draft` or `withdrawn`. `record` is the record commit.
- A handle is unique among every change listed, in every repository. Every record of a listed
  repository counts, so a handle also names its change within that repository alone. When one change
  ID is recorded in more than one listed repository, each of those entries has the qualified handle
  `<repo>:<prefix>`, such as `cosmo:nxvp`. git refuses a colon in a ref name, so a qualified handle
  is never also a branch, and never reads as `origin/<branch>`. Every verb that takes a handle
  accepts the qualified form, in the repository it names, and refuses one that names another
  repository.
- `landed_commit` is the commit on `main` the version landed as: its own head when `landed`, the
  transfer statement's commit when `landed-recorded`, and otherwise `null`.
- `request` is the request filed at the version's head under either of its request names, or `null`.
  `outcome` is the outcome record about the version's head, or `null`; its `tip` is `null` unless it
  is an object ID. Its `decision` takes the values `valley-status/v1` gives it, the refusals
  included.
- `includes` lists the changes whose active head is a strict ancestor of this one's, nearest only.
  It is ancestry alone: approving this change lands their content too.
- `restack` is the change's latest restack report, or `null`, with the shape and the meaning of
  `current` that `valley-status/v1` gives it.
- `knots` lists the knots the change does: every knot in its repository's plan graph whose `changes`
  lists `valley:<repo>/<change ID>`, with its ID, type, status and title. Active knots come first,
  then terminal ones, each group in ID order. Links to other projects or to GitHub are ignored. Each
  repository's graph is read once per command. When the graph is missing or unreadable, every change
  gets `[]`, and `-v` says why on stderr.
- `branches` lists each branch on origin, other than `main`, that `main` does not reach and that is
  no change's active head. A branch name that is not UTF-8 is left out, with a warning on stderr.

`valley pending` prints three sections. `ready` holds the ready changes whose active version is
neither `landed` nor `landed-recorded`. `branches` holds the branches no change is on. `drafts`
holds the drafts. When no ready change is at the top of a stack, one line says so in place of
`ready`. That line also says where any ready change inside another section's stack is. With `--all`,
each repository is headed by its name.

Each entry is a block. Its first line is its name, with the age and author of its head: the handle
and version for a change, the branch for a branch, and the handle and version on its branch for a
draft. The lines under it give the title and what the entry adds to its base, in commits and diff. A
change's title is its record's, for the active version. A branch's title is the subject of its first
own commit. When a knot links a change, the first knot's ID follows the title, as
`stack-c: build on stack-b · oc-6335749`. The title is cut short to make room; the knot ID is kept
whole.

An entry's base is the nearest other entry its head strictly contains, or `main` when there is none.
That is the ancestry `includes` reads. A stack is an entry no other entry contains, the top, then
every entry the top contains. Those entries are listed under the top at one indentation, however
deep the stack is. Each entry is listed before the entries it contains. Approving the top lands
everything listed under it, and the top says how many entries that is.

Each listed entry is on the entry listed right after it. Where it is not, it says `on <base>`. That
happens at a fork, where two entries are on one base: the base is listed under only one of them, and
the other says `on <base>`. A stack is printed in the section of its top entry. A listed entry from
another section is marked with it, as `(ready)` marks a ready change inside a branch.

A change also says `includes <handle>` for a change its head contains that is not an entry, such as
a withdrawn one, and `uncertain` when that applies. It says `restack:` and what its restack report
says when that report is current. A historical report is left out. `--checks` adds the checks each
change requires, by cost, as `valley checks` derives them. Within a section, the stack with the
latest commit comes first. Every line fits the terminal's width, or 80 columns when stdout is not a
terminal. A title is cut short with `…`, and a handle or a branch never is. On a terminal, names are
bold, unless `NO_COLOR` is set.

`valley pending --json` prints `valley-pending/v1`. It has the shape of `valley-changes/v1`, with
the human view's sections. `changes` holds only the ready changes that have not landed. Each
repository has a `drafts` array as well, holding the drafts, with the same entry shape, including
`knots`. `branches` is the same as in `valley-changes/v1`. Its `includes` counts only changes that
have not landed.

```text
ready
pppp v1                                                 1 minute ago · klaus-env
  stack-c: build on stack-b · oc-6335749
  1 commit · 1 file +5 −0
  approving it lands the 2 below too
  ├ mmmm v1                                            2 minutes ago · klaus-env
  │   stack-b: build on stack-a
  │   1 commit · 1 file +12 −3
  └ nxvp v1                                            3 minutes ago · klaus-env
      stack-a: the first layer
      2 commits · 4 files +80 −1

vvvv v1                                                   1 hour ago · klaus-env
  lost: a change whose request was consumed
  1 commit · 1 file +1 −0
  uncertain: it may have landed without its evidence being stored

branches
loose                                                  6 seconds ago · klaus-env
  loose: work no record is on
  1 commit · 1 file +1 −0

drafts
qqqq v1 on paused                                         3 days ago · klaus-env
  paused: not ready yet
  1 commit · 1 file +1 −0
```

`review`, `approve`, `rebase`, `status`, `unready` and `withdraw` read a branch name as the change
whose active version is on it. A change whose active version is `landed` or `landed-recorded` is
skipped, so a branch name can be used again once its change has landed, whether or not the branch
has moved since. A landing read wrongly could make a draft's branch read as a plain branch. Nothing
lands from that: the integrator refuses a request at a draft's version head whatever it is named.
Checked by `valley-changes`, by `valley-status` for restack reports, and by the Go tests in
`status/` for handles, URLs and the JSON writer.

### Version and self-update

The installed `valley` package carries a version stamp: the CLI's version and the commit nix built
it from, such as `0.1.0-97db597` (`nix/packages.nix`). `valley --version` prints it. A build from a
tree with uncommitted changes ends in `-dirty`, and a build with no known commit ends in `-unknown`.
The stamp is on the package's outer wrapper and in its name only. So the packages that the checks
and the integrator use do not change with every commit. The script run straight from a checkout says
it is unstamped.

`valley self-update` keeps a user's `valley` at the tip of a branch of the-valley, without a system
rebuild. It is for a machine where `valley` is a working tool, such as an agent's guest. The machine
the operator approves from, and the valley host, keep the version their system configuration pins.
So a commit on `main` never changes the tool that approves or admits the next one.

Self-update is a Go module (`selfupdate/`) that `valley-go` runs. It mirrors klaus's self-update:
the same steps, state file and flags. Each run:

1. resolves the branch with `git ls-remote`;
2. stops if the installed `valley` was built from that commit. The state file says which commit
   self-update last installed. Before its first install, the running `valley`'s stamp says;
3. builds exactly that commit with nix:
   `nix build 'git+<repo>?ref=refs/heads/<branch>&rev=<sha>#packages.<system>.valley'`;
4. smoke-tests the build, in an environment that carries nothing of the running `valley`'s. Its
   `valley --version` must report the commit, and its `valley help` must exit 0. Its
   `valley checks --names` must derive two known checks from a tiny policy the package ships
   (`examples/policy/smoke/`), which shows that the schema the build carries composes a policy;
5. installs the build into the user's default nix profile, in place of the element named `valley`.
   The `valley` the system installs stays underneath as the fallback.

The state file, `$XDG_STATE_HOME/valley/self-update.json`, records the current install, the previous
one, and the last failure. A commit that fails to build or to pass the smoke test is skipped until
the branch moves, or until `--retry`. `--rollback` reinstalls the previous build, and `--pin` and
`--unpin` freeze and release the current one.

The repository and branch come from `--repo` and `--branch`, or from the user's git config,
`valley.selfUpdate.repo` and `valley.selfUpdate.branch`. The branch defaults to `main`. No
repository's own config is read. A repository is refused unless it is an `https://`, `ssh://`,
`git://` or `file://` URL, an absolute path, or `user@host:path`, with no character that nix's
flake-ref parser would rewrite. `user@host:path` is read under the login's home, as git reads it, so
`git@classic-laddie:the-valley` is `ssh://git@classic-laddie/~/the-valley` to both git and nix. Nix
fetches the repository as the user who runs self-update, with that user's ssh keys and config.

The flake's home-manager module, `homeManagerModules.valley` (`nix/home-module.nix`), runs
self-update on a timer. `programs.valley.selfUpdate.enable` adds the `valley-self-update` user
service and a timer that starts it two minutes after boot and every five minutes after. `.repo`
names the repository, and `.branch` the branch. The service runs the first `valley` on its `PATH`:
the one in the user's nix profile, then the one the user's home-manager profile installs.

Checked by `selfupdate-unit`, over a real repository with a stand-in for nix, and by
`valley-self-update`, which runs the real package's smoke test with a stand-in for nix and holds the
units the home-manager module renders. No check runs a real nix build or profile install, because a
check's build has no nix daemon and no writable store.

## The plan graph

A project's plan is data kept with the project, in its
[project storage](#the-source-and-project-storage): a graph of knots at one ref,
`refs/the-valley/knots`, in the project's repository. A knot is an outcome, a bug, an idea or a
question, in the node format `schema/node.cue` states. A question is something work can wait on
until it is answered. Its `owner` names who has to answer. The ref is a cord and each knot is tied
on it, as people "return to knotting cords and using them" in the Tao Te Ching's chapter 80. The ref
never touches `main`, and nothing that decides a change reads it: no check, policy, build or
approval. A knot that says something was approved or landed is not evidence of it.

The ref has its own root history. Its first commit has no parent, and each later commit has the
previous tip as its one parent. Each commit is one operation, and its tree is the whole graph:
`outcomes/`, `bugs/`, `ideas/` and `questions/` hold one file per knot, named `<id>-<slug>.md`, and
`comments/<id>/` holds one file per comment on that knot, named `<stamp>-<op>.md`. A knot's ID is
its type's prefix and the first 7 hex digits of the SHA-256 of its slug. When that ID is taken, the
slug gains a suffix, `-2`, `-3` and on, until the ID is free.

The knot module (`knot/`) is the one reading of the graph, shared by the CLI and the push hook. A
knot's frontmatter is a strict subset of YAML that the module owns, because Go has no YAML parser.
Keys come in a fixed order, titles and other free text are JSON strings, and lists are sorted flow
lists. The module refuses any file that does not read back byte for byte as it would write it, so
there is one way to write each knot. The form is YAML, so the knowledge lint reads it too. The
module also fixes the graph's limits as named constants: 5,000 knots, 20,000 comments, 64 KiB a
knot, 16 KiB a comment, and 32 changed files a commit.

The module holds a graph to its layout, its fields and its edges. `parent` names an outcome in the
graph. `blocked_by`, on outcomes and bugs, names knots in the graph, or another project's knot as
`<project>:<id>`, which only a reader resolves. Neither kind of edge forms a cycle.
`graduated_into`, on a graduated idea, names an outcome in the graph, when the idea is taken on as
work, or a question, when it needs settling. `changes`, on outcomes, bugs and questions, links
changes, each as `valley:<project>/<change ID>` or as `https://github.com/<owner>/<repo>/pull/<n>`.
On an outcome or a bug, they are the changes that do its work. On a question, it is the change that
records its answer in the source's docs. `valley review`, `valley pending`, `valley changes` and
`valley status --json` read these links to show the knots each change does (see
[Listing changes](#listing-changes)). A question may link none, when its answer is a comment or an
outcome that does the work. An idea links none, because it graduates into an outcome or a question.
A knot is never deleted, and its `id`, `type`, `created` and `created_by` never change. A comment is
only ever added. A new knot's `created_by`, and a new comment's `author`, name the principal that
writes it. The module and the CUE schema with the knowledge lint are two validators of one
documented schema, and `knot-agree` runs both over shared fixtures and generated graphs and fails on
any disagreement about the rules they share. The canonical form, the layout, cycles and transitions
are the module's alone, and `knot-unit` covers them.

`valley knot` is the graph's API: `new`, `show`, `list`, `set`, `link`, `unlink`, `edit`, `comment`,
`claim`, `unclaim` and `schema`, each with `--json`, and each with `--project` for another project
on the same host. A write reads the tip with one `ls-remote`, one fetch and one batched read of the
tree. It builds the new commit with git plumbing on a private index, never touching the worktree,
and holds the result to the rules the hook applies. Then it pushes with
`--force-with-lease=refs/the-valley/knots:<old tip>`. When the lease fails, the operation is formed
again on the new tip, at most 5 tries in all, after a random pause. `set`, `edit`, `link --parent`,
`claim` and `unclaim` compare before they write: a field another write changed meanwhile, a claim
another run holds, or a claim that changed before `unclaim` lands, is reported as a conflict, with
exit status 3, and is never overwritten. `--if` adds a comparison of its own, and a field it names
that no knot has is refused before anything is read. Each operation carries a `Valley-Op` trailer
whose ID it keeps across every try. After a push whose outcome it cannot tell, the CLI looks for
that ID among the ref's latest 64 commits before trying again, so no operation is recorded twice. A
push the hook refuses is not tried again, and the CLI prints what the hook said. Checked by
`valley-knot`, against bare repositories with no hook, and by `knots-e2e` through the hook.

The host decides every push to the ref in `valleyhook` (`valleyhook/knots.go`), with the knot
module. It matches `refs/the-valley/knots` exactly, so a ref that only begins with that name stays
in the closed namespace. Any named principal may write the graph, and an untagged key may not. Every
writer can change every knot in every project's graph: with two principals, the operator's and
`klaus-env`, that is intended, and a principal added later gets the same reach. A push that creates
the ref brings one commit with no parent. An update brings one commit whose only parent is the old
tip, so a push is one operation and the ref never rewinds. No push deletes the ref; a graph that
must be recovered is recovered on the host, and its history stays the record. The commit's committer
is the pushing principal, and its message ends with a `Valley-Op` trailer.

The hook reads the old and new trees with the bounded walk attestations use, with the graph's own
budget: trees 3 deep, 20,000 entries in one tree and 30,005 in all, a shared subtree counted each
time a tree names it, 2 MiB a tree, 64 MiB read in all, and 5 seconds. When the 5 seconds pass, the
hook stops the git process it reads with, and refuses the push. It refuses one whose check ends late
as well, even if every read began in time. The walk refuses any tree entry git does not write, since
the host does not run git's fsck on what a push brings. An entry's name is not empty, `.` or `..`,
and holds no slash, so a root entry named `ideas/<id>-<slug>.md` is refused. Its mode is exactly one
of git's five, so `040000`, which git reads as a tree, is refused too. Attestations are walked the
same way. The hook then holds the push to the module's rules. The layout is checked exhaustively:
directories of mode `40000`, each an entry of its own, and plain files of mode `100644` at the
layout's paths and nowhere else, no empty directory, no repeated name, valid UTF-8 with no control
character but newline and tab. Every knot and comment the commit adds or changes is read and
checked, against the old tree for the transitions, as the pushing principal for `created_by` and
`author`. Every tip the hook admitted was valid, so it reads only those files and the knots their
edges reach, but it checks the counts from the listing on every push, and a cycle is looked for
along the edges of every knot the push changes. A push that also carries an invalid change record,
attestation or integration request is refused whole, like any push with one refused update.

Measured at the limits, on the development VM: an update adding a comment to a graph of 5,000 knots
and 19,999 comments takes the hook 0.65 s when the comments are spread over every knot, so the walk
reads a directory of comments for each, and 0.1 s when they are all on one knot. One changing the
knot at the head of a 5,000-knot `blocked_by` chain of knots of about 250 bytes, so the cycle walk
reads every knot, takes 0.33 s. A typical update of a graph of 100 knots takes 13 ms. The cycle walk
counts against the 64 MiB budget, so a chain of more than about 4,000 knots of 16 KiB each cannot be
extended until it is shortened; knots of a few KiB never come near it. Reading the largest graph
with `valley knot list` takes 0.4 s.

`valley knot list --ready` and `show` derive what the graph does not store. A knot is ready when it
is an outcome or a bug, is active and unclaimed, and every knot it is blocked by is terminal. An
idea is never ready. Nor is a question: it is answered, not picked up as work. A question clears the
work waiting on it when it is answered or dropped. A blocker that ended abandoned, discarded,
superseded or dropped still clears, and the view marks it. A parent's progress is its terminal
children that are not abandoned, out of its children that are not abandoned. A claim older than a
day is flagged as stale. A write that moves a knot to a terminal status clears its claim in the same
commit, and a claim left on a terminal knot is ignored. `show` escapes control characters and
characters that reorder text before printing, though the graph holds none.

### The plan view

`valley plan` shows a project's plan graph as a tree (`status/plan.go`). It is the view the operator
reads to see where things stand without asking anyone. It reads the graph as the knot verbs do: one
`ls-remote`, one fetch and one batched read.

The roots of the tree are the outcomes with no parent: the milestones. Under each root come the
knots whose parent it is, indented, and their children under them, at every depth. Within a level,
knots are in the order they were created, then by title, then by ID. Active knots with no parent
that are not outcomes are listed after the roots, under `no milestone`.

Each knot shows a glyph for its state, then its ID and its title. These are the states:

| Glyph | State         | What it means                                                                                    |
| ----- | ------------- | ------------------------------------------------------------------------------------------------ |
| ○     | `ready`       | An active outcome or bug, unclaimed, whose blockers are all terminal.                            |
| ◐     | `in-progress` | An active knot that is claimed, or whose status is `in-progress`.                                |
| ⊘     | `blocked`     | An active knot with a blocker that is not terminal, or that cannot be read.                      |
| ◇     | `active`      | Any other active knot, such as an idea or an open question.                                      |
| ✓     | `done`        | A terminal knot that finished: `done`, `closed`, `answered` or `graduated`.                      |
| ✗     | `abandoned`   | A terminal knot that ended without finishing: `abandoned`, `discarded`, `superseded`, `dropped`. |

An active knot also shows its claim: the run and the claim's age, marked `stale` past a day. It
shows its owner, and its blockers by ID. A finished blocker is left out. A blocker that ended
abandoned, discarded, superseded or dropped clears the knot, and is shown with how it ended. A
reference to another project's knot, `<project>:<id>`, in `blocked_by` or `target`, is shown as
written. It is marked `dangling` when that project's graph cannot be read or holds no such knot.

A root shows its progress at the right margin, as a count and a bar. Progress counts every
descendant, not only the children: those that are terminal and not abandoned, out of those that are
not abandoned. A knot under an abandoned knot still counts. A root whose descendants are all
terminal is shown as one line.

Three sections follow the tree:

1. `waiting on you` lists the active outcomes and bugs, and the open questions, that the operator
   owns. Then it lists the changes ready for approval, as `valley pending` selects them: each one's
   handle, version, repository and title. The operator is the principal
   `git config valley.principal` names, or the one `--owner` names. When neither names one, the
   section says how to name one, and still lists the changes.
2. `in flight` lists the active knots that are claimed, the oldest claim first.
3. `open residuals` lists the open bugs. Bugs with no parent come first. The others are grouped
   under their parent.

Without a flag, `plan` shows the current repository's project. `--project` names another project on
the same host. `--all` shows every project the host publishes at `refs/the-valley/projects`, found
as `valley pending --all` finds them, each under its name. A project that cannot be read is reported
in its own section, and the others are shown all the same. A project with no graph gets one line
saying so, with the command that starts one: `valley knot new outcome "<title>"`. Lines fit the
terminal as `valley pending`'s do. On a terminal each glyph has a colour and each root's title is
bold, unless `NO_COLOR` is set.

```text
○ oc-65866d3  Slice one lands                                     2/2 ██████████

○ oc-1b5d5fe  Slice two lands                                    2/10 ██░░░░░░░░
  ✓ oc-7125815  Read the graph once
  ◇ qn-f10cc70  Choose the glyphs · owner patrick
  ◇ ida-9c65fa3 Colour the glyphs · owner patrick
  ○ oc-c9066f1  Document the schema · owner patrick
                blocked by oc-23ebda7 (abandoned)
                lands in cosmo:oc-7654321 (dangling)
  ◐ oc-c23db6f  Draw the tree · claimed by run-7 11h ago · owner klaus-env
    ⊘ oc-70947c5  Golden files · blocked by bd-6e27560
    ✓ bd-b198e52  The tree misses a level
    ○ bd-6e27560  Width is off by one
  ◐ oc-a7dff99  Old work · claimed by run-0 13d ago, stale
  ⊘ oc-f982ae7  Ship it · blocked by cosmo:oc-2172d43,
                ghost:oc-0123456 (dangling), oc-70947c5
  ✗ oc-23ebda7  Use a tree widget

○ oc-24df5c7  A late milestone                                    0/0 ░░░░░░░░░░

no milestone
  ○ bd-bee76f4  A stray report
  ◇ ida-162ff1b The graph as a web page

waiting on you (patrick)
  ◇ qn-f10cc70  Choose the glyphs · owner patrick
  ○ oc-c9066f1  Document the schema · owner patrick
                blocked by oc-23ebda7 (abandoned)
                lands in cosmo:oc-7654321 (dangling)
  ready for approval
    pqrs v1  the-valley  plan: the plan graph as a view

in flight
  ◐ oc-a7dff99  Old work · claimed by run-0 13d ago, stale
  ◐ oc-c23db6f  Draw the tree · claimed by run-7 11h ago · owner klaus-env

open residuals
  ○ bd-bee76f4  A stray report
  under oc-c23db6f  Draw the tree
    ○ bd-6e27560  Width is off by one

○ ready  ◐ in progress  ⊘ blocked  ◇ active  ✓ done  ✗ abandoned
```

`VALLEY_NOW` stands in for the clock of the knot verbs and of `plan`. It holds an RFC 3339 time. A
knot's `created` date, a claim's `claimed_at` and a comment's stamp are written at that time, and a
claim's age is read against it. The checks set it, so the dates and ages they compare are the same
on every run. It lets a writer do nothing new: the hook holds none of those times to its own clock,
so a writer could record any of them by writing the file.

Checked by `valley-plan`, which builds a graph with the real knot verbs and compares the text and
the JSON with golden files in `status/testdata/plan/`, and by the Go tests in `status/` for progress
and ordering.

`valley plan --json` prints `valley-plan/v1`, an indented JSON document. The web UI is to render it,
so it is a contract with a version, like the status object. A change to the name or meaning of any
field is a new version.

```json
{
  "schema": "valley-plan/v1",
  "now": "2026-10-15T09:00:00Z",
  "operator": "patrick",
  "projects": [
    {
      "project": "the-valley",
      "error": null,
      "tip": "<sha>",
      "roots": ["oc-65866d3", "oc-1b5d5fe", "oc-24df5c7"],
      "knots": [
        {
          "id": "oc-c23db6f",
          "type": "outcome",
          "status": "in-progress",
          "active": true,
          "title": "Draw the tree",
          "created": "2026-10-02",
          "created_by": "patrick",
          "parent": "oc-1b5d5fe",
          "owner": "klaus-env",
          "blocked_by": [],
          "claimed_by": "run-7",
          "claimed_at": "2026-10-14T21:30:00Z",
          "changes": [],
          "path": "outcomes/oc-c23db6f-draw-the-tree.md",
          "comment_count": 0,
          "ready": false,
          "waiting_on": [],
          "cleared_by_ending": [],
          "dangling": [],
          "unresolved": [],
          "children": ["oc-70947c5", "bd-b198e52", "bd-6e27560"],
          "blocks": [],
          "progress": { "done": 1, "of": 3 },
          "claim": { "by": "run-7", "at": "2026-10-14T21:30:00Z", "age_seconds": 41400, "stale": false },
          "state": "in-progress",
          "edges": []
        }
      ],
      "waiting_on_you": {
        "knots": ["qn-f10cc70", "oc-c9066f1"],
        "changes": [
          {
            "repo": "the-valley",
            "handle": "pqrs",
            "change_id": "pqrstuvwxyzkpqrstuvwxyzkpqrstuvw",
            "version": 1,
            "title": "plan: the plan graph as a view",
            "head": "<sha>"
          }
        ],
        "changes_error": null
      },
      "in_flight": ["oc-a7dff99", "oc-c23db6f"],
      "open_residuals": [
        { "parent": null, "bugs": ["bd-bee76f4"] },
        { "parent": "oc-c23db6f", "bugs": ["bd-6e27560"] }
      ]
    }
  ]
}
```

- `now` is the time the view was derived at: the clock, or `VALLEY_NOW`. `operator` is the principal
  read as the operator, or `null` when none is named.
- `projects` holds one entry for each project read: the current one, the one `--project` names, or
  with `--all` every project the host publishes, in name order, with a graph or without one.
- `error` says why a project's graph could not be read, and is otherwise `null`. `tip` is the commit
  `refs/the-valley/knots` points at, or `""` when the project has no graph.
- `roots` lists the IDs of the root outcomes, in tree order. `knots` lists every knot, in the order
  the tree lists them, terminal knots included.
- Each knot has the fields a knot has in `valley knot list --json`, with two differences. Its
  `children` are in tree order. Its `progress` counts every descendant, where `valley knot`'s counts
  only children, and it is set for every knot with children.
- Each knot has two fields more. `state` is one of the six states above. `edges` lists the knot's
  references to another project's knot, from `blocked_by` and from `target`.
- An edge has the `field` it is in, the `ref` as written, and its `project` and `id`. Its `state` is
  `resolved` when that project's graph holds the knot, and then `status` and `title` are the knot's.
  It is `dangling` otherwise. `why` is then `missing` when the graph was read and holds no such
  knot, and `unreadable` when the graph could not be read. `why`, `status` and `title` are `null`
  where they do not apply.
- `waiting_on_you.knots` lists the IDs of the knots that wait on the operator, in tree order, or is
  `null` when no operator is named. `waiting_on_you.changes` lists the changes ready for approval.
  It is `null` when they could not be read, and `changes_error` then says why.
- `in_flight` lists the IDs of the claimed active knots, the oldest claim first. `open_residuals`
  lists the open bugs in groups. The group whose `parent` is `null` holds the bugs with no parent
  and comes first. Each other group's `parent` is the ID of the knot its bugs belong to.
- `waiting_on_you`, `in_flight` and `open_residuals` are `null` for a project with no graph, or
  whose graph could not be read.

Without `--all`, a graph that cannot be read prints no document. The message goes to stderr and the
exit status is 1.

## The life of a change

1. An author pushes a topic branch. The hook accepts it, and the bus gets `ref-updated`. The mirrors
   never get the branch.
2. The author marks the branch's head ready with `valley ready`. That writes the change's record at
   `refs/the-valley/changes/main/<change-id>`, with the head as a version. `valley pending` and
   `valley changes --json` list the change by its handle.
3. The operator approves one head of the change: with `[a]pprove` in `valley review <handle>`, or
   with `valley approve <handle> --head <sha>` from a front end. The required checks run over that
   head, and one push files the evidence and the integration request
   `refs/the-valley/integration-requests/main/c-<change-id>` at that head. The hook verifies each
   note and the `approve` grant.
4. On its next pass the integrator reads the records. It refuses a request that would bring a
   version of a change that is not `ready`, and one whose commits hold a merge. Otherwise it builds
   the candidate, derives the policy, judges the evidence, and lands or refuses. If the author
   unreadies or withdraws the change before it lands, the request stays and does not land; a
   withdrawal also deletes it. Only a record written in the moment before the target moves is
   missed.
5. On a landing, the publisher sends `ref-updated` to the bus and pushes `main` and tags to the
   mirrors. `valley status <handle>` shows the outcome, and the version's landing evidence.

A branch no record is on is approved by its name and requested under it.

## How cosmo deploys it

This section describes the reference deployment (**deployment choice**): `cosmo` is the reference
NixOS fleet, `klaus` is the agent runner, `classic-laddie` is the primary valley host, and `qinling`
is the instance repository.

cosmo's `hosts/classic-laddie/default.nix` imports `the-valley.nixosModules.valley-host` and points
`services.valley.config` at `hosts/classic-laddie/valley.cue`. It turns on the bus, the integrator
and the identity compiler, with the private repository `qinling` as the instance project. The host
declares a small set of static keys beside the compiled registry keys.

`valley.cue` declares two projects, `the-valley` and `qinling`. Both protect `main`. `the-valley`
mirrors to GitHub at `gunk-dev/the-valley`, and `qinling` has no mirror. The mirror push uses the
git user's ssh key, from an agenix secret. Backups are a nightly restic run of `/srv/git` to an
offsite Storage Box over sftp, kept 7 daily, 4 weekly and 6 monthly. The backup target still needs
provisioning, so the run logs a failure until it is set up.

## Not deployed or not wired yet

| What                                        | State                                                                                              |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `sigverify`                                 | On `main` and tested. Nothing calls it.                                                            |
| Hardware-backed keys in the registry        | `schema/identity.cue` accepts `ssh-ed25519` keys only.                                             |
| SSHSIG evidence                             | Attestations use the signed-note format.                                                           |
| Approvals                                   | The integrator checks evidence only. No approval is required for any path class.                   |
| Approval carry                              | `valley checks` reports a change's carry mode. Nothing acts on it.                                 |
| The restacker                               | `valley-restacker once --report-only` builds the writes of a pass (`restack/`). Nothing is pushed. |
| The check worker                            | `valley-worker` publishes evidence for ready changes (`status/worker.go`). No host runs it.        |
| Reviews                                     | `review/` defines the record and its history. The hook refuses every push to them.                 |
| A crash-safe landing ledger                 | Landing is several separate ref updates.                                                           |
| Policy and registry snapshot in the outcome | The outcome record holds the head, the tip and an evidence hash.                                   |
| Registry compile on a qinling landing       | The compiler runs on its 5-minute timer only.                                                      |
| Bus authentication                          | The bus listens on localhost with no authentication.                                               |
| Other projects on the valley                | cosmo and klaus are not hosted on the valley.                                                      |
| Self-update in the agent guest              | The home-manager module exists. cosmo does not enable it yet.                                      |
