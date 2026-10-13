---
type: decision
id: dcr-5da1f36
status: decided
title: "A project is one Git repository"
created: 2026-07-06
source: ratified 2026-07-06
---

# A project is one Git repository

A valley project is one Git repository. The declared project name is also the repository name, and
its store is one bare repository named `<project>.git`.

This gives a project one name, one clone operation, one knowledge graph, and one migration unit. The
choice keeps the initial model simple.

Projects containing several repositories, or no repository, remain outside the current scope. A
concrete [user scenario](../../design/user-scenarios.md) must justify expanding that boundary.

[[ida-3e87f5c]](../ideas/ida-3e87f5c-self-describing-projects.md) explores how the repository
carries the project's identity and description.
