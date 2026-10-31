# Architecture as deployed

This document describes what the-valley runs today, read from the code, and the architectural
principles its design follows. It makes four kinds of statement. A principle is a rule the design
follows, with the reason for it; the code that carries it out is cited like any other behaviour.
Behaviour is what the code does, cited to the files that do it. A deployment choice is what a host's
configuration decides, rather than the code. It is labelled **deployment choice**. A guarantee names
the flake check (`checks.x86_64-linux.<name>`) or test that demonstrates it; a statement of
behaviour with no named check is unchecked. Security and rollouts are in
[security.md](./security.md), planned work lives in
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
- `valley-worker`, the check worker, which builds and publishes pure check evidence for ready
  changes before approval (`status/worker.go`);
- `valley-web-publish`, `valley-web-receive`, `valley-web`, and the web export (`web/`). The
  publisher builds a signed export of the projects that have an effective reader and uploads it; a
  valley host can run it on a timer. `valley-web-receive` receives the uploads into a state
  directory, where `web/export` stages and activates them. `valley-web` serves the active export's
  plan, knots, and `main` source over HTTP (`GET` and `HEAD`). An edge runs the receiver and
  `valley-web` side by side from a NixOS module or a container image;
- read access, which reads and evaluates the two consents for reading a project (`readaccess/`);
- `valley-restacker`, the restacker, which rebases ready changes onto `main` as its own principal,
  one controller for each project a host lists (`restack/`);
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
forwarding on. A host's configuration can add its own block with `DisableForwarding yes` and
`PermitTTY no` (**deployment choice**). Build-time assertions refuse an sshd config that would let a
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

### The build slice

The build slice caps the total CPU and memory that all Nix daemon builds on a host can use at once.
It keeps a runaway build from starving the host's other services (knot oc-03b6f8c). A host turns it
on with `services.valley.buildSlice.enable` (`nix/valley-host.nix`). It is off by default, and
turning it on is a **deployment choice**. The check worker cannot be enabled without it
(`implemented`).

Every user who builds through the Nix daemon shares one pool of CPU and memory. That pool covers the
check worker's builds and evaluation, the daemon itself, and local builds through the daemon, such
as those an operator runs with `valley approve`. The Nix daemon runs every build as its own child
process inside the daemon's cgroup, no matter which user asked for the build. To cap those builds,
the module places `nix-daemon.service` in `valley-builds.slice` (`implemented`). By default, the
slice sets `CPUQuota` to `400%` (`buildSlice.cpuQuota`, four CPUs' worth), `MemoryMax` to `8G`
(`buildSlice.memoryMax`), and `MemorySwapMax=0` (no swap).

When the slice hits its memory cap, the kernel's OOM killer kills the single process in the slice
using the most memory. That is usually the build that pushed the slice over the cap, but it can be
an unrelated build in the slice that happens to hold more memory at that moment. Nothing exempts the
daemon's own processes from being chosen. When a child build is killed, `nix-daemon.service` stays
up because its unit delegates its cgroup, which sets systemd's `OOMPolicy` to `continue`. Services
outside the slice are unaffected.

Checked by `worker-e2e` and `module-eval`. In `worker-e2e`, a build that tries to use six CPUs is
throttled to four. The test then runs two unrelated builds alongside a worker check that allocates
memory without bound. Once the larger unrelated build has settled, the test sets the slice's memory
cap at runtime to the anonymous and shared memory the slice already holds plus half of that build's
resident memory, then starts the worker. The kernel kills at least two processes. The worker's
runaway build fails, the larger unrelated build fails, and the smaller unrelated build finishes.
`sshd`, `nix-daemon`, and the integrator controllers stay up, and no other unit fails. `module-eval`
checks that the slice renders the quota and memory cap the host configures.

The slice does not cap:

- Nix commands that bypass the daemon, such as a build `root` runs directly on the store, which run
  outside the slice and are uncapped.
- Disk space, scratch directories, `/nix/store` growth, or disk I/O.
- Exploits against shared host components. The slice limits CPU and memory use only; builds still
  share the Nix evaluator, the daemon, the kernel, and `/dev/kvm` with the host.
- Device access. The slice sets no device policy, so the daemon can still hand `/dev/kvm` to NixOS
  VM tests, whose virtual machines must fit within the slice's memory cap alongside everything else
  in the slice.

## What a push may write

Every repository's `pre-receive` hook runs `valleyhook pre-receive` (`valleyhook/main.go`,
`valleyhook/policy.go`). The hook is default-deny. If any one ref update is refused, the whole push
is refused. The hook reads the project's push policy with every key spelled exactly as its field
(`exactjson/`). A policy that holds any other key, or a key twice in one object, refuses the whole
push. A push may update at most 1000 refs, and the hook refuses one that updates more before it
decides any ref. It asks `git symbolic-ref` whether each pushed ref is symbolic, so it reads a
symbolic ref exactly as git does. Checked by `protect-e2e`, `ssh-e2e` and `valleyhook-unit`, and for
the plan graph by `knots-e2e`.

