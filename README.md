# the-valley

the-valley is a software change, verification, and planning system for teams where humans and
autonomous agents build together. Like a valley, it is the low place where streams of work meet and
settle into `main`.

## How a change moves

1. **Author** — A person or agent commits work, inspects the
   [checks](./docs/concepts.md#checks-and-evidence) the
   [change](./docs/concepts.md#a-change-ready-approve-submitted) **requires** (`valley checks`), and
   marks it **ready** (`valley ready`).
2. **Review** — An operator lists ready changes (`valley pending`) and inspects one
   (`valley review <change>`).
3. **Approve** — A [principal](./docs/concepts.md#principals-and-grants) allowed to approve approves
   the change (`valley approve <change>`, or `[a]pprove` in `valley review`), running and signing
   the required checks and writing an integration request for that commit.
4. **Integrate** — When enabled on the
   [host](./docs/concepts.md#the-host-the-hook-and-the-integrator), the **integrator** checks each
   request, verifies a ready change's signed check evidence against the verification policy, and
   records an outcome. When the change passes and integrates cleanly into `main`, the integrator
   signs the transfer, advances `main`, and stores the signed evidence so the change is
   **submitted**.

## Quick tour

```sh
valley plan                     # view the live plan graph (knots)
valley checks                   # list checks required for your diff
valley ready --stamp            # stamp a Change-Id, push, and mark v1 ready
valley pending                  # list ready changes and what each awaits
valley review kqxm              # inspect the diff (and interactively approve)
```

## Configuration and current status

- **Configuration vs. guarantees:** Sealing `main` against direct SSH pushes, choosing which
  principals may approve, and enabling the integrator or identity compiler are host choices
  (`valley.cue`, `services.valley`); push mirrors receive only `main` and tags, not
  [project storage](./docs/concepts.md#records-project-storage-the-plan-graph-and-knots).
- **Not built yet:** For restacking, the rebase library behind `valley rebase` and `[b]ase` in
  `valley review`, the host's `restack` grant and report validation, and the read-only planner
  (`valley-restacker once --report-only`) are built, while the service that rebases ready changes
  automatically after each landing is not built or running yet. Automatic approval carry across
  restacks, skipping already-attested checks in `valley approve`, hardware-key (`sk-*`) touch-bound
  approval envelopes, and atomic multi-ref landing transactions are also not built yet.

## Documentation

- **[Concepts and glossary](./docs/concepts.md)** — Core concepts and every term defined.
- **[Guide](./docs/guide.md)** — Setup and everyday journeys for authors, reviewers, planners,
  administrators, and contributors.
- **[Architecture](./docs/architecture.md)** — Component design, policy, identity, and checks.
- **[Security](./docs/security.md)** — Threat model, trust boundaries, and rollout gates.
- **[Archive](./archive/)** and **[Research](./research/)** — Historical nodes and research notes.
