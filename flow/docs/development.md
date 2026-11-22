# Developing flow

flow, the agent-run client that `valley flow` runs, is the Go module `the-valley/flow` in `flow/`,
with its entry point in `cmd/klaus` and its packages under `internal/`. Changes to it are changes of
this repository and follow its rules ([AGENTS.md](../../AGENTS.md)): one commit per change, build
output in a scratch directory outside the tree.

## Building

```bash
nix build .#valley-flow
```

The flake's `valley-flow` package, for `x86_64-linux` and `aarch64-darwin`, builds `cmd/klaus`,
installed as `bin/klaus` and `bin/valley-flow`, with the version stamped through
`-X the-valley/flow/internal/cmd.version=<v>`: the CLI version of `nix/packages.nix`,
`0.1.0-<short commit>`, with `-dirty` for a tree with uncommitted changes, or `0.1.0-unknown` when
nix knows no commit. The package's source holds no tests and no prose, and the package runs no
tests: the checks below do. It depends on none of the programs flow runs. The `klaus` package puts
symlinks to `valley-flow`'s two executables in a store path named `klaus-<v>`. Self-update builds
`klaus`.

## Testing

flow's tests run only through nix checks, each built on its own in the build sandbox:

```bash
nix build --option sandbox true --no-link .#checks.x86_64-linux.flow-build
nix build --option sandbox true --no-link .#checks.x86_64-linux.flow-unit
nix build --option sandbox true --no-link .#checks.x86_64-linux.flow-vet
nix build --option sandbox true --no-link .#checks.x86_64-linux.flow-e2e
```

| Check        | Runs                                                                              |
| ------------ | --------------------------------------------------------------------------------- |
| `flow-build` | both packages; each one's `klaus --version` must print `klaus version <v>`        |
| `flow-unit`  | `go test ./...`, with git, tmux, the newest nix, `ps` and `lsof`                  |
| `flow-vet`   | `go vet ./...`                                                                    |
| `flow-e2e`   | `go test -tags e2e ./e2e/...` (slow), with git, tmux, bash and the valley command |

A change touching `flow/` requires all four; `valley checks` lists what a change requires. The
checks rebuild the package over the whole module, tests included, with the package's own vendored
modules: one fixed-output derivation, `klaus-go-modules`, pinned by `vendorHash` in
`nix/packages.nix` and resolved from the whole module with its tests. A change to `go.mod` or
`go.sum` changes that hash.

No check runs flow on a Mac. `valley-darwin-build` compiles, links and vets it for `darwin/arm64`
from `x86_64-linux`, over the same vendored modules.

The checks set `KLAUS_TEST_TOOLS=required`: a test that would skip for a missing tool (tmux, nix,
valley, or a valley lacking a verb it needs) fails instead, so a scenario that did not run cannot
pass. The tmux checks clear `TMUX` and `TMUX_PANE`, put a private `TMUX_TMPDIR`, `HOME` and XDG
directories under the build's temporary directory, and fail if a socket is left under `TMUX_TMPDIR`
(which means a test reached tmux through the default socket directory). `flow-unit` runs nix against
a dummy store, so the test that has nix parse self-update's flake references needs no store; it
takes the newest nix because nix 2.31 refuses `git+git://` references. `flow-e2e` runs this
repository's `packages.valley-unstamped` for its restack and landing scenarios.

## Dependencies

`flow/` is the one module of this repository that may import third-party Go modules: Bubble Tea, Lip
Gloss, `charmbracelet/x/ansi`, fsnotify, Cobra and pflag, with their indirect dependencies. Nothing
outside `flow/` imports from it, directly or transitively, and nothing a host runs with authority
does; the `go-dependencies` check enforces this. No vendor directory or `go.work` is committed.

## Layout

| Package                                                           | Holds                                                      |
| ----------------------------------------------------------------- | ---------------------------------------------------------- |
| `internal/cmd`                                                    | the Cobra commands, the dashboard TUI, launch and finalize |
| `internal/run`                                                    | run state and the session store                            |
| `internal/backend`                                                | the claude, codex and agy CLI contracts                    |
| `internal/stream`                                                 | the JSONL stream formatter and check timing                |
| `internal/scan`                                                   | the sensitivity scanner                                    |
| `internal/git`, `internal/tmux`, `internal/nix`                   | git worktrees, hooks and the data ref; tmux panes; nix     |
| `internal/config`                                                 | configuration, prompt templates and worker rules           |
| `internal/pipeline`                                               | the GitHub pipeline's state machine                        |
| `internal/github`, `internal/webhook`                             | GitHub queries; the webhook receiver                       |
| `internal/valley`                                                 | the `valley` CLI: ready, unready, changes, knots           |
| `internal/integration`                                            | whether a pushed branch landed                             |
| `internal/restack`, `internal/checksfix`                          | restack-conflict and failed-check dispatch                 |
| `internal/draft`                                                  | budget pause                                               |
| `internal/review`, `internal/prose`                               | linters, peer and cross-model review; the prose pass       |
| `internal/consult`, `internal/jobs`                               | consults; job records                                      |
| `internal/event`, `internal/report`                               | the event log; `valley flow report time`                   |
| `internal/project`, `internal/projectsync`, `internal/houserules` | the registry; syncing; house rules                         |
| `internal/selfupdate`                                             | `valley flow self-update`                                  |

