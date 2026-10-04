---
type: bug
id: bd-75c8721
status: closed
title: A push could write any namespace the hook did not name, replacement refs included
created: 2026-10-04
source: adversarial review of the push boundary, 2026-10-04
---

# A push could write any namespace the hook did not name

The pre-receive hook guarded two things: protected refs, and the create-only attestation namespace.
Every other ref was open to any key that could push. Two of those open namespaces were holes.

The first was `refs/replace/*`. A replacement ref makes git read one object in place of another,
wherever git looks the first one up. git honours replacement refs by default. The identity compiler
read the registry from the instance repository's tip with plain `git ls-tree` and `git show`, and
the integrator read the floor, the project's policy and a change's tree the same way. So a pushed
`refs/replace/<blob>` could swap in a registry that authorized any key as anything, or a floor that
required nothing, without moving a ref anyone watches. Every reader would have agreed on the forged
content, because every reader asks git the same question.

The second was the integration-request namespace. [[dcr-e544f20]]
([decisions/dcr-e544f20-access-is-verbs-on-projects.md](../decisions/dcr-e544f20-access-is-verbs-on-projects.md))
defines a request verb whose gate is a check of namespace writes against the principals holding it.
No such check existed, so any key that could push could ask for anything to land. Who may file
requests is the human gate on a valley whose floor demands approval statements, so the verb had to
ship with its gate.

## The resolution

The hook's rules are one Go program, [valleyhook/](../../valleyhook/). It runs on every project a
host serves, protected or not, and the hook a host installs only hands it the pushing principal, the
project's declared push policy, the grants, and the keys attestations are checked against. A write
must pass every rule that applies to its ref, and no rule stands in for another:

- Replacement refs, notes, and the valley's namespace other than attestations and integration
  requests take no push, whatever a declaration says.
- A ref that is symbolic in the repository takes no push, because git would write the ref it points
  at.
- Topic branches are open.
- An attestation ref may only be created. Its name must be exactly `<tree digest>/<key hash>`. It
  must point at a tree of notes, every note about that tree and signed under that key hash. Where
  the host names the keys a controller accepts, the signature must verify under one of them.
- An integration request takes the request grant, whoever else the project's protection names.
- Every other ref, tags included, takes a named grant of the project, which names the principals it
  opens a pattern to.
- A protected ref also takes a declared writer.

The request grant is a boundary kind in the identity schema, `"request"`. The compiler writes each
holder into a grants file the hook reads. A host can also grant it by hand, and the two add up. The
hand grant matters the first time: the hook refuses the request that would land the registry change
granting request, unless the host already grants it to whoever files that change.

Replacement refs are also turned off wherever git is read for a decision: in the identity compiler,
in `attest` (so a standalone `attest verify` too), in the integrator and every program it runs, and
in the `valley` CLI. Each one also drops the inherited `GIT_*` variables that could point git at
other objects. A replacement ref already on a host is therefore inert, and valley-init reports any
it finds on every activation without deleting it.

Three things around the hook could still have bypassed it, and each is refused. A pre-receive hook
the module did not write, or a `core.hooksPath` from any scope, would run instead of the policy, so
valley-init fails the activation over either; a project that wants a hook of its own composes it
after the policy, where it can only refuse more. The instance repository must declare protection of
the ref the floor and the registry are read from. And sshd is held to the one way a principal
arrives: the tag on the key's own entry. The module refuses an sshd that accepts the principal
variable from the client, sets it itself, or reads more of the user's environment than that one
variable. It also refuses a key declared under two tags. The git user's keys come only from the
declared and compiled files, and valley-init refuses a `~/.ssh/environment` in its home.

## What remains

The hook does not check that an attestation's signer is the principal pushing it. The signer and the
pusher are routinely different principals. A host's own key signs the claims `attest run` makes on
it, and the operator's key pushes them. On a host that names no verifier keys, the hook checks what
a note claims about its signer and not the signature, so a name there can still be taken by a note
claiming a signer it was not signed by.

The request namespace does not record who filed a request. Any holder of the grant can therefore
replace or withdraw any request, not only its own. The grant is the boundary: who holds request is
decided in the registry, and the registry is governed.

## Rolling it out

The order matters, because the hook refuses the very request that lands a registry change.

1. Check that every key the host declares by hand that acts for a principal is tagged with it. An
   untagged entry for a key the registry also holds wins in sshd and pushes as nobody, and the
   compiler now refuses that registry rather than compile a grant that would never apply.
2. Declare `services.valley.grants.request` for whoever files the registry change, and protect the
   instance repository's main. Then deploy.
3. Resolve whatever valley-init refuses: a hand-written pre-receive hook, a `core.hooksPath`, a
   `~/.ssh/environment` in the git user's home. Read the replacement refs it reports before deleting
   them.
4. Before the integrator runs again, audit the integration-request queue. The requests already there
   were filed before anything checked who filed them, and a request records no filer. Where a
   request's origin is in doubt, withdraw it and have it filed again by a principal holding the
   grant.
5. Land the registry change granting request, and check that the compiled grants file names the
   holders. Then check that a push from a holder's own key is accepted, with the tag the compiled
   file gives it.
6. Only then remove the hand grant.

## Related

- [[dcr-e544f20]]
  ([decisions/dcr-e544f20-access-is-verbs-on-projects.md](../decisions/dcr-e544f20-access-is-verbs-on-projects.md))
  — the request verb, whose gate this ships.
- [[dcr-b87f6e8]]
  ([decisions/dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry the grant compiles from, and the boundary rule it follows.
