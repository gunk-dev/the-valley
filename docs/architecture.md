# Architecture as deployed

This document describes what the-valley runs today, read from the code. It makes three kinds of
statement. Behaviour is what the code does, cited to the files that do it. A deployment record
reports an observation of a host, and names the record and its date. A guarantee names the flake
check (`checks.x86_64-linux.<name>`) or test that demonstrates it; a statement with no named check
is unchecked. Security and rollouts are in [security.md](./security.md), planned work is in
[roadmap.md](./roadmap.md), and purpose and principles are in [purpose.md](./purpose.md).

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

The hold file belongs to the operator, who creates it to pause all pushes. `valley-init` never
touches it.

## What a push may write

Every repository's `pre-receive` hook runs `valleyhook pre-receive` (`valleyhook/main.go`,
`valleyhook/policy.go`). The hook is default-deny. If any one ref update is refused, the whole push
is refused. Checked by `protect-e2e`, `ssh-e2e` and `valleyhook-unit`.

| Ref namespace                                       | Who may push                                                                                     | Rule                                                                                                                                                                    |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refs/heads/*` (topic branches)                     | Any key                                                                                          | Create, update and delete.                                                                                                                                              |
| Protected refs (default `refs/heads/main`)          | Principals in `protection.writers` (default empty)                                               | The integrator moves them on disk. cosmo names `integrator` as a writer of both its projects.                                                                           |
| `refs/the-valley/attestations/<tree>/<key-hash>`    | Any key                                                                                          | Create only. The ref must name a tree of notes within size bounds, and each note must verify under a known signer and be about that tree (`valleyhook/attestation.go`). |
| `refs/the-valley/integration-requests/<target>/<c>` | Principals with the `request` grant                                                              | Create, update and delete.                                                                                                                                              |
| `refs/the-valley/changes/main/<change-id>`          | Any named principal creates; the owner or a `request` holder updates; a `request` holder deletes | A change record. The hook allows only the transitions in [Change records](#change-records) (`valleyhook/record.go`).                                                    |
| Every other `refs/the-valley/*`                     | Nobody                                                                                           | This covers the integrator's outcome records.                                                                                                                           |
| `refs/replace/*`, `refs/notes/*`                    | Nobody                                                                                           | A replace ref would let git show other content under a watched name.                                                                                                    |
| Symbolic refs                                       | Nobody                                                                                           |                                                                                                                                                                         |
| Tags and every other ref                            | Principals named in a project grant: `grants.<name>: {refs, writers}` (`schema/valley.cue`)      | Grant patterns cannot name `heads`, `replace`, `notes` or `the-valley`.                                                                                                 |

The `request` grant comes from two places: the host's declared `services.valley.grants.request` and
the identity compiler's `grants` file. Why replace refs are closed is recorded in
[bd-75c8721](../archive/.the-valley/bugs/bd-75c8721-pushes-could-write-any-namespace.md).

### Change records

A change is one piece of work aimed at `main`. Its ID is stable across rebases, in jj's form: 32
letters from `k` to `z`. Each time a head of the change is marked ready, the change gains a version.
The change's record lists the versions, and the hook guards every write to it. Records are
observational: nothing reads them for authority. The integration request is still the only way a
change reaches `main`, and the integrator does not read records.

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
is 1 to 200 bytes with no control characters. `state` is `ready` or `withdrawn`.

The record commit's parents tie it to what it records. In the table, `P` is the record commit's
parents, `old` is the ref's value before the push, and `h` is the head of the version being added.

| Transition  | Who                             | `P`        | What the record must say                                                                                                                                                                            |
| ----------- | ------------------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Create      | Any named principal             | `[h]`      | One version: `n` 1, `head` `h`, and the pusher as `uploader`. The pusher as `owner`, `seq` 1, `active` 1, `state` `ready`. `target` and `change_id` are the ref's.                                  |
| New version | The owner or a `request` holder | `[old, h]` | `old`'s versions unchanged, plus one with the next `n`, `head` `h` other than the active head, and the pusher as `uploader`. `active` is the new `n`, `state` `ready`, `seq` one more than `old`'s. |
| Withdraw    | The owner or a `request` holder | `[old]`    | Everything as in `old`, except `state` goes from `ready` to `withdrawn` and `seq` goes up by one. `active` stays.                                                                                   |
| Ready again | The owner or a `request` holder | `[old, h]` | As for a new version. A withdrawn change comes back only at a new head.                                                                                                                             |
| Delete      | A `request` holder              | —          | Allowed, to keep storage tidy.                                                                                                                                                                      |

The hook refuses every other write. That includes a key that names no principal, extra parents,
parents in another order, a head that is not a commit, a tree of another shape, and `ready` to
`ready` without a new version. The owner, the target and the change ID never change.

Each record commit is at most 4 KiB, its tree at most 1 KiB, and its `record.json` at most 64 KiB. A
push may update at most 32 records, and a repository may hold at most 2000, counted with one
`for-each-ref`. The hook checks each bound before it reads anything past it.

The writer numbers versions, and the hook holds them to append-only and gap-free. When the
integrator starts reading records, it takes this numbering as it is. For now, a change is owned by
the principal whose key pushed its first record, so `klaus-env` owns every agent change.

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
and all tags to each URL. After that it deletes every other branch on the mirror. Tags are copied
whether or not `main` reaches them, refs outside `refs/heads/` and `refs/tags/` on the mirror are
left alone, and the branch deletion is best effort. The push uses the git user's own ssh identity,
which the host provides. Checked by `mirror-e2e`.

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
   that points at the change's head commit.
2. **Skip what is decided.** The outcome record
   `refs/the-valley/integration-outcomes/<target>/<change>` holds the head, the target tip, a hash
   of all attestation refs, and the outcome. If the first three still match, the request is skipped.
   A `failed` outcome is retried after 10 minutes.
3. **Build the candidate.** The base is `git merge-base` of the tip and the head. If the base is the
   tip, the candidate is the head. Otherwise `git merge-tree --write-tree` builds the tree, and the
   integrator commits it with the tip as the only parent. The commit keeps the head's author. Its
   committer is the integrator at `integrator@the-valley.invalid`. A conflict makes the request
   stale.
4. **Derive the required checks** (see Policy below).
5. **Judge the evidence** (see Evidence below). The outcome is `land`, `stale` or `reject`. A stale
   request publishes `request-stale`.
6. **Land.** The integrator signs a transfer statement that records the policy digest and the checks
   it relied on. Then it moves the target with compare-and-swap
   (`git update-ref <target> <new> <tip>`). That is the commit point. Then it queues the landing for
   the publisher, stores its countersigned evidence, deletes the request with compare-and-swap,
   publishes `integration-succeeded`, and writes the outcome record.

Each step is a separate `git update-ref` call. A crash between steps can leave a landing without its
evidence or outcome record. Checked by `integrator-e2e` (scenarios for fast-forward, merge,
conflict, stale evidence, tampered notes, retries and a moved target) and `integrator-unit`.

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
(`integrator/policy.go`). A path class maps path globs to checks. A change owes the union of the
checks of every class one of its paths matches. A path no class matches owes the `unclassified`
checks. The integrator gets this answer by running `valley checks`, which diffs with renames counted
on both sides. Checked by `policy-deriver` and `cue-vet`.

The derivation fails closed. A failed read of the diff, the target, a policy layer or the composed
policy makes `valley checks` exit non-zero and print no report. Without that, the failure would read
as an empty list, and an empty list derives a change that owes nothing. The integrator also refuses
output that is not a whole report: no header, no required-checks section, or a required-checks
heading with no checks under it. It believes a report of no changed paths only when git agrees that
the two trees are the same. Checked by `policy-deriver`, `valley-request` and `integrator-unit`.

A check also declares a `cost`, `quick` by default. A `slow` check takes minutes, or needs special
hardware such as KVM. `valley checks` reads the cost from the composition it derives from. After the
report it prints two lines, `quick:` and `slow:`, each listing the owed checks of that cost.
`valley checks --names` prints the owed check names alone, one per line, and `--cost quick` or
`--cost slow` keeps one set. The integration gate does not read the cost. Approving runs every owed
check, and the integrator requires evidence for every owed check, whatever its cost. The cost
changes neither which checks a change owes nor what the integrator accepts. Checked by
`policy-deriver` and `integrator-unit`.

the-valley's own project layer is `policy/project/policy.cue`. It gives each part of this repository
a class that owes the checks reading that part. A Go module owes its unit tests, and its end-to-end
check where it has one. The CLI owes its script and status checks. The Nix, the schemas and the
policy owe their evaluations and vets. The NixOS module also owes the end-to-end checks that run
what it renders, and two of those boot NixOS machines. A change to `flake.nix` or `flake.lock` does
not owe them, so a nixpkgs bump boots no machine. A check's own files, such as its script under
`nix/checks/`, owe that check. A change to documentation alone owes the floor's `prose-format` and
nothing else. A project layer that stopped composing would stop every derivation once it landed,
including the derivation of its own fix. So the `own-policy` check composes the layer with the
example floor before it lands. It fails if the layer names a check this flake does not define, if a
script under `nix/checks/` owes no check of its own, or if a docs-only change would owe more than
`prose-format`.

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
`ready` and `withdraw` verbs are a Go program, `valley-go` (`status/`), which the package puts on
the script's `PATH`.

| Command                      | What it does                                                                                          |
| ---------------------------- | ----------------------------------------------------------------------------------------------------- |
| `valley pending`             | Lists branches not merged into `main`, each with its head commit's subject.                           |
| `valley review <b>`          | Shows a branch and offers `[a]pprove`, `[r]eject` and `[s]kip`, plus `[b]ase` when it needs a rebase. |
| `valley approve <change>`    | Does what `[a]pprove` does, without showing the branch.                                               |
| `valley rebase <change>`     | Does what `[b]ase` does, without showing the branch, and prints the new head.                         |
| `valley checks`              | Reports the checks a change owes, grouped by declared cost. It runs nothing.                          |
| `valley status [--json] <c>` | Reads the request and outcome record for a change. `--json` prints the `valley-status/v1` object.     |
| `valley ready [<branch>]`    | Marks a head of the branch ready as a new version of its change, in the change's record.              |
| `valley withdraw <change>`   | Marks a ready change withdrawn, as its owner or the operator.                                         |
| `valley tail [subj]`         | Subscribes to bus events.                                                                             |
| `valley replay [dir]`        | Publishes one `ref-updated` event per current ref.                                                    |

`[a]pprove` is the operator's approval of a change. It runs the owed checks with `attest` in a
worktree of the branch head. Then one atomic push carries the evidence refs and the integration
request, with a lease on the request ref. When the request already names this head, the push leaves
the request ref out. When nothing is owed as well, there is no push. `[b]ase` rebases the branch in
a temporary worktree and pushes it with a lease. `[r]eject` deletes the remote branch after a
prompt.

`valley approve` and `valley rebase` are for front ends, such as the klaus dashboard, that show a
change their own way. Each runs the same function as its key, so the output and the refusals are the
same. `approve` exits non-zero when it refuses. `rebase` does not re-review the result, and it
leaves a branch that already fast-forwards from `main` alone. Checked by `valley-cli`,
`valley-request`, `valley-status`, `valley-ready` and `review-notes`.

`valley ready [--head <sha>] [--change <id>] [--stamp] [<branch>]` writes a change's record
(`status/ready.go`). The branch is the current one unless named. The head is `--head` when given,
and otherwise the branch's head on origin. Either way the head must already be on origin. klaus
passes `--head` with the head it pushed, so the version names exactly that head. ready reads the
existing record, if there is one, and adds a version. It refuses a head equal to the active
version's. It pushes the record ref with a lease on the value it read, and prints the handle and
version, such as `cosmo nxvp v2 ready`. The record names the principal from
`git config valley.principal`. The host derives the pushing principal from the key, and the client
cannot ask it, so the hook refuses a record that names anyone else.

ready takes the change ID from the first of these that has one:

1. `--change <id>`;
2. the head commit's `change-id` header, as jj writes it, read from the commit object itself;
3. a `Change-Id:` trailer in the head's message;
4. a new ID, which it prints.

A header and a trailer that disagree are refused, and so are malformed values. IDs on other commits
of the change are not read. ready also refuses a change whose commits, from the merge base with
`main` to the head, include a merge, or that has more than one merge base. `--stamp` amends the
local branch's head to carry the ID as a trailer, and pushes the branch with a lease. It refuses a
head that is already on origin, unless the change's record names the user as owner.

A change is named by its full ID or by its handle. The handle is the shortest prefix of the ID, at
least four letters, that no other change recorded on origin shares. A change can also be named by
its branch, as it was before records existed. Every verb that takes a change takes all three
(`status/changes.go`). `review`, `approve` and `rebase` act on the branch that the change's active
version names.

`valley status` takes one `ls-remote` snapshot of origin and fetches the objects it names, without
moving a local branch or writing `FETCH_HEAD`. It reads ancestry first: a head already in
`origin/main` is integrated, even if recording the outcome failed. An outcome record it does not
recognise is `unknown`, never a success.

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
  "state": "stale"
}
```

- `change` is the branch as given, without `origin/`. `request_name` names its integration request:
  the branch with each `/` replaced by `-`.
- `target_tip` is `main` on origin. `head` is the branch head on origin. When the branch is gone,
  `head` is the request's commit, or else the outcome's head, or else `null`.
- `request` is `null` when origin holds no integration request for the change. `outcome` is `null`
  when origin holds no outcome record.
- `outcome.decision` is `land`, `stale`, `reject`, `failed` or `unknown`. A record that does not
  parse is `unknown`. `outcome.head` and `outcome.tip` are `null` unless they are object ids, and
  `failed_at` is `null` unless the decision is `failed`.
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
| `failed`                | The integrator failed while processing the request. The change may still have landed. |

An error prints no object. The message goes to stderr and the exit status is non-zero. The errors
are an unreadable origin, an origin without `main`, nothing on origin for the change, and a change
name that is not UTF-8, which JSON cannot carry exactly. Checked by `valley-status`, and by the Go
tests in `status/` for the outcome record's parser.

## The life of a change

1. An author pushes a topic branch. The hook accepts it, the bus gets `ref-updated`, and the mirrors
   prune it.
2. The operator approves the change, with `[a]pprove` in `valley review <branch>` or with
   `valley approve <branch>`. The owed checks run, and one push files the evidence and the
   integration request. The hook verifies each note and the `request` grant.
3. On its next pass the integrator builds the candidate, derives the policy, judges the evidence,
   and lands or refuses.
4. On a landing, the publisher sends `ref-updated` to the bus and pushes `main` and tags to the
   mirrors. `valley status <branch>` shows the outcome.

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
