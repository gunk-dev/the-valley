---
type: decision
id: dcr-f41f718
status: proposed
title: Verification policy is a declared document — where it lives is the fork to resolve
created: 2026-07-31
source: cosmo-readiness design pass, 2026-07-31
---

# Declared verification policy

Verification policy — the mapping from path classes to required checks — becomes a declared CUE
document. This node proposes that, and states the one fork it does not resolve: whether the document
lives in the host declaration, in the project's own repository, or split across both. A concrete
draft schema accompanies the proposal ([schema/verification.cue](../../schema/verification.cue) and
the sample at [examples/verification-policy.cue](../../examples/verification-policy.cue)) so the
shape can be read rather than imagined.

The design this gives a home to is already written down. [[ida-1ec03b1]]
([ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))
establishes that required checks are derived from the actual tree diff and never from a
contributor's claim, that `.the-valley/**` takes signature plus knowledge lint while code takes the
full suite and a mixed diff takes the max, and — the sentence this node acts on — that the policy
itself is data, so it is a CUE document versioned in-repo. What is missing is the schema and the
answer to _in which repo_.

## Why this is needed now

cosmo gates every change through GitHub Actions. Its `.github/workflows/ci.yml` runs `nixfmt --check`
and a `nix build` matrix over classic-laddie, makers-nix and johnny-walker, and branch protection
makes that gate the condition of merging. S1 direct-push mode has no pull request object, so
migrating cosmo to a valley host does not weaken that gate — it deletes it. The repository that
builds five machines would land changes with nothing checking them, and nothing on the valley host
runs a check today. Declared verification policy is the piece that has to exist before cosmo can be
hosted here. The wider readiness picture is tracked separately as the cosmo-readiness outcome node,
in flight on branch `oc/cosmo-readiness`.

## The fork: where the policy document lives

### Option A — in the host declaration

A `verification` field on `#Project` in [schema/valley.cue](../../schema/valley.cue), carrying the
path classes and required checks. The host knows what each project it serves must pass.

Changing policy means changing the host declaration, which means a host deploy. No contributor to a
project can change that project's checks; only whoever owns the host's declaration can. A fresh clone
knows nothing on its own — clone the project and there is no way to say what its changes must pass
without asking a host. A project served by two hosts has two independent declarations, free to
diverge silently, and neither host is wrong. An enforcement point on the host has the policy in hand
before a push arrives, so a `pre-receive` hook could read it with no access to the pushed tree at
all, and the integrator could read it the same way.

The cost is the one that matters: policy is not versioned with the code it gates. A change that adds
a check and the code needing that check cannot land together — they land in two repositories, in an
order nothing enforces.

### Option B — in the project's own repository

A versioned CUE document in the project's tree. This is the plainer reading of [[ida-1ec03b1]]'s
"versioned in-repo", and it is the direction [[ida-3e87f5c]]
([ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md))
already adopted: a project's declaration travels in its store, and host config shrinks toward a
serving list.

Anyone who can land a change in the project can change its policy, and that change goes through the
policy in force before it. A fresh clone knows everything: the policy document plus the repository's
own checks say exactly what a change must pass, offline, with no host contacted. A project served by
two hosts means the same thing on both, by construction, because both read the same file out of the
same tree. Checks travel with the code that needs them, so adding a check and the code it covers is
one commit.

The enforcement question is sharper here and is worth stating plainly. At push time an in-repo policy
lives in the tree being pushed, which is to say it is supplied by the change being gated; a
`pre-receive` hook reading it would let a change weaken its own gate. The integrator does not have
that problem, because it already holds both trees and can read the policy at the target ref's tip —
the policy that is already integrated. Weakening then takes two landings: one that changes the
policy under the old policy, and then the change that wanted the weakening. That is the right shape,
and it is another reason integration-time enforcement beats push-time enforcement, consistent with
the one structural invariant in [contribute.md](../../design/contribute.md).

What Option B alone cannot do is let a host insist. A project whose policy document goes empty — or
never had one — is served exactly as before, and nothing notices. For cosmo that is the whole
problem restated: a gate a project can remove by editing its own repository is weaker than the branch
protection it replaces.

### Option C — split

The project declares which checks apply to which paths. The host declares that verification is
required for that project, and nothing about its content.

This gives each side the half it can actually hold. The project owns the content of its checks, so
they travel with the code, a fresh clone is self-sufficient, and two hosts agree. The host owns
whether the gate exists at all, so a project cannot disable its own gate by landing a commit, and a
host can refuse to serve a project whose policy has gone missing. The host-side field is readable
before any tree is fetched, which is what makes that refusal cheap.

The risk is drift in the other direction: a host-side field that grows from "verified" into "verified
against these checks" is Option A wearing Option C's clothes. The field has to stay one declaration
of requirement, not a second copy of the policy.

## Recommendation

Option C, with essentially all of the weight in the project.

The project-side document is the substance, and it is what the draft schema ships. The host-side half
is one optional block — enabled or not, plus the source it reads policy from — sketched in the draft
as `#ProjectVerification` and deliberately **not** applied to `schema/valley.cue`. It is not applied
because no enforcement point reads it yet, and a host option the host cannot act on misrepresents the
boundary; the same reasoning kept per-project access out of both CUE and Nix in [[dcr-0f5d9b1]]
([dcr-0f5d9b1-cue-config-host-module.md](./dcr-0f5d9b1-cue-config-host-module.md)).