Pure-logic packages are unit-tested directly; wrappers around git and tmux are tested against
temporary repositories and private tmux servers.

## Keeping tests away from real tmux

Agent panes export `KLAUS_SESSION_ID`, so everything an agent runs in its worktree, `go test`
included, inherits the coordinator's session ID. A test that handed a real tmux client a session
name derived from it would aim at the live `klaus-agents-<session-id>` session every running agent
lives in. Three layers prevent that:

- Unit tests in `internal/cmd` use `isolatedTmux(t)`, an in-memory tmux client with a pinned
  `KLAUS_SESSION_ID`; `TestUnitTestsNeverUseRealTmux` fails if a test there reaches for the
  exec-backed client.
- Every package whose code can reach the real client (`internal/cmd`, `internal/run`,
  `internal/pipeline`, `internal/tmux`) runs its tests through `tmuxtest.Main`, which starts them as
  launch starts a worker: no `TMUX`, `TMUX_PANE` or `KLAUS_TMUX_SOCKET`, and a `TMUX_TMPDIR` of
  their own. A test that wants a server starts one with `tmux -S <its temp dir>/…`.
- The checks fail on any socket left in the default socket directory.

## The end-to-end suite

`e2e/`, behind the `e2e` build tag, drives the real `klaus` binary (built once by `TestMain`)
against a real, isolated tmux server and real git repositories: pane creation, the
`worker | tee | _format-stream; _finalize` pipeline, worktree lifecycle and run state.

| Concern   | Isolation                                                                                                                                                                            |
| --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| tmux      | a private server per test on `tmux -S <tmpdir>/tmux.sock`; flow reaches it through `$TMUX`; its panes get a per-test `TMUX_TMPDIR` and no `KLAUS_TMUX_SOCKET`; killed in `t.Cleanup` |
| state     | `HOME` is a per-test temporary directory, so `~/.klaus` lands under it; the harness's session exists there before the test runs                                                      |
| externals | a per-test bin directory first on `PATH` holds klaus and fake `claude` and `gh`; git, tmux and (where a scenario asks for it) `valley` stay real                                     |
| git       | a sandbox repository with a bare local `origin`, so pushes need no network                                                                                                           |

A unique socket and HOME per test let tests run in parallel. In the check there is no network, no
terminal and no `/usr/bin/env`: `WriteStub` runs a `#!/usr/bin/env bash` stub under the `bash` on
`PATH`, and other scripts need `#!/bin/sh`.

tmux runs pane commands through its default shell, and a login shell can rewrite `PATH` and hide the
stubs, so the harness starts the server with `-f /dev/null` and points `default-shell` at a wrapper
that forces a fixed `PATH` and `HOME`.

`TestWorkerCannotKillTheSharedServer` launches a worker of each backend that runs
`tmux kill-server`, `TMUX_TMPDIR=<dir> tmux kill-server` and `tmux ls`. It checks that the harness's
server and its first session survive, the worker saw no `TMUX` and its own `TMUX_TMPDIR`,
`klaus status` in the worker still found its pane through `KLAUS_TMUX_SOCKET`, and the run
finalized. A fake worker runs tmux only when every `TMUX_TMPDIR` it passes exists, since tmux falls
back to `/tmp` for a missing one.

### The fake `claude`

`_finalize` parses the run's captured JSONL. The stub emits an `assistant` event whose text holds a
GitHub URL matching `https?://github\.com/[^\s"<>\]]+/pull/\d+` (placeholder slugs such as
`owner/repo` are rejected) and a `result` event with `total_cost_usd`, `duration_ms` and
`session_id`, which become the run's cost, duration and URL. The sandbox's origin is a local path,
so its runs deliver by push and take no URL from the log; a test that wants GitHub delivery calls
`UseGitHubOrigin` before launching. The stub blocks until `ReleaseClaude`, so a test can observe the
live pane before finalize tears it down. `WriteDefaultClaudeOutput` replaces its output, and
`WriteStub` replaces any stub.

### Adding a scenario

```go
//go:build e2e

func TestSomething(t *testing.T) {
    t.Parallel()
    h := NewHarness(t)

    res := h.RunKlaus("launch", "do the thing")
    // assert on res, h.ReadState(id), h.ListPanes(), h.PaneTitle(id), ...
}
```

The `Harness` helpers in `harness_test.go`:

- `RunKlaus(args...)`, `RunKlausIn(dir, args...)`: run the binary in the isolated environment, from
  the sandbox repository or a given directory (such as `h.E2EDir`, which is no repository);
- `RegisterProject(ref, path)`: register a checkout in the temporary HOME's registry;
- `ReadState(id)`, `WaitForState(id, pred, timeout)`, `RunIDs()`, `SeedState(state)`: run state;
- `ListPanes()`, `PaneExists(id)`, `PaneTitle(id)`: the isolated tmux server;
- `WaitForClaudeStart`, `ReleaseClaude`, `ClaudeArgv`, `GHArgv`: coordinate the stubs;
- `WriteStub(name, script)`, `WriteDefaultClaudeOutput(jsonl)`, `UseGitHubOrigin()`: shape the
  externals.

A scenario that needs a tool or a valley verb calls `skipWithout` when it is missing, which fails
instead of skipping under `KLAUS_TEST_TOOLS=required`.
