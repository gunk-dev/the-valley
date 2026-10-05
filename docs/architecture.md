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

| Ref namespace                                       | Who may push                                                                                | Rule                                                                                                                                                                    |
| --------------------------------------------------- | ------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refs/heads/*` (topic branches)                     | Any key                                                                                     | Create, update and delete.                                                                                                                                              |
| Protected refs (default `refs/heads/main`)          | Principals in `protection.writers` (default empty)                                          | The integrator moves them on disk. cosmo names `integrator` as a writer of both its projects.                                                                           |
| `refs/the-valley/attestations/<tree>/<key-hash>`    | Any key                                                                                     | Create only. The ref must name a tree of notes within size bounds, and each note must verify under a known signer and be about that tree (`valleyhook/attestation.go`). |
| `refs/the-valley/integration-requests/<target>/<c>` | Principals with the `request` grant                                                         | Create, update and delete.                                                                                                                                              |
| Every other `refs/the-valley/*`                     | Nobody                                                                                      | This covers the integrator's outcome records.                                                                                                                           |
| `refs/replace/*`, `refs/notes/*`                    | Nobody                                                                                      | A replace ref would let git show other content under a watched name.                                                                                                    |
| Symbolic refs                                       | Nobody                                                                                      |                                                                                                                                                                         |
| Tags and every other ref                            | Principals named in a project grant: `grants.<name>: {refs, writers}` (`schema/valley.cue`) | Grant patterns cannot name `heads`, `replace`, `notes` or `the-valley`.                                                                                                 |

The `request` grant comes from two places: the host's declared `services.valley.grants.request` and
the identity compiler's `grants` file. Why replace refs are closed is recorded in
[bd-75c8721](../archive/.the-valley/bugs/bd-75c8721-pushes-could-write-any-namespace.md).

## After a push: events and mirrors

Each repository's `post-receive` hook runs every program in `hooks/post-receive.d/`
(`nix/valley-host.nix`). There are two:

- **The bus publisher**, when the bus is on. It publishes one `ref-updated` event for every ref the
  push updated, on the subject `valley.git.<repo>.ref-updated`. Checked by `bus-e2e`.
- **The mirror hook**, when the project declares mirrors. It does not push. It writes the push's ref
  moves, one `<old> <new> <ref>` line each, as a file in the repository's publish queue,
  `valley-publish-queue/`, and returns. A dead mirror therefore never slows or fails a push.

The integrator moves refs with `git update-ref`, which runs no `post-receive` hook. So it does both
jobs itself for each landing (`integrator/bus.go`). It publishes the `ref-updated` event straight to
the bus, with the same payload the bus publisher would send. It also writes the move to the same
publish queue.

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

`bin/valley` is a shell script, also packaged with its schema (`nix/packages.nix`).

| Command               | What it does                                                                                      |
| --------------------- | ------------------------------------------------------------------------------------------------- |
| `valley pending`      | Lists branches not merged into `main`.                                                            |
| `valley review <b>`   | Shows a branch and offers `[a]sk`, `[r]eject` and `[s]kip`, plus `[b]ase` when it needs a rebase. |
| `valley checks`       | Reports the checks a change owes. It runs nothing.                                                |
| `valley status <b>`   | Reads the request and outcome record for a branch.                                                |
| `valley tail [subj]`  | Subscribes to bus events.                                                                         |
| `valley replay [dir]` | Publishes one `ref-updated` event per current ref.                                                |

`[a]sk` runs the owed checks with `attest` in a worktree of the branch head. Then one atomic push
carries the evidence refs and the request ref, with a lease on the request. When the request already
names this head, the push leaves the request ref out. When nothing is owed as well, there is no
push. `[b]ase` rebases the branch in a temporary worktree and pushes it with a lease. `[r]eject`
deletes the remote branch after a prompt. Checked by `valley-cli`, `valley-request`, `valley-status`
and `review-notes`.

## The life of a change

1. An author pushes a topic branch. The hook accepts it, the bus gets `ref-updated`, and the mirrors
   prune it.
2. The operator runs `valley review <branch>` and chooses `[a]sk`. The owed checks run, and one push
   files the evidence and the request. The hook verifies each note and the `request` grant.
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
