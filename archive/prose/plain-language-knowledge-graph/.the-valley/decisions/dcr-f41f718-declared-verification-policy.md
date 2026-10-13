---
type: decision
id: dcr-f41f718
status: decided
title: "Compose verification policy from a valley floor and project requirements"
created: 2026-07-31
source: cosmo-readiness design pass, 2026-07-31
---

# Compose verification policy from a valley floor and project requirements

Verification policy maps changed paths to required checks. It is declared in CUE and combines a
valley's shared policy, called the **floor**, with the project's own requirements. A project can add
requirements and override optional defaults. It cannot remove a mandatory requirement from the
floor.

[schema/verification.cue](../../schema/verification.cue) defines the format, and
[examples/policy/](../../examples/policy/) demonstrates composition.
[[ida-1ec03b1]](../ideas/ida-1ec03b1-path-scoped-verification-policy.md) establishes that the
required checks come from the actual diff.

## Policy layers

The valley repository carries the floor and project-type templates. Qinling serves this role for the
current deployment. Each project carries a separate policy directory defining its additional classes
and checks.

Both directories use the same schema. CUE unification combines their constraints, so the effective
value must satisfy both layers.

The floor should remain small. It expresses requirements the valley applies to every project it
owns. Most project-specific requirements belong in the project's policy or in templates it selects.

Policy belongs to the valley and project, independent of which hosts run them. Placing verification
policy in a host declaration would allow two hosts serving the same valley to disagree about its
rules. The deployment boundary is described in
[[dcr-9b5da04]](dcr-9b5da04-hosts-serve-isolated-valleys.md).

## Mandatory requirements and optional defaults

CUE represents the distinction directly:

- A concrete `true` requirement in the floor cannot unify with a project's `false`.
- A default such as `bool | *true` applies unless the project supplies another permitted value.
- Open structs allow projects to add classes and checks.
- Project-type definitions supply reusable policy fragments that a project selects and refines.

A class's required checks, path patterns, and the `unclassified` requirements are sets represented
as structs keyed by name. Composition must combine entries by name; positional lists would not
provide that behavior.

When both layers contribute to the same class, its enabled path patterns and requirements combine. A
project cannot remove a concrete floor pattern to avoid the checks attached to it.

## Selecting required checks

Class matching is a set operation. For each changed path, find every class it matches. The required
check set is the union of those classes' enabled requirements.

Classes can overlap. A knowledge node may require both knowledge validation and prose formatting. A
change touching prose and code requires the checks for both; no separate mixed-change rule is
needed.

Every policy must explicitly declare `unclassified`, which determines the checks for a path matching
no class. This makes uncovered paths an intentional policy choice.

Each required check name must exist in the composed catalogue. Unknown names fail validation.
Otherwise a misspelling could silently remove a check.

Change signatures are a structural integration requirement. They are not selected by path class.
[[dcr-9c75283]](dcr-9c75283-substrate-adoption-and-the-core.md) separately defines the core approval
requirement and the policy exemptions available for ordinary changes.

## Where policy is read

The floor is read from the valley repository's integrated tip. A project cannot substitute a
vendored copy or choose an old floor revision. The provenance of the input is what makes the floor
mandatory; CUE cannot determine where a file came from.

The project's policy is read from the target branch's integrated tip. A proposed change therefore
cannot supply the policy used to admit itself.

Weakening a valley requirement needs an integrated change in the valley repository under the rules
that already govern that repository. Templates may be pinned because they are optional defaults a
project could otherwise write itself.

[[ida-5006606]](../ideas/ida-5006606-a-project-names-its-owning-valley.md) defines how a project
identifies its owning valley.
[[ida-a9e274c]](../ideas/ida-a9e274c-mandated-checks-come-from-the-instance.md) explains how
valley-mandated check implementations are supplied.
[[bd-eaefe82]](../bugs/bd-eaefe82-check-definitions-come-from-the-branch.md) tracks the separate
risk of trusting check definitions from a submitted branch.

## Directories and composition

Each policy layer is a CUE package. All files in its directory participate in unification; no extra
manifest is needed.

The valley determines which floor documents are loaded. Projects choose which template definitions
to embed. Concrete fields apply when loaded; definitions remain inactive until selected.

Two files can contribute requirements to the same class. Incompatible constraints fail validation
with the conflicting fields and source locations. File order must not select a winner.

The directory convention also makes policy inspectable with ordinary repository tools. It does not
give a project authority to edit the valley's floor.

## Missing layers and bootstrap

A missing project layer adds no project-specific requirements. The floor and core still apply. This
permits a project's first policy change to pass through normal integration.

[[dcr-9c75283]](dcr-9c75283-substrate-adoption-and-the-core.md) defines the adopted bootstrap
design: a valley with no floor operates under the core's physical-approval requirement. An empty
floor cannot remove that requirement.

The core's approval mechanism must be available before this bootstrap behavior is enabled. The
current policy tooling requires the valley policy directory and reports its absence. Operators must
not interpret a missing or unreadable policy input as permission to integrate without checks.

## Validation examples

The worked policy in [examples/policy/](../../examples/policy/) demonstrates a mandatory
prose-format check, a second file adding link checking to the same class, and a documentation
template offering knowledge lint as a default. The project adds its own shell-checking class and
declines the optional knowledge check.

This exercises the important composition cases:

- Adding project requirements succeeds.
- Declining an optional default succeeds.
- Declining a mandatory check or removing mandatory path coverage fails.
- Selecting an undefined template or check fails.
- Invalid names, unsupported runners, unknown fields, and unsafe path patterns fail.
- An empty project declaration composes to the floor.

These examples establish schema behavior. Live enforcement also depends on the policy resolver,
check execution, evidence verification, and protected-ref enforcement.

## Deployment and verification

[bin/valley](../../bin/valley) provides `valley checks` to report the checks required by a diff. The
review workflow can run those checks, sign the results, and submit an integration request. The
integrator evaluates the request against governing policy and evidence before updating the protected
target.

The initial live exercise uses Qinling and the-valley, with overlapping prose and knowledge classes.
Useful observations are:

- A knowledge-only change requires knowledge lint and prose formatting.
- A change confined to an ordinary Markdown document requires prose formatting.
- An attempt to disable mandatory formatting fails composition.
- Invalid node frontmatter fails the knowledge check with the file and field identified.
- An uncovered path receives the declared `unclassified` requirements.

These observations check both missing requirements and unnecessary requirements. Reading a plausible
policy file is insufficient evidence that the full path works.

## Migration consequences

A project clone contains its own policy but needs the current valley floor to determine its full
requirements. This is the cost of allowing a valley to impose rules across projects. Pinned optional
templates preserve part of the offline workflow.

Cosmo additionally needs policy for Nix formatting and its five-machine build matrix. Check latency
remains a practical concern. Migration also requires verified policy provenance and enforced
integration; a command that merely reports required checks cannot replace the existing gate.

[[oc-87deec8]](../outcomes/oc-87deec8-valley-can-host-cosmo.md) tracks those migration requirements.
[[ida-7638082]](../ideas/ida-7638082-host-has-no-check-runner.md) inventories the checks that must
be accounted for. The system's own policy changes remain part of the history described in
[self-transparency.md](../../design/self-transparency.md).
