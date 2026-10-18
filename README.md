# the-valley

the-valley is a software change, verification, and planning system for teams where humans and
autonomous agents build together. Like a valley, it is the low place where streams of work meet and
settle into `main`.

## How a change moves

1. **Author** — A person or agent commits work locally, inspects the checks the change requires
   (`valley checks`), and marks the change **ready** (`valley ready`).
2. **Review** — An operator inspects ready changes (`valley pending`, `valley review <change>`).
   When a change has an earlier **approved version**, `valley review` compares the active version
   against it: a clean restack prints a short summary with any landings between the two bases and
   pages nothing; a version that changed files pages the comparison (the files whose change differs,
   the range-diff, and the shown version's diff of those files); and when no comparison is
   available, it pages the full diff (`[v]iew full diff` is available at the prompt in every case).
3. **Approve** — A principal holding the `request` grant approves the change
   (`valley approve <change>`, or `[a]pprove` inside `valley review`), running and signing the
   required checks and writing an integration request that binds that exact commit for integration.
4. **Integrate** — When the host's integrator is enabled and `main` is protected, the integrator
   verifies signed check evidence against the two-layer CUE policy (instance floor + optional
   project policy), integrates the change into `main`, countersigns the evidence with a transfer
   statement, and writes an outcome record (`land`, `stale`, `reject`, `refused-not-ready`,
   `refused-merge`, `voided-withdrawn`, or `failed`).

## Quick tour

```sh
valley plan                     # view the live plan graph (knots)
valley checks                   # list checks required for your diff
valley ready --stamp            # stamp a Change-Id, push, and mark v1 ready
valley pending                  # list ready changes and what each awaits
valley review kqxm              # inspect the diff (and interactively approve)
```

## Prerequisites and current limitations

- **Prerequisites:** On the client, the bundled environment from this flake (`nix develop` or the
  flake package, providing `valley`, `valley-go`, `attest`, `cue`, `git`, `ssh`, `coreutils`, and
  `less` for interactive review) and `nix` with flakes enabled; on the server, a NixOS host running
  `services.valley`.
- **Configuration vs. guarantees:** Sealing `main` against direct SSH pushes, restricting who holds
  the `request` grant, and enabling the integrator or identity compiler are host configuration
  choices in `valley.cue` and `services.valley`.
- **Not built yet:** The automatic host restacker service (`restack` reports are validated and
  displayed, and the read-only planner `valley-restacker once --report-only` is implemented, but the
  write pass and host service are not built yet, so authors restack manually with `valley rebase`),
  automatic approval carry across restacks, skipping already-attested checks during
  `valley approve`, hardware-key (`sk-*`) touch-bound approval envelopes, and crash-safe atomic
  multi-ref landing transactions.
- **Finding the live plan:** The project's roadmap and open questions live in the plan graph
  (`refs/the-valley/knots`), viewed by running `valley plan` against a clone of the valley host. The
  host's mirror publisher sends only `main` and tags (`refs/heads/main` and `refs/tags/*`), so
  cloning from a push mirror (such as GitHub) does not fetch the host's plan graph or change
  records.

## Documentation

- **[Concepts and glossary](./docs/concepts.md)** — Why the-valley is shaped this way, core
  concepts, and every term defined.
- **[Architecture](./docs/architecture.md)** — Component design, verification policy, identity
  compilation, and executable checks.
- **[Security](./docs/security.md)** — Threat model, trust boundaries, and rollout gates.
- **[Archive](./archive/)** and **[Research](./research/)** — Historical design exploration
  (`dcr-*`, `ida-*`, `bd-*`, `oc-*`) and foundational notes.