| Ref namespace                                                                 | Who may push                                                                                     | Rule                                                                                                                                                                                                                            |
| ----------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refs/heads/*` (topic branches)                                               | Any key                                                                                          | Create, update and delete.                                                                                                                                                                                                      |
| Protected refs (default `refs/heads/main`)                                    | Principals in `protection.writers` (default empty)                                               | The integrator moves them on disk, so it needs no place in `protection.writers`.                                                                                                                                                |
| `refs/the-valley/attestations/<tree>/<key-hash>`                              | Any key                                                                                          | Create only. The ref must name a tree of notes within size bounds, and each note must verify under a known signer and be about that tree (`valleyhook/attestation.go`).                                                         |
| `refs/the-valley/integration-requests/<target>/<c>`                           | Principals with the `approve` grant (also accepted as `request`)                                 | Create, update and delete. A change with a record is requested as `c-<change-id>` (see [The integrator](#the-integrator)).                                                                                                      |
| `refs/the-valley/changes/main/<change-id>`                                    | Any named principal creates; the owner, `approve` or `restack` holders update; `approve` deletes | A change record. The hook allows only the transitions in [Change records](#change-records) (`valleyhook/record.go`). The integrator reads it to refuse.                                                                         |
| `refs/the-valley/restack/main/<change-id>`                                    | Principals with the `restack` grant                                                              | A restack report about a change the repository records. Created and extended, never deleted (see [Restack reports](#restack-reports), `valleyhook/report.go`).                                                                  |
| `refs/the-valley/knots`                                                       | Any named principal                                                                              | The plan graph, that name exactly. One commit on the tip that keeps the graph valid; never deleted (see [The plan graph](#the-plan-graph), `valleyhook/knots.go`).                                                              |
| `refs/the-valley/projects` (instance repository)                              | Nobody                                                                                           | `valley-init` writes it on disk: the host's project list (see [valley-init](#valley-init-and-the-push-hold)).                                                                                                                   |
| `refs/the-valley/signers` (instance repository)                               | Nobody                                                                                           | `valley-signers` writes it on disk, and the integrator can: what clients verify transfer statements with (see [The signers publication](#the-signers-publication)).                                                             |
| Every other `refs/the-valley/*`                                               | Nobody                                                                                           | This covers the integrator's outcome records, `refs/the-valley/integration-outcomes/<target>/<c>`, its landing records, `refs/the-valley/integration-landings/<target>/<head>`, and review histories (see [Reviews](#reviews)). |
| `refs/replace/*`, `refs/notes/*`                                              | Nobody                                                                                           | A replace ref would let git show other content under a watched name.                                                                                                                                                            |
| Symbolic refs                                                                 | Nobody                                                                                           |                                                                                                                                                                                                                                 |
| `refs/refs/*`, `refs/heads/refs/*`, `refs/tags/refs/*`, `refs/remotes/refs/*` | Nobody creates or updates one                                                                    | A ref named like another ref. When that ref is missing, git reads, fetches and pushes to this one in its place. A delete follows the rule of its namespace.                                                                     |
| Tags and every other ref                                                      | Principals named in a project grant: `grants.<name>: {refs, writers}` (`schema/valley.cue`)      | Grant patterns cannot name `heads`, `replace`, `notes` or `the-valley`.                                                                                                                                                         |

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
change is readied first. Approvals have no records of their own: signed approval statements are
**not built**.

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
so an agent's principal owns every change the agent starts.

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

5. **Build the candidate.** Every change lands as one commit that the integrator writes, with the
   tip as its only parent. None of the head's own commits reaches the target, so the target's
   history holds only the trees that review showed. The base is `git merge-base` of the tip and the
   head. If the base is the tip, the candidate's tree is the head's tree. Otherwise
   `git merge-tree --write-tree` builds the tree, and a conflict makes the request stale. The commit
   keeps the head's author and message. Its committer is the integrator at
   `integrator@the-valley.invalid`. A stack approved at its top lands as one commit, with landing
   evidence for its top alone.
6. **Derive the required checks** (see Policy below).
7. **Judge the evidence** (see Evidence below). The outcome is `land`, `stale` or `reject`. A stale
   request publishes `request-stale`.
8. **Land.** The integrator signs a transfer statement that records the policy digest and the checks
   it relied on. Then, as the last thing before the commit point, it reads every record again into a
   fresh index and applies step 4 once more, since judging and signing can take minutes. Then it
   lands and records the landing in one ref transaction, as described below. Then it queues the
   landing for the publisher and publishes `ref-updated` and `integration-succeeded`.

A landing is published in this order (`integrator/refs.go`):

1. **Build every object.** The integrator writes the landing commit, then the evidence: each
   attestation ref's new tree, with the countersigned notes and the transfer statement added to what
   the ref already holds. It writes the landing record for the head (see
   [Landing records](#landing-records)) and the outcome record that says `land`. Each ref but the
   target is read here, and the value read is kept.
2. **Apply them in one transaction.** One `git update-ref --stdin` transaction (`start`, `update`
   and `delete` lines, `prepare`, `commit`) moves the target from the judged tip to the landing
   commit, moves each evidence and record ref from the value it was read at, and deletes the request
   while it still names the head that lands. git locks and compares every ref before it moves any,
   so all of them change or none does. This is the commit point.
3. **On a conflict, nothing changed.** When the target moved, nothing lands, and the request is
   judged again next pass. When another ref moved, as when a push created an attestation ref the
   landing adds to, the integrator reads the refs again, builds the objects again, and retries,
   three times in all.
4. **Notices last.** The publisher's queue and the bus events follow the transaction, as best
   effort. A failure of either is reported and changes nothing the transaction recorded.

A request that was replaced while the pass worked is a new approval. The transaction leaves it
standing, and the integrator says so. One window remains: a crash inside git's commit phase. With
the files ref backend, git renames each ref's lock file into place one ref at a time, so a crash
there can leave some refs moved and others not. A landing cut short this way can read `uncertain`.
Checked by `integrator-e2e` (scenarios for a change on the tip, merge, conflict, stale evidence,
tampered notes, retries, a moved target, a request approved again during a landing, requests named
for changes, drafts under either kind of name, a child of a draft's head, merges, a request voided
by a withdrawal, a landing record that outlasts the next version's landing, and a tree that lands
twice) and `integrator-unit`.

### Landing records

A landing record says that one head landed. The integrator writes one for each head it lands, at
`refs/the-valley/integration-landings/<target>/<head>`, in the landing's transaction
(`change/landing.go`). It names the commit the head landed as and the request name it landed under.
An outcome record is kept per request name, so the next version of a change to land under the same
name replaces it. A landing record is kept per head, so that later landing leaves it in place. No
push may write the namespace (see [What a push may write](#what-a-push-may-write)). Clients read it
to bind a transfer statement to the exact head that landed (see
[Listing changes](#listing-changes)). Its format is in
[the reference](./reference.md#integrator-landing-record-refsthe-valleyintegration-landingstargethead).

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
transfer statement), `record` (building the landing's evidence trees, landing record and outcome
record), `land` (the commit point's ref transaction), and `publish` (the publisher's queue and the
bus). A voided request gets a timing line too. A refusal that repeats is not reported again, so it
is not timed again either. The line is output only: no ref, record or event carries it. The line is
printed after the decision's outcome is recorded. It is dropped when it cannot be written within a
second, so a journal that stalls or goes away cannot keep an outcome from being recorded. Checked by
`integrator-unit` and `integrator-e2e`.

The security invariant is the operator's approval: only content the operator approved lands. The
request names the exact head that was approved, and the integrator lands only what a request names.
So a record that moves after the request is filed cannot change what lands.

Readiness is the author's workflow signal, and the integrator enforces it as closely as refs allow.
It does not enforce it atomically. The integrator reads the records last just before it moves the
target, and a record written between that read and the move is not seen. So a head the operator
approved can still land after its author has paused it. Content the operator did not approve can
never land this way. The race-free design binds each approval to the record's withdraw generation in
a signed approval statement (**not built**).

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
`valley-restacker run` (`restack/`) runs a write pass at startup and then waits `--interval`
(default 15 seconds) after each pass before starting the next, until stopped.
`valley-restacker once` runs a single write pass. Every pass reads origin whether or not `main` has
moved, so a change readied after the last landing is picked up without waiting for another landing.
If the state directory holds an open journal from earlier operations, a write pass settles that
journal first. It then plans each family, builds the pass's writes, adds them to the journal, and
pushes them (see [Publishing](#publishing)). `valley-restacker once --report-only` plans and builds
the same writes and evaluates any existing journal, without writing a journal or pushing to origin.
On a host, `services.valley.restacker` runs one controller for each configured project (see
[The restacker on the host](#the-restacker-on-the-host)).

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
once while one runs. `run` holds the lock for its entire lifetime, so only one controller can use a
state directory at a time. When a pass fails, `run` writes the error to stderr and doubles the wait
before the next pass, from `--interval` up to 5 minutes, until a pass succeeds. The directory
belongs to the repository, target and principal of the first pass that used it, recorded in its
`binding.json`. A pass for any other is refused, and needs its own `--state`. A pass runs for at
most `--timeout`, 10 minutes by default. When the timeout passes, or the pass is interrupted or
stopped, every git, cue and attest it started is killed, with whatever each of them started, ssh
included. Writing the plan is bounded the same way. A pass whose output nobody reads ends at its
timeout. Under `once`, the program then lets go of the lock, so the next pass can take it. Under
`run`, the program keeps the lock and starts its next pass after the wait. A message of the
program's own, such as the error it ends with, is given up on after 5 seconds, so a stderr nobody
reads cannot hold the process either. Checked by `restack-unit` and `restack-e2e`.

To read a snapshot of origin, a pass lists origin's refs and then fetches the objects they name. If
a ref moves between the listing and the fetch and origin has pruned the old object (for example,
when `main` is rewritten), the fetch fails. `ReadSnapshot` then lists origin and fetches again, up
to three attempts in total, before returning the error (`rebase/snapshot.go`). Checked by
`rebase-unit`.

A family is the connected layers of one snapshot, as the rebase engine captures them
(`rebase.Capture`). Two stacks on a shared lower layer are one family. The planner gives each family
one of five answers:

- **leave**: no layer is a ready change: drafts, withdrawn changes and uncertain landings alone. A
  plain branch is never planned on its own, and a submitted change is no layer.
- **current**: the family already descends from `main`'s tip.
- **hold**: something stops it, and each reason is listed. A layer that is not a ready change holds
  its family, as does any refusal of the engine's `Family.Check`.
- **restack**: nothing holds it.
- **wait**: the family shares a change or a branch with a family still held in the journal. A family
  that this pass would otherwise mark `restack` or `hold` also waits when it shares a change or a
  branch with another such family. The pass builds no writes for a waiting family until that overlap
  clears (see [Publishing](#publishing)). For example, two ready changes that name the same branch
  are captured as two separate families when neither head descends from the other and no shared
  lower layer joins them. Because the journal never holds two families that share a branch, both
  families wait, each naming the other in its reason, while unrelated families are planned as usual.

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
- Its refusal ends with a note stating what the report omitted and naming
  `observations/<digest>.json` in the state directory, where `<digest>` is the digest of the full
  observation. That digest distinguishes two fallback reports whose truncated fields look the same.
- If the report is still over 64 KiB, it sheds its conflict paths, then its blocking requests, then
  every layer but the two it keeps.
- If the change's own layer alone is still over 64 KiB, no report of that change is built, and the
  pass's writes say why.

A family with more than 64 recorded changes still proceeds: each change's report keeps that change's
own layer. A replacement report's result describes the whole family, whichever layers it keeps. It
says `complete` when every version of the family was added, and `partial` when any was not. So a
replacement that keeps only its own layer can say `partial` because of another layer's version, and
the journal accepts it (see [The journal](#the-journal)). Checked by `restack-unit` and
`restack-e2e`.

Before saving a journal that contains a truncated report, the restacker writes the full observation
to `observations/<digest>.json` in the state directory. The file stores the exact JSON bytes that
were hashed to form `<digest>`, so its SHA-256 prefix matches `<digest>`. The restacker never
deletes observation files, so omitted details (such as conflict paths beyond the 100-path report
limit) remain on disk after the journal closes. Checked by `restack-unit`. Because these files
accumulate without bound, an operator reclaims space by removing old files manually (see
[Helper and host commands](reference.md#helper-and-host-commands)).

The restacker skips rebasing a family when every recorded change in it already has a current report
with result `conflict`. A conflict report remains current only while `main`, and each change record,
branch, and blocking request the report lists, stay at the commits the report observed. Currency
checks only the refs listed in each report. When any of those refs moves, the report becomes
historical, and a later pass tries the rebase again. A new recorded layer that joins the family does
not make the existing reports historical. That layer has no current conflict report of its own, so
the family does not qualify for the skip, and the pass tries the rebase. Any omitted conflict paths
remain in the observation file named by the report. Checked by `restack-unit`.

### The journal

The journal records every pending write in `journal.json` in the state directory before the
restacker pushes anything (`restack/journal.go`). Its format is `valley-restack-journal/v1`, which
is internal to the restacker. The journal header names the repository, target, and principal. An
open family stays in the journal across passes while later passes append newly built families, so
one journal can hold families from multiple operations. For each family, the journal stores:

- the operation ID that built it, along with `main`'s tip and the evidence fingerprint captured by
  that operation;
- the full observation: every layer (including plain branches), related integration requests, all
  conflict paths, and the full refusal text;
- the pending branch push, version updates, and initial reports in the serialized forms defined by
  the rebase engine (`rebase/pending.go`);
- any replacement reports built later to record what actually happened after the branches moved (see
  [Publishing](#publishing));
- the writes confirmed on origin (`accepted`): whether a read saw the branch push on origin, and the
  change IDs whose new versions a read saw on origin (see [Publishing](#publishing)).

Each conflict path is base64-encoded so file names with non-UTF-8 bytes survive JSON serialization
byte for byte. Every object those writes would push is kept reachable in the scratch repository
under `refs/valley-restack/<operation>/<object>` for as long as the journal names it: each prepared
head, each record commit, and each report and replacement commit. An object that a report's JSON
only names is not kept by these refs. The scratch repository fsyncs every object and ref it writes,
and the restacker writes `journal.json` via an fsynced temporary file renamed into place.

A journal is read as strictly as it is written. A field missing, unknown or named twice, a form that
does not agree with itself, and a journal for another repository, target or principal are refused,
and nothing in a refused journal is decided. A refused journal is an error that recovery reports,
never a crash. A family's writes must agree with each other:

- A family that would restack adds exactly one version of each of its changes.
- Each report gives the family's result: complete, conflict or blocked.
- A report says a version was published exactly when the family adds that version, and then names
  that version's head, number and record commit.
- A replacement report is stored only after origin is recorded as having accepted the family's
  branch push. Every layer in a replacement reports that change's prepared head as published, and
  includes version and record fields only when they match the version the family adds.
- A replacement report that says `complete` gives every layer it lists a published version. One that
  says `partial` lists a layer with no version, or leaves out a change of the family. A fallback
  report that leaves out layers may say either, because its result describes the whole family: the
  version that was not added may belong to a layer it leaves out.
- A version can be recorded as accepted by origin only when the family's branch push is also
  recorded as accepted, and only for a change whose version the family adds, in bottom-up layer
  order without duplicates.
- No two families in the journal may share a change or a branch.

The restacker does not keep a journal that would not read back as written. A lost journal is never
inferred. A change whose branch a pass moved with no journal to say so is held by the planner,
because its branch is not at its recorded head. Checked by `restack-unit`.

### Recovery

Recovery reads origin and the journal, and decides each write in the order a pass makes them: the
branch push, then the versions, lower layers first, then the reports and replacements
(`restack/recover.go`). Each write is `done` when origin holds it or has moved on from it, `finish`
when it is still valid to push, or `stop` when it must not be pushed. A stop is final when no later
pass could push it, and terminal when it is also a local failure the journal keeps the diagnostics
of. Any write recorded in the journal as accepted by origin is treated as `done` on every subsequent
read, even if the branch or record on origin moves afterward (see [Publishing](#publishing)).

| Write           | `finish` while                                                                                                                                            | `stop` (final) on                                                                                                                                             |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The branch push | `main` is at the tip the family was rebased onto, every branch is at its lease, every record is at the record commit read, and nothing has been submitted | a branch moved, `main` moved, a record moved (an unready, a withdrawal, a new version), a submission, or a prepared head missing from the scratch repository  |
| A version       | its branch is at the prepared head, or the branch push is still to finish, and its record is the record commit read                                       | the author moving the branch or the record, a withdrawal, an unready, a submission, a deleted branch, or a `main` that does not descend from the captured tip |
| A report        | origin's report is the one it was built on, and what it says was published is on origin or is still to finish                                             | a version it says was added stopped, or origin's report moved                                                                                                 |
| A replacement   | origin's report is still the commit it was built on, and its content still matches the current version decisions                                          | origin's report moved, or the replacement does not match the current version decisions (a new replacement is built in its place)                              |

A related integration request that might still land is a stop that is not final: the branch push
waits, and the versions pause. Requests are related as the planner relates them, by every version
head the changes have had, the prepared heads included. A deleted change record is a terminal stop
for its version and its report, since the hook takes no report about a change the repository does
not record. A family whose writes stop does not stop another's. A `main` that has moved on, and
still descends from the captured tip, does not stop a version whose branch was pushed.

In `--report-only` mode recovery decides and pushes nothing, and leaves the journal as it is. The
decisions are printed, and under `recovery` with `--json`. A write pass pushes what recovery decides
to finish (see [Publishing](#publishing)). Checked by `restack-unit`.

### Publishing

A write pass executes its journal using the same decision rules as crash recovery
(`restack/publish.go`). In each step, it reads a fresh snapshot of origin, evaluates every write in
the journal as described in [Recovery](#recovery), and pushes the next phase of writes marked
`finish`. Each family advances one phase per step: first the atomic branch push, then the version
updates from bottom layer to top, and finally the reports and replacement reports. The pass reads a
full snapshot of origin again before every phase. So an integration request filed after a family's
branch push pauses its version updates when the snapshot read for that phase shows the request. A
request filed after that snapshot was read does not pause that phase. Each version push also reads
its branch on origin once more, and pushes nothing unless the branch is still at the prepared head
(`rebase/record.go`). If a push succeeds on origin but the reply to the client is lost, the next
read sees the new ref on origin and marks the write `done`, so a version is never appended twice.
One `settle` call runs at most eight read-and-push steps; any write still marked `finish` after the
eighth step stays in the journal for the next pass.

Once origin accepts a family's branch push, the pass works to give each recorded change in that
family a report on origin that describes what happened. The initial reports built during planning
say `complete`, which remains accurate when every change's new version is added. If any version is
stopped or paused, the pass builds a replacement report for each change in the family. A
replacement's result describes the whole family's versions: `partial` while any version of the
family is not added, and `complete` once every one is. A replacement records the versions that were
added on origin, and states in its refusal why each remaining version was not added. The restacker
builds replacements from the journal's observation and the latest snapshot of origin, and saves them
to the journal before pushing them. Recovery evaluates replacements like any other write, so a
replacement whose push fails or loses its reply stays in the journal for the next step. The
restacker skips building a replacement when origin's current report for that change already has the
same content.

Two kinds of change get no replacement:

- a change whose record was deleted from origin, since the hook takes no report about a change it
  does not record. Recovery stops that change's pending report for good, and its decision quotes the
  report it did not push (`restack/recover.go`);
- a change no report of which fits the report format, even with its own layer alone (see
  [What a pass builds](#what-a-pass-builds)).

If a family's branch push stops before moving any branch, the journal pushes no report for it. The
restacker's planning reports on that family instead: in the same pass, when the family settles
before the pass plans, or in the next pass. The restacker never writes the `superseded` result.

Before pushing or building replacements in a step, the pass records in `journal.json` any branch
push or version update that the latest read saw on origin (`accepted`). From then on, recovery
treats those writes as `done` regardless of what happens to the branches or change records on origin
later. Even if an author rewrites or deletes every branch the family moved, the journal remembers
that the branches moved and which versions were added, and continues pushing the family's reports or
replacements until origin accepts them.

Families in the journal advance independently. At each step, every family with a write marked
`finish` pushes its next phase, while any other family whose branches moved builds replacements if
needed. A write that origin repeatedly refuses therefore delays only its own family. The pass
retires finished families from the journal as soon as a read shows no writes to push and no
replacements to build across any family, or on the eighth and final step of `settle` for the
families that have nothing left to push or build then. A family is retired only when it was idle
during that step and is closed. A family remains open in two cases:

- at least one of its writes is still marked `finish`;
- origin accepted its branch push, and at least one of its version updates is neither `done` nor
  finally stopped (`stop (final)`). For example, a version paused by a pending integration request
  keeps its family open, and its replacement reports say `partial` until that request resolves.

A pending report or replacement is marked `finish` until a read sees it on origin, so a family with
a report still to push stays open. A family can still be retired without a report of each of its
changes on origin. That happens when a change's record was deleted from origin, and when no report
of a change fits; in both cases no write is left to finish. When all families have been retired, the
pass removes `journal.json` and deletes all `refs/valley-restack/*` retention refs from the scratch
repository.

Any family that remains open stays in `journal.json` for the next pass. When a write pass plans new
work while a journal is still open, any family in the new snapshot that shares a change or a branch
with a journaled family is marked `wait`, and the pass builds no writes for it so its refs cannot
race the open family. Unrelated families are planned, built, and appended to the same journal. A
family paused by an integration request therefore holds back only its own changes and branches. A
family whose branch push stopped without moving any branch is retired from the journal and planned
again: by the same pass when the family settles before the pass plans, or by the next pass. If
`journal.json` cannot be read or validated, or belongs to a different repository, target, or
principal, every write pass refuses to proceed until the operator fixes or moves the file,
preventing a new journal from overwriting an unrecovered one.

Checked by `restack-unit`, through the rendered hook:

- after a landing, a two-owner stack is restacked once, each layer onto the new layer below it,
  while a conflicting change beside it is reported as `conflict` and skipped on the next pass;
- an integration request holds its family until the request has a current `stale` outcome;
- a journal interrupted after any step finishes on the next pass with one version per change;
- an owner's push to a restacked branch stops that change's version update and produces a `partial`
  replacement report;
- a replacement report refused by origin stays in the journal until a later pass pushes it, and one
  whose push reply is lost is seen on origin by the next read without being pushed twice;
- a replacement report refused by origin is still pushed after the author rewrites or deletes every
  branch the family moved;
- a report that origin keeps refusing holds only its own family in the journal, while a family
  restacked beside it is retired and restacked again after the next landing;
- two ready changes that name the same branch both wait while an unrelated change beside them is
  restacked;
- an integration request filed between the branch push and version updates pauses the versions and
  keeps the family open until released, while an unrelated change readied beside it is restacked;
- the project's `maxAutomaticVersions` cap holds a change that has reached the limit;
- `run` converges on a change readied after the landing and exits cleanly when stopped.

Checked by `restack-e2e` as well: after the real integrator lands a change, write passes over an SSH
stand-in restack two owners' changes once each, including when the reply to the first record push is
dropped.

### The restacker on the host

`services.valley.restacker` runs one systemd service, `valley-restacker@<project>.service`, for each
project in `projects` (`nix/valley-host.nix`). Each service runs `valley-restacker run` as the
system user `valley-restacker` in its own group `valley-restacker`. That user belongs to no other
group and cannot read or write the bare repositories under `services.valley.dataDir` (`/srv/git` by
default) or the integrator's and worker's keys. Each project gets its own `0700` state directory at
`/var/lib/valley-restacker/<project>`, which is the only persistent path the sandboxed unit can
write (`ProtectSystem=strict`). The unit's private temporary directories (`PrivateTmp`) are writable
too, and do not outlive the service.

The controller connects to the host's repositories over `localhost` SSH as its own principal. It
authenticates with the private key at `sshKeyFile`, which systemd reads as root and passes to the
service via `LoadCredential`, so the key file on disk can be readable by root alone. Before each
start, `ExecStartPre` writes the host's own SSH public keys into `<state>/known_hosts`, and the
controller's SSH client accepts only those host keys. The controller reads the instance floor from
`integrator.instanceProject` over the same local SSH connection, or from the local directory
`integrator.instancePolicy`. It verifies transfer statements using the same `known-signers` file,
`signer-projects` file, and `--integrator-name` as the integrator controllers.

Enabling `services.valley.restacker` configures the local systemd units and creates the
`valley-restacker` Unix user and group. Enrolling the restacker's public key under its principal
(`restacker.principal`, default `valley-restacker`), granting that principal `restack`, and
provisioning the private key at `sshKeyFile` are each a **deployment choice**. Until the key is
enrolled and granted `restack`, `valleyhook` refuses the controller's version updates and reports.

Checked by `restack-host-e2e` in a NixOS VM with the principal enrolled and the key provisioned:

- after `main` advances, the service restacks a ready change as `valley-restacker`;
- the `valley-restacker` Unix user is denied access to `/srv/git`, the integrator's key and state,
  and the root-owned key file under `/run/valley-keys`;
- over SSH, the restacker's key can push a topic branch like any named principal, and the hook
  refuses its pushes to `main`, integration requests, integration outcomes, and change record
  deletions.

## The check worker

The check worker builds the checks a ready change requires before anyone approves it. It publishes
the evidence on `origin`, where approval reuses it instead of building it (see
[The valley command](#the-valley-command)). `valley-worker` runs one pass of it. It is
`valley-go work` (`status/worker.go`), packaged with this flake's git, cue and `attest` and the
verification schema (`nix/packages.nix`). It keeps no queue, lease or schedule, and nothing connects
to it. A host that turns the worker on runs a pass on a timer (see
[The worker on the host](#the-worker-on-the-host)).

A pass builds evidence only for the projects named with `--repo`. It reads each project as
`valley pending --repo` does, so it can run from a clone of any project the host serves. To find a
project other than its clone's own, it also reads `policy/valley` on `main` of `origin` to find the
instance repository, then reads `refs/the-valley/projects` there. When that `main` holds
`policy/instance` and no `policy/valley`, `origin` is itself the instance repository. To load a
project's policy floor, it can fetch `main` of the owning valley that the project's `policy/valley`
names. For each project it follows approval's steps:

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
others (**deployment choice**). The controllers count the worker's evidence nowhere else. The host
module refuses a host that runs the worker without that restriction.

The evidence is about the head's tree, so it stays true if the change moves on while the batch runs.
The pass does not read the change's record again before it publishes. A superseded head's evidence
is published, and the next pass builds the new version.

The attestation namespace admits creations only, so the worker never builds what it could not
publish. When `origin` already holds the worker's own ref for the tree and that ref lacks a required
check, the pass says so and builds nothing. Approval builds the rest under the operator's key
([bd-7b7d58f](../archive/.the-valley/bugs/bd-7b7d58f-same-tree-re-attestation-cannot-be-published.md)).

A push whose reply is lost is not built again. If the push landed, the next pass finds the evidence
on `origin`. If it did not land, the ref is still stored in the worker's clone, and a later pass
pushes that same object again.

After a failure, the same thing waits before it is tried again. A batch that fails waits 15 minutes
at first, and a push that fails waits one minute. Each failure in a row doubles the wait, up to 24
hours. A wait is measured from when the attempt ended, so a long build does not use up its own wait.
The waits are kept in `failures.json` in the worker's state directory, project by project. A batch's
key is its tree and the names of the checks it runs, the ones `origin` holds no evidence for. A new
key escapes the previous key's wait. A batch with the same key keeps the earlier wait, whichever
change it belongs to. A push is known by the object it publishes, so changes whose heads hold the
same tree wait together.

A pass forgets the waits it did not meet in a project, but only for a project it read whole: every
ready change's checks derived and its evidence read. A pass over other projects, or one that could
not read this project, leaves its waits as they are.

Passes that share a state directory take turns. Each holds an `flock` on the `lock` file there for
the whole pass. The pass also hands that open file to `attest`, so the lock stays held while a batch
runs, even when the pass that started the batch is killed. A second pass waits for all of it. Then
it reads `origin` after the first has published, or finds the ref the batch stored and publishes it.

A read of `origin` that fails stops the pass, because a read that did not happen must not read as no
changes or no evidence. Anything else that goes wrong with one change is reported, and the pass goes
on to the next. The pass exits 1 when anything went wrong, with one exception. When `failures.json`
cannot be read or does not parse, the pass prints a warning on `stderr`, starts the record empty,
and goes on, and that alone does not make it exit 1.

Checked by `valley-request`, which runs the packaged worker through the protected host's hook and
lands its evidence with the real integrator.

### The worker on the host

`services.valley.worker.enable` runs `valley-worker` on a timer so ready changes have their Nix
checks built and signed before an operator approves them (`nix/valley-host.nix`). It is off by
default, and turning it on is a **deployment choice**. When enabled, the module creates
(`implemented`):

- the `valley-worker` system user and group, with a `0700` state directory at `worker.stateDir`
  (`/var/lib/valley-worker` by default);
- `valley-worker.service`, a oneshot unit that runs one pass of `valley-worker` over
  `worker.projects` as `valley-worker`, inside `valley-builds.slice`;
- `valley-worker.timer`, which starts `valley-worker.service` 5 minutes after activation and waits
  `worker.interval` (`1m` by default, plus up to 1 minute of systemd timer slack) after each pass
  finishes before starting the next.

Two passes that use the same state directory never overlap. systemd only starts the oneshot unit
while it is inactive, and the worker's own `flock` on `<stateDir>/lock` serializes any pass run by
hand with `--state <stateDir>`. A pass run with a different state directory takes a different lock.

In the default configuration, the `valley-worker` user cannot read or write the repository directory
(`services.valley.dataDir`, `/srv/git` by default) on disk. The module makes that directory `0750`,
owned by `services.valley.user` and the repository group (`services.valley.group`, `git` by
default). It gives the worker its own primary group, `valley-worker` (`implemented`). This keeps the
worker out only while it is not a member of the repository group, as its primary group or as a
supplementary one. The module does not stop a host's configuration from adding that membership
(**deployment choice**). Instead of reading the directory, the worker connects to the host's own
`sshd` as the repository account (`services.valley.user`, `git` by default) over `localhost`, using
its own SSH key. Every push from the worker goes through `valleyhook shell` and
`valleyhook pre-receive` like any external client's push. The service runs in a git repository
without a checkout at `<stateDir>/origin` whose `origin` remote points at the local instance
repository (`<user>@localhost:<instanceProject>.git`, where `<user>` is `services.valley.user`).
From there, `valley-worker` discovers each project in `worker.projects` through
`refs/the-valley/projects`, just as `valley pending --repo` does.

The service pins the worker's SSH connections to the local host. It writes the host's own SSH host
keys to `<stateDir>/known_hosts` under `localhost` and passes `HostKeyAlias=localhost` and
`StrictHostKeyChecking=yes` in `GIT_SSH_COMMAND`. If a project's `policy/valley` names this host by
its public hostname, such as `git@valley-host:valley.git`, SSH still verifies it against the local
host key. A server presenting an unrecognized host key fails verification. This host-key pin
restricts SSH connections only; it does not cut the worker off from the network. When the worker
fetches an instance floor named in `policy/valley`, `valley-go` sets
`GIT_ALLOW_PROTOCOL=ssh:git:https:file` in its own child environment (`status/list.go`), so the host
module cannot narrow that list, and `git://` and `https://` URLs still reach other hosts without
using SSH. Similarly, when Nix builds a check, it can fetch flake inputs over the network from the
URLs in `flake.lock`.

The worker uses one Ed25519 key to sign check attestations under `worker.signerName` and to
authenticate its SSH connections (`implemented`). On its first start, `valley-worker.service`
generates `<stateDir>/key` (mode `0400`, owned by `valley-worker` inside the `0700` state
directory). It writes the key to `<stateDir>/key.new` first and renames `key.new.pub` and `key.new`
into place, so an interrupted start never leaves a half-written key. Alongside `<stateDir>/key`, the
service writes `<stateDir>/key.pub` (the OpenSSH public key) and `<stateDir>/verifier` (the
`known-signers` line for `worker.signerName`). Whenever `<stateDir>/verifier` is created or changes,
the service prints both public lines to the systemd journal. In normal operation, the service never
prints or copies the private key, and it does not place the key in a git repository or the Nix
store. Other unprivileged users on the host (`git`, `valley-integrator`, `nobody`) cannot read
`<stateDir>`. However, any process running as `valley-worker` — including the `git` and `nix`
commands a pass invokes — can read `<stateDir>/key`, and can replace `key` or `key.pub` in
`<stateDir>` (which the journal prints as it finds on disk). Checked by `worker-e2e`.

A newly generated key has no access until the operator enrolls it (**deployment choice**). Until
then, `sshd` rejects the worker's connection and each pass stops at its first read of `origin`
(`implemented`).

1. Enable the build slice and the worker on the host, restrict `worker.signerName` in
   `integrator.signerProjects` to the exact list in `worker.projects`, and deploy the host
   configuration:

   ```nix
   services.valley.buildSlice.enable = true;
   services.valley.worker = {
     enable = true;
     projects = [ "the-valley" ];
     signerName = "worker/attestations";
   };
   services.valley.integrator.signerProjects."worker/attestations" = [ "the-valley" ];
   ```

2. After `valley-worker.service` has run once, read the worker's public key and verifier line:

   ```sh
   journalctl -u valley-worker -g "the worker's"
   sudo cat /var/lib/valley-worker/key.pub /var/lib/valley-worker/verifier
   ```

3. Enroll the key in the identity registry as its own `machine` principal with `signs` set to
   `worker.signerName` and a `git-push` grant, then land that change. On the next compilation,
   `valley-identity` adds the key to `known-signers` and to the repository account's
   `authorized_keys`:

   ```cue
   "worker": {
   	kind: "machine"
   	keys: [{
   		class:  "ssh-ed25519"
   		bound:  "host"
   		public: "ssh-ed25519 AAAA..."
   		signs:  "worker/attestations"
   	}]
   	grants: push: boundary: "valley-host-push"
   	expires: "2027-06-01"
   }
   ```

   On a host that does not use `services.valley.identity`, add `key.pub` to
   `services.valley.authorizedKeys` as an untagged string and add the `verifier` line to the file at
   `integrator.knownSignersFile`.

4. If other hosts share the same identity registry, add `worker.signerName` to
   `services.valley.integrator.signerProjects` on those hosts as well. Without an entry in
   `signerProjects`, a controller accepts a known signer's evidence in every project it serves.

At NixOS evaluation time, `services.valley.worker` refuses to build unless all of the following hold
(`implemented`; checked by `module-eval`):

- `services.valley.buildSlice.enable` is `true`.
- `services.valley.integrator.enable` is `true` and `services.valley.integrator.instanceProject` is
  set.
- `worker.projects` is non-empty and names only repositories this host serves.
- `worker.signerName` differs from the integrator's signing name.
- `integrator.signerProjects.<signerName>` lists the exact projects in `worker.projects`.

Once enrolled in the identity registry with a `git-push` grant (**deployment choice**), the worker's
key authenticates as a named principal (`VALLEY_PRINCIPAL=worker`) on every repository the host
serves. `valleyhook pre-receive` allows a named principal without `approve` or `restack` grants to
push (`implemented`):

- attestation refs (`refs/the-valley/attestations/<tree>/<key-hash>`), which are all the worker
  writes in normal operation;
- topic branches (`refs/heads/*`), which it can create, update, or delete;
- change records (`refs/the-valley/changes/main/<change-id>`) that name it as the owner;
- new commits on the plan graph (`refs/the-valley/knots`).

A host that leaves the worker out of `protection.writers` keeps it from writing protected refs such
as `main`, and the hook enforces that deployment choice (`implemented`). The hook also stops the
worker from filing integration requests or updating another principal's change records.
`integrator.signerProjects` restricts which projects count the worker's signed notes; it does not
restrict which repositories the key can push to. An untagged key in `services.valley.authorizedKeys`
has no principal name, because its session leaves `VALLEY_PRINCIPAL` unset. So the hook allows it to
push only attestation refs and topic branches. A registry key can push to topic branches, its own
change records, and the plan graph, which a worker does not need. A permission that limits a key to
attestation pushes is **not built** (knot oc-6da5273). It must exist before a worker runs on more
than one host.

The worker's builds are capped at one check build at a time, 4 CPUs, and 8 GiB of memory
(**deployment choice**). `valley-builds.slice` enforces the 4-CPU and 8-GiB limits across the Nix
daemon and the worker (see [The build slice](#the-build-slice)), and `valley-worker.service`
configures how the worker invokes Nix (`implemented`):

- `valley-worker.service` runs inside `valley-builds.slice`, so the worker's own flake evaluation
  shares the slice's CPU and memory caps with the daemon's builds.
- `attest run` starts one `nix build` process per CPU available to it (`runtime.NumCPU()`).
  `valley-worker.service` pins the worker to a single CPU (`CPUAffinity=0`), so `attest` runs one
  check build at a time.
- `NIX_CONFIG` sets `max-jobs = 1` and `cores = 4`. The Nix daemon applies both settings per client
  connection, including connections from untrusted users. Each `nix build` therefore builds one
  derivation at a time and passes `NIX_BUILD_CORES=4` to the builder. `NIX_BUILD_CORES` is only a
  hint; the slice's `CPUQuota` is what enforces the CPU cap.
- `NIX_CONFIG` sets `builders =` and `accept-flake-config = false`, so the worker never delegates
  builds to remote machines and ignores `nixConfig` blocks in candidate flakes.

Individual builds have no timeout. A check build that hangs holds the worker lock and blocks further
passes until an operator stops `valley-worker.service`.

Checked by `worker-e2e` and `module-eval`. On a six-CPU VM, `worker-e2e` runs the worker against a
ready change with two required Nix checks and verifies that they build one at a time inside
`valley-builds.slice` with `NIX_BUILD_CORES=4`. `module-eval` checks the rendered systemd units and
`NIX_CONFIG`.

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

A second deriver is written in Go (`derive/`). The check worker runs it, while `valley checks`,
approval and the integrator use the one in `bin/valley`. It reads the whole composed policy through
`composed.Read` (`composed/model.go`): every class's paths, requirements and carry mode, the
`unclassified` set, and each check's runner, cost and validity. It asks cue whether each requirement
is mandatory the way `valley checks` asks: it vets the layers with a document setting that
requirement to `false`. It reads a diff as `valley checks` does, with renames counted on both sides,
and keeps every path whole, whatever bytes it holds. It matches globs byte by byte, as
`valley checks` does. It reads a layer's documents as `valley checks` does. Checked by
`derive-unit`.

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
the change as a new version. The cap is how many versions of one change may have been uploaded by
the principal the restacker is configured to push as. The planner counts every version of the change
with that uploader, and holds a change whose next version would go over the cap. A host may impose a
lower ceiling of its own, outside the policy. The carry modes and the cap are part of the composed
policy, so the integrator's policy digest, a SHA-256 of the whole composition, changes when either
does. Approval carry reads the carry modes, and is not built yet, so nothing acts on them. The
restacker's planner reads the cap (`composed/composed.go`, `restack/plan.go`). Checked by `cue-vet`,
`integrator-unit`, `restack-unit` and `restack-e2e`.

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

The flake gives each check only the files it reads, and gives each package only the files its build
reads (`nix/checks.nix`, `nix/packages.nix`). An edit to any other file leaves a check's derivation
and inputs digest unchanged, so its evidence transfers. Building a package does not run tests. Each
Go module's unit tests run in its `*-unit` check, which builds the package again with its test files
and the files they read. Editing a test therefore changes only that `*-unit` check. Four checks test
what the NixOS host module renders without running the integrator, the identity compiler, or
valleyhook: `module-eval`, `mirror-e2e`, `bus-e2e`, and `init-e2e`. Those checks render the module
with stand-in scripts for the integrator, the identity compiler, and `valleyhook`
(`nix/checks/hosts.nix`), so editing one of those programs leaves the four checks unchanged.
`nix/tools/drv-churn.sh` measures which checks a representative edit changes.

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

The identity registry is CUE under `identity/` in the instance repository, read from a single commit
of its `main` (`identity/registry.go`, `schema/identity.cue`). It declares principals and
boundaries.

- A principal has a kind (`human`, `machine` or `service`), one or more `ssh-ed25519` keys, and
  grants. A key may also sign attestations, under a name it declares, and may carry a YubiKey PIV
  attestation that the compiler verifies (`identity/attestation.go`).
- A grant names a boundary. A boundary has a kind: `git-push`, `registry`, `approve`, `restack` or
  `edge-read`. The kind `approve` is also accepted as `request`. A registry using either name
  compiles the same files. A grant at an `edge-read` boundary is a read grant and names the projects
  it covers. The identity compiler ignores read grants when writing its output files; the web
  publisher and `valley-read-coverage` read them directly from the registry (see
  [Read access](#read-access)).
- `anyone`, the reader who presents no credential, has its own top-level entry beside `principals`.
  It holds only read grants and has no keys.
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

When asked, the compiler also writes the approval keys and the touch-always hardware approval keys
of human principals holding an `approve` grant (see
[Approval signers files](reference.md#approval-signers-files)). The host module does not write them,
and no gate reads them.

The compiler refuses a registry where no principal holds a registry grant, where a key's hardware
attestation does not verify, where one key pushes for two principals, where a pushing key matches a
host-declared key under a different tag or none, or where a key that approves is also held by
another unexpired principal. Keys that neither push nor approve are not checked for collisions.

The compiler reads every key of the registry's export spelled exactly as the schema spells its field
(`exactjson/`). A registry may reopen a schema definition and add a field to it, such as `Grants`
beside `grants`. Go's JSON decoder alone would read that field as `grants`. So the compiler refuses
a registry whose export holds a key that is not a field of the schema, spelled exactly. It also
refuses an export that holds a key twice in one object.

The compiler reads the registry at `--ref` by that exact name, and reads the commit the ref held
from then on. An instance repository with no such ref fails the compilation, whatever other ref git
would take the name for (see
[Refs are read by their exact names](security.md#refs-are-read-by-their-exact-names)).

The compiler computes everything before it writes anything, so a refused registry leaves the last
good files in place. Checked by `identity-e2e`, `identity-schema` and `exactjson-unit`, and by the
Go tests in `identity/identity_test.go`. Why identity is a governed registry, and why machine keys
expire, is in
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
Nothing in the-valley calls `sigverify` yet.

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
  `nxvp v2 is a draft: its owner (alice) hasn't marked it ready, so it can't be approved yet.`
- A withdrawn change: `nxvp is withdrawn: there is nothing to approve.`

The prompt then offers only `[v]iew full diff` and `[s]kip`. A change is often a draft because an
agent is working on it: an agent runner unreadies a change while its run works, and readies the new
head when the run finishes. `[a]pprove` would be refused. `[b]ase` is not offered either, since
adding the rebased head as a version would ready the change behind its owner's back. `[r]eject` is
not offered, because it records nothing on the change. It only deletes the branch, which the owner
may still be working on. The state can change while the review is open, so `[a]pprove` and `[b]ase`
still read the record again when they act, and refuse a change that is not ready. A branch no record
is on has no state, and is offered the usual prompt.

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

`valley approve` and `valley rebase` are for front ends, such as an agent runner's dashboard, that
show a change their own way. Each runs the same code as its key, so the checks, the request write
and the refusals are the same. `approve` exits non-zero when it refuses. `rebase` does not re-review
the result, and it leaves a stack that already fast-forwards from `main` alone. Checked by
`valley-cli`, `valley-request`, `valley-status`, `valley-ready`, `valley-changes`, `review-notes`
and `integrator-e2e`.

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
  record's version heads, active or not. A front end passes the head it showed, so what is approved
  is what was seen.
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
lower change landed drop out, since the integrator landed them as one new commit and their content
is on `main`. So does a branch below such a change: it is no layer. The rebase reads that landing as
`valley changes` does, and verifies the transfer statement with the same signers (see
[Listing changes](#listing-changes)).

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
on origin. An agent runner passes `--head` with the head it pushed, so the version names exactly
that head. ready reads the existing record, if there is one, and adds a version. For a ready change,
it refuses a head equal to the active version's. It pushes the record ref with a lease on the value
it read, and prints the handle and version, such as `app nxvp v2 ready`. A draft or a withdrawn
change comes back by `ready` too. At the head of its active version, it is ready again at that
version, and the record commit's one parent is the record before it. At another head, it gains the
next version. The record names the principal from `git config valley.principal`. The host derives
the pushing principal from the key, and the client cannot ask it, so the hook refuses a record that
names anyone else. `--if-active <sha>` adds the version only while the change is `ready` at an
active version whose head is `<sha>`.

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
handle, the active version and the new state, such as `app nxvp v2 draft`. Each says when a request
for the change is pending, and `withdraw` warns, as [Change records](#change-records) describes.
`valley pending` lists the changes whose record says they are drafts in a section of their own, at
the head of each active version. A branch whose tip is a draft's head is listed only there.

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

`valley status --json <change>` prints one JSON object on one line. Front ends read it, so it is a
contract with a version. A change to the name or meaning of any field is a new version.

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
`valley-changes/v1` object (`status/list.go`). Front ends read it, so it is a contract with a
version, like the status object. `valley pending` is the view a person reads (`status/pending.go`).

Both read one snapshot of origin for each repository they list. For each version of each change,
they decide its landing evidence (`status/inventory.go`). The first rule that applies is the answer:

1. `landed`: the version's head is on `main`'s first-parent history, and no commit on that history
   between the head and `main`'s tip is a merge. That is, `git rev-list --first-parent` from `main`
   lists the head, and `git rev-list --first-parent --merges <head>..main` lists nothing. The
   integrator never writes a merge, and it never puts a change's own commits on `main`. This rule
   matches a head that reached `main` by a direct write, or through an earlier integrator that
   landed a head commit directly. Ancestry alone is not enough: a merge can drop the content of
   either of its parents, so a head below any merge has not landed by this rule.
2. `landed-recorded`: every one of these holds.
   - A commit `L` on `main`'s first-parent history, with no merge above it, carries a transfer
     statement whose `change` is one of the version's request names. The request names are the
     version's branch with each `/` replaced by `-`, and `c-<change id>`.
   - `L` has one parent. Every commit that the integrator writes has one parent.
   - The statement's `base` is the `valley-tree-v1` digest of the version's tree, and its signature
     verifies as the integrator's.
   - The integrator recorded landing this version's head as `L` under that request name. A statement
     about trees cannot name the exact head that landed, and the integrator's records do. When
     origin holds a landing record for the head, it must name `L`, the request name and the head. A
     landing made before landing records were written has none, and then the outcome record for that
     request name must say `land` with this version's head. Only the integrator writes either
     record: the hook refuses a push to them.
   - The integrator's merge of the version's head onto `L`'s parent gives `L`'s tree. The client
     recomputes it as `integrator/git.go` makes it:
     `git merge-tree --write-tree --merge-base=<merge base> L^1 <head>`. When the change's base is
     `L`'s parent, the merge base is `L`'s parent and `merge-tree` gives the head's tree.

   A head with a landed tree on another parent is another delta. These rules keep it from borrowing
   the landing's evidence.

3. `uncertain`: a request for the version was consumed, or its outcome record says `failed`, or a
   landing record names its head. The integrator records a landing in the transaction that moves
   `main`, so only a landing made before it did, or one cut short inside git's commit phase, exists
   without its evidence. A request is consumed when its outcome record says `land` for this head and
   the request ref does not point at it.
4. `not-landed`: otherwise.

Neither record alone is proof of a landing. A landing record is kept per head, so a later landing
under the same request name leaves the earlier one `landed-recorded`. An outcome record is kept per
request name, and a later pass overwrites it. Take a landing made before landing records were
written, whose head is not on `main`'s first-parent history. Only its outcome record bound it to its
head. When a newer version under the same request name replaces that outcome record, the version
reads `not-landed`, not `uncertain`, and the overwritten record cannot be recovered. Its commits can
then count as work in the listing of a change whose head descends from it. Rebasing that change does
not guarantee their removal: commits replayed by the rebase can still count as work.

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
A clone is made in a temporary directory beside its place and renamed into place once its origin is
set, so a run killed while making it, git included, leaves no clone behind. A clone already in place
is used as it is when its configuration names its origin. One that does not was left half made by an
older version, and is set aside and made again. When discovery is unavailable, `--all` says so and
lists the current repository alone. `--repo <name>` limits either command to one project.

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
  "host": "valley-host",
  "repos": [
    {
      "repo": "app",
      "url": "git@valley-host:app.git",
      "error": null,
      "changes": [
        {
          "change_id": "nxvpwlkmqtsrzyoukklnpmqrstuvwxyz",
          "handle": "nxvp",
          "target": "main",
          "owner": "alice",
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
              "uploader": "alice",
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
  `<repo>:<prefix>`, such as `app:nxvp`. git refuses a colon in a ref name, so a qualified handle is
  never also a branch, and never reads as `origin/<branch>`. Every verb that takes a handle accepts
  the qualified form, in the repository it names, and refuses one that names another repository.
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
- `branches` lists each branch on origin, other than `main`, that `main` does not reach, that is no
  change's active head, and that no landed version's head reaches. A version lands as one new
  commit, so `main` reaches neither its own commits nor a branch below them, though their content is
  on `main`. A branch name that is not UTF-8 is left out, with a warning on stderr.

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
That is the ancestry `includes` reads. Its commits and diff are counted from the nearer of its base
and the head of any landed version its head contains, since that version's commits are not on `main`
and its content is. A stack is an entry no other entry contains, the top, then every entry the top
contains. Those entries are listed under the top at one indentation, however deep the stack is. Each
entry is listed before the entries it contains. Approving the top lands everything listed under it,
and the top says how many entries that is.

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
pppp v1                                                     1 minute ago · alice
  stack-c: build on stack-b · oc-6335749
  1 commit · 1 file +5 −0
  approving it lands the 2 below too
  ├ mmmm v1                                                2 minutes ago · alice
  │   stack-b: build on stack-a
  │   1 commit · 1 file +12 −3
  └ nxvp v1                                                3 minutes ago · alice
      stack-a: the first layer
      2 commits · 4 files +80 −1

vvvv v1                                                       1 hour ago · alice
  lost: a change whose request was consumed
  1 commit · 1 file +1 −0
  uncertain: it may have landed without its evidence being stored

branches
loose                                                      6 seconds ago · alice
  loose: work no record is on
  1 commit · 1 file +1 −0

drafts
qqqq v1 on paused                                             3 days ago · alice
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

Self-update is a Go module (`selfupdate/`) that `valley-go` runs. It installs only a commit it has
built and smoke-tested, and it keeps the previous install to roll back to. Each run:

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
`git@valley-host:the-valley` is `ssh://git@valley-host/~/the-valley` to both git and nix. Nix
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
A knot is never deleted, and its `id`, `type`, `created` and `created_by` never change. A comment
file is never moved or deleted, so its ID — the filename without `.md` — never changes either. A
comment's body can be edited in place. An edit keeps the comment's `author` and path, sets `edited`
to the edit timestamp, and sets `edited_by` to the editing principal. On a push, a new knot's
`created_by`, a new comment's `author`, and an edited comment's `edited_by` must match the pushing
principal. Because every write adds a commit to `refs/the-valley/knots`, the ref's commit history
preserves every earlier text of a knot and its comments. The module and the CUE schema with the
knowledge lint are two validators of one documented schema, and `knot-agree` runs both over shared
fixtures and generated graphs and fails on any disagreement about the rules they share. The
canonical form, the layout, cycles and transitions are the module's alone, and `knot-unit` covers
them.

`valley knot` is the graph's API: `new`, `show`, `log`, `list`, `set`, `link`, `unlink`, `edit`,
`comment`, `claim`, `unclaim` and `schema`, each with `--json`, and each with `--project` for
another project on the same host. A write reads the tip with one `ls-remote`, one fetch and one
batched read of the tree. It builds the new commit with git plumbing on a private index, never
touching the worktree, and holds the result to the rules the hook applies. Then it pushes with
`--force-with-lease=refs/the-valley/knots:<old tip>`. When the lease fails, the operation is formed
again on the new tip, at most 5 tries in all, after a random pause. `set`, `edit`, `link --parent`,
`claim`, `unclaim` and `comment --edit` compare before they write: a field another write changed
meanwhile, a claim another run holds, a claim that changed before `unclaim` lands, or a comment file
another edit changed is reported as a conflict, with exit status 3, and is never overwritten. `--if`
adds a comparison of its own, and a field it names that no knot has is refused before anything is
read. Each operation carries a `Valley-Op` trailer whose ID it keeps across every try. After a push
whose outcome it cannot tell, the CLI looks for that ID among the ref's latest 64 commits before
trying again, so no operation is recorded twice. A push the hook refuses is not tried again, and the
CLI prints what the hook said. Checked by `valley-knot`, against bare repositories with no hook, and
by `knots-e2e` through the hook.

`valley knot log` and `valley knot show --at` read a knot's history from `refs/the-valley/knots`.
`log` lists every commit that added, changed, or deleted the knot's file or a file under
`comments/<id>/`, newest first in topological order down to the root commit. For each commit, `log`
parses the UTC committer time, committer name, and `Valley-Op` operation ID with `knot.ParseCommit`,
the same parser the push hook uses. `show --at <commit>` loads the graph tree at `<commit>` and
displays the knot and its comments at that point in history. `<commit>` must be the fetched tip of
`refs/the-valley/knots` or one of its ancestors. Derived fields still use current state: claim age
is computed against the current clock, and cross-project `<project>:<id>` blockers resolve against
each target project's current tip.

The host decides every push to the ref in `valleyhook` (`valleyhook/knots.go`), with the knot
module. It matches `refs/the-valley/knots` exactly, so a ref that only begins with that name stays
in the closed namespace. Any named principal may write the graph, and an untagged key may not. Every
writer can change every knot in every project's graph. That fits a deployment where the writers are
the operator and the operator's agents. Any principal added later gets the same reach. Comment edits
are the one exception: only the comment's `author` or a principal with the `approve` grant (also
accepted as `request`) may edit an existing comment. The knot module checks that an edit keeps
`author` and sets `edited` and `edited_by`, while `valleyhook` checks the host's grants file to
decide whether the pushing principal may edit another principal's comment. The CLI validates all
other graph rules before pushing, but it does not read host grants, so the hook enforces comment
edit authorization on push. A push that creates the ref brings one commit with no parent. An update
brings one commit whose only parent is the old tip, so a push is one operation and the ref never
rewinds. No push deletes the ref; a graph that must be recovered is recovered on the host, and its
history stays the record. The commit's committer is the pushing principal, and its message ends with
a `Valley-Op` trailer.

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
checked, against the old tree for the transitions, as the pushing principal for `created_by`,
`author` and `edited_by`. Every tip the hook admitted was valid, so it reads only those files and
the knots their edges reach, but it checks the counts from the listing on every push, and a cycle is
looked for along the edges of every knot the push changes. A push that also carries an invalid
change record, attestation or integration request is refused whole, like any push with one refused
update.

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
  ◇ qn-f10cc70  Choose the glyphs · owner operator
  ◇ ida-9c65fa3 Colour the glyphs · owner operator
  ○ oc-c9066f1  Document the schema · owner operator
                blocked by oc-23ebda7 (abandoned)
                lands in app:oc-7654321 (dangling)
  ◐ oc-c23db6f  Draw the tree · claimed by run-7 11h ago · owner alice
    ⊘ oc-70947c5  Golden files · blocked by bd-6e27560
    ✓ bd-b198e52  The tree misses a level
    ○ bd-6e27560  Width is off by one
  ◐ oc-a7dff99  Old work · claimed by run-0 13d ago, stale
  ⊘ oc-f982ae7  Ship it · blocked by app:oc-2172d43,
                ghost:oc-0123456 (dangling), oc-70947c5
  ✗ oc-23ebda7  Use a tree widget

○ oc-24df5c7  A late milestone                                    0/0 ░░░░░░░░░░

no milestone
  ○ bd-bee76f4  A stray report
  ◇ ida-162ff1b The graph as a web page

waiting on you (operator)
  ◇ qn-f10cc70  Choose the glyphs · owner operator
  ○ oc-c9066f1  Document the schema · owner operator
                blocked by oc-23ebda7 (abandoned)
                lands in app:oc-7654321 (dangling)
  ready for approval
    pqrs v1  the-valley  plan: the plan graph as a view

in flight
  ◐ oc-a7dff99  Old work · claimed by run-0 13d ago, stale
  ◐ oc-c23db6f  Draw the tree · claimed by run-7 11h ago · owner alice

open residuals
  ○ bd-bee76f4  A stray report
  under oc-c23db6f  Draw the tree
    ○ bd-6e27560  Width is off by one

○ ready  ◐ in progress  ⊘ blocked  ◇ active  ✓ done  ✗ abandoned
```

`VALLEY_NOW` stands in for the clock of the knot verbs and of `plan`. It holds an RFC 3339 time. A
knot's `created` date, a claim's `claimed_at`, a comment's stamp and an edit's `edited` are written
at that time, and a claim's age is read against it. The checks set it, so the dates and ages they
compare are the same on every run. It lets a writer do nothing new: the hook holds none of those
times to its own clock, so a writer could record any of them by writing the file.

Checked by `valley-plan`, which builds a graph with the real knot verbs and compares the text and
the JSON with golden files in `status/testdata/plan/`, and by the Go tests in `status/` for progress
and ordering.

`valley plan --json` prints `valley-plan/v1`, an indented JSON document. The web export reads it to
build the plan the web server shows, so it is a contract with a version, like the status object. A
change to the name or meaning of any field is a new version.

```json
{
  "schema": "valley-plan/v1",
  "now": "2026-10-15T09:00:00Z",
  "operator": "operator",
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
          "created_by": "operator",
          "parent": "oc-1b5d5fe",
          "owner": "alice",
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

## Read access

`readaccess/` evaluates whether a principal is authorized to read a project at a destination
(`readaccess/`, `schema/read.cue`, `schema/identity.cue`). Authorization requires two consents, one
from the project and one from the instance. Both consents must match (`implemented`).
`valley-web-publish` exports every project that has an authorized reader at its destination, whether
`anyone` or a named principal. Its authorization names each project's readers (see
[What reaches the edge](#what-reaches-the-edge)).

- **The project's consent** lives in `policy/read/*.cue` on the project's `main`. It declares the
  project's name, its 32-character hex publication identity, and the set of principals in `readers`.
  An empty `readers` set authorizes no one.
- **The instance's consent** is a read grant in the identity registry at a boundary of kind
  `edge-read`. The boundary names one `destination`, the web server that receives the export. The
  grant's `projects` field names what it covers: `"all"` for every project, or a map from project
  name to the publication identity declared in that project's `policy/read`.

**`anyone`** represents a reader who presents no credential. The registry stores `anyone` in its own
top-level entry with no keys, no external identity, and no expiry. `anyone` can hold grants only at
`edge-read` boundaries, cannot be the `genesis` entry, and cannot be a principal name under
`principals`. A project is public at a destination when both consents authorize `anyone` there. An
SSH key admitted without a principal tag pushes as no principal (`VALLEY_PRINCIPAL` stays unset),
whereas `anyone` applies only to unauthenticated edge reads.

Each project has a single reader set. A read grant covers a whole project; there are no grants for
individual refs, knots, or files. Instance membership, push access, approval grants, and restack
grants confer no read access.

The schema and evaluator treat every named principal the same, including the operator
(`implemented`). For `readaccess/` and `valley-read-coverage` to count a project as authorized for
the operator, the project's `policy/read` must name the operator and an instance grant must cover
the project. An instance can grant the operator `projects: "all"` while each project names the
operator in its own `policy/read` (**deployment choice**). The starter policy for a new project,
`examples/policy/read/new-project/read.cue`, names only `operator`. `valley-read-coverage` reports
which projects lack either consent for a given principal without modifying anything
(`readaccess/cmd/valley-read-coverage/`).

A grant for a specific project binds both the project's name and its publication identity. If a new
project reuses an old project's name with a different identity, the old grant does not cover it. A
grant with `projects: "all"` covers any identity, and still requires each project's own consent. A
`policy/read` whose `project` field does not match the repository it was read from is invalid and
consents to nothing.

### Reading the two consents

`readaccess.Reader` reads both consents from git commits (`readaccess/read.go`). It reads a
project's `policy/read` from `refs/heads/main`, and it reads the registry from the instance
repository's registry ref (`services.valley.identity.ref`, default `refs/heads/main`). Both
`valley-web-publish` and `valley-read-coverage` resolve the registry ref once with
`readaccess.ResolveRef`, which runs `git show-ref` on that exact full ref name, and then peel the
resulting object ID to a commit and read from that commit. To read a directory, the reader walks the
tree with `git ls-tree` and reads each regular `*.cue` file with `git cat-file`, without checking
out a worktree. If a directory entry's name contains a slash, is `.` or `..`, or appears more than
once, the document is invalid and no files are copied. Otherwise, the reader writes the files into a
temporary directory with no `cue.mod` and runs `cue export --out json` with the schema.

In CUE, a document can redeclare a schema definition, and unification can reopen a closed struct to
extra fields. In `schema/read.cue`, `readers` is closed with `close()`, which a redeclaration cannot
reopen. After `cue export` succeeds, the Go reader checks the exported JSON in two separate ways
(`readaccess/read.go`).

First, it checks the shape. It decodes the JSON with `exactjson`, which refuses a key that is not a
field of the Go struct it is read into, spelled exactly, and a key that appears twice in one object.
The decode also refuses a value of the wrong JSON type. So a registry that reopens `#Anyone` cannot
hold `Grants` beside an empty `grants` and have the reader take it for `grants`. The parts of the
registry that read grants do not use are kept as raw JSON, and are checked only for a key repeated
in one object: `keys`, `external`, `genesisGovernance`, `externalGovernance` and `readScopes`.

Second, it checks again the values it acts on. For `policy/read`, it checks that the project name
matches the repository the policy was read from, the publication identity, and every reader name.
For the registry, it checks every principal name and every `expires` date. Then, for each grant of
an unexpired principal and of `anyone`, it checks that the grant's boundary is declared, that
`anyone` holds grants at `edge-read` boundaries alone, and that a grant at any other boundary names
no `projects`. For a grant at an `edge-read` boundary, it checks the boundary's `destination` and
every project name and identity in the grant's scope.

The Go reader does not check boundary names, grant names, boundary kinds other than `edge-read`,
principal kinds, keys, or what `genesis` must be. Those rest on `cue export` under
`schema/identity.cue`, which refuses a registry that breaks them.

A document that is missing, exceeds its size bounds, or fails schema or Go validation consents to
nothing. If `git` or `cue` fails to execute, crashes, or times out, the reader returns an execution
error so the caller aborts rather than treating the failure as a withdrawn consent. A principal
whose `expires` date has arrived receives no read grants, matching the identity compiler. Only
grants at `edge-read` boundaries for the target destination are collected.

`Grants.May` reports whether a project's `Consent` names a principal and the principal's `Grants`
cover the project and its identity. `Grants.Readers` returns the sorted list of principals for whom
both consents match (`readaccess/access.go`).

### What reaches the edge

`valley-web-publish` exports every project that has an effective reader at `--destination` and a web
URL (`implemented`, `web/cmd/valley-web-publish/valley.go`). An effective reader is a principal that
the project's `policy/read` names and that a read grant at `--destination` covers for the project,
including `anyone` (`Grants.Readers`). The pass reads the registry's grants for `--destination`
first. It then reads `policy/read` only for the projects that the host declaration places in a group
and that some principal's grant selects, either by naming the project or with `projects: "all"`. A
project with no group has no web URL and is not exported. When a grant names such a project, the
pass logs a warning on `stderr` (see [Groups and web URLs](#groups-and-web-urls)). A project with no
effective reader is not exported either.

The policy path signs an authorization that names each exported project with its effective readers
(`implemented`, `web/cmd/valley-web-publish/policy.go`). `anyone` is among them only when the
project's policy names `anyone` and a grant to `anyone` covers the project's identity. The content
pass exports the same projects into one shared export for all readers of the edge. For each exported
project, the export and its git bundle carry the project's reader view: every knot and comment in
its plan graph, its ready changes, and `main` with its commit history (see
[Web export](./reference.md#web-export-the-valleyweb-exportv2)). The export records no reader
access; the separate signed authorization names who may read each project, and the edge serves only
what that authorization allows (see [The authorization stream](#the-authorization-stream)). A
project leaves the edge when it loses its last effective reader. Removing one of several effective
readers drops that reader from the project's authorization and leaves the project on the edge for
the others.

When building `export.json`, the publisher strips derived details about projects that are not
exported (`implemented`, `web/export/project.go`). A reference to a project that is not exported
keeps its identifying reference fields but loses its title, status, and other resolved details,
while a reference to an exported project keeps those details. The export's own projection is a
storage boundary. At request time, `valley-web` strips the derived details of any exported project
that the requesting reader may not see (see [Each reader's view](#each-readers-view)).

`valley-web-publish` can write the authorization and the export to `--out` or send them over HTTP to
a receiver URL. `valley-web-receive` receives the upload into its store, staging it with
`Store.Stage` and activating it with `Store.Activate` (`web/export/store.go`; see
[The receiver](#the-receiver)). `valley-web` serves only `GET` and `HEAD` from the store
(`web/server/server.go`). A missing or invalid registry grants nothing, so the pass authorizes no
project and builds an empty export. A missing or invalid `policy/read` consents to nothing, so that
project is not exported. If the pass cannot read the instance repository or the schemas, it aborts
before signing anything. If it cannot read a selected project, the policy check is incomplete: the
pass sends the authorization and then fails before building or uploading an export.

Checked by `readaccess-unit`, which reads both consents from real commits under the real schemas
(`readaccess/read_test.go`, and `TestCoverage` in `readaccess/cmd/valley-read-coverage/main_test.go`
for `valley-read-coverage`), by `web-unit` (`TestVisibilityNeedsBothConsents`, which checks each
authorization's readers, `TestAProjectMadePrivateLeavesTheReceiver`,
`TestAGrantSpelledAnotherWayIsRefused` and `TestPrivateSentinelsNeverLeave`), by `web-server-unit`
(`TestThePublishersOutputServesEachReader`, over the real publisher's output, which injects each
reader's principal and uses no link or session), by `web-push-e2e`, which exports a project two
named readers may read, keeps one with no effective reader off the edge, and signs a named reader in
with a link to read that project, by `cue-vet` and `identity-schema`, which test both schemas
against rejected examples, and by `identity-e2e`, which compiles the example registry containing
read grants and `anyone` against golden files. Unbuilt pieces are listed under
[Not deployed or not wired yet](#not-deployed-or-not-wired-yet).

### The authorization stream

Read access at the edge travels in a separate signed document, the authorization, independent of the
content export (`implemented`; see
[Web authorization](./reference.md#web-authorization-the-valleyweb-authorizationv1) for the format).
Authorizations and content exports are two streams that share one signing key, one receiver, and one
store. Each stream has its own sequence, its own locks on the publisher and the receiver, and its
own admission rules.

- **Single writer.** Only the publisher's policy path signs authorizations
  (`web/cmd/valley-web-publish/policy.go`). Every pass runs the policy path before any content work,
  and `--policy-only` runs the policy path alone. While holding `<state>/authorization.lock`, the
  policy path reads the instance's read grants and each selected project's `policy/read`, allocates
  the next sequence number, and signs the authorization. Holding one lock across the check,
  allocation, and signing guarantees that an authorization with a larger sequence number always
  comes from a later policy check. The policy path releases the lock before sending the
  authorization to the receiver.
- **Monotonic sequence counter.** The authorization sequence comes from a counter in
  `<state>/authorization-sequence`, which the publisher syncs to disk before using it. Because the
  counter never reads a clock, a backward clock jump cannot give a new authorization an earlier
  sequence number. The receiver admits an authorization by its sequence number alone and ignores its
  age, so a valid withdrawal with a higher sequence number is always accepted. The receiver refuses
  any delayed or replayed authorization whose sequence number is at or below the stored sequence. If
  the receiver already holds a higher sequence number — for example, after the publisher's state is
  restored from an older backup — the policy path re-evaluates the policy and signs a new
  authorization above the receiver's sequence number instead of resending an earlier signature. A
  higher sequence alone does not make a check fresh, so the retry keeps no access that it could not
  check.
- **Kept access needs the receiver's agreement.** When a project's policy cannot be read, the policy
  path starts from the readers and publication identity that the last acknowledged authorization
  recorded for that project. It keeps only the readers that a current grant still covers for that
  identity, and it adds no reader without reading the project's consent. It names the acknowledged
  authorization's sequence as the new authorization's `basis`. The receiver stores an authorization
  with a `basis` only while its stored authorization has that exact sequence, and refuses it as
  `stale-basis` otherwise (`web/export/authorization.go`). After a `replayed` or `stale-basis`
  refusal, the publisher's record is out of date, and the retry keeps nothing it could not check. A
  publisher state restored from an old backup therefore cannot sign a withdrawn grant above the
  receiver's withdrawal.
- **Content uploads confer no access.** A content manifest records each project's publication
  identity, ref tips, and bundle digest, without any reader list. The receiver builds repositories
  in a generation only for projects named in the stored authorization, and `valley-web` serves a
  project only while the stored authorization names that project and its publication identity. A
  delayed content upload, whatever its sequence number, cannot grant access or restore a withdrawn
  project.
- **Independent receiver lock and slots.** The receiver stores an authorization under a dedicated
  lock and request-slot pool without running repository checks, plan-graph reads, or generation
  sweeps (`web/export/authorize.go`). A stalled content upload or a long commit cannot delay a
  withdrawal. When the receiver replies `200`, the authorization is already synced to disk.
- **Checked on every request.** `valley-web` reads the stored authorization at the start of every
  request and checks it again after rendering the response. If a new authorization arrived while the
  response was rendering, `valley-web` discards the response and renders it again. `valley-web`
  holds `serving.lock` shared across that last check and writing the response headers. The receiver
  takes `serving.lock` exclusively to rename a new authorization into place and replies only after
  releasing the lock (`web/export/authorize.go`, `web/server/server.go`). So a response whose
  headers are written after the receiver replies was checked against the new authorization, without
  restarting `valley-web`. Responses whose headers were written before finish normally. A request
  waits for `serving.lock` only until its deadline, or until its reader goes away, so a receiver
  stopped while it holds the lock makes requests answer `503` and writes nothing they rendered. When
  the authorization is missing or invalid, `valley-web` serves no projects.
- **Withdrawal and repository pruning.** A new authorization omits a project once the project has no
  effective reader left. `valley-web` then hides that project starting with the next request. Once
  no reader holds the active generation, the receiver deletes the project's git repository from that
  generation and omits it from future activations. The signed `export.json` file remains unchanged
  on disk, so the withdrawn project's plan text stays in the active generation until a later
  activation replaces and deletes that generation. Repository deletion runs in the background and
  retries on the next authorization, activation, or receiver startup without delaying the
  authorization reply. Under the authorization lock, each prune reads the stored authorization
  again, decides what to delete, and renames those repositories away, so a prune never deletes a
  repository that an acknowledged authorization names again.
- **Policy-check freshness.** Every pass sends an authorization, even when no content changed, and
  records in `checked` the time of the last complete policy check. Web pages display that timestamp
  as "Policy last checked …" alongside the export's collection time, and mark a page stale only when
  the policy check is old. An incomplete policy check reuses the previous authorization's `checked`
  time so the timestamp on the page does not advance. A recent `checked` time shows that the
  publisher can reach the edge, while `collected` shows the age of the exported content.

Checked by `web-unit` (`TestVerifyAuthorizationSteps`, `TestAnAuthorizationWithABasisNeedsItStored`,
`TestStoreAuthorizesWhileTheActivationLockIsHeld`, `TestStorePrunesAWithdrawnRepository`,
`TestAPruneKeepsWhatAnAuthorizationNamesAgain`, `TestTheAuthorizationSequenceIsDurable`,
`TestTheAuthorizationCounterSurvivesFailedWrites`, `TestARestoredStateRestoresNoWithdrawnAccess`,
`TestAReceiverThatLostItsStateGetsTheReadableGrants`, and `TestUploadSkipsAnUnchangedDigest`), by
`web-receiver-unit` (`TestAnAuthorizationIsAdmittedBySequence` and
`TestASaturatedUploadLeavesValleyWebServing`), by `web-server-unit`
(`TestAuthorizationIsReadOnEveryRequest`, `TestAResponseIsHeldToTheAuthorizationWhenWritten`,
`TestAWithdrawalWaitsForAResponseItsCheckAdmitted` and `TestAServingLockWaitEndsWithTheRequest`),
and by `web-push-e2e`. That check uses `--policy-only` to withdraw both a public project and a
project readable only by named principals while a content upload is stalled on the receiver's
activation lock, and verifies that the stalled upload restores neither project's authorization or
repository. It also verifies that a policy check that cannot read a project's policy keeps only the
stored readers that a current grant still covers, and that removing one of two effective readers
leaves the project on the edge for the remaining reader.

### One pass, one instance

A publisher pass reads one instance: the instance repository at `--instance-repo`
(`web/cmd/valley-web-publish/valley.go`). At the start of the pass, the publisher fetches the
instance repository's `main` once into its cached bare repository (`origin.git`) and requires that
commit to carry `policy/instance` and no `policy/valley` (see the pass steps in
[Web export publisher](./reference.md#web-export-publisher)). If that fetched `main` carries
`policy/valley`, the pass fails because the repository points to another instance instead of
defining one.

None of the `valley` commands that run later in the pass read `policy/valley` from the instance
repository's live `main`:

- `valley plan` and `valley pending` run inside the staged pointer repository (`_pointer.git`), a
  bare repository whose `origin` is itself and whose `main` holds a single file, `policy/valley`,
  containing `--instance-repo`. The CLI reads `policy/valley` on its `origin`'s `main` and follows
  no further `policy/valley` in the target repository. Both commands therefore read the project list
  and signers from `--instance-repo` alone.
- `valley knot show --project` runs inside `origin.git`, whose `origin` is `--instance-repo`. It
  resolves the named project as a sibling of its `origin` and reads no `policy/valley`.

Because neither command reads `policy/valley` from the instance repository's live `main`, a commit
that adds `policy/valley` to the instance repository during a pass cannot redirect the pass to
another instance, even when it is removed again before the pass finishes or when two instances serve
projects of the same name.

Separate CLI invocations read the plan and each knot's detail, so a project's plan graph can advance
between commands. `export.Build` returns `export.ErrInconsistent` when a knot's detail was read at a
different graph tip than the plan. When that happens, the pass re-reads the plan and knot details
while reusing the instance commit, project list, and read grants fetched at the start of the pass,
for up to three attempts in total. If all three attempts are inconsistent, the pass fails.

### Read links

A read link is a bearer credential that lets its holder read what one registry principal may read at
the edge. The host issues and revokes links, and carries their verifiers to the edge in the
authorization (`implemented`). When started with `--origin`, `valley-web` redeems each link once
into a session for the link's principal (`implemented`; see [Readers' sessions](#readers-sessions)).

- **The credential file:** `valley-web-publish link`, run as root through the `valley-web-link`
  wrapper, writes one record per link to a file outside every repository, cache and export. A record
  holds the link's ID, principal, note, verifier, issue time, revocation time and the file revision
  that set its state. The verifier is the SHA-256 of the link's 32-byte secret. The secret itself is
  printed once and kept nowhere. Because the edge also holds the verifiers, the credential file
  requires integrity rather than confidentiality: root alone writes it, and the publisher reads it
  through the group `valley-web-links` (`deployment choice`).
- **Bindings in the authorization:** Every time a pass signs an authorization, it holds the
  authorization lock from before it checks the policy until it has signed. After the policy check,
  it takes the links lock as well, reads the file, signs, and then releases the links lock. It signs
  one `binding` record per active link. A revoked link has no binding, and absence is revocation. A
  file that cannot be read sends an authorization with no bindings and fails the pass.
- **One links lock serializes changes and passes:** The links lock is the file `lock` in the
  credential file's directory. `issue`, `revoke` and `prune` hold it while they read and write the
  file, and take no lock in the publisher's state. A pass holds it from its read of the file until
  it has signed. So once a revoke has finished, no pass signs the revoked link. A pass takes the
  authorization lock first and the links lock second, and a change takes the links lock alone, so
  the two never wait for each other in a cycle. A change does not wait for a policy check, only for
  a pass that is reading the file and signing. Each side waits at most 60 seconds. If the wait runs
  out, a change fails and leaves the file unchanged, whereas a pass signs an authorization with no
  bindings and fails so that any withdrawal still reaches the edge (`implemented`).
- **Root follows no path the publisher controls:** Under the module, link commands run as root
  (`deployment choice`). Root owns the credential file's directory with mode 0750, and tmpfiles
  creates `lock` inside it, owned by root and the group `valley-web-links`, mode 0640. The publisher
  opens and locks `lock` through that group, and can make, replace or remove nothing there
  (`deployment choice`). Link commands open the lock without following a symbolic link or waiting on
  a FIFO, refuse anything but a regular file owned by root, and change the owner of nothing. The
  delivery record is the one file they read in the publisher's state, and they read it with the same
  checks (`implemented`).
- **The revision moves forward:** Every change writes a revision one past the greater of the file's
  revision and the greatest one a pass has signed from. So a change to a file restored from an older
  copy still writes past every revision already signed.
- **A restore restores the links:** A pass signs the credential file as it finds it. Restoring the
  host from a backup therefore restores the links as they were at the backup, and a link revoked
  after the backup is active again after the next pass. `valley-web-link revoke --all` cuts every
  link off ([security.md](./security.md#bearer-read-credentials)).
- **The file stays readable:** Reads and writes hold the file to 1 MiB. `issue` refuses to write it
  within 64 KiB of that limit, which always fits a revocation of every active link, so a revoke is
  never refused for size. `link prune` removes the revoked links whose revocation is enforced. A
  link absent from the file is as revoked as one recorded revoked, since the edge accepts only a
  link the stored authorization binds.
- **Why verifiers:** The edge receives verifiers and never secrets. A copy of the edge's store, or
  of the authorization, lets no one read: a verifier cannot be turned back into the link.
- **Delivery:** After every issue or revoke, the wrapper starts a policy-only pass. That pass takes
  the authorization lock and the links lock alone, so a stalled content upload does not hold up a
  revocation. The publisher records the file revision whose links the last acknowledged
  authorization carried. `link list` reports a link as `enforced` only when that revision is at
  least the one that set the link's state, and as `pending` otherwise. A pass already running when
  the change was made may have signed before it, so while a link is pending the wrapper starts the
  policy pass once more. It never repeats the change. The wrapper exits non-zero while a link is
  still pending, and running a revoke again retries the delivery.
- **Backup:** When the publisher is enabled, the host backup holds the credential file's directory
  and the publisher's state under `/var/lib/private`: its export key, its authorization counter and
  its delivery record (`deployment choice`).

## The web server

`valley-web` serves a receiver's store over HTTP (`web/server/`, `web/cmd/valley-web/`). It builds
each response for one reader (see [Each reader's view](#each-readers-view)). For the projects that
reader may see, it serves a projects page and a knots page of all of them. For each of those
projects, it serves the project's own knots page, each knot page, and a source browser for `main`
under the project's group, including a gzipped tar archive for each reachable commit. When started
with `--origin`, `valley-web` serves a request that carries a valid session cookie as that session's
principal, and serves every other request as `anyone` (`implemented`; see
[Readers' sessions](#readers-sessions)). Without `--origin`, it serves every request as `anyone`.
`valley-web` runs alongside the receiver as a separate process (see [The receiver](#the-receiver)).
It renders Markdown files and READMEs as documents with its own renderer, package `web/markdown`,
which uses the standard library alone. The renderer writes only a fixed set of elements and
attributes, and shows raw HTML as text. A relative link in a document is resolved to the target's
page at the same commit with no git call. Its flags, routes, and limits are in
[Web server (`valley-web`)](./reference.md#web-server-valley-web).

### Each reader's view

Each request is read as one principal, called its reader. A reader's view is the set of projects
that reader may see. The server builds every page, link, redirect, and source route from that view.

**Which projects a reader sees.** The filter `visible` selects the projects in a reader's view
(`implemented`, `web/server/plan.go`). A reader sees a project when two conditions hold: the project
belongs to one of the export's groups, and the stored authorization allows either `anyone` or the
reader's principal to read it. `Authorization.Allows` checks one principal at a time against the
project's name and publication identity (`web/export/authorization.go`). `visible` checks `anyone`
and the reader's principal and takes the union of both results. A project without a group has no web
pages.

**What a reader's view holds.** To build a view's lookup index and knots page, `projectForReader`
copies the projects in the reader's view and each of their knots (`implemented`,
`web/server/projection.go`). The decoded export stays unchanged, and all views of that generation
share it. Each copied knot keeps its own fields and the `state` and `ready` values that the host
derived. Hiding a project never recalculates the host's plan. `projectForReader` removes derived
information about projects outside the reader's view:

- `waiting_on`, `cleared_by_ending`, `dangling`, and `unresolved` keep only references to knots in
  projects the reader sees.
- `waiting_on_hidden` is `true` when the export set it or when `projectForReader` removed a
  reference from `waiting_on`. It records only that a `waiting_on` reference was hidden, and says
  neither which knots nor how many.
- In those four lists, a valid entry is either a knot ID in the knot's own graph or
  `<project>:<id>`. Any entry that does not match one of those two shapes is also removed, even if
  it begins with the name of a visible project.
- In `edges`, an edge into a project the reader sees keeps its exported fields. An edge into any
  other project becomes a `hidden` edge, and its title, status, and reason are dropped regardless of
  its exported state.

The authored reference fields — `blocked_by`, `target`, `graduated_into`, and change links — stay
unchanged in the copied knot. When rendering a page, the server resolves each reference through the
view's index after checking that the entire value matches a shape allowed for its field
(`knot/validate.go`, `web/server/knot.go`):

- A `blocked_by` entry (or `waiting_on` entry on the knots page) is a knot ID in the knot's own
  graph (`knot.IsLocalID`), or `<project>:<id>` (`knot.IsQualifiedID`).
- A `target` is a project name (`knot.IsName`), or `<project>:<id>` (`knot.IsQualifiedID`). Because
  a single-segment `target` always names a project rather than a knot in the same graph, a project
  whose name looks like a knot ID is still checked as a project.
- `graduated_into` is the ID of an outcome or question in the knot's own graph (`knot.IsLocalID`).
- A change link is `valley:<project>/<change ID>` or `https://github.com/<owner>/<repo>/pull/<n>`
  (`knot.IsChangeLink`).

Because the server validates the whole string before looking up a project or showing any text, a
malformed reference that begins with a visible project name — such as `app:site:oc-b000001` for
`<project>:<id>` or `valley:app/site/secret` for a change link — fails the shape check.

When a reference names a project outside the reader's view, or fails its field's shape check, the
page shows a fixed placeholder with no link and none of the reference's text:

- "an unpublished project" for a `target` that names a project or fails the shape check;
- "a knot in an unpublished project" for `blocked_by`, `waiting_on`, `graduated_into`, or a
  `<project>:<id>` entry in `target`;
- "a change in an unpublished project" for a change link.

The server never infers whether a knot is waiting from `blocked_by`, because a blocker listed there
may already be done. Instead, it uses `waiting_on` and `waiting_on_hidden`. The server needs the
export's `waiting_on_hidden`, because the edge cannot reconstruct the blocker status of a project
that `export.json` omits. The export sets the flag when it hides a `waiting_on` reference into an
omitted project, and `projectForReader` sets it when it hides a `waiting_on` reference that is
malformed or names a project the reader may not see. Which references `waiting_on` holds is
described in [Unpublished blockers](#unpublished-blockers). As a result, a knot whose only blocker
is in a hidden project renders identically whether the export omits that project or includes it for
another reader. When the blocker is done, neither view shows the knot as waiting. When the blocker
is open, both views show once that it waits on "a knot in an unpublished project".

A project outside the reader's view adds no entry on the projects page, navigation link, former
name, alias, redirect, target title or status, or source route. A group that contains no project in
the reader's view has no page and no group alias. Any request for a hidden project returns the same
static `404` page as a request for a project that does not exist.

This filtering applies only to the export's structured fields. Authored prose in a visible project —
such as a knot body, a comment, a README, or an absolute link — is published as written.

Checked by `web-server-unit`:

- `TestNamedReadersSeeTheirOwnProjects` serves `anyone` and two named readers over signed fixtures
  that hold private projects across every route, alias, landing redirect, cross-project reference,
  Markdown page, and archive.
- `TestVisibilityIsTheUnionUnderThePublicationIdentity` checks that `visible` takes the union of
  `anyone` and the reader's principal and matches each project's publication identity.
- `TestAHiddenBlockerReadsAlikeInEitherExport` builds one export without a blocker's project and one
  holding it for another reader, and compares the projected fields and rendered pages byte for byte
  with the blocker done and open.
- `TestProjectionHidesOtherProjects` checks that `projectForReader` strips hidden projects from the
  derived lists and edges without changing the decoded export.
- `TestATargetNamesNoProjectTheReaderMayNotSee` gives a public knot a target in a private project
  whose name looks like a knot ID, checks that no page for an unauthorized reader names it while an
  authorized reader gets a link to its source, and checks the shape rules for `graduated_into`,
  `blocked_by`, `waiting_on`, and change links.
- `TestMalformedReferencesNameNothing` passes values that start with a visible project name yet
  match none of their field's shapes, checking that `projectForReader` removes them from the four
  derived lists and that every page shows them as unpublished with none of their text for all
  readers.

### Requests, loads and indexes

Every request runs through eight steps (`implemented`, `web/server/server.go`):

1. Check the HTTP method, read the stored authorization, and identify the request's reader.
2. Obtain the active generation's decoded export and the index for the reader's view.
3. Resolve the request path through that index.
4. Answer redirects and `404` pages directly from the index without taking a `git` slot.
5. For a source route that reads a repository, take a `git` slot and then open the generation from
   which the view was built.
6. Render the complete response in memory.
7. Release the generation and unpin the index.
8. Re-read the authorization under the shared serving lock and write the response headers.

A request holds no generation on disk while waiting for its view or for a `git` slot. If the active
generation advances before the request opens its generation in step 5, the request releases its
`git` slot, generation, and index, and starts again from step 2 within its 15-second deadline.

**Decoded exports.** The server caches the decoded `export.json` of the newest generation it has
read. When a request needs a generation that has not been decoded yet, it waits for a load. Each
load runs in its own worker, which opens the active generation, reads and decodes `export.json`, and
releases the generation. Requests waiting for the same generation share one load. If all waiting
requests leave before the load finishes, the worker cancels the load and discards the partial read.

At most 2 loads run at once (`server.MaxLoads`), using two loader slots: one general slot and one
slot reserved for anonymous requests. The reserved slot runs a load only while at least one
anonymous request waits for that load, so loads with only named waiters use the general slot alone.
When an anonymous request joins a load that is waiting to start, it can start that load on the
reserved slot, and named readers' requests for the same generation can join it there. If the last
anonymous waiter leaves a load on the reserved slot while named waiters remain:

- **When the general slot is free:** the load moves to the general slot and continues running, so
  the named waiters lose no progress.
- **When the general slot is busy:** the server cancels the load and frees the reserved slot at
  once. The remaining named waiters wait for the general slot within their own deadlines. The
  cancelled worker holds no loader slot while it exits at its next cancellation check.

Because a load with only named waiters never holds the reserved slot, an anonymous request never
waits for a loader behind named readers' loads. Concurrent requests for a generation share one read.
A read that every waiting request abandons is cancelled, so a later request may read that generation
again, as it may after the reserved slot's cancellation above.

**Indexes.** Once a generation's export is decoded, the server builds an index for each view it
serves. An index is keyed by three values: the generation sequence number, the exact revision of the
authorization (its signed bytes), and the sorted names and publication identities of the view's
projects. Readers who see the same set of projects share one index, and a named reader who sees no
more than `anyone` shares the `anyone` index. The reader's identity never enters an index or any
page rendered from it. Each index is built in its own worker after the export is decoded; that
worker holds no loader slot, and no load waits for an index build.

Cached and building indexes occupy places in two pools:

- **Anonymous places (2 places):** Reserved for the `anyone` index — one for the current generation
  and one for a generation replacing it or being replaced. Named readers' requests never take an
  anonymous place.
- **Named places (8 places, `server.MaxViews`):** Shared by named readers' requests. Any view that
  includes projects beyond `anyone` takes a named place. A named reader whose view matches `anyone`
  serves from the shared `anyone` index object while occupying a named place for it.

A request pins its index from step 2 until rendering finishes in step 7, before the response is
written. While an index is building or pinned by active requests, it occupies a place in each pool
that has at least one request waiting for or serving from it. As soon as no requests from one pool
pin the index, it releases that pool's place if requests from the other pool still pin it. For
example, when only named readers' requests pin the `anyone` index, it occupies a named place and no
anonymous place. When the last request unpins an index, the index remains cached in the single pool
of that last request. An index from an older generation or an older authorization stays counted in
its pool until the last request using it unpins it, and is then dropped.

To free a place in a full pool, the server evicts only an index that no request is currently
pinning. If all waiting requests leave an index while it is still building, the server cancels the
build. The cancelled build keeps its pool place until its worker exits, and no new request can join
it. As a result, at most 10 workers (2 anonymous and 8 named) can build indexes at once.

When a request must wait for a generation load, an index build, or a free place in its index pool,
it takes one of 64 view-waiting places (`server.MaxLoadWaiting`) and answers `503` if its 15-second
deadline expires first. Named readers' requests may take at most 48 of those 64 waiting places
(`server.MaxNamedWaiting`), including when waiting for the `anyone` index or for a named place to
hold it. Anonymous requests may take any of the 64 places. A request that finds no waiting place it
may take — all 64 full for an anonymous request, or 48 named places or all 64 places full for a
named request — answers `503` at once.

Together, these reservations keep named readers from exhausting resources that anonymous requests
need. At least 16 waiting places, the reserved loader slot, and the 2 anonymous index places are
always available to anonymous traffic. The reservations do not isolate CPU or `git`: every other
limit is shared by all readers, and adding readers does not raise it. When all 8 named index places
are full, named readers' requests wait while anonymous requests do not, and an anonymous request for
the current generation always gets the current `anyone` index without waiting on any named reader's
request. Anonymous requests share their 16 reserved waiting places, reserved loader slot, and 2
index places among themselves. The limits shared by all readers are 8 `git` slots (`server.MaxGit`),
2 Markdown rendering slots (`server.MaxRendering`), the 64 MiB response budget
(`server.MaxBuffered`) with its 48 MiB cap for large responses (`server.MaxLarge`), the 8 MiB
response size limit (`server.MaxResponse`), and the archive limits (see
[Web server (`valley-web`)](./reference.md#web-server-valley-web)).

Checked by `web-server-unit`:

- `TestIdenticalViewsShareAnIndex` checks that four readers across two distinct views build two
  indexes.
- `TestAnonymousViewsSurviveNamedSaturation` fills every named place with in-progress index builds
  and verifies that `anyone` is still served, that a new generation loads and indexes meanwhile,
  that a request waiting for room holds no loader, `git` slot, or generation and answers `503` at
  its deadline (or nothing if the client disconnects), and that a named reader whose view matches
  `anyone` still waits for a named place.
- `TestNamedRequestsTakeNoAnonymousPlace` has a named reader whose view matches `anyone` pin two
  generations' `anyone` indexes, activates a third generation, and checks that an anonymous request
  for the knots page is served at once, including when all other named places are full and that
  named reader's request for the third generation is waiting.
- `TestAnonymousRequestsKeepTheirWaitingPlaces` fills the 48 named waiting places, checks that the
  next named request answers `503` at once, and verifies that 16 anonymous requests can still wait
  for a new generation's load and be served.
- `TestAnonymousRequestsKeepALoader` checks the reserved loader slot.
- `TestALoadAnonymousRequestsLeaveMovesToTheOtherLoader` has the sole anonymous waiter leave a load
  on the reserved loader while a named reader still waits, checking that the load moves to the free
  general loader, that the reserved loader is immediately free for an anonymous request on a newer
  generation, and that the first export is read only once.
- `TestALoadAnonymousRequestsLeaveGivesBackTheKeptLoader` repeats that scenario while the general
  loader is busy with another named load, checking that the shared load is cancelled, that the
  reserved loader is freed at once so an anonymous request for a third generation succeeds while
  both named loads are held, and that the remaining named waiter is served from a second load of its
  generation once the general loader frees up.
- `TestIndexCancellationGivesBackItsPlace` cancels an index build at each step, and
  `TestProjectionStopsWhenCancelled` cancels `projectForReader` inside a knot's lists and edges.
- `TestAStoppedBuildKeepsItsPlace` cancels a build whose worker continues running, checking that
  requests for another view and for the same view wait for room and that at most 8 workers build
  named indexes at once.
- `TestSourceAdmission` checks that redirects, missing pages, and hidden projects take no `git` slot
  when all `git` slots are full.

### Readers' sessions

A reader signs in by redeeming a read link once to receive a session cookie (see
[Reader sessions](./reference.md#reader-sessions) for the routes and formats). `valley-web` tracks
redeemed links and active sessions in its browser state (`web/readerstate`), stored in `browser/`
beside the store descriptor (`implemented`). For each redeemed link, the browser state permanently
records the binding under which the link was redeemed, along with the SHA-256 hash of its session
token until the reader signs out.

**One owner.** Only one `valley-web` process may open a `browser/` directory at a time. At startup,
`valley-web` acquires an exclusive `flock(2)` lock on `browser/owner.lock` and holds it until exit;
a second process pointed at the same directory refuses to start. `valley-web` also refuses to start
if `browser/` is accessible to any other user. Because a single process owns `browser/`, it can keep
the decoded state in memory knowing no other process modifies the files on disk. The deployment
provisions `browser/` owned by `valley-web`'s user with mode `0700`; neither `valley-web` nor the
receiver creates that directory or changes its owner.

**Reads.** A request resolves its session under a read lock on the in-memory snapshot, without
taking a `flock(2)` lock or re-parsing `browser/state`. Waiting for the read lock stops if the
request's deadline expires or the client disconnects. Under the read lock, `valley-web` runs one
`lstat(2)` on `browser/state` to verify that the file on disk still matches the file it last read or
wrote. If `browser/state` is missing, replaced, or modified, `valley-web` marks the browser state
unavailable until the process restarts and never falls back to an earlier snapshot. While the
browser state is unavailable, requests carrying the session cookie answer `503` and requests without
the cookie are served as `anyone`.

**Transactions.** Redemption and logout acquire three locks in fixed order and release them in
reverse order:

1. The in-memory snapshot's write lock.
2. The transaction file lock (`flock(2)` on `browser/lock`).
3. The receiver's serving lock (`v2/serving.lock`, shared), under which the transaction re-reads the
   stored authorization.

A transaction never acquires a browser-state lock while holding `v2/serving.lock`. Waiting for any
lock stops if the request's deadline expires or the client disconnects. The transaction checks the
request context after acquiring each lock and checks again after encoding the new state document,
immediately before creating `browser/state.new`. If the deadline has expired at any of those checks,
the transaction aborts without writing anything, so a timed-out redemption consumes no link and a
timed-out logout ends no session.

Once a transaction creates `browser/state.new`, it runs to completion. It writes the full next
document to `state.new`, syncs `state.new`, renames it over `state`, and syncs `browser/`. Only
after the directory sync succeeds does `valley-web` update the in-memory snapshot and send `204`
with `Set-Cookie`. If any step from writing `state.new` through syncing `browser/` fails, the
outcome is uncertain, and `valley-web` marks the browser state unavailable until the process
restarts. Because redemption marks the link consumed and records its session hash in a single
transaction, at most one concurrent redemption of the same link can commit. A crash before the
rename leaves the old `state` in place, so the link can still be redeemed on retry; a crash after
the rename leaves the link consumed even if the `204` response never reached the browser. At
startup, `valley-web` removes any leftover `state.new` without reading it, and syncs `state` and
`browser/` after loading `state` into memory.

**Initializing `browser/state`.** When `browser/state` does not exist yet, `valley-web` creates an
empty `browser/state` only when `v2/authorization` is present, parses cleanly, and contains no
`binding` records. If `v2/authorization` is missing, unreadable, or binds any link, `valley-web`
leaves `browser/state` uncreated. Initialization takes the same three locks as a transaction,
re-reads `v2/authorization` under the shared `v2/serving.lock`, and holds `v2/serving.lock` until
the empty `browser/state` is synced on disk. Because the receiver needs an exclusive lock on
`v2/serving.lock` to rename a new authorization into place, no authorization carrying a binding can
land between that check and the commit. `valley-web` attempts initialization at startup and on every
request while `browser/state` is missing, so it creates the file as soon as an authorization with no
bindings arrives, without a restart. Any link bound before `browser/state` exists can never be
redeemed; the operator must revoke all links so the edge initializes `browser/state`, and then issue
new links.

**Requests.** A `POST` to `/-/redeem` or `/-/logout` executes once under the request's 15-second
deadline and never reads the export. A `GET` or `HEAD` request resolves its session under the
browser state's read lock when it arrives and renders the page as that session's principal. Before
writing response headers, the server's final check acquires the browser state's read lock and then
the shared `v2/serving.lock`, re-reads the authorization, and resolves the session again. If either
the authorization or the session changed during rendering, the server discards the response and
renders again, up to three renders in total. If the request's deadline has expired by that final
check, the server answers `503` without renewing the cookie. Otherwise, while still holding both
locks, the server attaches the renewed `Set-Cookie` header and writes the response headers, then
releases both locks before writing the body. Any logout or revocation that commits before the final
check takes effect on that response; one that commits after the headers are written takes effect on
the next request.

**Boundaries.** The stored authorization alone decides which projects a session's principal may
read; the browser state cannot grant access beyond that authorization. No authorization can make a
redeemed link redeemable again. A session remains valid only while the stored authorization binds
its credential ID to the exact principal and verifier under which the link was redeemed, so
re-binding the same credential ID to another principal or secret does not carry the session over.
The receiver's `Store.Authorize` never takes a browser-state lock, so a full or unavailable browser
state never delays storing a new authorization or revocation. Rolling `browser/state` back to an
older, internally valid copy is not detected.

Checked by:

- `web-server-unit`, which runs the browser-state unit tests (`web/readerstate/`) and the server's
  session tests (`web/server/session_test.go`): single-use redemption, identical `403` refusals, the
  `POST` header and body contract, cookie parsing and exact binding checks, the reader pages'
  `Content-Security-Policy`, unavailable and full states, initializing `browser/state` and its race
  against a new binding, concurrent redemptions, deadlines expiring while waiting on a stalled write
  or encoding a transaction, and revocation or logout during page, archive, or redirect rendering.
- `web-reader-state-crash`, which kills `valley-web` or injects filesystem errors at each step of
  redemption, logout, and initial state creation, then restarts `valley-web` on the resulting
  directory (`web/cmd/valley-web/crash_test.go`). It verifies that logout never answers `204` before
  its change is durable on disk, and that an initial state creation interrupted before its rename,
  followed by an authorization with link bindings leaves `browser/state` uncreated on restart.
- `web-reader-browser-e2e`, which drives `/-/redeem` in headless Chromium behind a TLS proxy in the
  build sandbox (`web/cmd/valley-web/browser_test.go`). It also spoils two redemption responses to
  verify that the page reports an unknown outcome and queries `/-/session` on retry before signing
  any session out.
- `web-push-e2e`, which redeems a link issued by the packaged publisher and exercises server
  restarts, link revocation, logout, and recovery from a missing or corrupted `browser/state`. It
  also redeems a link of a named reader of a project that `anyone` may not read. The session follows
  `/` to the projects page, finds that project there, and opens its tree page and its knots page
  from the page's links. It reads the project's knot, archive and the knots page of every project,
  and gets `404` for its tree page once a pass delivers the link's revocation.
- `web-receiver-unit`, which uploads a signed export with private projects through the real receiver
  to the packaged `valley-web` (`web/cmd/valley-web-receive/readers_test.go`). It verifies that two
  named principals and `anyone` each see the knots, source pages, and archives of their authorized
  projects alone, receive `404` for the rest, and lose access on the next request after a grant is
  withdrawn.

### Reading the state directory

`valley-web` only reads the receiver's store. Both programs take the path to the store's descriptor
file, and the store's payloads live in the `v2/` directory beside that file. `valley-web` opens the
store with `export.OpenReadOnly`, which creates no files and takes only shared `flock(2)` locks: on
the generation opened for a request, and on `serving.lock`, which it opens read-only, from its last
check of the authorization until the response's headers are written (`web/export/store.go`,
`web/export/authorize.go`). When the receiver opens the store with `export.OpenStore`, it recovers
any interrupted activation, sweeps old generations, and prunes withdrawn repositories. Until the
receiver finishes an interrupted activation, `valley-web` continues to serve the generation that
`current` points to.

The store's entry point is a regular file so that binaries built for the v1 store layout, which
required a directory path for `--state`, refuse to open it. The `v2/` payload directory also omits
the `active` symlink and `exports/` directory that v1 binaries read, so pointing a v1 binary at
`v2/` serves nothing. Rolling the edge back to v1 binaries therefore serves no content from a v2
store, and no v1 binary ever reads the store without checking the authorization. The receiver writes
nothing outside the descriptor and `v2/`: state of the v1 layout beside the descriptor, which held
public projects only, stays until the operator deletes it by hand.

Because `valley-web` writes nothing to the receiver's store, it can run as a separate Unix user with
read-only access to the store. The one place it writes is its own browser state in `browser/` beside
the descriptor (see [Readers' sessions](#readers-sessions)). The receiver keeps each staged upload
private to its own user until activation, and explicitly sets world-readable modes on `v2/`,
`generations/`, the descriptor, the authorization, and every directory and file of an activated
generation regardless of its umask (`web/export/store.go`; see
[Web export receiver state](./reference.md#web-export-receiver-state) for the file modes). The mode
of the descriptor's parent directory is a **deployment choice**. The receiver creates that directory
if it does not exist and leaves its mode unchanged, and the deployment grants the server's user read
and search access to it, for example through its group.

Every `git` command that `valley-web` runs names the repository with `--git-dir`
(`web/export/bundle.go`). Passing `--git-dir` lets `git` read a repository owned by the receiver's
user without checking `safe.directory`, which the server's isolated `git` configuration cannot set.

Checked by `web-unit` (`TestStoreModes`), which checks the staging and generation modes under umask
`0077`, and by `web-server-unit` (`TestServesAReadOnlyStateDirectory`), which runs `valley-web` on a
state directory written under umask `0077` with read-only permissions and repositories that `git`
treats as owned by another user.

### Groups and web URLs

Every project on the web belongs to a group. Project URLs begin with the group name followed by the
project's web name: `/<group>/<project>/…`, such as `/tools/app/knots/<id>`. `/<group>/` lists the
group's projects. The root path `/` redirects to the projects page at `/-/projects`, or to the
source tree of the reader's one project, and the knots page of every project is at `/-/knots`.
Because URLs appear in links, commits, and knots, renaming a group or a project redirects old URLs
only if the old name stays in the group's `aliases` or the project's `formerNames`. The lookup index
uses only declared names (`implemented`, `web/server/plan.go`).

Groups and project placements are declared in `valley.cue` alongside each project's git store,
mirrors, ref protection, and push grants (`schema/valley.cue`). At build time, the NixOS modules
validate `valley.cue` with `cue vet` and export it to JSON with `cue export`. The web publisher
module passes that exported JSON file to `valley-web-publish` as `--host` (`nix/web-publisher.nix`).
Which groups exist and which group each project belongs to is a **deployment choice**.

```cue
groups: core: {name: "tools", aliases: ["t"]}
groups: web: name: "docs"
projects: app: {group: "core", formerNames: ["tool"], aliases: ["app"]}
projects: "docs-app": {group: "web", name: "app"}
```

- **Group:** Each group has a permanent identifier (`core` above) that projects reference in
  `group`. That identifier never appears in URLs. The group's `name` (`tools`) is the first segment
  of its projects' URLs. Each entry in `aliases` (`t`) is an alternate first segment that redirects
  to `name`. When a group changes its `name`, adding the old name to `aliases` keeps existing links
  working. Group members and stewards are **not built**.
- **Project identifier:** The key under `projects` (`app` and `docs-app` above) is the project's
  permanent identifier. It is unique across the host and names the project's repository, plan graph,
  `policy/read`, and read grants.
- **Project web name:** The project's `name` field is the second segment of its URLs, and defaults
  to the project's identifier when omitted. Above, `app` is served at `/tools/app/…` and `docs-app`
  is served at `/docs/app/…`. A web name must be unique only within its group, so two groups can
  each have a project with the web name `app`. Changing `name` renames the project on the web while
  keeping its identifier unchanged.
- **Former names:** `formerNames` lists a project's previous web names within its group. Requests to
  `/tools/tool/…` redirect to `/tools/app/…`.
- **Short aliases:** `aliases` on a project lists single-segment shortcuts at the root of the URL
  path. Requests to `/app/…` redirect to `/tools/app/…`.

Only projects in a group have web URLs. A project that sets a custom `name`, a nonempty
`formerNames`, or a nonempty `aliases` must also set `group`. Explicit empty lists are valid without
it.

URL names fall into two namespaces. First-segment names — group `name` values, group `aliases`, and
project `aliases` — share one namespace across the whole server. Second-segment names — the `name`
and `formerNames` of projects in the same group — share one namespace per group, and names in
different groups never collide. Within each namespace, names are compared case-insensitively in
ASCII, and duplicate names are rejected. `valley-web` reserves paths starting with `/-/` for its own
routes. Because every valid name starts with an ASCII letter or digit, no group or alias can be `-`,
and server routes under `/-/` never collide with group or alias routes. Three checks enforce these
rules (`implemented`):

1. `cue vet` validates the host declaration at build time (`schema/valley.cue`).
2. `valley-web-publish` validates `--host` with `export.CheckNames` before starting a pass
   (`web/cmd/valley-web-publish/host.go`, `web/export/groups.go`). It also requires `--host` to be a
   JSON object with non-null `projects` and `groups` maps, non-null project and group entries, and
   valid project names as keys, so a truncated or malformed file cannot be misread as an empty host
   declaration.
3. `export.Validate` runs `export.CheckNames` when the publisher encodes `export.json` and when the
   receiver or `valley-web` decodes it (`web/export/export.go`).

`export.json` includes only the groups that contain at least one exported project, along with each
exported project's group, web name, former names, and short aliases (`web/export/project.go`).
Groups that contain no exported project, as well as the web names, former names, and aliases of
projects that are not exported, are omitted. An export that omits `groups` and per-project `group`
fields still decodes, and `valley-web` serves none of its projects because they have no group URL.

`valley-web` responds to requests under a group alias, a project short alias, or a project former
name with a `308` permanent redirect. A group alias redirects to the same path under the group's
`name`. A project short alias redirects to the same path under `/<group>/<project>`, and a former
name redirects to the same path under the project's current web name. The server copies the
remaining path and raw query string verbatim into `Location`, without path cleaning, and preserves a
trailing `?` when the query string is empty. Because `Location` always begins with `/` followed by a
group name, it stays on the server's origin. When a client resolves dot segments in the redirected
path, the resulting request lands on another path on the same server and is handled normally.
Requests for a project the reader may not see — under its group, an unknown group, a short alias, or
a former name — return the same `404` response as requests for a nonexistent project (see
[Each reader's view](#each-readers-view)). The server renders a single static `404` page at startup
that includes no data from the request or the export (`web/server/render.go`; see
[Web server (`valley-web`)](./reference.md#web-server-valley-web)).

When loading a new generation, `valley-web` decodes `export.json` and validates its names. Building
a reader's index then copies the reader's projects and indexes their groups, aliases, and former
names. Each step checks its context before processing each name, so a cancelled load stops at the
next cancellation check and releases its generation and any loader slot it still holds, and a
cancelled index build stops and releases its place (`web/export/groups.go`, `web/export/export.go`,
`web/server/projection.go`, `web/server/plan.go`).

Checked by `cue-vet`, which verifies that the eight rejected host declarations report the colliding
name or missing group and exports two groups that each contain a project named `app`; by `web-unit`
(`TestBuildRefuses`, `TestBuildExportsThePublicProjectsGroups`, `TestDecodeIsStrict`,
`TestDecodeHoldsGroupsToTheirProjects`, `TestDecodeTakesAnExportWithoutGroups`,
`TestCheckNamesStopsWhenCancelled`, `TestValidatingGroupsStopsWhenCancelled`,
`TestGroupsComeFromTheHostDeclaration`, and `TestAMalformedOrCollidingHostDeclarationIsRefused`);
and by `web-server-unit` (`TestHiddenProjectAnswersLikeMissing`, `TestAliasesRedirectPermanently`,
`TestTwoGroupsEachHoldAnApp`, `TestRedirectsLeadWhereTheBrowserGoes`,
`TestPlacingStopsWhenCancelled`, `TestNotFoundIsTheOnePage`, and
`TestProjectNamesBesideTheServersPaths`, which serves groups and projects named `source`, `knots`,
and `web-export` on every route and checks that paths under `/-/` answer `404`).

### Slow readers

`valley-web` renders each response in full before writing it to the connection. Rendered responses
draw their memory in 64 KiB pieces from a shared 64 MiB budget (`server.MaxBuffered`) and hold those
pieces until the write finishes or the 60-second write timeout expires (`web/server/render.go`).
Without a cap on large responses, a few clients that stop reading large knot, directory, or file
pages could hold the entire 64 MiB budget until the write timeout, causing every other request to
answer `503`. Behind a reverse proxy, every request arrives from the proxy's address. A per-address
limit there is one limit shared by every reader of the proxy, because it cannot tell those readers
apart.

To keep room for small pages, the budget caps the memory held by responses larger than 256 KiB
(`server.SmallPage`) at 48 MiB (`server.MaxLarge`; see
[Web server (`valley-web`)](./reference.md#web-server-valley-web)). While a response is 256 KiB or
smaller, its pieces count only against the 64 MiB total. As soon as a response grows past 256 KiB,
all of its pieces — including the first 256 KiB — count against the 48 MiB large-response cap as
well. Slow readers of large pages can therefore hold at most 48 MiB at once, leaving at least 16 MiB
for small pages.

Checked by `web-server-unit` (`TestSlowReadersShareTheBudget`), which opens stalled connections
requesting more than 64 MiB in total across three large page sizes: one 64 KiB piece past 256 KiB, 1
MiB, and 4 MiB. Counting a large response's first 256 KiB against the 48 MiB cap prevents 64 stalled
1 MiB responses from consuming all 64 MiB. The stalled readers hold at most 48 MiB, and requests for
the knots page, a knot page, and a directory page still succeed.

### A source request's git work

At most 8 source requests (tree, file, log, or archive) may run `git` at once (`server.MaxGit`). A
request takes a `git` slot only after its view is ready and its route is resolved through it (see
[Requests, loads and indexes](#requests-loads-and-indexes)). Waiting for an export to be read or an
index to be built therefore uses the request's 15-second deadline and holds no `git` slot. To keep a
slow `git` command from holding a slot for the full request timeout, a source request may hold its
`git` slot for at most 5 seconds from when it takes the slot (`web/server/server.go`). That 5-second
limit covers all of the request's `git` commands. When the 5 seconds expire, the server stops `git`,
releases the slot, and answers `503`. When `git` finishes in time, the request releases its `git`
slot before rendering HTML, so page rendering holds no `git` slot (`web/server/render.go`).

Rendering Markdown takes memory that grows with the file, not with the page: the renderer parses the
whole document before it writes anything. The renderer caps what it builds (`web/markdown`). A
document holds at most 65,536 blocks (`markdown.MaxBlocks`), counting quotes, lists and list items.
Its inline content takes at most 262,144 nodes (`markdown.MaxInlineNodes`). Within a block, at most
4,096 brackets wait for their `]`. Past each cap, the rest of the document renders as escaped text.
To match a reference such as `[label]`, the renderer copies the label. A render copies at most 4 MiB
of labels, and past that a reference renders as text. So one render of a file of up to 1 MiB
allocates less than 128 MiB in all when `MaxOutput` is set, as the server always sets it
(**implemented**). `MaxOutput` zero means unlimited output.

Checked by `web-unit` (`TestAllocation`). It renders each input of the renderer's adversarial suite
at 1 MiB, measures the bytes allocated and the heap's peak with `runtime.ReadMemStats`, and fails if
either passes 128 MiB. Some inputs nest emphasis or links, or repeat a reference, and are built to
stay under the inline cap. Each of them is measured a second time, padded to 1 MiB with the content
that allocates most. The most any input allocates is 69 MiB: a paragraph past the inline cap
followed by empty headings past the block cap. `TestRepositoryDocs` checks that each of the
repository's documents, the largest among them `docs/reference.md`, renders whole and takes less
than a quarter of each cap.

At most 2 source requests render Markdown at once (`server.MaxRendering`). Two renders allocate at
most 256 MiB, and rendered responses hold at most 64 MiB (`server.MaxBuffered`; see
[Slow readers](#slow-readers)). Together that is 320 MiB, well under the 1 GiB `MemoryMax` that
`valley-web` has by default (`webMemoryMax`, a **deployment choice**). The server's other memory,
such as the exports it has read and the `git` processes it runs, is separate and not counted in
these 320 MiB. A request waits for a rendering slot while it still holds its `git` slot, and
releases the `git` slot once it has one (`web/server/source.go`). The requests that hold a file's
text while waiting to render are therefore among the 8 that hold a `git` slot. The renderer stops
once its output would pass 2 MiB, or once the request's context ends (`web/markdown`).

Each source route also bounds the work it asks `git` to do (`web/server/source.go`; see
[Web server (`valley-web`)](./reference.md#web-server-valley-web) for the limits). When resolving a
path through intermediate directories, the server runs `git ls-tree` without `--long`, because
`--long` looks up the blob size of every entry in each directory. All directories along the path
share one 4 MiB output budget (`server.MaxRead`), so resolving a deep path reads no more from `git`
than listing a single directory.

On a history page, the server runs `git log --date-order --max-count=50` starting from the page's
starting commits (`<commit>` on the first page, or the commits in `?from=` on an older page). Each
page starts `git log` directly at those commits and skips no commits of newer pages. For each page,
`git` prints at most 50 commits (`server.LogPage`), and the page reads at most 4 MiB of that output
(`server.MaxRead`). All of this `git` work runs within the 5 seconds that the request holds its
`git` slot.

As the page lists commits, it tracks the frontier for the next older page: the page's starting
commits plus the retained parents of each commit shown on the page, minus the commits shown on the
page. A commit's retained parents are its first 128 parents (`server.MaxParents`). Only a merge with
more than 128 parents has parents that are not retained. Because `--date-order` never lists a commit
before any of its children, every unshown ancestor that `<commit>` reaches through retained parents
is reachable from that frontier. No commit already shown on this page or a newer page is reachable
from it.

While the frontier holds at most 64 commits (`server.MaxFrontier`), the "Older" link passes all of
them in `?from=`. Following "Older" from the first page then visits each ancestor reached through
retained parents once, including across merges. When wide merges leave more than 64 commits in the
frontier, the "Older" link passes the first 64 and the page adds a separate `?from=<id>` link for
each remaining commit. Those links keep every ancestor along retained parents reachable, though two
separate links may converge on a shared ancestor and show it on both paths. To bound the number of
links on a page, a page stops before any commit after the first that would leave more than 192
unshown commits (`server.MaxLeftToShow`). The first commit on a page always fits within 192 because
the page starts from at most 64 commits (leaving 63 others) and follows at most 128 parents
(`server.MaxParents`).

`git log` prints all of a commit's parent IDs on one line, which for a merge with 400 parents
exceeds 16 KiB. To keep a long parent line from truncating a commit's ID or subject, the server
formats each commit as two NUL-terminated records — first its ID and parents, then its date, author,
and subject — and keeps up to 16 KiB of each record. Sixteen KiB holds a commit ID and 128 parents
in both SHA-1 and SHA-256 object formats. When a merge has more than 128 parents, the page shows the
commit with a note. The page parses only the first 128 parents, so a parent line cut at 16 KiB still
parses. A merge with any number of parents therefore renders, within the request's usual limits on
output and time.

The page retains only a merge's first 128 parents for older pages. This limit applies to the
frontier alone. `git log` still walks every parent of the merge, so a commit reached only through a
later parent may appear on the same page, and the page retains that commit's parents in turn.
Commits reached only through later parents may be omitted from older pages.

When the receiver accepts a bundle or carries a repository into a new generation, it writes a
commit-graph into the repository (`web/export/bundle.go`). Already-activated generations are left
untouched, so a generation activated without commit-graphs has none until the next activation
carries its repositories again. With a commit-graph, `git merge-base` checks commit reachability
without parsing each commit, and `git log --date-order` walks the history faster. Without a
commit-graph, `git log --date-order` walks the commit history before printing a commit, within the 5
seconds that the request holds its `git` slot.

`export.Reachable` requires every commit ID to match the length of the project's `source_tip`
(`web/export/bundle.go`). In a SHA-256 repository, `git` treats 40 hex digits as an abbreviated
prefix, and in a SHA-1 repository it treats 64 hex digits as the commit named by their first 40 hex
digits. Requiring the exact length of `source_tip` gives each page a single address.

Checked by `web-server-unit`:

- `TestSourceGitHasItsOwnBound`, `TestAWaitForAViewHoldsNoGitSlot`, and
  `TestRenderingHoldsNoGitSlot` test a `git` command that never finishes, a slow export read, and a
  slow page render.
- `TestResolveSharesOneBudget` resolves a path through two large directories.
- `TestHistoryAcrossMerges` follows the "Older" links across a two-branch merge that spans multiple
  pages and checks that every commit appears once. `TestWideHistoryStaysReachable` follows every
  link from a 65-branch merge and reaches every commit. `TestCrowdedHistoryEndsEarly` serves a merge
  whose next commit would leave 199 unshown commits and reaches every commit through its links.
- `TestMergeOfManyParents` serves a 420-parent merge whose ID and parent line exceed 16 KiB, both as
  the first commit on a page and as the second.
- `TestSHA256CommitIDs` serves a SHA-256 project.

Also checked by `web-unit` (`TestAcceptBundle` and `TestReachable`), which test the commit-graph and
both object formats.

### Source archives

A reader can download a commit's tree as a gzipped tar archive at
`/<group>/<project>/source/archive/<commit>.tar.gz` (`web/server/archive.go`; see
[Web server (`valley-web`)](./reference.md#web-server-valley-web) for routes, headers, and limits).
Every directory page links to the archive for its commit.

The archive covers the same source as the file pages: the tree of a commit that the project's
published `main` reaches, and none of the history. It is not limited to what a file page shows. It
also holds binary files, and files larger than the 1 MiB a file page shows (`server.MaxFile`).

`/<group>/<project>/source/archive/main.tar.gz` redirects with `302` to the archive of the commit at
`main`'s tip, using the same headers as `/<group>/<project>/source/`. The redirect is temporary
because `main` moves. A script or agent can fetch `main.tar.gz` with `curl -L` to download the
current tip directly. `main` is the only name the route accepts in place of a commit ID; any other
ref name, `HEAD`, or an abbreviated ID answers `404`. Because the redirect reads only the export's
`source_tip`, it runs no `git` command and takes no `git` slot, just like
`/<group>/<project>/source/`.

Both archive routes use the same project lookup, reachability check, and authorization check as the
other source routes (`web/server/source.go`). A project the reader may not see answers `404` on both
routes, matching its tree page, and takes no `git` slot.

The server writes the tar archive itself, in Go, from the commit's tree. It lists the tree with
`git ls-tree -r -t -l -z`, and reads every file's content with one `git cat-file --batch` process.
Both commands run through the `export` package's `git` helpers, in the same isolated environment as
every other source request. Neither command reads git attributes, so the archive does not apply
`export-ignore` or `export-subst`. The archive is the commit's tree byte for byte.

Every entry is placed under one directory named from the project's web name and the commit's first
12 hex digits. The entries follow the tree's order, and each directory comes before what it holds.

- A regular file has mode `0644`, or `0755` when git records it as executable (`100755`).
- A symbolic link is a tar symbolic link whose target is the link's content in git. The server never
  follows it.
- A submodule is an empty directory.
- Every entry carries the commit's committer time as its modification time, uid and gid 0, and empty
  user and group names.
- Go's tar writer chooses USTAR or PAX for each header, whichever the header needs.

The server compresses the tar stream in process with Go's `compress/gzip` at a fixed compression
level, with an empty file name and zero modification time in the gzip header. The archive's bytes do
not depend on the time of the request, the repository's refs, its git configuration, or attributes.
The tar paths include the project's web name. So a commit produces the same archive bytes on every
request to the same build under the same web name.

The server checks the tree's listing before it reads any file. It refuses an archive whose files add
up to more than 32 MiB before compression (`server.MaxArchiveInput`). It also refuses one whose tree
holds more than 10,000 entries (`server.MaxArchiveEntries`), or whose listing is longer than 4 MiB
(`server.MaxRead`). A refused archive answers `500` with the "Page too large" page.

The server then streams each file from `git` through the tar and gzip writers into the response
body. The body uses the same response budget (`server.MaxBuffered`) and 8 MiB response limit
(`server.MaxResponse`) as HTML pages. So the server holds the compressed archive in memory, and
never a whole file. It builds the archive while holding the request's `git` slot (within the
5-second `git` timeout), releases the slot once the archive is built, and re-checks the stored
authorization before writing the first byte. A compressed archive that exceeds 8 MiB is discarded
and answers `500` with the "Page too large" page. Like every response, an archive carries
`Cache-Control: private, no-store`, because the authorization can change while a commit's archive
URL stays the same.

Checked by `web-server-unit`:

- `TestSourceArchive` compares the archive's entries, in order, against the commit's tree, with
  their modes, times, and contents. It checks that two requests return identical bytes, verifies the
  headers and the link on directory pages, and checks the `404` response for invalid or unreachable
  commit names and for projects excluded by the authorization.
- `TestArchiveAppliesNoAttributes` commits a `.gitattributes` that sets `export-subst` and
  `export-ignore`, and a file that holds `$Format:%ar$`. The archive holds every file, and that file
  verbatim. The test then moves a ref, and adds attributes and git configuration to the repository,
  and the archive keeps the same bytes.
- `TestArchiveLinksAndSubmodules` checks a symbolic link, a submodule, an executable file, and the
  PAX format for a long name and a long link target.
- `TestArchiveInputBound` checks that a tree whose files pass 32 MiB, and a tree of more than 10,000
  entries, answer the "Page too large" page and hold none of the response budget.
  `TestArchiveTooLarge` checks an archive whose compressed bytes exceed a shortened `maxResponse`
  bound.
- `TestArchiveStreamsIntoTheBody` serves a 24 MiB file that compresses to less than 256 KiB. Once
  the response is rendered, it holds only the budget pieces that the compressed body fills.
- `TestArchiveOfMainsTip` checks the redirect's status, `Location`, and headers against
  `/<group>/<project>/source/`. It verifies that the redirect takes no `git` slot and follows `main`
  when `main` advances, and checks that `HEAD.tar.gz` and a branch name answer `404`.
- `TestHiddenProjectAnswersLikeMissing` checks the redirect for a project excluded by the
  authorization.

`web-push-e2e` downloads an archive from `valley-web`, lists it with `tar`, and fetches
`main.tar.gz` with `curl -L` to verify that it returns the same bytes.

### Unpublished blockers

A knot's `waiting_on` lists each `blocked_by` reference that remains a blocker. A reference remains
a blocker while it names an open knot, a missing knot, or a knot in a graph that could not be read
(`knot/derive.go`). In `export.json`, the export removes every reference into an unpublished project
from `waiting_on` (`web/export/project.go`). The knot's `state` is unchanged. When the export
removes at least one reference from a knot's `waiting_on`, it sets `"waiting_on_hidden": true` on
the knot. The server applies the same rule for each reader, stripping references into projects
outside that reader's view (see [Each reader's view](#each-readers-view)). The knots page's "waits
on" chip then includes "a knot in an unpublished project" alongside any blockers the reader sees
(`web/server/knot.go`; see [Web server (`valley-web`)](./reference.md#web-server-valley-web)).

In `export.json`, `blocked_by` and `edges` still keep each hidden blocker's `"<project>:<id>"`
reference. When a knot names a single hidden blocker, a reader of `export.json` can tell from
`waiting_on_hidden` whether that reference remains a blocker. The field is present while it does,
and absent once the hidden knot has a terminal status, even when an exported blocker keeps the knot
`blocked`. The export permits this inference.

Checked by `web-unit`, where `TestBuildHidesForeignEdges` checks that `waiting_on_hidden` is set on
the fixture's knot with a hidden blocker, and `TestWaitingOnHiddenFollowsAHiddenBlocker` checks that
the field is omitted once that hidden blocker is done. Also checked by `web-server-unit`
(`TestWaitsOnChip`), which checks that the knots page's chip lists both the public blocker and the
unpublished one.

## The receiver

`valley-web-receive` receives export uploads from `valley-web-publish` into a state directory using
the three-step upload protocol in package `export` (`web/cmd/valley-web-receive/`). `valley-web`
serves the activated export from that directory. The receiver's flags, routes, and limits are in
[Web export receiver (`valley-web-receive`)](./reference.md#web-export-receiver-valley-web-receive).

### Two processes over one state directory

`valley-web-receive` and `valley-web` run as separate processes that share only the state directory.
The receiver opens the directory with `export.OpenStore` and is its only writer. `valley-web` opens
it with `export.OpenReadOnly` and writes nothing to the store (see
[Reading the state directory](#reading-the-state-directory)). Because the store coordinates access
with `flock(2)` locks across processes, activating a new export never deletes a generation that an
in-flight `valley-web` request still holds (`web/export/store.go`).

Running the receiver and `valley-web` as separate processes isolates public reads from uploads in
three ways:

- **Separate budgets.** Each process has its own listener and concurrency limits. The receiver
  listens on the address the publisher reaches, and `valley-web` listens on the address readers
  reach. When uploads saturate the receiver, it answers additional upload requests with `503` while
  `valley-web` continues serving within its own limits.
- **Separate users.** `valley-web` handles requests from any reader. Its user has read-only access
  to the state directory and no access to the receiver token, so a fault in `valley-web` cannot
  modify the stored export or expose the token.
- **Separate memory.** Each process's memory limit is a **deployment choice**. A limit covers both
  the Go process and the `git` commands it spawns. On NixOS, the receiver module sets `MemoryMax`
  with no swap on each service unit, from its `receiverMemoryMax` and `webMemoryMax` options, which
  default to `2G` and `1G`. In containers, each container's memory and swap limits are set where the
  container is started; the guide's example commands set both. Without per-process memory limits, a
  memory spike in the receiver could exhaust host memory and affect `valley-web`.

A single process with two listeners would share one heap, one file-descriptor table, and one Unix
user between public reads and uploads, allowing load or a fault on one side to affect the other.

### Authenticating the transport

Every request to the receiver must carry a bearer token in `Authorization: Bearer <token>`
(`web/export/token.go`). The receiver checks this header before routing the request, taking a
request slot, or reading any of the request body (`web/cmd/valley-web-receive/`). It hashes both the
received `Authorization` header and the expected header with SHA-256 and compares the digests in
constant time. If the header is missing or does not match, the receiver replies `401` with refusal
code `unauthenticated` and closes the connection.

The bearer token and the manifest signature check different things. The manifest signature controls
what the edge serves: the receiver stages a manifest only when it opens under a pinned verifier key,
and keeps only bodies whose SHA-256 digests and sizes the manifest declares. The bearer token
controls who may use the receiver at all. Without the token check, any client able to reach the
receiver could force it to verify manifests, stream bodies into staging up to the size caps, or
start commit requests that run `git`. The token check rejects unauthenticated requests before any of
that work begins.

The publisher reads the token from `--receiver-token` (`web/cmd/valley-web-publish/main.go`), and
the receiver reads it from `--token-file`. Both use `export.ReadToken`, which requires one line of
32 to 256 visible ASCII characters. Because the publisher's HTTP client follows no redirects, it
sends the token only to the configured receiver URL (`web/cmd/valley-web-publish/upload.go`).
Security properties of the token are covered in
[The edge's receiver](./security.md#the-edges-receiver).

### Bounds

The receiver bounds concurrent requests, open connections, body sizes, staged files, and total disk
usage (`web/cmd/valley-web-receive/main.go`, `web/cmd/valley-web-receive/receive.go`,
`web/export/store.go`; see
[Web export receiver (`valley-web-receive`)](./reference.md#web-export-receiver-valley-web-receive)):

- **Requests.** At most 4 upload requests and 2 authorization requests run at once, in separate
  pools so saturated uploads leave room for an authorization. Any additional authenticated request
  receives `503` with a `Retry-After` header and no refusal body, and the publisher retries it after
  a pause. Unauthenticated requests take no request slot. Once a request's headers arrive, a
  per-step time limit bounds connection reads and writes as well as store operations: 1 minute for a
  manifest, 30 minutes for a body, and 30 minutes for a commit. If that limit expires or the client
  disconnects while a manifest request is waiting for the store lock, measuring the state directory,
  sweeping it, or waiting for an older upload's body writers to stop, `Store.Stage` stops and
  releases the lock. It checks the context before each directory it deletes or creates, and each
  deletion stops part way once the context is done, leaving the rest in the trash for the next
  sweep. Likewise, if the limit expires or the client disconnects while a commit request waits for
  the lock or builds repositories, `Store.Activate` kills the process group of any running `git`,
  with every process that `git` started, and activates nothing. Each `git` runs in a process group
  of its own (`web/export/bundle.go`).
- **Connections.** At most 32 connections are open at once; additional connections wait in the
  listen queue. The server closes a connection that sends no complete request headers within 10
  seconds, and closes an idle connection after 1 minute. Every request — including `OPTIONS *` —
  reaches the receiver's own handler, which refuses unauthenticated requests and closes their
  connections before reading any request body. On shutdown, the receiver closes its listener first,
  which immediately unblocks any wait for a free connection slot. It gives in-flight requests up to
  10 seconds to finish, then closes their connections to cancel their contexts, which kills the
  process group of any commit's `git`. It waits up to 5 seconds more for those requests and their
  `git` subprocesses to exit before terminating, so a stop takes at most 15 seconds regardless of
  how many connections are open.
- **Bodies.** At step 1, `Manifest.CheckSizes` refuses a manifest as `too-large` when any declared
  body exceeds `--max-body` or when the unique declared bodies together exceed `--max-upload`. At
  step 2, `export.StageBody` checks `--max-body` again and reads at most one byte past the declared
  size.
- **Staging.** `Store.Stage` discards every older staged upload when it stages a manifest, so at
  most one upload is staged at a time. The staging directory holds that upload's completed bodies
  and at most one partial file for each body currently being uploaded. A request for a body while
  another request is already staging the same body of the same upload receives `503`. Each body
  request registers as a store writer (`Store.Writer`). Before `Store.Stage` deletes an older
  upload, it stops that upload's body writers and waits until each has closed and removed its
  partial file. As a result, no deleted partial file remains held open on disk, and a stalled body
  request from the older upload never blocks the newer upload's request for the same body with
  `503`. `Store.Activate` stops and waits for the upload's body writers in the same way before
  removing `bodies/`. Staging remains private to the receiver's user until activation (see
  [Reading the state directory](#reading-the-state-directory)).
- **Storage.** `--max-upload` counts each unique body once, whereas activation writes a bare
  repository for every exported project (even when several projects share one bundle) and writes a
  new generation alongside any existing ones still held. The receiver therefore caps the disk space
  used by the entire state directory at `--max-storage` (`Store.MaxStorage`). At step 1,
  `Store.Stage` estimates the post-upload directory size by taking current disk usage (excluding
  staging directories being replaced) and adding each unique declared body once plus one repository
  per project, sized at the larger of its bundle or the carried repository plus 1 MiB for Git's own
  files. Because an old generation that a reader held during the previous activation's sweep may now
  be free, `Store.Stage` sweeps unheld old generations and re-measures before refusing a manifest as
  `too-large`. Because a repository with many small objects can still take more space for its pack
  index and commit-graph than the step-1 estimate, `Store.Activate` re-measures the state directory
  after building each repository. If the directory exceeds `--max-storage`, `Store.Activate` removes
  the newly built repository and refuses the commit as `too-large`.

  `--max-storage` is best effort. Building a single repository can temporarily pass the cap by the
  amount the step-1 estimate missed before the post-build check removes it, and a file held open
  after deletion consumes disk space that directory measurements cannot see. The hard bound is the
  filesystem the state directory lives on: a fixed-size volume or a quota. Which mechanism an edge
  uses, and its size, is a **deployment choice**. Every edge should use one sized slightly above
  `--max-storage` (see [Run the web edge](./guide.md#run-the-web-edge)).

The receiver tracks the newest staged upload in memory. After a restart, it refuses a body or commit
for an upload staged before the restart with `no-manifest`, and the publisher signs a new manifest.

### Running the receiver and `valley-web`

On NixOS, `services.valley.webReceiver` runs two systemd units, `valley-web-receive` and
`valley-web` (`nix/web-receiver.nix`). They run as users `valley-web-receive` and `valley-web`, both
in group `valley-web`. The receiver unit owns the state directory (`/var/lib/valley-web` by default)
with mode `0750`, so `valley-web` reads it through the shared group, while `valley-web`'s unit
mounts the state directory read-only. When `origin` is set, systemd-tmpfiles creates the state
directory owned by `valley-web-receive` (mode `0750`) and `<stateDir>/browser` inside it owned by
`valley-web` (mode `0700`). `valley-web`'s unit can write only `<stateDir>/browser`, and the
receiver's unit cannot access `<stateDir>/browser`. Because systemd-tmpfiles gives the state
directory the owner that `StateDirectory` on `valley-web-receive.service` expects, systemd does not
recursively chown `<stateDir>/browser` when starting the receiver. The receiver unit reads the token
file as a systemd credential from a path outside the Nix store. Aside from those two directories,
the only writable paths in either unit are temporary: each unit's private `/tmp` and `/var/tmp` from
`PrivateTmp` (invisible to other units and removed by systemd when the unit stops) and the host's
`/dev/shm` from `PrivateDevices`. Neither unit requires the other. `valley-web` starts after the
receiver and keeps serving while the receiver restarts. Each unit has its own `MemoryMax` with no
swap.

On a host that does not run NixOS, the `valley-web-image` package builds a container image with Nix
(`nix/packages.nix`). It contains `valley-web`, `valley-web-receive`, and the `git` binary both run.
Its `/etc/passwd` and `/etc/group` define users `valley-web-receive` (uid 990) and `valley-web` (uid
991), both in group `valley-web` (gid 990). Its volume directory, `/var/lib/valley-web`, is owned by
the receiver's user with mode `0750` and contains `browser/`, owned by `valley-web` with mode
`0700`. The image defaults to user `valley-web` and defines no entrypoint or default command. An
edge runs two containers from this image sharing the state volume: the receiver runs as
`valley-web-receive` with the state volume writable, and `valley-web` runs as `valley-web` with the
state volume read-only and a second writable volume mounted at `/var/lib/valley-web/browser` (see
[Run the web edge](./guide.md#run-the-web-edge)). How an edge runs the containers, where they
listen, how the token reaches them, and each container's memory and swap limits are **deployment
choices**.

Checked by:

- `web-receiver-unit`, which runs the receiver as a separate process over the shared fixture and
  real `git`:
  - `TestAnUploadActivatesAndValleyWebServesIt` runs each step of an upload with the real
    `valley-web` serving the activated export, and checks that replaying the manifest or commit is
    refused.
  - `TestAnUnauthenticatedRequestIsRefused` checks that requests without a valid token are refused
    before anything is staged.
  - `TestAManifestIsAdmittedOnlyWhenFresh` checks that stale, future-dated, foreign-key, and
    foreign-destination manifests are refused.
  - `TestABodyIsKeptOnlyWhenItVerifies` checks that invalid bodies are refused, a concurrent upload
    of the same body receives `503`, and an interrupted body upload leaves the active export
    unchanged.
  - `TestAnOversizedUploadIsRefused` and `TestAnUploadPastTheStorageCapIsRefused` check that uploads
    exceeding `--max-body`, `--max-upload`, or `--max-storage` (including a manifest naming one
    bundle for eight projects) are refused while uploads within the caps succeed.
  - `TestASaturatedUploadLeavesValleyWebServing` holds all request slots with stalled manifests and
    then holds all connections with incomplete headers while `valley-web` serves every page, and
    checks that a body upload completes once the header timeout closes the stalled connections.
  - `TestOptionsStarHoldsNoConnection` sends `OPTIONS *` with a withheld body on every connection
    slot and checks that each is answered immediately and an upload succeeds.
  - `TestAManifestStopsWaitingWhenItsClientGoesAway` checks that a manifest waiting on the store
    lock stages nothing after its client disconnects.
  - `TestANewerManifestStopsTheOlderUploadsBodies` checks that staging a newer manifest stops a
    stalled body upload of the older manifest with `superseded`, leaves no partial file open, and
    accepts the newer manifest's upload of the same body.
  - `TestAReleasedGenerationMakesRoomForAnUpload` checks that a manifest is refused for space while
    a reader holds a large old generation and is accepted once the reader releases it.
  - `TestStopsOnSIGTERM` checks that `SIGTERM` stops the receiver when all connections are held. It
    allows the 10-second stop and 5-second drain timeouts plus 5 seconds of tolerance, 20 seconds in
    all, and waits up to 25 seconds for the receiver to exit.
  - `TestACommitWhoseClientGoesAwayStopsItsGit` and `TestAStopWaitsForTheGitACommitRuns` run a `git`
    that stalls in `commit-graph` as a shell with a forked child that holds `git`'s output open.
    They check that a client disconnecting during a commit, and a `SIGTERM` during one, each kill
    both the shell and its child. After the disconnect, the bundle stays staged and the commit made
    again activates the upload. After the `SIGTERM`, the receiver exits only once the commit has
    ended.
- `web-unit`, which tests `Store.Stage` and `Store.Activate` lock cancellation
  (`TestStoreWaitsForItsLockUntilTheContextIsDone`), a capacity sweep that stops once its context is
  done (`TestStoreSweepStopsWithItsContext`), stopping active body writers on discard and commit
  (`TestStoreStopsTheWritersOfDiscardedBodies`), post-build storage enforcement
  (`TestStoreHoldsActivationToMaxStorage`), and end-to-end publication from `valley-web-publish` to
  the real receiver and `valley-web` including wrong-token refusal and grant withdrawal
  (`TestPublishesToTheRealReceiver`).
- `module-eval`, which checks the users, file modes, credentials, memory caps, and publisher IP
  address filter rendered by the NixOS modules, and checks that evaluation fails when the publisher
  has no allowed address; `web-publisher-e2e`, which checks in a NixOS VM that the token reaches the
  publisher unit as a credential while the source file stays readable only by root, and that the
  unit can connect to a listener on its allowed address while connections to disallowed IPv4 and
  IPv6 listeners fail (even though root can reach them); and `web-image`, which unpacks the
  container image and checks its programs, users, volume, and default user.
- `web-push-e2e`, which runs the full push path in the build sandbox as a single user using the
  packaged `valley-web-publish`, `valley-web-receive`, `valley-web` (as built and wrapped by
  `nix/packages.nix`), `valley` CLI, and `git`. It verifies that a pass publishes two projects that
  `valley-web` serves at their group URLs, that a short alias and a former name redirect with `308`,
  and that an upload with a wrong token fails as `unauthenticated` while the edge keeps serving. It
  then verifies that withdrawing a read grant removes the project on the next pass, that both
  servers preserve the active export across a restart, and that stopping the receiver leaves
  `valley-web` serving until the receiver starts again and the next pass applies the withdrawal.
  Because neither `module-eval` nor `web-image` runs the edge units or containers, runtime
  enforcement of the memory caps and sandbox on the edge is unchecked.

## The life of a change

1. An author pushes a topic branch. The hook accepts it, and the bus gets `ref-updated`. The mirrors
   never get the branch.
2. The author marks the branch's head ready with `valley ready`. That writes the change's record at
   `refs/the-valley/changes/main/<change-id>`, with the head as a version. `valley pending` and
   `valley changes --json` list the change by its handle.
3. The operator approves one head of the change: with `[a]pprove` in `valley review <handle>`, or
   with `valley approve <handle> --head <sha>` from a front end. Approval reuses eligible evidence
   `origin` already holds for that head's tree (see [The valley command](#the-valley-command)), runs
   the remaining required checks over that head, and in one push files any new evidence and the
   integration request `refs/the-valley/integration-requests/main/c-<change-id>` at that head. The
   hook verifies each note and the `approve` grant.
4. On its next pass the integrator reads the records. It refuses a request that would bring a
   version of a change that is not `ready`, and one whose commits hold a merge. Otherwise it builds
   the candidate, derives the policy, judges the evidence, and lands or refuses. If the author
   unreadies or withdraws the change before it lands, the request stays and does not land; a
   withdrawal also deletes it. Only a record written in the moment before the target moves is
   missed.
5. On a landing, the publisher sends `ref-updated` to the bus and pushes `main` and tags to the
   mirrors. `valley status <handle>` shows the outcome, and the version's landing evidence.

A branch no record is on is approved by its name and requested under it.

## What a deployment supplies

The host module installs the valley's services and hooks (`nix/valley-host.nix`). A deployment
supplies the host configuration, the project declaration and the secrets (**deployment choice**).
[Running a valley host](./guide.md#running-a-valley-host) walks through each step.

- **The host configuration.** The host's NixOS configuration imports
  `the-valley.nixosModules.valley-host` and points `services.valley.config` at `valley.cue`. It
  enables the services the host runs, such as the bus, the integrator and the identity compiler, and
  names the instance repository in `services.valley.integrator.instanceProject`. It can also declare
  static SSH keys in `services.valley.authorizedKeys` beside the keys the registry compiles.
- **The declaration.** `valley.cue` names the projects, the refs each one protects, their mirrors,
  and the backup policy. A backup is a restic run of the data directory over sftp. By default it
  runs nightly and keeps 7 daily, 4 weekly and 6 monthly snapshots (`schema/valley.cue`).
- **The secrets.** The deployment provisions each secret as a file outside the Nix store. These are
  the integrator's signing key (`services.valley.integrator.signingKeyFile`), the git user's ssh key
  for mirror pushes, and the four backup files (`services.valley.backup.*`): the repository URL,
  password, ssh key and known hosts. The build refuses a host whose declaration enables backup
  without all four backup files (`nix/valley-host.nix`).

## Not deployed or not wired yet

| What                                         | State                                                                                                                                                                                                                                                                              |
| -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `sigverify`                                  | On `main` and tested. Nothing calls it.                                                                                                                                                                                                                                            |
| Hardware-backed keys in the registry         | `identity compile` verifies YubiKey PIV attestations. No host service or gate uses them.                                                                                                                                                                                           |
| SSHSIG evidence                              | Attestations use the signed-note format.                                                                                                                                                                                                                                           |
| Approvals                                    | The integrator checks evidence only. No approval is required for any path class.                                                                                                                                                                                                   |
| Approval carry                               | `valley checks` reports a change's carry mode. Nothing acts on it.                                                                                                                                                                                                                 |
| The restacker                                | The host module runs it on each project listed (`services.valley.restacker`). It is off by default, and a deployment registers its principal.                                                                                                                                      |
| The check worker                             | The host module runs it on a timer (`services.valley.worker`). It is off by default.                                                                                                                                                                                               |
| The build slice                              | `services.valley.buildSlice` caps every build of the nix daemon. It is off by default.                                                                                                                                                                                             |
| The web server and export                    | `valley-web-publish` uploads exports, `valley-web-receive` activates them, and `valley-web` serves them (`web/`). `services.valley.webReceiver` and the `valley-web-image` package run the receiver and `valley-web` on an edge. Whether an edge runs them is a deployment choice. |
| Named readers at the edge                    | `valley-web-publish` exports each project a named principal may read, with that principal in its authorization. `valley-web` signs readers in and serves each session as its principal only when started with `--origin`. Whether an edge sets an origin is a deployment choice.   |
| Withdrawal without the instance and registry | The policy path withdraws a project without reading any content, but it signs nothing when it cannot read the instance repository or the identity registry. A project whose own policy cannot be read keeps its stored readers that a grant still covers.                          |
| Filesystem sandbox for the web publisher     | The unit's filesystem is read-only and named key paths are hidden, but the unit can read the host's other repositories, not only the declared ones.                                                                                                                                |
| Clones from the edge                         | `valley-web` serves no git clone route.                                                                                                                                                                                                                                            |
| Read grants over ssh                         | sshd lets every admitted key fetch every repository (`valleyhook/shell.go`). Read grants govern only web exports.                                                                                                                                                                  |
| Reviews                                      | `review/` defines the record and its history. The hook refuses every push to them.                                                                                                                                                                                                 |
| A crash-safe landing ledger                  | Landing is several separate ref updates.                                                                                                                                                                                                                                           |
| Policy and registry snapshot in the outcome  | The outcome record holds the head, the tip and an evidence hash.                                                                                                                                                                                                                   |
| Registry compile on an instance landing      | The compiler runs on its 5-minute timer only.                                                                                                                                                                                                                                      |
| Bus authentication                           | The bus listens on localhost with no authentication.                                                                                                                                                                                                                               |
