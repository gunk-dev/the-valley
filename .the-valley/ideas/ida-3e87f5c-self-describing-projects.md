---
type: idea
id: ida-3e87f5c
status: adopted
title: Self-describing projects — identity transcends the host
created: 2026-07-06
source: ratified by patflynn
---

# Self-describing projects — identity transcends the host

A project's identity transcends the host that serves it. Today the host's `valley.cue` declares
which projects it serves; the direction is **self-describing projects** — a project's own
declaration travels in its store, the way the `.the-valley/` knowledge graph already does, and host
config shrinks to a serving list.

Open design questions, named here but not resolved:

- Where the in-store declaration lives, and its schema.
- How a host's serving list references a project.
- How project names resolve across hosts — a federation question; see [[ida-8482624]].

## Related

- The store this declaration travels in: [[dcr-5da1f36]]
  ([decisions/dcr-5da1f36-project-is-repo.md](../decisions/dcr-5da1f36-project-is-repo.md))
- Cross-host resolution belongs to the federation layer: [[ida-8482624]]
  ([ida-8482624-federation-groups.md](./ida-8482624-federation-groups.md))
