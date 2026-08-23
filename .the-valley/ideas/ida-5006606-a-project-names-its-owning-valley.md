---
type: idea
id: ida-5006606
status: adopted
title: A project names its owning valley in its own tree
created: 2026-08-22
source: review of the asker's policy defaults, 2026-08-22
---

# A project names its owning valley

A project names its owning valley in one line in its own tree, at `policy/valley`, holding that
valley's repository as `git fetch` takes it. Anything that derives the project's policy reads that
line, fetches the repository, and takes the floor from `policy/instance` at its integrated tip. A
valley's own config repository carries `policy/instance` and uses its own, so it needs no pointer.

The floor is the owning valley's, and it lives in that valley's repository ([[dcr-f41f718]]
([decisions/dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md)),
[[dcr-9b5da04]]
([decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md))).
The integrator learns which repository that is from the host it runs on, because it runs on the
host. Nothing gave that answer to a checkout on a contributor's machine — and a checkout is exactly
where `valley checks` reports what a change owes and where review's `[a]sk` derives the same thing
before filing a request. The pointer is what supplies it, and fetch is all it asks for: anyone the
valley governs holds fetch on the valley.

## The rule this exists to keep

**A gate's default sources are the ones that govern the project. Every other layer is reachable only
by naming it.** A tool with no way to find the floor will be given one anyway, and the tempting
substitute is whatever policy directory the tree already contains. That is how the asker came to
derive a change's required checks from the worked example under `examples/policy/`: the example
composes, so the derivation succeeded and reported a check no one had written, named after a
demonstration. A derivation that succeeds against the wrong layers is worse than one that refuses,
because nothing about the output says which layers it read. So each of the two layers now defaults
to a real source and refuses loudly when it is not there, and the options that point elsewhere stay
— reading the worked example is a thing a person asks for by name.

## The client derives from exactly the sources the integrator uses

Every source the derivation reads is a source the integrator reads. The project's own layer comes
from the target's tip. The pointer naming the owning valley comes from the target's tip. The floor
comes from that valley's integrated tip. None of them comes from the working tree.

The working tree is the wrong source entirely, and not only for the reason a branch is: a change
never supplies the policy that gates it, and neither does whoever happens to be reviewing it. A
reviewer's checkout is whatever branch they were last on. Reading policy from it makes the answer a
function of that, which is how `[a]sk` came to read `policy/project` from a checkout that predated
the layer landing and refuse a change nothing was wrong with. The rule also buys something positive:
review works identically from any branch, however stale, so there is nothing to synchronize before
reviewing.

Absence has to agree too, layer by layer, because a client that refuses where the integrator
composes is as wrong as one that composes where the integrator refuses. A target carrying no project
layer is derived under the floor alone; a floor that is not there is refused.

The schema is not one of these sources. It is the shape a policy is written in rather than a layer
of policy, it ships with the tool, and the integrator takes its own from its deployment for the same
reason.

## The pointer is never the gate

A change can edit `policy/valley`, because it is a file in the tree like any other. That costs
nothing, because the pointer is not what gates anything. The integrator composes the policy from its
own configuration and from the target ref's tip, so a branch that repoints its own floor is judged
under the floor it tried to leave — the same shape as every other part of the policy a change cannot
supply for itself. What the pointer buys is that the operator's local reading agrees with the
integrator's, which is the whole reason the local reading is printed.

## Open

- Nothing caches the fetch. Every derivation reaches the valley's repository again, which is right
  for a verb a person runs and wrong for anything run per commit.
- The pointer names a repository and takes the ref as given (`refs/heads/main`). A valley whose
  integrated tip is some other ref has no way to say so yet.
