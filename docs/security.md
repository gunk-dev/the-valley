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
  branches and create attestation refs. The hook accepts an attestation ref only when every note in
  it verifies under a known signer (`valleyhook/policy.go`, `valleyhook/attestation.go`). An agent's
  key can also create change records, which makes the agent's principal the owner of those changes,
  and add versions to them (`valleyhook/record.go`). A record never makes anything land. The
  integrator reads records only to refuse a request or to void a withdrawn change's requests
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

| Threat                              | Response                                                                                                                                                                     |
| ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| An agent acting as the operator     | Agents run with their own principals and no operator keys (step 1). The push hook scopes refs by principal (step 2).                                                         |
| An interactive coordinator          | An agent that directs other agents runs with them, under an agent's principal. Only a verified touch stops an agent that holds a grant, and that gate is not built (step 3). |
| A compromised restacker             | Its `restack` grant adds versions to ready changes and writes reports, never requests. What it adds lands only once the operator approves that head (step 2).                |
| A compromised agent                 | Assume it runs arbitrary commands inside its boundary. The boundary holds no production secrets and no signing sockets, and its egress is limited (step 1).                  |
| A broken but honest change          | A landing needs a signed statement that each required check passed. A failed activation rolls back or holds (step 5). A human signature does not prove correctness.          |
| A lost hardware key                 | An offline backup hardware key holds every role (see [Trust roots](#trust-roots)).                                                                                           |
| The valley host compromised or down | Hosts that deploy from it keep their current systems. A compromised valley host can move `main`, so recovery that does not depend on it needs signed releases (step 4).      |
| A reader of the edge                | Reads only what both consents let `anyone` read. A project `anyone` may not read never reaches the edge (see [Who can read a project](#who-can-read-a-project)).             |

## Trust roots

The operator's trust roots are hardware keys that the deployment supplies (**deployment choice**):

- **Two hardware keys**, a primary and an offline backup. Each holds every role: an approve key that
  needs a touch, and a release signing key that needs a PIN and a touch. No gate uses these keys
  yet, because the gates of steps 3 and 4 are **not built**.
- **An offline root for trust-root changes**, such as a Tillitis TKey. `sigverify` has a
  `tkey-signer` class for it (`implemented`). Nothing uses one for trust-root changes yet (**not
  built**).

## What a client trusts to read a landing

A client decides that a version landed through an integrator merge (`landed-recorded`) by verifying
the integrator's transfer statement. It verifies the statement with a known-signers file and the
integrator's signing name. By default it reads both from the host's publication at
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
integrator's outcome record for the version, saying it landed at the version's head, and no push may
write an outcome record. A wrong publication can make a real landing read `uncertain`.

## Who can read a project

Read access is a grant to a principal, and it takes two consents
([Read access](./architecture.md#read-access)). A project's own `policy/read` names the principals
it lets read it. The instance's identity registry grants principals read at an edge read boundary,
for named projects or for every project. Neither consent alone lets anyone read, so the project's
policy and the instance's registry each hold a key (`implemented`, `readaccess/access.go`).

- **`anyone`**, the reader who presents no credential, reads at an edge the projects whose policy
  names `anyone` and which a grant to `anyone` covers there (`implemented`).
- **A named principal**, the operator included, holds read grants the same way. The edge
  authenticates no reader, so a named principal reads there only what `anyone` may.
- **Membership, push access, approval and restacking** give no read access at the edge.

Who is named in each project's policy and granted in the registry is a **deployment choice**.

**What reaches the edge.** The publisher sends a destination only the projects `anyone` may read
there (`implemented`, `webui/cmd/valley-web-publish/valley.go`). For each, the edge holds the
project's reader view in plaintext: every knot and comment of its plan graph, its ready changes, and
`main` with its history. The edge is the security boundary for what it holds. Its administrators,
and any process with access to its storage, can read all of it. A read grant at an edge read
boundary is consent to send a project's reader view to the destination the boundary names. A project
`anyone` may not read never leaves the valley host.

**A grant is bound to a project.** A grant that names a project names its publication identity too,
so a new project that reuses a name is not covered by the old project's grant. A policy is read only
from the project it names. A policy or registry that is missing, refused by its schema, or past its
bounds consents to nothing (`implemented`, `readaccess/read.go`).

**The registry is read at its ref by that exact name.** The publisher and `valley-read-coverage`
resolve the registry ref with `git show-ref`, which reads that one ref (`implemented`,
`readaccess/read.go`). Everything after reads the commit the ref held. `git rev-parse` is not used,
because it does not stop at the name it is given. For a missing `refs/heads/registry`, it tries
`refs/heads/refs/heads/registry`, a branch any pusher can make. An instance repository with no ref
of the registry's name holds no registry, so every project is private. The host module refuses a
`services.valley.identity.ref` that is not a full ref name (`implemented`, `nix/valley-host.nix`).

**Reading over ssh is not governed by read grants.** On the valley host, every key sshd admits for
the git user can fetch every repository. The git user's shell holds pushes and leaves fetching open
(`valleyhook/shell.go`). A key with no principal tag fetches as no principal, and is not `anyone`.

**Withdrawal.** A project leaves the edge when either consent is withdrawn, with the next export the
receiver activates. A pass that cannot read some project sends nothing, so a withdrawal waits until
every project reads. While the valley host cannot reach the edge, the edge keeps serving the last
export it activated, with no expiry. Every part of read access that is not built yet is listed in
one place, [Not deployed or not wired yet](./architecture.md#not-deployed-or-not-wired-yet).

Checks: `readaccess-unit` reads both consents out of real commits under the real schemas, including
policies that declare a schema definition again and trees with malformed entry names. `webui-unit`
holds the publisher to both consents (`TestVisibilityNeedsBothConsents`) and finds no private
sentinel in any export or bundle (`TestPrivateSentinelsNeverLeave`). Both `webui-unit`
(`TestTheRegistryIsReadAtItsRef`) and `readaccess-unit` (`TestRegistryRef`) keep every project
private when the registry ref is missing and `refs/heads/refs/heads/registry` grants `anyone` read.
`module-eval` refuses a short registry ref. `cue-vet` and `identity-schema` hold the schemas to
their rejected examples.

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
- **Every admitted key can fetch every repository.** Read grants govern the edge alone. A key sshd
  admits for the git user, an agent's included, can fetch any project on the valley host.
- **A withdrawal waits on every project's read.** A project leaves the edge only with an export, and
  a pass that cannot read some project sends none.
- **The bus has no authentication.** Any local process on the valley host can publish events.
  Nothing treats an event as authoritative.
- **Listing `integrator` in `protection.writers` opens a push path to `main`.** The integrator moves
  `main` on disk and needs no place in `protection.writers`. A deployment that lists `integrator`
  there lets any key tagged `integrator` push `main` directly. No declared or registry key may carry
  that tag.
