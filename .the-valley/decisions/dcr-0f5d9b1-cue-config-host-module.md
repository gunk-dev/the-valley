---
type: decision
id: dcr-0f5d9b1
status: decided
title: the-valley owns its host module; CUE owns the domain model, Nix installs it
created: 2026-07-06
---

# CUE config and host-module ownership

Four decisions, taken together, shape the repo's first shipped artifacts (the schema and the NixOS host module):

1. **the-valley exports the host module; consumers install and configure it.** The flake's `nixosModules.valley-host` is the implementation; consumer machines (cosmo's hosts) enable it and supply machine integration only — data directory, unix user, SSH keys. This supersedes the cosmo-owned approach of cosmo PR #601, where the module lived in cosmo: the thing that defines what a valley host *is* belongs with the-valley, not with any one machine's infra repo.

2. **The canonical config format is CUE, independent of Nix.** The domain model must be legible to agents and tools in any runtime; Nix is one installer, never the owner of the model. CUE is already the committed schema language for event schemas ([roadmap, cross-cutting threads](../../design/roadmap.md#cross-cutting-threads)), so config and events share one schema language. The module vets and exports the CUE at build time; if it needs the data in Nix, it comes from the `cue export` — Nix never redefines the schema.

3. **The declaration unit is a project, with git as one nested store type.** `#Project` has a `git` store (the only one today), not a top-level repo. This keeps the-valley from hard-coding a commitment to being git-only that the design deliberately does not make.

4. **Push mirrors are the one connective mechanism; per-project access is deferred.** Public exposure and the migration's dual-push ([S1](../../design/user-scenarios.md#s1--my-repos-live-on-my-infrastructure-and-i-can-never-lose-them), [oc-9949561](../outcomes/oc-9949561-push-replication.md)) are the same thing: per-project declared push mirrors, best-effort, deletions propagated. Per-project *access* is deliberately absent from both CUE and Nix until S5 (first collaborator): with a single git user and git-shell it is not honestly enforceable without forced-command machinery, and offering the option would misrepresent the security boundary.
