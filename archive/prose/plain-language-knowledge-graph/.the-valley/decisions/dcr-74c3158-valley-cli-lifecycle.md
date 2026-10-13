---
type: decision
id: dcr-74c3158
status: decided
title: "Keep the CLI in this repository until an explicit graduation decision"
created: 2026-07-13
source: ratified 2026-07-13
---

# Keep the CLI in this repository until an explicit graduation decision

The `valley` CLI lives at [bin/valley](../../bin/valley) because its commands apply to any valley
project. It can run from a checkout or through the flake package with `nix run .#valley`.

The initial implementation is a shell script.
[[ida-b9f646c]](../ideas/ida-b9f646c-nix-backend-not-substrate.md) describes the portability goal
behind that choice.

## Conditions for graduation

The lifecycle policy calls for moving the CLI to its own repository and a suitable implementation
language when any of these conditions holds:

- It grows beyond roughly 300 lines.
- It needs persistent state, configuration, or a daemon.
- It needs dependencies beyond Git and coreutils.
- Its behavior requires substantial testing.
- It needs nontrivial parsing or concurrency.

Graduation must be recorded as an explicit decision. These conditions are intended to keep a
convenient script from becoming difficult to understand and maintain.

[[dcr-2aa1a12]](dcr-2aa1a12-valley-cli-graduation.md) records that the dependency condition has been
reached and proposes either graduating or replacing these rules with a specification-coverage
requirement. That proposal remains undecided.
