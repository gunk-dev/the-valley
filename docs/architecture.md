# Architecture as deployed

This document describes what the-valley runs today, read from the code, and the architectural
principles its design follows. It makes four kinds of statement. A principle is a rule the design
follows, with the reason for it; the code that carries it out is cited like any other behaviour.
Behaviour is what the code does, cited to the files that do it. A deployment record reports an
observation of a host, and names the record and its date. A guarantee names the flake check
(`checks.x86_64-linux.<name>`) or test that demonstrates it; a statement of behaviour with no named
check is unchecked. Security and rollouts are in [security.md](./security.md), planned work is in
[roadmap.md](./roadmap.md), and the project's purpose and principles are in
[purpose.md](./purpose.md).

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
| `refs/the-valley/integration-requests/<target>/<c>` | Principals with the `request` grant                                                              | Create, update and delete. A change with a record is requested as `c-<change-id>` (see [The integrator](#the-integrator)).                                              |
| `refs/the-valley/changes/main/<change-id>`          | Any named principal creates; the owner or a `request` holder updates; a `request` holder deletes | A change record. The hook allows only the transitions in [Change records](#change-records) (`valleyhook/record.go`). The integrator reads it to refuse.                 |
| `refs/the-valley/knots`                             | Any named principal                                                                              | The plan graph, that name exactly. One commit on the tip that keeps the graph valid; never deleted (see [The plan graph](#the-plan-graph), `valleyhook/knots.go`).      |
| `refs/the-valley/projects` (instance repository)    | Nobody                                                                                           | `valley-init` writes it on disk: the host's project list (see [valley-init](#valley-init-and-the-push-hold)).                                                           |
| Every other `refs/the-valley/*`                     | Nobody                                                                                           | This covers the integrator's outcome records, `refs/the-valley/integration-outcomes/<target>/<c>`.                                                                      |
| `refs/replace/*`, `refs/notes/*`                    | Nobody                                                                                           | A replace ref would let git show other content under a watched name.                                                                                                    |
| Symbolic refs                                       | Nobody                                                                                           |                                                                                                                                                                         |
| Tags and every other ref                            | Principals named in a project grant: `grants.<name>: {refs, writers}` (`schema/valley.cue`)      | Grant patterns cannot name `heads`, `replace`, `notes` or `the-valley`.                                                                                                 |

Branches and tags hold the source. The namespaces under `refs/the-valley/` are project storage, as
[The source and project storage](#the-source-and-project-storage) describes.

The `request` grant comes from two places: the host's declared `services.valley.grants.request` and
the identity compiler's `grants` file. Why replace refs are closed is recorded in
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
under the name a branch's request has, for the branch of each of the change's versions. Only a
`request` holder may touch a request ref, so the warning says the operator must drop it if the
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

| Transition                | Who                             | `P`        | What the record must say                                                                                                                                                                            |
| ------------------------- | ------------------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Create                    | Any named principal             | `[h]`      | One version: `n` 1, `head` `h`, and the pusher as `uploader`. The pusher as `owner`, `seq` 1, `active` 1, `state` `ready`. `target` and `change_id` are the ref's.                                  |
| New version               | The owner or a `request` holder | `[old, h]` | `old`'s versions unchanged, plus one with the next `n`, `head` `h` other than the active head, and the pusher as `uploader`. `active` is the new `n`, `state` `ready`, `seq` one more than `old`'s. |
| Unready                   | The owner or a `request` holder | `[old]`    | Everything as in `old`, except `state` goes from `ready` to `draft` and `seq` goes up by one. `active` stays.                                                                                       |
| Withdraw                  | The owner or a `request` holder | `[old]`    | Everything as in `old`, except `state` goes from `ready` or `draft` to `withdrawn` and `seq` goes up by one. `active` stays.                                                                        |
| Ready again, same version | The owner or a `request` holder | `[old]`    | Everything as in `old`, except `state` goes from `draft` or `withdrawn` to `ready` and `seq` goes up by one. `active` stays.                                                                        |
| Ready again, new version  | The owner or a `request` holder | `[old, h]` | As for a new version, from `draft` or `withdrawn`.                                                                                                                                                  |
| Delete                    | A `request` holder              | —          | Allowed, to keep storage tidy.                                                                                                                                                                      |

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
takes this numbering as it is. For now, a change is owned by the principal whose key pushed its
first record, so `klaus-env` owns every agent change.

A record keeps every head it names reachable, because each head is a parent of a record commit. So
records retain source objects the way topic branches do, and the hook restricts them no further.

Checked by `valleyhook-unit`, with a case for each rule of each transition against real git objects,
by `change-unit`, which runs the parser's tests and its fuzz target's seed corpus, and by
`valley-ready`, which drives the transitions through real pushes.

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

## Policy

A project's required checks come from three CUE layers, unified as
`floor & templates[type] & project` (`schema/verification.cue`):

- **The schema**, `schema/verification.cue`, ships inside the `valley` and integrator packages
  (`nix/packages.nix`).
- **The floor and templates** come from the owning valley's instance repository, at
  `policy/instance/*.cue` on its `main`. `policy/valley` names that repository. A value the floor
  writes as `true` is mandatory. A template value written `bool | *true` is a default a project may
  turn off.
- **The project layer** is `policy/project/*.cue`, read at the tip of the target branch.

Both layers are read from integrated tips, never from the change being judged
(`integrator/policy.go`). A path class maps path globs to checks. A change requires the union of the
checks of every class one of its paths matches. A path no class matches requires the `unclassified`
checks. The integrator gets this answer by running `valley checks`, which diffs with renames counted
on both sides. Checked by `policy-deriver` and `cue-vet`.

The derivation fails closed. A failed read of the diff, the target, a policy layer or the composed
policy makes `valley checks` exit non-zero and print no report. Without that, the failure would read
as an empty list, and an empty list derives a change that requires nothing. The integrator also
refuses output that is not a whole report: no header, no required-checks section, or a
required-checks heading with no checks under it. It believes a report of no changed paths only when
git agrees that the two trees are the same. Checked by `policy-deriver`, `valley-request` and
`integrator-unit`.

A check also declares a `cost`, `quick` by default. A `slow` check takes minutes, or needs special
hardware such as KVM. `valley checks` reads the cost from the composition it derives from. After the
report it prints two lines, `quick:` and `slow:`, each listing the required checks of that cost.
`valley checks --names` prints the required check names alone, one per line, and `--cost quick` or
`--cost slow` keeps one set. The integration gate does not read the cost. Approving runs every
required check, and the integrator requires evidence for every required check, whatever its cost.
The cost changes neither which checks a change requires nor what the integrator accepts. Checked by
`policy-deriver` and `integrator-unit`.

the-valley's own project layer is `policy/project/policy.cue`. It gives each part of this repository
a class that requires the checks reading that part. A Go module requires its unit tests, and its
end-to-end check where it has one. The CLI requires its script and status checks. The Nix, the
schemas and the policy require their evaluations and vets. The NixOS module also requires the
end-to-end checks that run what it renders, and two of those boot NixOS machines. A change to
`flake.nix` or `flake.lock` does not require them, so a nixpkgs bump boots no machine. A check's own
files, such as its script under `nix/checks/`, require that check. A change to documentation alone
requires the floor's `prose-format` and nothing else. A project layer that stopped composing would
stop every derivation once it landed, including the derivation of its own fix. So the `own-policy`
check composes the layer with the example floor before it lands. It fails if the layer names a check
this flake does not define, if a script under `nix/checks/` requires no check of its own, or if a
docs-only change would require more than `prose-format`.

The reasons for CUE here, and for the mandatory-versus-default encoding, are in
[dcr-0f5d9b1](../archive/.the-valley/decisions/dcr-0f5d9b1-cue-config-host-module.md) and
[dcr-f41f718](../archive/.the-valley/decisions/dcr-f41f718-declared-verification-policy.md).

## Evidence: attest and notes

`attest run` runs checks against a commit exported with `git archive` (`attest/run.go`,
`attest/check.go`). A `nix` check builds the flake check's derivation. A `command` check runs a
shell command in the exported tree. If every check passes, `attest` writes a statement per check,
signs it, and stores it under `refs/the-valley/attestations/<tree>/<key-hash>`.

A statement is line-based text that opens with `the-valley/attestation/v1` (`attest/text.go`,
`schema/attestation.cue`). Its subject is the tree digest. A pure statement records the derivation,
its inputs digest and the output. An effectful statement records the environment and the time of the
observation. The envelope is a signed note: the `sumdb/note` format with Ed25519, implemented in
`note/note.go`. Checked by `attest-e2e`, `attest-conformance`, `attest-schema` and `note-unit`.

The integrator reads evidence at the head's tree and verifies each note with `attest verify`
(`integrator/evidence.go`). It does not run the checks itself. A required check is met when its note
is signed by a known signer, says the check `passed`, and one of these holds
(`integrator/verdict/verdict.go`):

- **A pure check, unmoved tree.** The landed tree is the attested tree. No inputs are compared.
- **A pure check, same inputs.** `attest inputs` on the candidate gives the same inputs digest and
  derivation hash. `attest inputs` evaluates the derivation and does not build it; evaluation can
  still build anything the flake imports from a derivation.
- **An effectful check.** No class that requires it changed between the base and the tip, and the
  evidence is younger than the check's `validity` window.

Checked by `integrator-unit` (the verdict tests) and `integrator-e2e`.

Check definitions come from the change being judged. `attest` builds the flake checks of the
exported tree, so a change can edit the check it is judged by
([bd-eaefe82](../archive/.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md),
[ida-a9e274c](../archive/.the-valley/ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md)).
the-valley's project layer declares no class for its Go, Nix and CUE paths. Whether the instance
floor adds one depends on qinling's private policy.

## Identity

The identity registry is CUE under `identity/` in the instance repository, read at its `main`
(`identity/registry.go`, `schema/identity.cue`). It declares principals and boundaries.

- A principal has a kind (`human`, `machine` or `service`), one or more `ssh-ed25519` keys, and
  grants. A key may also sign attestations, under a name it declares.
- A grant names a boundary. A boundary has a kind: `git-push`, `registry` or `request`.
- A machine, service or external principal must declare `expires`. From that day on, a successful
  compile leaves the principal out of every file. A compile that is refused keeps the previous
  files, expired entries included. Keys the host declares statically are outside the registry and do
  not expire.

`valley-identity` compiles the registry every 5 minutes, as git (`nix/valley-host.nix`,
`identity/compile.go`, `identity/render.go`). It writes three files under
`/var/lib/valley-identity/`:

- `authorized_keys.git`: one line per key of a principal with a `git-push` grant, tagged
  `environment="VALLEY_PRINCIPAL=<principal>"`. sshd reads it after the host's declared keys.
- `known-signers`: the keys that may sign attestations, for the hook and the integrator.
- `grants`: one `request <principal>` line per holder of the `request` grant.

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
`ready`, `unready`, `withdraw`, `changes`, `pending`, `knot` and `plan` verbs are a Go program,
`valley-go` (`status/`), which the package puts on the script's `PATH`.

| Command                      | What it does                                                                                          |
| ---------------------------- | ----------------------------------------------------------------------------------------------------- |
| `valley pending`             | Lists the ready changes not yet landed, then the branches no change is on, then the drafts.           |
| `valley changes --json`      | Prints every change in every state, across the host with `--all`, as `valley-changes/v1`.             |
| `valley review <b>`          | Shows a branch and offers `[a]pprove`, `[r]eject` and `[s]kip`, plus `[b]ase` when it needs a rebase. |
| `valley approve <change>`    | Approves one head of the change, as `[a]pprove` does. `--head <sha>` names the head.                  |
| `valley rebase <change>`     | Does what `[b]ase` does, and adds the rebased head to the change's record as a new version.           |
| `valley checks`              | Reports the checks the policy requires of a change, grouped by declared cost. It runs nothing.        |
| `valley status [--json] <c>` | Reads the request and outcome record for a change. `--json` prints the `valley-status/v1` object.     |
| `valley ready [<branch>]`    | Marks a head of the branch ready as a new version of its change, in the change's record.              |
| `valley unready <change>`    | Makes a ready change a draft, as its owner or the operator.                                           |
| `valley withdraw <change>`   | Marks a ready change or a draft withdrawn, as its owner or the operator.                              |
| `valley knot <verb>`         | Reads and writes the project's plan graph (see [The plan graph](#the-plan-graph)).                    |
| `valley plan`                | Shows the plan graph as a tree, and what waits on the operator (see [The plan view](#the-plan-view)). |
| `valley tail [subj]`         | Subscribes to bus events.                                                                             |
| `valley replay [dir]`        | Publishes one `ref-updated` event per current ref.                                                    |

`[a]pprove` is the operator's approval of a change. It runs the required checks with `attest` in a
worktree of the head it approves. Then one atomic push carries the evidence refs and the integration
request, with a lease on the request ref. When the request already names this head, the push leaves
the request ref out. When nothing is required as well, there is no push. `[b]ase` rebases the branch
in a temporary worktree and pushes it with a lease. `[r]eject` deletes the remote branch after a
prompt.

A change with a record is requested as `refs/the-valley/integration-requests/main/c-<change-id>`. A
branch no record is on is requested under its name with each `/` replaced by `-`, as before records
existed. A branch whose name maps to `c-<change-id>` is refused, by `approve` and by `status`: that
name is the change's.

When the checks fail under `[a]pprove`, approving the same head again can only fail again. So the
prompt then says approving needs a new head. It offers only `[r]eject` and `[s]kip`, plus `[b]ase`
where it is offered. `[a]pprove` comes back once the change is at another head, as it is after
`[b]ase`. A refusal that is not a check failure, such as a lost lease race, keeps the prompt as it
was.

A change with a stack under it is reviewed one layer at a time, bottom first, each against the layer
below it. The layers are what `valley pending` lists under the change: the changes whose active
version has not landed, and the branches no record is on, whose heads the change's head contains.
`valley-go stack` lists them (`status/stack.go`). A withdrawn change under the change is a layer
too.

- `review` first lists the layers. It warns of each lower layer that is a draft or withdrawn, since
  the integrator refuses to land it or anything above it. It says so when the bottom does not
  fast-forward from `main`.
- Each layer's page is headed `change <k> of <n>: <name> on <below>`. The page shows the layer's log
  and its diff from the layer below. Notes taken on it are kept under its own branch.
- After each lower layer, `review` asks `[n]ext, [a]pprove up to here, [s]kip`.
  `[a]pprove up to here` approves that layer at the head shown, exactly as reviewing it alone would.
  That lands it and every layer under it. `[s]kip` stops, and nothing is done.
- After the top, the prompt is the usual one. A stack is never offered `[b]ase`, because rebasing
  one layer would break the layers above it. Only the top is offered `[r]eject`.
- Every layer's notes are printed at the end, whatever the verdict.

A change with nothing under it is reviewed against `main` as before.

`valley approve` and `valley rebase` are for front ends, such as the klaus dashboard, that show a
change their own way. Each runs the same function as its key, so the checks, the request write and
the refusals are the same. `approve` exits non-zero when it refuses. `rebase` does not re-review the
result, and it leaves a branch that already fast-forwards from `main` alone. Checked by
`valley-cli`, `valley-request`, `valley-status`, `valley-ready`, `valley-changes` and
`review-notes`.

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

`valley rebase <change>`, and `[b]ase` in a review, rebase a change with a record at its active
version's head. They push the branch that version names, with a lease, and then add the rebased head
as a new version, as the principal `valley.principal` names. They add it with
`valley ready --if-active <head>`, which writes the version only while the change is still `ready`
at the head the rebase began with. The record ref is leased on the record that check read, so a
change unreadied in the meantime stays a draft at its old head. `rebase` refuses a version that
names no branch. After `[b]ase`, the review goes on at the new version.

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
its branch, as it was before records existed. Every verb that takes a change takes all three
(`status/changes.go`).

`review`, `approve` and `rebase` read a branch name further, whether it is written as `<branch>` or
`origin/<branch>`. They read every record on origin and look for the ones whose active version is on
that branch:

- If there are none, the name is a branch, as it was before records existed.
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
names its state. `valley-go resolve` gives these verbs the change's ID, handle, active version,
recorded head, branch and state on one line, and `valley-go versions` lists every version, for
`approve`.

The record can change while one of these verbs runs. Another principal may unready or withdraw the
change, or ready a new version, while a review is open or the checks run. So `approve` reads the
record again before it runs the checks, and again before it files the request. It refuses when the
change is no longer `ready`, or when the head it approves is no longer a version head. A new version
does not stop it, since the request names the head that was approved. `rebase` reads the record
again before it starts, and again before it pushes. It refuses when the change is no longer `ready`,
or when its active version is at another head than the one the rebase began with. These client
checks save a run that would come to nothing. The integrator makes the same check on origin.

A name that read as a plain branch can come to name a change while the verb runs. For example,
someone readies the branch, and then unreadies it, while a review of it is open. So before `approve`
files and before `rebase` pushes, the verb reads the original name again. If it names a change by
then, the change must be `ready`, and its active version's head must be the head the verb began
with.

`valley unready <change>` and `valley withdraw <change>` write a record that changes the state alone
(`status/ready.go`). Either may be run by the change's owner or by the operator, who holds the
`request` grant; the hook refuses anyone else. They take a change by any of its names, and print the
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

### The status object

`valley status --json <b>` prints one JSON object on one line. Front ends read it, klaus among them,
so it is a contract with a version. A change to the name or meaning of any field is a new version.

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
  "landing": null
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
- `change_id`, `handle`, `version`, `title`, `owner` and `landing` were added to `valley-status/v1`.
  Each is `null` for a branch no record is on. For a change with a record they are its ID, its
  handle, its active version's number and title, its owner, and the active version's landing
  evidence as [Listing changes](#listing-changes) decides it.
- `refused-not-ready`, `refused-merge` and `voided-withdrawn` were added to `valley-status/v1` as
  decisions and as states. A reader that meets a decision or a state it does not know should treat
  the status as unknown.
- `state` is one of these:

| `state`                 | What it means                                                                         |
| ----------------------- | ------------------------------------------------------------------------------------- |
| `integrated`            | The head is in `origin/main`.                                                         |
| `integrated-recorded`   | A `land` outcome names this head. The change landed as a different commit.            |
| `outdated-request`      | The request names an older commit than the branch head.                               |
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
   ref no longer points at it.
4. `not-landed`: otherwise.

The outcome record alone is never proof of a landing, because a later pass can overwrite it. A
landing whose outcome record a later pass overwrote reads `uncertain`, never `landed-recorded`.

The client checks a transfer statement with `attest verify`. The signature must be by the name in
`VALLEY_INTEGRATOR_SIGNER`, under a key in the file `VALLEY_KNOWN_SIGNERS` names, which is the
identity registry's compiled known-signers file. Without both, no transfer statement is believed: a
version that only a statement could show landed reads as `uncertain` or `not-landed`, and the
command says so on stderr.

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
          "includes": ["<change id>"]
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
own commit.

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
a withdrawn one, and `uncertain` when that applies. `--checks` adds the checks each change requires,
by cost, as `valley checks` derives them. Within a section, the stack with the latest commit comes
first. Every line fits the terminal's width, or 80 columns when stdout is not a terminal. A title is
cut short with `…`, and a handle or a branch never is. On a terminal, names are bold, unless
`NO_COLOR` is set.

`valley pending --json` prints `valley-pending/v1`. It has the shape of `valley-changes/v1`, with
the human view's sections. `changes` holds only the ready changes that have not landed. Each
repository has a `drafts` array as well, holding the drafts, with the same entry shape. `branches`
is the same as in `valley-changes/v1`. Its `includes` counts only changes that have not landed.

```text
ready
pppp v1                                                 1 minute ago · klaus-env
  stack-c: build on stack-b
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
Checked by `valley-changes`, and by the Go tests in `status/` for handles, URLs and the JSON writer.

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
there is one way to write each knot. The form is still YAML, so the knowledge lint reads it too. The
module also fixes the graph's limits as named constants: 5,000 knots, 20,000 comments, 64 KiB a
knot, 16 KiB a comment, and 32 changed files a commit.

The module holds a graph to its layout, its fields and its edges. `parent` names an outcome in the
graph. `blocked_by`, on outcomes and bugs, names knots in the graph, or another project's knot as
`<project>:<id>`, which only a reader resolves. Neither kind of edge forms a cycle.
`graduated_into`, on a graduated idea, names an outcome in the graph, when the idea is taken on as
work, or a question, when it needs settling. `changes`, on outcomes, bugs and questions, links
changes, each as `valley:<project>/<change ID>` or as a GitHub pull request URL. On an outcome or a
bug, they are the changes that do its work. On a question, it is the change that records its answer
in the source's docs. A question may link none, when its answer is a comment or an outcome that does
the work. An idea links none, because it graduates into an outcome or a question. A knot is never
deleted, and its `id`, `type`, `created` and `created_by` never change. A comment is only ever
added. A new knot's `created_by`, and a new comment's `author`, name the principal that writes it.
The module and the CUE schema with the knowledge lint are two validators of one documented schema,
and `knot-agree` runs both over shared fixtures and generated graphs and fails on any disagreement
about the rules they share. The canonical form, the layout, cycles and transitions are the module's
alone, and `knot-unit` covers them.

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
abandoned, discarded, superseded or dropped no longer blocks, and is shown with how it ended. A
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
   note and the `request` grant.
4. On its next pass the integrator reads the records. It refuses a request that would bring a
   version of a change that is not `ready`, and one whose commits hold a merge. Otherwise it builds
   the candidate, derives the policy, judges the evidence, and lands or refuses. If the author
   unreadies or withdraws the change before it lands, the request stays and does not land; a
   withdrawal also deletes it. Only a record written in the moment before the target moves is
   missed.
5. On a landing, the publisher sends `ref-updated` to the bus and pushes `main` and tags to the
   mirrors. `valley status <handle>` shows the outcome, and the version's landing evidence.

A branch no record is on is approved by its name and requested under it, as before records existed.

## How cosmo deploys it

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

| What                                        | State                                                                            |
| ------------------------------------------- | -------------------------------------------------------------------------------- |
| `sigverify`                                 | On `main` and tested. Nothing calls it.                                          |
| Hardware-backed keys in the registry        | `schema/identity.cue` accepts `ssh-ed25519` keys only.                           |
| SSHSIG evidence                             | Attestations use the signed-note format.                                         |
| Approvals                                   | The integrator checks evidence only. No approval is required for any path class. |
| A crash-safe landing ledger                 | Landing is several separate ref updates.                                         |
| Policy and registry snapshot in the outcome | The outcome record holds the head, the tip and an evidence hash.                 |
| Registry compile on a qinling landing       | The compiler runs on its 5-minute timer only.                                    |
| Bus authentication                          | The bus listens on localhost with no authentication.                             |
| Other projects on the valley                | cosmo and klaus are not hosted on the valley.                                    |
