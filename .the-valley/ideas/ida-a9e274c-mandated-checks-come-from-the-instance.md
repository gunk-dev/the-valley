---
type: idea
id: ida-a9e274c
status: exploring
title: A check a policy mandates is supplied by the instance, not by the project it checks
created: 2026-08-02
source: design conversation, 2026-08-02
---

# A check a policy mandates comes from the instance

When a verification policy requires a check by name, that name must resolve to a derivation the
instance supplies. The project being checked contributes the tree, and nothing else. A flake input
is how the derivation arrives: the instance's repository is an input of the project's flake, and the
project instantiates the check over its own source.

The alternative is that every project writes the check itself and the policy trusts the name. Then
the policy says `knowledge-lint` and each project decides what those characters mean — which makes
the floor a naming convention rather than a floor. A group with ten projects would have ten lints
drifting apart, and the one that drifted would be the one that stopped failing.

Supplying the derivation carries the toolchain with it. The instance pins the versions the check
runs against, so two projects in a group get the same answer for the same tree, and a project cannot
weaken a check by pinning an older tool. This is why the exposed entry point takes a system and a
tree rather than a package set: a consuming project needs no nixpkgs of its own for a check it did
not write.

What the project keeps is the decision to be checked at all, which is exactly what the policy layers
already govern ([[dcr-f41f718]]): the instance floor states what a project cannot take back, and the
project document selects the rest. This idea is about where the check's code comes from once the
policy has named it, and it applies to every check a floor can mandate, not only the knowledge lint
([[ida-1ec03b1]]).

## Consequences

- The instance repository becomes a dependency of every project it governs, and its availability
  becomes a build-time concern. A project that cannot fetch the instance flake cannot run the checks
  the instance mandates.
- Upgrading a check is a change in one repository, and it reaches projects when they update the
  input. Which means a project can sit on an old input and run an old check — the input revision a
  project pins is itself something a policy may eventually have to say something about.
- A project may still write checks of its own. Nothing here narrows what a project can add; it
  narrows where the mandated ones come from.

## Related

- [[dcr-f41f718]]
  ([decisions/dcr-f41f718-declared-verification-policy.md](../decisions/dcr-f41f718-declared-verification-policy.md))
  — the two policy layers this sits under.
- [[ida-1ec03b1]]
  ([ideas/ida-1ec03b1-path-scoped-verification-policy.md](./ida-1ec03b1-path-scoped-verification-policy.md))
  — path-scoped policy, and the knowledge lint's three lifetimes.
- [[ida-b9f646c]]
  ([ideas/ida-b9f646c-nix-backend-not-substrate.md](./ida-b9f646c-nix-backend-not-substrate.md)) — a
  flake input is one backend for this, not the substrate; a check named by policy must be resolvable
  without Nix eventually.
