---
type: decision
id: dcr-f41f718
status: decided
title: Verification policy is a declared document, composed from an instance floor and a project layer
created: 2026-07-31
source: cosmo-readiness design pass, 2026-07-31
---

# Declared verification policy

Verification policy — the mapping from path classes to required checks — is a declared CUE document.
It is composed from two documents, not one. The group's instance repository carries a mandatory
floor and a set of project-type templates; the project's own repository carries everything above the
floor. The effective policy is the unification of the two. The schema is
[schema/verification.cue](../../schema/verification.cue), with a worked example at
[examples/policy/](../../examples/policy/), so the shape can be read rather than imagined.

The design this gives a home to is already written down. [[ida-1ec03b1]]
([ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))
establishes that required checks are derived from the actual tree diff and never from a
contributor's claim, that `.the-valley/**` takes signature plus knowledge lint while code takes the
full suite and a mixed diff takes the max, and — the sentence this node acts on — that the policy
itself is data, so it is a CUE document versioned in-repo. This node supplies the schema and the
answer to _in which repo_.

## Why this is needed now

cosmo gates every change through GitHub Actions. Its `.github/workflows/ci.yml` runs
`nixfmt --check` and a `nix build` matrix over classic-laddie, makers-nix and johnny-walker, and
branch protection makes that gate the condition of merging. S1 direct-push mode has no pull request
object, so migrating cosmo to a valley instance does not weaken that gate — it deletes it. The
repository that builds five machines would land changes with nothing checking them, and nothing on
the instance runs a check today. Declared verification policy is the piece that has to exist before
cosmo can be hosted here. The wider readiness picture is tracked separately as the cosmo-readiness
outcome node, in flight on branch `oc/cosmo-readiness`.

## The two layers

Both layers are the same schema and the same kind of document: a CUE document versioned in the
repository it belongs to. The weight sits in the project layer, and the floor is deliberately small.

