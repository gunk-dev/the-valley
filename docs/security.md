# Security and rollouts

This document describes how the-valley and cosmo protect the operator's hosts. It follows the path a
change takes, and names the gate at each step, its status, and the check that demonstrates it. A
claim with no check says so. The components are described in [architecture.md](./architecture.md),
and the planned gates are in [roadmap.md](./roadmap.md).

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
| 5    | Bad rollouts undo themselves                      | Live                       |

### 1. Agents are not the operator

The coordinator and all agents run in the `klaus-env` VM on classic-laddie (cosmo
`modules/klaus-env/`, `hosts/classic-laddie/klaus-env.nix`, `docs/klaus-env.md`). The operator's
Unix account runs no agents.

- **The boundary is the VM.** The guest has no host directory shares and no operator secrets. It
  runs its own Nix daemon. Root inside the guest is accepted.
- **Its own identities.** On GitHub the guest acts as `patflynn-agent`. On the valley its key
  belongs to the `klaus-env` principal, which may push topic branches and nothing else.
- **Caps.** The VM has 8 vCPUs and 10 GiB of memory. Its systemd slice caps CPU at 900% and memory
  at 12 GiB, so guest builds cannot starve the host.
- **Egress.** Host firewall tables (`modules/klaus-env/host.nix`) let the guest reach the two LAN
  DNS resolvers, ssh on the host itself, and TCP 443 on public IPv4. Everything else is dropped:
  private and other special-purpose ranges, the tailnet, other host ports, spoofed sources and all
  IPv6.
- **ssh into the host.** From the guest's subnet, sshd admits only the valley git user, with no
  forwarding, TTY or tunnel.

Checks: cosmo `checks.x86_64-linux.klaus-env-firewall` (a three-node VM test with negative
controls), `klaus-env-sshd` (the host's real sshd config) and `klaus-env-check` (the probe tool's
unit tests). From inside the running guest, `klaus-env-check` probes the live boundary; its last run
passed 54 of 54. That the guest's valley key cannot file a request is shown by the refusal of a
request push from the guest (step 2).

### 2. Only the operator can file an integration request

The push hook refuses an integration-request ref unless the pushing principal holds the `request`
grant (`valleyhook/policy.go`). The principal comes from the key that logged in, and a client cannot
set it (`valleyhook/shell.go`). The registry gives `request` to the operator's principal only. Every
key of that principal holds it.

Checks: `protect-e2e` and `ssh-e2e` (a real sshd, a grant holder and a non-holder) in the-valley.
From the guest, a request push and a `refs/replace` push are refused and a topic push is accepted.

### 3. Filing takes a touch (queued)

The `request` grant moves to the operator's `ssh:valley-approve` YubiKey keys, verified with
`sigverify`. Until then, filing a request needs the operator's principal and no touch. `sigverify`
itself is checked by `sigverify-unit` and `sigverify-e2e`, including a touchless-signature negative
test. The registry schema does not accept hardware-backed keys yet.

### 4. Hosts deploy only what the operator signed (designed, redesign pending)

The design is M1-C: hosts verify signed, sequenced release tags and never track `main`. It is being
rebuilt on `sigverify`. Today the converging hosts (classic-laddie, weller, cutie-pi and sweetie-pi)
build and activate the tip of cosmo's `main` (cosmo `modules/converge/`, `docs/converge.md`). On
classic-laddie and weller a GitHub push webhook starts the run. Every converging host also runs it
hourly. So a merge to cosmo's `main` deploys within the hour.

### 5. Bad rollouts undo themselves

cosmo's converge activates a new generation with `switch-to-configuration test`, then runs the last
known good generation's health check (cosmo `modules/converge/`, `docs/converge.md`). The health
check covers required units, an sshd banner, the default route, and on the Pis their DNS service.

- **Pass:** converge commits the generation with `switch`.
- **Fail:** converge switches back to the running system, quarantines the revision, and records
  `phase=failed`. A quarantined revision is not applied again until the operator clears it or `main`
  moves past it.
- **Kernel or boot changes:** converge stages the generation for the next boot. After the reboot,
  the health check runs. On failure, converge stages the last good generation back and the next
  reboot restores it.
- **Resolvers:** a Pi does not reboot while its peer resolver is unhealthy (cosmo
  `hosts/pi-dns/peer-check.nix`). classic-laddie has a public fallback resolver.

Checks: cosmo `checks.x86_64-linux.cosmo-rebuild`, `cosmo-rebuild-vm` (a VM test of rollback),
`auto-reboot`, `resolver-redundancy` and `converge-status`.

