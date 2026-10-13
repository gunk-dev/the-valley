---
type: idea
id: ida-a9e274c
status: exploring
title: "Use valley-controlled implementations for mandatory checks"
created: 2026-08-02
source: design conversation, 2026-08-02
---

# Use valley-controlled implementations for mandatory checks

When a valley requires a check, the valley must control which implementation and toolchain satisfy
that requirement. The project supplies the tree to be checked. A matching check name in the
project's flake is not enough.

Otherwise two projects could define different behavior under the same required name, or a proposed
change could replace the check with one that always succeeds.

## Distribution

A flake input can distribute a reusable check. The project instantiates it over its source tree,
while the supplying flake pins the toolchain. An entry point taking a system and source tree keeps
consumers from supplying a different package set.

Projects remain free to add checks of their own.
[[dcr-f41f718]](../decisions/dcr-f41f718-declared-verification-policy.md) determines which
requirements are mandatory and which are selected by the project.

## Governing revision

Distributing the check through a project input does not by itself enforce the current valley
requirement. A project can retain an old input revision.

The integration path must resolve the mandatory implementation from the valley's governing source,
as required by
[[dcr-439b771]](../decisions/dcr-439b771-integration-occ-over-content-addressed-evidence.md).
[[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md) tracks the
implementation risk of trusting definitions from the submitted branch.

Updating one supplied check can reduce drift across projects, but the system still needs to
establish which revision the evidence used.

## Consequences

The check's source and dependencies must be available wherever verification runs. Reusing the
valley's flake can make the valley repository a build dependency for its projects.

A flake is the current distribution mechanism.
[[ida-b9f646c]](ida-b9f646c-nix-backend-not-substrate.md) requires the check contract to remain
expressible with other runners. [[ida-1ec03b1]](ida-1ec03b1-path-scoped-verification-policy.md)
describes path-based selection and reuse of the knowledge lint.
