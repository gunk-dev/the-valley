# Security and rollouts

This document describes how the-valley protects the hosts it runs on and the hosts that deploy what
lands on it. It follows the path a change takes, and names the gate at each step and its status. The
components are described in [architecture.md](./architecture.md), and the planned gates live in
[the plan graph](./concepts.md#records-project-storage-the-plan-graph-and-knots) (`valley plan`).

The document makes three kinds of statement:

- **Behaviour** is what the code does, cited to the files that do it. It is labelled `implemented`
  where needed. A guarantee names the executable check that demonstrates it. A statement with no
  named check is unchecked, and says so.
- **A deployment choice** is what a deployment configures or must supply. It is labelled
  **deployment choice**, and the code does not enforce it.
- **A gate that is not built** has a design but does not work yet. It is labelled **not built**.

The main threat is an AI agent acting as the operator. So the gates must hold even when an agent
ignores its instructions. The human act of authority is a touch on a hardware key. An approval gate
that the gated agent can administer is only a setting that agent controls
([ida-b7025b5](../archive/.the-valley/ideas/ida-b7025b5-human-decisions-are-signed-acts.md)). A gate
counts as done only when a check has shown it refusing what it should refuse
([oc-9949561](../archive/.the-valley/outcomes/oc-9949561-push-replication.md)).

## The path a change takes

| Step | Gate                                              | Status            |
| ---- | ------------------------------------------------- | ----------------- |
| 1    | Agents are not the operator                       | Deployment choice |
| 2    | Only the operator can file an integration request | Implemented       |
| 3    | Filing takes a touch                              | Not built         |
| 4    | Hosts deploy only what the operator signed        | Not built         |
| 5    | A failed rollout rolls back or holds              | Deployment choice |

### 1. Agents are not the operator

Agents must never act with the operator's identity or keys. The-valley names every pusher by its
key, so each agent can have an identity of its own. The deployment keeps the agents apart from the
operator's account, keys and secrets (**deployment choice**).

- **The boundary.** A deployment runs its agents outside the operator's Unix account, in a boundary
  such as a VM with its own kernel. The boundary has no host directory shares and no operator
  secrets. An agent is assumed to have root inside the boundary.
- **Their own identities.** Agents push with their own keys under their own principal, with a push
  grant and no `approve` grant (**deployment choice**). The hook takes the principal from the key
  that logged in (`valleyhook/shell.go`). Like any admitted key, an agent's key can push topic
  branches and create attestation refs. No key can create or update a ref named like another ref,
  such as a branch whose name begins with `refs/`, because git reads, fetches and pushes to such a
  ref in place of a missing one (`implemented`, `valleyhook/policy.go`; checked by `protect-e2e` and
  `valleyhook-unit`). The hook accepts an attestation ref only when every note in it verifies under
  a known signer (`valleyhook/policy.go`, `valleyhook/attestation.go`). An agent's key can also
  create change records, which makes the agent's principal the owner of those changes, and add
  versions to them (`valleyhook/record.go`). A record never makes anything land. The integrator
  reads records only to refuse a request or to void a withdrawn change's requests
  (`integrator/records.go`). Any principal that can create a record can hold back a head by
  recording it in a change and unreadying that change. Only an `approve` holder may delete that
  record, and deleting it releases the head.
- **Caps.** The deployment caps the boundary's CPU, memory and I/O, so agents cannot starve the
  valley host.
- **Egress.** The boundary reaches only what agents need, such as name resolution, ssh to the valley
  host and HTTPS to the public internet. Everything else is dropped: private and other
  special-purpose address ranges, other ports on the host, and spoofed sources.
- **ssh into the valley host.** From the agents' network, sshd admits only the valley git user, with
  no forwarding, TTY or tunnel. The host module's `Match User git` block turns off TCP, agent and
  X11 forwarding and tunnels, and leaves stream-local forwarding on (`nix/valley-host.nix`). The
  deployment adds `DisableForwarding yes` and `PermitTTY no` to close the rest (see
  [Repositories and the git user](./architecture.md#repositories-and-the-git-user)).
- **Tools that follow `main`.** `valley self-update` can keep an agent's `valley` at the tip of
  `main`, through the flake's home-manager module (see
  [Version and self-update](./architecture.md#version-and-self-update)). It installs only a commit
  that has landed, built from the exact commit `git ls-remote` named, once it passes a smoke test.
  The valley host and the machine the operator approves from must not run it. Their `valley` stays
  at the version their system configuration pins, so a landing never changes the tool that approves
  or admits the next change.

Checks: `ssh-e2e` runs the git user's sshd block behind a real sshd. No check in this repository
exercises a deployment's boundary, caps or egress. A deployment that runs agents needs its own
checks for them, with negative controls that show each forbidden connection refused.

### 2. Only the operator can file an integration request

The push hook refuses an integration-request ref unless the pushing principal holds the `approve`
grant (`valleyhook/policy.go`). The grant is also accepted as `request`: a principal holding either
name holds it (`valleyhook/grants.go`). The principal comes from the key that logged in, and a
client cannot set it (`valleyhook/shell.go`). Checks: `protect-e2e`, `identity-e2e`, and `ssh-e2e`,
which uses test identities, a grant holder and a non-holder, behind a real sshd.

Who holds the grant is a **deployment choice**. A deployment that gives it to the operator's
principal alone makes the operator the only one who can file. Every key of that principal then holds
the grant.

The security invariant is the operator's approval: only content the operator approved lands. The
request names the exact head that was approved, and the integrator lands only what a request names
(`integrator/refs.go`). Checks: `integrator-e2e` and `valley-ready`.

The `restack` grant is the host restacker's, and it files nothing. The hook lets a holder that
neither owns a change nor holds `approve` add exactly one version to the change's record, while the
change is `ready`. The version keeps the branch and title of the active one, and names the holder as
its uploader. A holder may also write restack reports (`refs/the-valley/restack/main/*`). The grant
authorises no other record write, no record delete, and nothing on integration requests
(`valleyhook/record.go`, `valleyhook/report.go`). Checks: `protect-e2e` and `identity-e2e`.

A `restack` holder is still a named principal, and keeps every ordinary power of one. So a
compromised restacker key can:

- rewrite or delete unprotected branches;
- add arbitrary versions to other owners' ready changes;
- create and manage its own records, and write valid plan-graph updates;
- publish misleading reports, consume storage, exhaust a record's capacity, or withhold work.

It cannot write a protected `main`, file, replace or withdraw a request, write an integrator
outcome, or forge a check signature. It can relay evidence someone else signed, as any key can. The
hook proves who uploaded a version and nothing about its content: not that it is a rebase of the old
head, holds only the change's content, keeps its message and author, or sits on `main`. What
protects the operator is approval. A request names the exact head the operator approved, so a
version the restacker added lands only once the operator approves that head. Review compares a
version with its approved version from git objects, and never trusts a report's account of it (see
the architecture's
[Review against the approved version](./architecture.md#review-against-the-approved-version)).

On a host, the restacker runs as the Unix user `valley-restacker` in its own group
`valley-restacker` (`nix/valley-host.nix`). That user has no filesystem access to the served
repositories. It connects to them only over local SSH as its principal, so every write it makes goes
through the hook. It holds no integrator key, no worker key, and no signing name, so it cannot sign
evidence or a landing. Its own private key is provisioned outside the Nix store and passed to the
service by systemd as a credential (`LoadCredential`). Enrolling the principal, granting it
`restack`, and provisioning the private key are a **deployment choice**. Check: `restack-host-e2e`.

Readiness is different. A change record's state is the author's workflow signal, and the integrator
enforces it as closely as refs allow. It does not enforce it atomically. A record written between
the integrator's last read of the records and its move of the target can be missed. That can land a
head the operator approved and the author has since paused. It can never land content the operator
did not approve. The race-free design binds each approval to the record's withdraw generation in a
signed approval statement (**not built**).

### 3. Filing takes a touch (not built)

This gate is **not built**. Its design gives the `approve` grant to hardware keys that need a touch
for every signature, and verifies each filing with `sigverify`. Until it is built, filing a request
needs the operator's principal and no touch. `sigverify` itself is checked by `sigverify-unit` and
`sigverify-e2e`, including a touchless-signature negative test. Enrolling a hardware-backed key is
`implemented`: the registry schema accepts `bound: "hardware"` with an attestation, the identity
renderer verifies the attestation, and it lists qualifying keys in the hardware approval-signers
output. Enforcing a touch when an approval is filed is **not built**.

### 4. Hosts deploy only what the operator signed (not built)

A host that deploys what lands on the valley should activate only a release the operator signed. The
design has each such host verify a signed, sequenced release tag before it activates anything, and
never track `main`. `sigverify` verifies such a tag (`implemented`, checked by `sigverify-unit` and
`sigverify-e2e`). Nothing calls it yet. The deployment supplies the host-side caller, which must
meet [sigverify's caller's contract](../sigverify/README.md#the-callers-contract) (**not built**).

Until a host verifies signed releases, a host that builds and activates the tip of `main` deploys
whatever lands, without checking who made it (**deployment choice**). Nothing bounds the delay
between a landing and its activation.

### 5. A failed rollout rolls back or holds

The-valley does not deploy the hosts that use what lands. A deployment's own activation step does,
and it must roll back or hold when a rollout fails (**deployment choice**):

- It checks a new system's health before it keeps the system.
- On a failure, it switches back to the running system. It does not apply the failed revision again
  until the operator clears it.
- When it cannot switch back, or a run is interrupted, it holds for the operator.
- A change that needs a reboot is checked after the reboot. When that check fails, the last good
  system is staged back for the next boot.

On the valley host itself, the-valley holds pushes while its own configuration settles.
`valley-init` deletes its converged record before it links the hooks, and `valleyhook shell` refuses
a push while that record does not match the running configuration (`nix/valley-host.nix`,
`valleyhook/shell.go`). The operator can also hold every push with a hold file. Checked by
`init-e2e` and `ssh-e2e` (see
[valley-init and the push hold](./architecture.md#valley-init-and-the-push-hold)).

## Threat model

| Threat                              | Response                                                                                                                                                                                                                                            |
| ----------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| An agent acting as the operator     | Agents run with their own principals and no operator keys (step 1). The push hook scopes refs by principal (step 2).                                                                                                                                |
| An interactive coordinator          | An agent that directs other agents runs with them, under an agent's principal. Only a verified touch stops an agent that holds a grant, and that gate is not built (step 3).                                                                        |
| A compromised restacker             | Its `restack` grant adds versions to ready changes and writes reports, never requests. What it adds lands only once the operator approves that head (step 2). Its Unix user reaches the repositories only through the hook.                         |
| A compromised agent                 | Assume it runs arbitrary commands inside its boundary. The boundary holds no production secrets and no signing sockets, and its egress is limited (step 1).                                                                                         |
| A broken but honest change          | A landing needs a signed statement that each required check passed. A failed activation rolls back or holds (step 5). A human signature does not prove correctness.                                                                                 |
| A lost hardware key                 | An offline backup hardware key holds every role (see [Trust roots](#trust-roots)).                                                                                                                                                                  |
| The valley host compromised or down | Hosts that deploy from it keep their current systems. A compromised valley host can move `main`, so recovery that does not depend on it needs signed releases (step 4).                                                                             |
| A reader of the edge                | Reads from an export only the projects that both consents in that pass authorize `anyone` to read. A pass omits every other project from the export and bundles it builds (see [Who can read a project](#who-can-read-a-project)).                  |
| A stranger reaching the receiver    | The receiver refuses the request as `unauthenticated` before reading the request body, without taking a request slot. A connection flood can still delay uploads while `valley-web` keeps serving (see [The edge's receiver](#the-edges-receiver)). |
| A stolen receiver token             | The holder can spend the receiver's capacity up to its caps. Serving a new export still requires a manifest signed by the export key (see [The edge's receiver](#the-edges-receiver)).                                                              |

## Refs are read by their exact names

Each program listed below reads a ref by its exact name (`implemented`). `git rev-parse`, and every
git command given a ref name, does not stop at the name it is given. For a missing ref, git tries
other names in turn, among them `refs/tags/<name>` and `refs/heads/<name>`. A branch named
`refs/heads/registry` is stored as `refs/heads/refs/heads/registry`. A program that asked git for a
missing `refs/heads/registry` would read that branch.

The valley host's hook refuses to create or update a ref named like another ref (`implemented`,
`valleyhook/policy.go`). A host repository can hold one made before the hook refused them, and a
repository the hook does not guard, such as a local clone, can hold one at any time.

So each of these programs reads a ref once, by its exact name, and from then on reads the object the
ref held. It reads a local ref with `git show-ref`, and a remote one from the `git ls-remote` line
that names it. A missing ref is missing:

- The identity compiler fails when the instance repository has no registry ref, and the last good
  files stay in place (`identity/registry.go`).
- The integrator refuses a request named for a change that has no record. It does not judge a
  request whose target is missing, and a withdrawn change does not void such a request. A request
  with no memo has none, and the signers publication is written when there is none yet. It reads
  evidence through the objects its refs held when it listed them. A request that is gone when the
  integrator lands its head is not reported as moved, and the landing's transaction does not touch
  it (`integrator/`).
- The integrator reads the floor at `--instance-ref` (`composed/composed.go`). The restacker takes
  only the `git ls-remote` line that names that ref exactly (`restack/main.go`). An instance
  repository with no such ref has no floor, and the pass is refused.
- The web publisher reads the instance repository's `main`, its project list and its registry ref
  with `readaccess.ResolveRef`. It refuses an instance repository with no `main` or no project list.
  It reads a project's `main` the same way, at the object it staged
  (`web/cmd/valley-web-publish/valley.go`).
- `valley-read-coverage` reads the registry ref, the project list and each project's `main` with
  `readaccess.ResolveRef` (`readaccess/cmd/valley-read-coverage/main.go`).
- The check worker reads its own evidence ref exactly, and fetches the floor at the commit the
  owning valley's `refs/heads/main` names (`status/worker.go`). `valley ready --stamp` finds no
  local branch when the one it names is missing (`status/ready.go`).
- `valley review`, `valley approve` and `valley checks` read `origin/main` and the change's branch
  by their exact names (`bin/valley`). approve reads `origin/main` once, and reads the merge base
  and both policy layers at that commit. The floor is fetched at the commit the owning valley's
  `refs/heads/main` names. A request ref missing on origin is missing, whatever a branch named like
  it holds.

The identity compiler, the integrator and the restacker refuse a configured ref that is not a full
ref name, such as `main`.

`git push` matches the ref it writes the same way. With `<ref>` missing on origin, a push to `<ref>`
writes a branch named `<ref>` instead, and with both present git refuses the push as ambiguous.
`valley approve` reads origin before it pushes. While origin holds a ref whose name ends in the
request ref's or an evidence ref's name, approve pushes nothing and names that ref (`implemented`).
On the valley host, the hook refuses such a push rather than writing it, because the ref it would
write is named like another ref (`implemented`, `valleyhook/policy.go`). The other programs that
push do not check origin for such a ref before they push (**not built**).

Checks: `identity-e2e` compiles with `refs/heads/registry` missing and a forged registry at
`refs/heads/refs/heads/registry`, and the compilation fails with the files unchanged.
`integrator-unit`, `restack-unit`, `web-unit`, `readaccess-unit`, `valley-ready` and
`valley-request` each make the ref git would read in place of a missing one, and show that the
program ignores it. `integrator-unit` deletes an evidence ref after the integrator lists it.
`valley-request` approves with a branch on origin named like the request, with a tag named like the
remote-tracking ref of `main` or of a branch that origin lacks, and with an owning valley whose
`main` is missing. It also runs the check worker with a tag named like the worker's own evidence
ref. `protect-e2e` and `valleyhook-unit` check the hook's refusal.

## Trust roots

The operator's trust roots are hardware keys that the deployment supplies (**deployment choice**):

- **Two hardware keys**, a primary and an offline backup. Each holds every role: an approve key that
  needs a touch, and a release signing key that needs a PIN and a touch. No gate uses these keys
  yet, because the gates of steps 3 and 4 are **not built**.
- **An offline root for trust-root changes**, such as a Tillitis TKey. `sigverify` has a
  `tkey-signer` class for it (`implemented`). Nothing uses one for trust-root changes yet (**not
  built**).

## What a client trusts to read a landing

A client decides that a version landed through a commit the integrator wrote (`landed-recorded`) by
verifying the integrator's transfer statement. It verifies the statement with a known-signers file
and the integrator's signing name. By default it reads both from the host's publication at
`refs/the-valley/signers` in the instance repository (`implemented`; see
[the signers publication](./architecture.md#the-signers-publication)).

That publication is project storage, not source. No change, review or check governs it. The client
trusts it for a different reason: only the host's machinery writes it, never a push. The push hook
refuses every push to it (`implemented`, checked by `signers-e2e` and `publish-e2e`). On the host,
the publisher writes it from the file and the name the controllers use (`implemented`, checked by
`signers-e2e`). The integrator can write it too, because the instance repository is group-writable
and the integrator is in that group to land code there (`implemented`, `nix/valley-host.nix`). So a
client that reads it trusts the host's disk, the host's configuration and the integrator. It already
trusts the same host to serve `main`, and the same integrator to decide what lands on it.

A revocation reaches clients through the publication. Once the registry lets no key sign, the
compiler writes a known-signers file with no key in it, and the host publishes that file. A client
then verifies no transfer statement with the revoked keys (`implemented`, checked by `signers-e2e`).
Two runs of the publisher take turns under a lock, so a run that read the file before a rotation
cannot publish it over a run that read it after (`implemented`, checked by `signers-e2e`).

The client finds the instance repository through `policy/valley` at `main`'s tip on origin, which is
integrated source. A change that lands a new `policy/valley` points clients at another repository's
publication. A flag or a variable set on the client replaces the publication for the value it gives.
The restacker reads neither the publication nor the variables, only its own flags.

A wrong publication cannot by itself make a version read `landed-recorded`. That also takes the
integrator's landing record for the version's head, or for a landing made before landing records
existed, its outcome record, saying the head landed as that commit. No push may write either record
(`implemented`, checked by `protect-e2e` and `valleyhook-unit`). A wrong publication can make a real
landing read `uncertain`.

## Who can read a project

Read access to a project requires two consents ([Read access](./architecture.md#read-access)). A
project's own `policy/read` names the principals allowed to read it. The instance's identity
registry grants principals read access at an `edge-read` boundary, for named projects or for all
projects. Both consents must match, so the project's policy and the instance's registry each hold a
key (`implemented`, `readaccess/access.go`).

- **`anyone`**, the reader who presents no credential, is authorized at an edge for the projects
  whose `policy/read` names `anyone` and which a read grant to `anyone` covers at that destination
  (`implemented`).
- **A named principal**, including the operator, is evaluated by the same two-consent rule in
  `readaccess/`. However, named grants publish nothing and `valley-web` authenticates no reader
  (**not built**), so at the edge a named principal can read only what `anyone` may read.
- **Membership, push access, approval and restacking** grant no read access at the edge.

Which principals each project's policy names and which grants the registry declares are a
**deployment choice**.

**What reaches the edge.** A publisher pass includes in its export and bundles only the projects
that both consents captured in that pass authorize `anyone` to read at the destination
(`implemented`, `web/cmd/valley-web-publish/valley.go`). A separate signed authorization controls
which of those exported projects the edge serves (see
[The authorization decides what is served](#the-edges-receiver)). Projects not authorized for
`anyone` in that pass are omitted from the export and bundles it builds. For each exported project,
the export and bundle hold the project's reader view in plaintext: every knot and comment in its
plan graph, its ready changes, and `main` with its history. Once an export is sent to an edge, that
edge is the security boundary for what it holds: its administrators, and any process with access to
its storage, can read all of it. Activating a newer export changes which generation the edge serves.
It does not guarantee that an older generation is deleted: the edge's store keeps a generation that
a reader still holds, or whose deletion failed, until a later sweep removes it (`implemented`,
`web/export/store.go`). So withdrawing a project from what the edge serves is not deleting it from
the edge's storage. A read grant at an `edge-read` boundary is consent to send a project's reader
view to the destination that boundary names.

**Groups place public projects and grant no read access.** A project reaches the edge only when
`valley.cue` places it in a group, because a project without a group has no web URL. Placing a
project in a group gives it a web URL once both read-access consents authorize `anyone`. Omitting
`group` keeps a project off the web even when both consents authorize `anyone`. The export includes
only groups that have at least one public project, along with the web names, former names, and short
aliases of public projects (`implemented`, `web/export/project.go`). Groups with no public projects
are omitted, and private projects' web names, former names, and short aliases are omitted. The web
server reads only the export. Requests for a private project — whether under its group, another
group, a former name, or a short alias — return the same `404` page byte for byte as requests for a
project that does not exist (`implemented`, `web/server/server.go`). A group alias redirects any
path under it to the group's `name` before checking whether the project exists, so hidden and
nonexistent projects redirect identically and then return `404`.

**A grant is bound to a project.** A grant that names a specific project binds both its name and its
publication identity, so a new project that reuses an old project's name with a new identity is not
covered by the old grant. A policy is valid only for the project repository it is read from. A
policy or registry that is missing, fails schema validation, or exceeds its size bounds consents to
nothing (`implemented`, `readaccess/read.go`).

**The registry is read at its ref by that exact name.** The publisher and `valley-read-coverage`
resolve the registry ref with `git show-ref`, which checks that exact full ref name (`implemented`,
`readaccess/read.go`). `git rev-parse` is not used to resolve ref names, because when a ref such as
`refs/heads/registry` is missing it falls back to `refs/heads/refs/heads/registry`, a branch any
pusher can create. Instead, `git show-ref` resolves the ref name to an object ID, `git rev-parse`
peels that object ID to a commit, and all subsequent reads use that commit ID. When the configured
registry ref is not `refs/heads/main`, an instance repository without it has no registry, so every
project is private. An instance repository without `refs/heads/main` is not an instance repository,
so the publisher aborts the pass instead of building an empty export (`implemented`,
`web/cmd/valley-web-publish/valley.go`). The host module rejects a `services.valley.identity.ref`
that is not a full ref name (`implemented`, `nix/valley-host.nix`).

**SSH fetches ignore read grants.** Read grants govern only web exports. On the valley host, every
SSH key admitted for the git user can fetch every repository (`valleyhook/shell.go`). The git user's
shell gates pushes and leaves fetching open. An admitted key without a principal tag runs as no
principal: its session leaves `VALLEY_PRINCIPAL` unset. It is not `anyone`, which applies only to
unauthenticated edge reads.

**Withdrawal.** When either consent for `anyone` is withdrawn, the next pass's policy path signs an
authorization that omits the project and sends it to the receiver before any content work begins
(`implemented`, `web/cmd/valley-web-publish/policy.go`). The receiver stores the authorization
without waiting for any content upload or commit, and `valley-web` stops serving the project on the
next request (`implemented`, `web/export/authorize.go`, `web/server/server.go`). The policy path
reads only the instance repository, the identity registry, and the `main` branch and `policy/read`
of each project selected by a read grant to `anyone`. Because it reads no plan graph or knots, a
broken or unreadable plan graph cannot delay a withdrawal. If a selected project's own policy cannot
be read, that project retains at most the access granted by the last authorization the receiver
acknowledged, and only while the receiver still holds that authorization, while withdrawals for
other projects still take effect (`implemented`, `web/export/authorization.go`). A publisher state
restored from an older backup keeps no such access. Projects not selected by a grant to `anyone` are
not read (`implemented`, `web/cmd/valley-web-publish/read_test.go`). If the instance repository or
the registry cannot be read, the pass signs no authorization, and the withdrawal waits for a later
pass that can read them. Authorizations have no serving expiry: while the valley host cannot reach
the edge, the edge continues serving what the stored authorization allows. Once the host reaches the
edge again, the next pass re-evaluates the policy and sends a fresh authorization, which takes
effect even if the pass's content upload fails. Unbuilt parts of read access are listed in
[Not deployed or not wired yet](./architecture.md#not-deployed-or-not-wired-yet).

Checks: `readaccess-unit` reads both consents from real commits under the real schemas, including
policies that redeclare a schema definition and trees with malformed entry names. `web-unit`
verifies that the publisher requires both consents (`TestVisibilityNeedsBothConsents`) and that no
private sentinel appears in any export or bundle (`TestPrivateSentinelsNeverLeave`). Both `web-unit`
(`TestTheRegistryIsReadAtItsRef`) and `readaccess-unit` (`TestRegistryRef`) verify that every
project stays private when the registry ref is missing even if `refs/heads/refs/heads/registry`
grants `anyone` read. `module-eval` rejects a short registry ref. `cue-vet` and `identity-schema`
test both schemas against their rejected examples.

## The web publisher

`valley-web-publish` runs on the valley host and uploads signed exports of public projects to the
edge. It reads local repositories, signs each manifest with its own key, and connects out to the
receiver. Nothing on the edge connects back to the valley host.

**Only projects `anyone` may read leave the host.** A pass exports a project only when three
conditions hold: the project's `policy/read` names `anyone`, a read grant to `anyone` for the
destination covers the project, and the host declaration places the project in a group
(`implemented`, `web/cmd/valley-web-publish/valley.go`). The edge serves every reader anonymously,
so everything that reaches it is public (see
[What reaches the edge](./architecture.md#what-reaches-the-edge)).

**The export key signs every manifest.** On its first start, the publisher generates an Ed25519 key
in its state directory and writes the key's verifier line beside it under the signing name
`<instance>/web-export` (`implemented`, `web/cmd/valley-web-publish/state.go`). The receiver admits
only manifests that open under a verifier line it pins. Pinning that verifier line on the edge is a
**deployment choice**. On NixOS, the state directory belongs to the unit with mode `0700`
(`implemented`, `nix/web-publisher.nix`).

**The pass runs confined.** On NixOS, the publisher's unit runs as a dynamic user in the `git` group
(`implemented`, `nix/web-publisher.nix`). The host repositories are mounted read-only, and the host
filesystem is read-only except for the unit's state and cache directories and its private temporary
directories. The `git` user's SSH identity and the host's signing, mirror, and backup keys are
mounted inaccessible. The unit's `safe.directory` list names only the repositories the host
declares. The unit can still read the host's other repositories through the filesystem. The full
filesystem sandbox is **not built**.

**The pass connects only to allowed IP addresses.** The unit denies all IP traffic
(`IPAddressDeny=any`) and allows only the addresses in
`services.valley.webPublisher.allowedAddresses` (`implemented`, `nix/web-publisher.nix`). The module
refuses to build when `allowedAddresses` is empty. A pass reads local repositories and runs `valley`
commands that use no network, so it needs to reach only the receiver. Which receiver addresses to
list is a **deployment choice**. The upload client connects directly to the receiver and ignores
proxy environment variables such as `HTTPS_PROXY` (`implemented`,
`web/cmd/valley-web-publish/upload.go`).

**The unit checks that the IP filter is active before each pass.** When the kernel or service
manager lacks cgroup BPF support, systemd logs a warning and leaves `IPAddressDeny` and
`IPAddressAllow` unenforced. Before each pass, the unit therefore runs
`valley-web-publish-egress-check` inside the unit's own cgroup (`ExecStartPre`, `implemented`,
`nix/web-publisher.nix`). The check sends a UDP datagram to `198.51.100.1` port `9`, an address the
filter must block. The pass starts only when that send fails immediately with `EPERM`. If the
datagram goes out or fails with any other error, the check aborts the pass and logs the reason. The
module refuses `allowedAddresses` entries that cover `198.51.100.1`. The host needs a route to
`198.51.100.1`, such as a default route, so the datagram reaches the cgroup filter instead of
failing earlier with no route. The check uses UDP because the cgroup filter returns `EPERM` on
`send` at once, whereas a dropped TCP SYN waits for a connection timeout.

**Host names resolve outside the unit.** The unit sets `GODEBUG=netdns=cgo` so the publisher
resolves host names through the C library instead of Go's built-in resolver (`implemented`,
`nix/web-publisher.nix`). `nix/packages.nix` builds the publisher with cgo enabled. On NixOS, the C
library queries `nscd` over a local Unix socket, and `nscd` sends the DNS query from outside the
unit's cgroup. When `nscd` is enabled (`services.nscd.enable`, on by default in NixOS; a
**deployment choice**), `allowedAddresses` needs only the receiver's addresses. If `nscd` is
disabled, the C library sends DNS queries from inside the unit, and the filter blocks them unless
`allowedAddresses` also includes the DNS resolver.

The cgroup filter checks only the destination IP address. It permits every port on an allowed
address and checks the resolved address independently of the host name in `--receiver`.

Checks: `module-eval` verifies the unit's IP filter, `ExecStartPre` check, `GODEBUG` setting,
filesystem confinement, and credentials, and verifies that evaluation fails when `allowedAddresses`
is empty or covers `198.51.100.1`. `web-publisher-e2e` boots a NixOS VM and runs the rendered unit
with a probe script in place of the publisher. Inside the unit, the egress check passes; run by root
outside the unit, the same check fails because root's datagram reaches `198.51.100.1`. The probe
verifies that Git accepts a declared repository and refuses an undeclared repository as dubiously
owned. It verifies that repository writes fail and that the git SSH key, backup SSH key, and raw
token file cannot be read. It also verifies that the unit can connect to a listener on its allowed
address while connections to disallowed IPv4 and IPv6 listeners fail, and that the C library
resolves a name served by a DNS server on a disallowed address while direct UDP packets from the
unit to that DNS server fail with `EPERM`.

## The edge's receiver

`valley-web-receive` accepts uploads from the publisher on the edge, and `valley-web` serves the
active generation (see [The receiver](./architecture.md#the-receiver)). Two independent checks guard
an upload.

**The authorization decides what is served.** The receiver stores an authorization only when it
opens under a pinned verifier, carries the authorization's type line, names the receiver's instance
and destination, and carries a sequence greater than the stored one's (`implemented`,
`web/export/authorization.go`). It accepts an authorization regardless of age, so no clock fault
holds up a narrowing. `valley-web` reads the stored authorization on every request and again before
each response's headers, under a lock that the receiver takes to replace the authorization. It
serves a project only while the authorization names the project with the export's identity for it
(`implemented`, `web/server/server.go`). A missing or invalid authorization serves no project. A
content manifest names no reader. The receiver builds a repository only for a project the
authorization names, and content never repairs or replaces the authorization. No content upload,
however late, grants or restores access (`implemented`, `web/export/store.go`). The guarantee covers
every response whose headers are written after the receiver replies (`implemented`,
`web/export/authorize.go`). A response whose headers were written before continues to completion.

**A withdrawn project's plaintext stays until it is replaced.** After a withdrawal, the receiver
deletes the project's repository once no reader holds the active generation (`implemented`,
`web/export/authorize.go`). The signed `export.json` keeps its bytes, so the project's plan text
stays on the edge's disk until a later activation replaces the generation and the old one is
deleted. Withdrawal hides a project from readers while leaving its plan text in the active
`export.json` until the next activation.

**The manifest's signature decides what content the edge holds.** The receiver stages a manifest
only when it opens under a pinned verifier, names the receiver's instance and destination, and
carries a sequence newer than any accepted sequence (`implemented`, `web/export/manifest.go`). It
keeps only bodies whose size and SHA-256 the manifest declares, and activates a generation only when
every bundle passes acceptance (`implemented`, `web/export/body.go`, `web/export/bundle.go`). Only a
holder of the publisher's export key can put content or an authorization on the edge. The bearer
token grants upload access alone.

**A rollback of the edge serves nothing from the store.** The store's entry point is a descriptor
file (`store`), which binaries of the first store layout refuse. Its payloads live under `v2/`,
which those binaries never read (`implemented`, `web/export/store.go`). Neither a rolled-back binary
nor a missing authorization can serve the store's content. The receiver deletes nothing outside its
descriptor and `v2/` (`implemented`), so the first layout's state stays beside the descriptor until
the operator deletes it by hand. It held public projects only, and a rolled-back binary pointed at
it serves that old public export.

**The token decides who may use the receiver.** Every request must carry the bearer token in its
`Authorization` header. The receiver checks the header before reading the request body and refuses
an unauthenticated request as `unauthenticated` (`implemented`,
`web/cmd/valley-web-receive/receive.go`). A stranger who reaches the receiver cannot trigger
manifest verification, stream a body into staging, or start a commit. A holder of the token without
the export key can spend the receiver's staging and request capacity up to its caps, while what the
edge serves stays signed by the publisher.

**The token stays out of the Nix store and out of `valley-web`'s reach.** On NixOS, both modules
load the token file as a systemd credential for their own unit from a path outside the Nix store,
and both refuse a store path at evaluation time (`implemented`, `nix/web-publisher.nix`,
`nix/web-receiver.nix`). `valley-web` runs as a separate user without that credential. The publisher
refuses HTTP redirects, so it sends the token only to the configured receiver URL (`implemented`,
`web/cmd/valley-web-publish/upload.go`). Neither program prints the token. Where a container host
stores the token file, and who can read it there, is a **deployment choice**.

**The token stays secret only on an encrypted or private network path.** Every upload request
carries the token. Whether the publisher reaches the receiver over HTTPS, through a TLS-terminating
reverse proxy, or over a private network is a **deployment choice**. Sending requests over plain
HTTP across an untrusted network exposes the token to anyone who can read that network.

**`valley-web` has read-only access to the store.** It opens the store read-only and writes nothing
there (`implemented`, `web/export/store.go`). On NixOS its unit mounts the state directory read-only
(`implemented`, `nix/web-receiver.nix`). The unit's only writable paths are temporary: its private
`/tmp` and `/var/tmp` from `PrivateTmp` (hidden from other units and removed when the unit stops)
and `/dev/shm` from `PrivateDevices` (`implemented`, `nix/web-receiver.nix`). Those temporary paths
are separate from the state directory, and the receiver ignores them. A fault in `valley-web`, which
handles requests from arbitrary readers, reaches neither the staging area nor the token.

Checks: `web-receiver-unit` verifies that the receiver refuses requests with no token, a wrong
token, a non-Bearer scheme, or two `Authorization` headers before staging anything
(`TestAnUnauthenticatedRequestIsRefused`), answers `OPTIONS *` with a withheld body immediately on
every connection without reading the body (`TestOptionsStarHoldsNoConnection`), and refuses stale,
future-dated, foreign-key, and foreign-destination manifests
(`TestAManifestIsAdmittedOnlyWhenFresh`). It also verifies that the receiver stores an authorization
dated long ago by its sequence, refuses a replayed authorization, and refuses a request of the first
protocol (`TestAnAuthorizationIsAdmittedBySequence`). `web-unit` (`TestPublishesToTheRealReceiver`)
verifies that a pass with a wrong token is refused while the edge keeps serving the active export,
and (`TestStoreDescriptorLeavesTheFirstLayout`) that both store openers refuse a directory and that
a new store deletes none of the first layout's state beside it. It verifies that a restored
publisher state keeps no withdrawn access (`TestARestoredStateRestoresNoWithdrawnAccess`), and that
a paused prune keeps a repository an authorization names again
(`TestAPruneKeepsWhatAnAuthorizationNamesAgain`). `web-server-unit` verifies that a withdrawal takes
effect at the next request, that a response rendered while the authorization changed is rendered
again, and that a withdrawal stored while a response holds the serving lock returns only after that
response's headers are written (`TestAuthorizationIsReadOnEveryRequest`,
`TestAResponseIsHeldToTheAuthorizationWhenWritten`,
`TestAWithdrawalWaitsForAResponseItsCheckAdmitted`), and that a request waits for a held serving
lock only until its deadline or until its reader goes away
(`TestAServingLockWaitEndsWithTheRequest`). `web-push-e2e` withdraws a project with `--policy-only`
while an upload is stalled, and checks that the stalled upload restores nothing and that a store
with its authorization removed serves no project. `module-eval` verifies the credentials and users
both modules render and rejects a token path in the Nix store. `web-publisher-e2e` verifies in a
NixOS VM that the publisher's unit receives the token as a credential while the token file on disk
stays readable by root alone.

## Secrets

The valley host needs a few secrets, and the deployment provisions each one as a file outside the
Nix store (**deployment choice**; see
[What a deployment supplies](./architecture.md#what-a-deployment-supplies)). They are the
integrator's signing key, the git user's ssh key for mirror pushes, and the backup's credentials.

A deployment must keep these secrets, and every key that decrypts them, out of agents' reach. Two
properties of encrypted secrets bear on that:

- A copy of any recipient's private key decrypts every secret encrypted to it, on any machine. When
  secrets are encrypted to host keys, each host's key is a software private key that can decrypt
  them.
- Old ciphertext stays decryptable by every key it was encrypted to. A secret once encrypted to a
  key that agents could read must be replaced with a new secret, not only re-encrypted.

## What the operator does

- Approves changes, with `[a]pprove` in `valley review` or with `valley approve`. Approving a change
  files its integration request. This is the only act on this list that a check enforces (step 2).
- Holds the hardware keys and manages the deployment's secrets.
- Signs releases and approves protected path classes, once those gates exist (**not built**).

## The autonomy ladder

Automation grows in deterministic forms, under policy the operator signs. Agents never decide for
themselves when to deploy.

1. **Today.** An approved landing on `main` is the gate. A host that deploys from `main` deploys it
   on its next run.
2. **Signed releases** (**not built**). A signed release is the gate, and a landing does not deploy.
   One touch and PIN signs a batch. Rollout through canaries, soak and rollback runs unattended.
3. **A batched cadence.** A scheduled release promotes everything accumulated, and one touch signs
   it.
4. **Approved upstreams.** When a deployment's inputs, the-valley among them, land through
   approval-gated integration, a change that only updates those inputs can land when its checks
   pass. Those updates still ship through releases.
5. **A delegated release signer.** A machine key in the release environment, out of agents' reach,
   signs releases that touch only declared low-risk path classes. The operator signs that policy
   with a touch. Entry criterion: a record of health-gated rollback catching real failures in
   production, and a measured count of touches per week.

## Residual risks

- **Agents must not be able to land outside the valley.** If a host deploys from a repository on a
  forge, an agent with merge rights there can deploy without an approval. A deployment gives agents'
  accounts no merge rights on such a repository.
- **Old secret ciphertext stays decryptable.** A secret encrypted to a key that agents could once
  read stays readable with that key, in every copy of the ciphertext, until the secret is rotated.
- **An escape from an agent VM on the valley host reaches the host.** The VM has its own kernel, and
  an escape through KVM or the hypervisor reaches the host that runs the valley.
- **Every key of the operator's principal holds `approve`.** Filing needs no touch until step 3 is
  built.
- **Check definitions come from the change.** A change can edit the flake check it is judged by
  ([bd-eaefe82](../archive/.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)).
- **Hosts must not update from an unsigned branch.** A host that upgrades itself from a branch
  without a signature check deploys whatever reaches that branch. When the branch is on a forge
  rather than on the valley, no gate in this document applies to it.
- **Readiness is not enforced atomically.** A change paused in the moment before the integrator
  moves the target can still land, at the head the operator approved (step 2).
- **Every admitted key can fetch every repository.** Read grants govern web exports alone. Any SSH
  key sshd admits for the git user, including an agent's key, can fetch any project on the valley
  host.
- **A withdrawal waits on reading the instance and the registry.** A pass that cannot read the
  instance repository or its registry signs no authorization, and the edge keeps serving what it
  has. A selected project whose own policy cannot be read keeps the access the receiver's current
  authorization gives it until a pass can read it.
- **A withdrawn project's plan text stays on the edge's disk.** The signed `export.json` keeps a
  withdrawn project's knots and comments until a later activation replaces the generation.
- **The receiver's token is a shared secret.** The publisher and the receiver share one bearer
  token, and every upload request carries it. Anyone holding the token can fill the receiver's
  request slots and staging area up to its caps, delaying uploads while the flood lasts. Plain HTTP
  over an untrusted network exposes the token in transit.
- **Anyone who reaches the receiver can occupy its connection limit.** An unauthenticated client can
  open all 32 concurrent connections and hold each one for up to the 10-second header timeout,
  delaying uploads while the flood lasts. Which network peers can reach the receiver's listener is a
  **deployment choice**.
- **The bus has no authentication.** Any local process on the valley host can publish events.
  Nothing treats an event as authoritative.
- **Listing `integrator` in `protection.writers` opens a push path to `main`.** The integrator moves
  `main` on disk and needs no place in `protection.writers`. A deployment that lists `integrator`
  there lets any key tagged `integrator` push `main` directly. No declared or registry key may carry
  that tag.
