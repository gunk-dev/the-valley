# klaus

klaus is a command-line client that launches and follows coding-agent runs. You talk to an
interactive coordinator (Claude Code, Codex or agy) in a session klaus starts. When work can run in
parallel, you or the coordinator launch autonomous agents, each in its own git worktree and its own
window of a detached tmux session. klaus records every run, formats its output, and finalizes what
it pushed. It follows the result through GitHub's review and CI or a valley's change states,
dispatches fix runs where it can, and reports events a coordinator can wait on. Approval stays with
the operator.

## Install

klaus is the package `packages.x86_64-linux.klaus` of this repository's flake (Linux x86_64 only).
Install it from your own repository URL, for example:

```bash
nix profile add 'git+ssh://git@valley-host/~/the-valley#klaus'
```

In a checkout, `nix build .#klaus` builds it. `klaus --version` prints `klaus version <v>`, where
`<v>` is `0.1.0-<short commit>`, `0.1.0-<short commit>-dirty` for a tree with uncommitted changes,
or `0.1.0-unknown` when nix knows no commit. A host can follow a branch with
[`klaus self-update`](./docs/guide.md#keeping-klaus-up-to-date).

```bash
klaus                                  # resume the latest coordinator session, or start one
klaus new                              # start a fresh coordinator session
klaus launch --prompt-file brief.md    # launch an agent run
klaus dashboard                        # follow runs, changes and jobs
```

## Runtime prerequisites

The package supplies none of the programs klaus runs; they come from `PATH`:

- `git` 2.30 or later, `tmux` (sessions run inside tmux) and `bash`;
- at least one authenticated agent CLI: `claude`, `codex` or `agy`;
- `gh` for repositories on GitHub, and `valley` for repositories that deliver to a valley;
- `nix` for `klaus self-update`, the default formatter and nix dev shells;
- `ssh` and `rsync` for runs on a sandbox host.

## Documentation

- [Guide](./docs/guide.md): setup and everyday journeys.
- [Reference](./docs/reference.md): commands, configuration, files, events and constraints.
- [Design](./docs/design.md): how runs, the pipeline and valley delivery work, and the trust
  boundaries.
- [Development](./docs/development.md): building and testing klaus.
- The repository's rules: [AGENTS.md](../AGENTS.md).
