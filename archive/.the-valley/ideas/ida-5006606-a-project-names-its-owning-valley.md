---
type: idea
id: ida-5006606
status: adopted
title: A project names its owning valley in its own tree
created: 2026-08-22
source: review of the asker's policy defaults, 2026-08-22
---

# A project names its owning valley

Authority points downward: valley membership is law, so the valley declares its projects in its own
repository, the same way it grants everything else. A project cannot claim its own governor. Until
that declaration has an implementation — it arrives with the multi-valley work — the host a project
runs on carries the declaration, because the host is where the project already runs under one
valley's law.

A project also carries a pointer, `policy/valley`, one line in its own tree naming that valley's
repository as `git fetch` takes it. The pointer is discovery, not authority: a claim a client
follows and then checks against the valley's declaration once that declaration exists. A mismatch is
a loud error. The valley's declaration is the truth; the pointer is only how a client finds it
before it can ask the valley directly.

Anything that derives the project's policy follows the pointer, fetches the named repository, and
takes the floor from `policy/instance` at that repository's integrated tip ([[dcr-f41f718]]
([decisions/dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md)),
[[dcr-9b5da04]]
([decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md](../decisions/dcr-9b5da04-hosts-serve-isolated-valleys.md))).
A valley's own config repository carries `policy/instance` and uses its own, so it needs no pointer.
Fetch is all the pointer asks for: anyone the valley governs already holds fetch on the valley.

## The rule this exists to keep

**A gate's default sources are the ones that govern the project. Every other layer is reachable only
by naming it.** A tool with no way to find the floor will be given one anyway, and the tempting
substitute is whatever policy directory the tree already contains. Each layer defaults to a real
source and refuses loudly when that source is absent; the options that point elsewhere stay, for a
person to name explicitly.

## The client derives from exactly the sources the integrator uses

Every source the derivation reads is a source the integrator reads. The project's own layer comes
from the target's tip. The pointer naming the owning valley comes from the target's tip. The floor
comes from that valley's integrated tip. None of them comes from the working tree, because a change
never supplies the policy that gates it, and neither does whoever happens to be reviewing it. Review
works identically from any branch, however stale, so there is nothing to synchronize before
reviewing.

Absence has to agree too, layer by layer: a target carrying no project layer is derived under the
floor alone; a floor that is not there is refused.

## The pointer is never the gate

A change can edit `policy/valley`, because it is a file in the tree like any other file, and that
costs nothing: the pointer is not what gates anything. The integrator composes the policy from its
own configuration and from the target ref's tip, so a branch that repoints its own floor is judged
under the floor it tried to leave — the same shape as every other part of the policy a change cannot
supply for itself.

## The pointer is transitional

Once addressing carries the valley — the ssh user a client connects as already selects the valley —
discovery collapses into the clone URL itself, and the breadcrumb retires. Until then, the pointer
is what lets a checkout on a contributor's machine find its floor at all: nothing else gives that
answer to `valley checks` or to review's `[a]sk`, both of which need it before either can report
what a change owes.

## Open

- Nothing caches the fetch. Every derivation reaches the valley's repository again, which is right
  for a verb a person runs and wrong for anything run per commit.
- The pointer names a repository and takes the ref as given (`refs/heads/main`). A valley whose
  integrated tip is some other ref has no way to say so yet.