Two consequences follow from the recommendation and should be read as part of it. Enforcement belongs
to the integrator, not to a `pre-receive` hook, so declared policy is inert data until Phase 3
([roadmap.md](../../design/roadmap.md)); Phase 2 is where a contributor can first produce the
evidence a policy asks for. And the checks a policy names are ordinary flake checks in the reference
implementation, so a project that already has checks has most of a policy — the document says which
ones apply where, not what a check is.

## What the draft schema commits to

The draft is small on purpose, and three choices in it are load-bearing.

Class matching is a set operation, never first-match. A change's required checks are the union of
`requires` over every class that at least one changed path matches. Classes may overlap deliberately
— a knowledge node is both knowledge and prose — and the union is the entirety of [[ida-1ec03b1]]'s
"mixed takes the max" rule. There is no separate mixed case, and no ordering to get wrong.

A path matching no class is answered explicitly, by a required `unclassified` field. The failure this
document must never have is a path that quietly requires nothing, so the policy has to state what an
uncovered path costs rather than defaulting to silence.

The signature is deliberately absent. [[ida-1ec03b1]] lists it alongside the knowledge lint because
it is enumerating what a knowledge-only diff must satisfy, but the signature is required of every
change by the structural invariant and the integrator, whatever paths the diff touches. It is not
path-scoped, so it is not policy.

The draft's own rejections are pinned by running `cue vet`, not by inspection: an unknown check name,
a stray top-level field, a host-side concern written into a policy, an absolute or `..` path pattern,
an unimplemented runner, an unknown field inside a class, and a malformed name all fail vet with an
error naming the field. Those rejections are not yet wired into a flake check, because the draft is
deliberately unwired.

## The staged path: qinling first, cosmo second

Every feature built for cosmo readiness is exercised live on the gunk-dev instance, on a project
already hosted there, before cosmo depends on it. The projects on classic-laddie today are the-valley
and qinling, and both are markdown-only. So the first exercisable class is knowledge and prose — the
formatter-idempotence check this repo already ships as a flake check, plus the frontmatter and
reference-integrity linting [[ida-1ec03b1]] describes — and not the `nix build` class cosmo
eventually needs.

### The smallest thing that could be turned on

Four pieces, none of which is an enforcement point:

1. qinling gains a policy document — the sample at
   [examples/verification-policy.cue](../../examples/verification-policy.cue) is written as exactly
   that policy: a `knowledge` class over `.the-valley/**` requiring a knowledge lint, a `prose` class
   over `**/*.md` requiring the prose format check, and both required for a path outside either.
2. `knowledge-lint` is built as a flake check in qinling: frontmatter vetted against a CUE `#Node`
   schema, and reference integrity — `[[wiki-links]]`, `blocked_by` ids, and relative links all
   resolve. `prose-format` already exists in this repo and is copied or shared.
3. A read-only deriver: given two commits, list the changed paths, match them against the policy, and
   print the required check names. It reports; it blocks nothing.
4. The two checks are run against the trees the deriver named.

### What observing it succeed looks like

The standard is verified by looking, not by reviewing configuration. Reading the policy file and
agreeing that it says the right thing is not the exercise.

- A commit that actually landed in qinling touching a knowledge node is fed to the deriver, and it
  prints exactly `knowledge-lint` and `prose-format` — the union of two overlapping classes, observed
  rather than asserted.
- A commit touching only `README.md` prints exactly `prose-format`. The knowledge lint's absence is
  the observation; a policy that over-requires is as wrong as one that under-requires, and only the
  second failure is loud.
- A node's frontmatter is deliberately broken on a scratch branch. `knowledge-lint` fails against
  that tree, and the failure names the file and the field.
- A file no class covers — a shell script in a markdown-only repository — makes the deriver print the
  `unclassified` set. The hole announces itself instead of being found later.

### What cosmo additionally requires

Naming these keeps the qinling exercise from being mistaken for cosmo readiness.

- A `format` class over `**/*.nix` requiring `nixfmt --check`, and a build class requiring the
  five-machine build matrix. The schema expresses both today; nothing else about them is ready.
- An answer to check latency. Five machines' worth of `nix build` is not something a contributor's
  machine finishes quickly, and neither placement of the policy document changes that. It is a real
  open question this node does not close.
- The host-side half, actually implemented. cosmo is precisely the case where a project must not be
  able to remove its own gate, so the field sketched in the draft has to exist and be read by
  something.
- An enforcement point — Phase 3's integrator, or a deliberate interim. Until then this machinery
  observes and reports, and **cosmo must not migrate onto an observation**. That is the difference
  between qinling exercising the feature and cosmo depending on it.

## Status

Proposed. The fork above is resolved at hand-review, not by this branch. The draft schema is a draft:
it is not referenced by `schema/valley.cue`, not covered by the flake's `cue-vet` check, and not read
by [nix/valley-host.nix](../../nix/valley-host.nix).

## Related

- The design this implements: [[ida-1ec03b1]]
  ([ida-1ec03b1-path-scoped-verification-policy.md](../ideas/ida-1ec03b1-path-scoped-verification-policy.md))
- The direction that argues for the project-side placement: [[ida-3e87f5c]]
  ([ida-3e87f5c-self-describing-projects.md](../ideas/ida-3e87f5c-self-describing-projects.md))
- The store the policy travels in: [[dcr-5da1f36]]
  ([dcr-5da1f36-project-is-repo.md](./dcr-5da1f36-project-is-repo.md))
- Why the host schema stays free of options the host cannot act on: [[dcr-0f5d9b1]]
  ([dcr-0f5d9b1-cue-config-host-module.md](./dcr-0f5d9b1-cue-config-host-module.md))
