# Roadmap

This roadmap orders the work on the-valley, cosmo and klaus. cosmo is the operator's NixOS fleet
configuration and the-valley's reference deployment. klaus is the agent runner. The order closes
security holes before it gives agents more autonomy.

The roadmap gives no dates. Each step says what it unlocks. What runs today is in
[architecture.md](./architecture.md), and the gates and their checks are in
[security.md](./security.md). Purpose and principles are in [purpose.md](./purpose.md).

## The sequence

| Step | Milestone                                       | Status                        |
| ---- | ----------------------------------------------- | ----------------------------- |
| 1    | Close present-day holes                         | Done                          |
| 2    | M3: agent isolation                             | Done                          |
| 3    | M5: cosmo on the valley                         | Next. The design is in review |
| 4    | M1-C: signed releases and touch-gated approvals | Queued                        |
| 5    | M6: klaus on the valley                         | Queued                        |
| 6    | M7: throughput and research                     | Ongoing, after step 5         |

M2 (real builds, a binary cache, promotion and canaries) and the rest of M4 (the-valley's refocus)
are planned. They have no place in the sequence yet.

### 1. Close present-day holes (done)

These steps closed the paths by which an agent could reach root on a host, or change a host, with no
human act:

- M0, containment: klaus does not merge on approval, unifi-sync runs only by hand, and the update
  workflows open pull requests.
- M0-H, credentials: two YubiKeys are enrolled, and every cosmo secret is encrypted to the YubiKeys
  and the hosts only. Credential rotation is deferred.
- M1-A, converge: a host health-checks each new generation, rolls back on failure, and holds the bad
  revision.
- M1-B, the verifier: `sigverify` checks OpenSSH signatures, including the hardware key's presence
  bit. It is landed and not wired to anything yet.
- The ref policy: the valley's push hook is default-deny, and filing an integration request needs
  the `request` grant.
- The CI runners' root helpers are hardened, so a pull request job cannot reach root on the CI host.

**Unlocks:** no agent-approved change reaches `main` automatically, and a bad rollout undoes itself.

### 2. M3: agent isolation (done)

The coordinator and all agents run in the `klaus-env` VM on classic-laddie. On GitHub they act as
the `patflynn-agent` account. On the valley they push as the `klaus-env` principal, which may push
topic branches and nothing else. The operator's Unix account runs no agents.

**Unlocks:** the valley's server-side gates bind agents whether or not the agents follow the rules.
An agent cannot file an integration request, because only the operator's principal holds `request`.

### 3. M5: cosmo on the valley (next)

cosmo is hosted on the valley, with GitHub as a mirror. The design is in review.

**Unlocks:** cosmo changes land only through the integrator, so `patflynn-agent` loses its path to
cosmo's `main` on GitHub. cosmo work continues while GitHub is unavailable.

The archive records two traps for a second project on the valley:
[ida-62a7a3b](../archive/.the-valley/ideas/ida-62a7a3b-reactions-have-no-home.md) (GitHub bots left
running on a mirror) and
[ida-a0e5d03](../archive/.the-valley/ideas/ida-a0e5d03-second-mirror-identity.md) (GitHub refuses
one deploy key on two repositories).

### 4. M1-C: signed releases and touch-gated approvals

This step is rebuilt on `sigverify`. It has two halves.

- **Releases.** The operator signs sequenced release tags with a YubiKey release key, which takes a
  PIN and a touch. Each host verifies the tag with trust roots from its running system, refuses an
  older or replayed release, and ignores unsigned pushes to `main`. Hosts never track `main`. The
  same check covers the hosts on `system.autoUpgrade` and the work profiles, or those paths retire.
- **Approvals.** The identity schema and compiler accept hardware-backed keys. The `request` grant
  moves to the operator's `ssh:valley-approve` YubiKey keys, so filing a request takes a touch. A Go
  `valley approve` signs the repository, the change, the candidate commit, the target and the policy
  snapshot, with batching. The integrator enforces approvals per path class. By default the
  approval-required classes are governance, policy, identity, check definitions and `docs/`.

**Unlocks:** landing on `main` and deploying to a host become separate acts, each signed by the
operator. Agents can then hold `request` for low-risk paths, while infrastructure changes still need
a touch.

### 5. M6: klaus on the valley

- The pipeline moves out of the dashboard into a daemon with persistent state, and with budgets per
  pull request, per session and for every backend.
- A valley backend: agents submit through `valley request` with `Change-Id` and `Klaus-Run`
  trailers, and paused runs checkpoint to valley refs.
- Instrumentation: head SHAs on events, a dispatch reason, check start and end, and repo-qualified
  keys.

**Unlocks:** human and agent work on cosmo and the-valley with no GitHub dependency, and a report of
where agent wall-clock time goes.

### 6. M7: throughput and research

- Measure first.
- Then try a hybrid speculative queue with evidence reuse.
- Then run the throughput experiments: land-then-verify with automatic revert for low-risk classes,
  and stacked changes. These run only where fixes really are cheap. DNS, routing, boot and
  credential changes stay outside them.

**Unlocks:** more accepted changes per human touch, measured against unchanged acceptance criteria.

## Planned, not yet sequenced

- **M2, builds and rollout.** CI builds every converging host, including the aarch64 Pis. Harmonia
  on classic-laddie serves the cache. CI runners get memory and CPU caps. `cosmo-promote` runs in
  the release environment. Rollouts go through role canaries and soak periods. `/srv/git` gets its
  own ZFS dataset with snapshots.
- **M4, the-valley's refocus.**
  - A crash-safe landing: one ref transaction moves the target, writes a decision record to an
    append-only ledger ref, and consumes the request. A write-ahead intent and recovery cover
    crashes, and interruption tests prove it.
  - The reconciliation memo includes the instance policy and registry snapshot.
  - Workers produce missing check results, and the integrator only referees.
  - Path classes for Go, Nix and CUE, so the integrator's own code is gated by real checks.
  - The review flow inverts: `valley request` produces the evidence before human review.
  - Effectful validity windows and the closure-transfer machinery are dropped.
  - `bin/valley`, `integrator/`, `identity/` and `attest/` become one Go binary, and SSHSIG with
    `sigverify` replaces the custom note format.
- **Valley identity follow-ups.** qinling bumps its the-valley pin. The registry compiles when
  qinling lands a change, as well as on its timer. The static key for `stoned-flynn` stops outliving
  its registry expiry.
- **Credential rotation.** The secrets whose old ciphertext stays in git history are rotated,
  starting with the integrator's signing key.

## Deliberately deferred

- Capability-style delegation chains
  ([ida-a8243d2](../archive/.the-valley/ideas/ida-a8243d2-agent-runs-act-under-delegated-authority.md)).
  Grants in the registry are flat.
- Approving broad intent, as a delegation contract.
- Speculative integration. The integrator stays sequential until measurements justify more.
- A custom change API, or "no `main`". Git and `main` stay the substrate.
- A self-hosted forge. GitHub stays as an untrusted mirror and transport, because trust comes from
  signatures.
- An agent-environment fulfillment engine. `klaus-env` is a declared NixOS VM.

## Open decisions

1. **The release environment.** Where release builds are verified and the operator signs. It must
   sit outside classic-laddie's root authority and away from agents. The options are weller with
   agents kept off it, a dedicated small machine, or a laptop used only for releases.
2. **The touch budget.** Which path classes need approval in cosmo and the-valley, and the batching
   policy, such as one ceremony per release covering integration and promotion.
3. **The work-profile switch.** Keep it paused, or bring it under signed promotion.
4. **Phase 2 release manifests.** When releases pin exact closures, signed off classic-laddie, so
   the cache becomes untrusted transport.
5. **`docs/` as an approval-required path class.** Now, or with the approval rework.
6. **The `klaus-env` memory size.** The VM's memory is too small for a full `nix flake check` of
   cosmo.
7. **Where new knowledge-graph nodes go.** See the proposal in [AGENTS.md](../AGENTS.md).
