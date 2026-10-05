# Security and rollouts

This document describes how the-valley and cosmo protect the operator's hosts. It follows the path a
change takes, and names the gate at each step and its status. The components are described in
[architecture.md](./architecture.md), and the planned gates are in [roadmap.md](./roadmap.md).

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
  record registers the guest's key as the `klaus-env` principal, with a push grant and no `request`
  grant. Like any admitted key, it can push topic branches and create attestation refs. The hook
  accepts an attestation ref only when every note in it verifies under a known signer
  (`valleyhook/policy.go`, `valleyhook/attestation.go`).
- **Caps.** The VM has 8 vCPUs and 10 GiB of memory. Its systemd slice caps CPU at 900% and memory
  at 12 GiB, and gives it a low I/O weight. The source notes the I/O weighting is advisory on ZFS.
  No check exercises the caps.
- **Egress.** Host firewall tables (`modules/klaus-env/host.nix`) let the guest reach the two LAN
  DNS resolvers, ssh on the host itself, and TCP 443 on public IPv4. Everything else is dropped:
  private and other special-purpose ranges, the tailnet, other host ports, spoofed sources and all
  IPv6.
- **ssh into the host.** From the guest's subnet, sshd admits only the valley git user, with no
  forwarding, TTY or tunnel.

Checks: cosmo `checks.x86_64-linux.klaus-env-firewall` (a three-node VM test with negative
controls), `klaus-env-sshd` (the host's real sshd config) and `klaus-env-check` (the probe tool's
unit tests). The cutover record also reports a run of `klaus-env-check` inside the live guest, which
passed 54 of 54 probes.

### 2. Only the operator can file an integration request

The push hook refuses an integration-request ref unless the pushing principal holds the `request`
grant (`valleyhook/policy.go`). The principal comes from the key that logged in, and a client cannot
set it (`valleyhook/shell.go`). Checks: `protect-e2e` and `ssh-e2e`, which use test identities, a
grant holder and a non-holder, behind a real sshd.

Who holds the grant on the live host is a deployment record. The ref-policy deployment record of
2026-10-04 (plan Status) shows the compiled grants file naming only the operator's principal. Every
key of that principal holds the grant. The cutover record reports that, from the guest, a request
push and a `refs/replace` push are refused and a topic push is accepted.

### 3. Filing takes a touch (queued)

The `request` grant moves to the operator's `ssh:valley-approve` YubiKey keys, verified with
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

| Threat                             | Response today                                                                                                                                                  |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| An agent acting as the operator    | Agents run in `klaus-env` with their own identities and no operator keys (step 1). The push hook scopes refs by principal (step 2).                             |
| The interactive coordinator        | It runs in `klaus-env` with the other agents. Only a verified touch stops an agent that holds a grant, and that gate is queued (step 3).                        |
| A compromised agent                | Assume arbitrary commands inside the VM. The VM has no production secrets and no signing sockets, and its egress is limited.                                    |
| A broken but honest change         | A landing needs a signed statement that each owed check passed. A failed activation rolls back or holds (step 5). A human signature does not prove correctness. |
| A lost hardware key                | An offline backup YubiKey holds every role (plan Trust roots, 2026-10-04).                                                                                      |
| classic-laddie compromised or down | Other hosts keep their current systems. Recovery that does not depend on classic-laddie arrives with M1-C.                                                      |
| A bad network or DNS change        | A Pi's reboot waits for a peer resolver, with the exceptions in step 5. classic-laddie has a fallback resolver. unifi-sync has no automatic trigger.            |

## Trust roots

- **Two YubiKeys**, a primary and an offline backup. Per the plan's Trust roots record of
  2026-10-04, each holds an approve key (`ssh:valley-approve`, touch), a release key
  (`ssh:cosmo-release`, PIN and touch) and an age PIV identity for secrets. No code uses the ssh
  keys yet. The age identities are cosmo's `keys.admins` (cosmo `secrets/keys.nix`).
- **A Tillitis TKey** is the intended offline root for trust-root changes. It is not set up.
  `sigverify` has a `tkey-signer` class for it.
- **Host ssh keys**, which agenix uses to decrypt each host's secrets (cosmo `secrets/keys.nix`).

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

- Files integration requests, with `valley review` and `[a]sk`. This is the only act on this list
  that a check enforces (step 2).
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
- **Every key of the operator's principal holds `request`.** Filing needs no touch until step 3
  lands.
- **Check definitions come from the change.** A change can edit the flake check it is judged by
  ([bd-eaefe82](../archive/.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)).
  the-valley's project layer declares no class for its own code paths. Whether the floor does
  depends on qinling's private policy.
- **Two hosts update unsigned.** johnny-walker and makers-nix run `system.autoUpgrade` from GitHub
  (cosmo `modules/common/system.nix`).
- **The bus has no authentication.** Any local process on classic-laddie can publish events. Nothing
  treats an event as authoritative.
- **`integrator` is a named writer of `main`.** cosmo lists it in `protection.writers` for both
  projects (cosmo `hosts/classic-laddie/valley.cue`). A key tagged `integrator` could push `main`
  directly. The host's declared keys carry no such tag. The registry's keys live in qinling.
