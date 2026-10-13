---
type: idea
id: ida-3e87f5c
status: adopted
title: "Keep a project's description with its repository"
created: 2026-07-06
source: ratified 2026-07-06
---

# Keep a project's description with its repository

A project's declaration should travel with the repository, as its knowledge graph already does.
Hosts would declare which projects they serve, while the project would retain its identity and
description when it moves.

[[dcr-5da1f36]](../decisions/dcr-5da1f36-project-is-repo.md) defines the current project as one Git
repository. Its local declaration can describe its own policy, but determining all required checks
also needs the current policy of its owning valley; see
[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md).

The declaration's location and schema, the way a host references it, and name resolution across
hosts still need design. [[ida-8482624]](ida-8482624-federation-groups.md) explores the cross-valley
part of resolution.
