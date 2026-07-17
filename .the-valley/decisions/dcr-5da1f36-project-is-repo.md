---
type: decision
id: dcr-5da1f36
status: decided
title: A valley project is 1:1 with a git repo
created: 2026-07-06
source: ratified by patflynn
---

# A valley project is 1:1 with a git repo

A **project** — the unit of declaration — is one git repo. The project's single store is one bare
repo named `<project>.git`; project name and repo name coincide.

**Rationale.** Fundamentally and ergonomically simpler: one name, one clone, one knowledge graph,
one migration unit. This is a conscious choice for simplicity's sake, not a claim that a repo is the
only way people model projects.

**Consequence.** Multi-repo and repo-less projects are out of scope until a rung on the
[scenario ladder](../../design/user-scenarios.md) demands them.

## Related

- The identity direction this store carries: [[ida-3e87f5c]]
  ([ideas/ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md))
