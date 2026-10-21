# Security and rollouts

This document describes how the-valley and cosmo protect the operator's hosts. It follows the path a
change takes, and names the gate at each step and its status. The components are described in
[architecture.md](./architecture.md), and the planned gates live in
[the plan graph](./concepts.md#records-project-storage-the-plan-graph-and-knots) (`valley plan`).

Deployment records and host configuration below describe the reference deployment (**deployment
choice**): `cosmo` is the reference NixOS fleet, `klaus` is the agent runner, `classic-laddie` is
the primary valley host, and `qinling` is the instance repository.

The document makes three kinds of statement:

- **Behaviour** is what the code does, cited to the files that do it.
- **A deployment record** reports an observation of a live host. It names its source and date, for
  example the cutover record of 2026-10-04 in the plan's Status table.
- **A guarantee** names the executable check that demonstrates it. A statement with no named check
  is unchecked, and says so.

The main threat is an AI agent acting as the operator. So the gates must hold even when an agent
ignores its instructions. The human act of authority is a touch on a hardware key. An approval gate
that the gated agent can administer is only a setting that agent controls
([ida-b7025b5](../archive/.the-valley/ideas/ida-b7025b5-human-decisions-are-signed-acts.md)). A gate
counts as done only when a check has shown it refusing what it should refuse
([oc-9949561](../archive/.the-valley/outcomes/oc-9949561-push-replication.md)).

## The path a change takes

| Step | Gate                                              | Status                     |
| ---- | ------------------------------------------------- | -------------------------- |
| 1    | Agents are not the operator                       | Live                       |
| 2    | Only the operator can file an integration request | Live                       |
| 3    | Filing takes a touch                              | Queued                     |
| 4    | Hosts deploy only what the operator signed        | Designed, redesign pending |
| 5    | A failed rollout rolls back or holds              | Live                       |

### 1. Agents are not the operator

The cutover record of 2026-10-04 (plan Status) puts the coordinator and all agents in the
`klaus-env` VM on classic-laddie, and no agents in the operator's Unix account. The VM is defined in
cosmo `modules/klaus-env/` and `hosts/classic-laddie/klaus-env.nix`.

- **The boundary is the VM.** The guest runs its own NixOS kernel under cloud-hypervisor, using the
  host's KVM. It has no host directory shares and no operator secrets. It runs its own Nix daemon.
  Root inside the guest is accepted.
- **Its own identities.** On GitHub the guest acts as `patflynn-agent`. On the valley, the cutover
  record registers the guest's key as the `klaus-env` principal, with a push grant and no `approve`
  grant. Like any admitted key, it can push topic branches and create attestation refs. The hook
  accepts an attestation ref only when every note in it verifies under a known signer
  (`valleyhook/policy.go`, `valleyhook/attestation.go`). It can also create change records, which
  makes it the owner of every agent change, and add versions to them (`valleyhook/record.go`). A
  record never makes anything land. The integrator reads records only to refuse a request, and to
  void a withdrawn change's requests (`integrator/records.go`). So any principal that may create a
  record can hold back a head, by recording it in a change it then unreadies. Only an `approve`
  holder may delete that record, and deleting it releases the head.
- **Caps.** The VM has 8 vCPUs and 10 GiB of memory. Its systemd slice caps CPU at 900% and memory
  at 12 GiB, and gives it a low I/O weight. The source notes the I/O weighting is advisory on ZFS.
  No check exercises the caps.
- **Egress.** Host firewall tables (`modules/klaus-env/host.nix`) let the guest reach the two LAN
  DNS resolvers, ssh on the host itself, and TCP 443 on public IPv4. Everything else is dropped:
  private and other special-purpose ranges, the tailnet, other host ports, spoofed sources and all
  IPv6.
- **ssh into the host.** From the guest's subnet, sshd admits only the valley git user, with no
  forwarding, TTY or tunnel.
- **Tools that follow `main`.** `valley self-update` can keep the guest's `valley` at the tip of
  `main`, through the flake's home-manager module (architecture,
  [Version and self-update](./architecture.md#version-and-self-update)). It installs only a commit
  that has landed, built from the exact commit `git ls-remote` named, once it passes a smoke test.
  The valley host and the machine the operator approves from do not run it. Their `valley` stays at
  the version their system configuration pins, so a landing never changes the tool that approves or
  admits the next change.

Checks: cosmo `checks.x86_64-linux.klaus-env-firewall` (a three-node VM test with negative
controls), `klaus-env-sshd` (the host's real sshd config) and `klaus-env-check` (the probe tool's
unit tests). The cutover record also reports a run of `klaus-env-check` inside the live guest, which
passed 54 of 54 probes.

### 2. Only the operator can file an integration request

The push hook refuses an integration-request ref unless the pushing principal holds the `approve`
grant (`valleyhook/policy.go`). The grant is also accepted as `request`: a principal holding either
name holds it (`valleyhook/grants.go`). The principal comes from the key that logged in, and a
client cannot set it (`valleyhook/shell.go`). Checks: `protect-e2e`, `identity-e2e`, and `ssh-e2e`,
which uses test identities, a grant holder and a non-holder, behind a real sshd.

Who holds the grant on the live host is a deployment record. The ref-policy deployment record of
2026-10-04 (plan Status) shows the compiled grants file naming only the operator's principal. Every
key of that principal holds the grant. The cutover record reports that, from the guest, a request
push and a `refs/replace` push are refused and a topic push is accepted.

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
did not approve. The race-free form binds each approval to the record's withdraw generation, in the
signed approval statements of M1-C.

### 3. Filing takes a touch (queued)

The `approve` grant moves to the operator's `ssh:valley-approve` YubiKey keys, verified with
`sigverify`. Until then, filing a request needs the operator's principal and no touch. `sigverify`
itself is checked by `sigverify-unit` and `sigverify-e2e`, including a touchless-signature negative
test. The registry schema does not accept hardware-backed keys yet.

### 4. Hosts deploy only what the operator signed (designed, redesign pending)

The design is M1-C: hosts verify signed, sequenced release tags and never track `main`. It is being
rebuilt on `sigverify`.

The converging hosts (classic-laddie, weller, cutie-pi and sweetie-pi) build and activate the tip of
cosmo's `main` (cosmo `modules/converge/`, `docs/converge.md`). On classic-laddie and weller a
GitHub push webhook starts a run. Every converging host also runs on an hourly timer with up to five
minutes of random delay. Converge accepts any new commit on `main`; it does not check who made it.
The build time, a standing hold, or a kernel or systemd change that waits for a reboot can delay
activation further. No check bounds the delay.

### 5. A failed rollout rolls back or holds

cosmo's converge activates a new generation with `switch-to-configuration test`, then runs the last
known good generation's health check (cosmo `modules/converge/scripts.nix`). The health check covers
required units, an sshd banner, the default route, and on the Pis their DNS service.

- **Pass:** converge commits the generation with `switch`. The health check runs before this second
  activation, so a failure that appears only during `switch` is not checked.
- **Fail:** converge switches back to the running system with `test`, quarantines the revision, and
  records `phase=failed`. If switching back fails, converge holds for the operator. A quarantined
  revision is not applied again until the operator clears it or `main` moves past it.
- **Interrupted run:** a run interrupted midway gets one repair attempt, then holds for the operator
  as `needs-operator`.
- **Kernel or boot changes:** converge stages the generation for the next boot. After the reboot,
  the health check runs. On failure, converge stages the last good generation back and the next
  reboot restores it.
- **Resolvers:** a Pi reboots only when a peer resolver answers, unless its own resolver is failing
  or converge has staged a rollback (cosmo `hosts/pi-dns/peer-check.nix`). classic-laddie has a
  public fallback resolver.

Checks: cosmo `checks.x86_64-linux.cosmo-rebuild`, `cosmo-rebuild-vm` (a VM test of rollback),
`auto-reboot`, `resolver-redundancy` and `converge-status`. The plan records open review findings on
converge, and cosmo's ROADMAP D11 carries them as the acceptance criteria of a Go port.

## Threat model

| Threat                             | Response today                                                                                                                                                      |
| ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| An agent acting as the operator    | Agents run in `klaus-env` with their own identities and no operator keys (step 1). The push hook scopes refs by principal (step 2).                                 |
| The interactive coordinator        | It runs in `klaus-env` with the other agents. Only a verified touch stops an agent that holds a grant, and that gate is queued (step 3).                            |
| A compromised restacker            | Its `restack` grant adds versions to ready changes and writes reports, never requests. What it adds lands only once the operator approves that head (step 2).       |
| A compromised agent                | Assume arbitrary commands inside the VM. The VM has no production secrets and no signing sockets, and its egress is limited.                                        |
| A broken but honest change         | A landing needs a signed statement that each required check passed. A failed activation rolls back or holds (step 5). A human signature does not prove correctness. |
| A lost hardware key                | An offline backup YubiKey holds every role (plan Trust roots, 2026-10-04).                                                                                          |
| classic-laddie compromised or down | Other hosts keep their current systems. Recovery that does not depend on classic-laddie arrives with M1-C.                                                          |
| A bad network or DNS change        | A Pi's reboot waits for a peer resolver, with the exceptions in step 5. classic-laddie has a fallback resolver. unifi-sync has no automatic trigger.                |

## Trust roots

- **Two YubiKeys**, a primary and an offline backup. Per the plan's Trust roots record of
  2026-10-04, each holds an approve key (`ssh:valley-approve`, touch), a release key
  (`ssh:cosmo-release`, PIN and touch) and an age PIV identity for secrets. No code uses the ssh
  keys yet. The age identities are cosmo's `keys.admins` (cosmo `secrets/keys.nix`).
- **A Tillitis TKey** is the intended offline root for trust-root changes. It is not set up.
  `sigverify` has a `tkey-signer` class for it.
- **Host ssh keys**, which agenix uses to decrypt each host's secrets (cosmo `secrets/keys.nix`).

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

## Secrets

cosmo keeps its secrets in agenix (cosmo `secrets/secrets.nix`, `docs/secrets-management.md`). Every
secret is encrypted to the two YubiKey age identities and to every host key, and to no user key.

- Decrypting with a YubiKey identity takes a touch.
- The host keys are software keys. A copy of any host's private key decrypts every secret, on any
  machine.
- The recipients include the host key of the retired `klaus-worker-0` VM until the next rekey.

Decrypted secrets on a host are mode `0400`. No check asserts the recipient list. A rekey is
verified by reading the age headers.

## What the operator does

- Approves changes, with `[a]pprove` in `valley review` or with `valley approve`. Approving a change
  files its integration request. This is the only act on this list that a check enforces (step 2).
- Merges on GitHub, for repositories still developed there. `patflynn-agent` can merge too (see
  Residual risks).
- Rekeys and edits secrets, with a YubiKey touch.
- With M1-C: signs releases and approves protected path classes.

## The autonomy ladder

Automation grows in deterministic forms, under policy the operator signs. Agents never decide for
themselves when to deploy.

1. **Today.** A merge to cosmo's `main` is the gate, and converge deploys it on its next run.
2. **After M1-C.** A signed release is the gate, and a merge does not deploy. One touch and PIN
   signs a batch. Rollout through canaries, soak and rollback runs unattended.
3. **A batched cadence.** A scheduled release promotes everything accumulated, and one touch signs
   it.
4. **Approved upstreams.** When klaus, the-valley and the other inputs land through approval-gated
   integration, cosmo's bumps of them can merge when checks pass. They still ship through releases.
5. **A delegated release signer.** A machine key in the release environment, out of agents' reach,
   signs releases that touch only declared low-risk path classes. The operator signs that policy
   with a touch. Entry criterion: a record of health-gated rollback catching real failures in
   production, and a measured count of touches per week.

## Residual risks

- **`patflynn-agent` can write to cosmo on GitHub.** An agent can merge to cosmo's `main`, and
  converge deploys it on its next run. This closes when cosmo moves onto the valley (M5).
- **Old secret ciphertext stays decryptable.** cosmo's git history holds secrets encrypted to a key
  agents could once read. Rotation is deferred.
- **A guest escape lands on classic-laddie.** The guest has its own kernel. An escape through KVM or
  cloud-hypervisor reaches classic-laddie, which also runs the valley.
- **Every key of the operator's principal holds `approve`.** Filing needs no touch until step 3
  lands.
- **Check definitions come from the change.** A change can edit the flake check it is judged by
  ([bd-eaefe82](../archive/.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)).
- **Two hosts update unsigned.** johnny-walker and makers-nix run `system.autoUpgrade` from GitHub
  (cosmo `modules/common/system.nix`).
- **Readiness is not enforced atomically.** A change paused in the moment before the integrator
  moves the target can still land, at the head the operator approved (step 2).
- **The bus has no authentication.** Any local process on classic-laddie can publish events. Nothing
  treats an event as authoritative.
- **`integrator` is a named writer of `main`.** cosmo lists it in `protection.writers` for both
  projects (cosmo `hosts/classic-laddie/valley.cue`). A key tagged `integrator` could push `main`
  directly. The host's declared keys carry no such tag. The registry's keys live in qinling.