## Threat model

| Threat                             | Response today                                                                                                                                      |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| An agent acting as the operator    | Agents run in `klaus-env` with their own identities and no operator keys (step 1). The push hook scopes refs by principal (step 2).                 |
| The interactive coordinator        | It runs in `klaus-env` with the other agents. Only a verified touch stops an agent that holds a grant, and that gate is queued (step 3).            |
| A compromised agent                | Assume arbitrary commands inside the VM. The VM has no production secrets and no signing sockets, and its egress is limited.                        |
| A broken but honest change         | Owed checks must pass before a landing. A bad rollout rolls back (step 5). A human signature authorizes risk; it does not prove correctness.        |
| A lost hardware key                | An offline backup YubiKey is enrolled for every role.                                                                                               |
| classic-laddie compromised or down | Other hosts keep their current systems. Recovery that does not depend on classic-laddie is planned with M1-C.                                       |
| A bad network or DNS change        | Pi reboots wait for a healthy peer resolver, and classic-laddie has a fallback resolver. unifi-sync has no automatic trigger and runs only by hand. |

## Trust roots

- **Two YubiKeys**, a primary and an offline backup. Each holds an approve key
  (`ssh:valley-approve`, touch), a release key (`ssh:cosmo-release`, PIN and touch) and an age PIV
  identity for secrets. The ssh keys are enrolled, and no code uses them yet. The age identities are
  cosmo's `keys.admins` (cosmo `secrets/keys.nix`).
- **A Tillitis TKey** is planned as the offline root for trust-root changes. It is not set up yet.
  `sigverify` has a `tkey-signer` class for it.
- **Host ssh keys**, which agenix uses to decrypt each host's secrets (cosmo `secrets/keys.nix`).

## Secrets

cosmo keeps its secrets in agenix (cosmo `secrets/secrets.nix`, `docs/secrets-management.md`). Every
secret is encrypted to the two YubiKey age identities and the host keys, and to no user key. So
decrypting a secret off a host takes a YubiKey touch. Decrypted secrets on a host are mode `0400`.
No check asserts the recipient list. A rekey is verified by reading the age headers.

## What only the operator does

- Files integration requests, with `valley review` and `[a]sk`.
- Merges on GitHub, for repositories still developed there.
- Rekeys and edits secrets, with a YubiKey touch.
- Later, with M1-C: signs releases and approves protected path classes.

## The autonomy ladder

Automation comes back in deterministic forms, under policy the operator signs. Agents never decide
for themselves when to deploy.

1. **Today.** A human merge is the gate, and a merge deploys within the hour.
2. **After M1-C.** A signed release is the gate, and a merge does not deploy. One touch and PIN
   signs a batch. Rollout through canaries, soak and rollback runs unattended.
3. **A batched cadence.** A scheduled release promotes everything accumulated, and one touch signs
   it.
4. **Approved upstreams.** When klaus, the-valley and the other inputs land through approval-gated
   integration, cosmo's bumps of them can merge when checks pass. They still ship through releases.
5. **A delegated release signer.** A machine key in the release environment, out of agents' reach,
   signs releases that touch only declared low-risk path classes. The operator signs that policy
   with a touch. Entry criterion: health-gated rollback has caught real failures in production, and
   the touches per week are measured.

## Residual risks

- **`patflynn-agent` can write to cosmo on GitHub.** An agent could merge to cosmo's `main`, which
  deploys within the hour. This closes when cosmo moves onto the valley (M5).
- **Old secret ciphertext stays decryptable.** cosmo's git history holds secrets encrypted to a key
  agents could once read. Rotation is deferred.
- **A guest escape lands on classic-laddie.** The VM shares classic-laddie's kernel and hypervisor,
  and classic-laddie also runs the valley.
- **Every key of the operator's principal holds `request`.** Filing needs no touch until step 3
  lands.
- **Check definitions come from the change.** A change can edit the flake check it is judged by
  ([bd-eaefe82](../archive/.the-valley/bugs/bd-eaefe82-check-definitions-come-from-the-branch.md)).
  the-valley's own code paths owe only the floor's `unclassified` checks.
- **Two hosts update unsigned.** johnny-walker and makers-nix run `system.autoUpgrade` from GitHub
  (cosmo `modules/common/system.nix`).
- **The bus has no authentication.** Any local process on classic-laddie can publish events. Nothing
  treats an event as authoritative.
