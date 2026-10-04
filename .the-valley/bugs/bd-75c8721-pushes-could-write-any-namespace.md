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

The hook's rules are one Go program, [valleyhook/](../../valleyhook/), and the hook a host installs
only hands it the pushing principal, the project's declared protection, and the grants. It holds
every push to an allowlist. Protected refs take a push only from a declared writer. Attestation refs
are create-only. Integration requests take a write only from a principal holding the request grant.
Topic branches are open. Every other namespace is refused, and replacement refs are refused to every
push, declared writers included. A project opens any other namespace — release tags are the expected
case — with an `allow` entry in its protection block, naming the principals it opens to.

The request grant is a boundary kind in the identity schema, `"request"`. The compiler writes each
holder into a grants file the hook reads. A host can also grant it by hand, which matters the first
time: the hook refuses the request that would land the registry change granting request, unless the
host already grants it to whoever files that change.

Replacement refs are also turned off wherever git is read for a decision: in the identity compiler,
in the integrator and every program it runs, and in the `valley` CLI. Each one also drops the
inherited `GIT_*` variables that could point git at other objects. A replacement ref already on a
host is therefore inert, and valley-init reports any it finds on every activation without deleting
it.

## What remains

A project without a protection block gets no hook, so its refs stay open to any push, replacement
refs included. The readers that make decisions no longer honour them there either. That openness is
what declaring no protection means.

The hook does not check that an attestation ref's key hash belongs to the principal pushing it. The
signer and the pusher are routinely different principals. A host's own key signs the claims
`attest run` makes on it, and the operator's key pushes them.

The request namespace does not record who filed a request. Any holder of the grant can therefore
replace or withdraw any request, not only its own. The grant is the boundary: who holds request is
decided in the registry, and the registry is governed.

## Related

- [[dcr-e544f20]]
  ([decisions/dcr-e544f20-access-is-verbs-on-projects.md](../decisions/dcr-e544f20-access-is-verbs-on-projects.md))
  — the request verb, whose gate this ships.
- [[dcr-b87f6e8]]
  ([decisions/dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry the grant compiles from, and the boundary rule it follows.
