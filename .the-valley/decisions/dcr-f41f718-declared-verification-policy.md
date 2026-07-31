---
type: decision
id: dcr-f41f718
status: proposed
title: Verification policy is a declared document, composed from an instance floor and a project layer
created: 2026-07-31
source: cosmo-readiness design pass, 2026-07-31
---

# Declared verification policy

Verification policy — the mapping from path classes to required checks — becomes a declared CUE
document. It is composed from two documents, not one. The group's instance repository carries a
mandatory floor and a set of project-type templates; the project's own repository carries everything
above the floor. The effective policy is the unification of the two. A draft schema accompanies the
proposal ([schema/verification.cue](../../schema/verification.cue)), with a worked two-document
example at [examples/verification-instance.cue](../../examples/verification-instance.cue) and
[examples/verification-project.cue](../../examples/verification-project.cue), so the shape can be
read rather than imagined.

The design this gives a home to is already written down. [[ida-1ec03b1]]
([ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))
establishes that required checks are derived from the actual tree diff and never from a
contributor's claim, that `.the-valley/**` takes signature plus knowledge lint while code takes the
full suite and a mixed diff takes the max, and — the sentence this node acts on — that the policy
itself is data, so it is a CUE document versioned in-repo. What is missing is the schema and the
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

The **instance layer** is the group's policy. A group has exactly one instance — that binding is
settled in [[ida-8482624]]
([ida-8482624-federation-groups.md](../ideas/ida-8482624-federation-groups.md)) and in
[architecture.md § federation](../../design/architecture.md#federation-the-group-is-the-unit) — so
the instance repository is the group's repository, and for the gunk-dev instance that is qinling. It
holds two things: a mandatory floor every project in the group clears, and project-type templates a
project selects and then narrows.

The **project layer** is the project's own policy, a versioned document in its own tree. It states
everything above the floor: the classes the project cares about, the checks it adds, and the
template defaults it declines.

Policy has no business at a lower layer than the instance. Group to instance is 1-to-1, but instance
to host deliberately is not: an instance is instantiable on a single machine and equally runnable as
a distributed system, with git hosting sharding across hosts and nothing in the architecture
assuming co-location. classic-laddie being the whole of the gunk-dev instance today is a fact about
deployment, not about the design. A policy field on a host would therefore be a policy field on _one
of several_ possible carriers of the same instance, free to diverge from its siblings with neither
one wrong — the same defect that rules out putting a project's policy in a place the project does
not control at all.

## The fork: which layer owns what

### Option A — the instance layer owns all of it

Every project's classes and required checks are declared in the instance repository, and a project's
own tree says nothing.

Changing a project's policy is a change to a different repository. No contributor to a project can
change that project's checks; only whoever can land in the instance repository can. A fresh clone
knows nothing on its own — clone the project and there is no way to say what its changes must pass
without asking the instance.

The cost is the one that matters: policy is not versioned with the code it gates. A change that adds
a check and the code needing that check cannot land together — they land in two repositories, in an
order nothing enforces.

### Option B — the project layer owns all of it

A versioned CUE document in the project's tree, and nothing anywhere else. This is the plainer
reading of [[ida-1ec03b1]]'s "versioned in-repo", and it is the direction [[ida-3e87f5c]]
([ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md))
already adopted: a project's declaration travels in its store, and instance config shrinks toward a
serving list.

Anyone who can land a change in the project can change its policy, and that change goes through the
policy in force before it. A fresh clone knows everything: the policy document plus the repository's
own checks say exactly what a change must pass, offline, with nothing else contacted. Checks travel
with the code that needs them, so adding a check and the code it covers is one commit.

The enforcement question is sharper here and is worth stating plainly. At push time an in-repo
policy lives in the tree being pushed, which is to say it is supplied by the change being gated; a
`pre-receive` hook reading it would let a change weaken its own gate. The integrator does not have
that problem, because it already holds both trees and can read the policy at the target ref's tip —
the policy that is already integrated. Weakening then takes two landings: one that changes the
policy under the old policy, and then the change that wanted the weakening. That is the right shape,
and it is another reason integration-time enforcement beats push-time enforcement, consistent with
the one structural invariant in [contribute.md](../../design/contribute.md).

What Option B alone cannot do is let the group insist. A project whose policy document goes empty —
or never had one — is served exactly as before, and nothing notices. For cosmo that is the whole
problem restated: a gate a project can remove by editing its own repository is weaker than the
branch protection it replaces.

### Option C — split, by force rather than by subject

Both layers are the same schema and the same kind of document. The instance layer states the
minimum: the checks no project in the group may decline, and the templates that give a project of a
known type a sensible starting set. The project layer states everything else. They compose by
unification, which can only narrow, so the floor is a floor by construction: the project layer can
add to it and cannot subtract from it.

The split is not "the instance says whether, the project says what". Both layers say what; they
differ in whether a project can answer back. That is what keeps the two halves from being two
vocabularies to learn, and what makes the composition a single `cue vet`.

The risk is the floor growing. A floor that keeps absorbing checks until it describes every class of
every project is Option A wearing Option C's clothes, and the discipline is to keep the floor the
minimum a group is willing to enforce on projects it has not read.

## Recommendation

Option C, with the weight in the project and a deliberately small floor.

Four things follow from it and should be read as part of the recommendation.

### Mandatory versus overridable is one bit per check, in CUE's own semantics

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

### The schema shape follows from that, and it is not the shape the first draft had

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

### Forcing comes from the floor's provenance, not from CUE

Unification on its own forces nothing. A project that vendored a copy of the floor into its own tree
and edited the copy would unify perfectly well; the copy is simply not the floor. What makes a floor
a floor is where it is read from: the instance repository's integrated tip, never the project's
tree.

That is Option B's "weakening takes two landings" argument one level up. Changing the floor is a
change landing in the instance repository, under that repository's own policy and its own
integrator, and it is visible in that repository's history — which is what
[self-transparency.md](../../design/self-transparency.md) asks of the system's own configuration.
The schema cannot enforce this, because a schema cannot see which file a field came from. The tool
that composes a policy is what makes it true, and the schema says so in its comments.

### The two halves resolve at different times

The mandatory floor resolves at head. A floor a project could pin is not a floor: pinning it would
make every past floor permanently available as an alternative to the current one, and weakening
would take one landing again — the project's.

Templates may be pinned. They are defaults, a project can already override them, and a stale default
costs nothing the project could not have written by hand.

## What the host-side half was for, and why it is gone

The first draft sketched a field on the host declaration — `enable`, plus the source policy is read
from — whose entire job was to stop a project deleting its own gate. That field is deleted, not
moved. It is not needed: a project belongs to a group whether or not it declares a policy, so an
absent or empty project policy still composes to the floor exactly. The hole the field was covering
is closed by the floor, and closed better, because the floor also says what the project must pass
rather than only that it must pass something. [schema/valley.cue](../../schema/valley.cue) is
untouched by this proposal.

## What the draft schema commits to

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

The claims above are pinned by running `cue vet`, not by inspection. The example in `examples/` is
two documents. The instance layer makes `prose-format` mandatory over `**/*.md` and for uncovered
paths, and offers a `docs` project type whose knowledge lint is a default. The project layer selects
that type, adds a `shellcheck` class of its own, and declines the defaulted knowledge lint.

Both directions were run.

```
$ cue vet -c schema/verification.cue \
    examples/verification-instance.cue examples/verification-project.cue
$ echo $?
0
```

The composed `policy` exports with `knowledge-lint` at `false` — the project's legitimate override —
alongside the floor's `prose-format` at `true` and the project's own `shellcheck` class. A project
that instead tries to unset the mandatory check fails, and the error names the field:

```
$ cue vet -c schema/verification.cue examples/verification-instance.cue unset.cue
policy.classes.prose.requires."prose-format": conflicting values false and true:
    ./examples/verification-instance.cue:29:29
    ...
    ./unset.cue:3:52
```

Narrowing a floor class's coverage fails the same way, at `policy.classes.prose.paths."**/*.md"`.
Selecting a project type the instance does not define is an evaluation error rather than a silent
fallback. An unknown check name, a stray top-level field, an absolute or `..` path pattern, an
unimplemented runner, an unknown field inside a class, and a malformed name all fail vet with an
error naming the field. A project document that declares nothing at all vets, and composes to the
floor exactly.

This example is the proposal's own evidence and nothing more. It is not the live exercise on
qinling, which is a separate step described below. Those rejections are also not yet wired into a
flake check, because the draft is deliberately unwired.

## What this costs

A project is now self-describing only above the floor, which trims [[ida-3e87f5c]]. A clone of the
project alone can no longer state its required set; it can state what the project adds, and must
consult the instance repository for the rest. That is a deliberate trade for the group being able to
force compliance, which is the whole reason the floor exists. The timing split preserves most of the
offline story: templates may be pinned, so the only thing a clone genuinely cannot answer alone is
the current floor, and the floor is small by design.

Enforcement belongs to the integrator, not to a `pre-receive` hook, so declared policy is inert data
until Phase 3 ([roadmap.md](../../design/roadmap.md)); Phase 2 is where a contributor can first
produce the evidence a policy asks for. And the checks a policy names are ordinary flake checks in
the reference implementation, so a project that already has checks has most of a policy — the
document says which ones apply where, not what a check is.

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
   a project document that selects that type. The example in `examples/` is written as very nearly
   those two files.
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

## Status

Proposed. The direction is agreed; the decision is recorded by hand-review and integration, not by
this branch. The draft schema is a draft: it is not referenced by
[schema/valley.cue](../../schema/valley.cue), not covered by the flake's `cue-vet` check, and not
read by [nix/valley-host.nix](../../nix/valley-host.nix).

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
