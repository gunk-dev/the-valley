# flow

flow is the agent-run client that `valley flow` runs. You talk to an interactive coordinator (Claude
Code, Codex or agy) in a session flow starts. When work can run in parallel, you or the coordinator
launch autonomous agents, each in its own git worktree and its own window of a detached tmux
session. flow records every run, formats its output, and finalizes what it pushed. It follows the
result through GitHub's review and CI or a valley's change states, dispatches fix runs where it can,
and reports events a coordinator can wait on. Approval stays with the operator.

## Install

`valley flow` runs a separate program, `valley-flow`, installed with its package; without it on
`PATH`, `valley flow` prints one line and exits 127. This repository's flake offers the client as
two packages of one program, `klaus` and `valley-flow`, for `x86_64-linux` and `aarch64-darwin`.
Install `klaus`, the one self-update builds, from your own repository URL, for example:

```bash
nix profile add 'git+ssh://git@valley-host/~/the-valley#klaus'
```

In a checkout, `nix build .#klaus` builds it. `valley flow --version` prints
`valley flow version <v>`, where `<v>` is `0.1.0-<short commit>`, `0.1.0-<short commit>-dirty` for a
tree with uncommitted changes, or `0.1.0-unknown` when nix knows no commit. A host can follow a
branch with [`valley flow self-update`](./docs/guide.md#keeping-flow-up-to-date).

```bash
valley flow                               # resume the latest coordinator session or start one
valley flow new                           # start a fresh coordinator session
valley flow launch --prompt-file brief.md # launch an agent run
valley flow dashboard                     # follow runs, changes and jobs
```

## Runtime prerequisites

The package supplies none of the programs flow runs; they come from `PATH`:

- `git` 2.30 or later, `tmux` (sessions run inside tmux) and `bash`;
- at least one authenticated agent CLI: `claude`, `codex` or `agy`;
- `valley` to run `valley flow` (and for repositories that deliver to a valley), and `gh` for
  repositories on GitHub;
- `nix` for `valley flow self-update`, the default formatter and nix dev shells;
- `ssh` and `rsync` for runs on a sandbox host;
- `ps` and `lsof` where there is no `/proc`, as on macOS, which ships both.

## Documentation

- [Guide](./docs/guide.md): setup and everyday journeys.
- [Reference](./docs/reference.md): commands, configuration, files, events and constraints.
- [Design](./docs/design.md): how runs, the pipeline and valley delivery work, and the trust
  boundaries.
- [Development](./docs/development.md): building and testing flow.
- The repository's rules: [AGENTS.md](../AGENTS.md).