The **instance layer** is the group's policy. A group has exactly one instance — that binding is
settled in [[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) and in
[architecture.md § federation](../../design/architecture.md#federation-the-group-is-the-unit) — so
the instance repository is the group's repository, and for the gunk-dev instance that is qinling. It
states the minimum: the checks no project in the group may decline, plus project-type templates that
give a project of a known type a sensible starting set.

The **project layer** is the project's own policy, a versioned document in its own tree. It states
everything else: the classes the project cares about, the checks it adds, and the template defaults
it declines.

The two compose by unification, which can only narrow, so the floor is a floor by construction: the
project layer can add to it and cannot subtract from it. Both layers say what; they differ only in
whether a project can answer back. That is what keeps the two halves from being two vocabularies to
learn, and what makes the composition a single `cue vet`.

The discipline this asks for is keeping the floor minimal. The floor is the minimum a group is
willing to enforce on projects it has not read; a floor that keeps absorbing checks until it
describes every class of every project has stopped being a floor.

Policy has no business at a lower layer than the instance. Group to instance is 1-to-1, but instance
to host deliberately is not: an instance is instantiable on a single machine and equally runnable as
a distributed system, with git hosting sharding across hosts and nothing in the architecture
assuming co-location. classic-laddie being the whole of the gunk-dev instance today is a fact about
deployment, not about the design. A policy field on a host would therefore be a policy field on _one
of several_ possible carriers of the same instance, free to diverge from its siblings with neither
one wrong. The host is not involved at any point, and [schema/valley.cue](../../schema/valley.cue)
carries nothing about verification.

## Mandatory versus overridable is one bit per check, in CUE's own semantics

This is why CUE suits the model rather than merely carrying it. Unification can only narrow, so the
_form_ of a value decides whether a project can answer back, and the schema needs no flag it would
have to interpret.

- A **concrete** constraint from the floor — a check required with the value `true` — cannot be
  unset. A project supplying `false` conflicts, and `cue vet` fails naming the field.
- A **template default** — written as the disjunction `bool | *true` — is overridable. It reads as
  `true` when nobody says otherwise, and a project supplying `false` unifies cleanly to `false`.
- **Open structs** mean a project may always add classes and checks, and can never remove the
  floor's.
- **Project-type templates** are policy fragments a project selects by name and then narrows, so a
  project that is an ordinary instance of its type works with very little configuration and can
  still override the non-mandatory parts or define its own.

## Everything that composes is keyed, never a list

CUE unifies lists positionally and requires equal length, so two lists that were written
independently simply conflict. Every part of the policy that has to compose across the two layers
therefore cannot be a list.

The path classes are keyed by class name rather than held in a list of class values. A class's
required checks, the `unclassified` set, and a class's path patterns are all sets written as structs
from name to `bool` rather than lists of names. When both layers name the same class, it is one
class: its coverage and its requirements are the union of what each layer contributed, and nothing
either layer made concrete can be taken back.

Keying a class's `paths` the same way is load-bearing and easy to miss. If coverage were a list, a
project could rewrite a floor class's patterns wholesale; keyed and concrete, a floor class's
coverage cannot be narrowed to a path the project never touches, which would otherwise escape the
floor everywhere else.

## Forcing comes from the floor's provenance, not from CUE

Unification on its own forces nothing. A project that vendored a copy of the floor into its own tree
and edited the copy would unify perfectly well; the copy is simply not the floor. What makes a floor
a floor is where it is read from: the instance repository's integrated tip, never the project's
tree.

Weakening the floor therefore takes two landings. Changing the floor is a change landing in the
instance repository, under that repository's own policy and its own integrator, and it is visible in
that repository's history — which is what [self-transparency.md](../../design/self-transparency.md)
asks of the system's own configuration. The schema cannot enforce this, because a schema cannot see
which file a field came from. The tool that composes a policy is what makes it true, and the schema
says so in its comments.

## The two halves resolve at different times

The mandatory floor resolves at head. A floor a project could pin is not a floor: pinning it would
make every past floor permanently available as an alternative to the current one, and weakening
would take one landing again — the project's.

Templates may be pinned. They are defaults, a project can already override them, and a stale default
costs nothing the project could not have written by hand.

## The document count is free; the enumeration is not

Each layer is described above as one document. That is how the example is written, not something CUE
imposes. A CUE package unifies any number of files, and unification is commutative and associative,
so file boundaries carry no meaning and factoring a layer across several documents is semantically
free. Two floor documents may contribute mandatory checks to the same class and compose without
complaint. Two documents that disagree on the same check fail at vet time, and the error names both
files and both line numbers, so a silent last-write-wins is not possible. That is what makes
fragmenting policy safe rather than risky.

The constraint that does exist is not the number of documents but who enumerates the set. A floor a
project can decline to load is not a floor, so floor documents are enumerated by the instance, while
template documents are selected by the project. That is the mandatory-versus-overridable distinction
above, expressed at document granularity instead of at field granularity.

Collecting policy into a standard directory is the natural expression of it, because a CUE package
already works that way: the directory is the enumeration, so no manifest is needed and nothing has
to list which files count. One directory holds both layers, because CUE's definition-versus-field
distinction does the selection. A concrete field is floor and loads unconditionally; a project-type
template written as a definition is inert until a project embeds it. Whether a policy document is
mandatory or optional is therefore visible in the syntax of the file itself. This repository already
uses the same move: [.the-valley/README.md](../README.md) describes the knowledge graph as "a
directory convention, not a system", where listing is `ls` and search is `grep` and no indexer
exists. A policy directory is that convention applied again rather than a new mechanism.

The obvious comparison is a workflow directory in a hosted forge, and it breaks in two places.
Workflow files are independent of one another; policy files unify. A new file in the floor directory
changes the effective policy of every project in the group. Contradictions fail loudly as above, but
silent addition is real and has no equivalent in the workflow-directory model. The second difference
matters more: a workflow directory has no floor. Anyone who can write it controls the checks
completely, which is precisely what this design denies a project. So the same convention is applied
twice with different authority — the floor directory in the instance repository, resolved at head,
and the project's own directory in its own tree. The drop-in property is safe only because of where
each directory lives. A project adding a document to its own directory can only narrow; a project
able to write the floor directory would be the entire gate gone.

A worked directory is at [examples/policy/](../../examples/policy/). Two floor documents contribute
to the same `prose` class — one requiring `prose-format`, one added later requiring `link-check`,
both pinning the same coverage pattern — and the composed class requires both. A `docs` template is
written as the definition `#docs`, and composing the instance directory alone exports a policy with
no `knowledge` class at all: the definition contributes nothing until the project document embeds
it. That project document embeds it, declines the template's defaulted knowledge lint, and adds a
`shellcheck` class of its own. A third floor document contradicting the second fails vet naming both
files:

```
floor.classes.prose.requires."link-check": conflicting values true and false:
    ./examples/policy/instance/floor-references.cue:19:27
    ./floor-disagree.cue:3:48
```

## What the schema commits to

Class matching is a set operation, never first-match. A change's required checks are the union of
`requires` over every class that at least one changed path matches. Classes may overlap deliberately
— a knowledge node is both knowledge and prose — and the union is the entirety of [[ida-1ec03b1]]'s
"mixed takes the max" rule. There is no separate mixed case, and no ordering to get wrong.

A path matching no class is answered explicitly, by a required `unclassified` field. The failure
this document must never have is a path that quietly requires nothing, so the policy has to state
what an uncovered path costs rather than defaulting to silence.

The signature is deliberately absent. [[ida-1ec03b1]] lists it alongside the knowledge lint because
it is enumerating what a knowledge-only diff must satisfy, but the signature is required of every
change by the structural invariant and the integrator, whatever paths the diff touches. It is not
path-scoped, so it is not policy.

Every check name a class requires must exist in the composed catalogue. A name that does not lands
in `checks` as an incomplete check and fails vet. Without this, a typo is not a vet error — it is a
check that silently stops being required.

## Evidence: the worked example

The claims above are pinned by running `cue vet`, not by inspection. The example is the policy
directory at [examples/policy/](../../examples/policy/). The instance layer makes `prose-format`
mandatory over `**/*.md` and for uncovered paths, requires `link-check` over the same class from a
second document, and offers a `docs` project type whose knowledge lint is a default. The project
layer embeds that type, adds a `shellcheck` class of its own, and declines the defaulted knowledge
lint.

Both directions were run.

```
$ cue vet -c schema/verification.cue \
    examples/policy/instance/*.cue examples/policy/project/*.cue
$ echo $?
0
```

The composed `policy` exports with `knowledge-lint` at `false` — the project's legitimate override —
alongside the floor's `prose-format` and `link-check` at `true` and the project's own `shellcheck`
class. A project that instead tries to unset a mandatory check fails, and the error names the field
and the floor document it came from:

```
$ cue vet -c schema/verification.cue examples/policy/instance/*.cue unset.cue
policy.classes.prose.requires."prose-format": conflicting values false and true:
    ./examples/policy/instance/floor-format.cue:26:29
    ./schema/verification.cue:184:16
    ./schema/verification.cue:184:42
    ./schema/verification.cue:185:9
    ./unset.cue:3:52
```

Narrowing a floor class's coverage fails the same way, at `policy.classes.prose.paths."**/*.md"`.
Selecting a project type the instance does not define is an evaluation error rather than a silent
fallback. An unknown check name, a stray top-level field, an absolute or `..` path pattern, an
unimplemented runner, an unknown field inside a class, and a malformed name all fail vet with an
error naming the field. A project document that declares nothing at all vets, and composes to the
floor exactly.

The example is evidence for the schema and nothing more. It is not the live exercise on qinling,
which is a separate step described below. The schema is unwired: it is not referenced by
[schema/valley.cue](../../schema/valley.cue), not covered by the flake's `cue-vet` check, and not
read by [nix/valley-host.nix](../../nix/valley-host.nix), so these rejections are demonstrated
rather than run continuously.

## What this costs

A project is now self-describing only above the floor, which trims [[ida-3e87f5c]]
([ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md)). A
clone of the project alone can no longer state its required set; it can state what the project adds,
and must consult the instance repository for the rest. That is a deliberate trade for the group
being able to force compliance, which is the whole reason the floor exists. The timing split
preserves most of the offline story: templates may be pinned, so the only thing a clone genuinely
cannot answer alone is the current floor, and the floor is small by design.

Enforcement belongs to the integrator, which already holds both trees and reads the policy at the
target ref's tip — the policy that is already integrated — so a change never supplies the policy
that gates it. That is consistent with the one structural invariant in
[contribute.md](../../design/contribute.md), and it means declared policy is inert data until Phase
3 ([roadmap.md](../../design/roadmap.md)); Phase 2 is where a contributor can first produce the
evidence a policy asks for. The checks a policy names are ordinary flake checks in the reference
implementation, so a project that already has checks has most of a policy — the document says which
ones apply where, not what a check is.

## The staged path: qinling first, cosmo second

Every feature built for cosmo readiness is exercised live on the gunk-dev instance, on a project
already hosted there, before cosmo depends on it. The projects on the instance today are the-valley
and qinling, and both are markdown-only. So the first exercisable class is knowledge and prose — the
formatter-idempotence check this repo already ships as a flake check, plus the frontmatter and
reference-integrity linting [[ida-1ec03b1]] describes — and not the `nix build` class cosmo
eventually needs.

qinling is also the instance repository, so the first exercise carries both layers at once: qinling
holds the floor and the templates, and the-valley holds a project policy that composes with them.

### The smallest thing that could be turned on

Four pieces, none of which is an enforcement point:

1. qinling gains the instance document — a floor requiring the prose format check over `**/*.md` and
   for uncovered paths, and a `docs` project type offering the knowledge lint — and the-valley gains
   a project document that selects that type. The example at `examples/policy/` is written as very
   nearly that arrangement.
2. `knowledge-lint` is built as a flake check: frontmatter vetted against a CUE `#Node` schema, and
   reference integrity — `[[wiki-links]]`, `blocked_by` ids, and relative links all resolve.
   `prose-format` already exists in this repo and is copied or shared.
3. A read-only deriver: given the instance tip, a project tree, and two commits, compose the policy,
   list the changed paths, match them against the composed classes, and print the required check
   names. It reports; it blocks nothing.
4. The two checks are run against the trees the deriver named.

### What observing it succeed looks like

The standard is verified by looking, not by reviewing configuration. Reading the policy files and
agreeing that they say the right thing is not the exercise.

- A commit that actually landed in the-valley touching a knowledge node is fed to the deriver, and
  it prints exactly `knowledge-lint` and `prose-format` — the union of two overlapping classes, one
  contributed by each layer, observed rather than asserted.
- A commit touching only `README.md` prints exactly `prose-format`. The knowledge lint's absence is
  the observation; a policy that over-requires is as wrong as one that under-requires, and only the
  second failure is loud.
- The project document is edited to decline `prose-format`, and the deriver refuses to compose the
  policy at all, naming the field. The floor is observed to hold against the project rather than
  argued to.
- A node's frontmatter is deliberately broken on a scratch branch. `knowledge-lint` fails against
  that tree, and the failure names the file and the field.
- A file no class covers — a shell script in a markdown-only repository — makes the deriver print
  the `unclassified` set. The hole announces itself instead of being found later.

### What cosmo additionally requires

Naming these keeps the qinling exercise from being mistaken for cosmo readiness.

- A `format` class over `**/*.nix` requiring `nixfmt --check`, and a build class requiring the
  five-machine build matrix. The schema expresses both today; nothing else about them is ready.
- An answer to check latency. Five machines' worth of `nix build` is not something a contributor's
  machine finishes quickly, and neither layer's owning the policy changes that. It is a real open
  question this node does not close.
- The floor actually read from the instance repository's tip by something, rather than from whatever
  tree happens to be at hand. Provenance is the whole of the forcing, so an implementation that gets
  it wrong has no floor at all.
- An enforcement point — Phase 3's integrator, or a deliberate interim. Until then this machinery
  observes and reports, and **cosmo must not migrate onto an observation**. That is the difference
  between qinling exercising the feature and cosmo depending on it.

## Related

- The design this implements: [[ida-1ec03b1]]
  ([ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))
- The group-to-instance binding the instance layer rests on: [[ida-8482624]]
  ([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md))
- The direction the project layer continues, and that the floor trims: [[ida-3e87f5c]]
  ([ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md))
- The store the policy travels in: [[dcr-5da1f36]]
  ([dcr-5da1f36-project-is-repo.md](./dcr-5da1f36-project-is-repo.md))
- Why the host schema stays free of options a host cannot act on: [[dcr-0f5d9b1]]
  ([dcr-0f5d9b1-cue-config-host-module.md](./dcr-0f5d9b1-cue-config-host-module.md))
