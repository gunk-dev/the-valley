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
  must point at a tree of notes, read one object at a time within fixed bounds on depth, entries,
  and bytes, each checked before anything past it is read. Every note must open under the keys a
  controller here accepts, the way a verifier opens it, be about that tree, and carry a verified
  signature under that key hash. A host that names no such keys accepts no attestation.
- An integration request takes the request grant, whoever else the project's protection names.
- Every other ref, tags included, takes a named grant of the project, which names the principals it
  opens a pattern to.
- A protected ref also takes a declared writer.

The request grant is a boundary kind in the identity schema, `"request"`. The compiler writes each
holder into a grants file the hook reads. A host can also grant it by hand, and the two add up. The
hand grant matters the first time: the hook refuses the request that would land the registry change
granting request, unless the host already grants it to whoever files that change.

The note envelope has one reading, the note module ([note/](../../note/)), which `attest` signs and
verifies with and the hook checks pushed notes with. A relayer can change a note's signature block
and nothing else, and every line it could add that one reader refuses, every reader refuses. So a
note the hook lets into the create-only namespace is a note every verifier opens.

Replacement refs are also turned off wherever git is read for a decision: in the identity compiler,
in `attest` (so a standalone `attest verify` too), in the integrator and every program it runs, and
in the `valley` CLI. Each one also drops the inherited `GIT_*` variables that could point git at
other objects. A replacement ref already on a host is therefore inert, and valley-init reports any
it finds on every activation without deleting it.

Things around the hook could still have bypassed it, and each is closed.

A pre-receive hook the module did not write, or a `core.hooksPath` set at any scope, to an empty
value included, would run instead of the policy. valley-init fails the activation over either, and a
project that wants a hook of its own composes it after the policy, where it can only refuse more.

A failed activation leaves no write path open. The git user's login shell is the valley's
(`valleyhook shell`), and it refuses every push until valley-init has recorded that it converged on
the configuration the shell was rendered with. valley-init removes that record before anything else
it does and writes it last. The operator can also hold pushes with a file valley-init never touches.
Fetches stay open.

The principal arrives one way. The shell reads the key sshd authenticated, finds that key's entry in
the files sshd authorized it from, and takes the tag on it. A principal or any `GIT_*` variable the
session arrived with is dropped, so a misconfigured sshd that passes client environment through
still names nobody and sets nothing git reads. The module also refuses such an sshd outright: an
`AcceptEnv` or `SetEnv` that could admit the principal or a `GIT_*` variable, in any Match block,
with the keyword in any case, and any `PermitUserEnvironment` but `no`. It refuses a key declared
under two tags, and the git user's keys come only from the declared and compiled files.

The instance repository must declare protection of the ref the floor and the registry are read from.

## What remains

The hook does not check that an attestation's signer is the principal pushing it. The signer and the
pusher are routinely different principals. A host's own key signs the claims `attest run` makes on
it, and the operator's key pushes them.

The request namespace does not record who filed a request. Any holder of the grant can therefore
replace or withdraw any request, not only its own. The grant is the boundary: who holds request is
decided in the registry, and the registry is governed.

## Rolling it out

The order matters, because the hook refuses the very request that lands a registry change, and
because requests already queued were filed before anything checked who filed them.

1. Tag every key the host declares by hand that acts for a principal. An untagged entry for a key
   the registry also holds is read first and pushes as nobody, and the compiler refuses that
   registry rather than compile a grant that would never apply.
2. Declare `services.valley.grants.request` for whoever files the registry change, and protect the
   instance repository's main.
3. Before deploying, stop the integrators and confirm they are inactive. Hold pushes with the
   operator's hold file. The new shell honours it from the moment the host switches, through
   valley-init's convergence, until the audits below are done.
4. Deploy. Resolve whatever valley-init refuses: a hand-written pre-receive hook, a
   `core.hooksPath`. Read the replacement refs it reports before deleting them.
5. Audit the integration-request queue. A request records no filer. Where a request's origin is in
   doubt, withdraw it, to be filed again by a principal holding the grant.
6. Release the hold, then start the integrators again.
7. Land the registry change granting request. Keep the hand grant until the compiled grants file
   names the holders and a request filed from a holder's own key is accepted.
8. Remove the hand grant, deploy, and file a request from the holder's key once more.

A protected project that moves onto the valley afterwards gets its first `main` by a seed on the
host ([integration.md](../../design/integration.md), _A protected stream starts from a seed_).

## Related

- [[dcr-e544f20]]
  ([decisions/dcr-e544f20-access-is-verbs-on-projects.md](../decisions/dcr-e544f20-access-is-verbs-on-projects.md))
  — the request verb, whose gate this ships.
- [[dcr-b87f6e8]]
  ([decisions/dcr-b87f6e8-identity-is-a-governed-registry.md](../decisions/dcr-b87f6e8-identity-is-a-governed-registry.md))
  — the registry the grant compiles from, and the boundary rule it follows.
